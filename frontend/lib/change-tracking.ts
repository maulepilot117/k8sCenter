/**
 * Hooks behind the YAML Apply page's change-tracking surface (Release E U31):
 * whether this server can keep a change record, the `?repairOf=` link a
 * receipt opens the page with, and the GitOps ownership of the previewed
 * objects. Each owns its request lifecycle (abort on change and unmount) so
 * the island only renders their state.
 *
 * Client-only, like `yaml-apply.ts`: effects never run during SSR.
 */

import { type ReadonlySignal, type Signal, useSignal } from "@preact/signals";
import { useCallback, useEffect } from "preact/hooks";
import { listReceipts, resolveOwnership } from "./change-api.ts";
import {
  isOperationId,
  ownershipFailureText,
  ownershipRefsFromPreview,
  type TrackingAvailability,
  trackingAvailabilityFromError,
} from "./change-copy.ts";
import type { OwnershipResult } from "./change-types.ts";
import type { ValidateResponse, YamlApplyPin } from "./yaml-apply.ts";

/** POST /v1/changes/ownership accepts at most this many objects per request. */
export const MAX_OWNERSHIP_REFS = 50;

/**
 * Probes `GET /v1/changes` once. Reachable turns `tracked` on (the default for
 * a server that can keep the record); a 404 (no such route) or 503 (no
 * database) leaves it off, and the returned availability carries the reason
 * to show. `tracked` starts off, so nothing is recorded by surprise while the
 * probe is in flight.
 */
export function useTrackingAvailability(
  tracked: Signal<boolean>,
): ReadonlySignal<TrackingAvailability> {
  const availability = useSignal<TrackingAvailability>({ status: "checking" });
  useEffect(() => {
    const controller = new AbortController();
    listReceipts({ pageSize: 1 }, controller.signal)
      .then(() => {
        availability.value = { status: "available" };
        tracked.value = true;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        availability.value = trackingAvailabilityFromError(err);
        tracked.value = false;
      });
    return () => controller.abort();
  }, []);
  return availability;
}

/** The `?repairOf=` the page was opened with, if any. */
export type RepairState =
  | { status: "none" }
  | { status: "valid"; id: string }
  | { status: "invalid" };

/**
 * Reads `?repairOf=<operation id>` once and mirrors a valid id into
 * `repairOf` for the apply hook. It is only ever a link: nothing is loaded
 * from the receipt, which stores no content. A malformed id is reported and
 * never sent. `stopRepair` drops the link and removes it from the URL.
 */
export function useRepairLink(repairOf: Signal<string | null>): {
  repair: ReadonlySignal<RepairState>;
  stopRepair: () => void;
} {
  const repair = useSignal<RepairState>({ status: "none" });
  useEffect(() => {
    const raw = new URLSearchParams(globalThis.location.search).get("repairOf");
    if (raw === null) return;
    if (isOperationId(raw)) {
      const id = raw.toLowerCase();
      repair.value = { status: "valid", id };
      repairOf.value = id;
    } else {
      repair.value = { status: "invalid" };
    }
  }, []);
  const stopRepair = useCallback(() => {
    repairOf.value = null;
    repair.value = { status: "none" };
    const url = new URL(globalThis.location.href);
    url.searchParams.delete("repairOf");
    globalThis.history.replaceState(null, "", url);
  }, []);
  return { repair, stopRepair };
}

/** GitOps ownership of the previewed objects, resolved after each preview. */
export type OwnershipState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "ready"; results: OwnershipResult[]; omitted: number }
  | { status: "error"; message: string };

/**
 * After every successful preview, asks who manages the previewed objects on
 * the cluster the apply is pinned to. A new preview, an edit (which clears
 * the preview) or unmount cancels the request in flight. A response that
 * names a different cluster than the preview is discarded rather than shown.
 */
export function useOwnershipPreview(
  preview: ReadonlySignal<ValidateResponse | null>,
  pin: ReadonlySignal<YamlApplyPin | null>,
  yamlContent: ReadonlySignal<string>,
): ReadonlySignal<OwnershipState> {
  const ownership = useSignal<OwnershipState>({ status: "idle" });
  const previewed = preview.value;
  const requestCluster = pin.value?.target.clusterId;
  const serverCluster = pin.value?.targetCluster;
  useEffect(() => {
    if (!previewed || !requestCluster) {
      ownership.value = { status: "idle" };
      return;
    }
    const allRefs = ownershipRefsFromPreview(
      previewed.documents,
      yamlContent.peek(),
    );
    const refs = allRefs.slice(0, MAX_OWNERSHIP_REFS);
    const omitted = allRefs.length - refs.length;
    if (refs.length === 0) {
      ownership.value = { status: "idle" };
      return;
    }
    const controller = new AbortController();
    ownership.value = { status: "loading" };
    resolveOwnership(requestCluster, refs, controller.signal)
      .then((res) => {
        if (controller.signal.aborted) return;
        if (serverCluster && res.clusterId && res.clusterId !== serverCluster) {
          ownership.value = {
            status: "error",
            message:
              "Ownership was resolved against a different cluster than the preview, so it is not shown. Validate again.",
          };
          return;
        }
        ownership.value = {
          status: "ready",
          results: res.results ?? [],
          omitted,
        };
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        ownership.value = {
          status: "error",
          message: ownershipFailureText(err),
        };
      });
    return () => controller.abort();
  }, [previewed, requestCluster]);
  return ownership;
}

/** How many resolved objects a GitOps controller is confirmed (or contested) to manage. */
export function managedCount(state: OwnershipState): number {
  if (state.status !== "ready") return 0;
  return state.results.filter(
    (r) => r.confidence === "confirmed" || r.confidence === "conflicting",
  ).length;
}
