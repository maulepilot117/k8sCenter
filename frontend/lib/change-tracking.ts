/**
 * Hooks behind the YAML Apply page's change-tracking surface (Release E U31):
 * whether this server can keep a change record, the `?repairOf=` link a
 * receipt opens the page with, and the GitOps ownership of the previewed
 * objects. Each owns its request lifecycle (abort on change and unmount) so
 * the island only renders their state.
 *
 * Client-only, like `yaml-apply.ts`: effects never run during SSR.
 */

import { useCallback, useEffect, useState } from "react";
import { useLatest } from "@/src/lib/use-latest.ts";
import { listReceipts, resolveOwnership } from "./change-api.ts";
import {
  assembleOwnership,
  isOperationId,
  ownershipFailureText,
  ownershipPlanFromPreview,
  type TrackingAvailability,
  trackingAvailabilityFromError,
} from "./change-copy.ts";
import type { OwnershipResult } from "./change-types.ts";
import type { ValidateResponse, YamlApplyPin } from "./yaml-apply.ts";

/** POST /v1/changes/ownership accepts at most this many objects per request. */
export const MAX_OWNERSHIP_REFS = 50;

/**
 * Probes `GET /v1/changes` once. Reachable turns tracking on through
 * `setTracked` (the default for a server that can keep the record); a 404 (no
 * such route) or 503 (no database) turns it off, and the returned
 * availability carries the reason to show. The caller's tracked flag starts
 * off, so nothing is recorded by surprise while the probe is in flight.
 */
export function useTrackingAvailability(
  setTracked: (on: boolean) => void,
): TrackingAvailability {
  const [availability, setAvailability] = useState<TrackingAvailability>({
    status: "checking",
  });
  // The probe answers after an await: call the setter from the latest render.
  const setTrackedRef = useLatest(setTracked);
  useEffect(() => {
    const controller = new AbortController();
    listReceipts({ pageSize: 1 }, controller.signal)
      .then(() => {
        setAvailability({ status: "available" });
        setTrackedRef.current(true);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        setAvailability(trackingAvailabilityFromError(err));
        setTrackedRef.current(false);
      });
    return () => controller.abort();
  }, []);
  return availability;
}

/** The `?repairOf=` the page was opened with, if any. */
export type RepairState =
  | { status: "none" }
  /** `clusterId` is the original receipt's cluster, when the link names it. */
  | { status: "valid"; id: string; clusterId?: string }
  | { status: "invalid" };

/**
 * Reads `?repairOf=<operation id>` once and hands a valid id to `setRepairOf`
 * for the apply hook. It is only ever a link: nothing is loaded from the
 * receipt, which stores no content. A malformed id is reported and never
 * sent. `stopRepair` drops the link and removes it from the URL; it keeps its
 * identity for the component's lifetime.
 */
export function useRepairLink(setRepairOf: (id: string | null) => void): {
  repair: RepairState;
  stopRepair: () => void;
} {
  const [repair, setRepair] = useState<RepairState>({ status: "none" });
  const setRepairOfRef = useLatest(setRepairOf);
  useEffect(() => {
    const params = new URLSearchParams(globalThis.location.search);
    const raw = params.get("repairOf");
    if (raw === null) return;
    if (isOperationId(raw)) {
      const id = raw.toLowerCase();
      const clusterId = params.get("cluster") || undefined;
      setRepair(
        clusterId
          ? { status: "valid", id, clusterId }
          : { status: "valid", id },
      );
      setRepairOfRef.current(id);
    } else {
      setRepair({ status: "invalid" });
    }
  }, []);
  const stopRepair = useCallback(() => {
    setRepairOfRef.current(null);
    setRepair({ status: "none" });
    const url = new URL(globalThis.location.href);
    url.searchParams.delete("repairOf");
    url.searchParams.delete("cluster");
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
 *
 * `yamlContent` is the text the preview ran over. It is read when a preview
 * lands, not watched: an edit clears the preview, which is what re-runs this.
 */
export function useOwnershipPreview(
  preview: ValidateResponse | null,
  pin: YamlApplyPin | null,
  yamlContent: string,
): OwnershipState {
  const [ownership, setOwnership] = useState<OwnershipState>({
    status: "idle",
  });
  const yamlRef = useLatest(yamlContent);
  const requestCluster = pin?.target.clusterId;
  const serverCluster = pin?.targetCluster;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new preview or a new request cluster is what starts a lookup; serverCluster arrives with the same pin as requestCluster, and the YAML is read through a ref on purpose (see above).
  useEffect(() => {
    if (!preview || !requestCluster) {
      setOwnership({ status: "idle" });
      return;
    }
    const plan = ownershipPlanFromPreview(preview.documents, yamlRef.current);
    const rowCluster = serverCluster ?? requestCluster;
    if (plan.slots.length === 0) {
      setOwnership({ status: "idle" });
      return;
    }
    const refs = plan.refs.slice(0, MAX_OWNERSHIP_REFS);
    if (refs.length === 0) {
      // Nothing resolvable: every row is "not checked", without a request.
      setOwnership({
        status: "ready",
        ...assembleOwnership(plan, [], MAX_OWNERSHIP_REFS, rowCluster),
      });
      return;
    }
    const controller = new AbortController();
    setOwnership({ status: "loading" });
    resolveOwnership(requestCluster, refs, controller.signal)
      .then((res) => {
        if (controller.signal.aborted) return;
        if (serverCluster && res.clusterId && res.clusterId !== serverCluster) {
          setOwnership({
            status: "error",
            message:
              "Ownership was resolved against a different cluster than the preview, so it is not shown. Validate again.",
          });
          return;
        }
        setOwnership({
          status: "ready",
          ...assembleOwnership(
            plan,
            res.results ?? [],
            MAX_OWNERSHIP_REFS,
            rowCluster,
          ),
        });
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        setOwnership({ status: "error", message: ownershipFailureText(err) });
      });
    return () => controller.abort();
  }, [preview, requestCluster]);
  return ownership;
}

/** How many resolved objects a GitOps controller is confirmed (or contested) to manage. */
export function managedCount(state: OwnershipState): number {
  if (state.status !== "ready") return 0;
  return state.results.filter(
    (r) => r.confidence === "confirmed" || r.confidence === "conflicting",
  ).length;
}
