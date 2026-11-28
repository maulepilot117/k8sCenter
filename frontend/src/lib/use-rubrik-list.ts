/**
 * The selected cluster's Rubrik namespace list, as the backup pages read it.
 *
 * Each Rubrik status answer (one per cluster switch) decides what to ask:
 * nothing while the provider is off, not set up or unavailable, and
 * GET /v1/rubrik/namespaces once the cluster is mapped. The answer is folded
 * into a `RubrikInput`, so every page shows the same states the same way:
 * a 409 is "not set up", a 403 "not applicable", an RSC failure class is
 * "RSC failing" with the time of the last answer this page saw.
 *
 * `reload()` reads the list again; `reload({ quiet: true })` keeps what is
 * shown while it reads, for a refresh after a write settles.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, apiGet } from "@/lib/api.ts";
import type { RubrikInput } from "@/lib/protection-overview.ts";
import type { RubrikNamespaceList } from "@/lib/rubrik-types.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { ensureRubrikStatus, rubrikStatus } from "@/src/lib/rubrik-status.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

// Full literal: e2e/tests/api-routes.spec.ts checks every quoted "/v1/..."
// path is mounted.
const RUBRIK_NAMESPACES_URL = "/v1/rubrik/namespaces";

/** RSC's own failure classes: everything else is k8sCenter's. */
export const RSC_FAILURE: Record<string, string> = {
  auth_failed: "RSC refused k8sCenter's credentials",
  unreachable: "RSC could not be reached",
  rate_limited: "RSC is rate limiting requests",
  rsc_error: "RSC returned an answer k8sCenter could not use",
};

/** Setup reasons only admins are sent (non-admins get not_set_up). */
const ADMIN_SETUP = new Set([
  "not_configured",
  "not_mapped",
  "mapping_invalid",
  "rsc_cluster_missing",
]);

export function useRubrikList(): {
  list: RubrikInput;
  reload: (opts?: { quiet?: boolean }) => void;
} {
  const status = useSignalValue(rubrikStatus);
  /** Bumped by reload() to read the list again. */
  const [tick, setTick] = useState(0);
  /**
   * The fetch time of the last list this page loaded, per cluster: what an
   * RSC failure with no earlier answer reports as the last fetch.
   */
  const lastFetched = useRef(new Map<string, string>());
  const [list, setList] = useState<RubrikInput>({ kind: "loading" });
  const gen = useRef(0);
  /** The next read keeps what is shown. */
  const quiet = useRef(false);

  useEffect(() => {
    ensureRubrikStatus();
  }, []);

  useEffect(() => {
    const g = ++gen.current;
    if (status.kind !== "enabled") {
      setList(
        status.kind === "disabled"
          ? { kind: "disabled" }
          : status.kind === "error"
            ? { kind: "error" }
            : { kind: "loading" },
      );
      return;
    }
    const state = status.status.cluster.state;
    if (state !== "mapped") {
      setList(
        state === "admin_only"
          ? { kind: "not_applicable" }
          : state === "unavailable"
            ? { kind: "error" }
            : { kind: "not_set_up", setupLink: ADMIN_SETUP.has(state) },
      );
      return;
    }
    void tick;
    const keep = quiet.current;
    quiet.current = false;
    if (!keep) setList({ kind: "loading" });
    const forCluster = selectedCluster.peek();
    apiGet<RubrikNamespaceList>(RUBRIK_NAMESPACES_URL)
      .then((res) => {
        if (gen.current !== g) return;
        const fetchedAt = res.data.freshness?.fetchedAt;
        if (fetchedAt) lastFetched.current.set(forCluster, fetchedAt);
        setList({ kind: "loaded", list: res.data });
      })
      .catch((err) => {
        if (gen.current !== g) return;
        const reason = err instanceof ApiError ? err.reason : undefined;
        const code = err instanceof ApiError ? err.status : 0;
        if (code === 409) {
          setList({
            kind: "not_set_up",
            setupLink: ADMIN_SETUP.has(reason ?? ""),
          });
        } else if (code === 403) {
          setList({ kind: "not_applicable" });
        } else if (reason && RSC_FAILURE[reason]) {
          setList({
            kind: "rsc_failing",
            reason,
            // The last answer this page saw, else the connection's own
            // record (admins only, when the server keeps one).
            lastFetchedAt:
              lastFetched.current.get(forCluster) ??
              status.status.connection?.lastSuccessAt,
          });
        } else {
          setList({ kind: "error" });
        }
      });
  }, [status, tick]);

  const reload = useCallback((opts?: { quiet?: boolean }) => {
    quiet.current = !!opts?.quiet;
    setTick((t) => t + 1);
  }, []);

  return { list, reload };
}
