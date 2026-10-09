/**
 * Shared resource counts store.
 *
 * Holds a `Record<kindPlural, number>` signal populated by a single
 * GET /v1/resources/counts call. SecondaryNav and the list-page Dashboard
 * islands both consume this so the network request is deduped.
 *
 * Client-only — MUST NOT be imported in server-rendered components.
 * Module-level signals are process-global singletons in Deno; importing
 * server-side would leak state across SSR requests.
 */
import { computed, effect, signal } from "@preact/signals";
import { api } from "@/lib/api.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";

/** Raw count map from the backend batch endpoint. null = not yet loaded. */
export const resourceCounts = signal<Record<string, number> | null>(null);

/**
 * True when the last read reported `metadata.truncated`: a remote kind hit the
 * paging cap, so a count equal to the cap is a lower bound, not exact.
 */
export const resourceCountsTruncated = signal(false);

/**
 * Per-kind cap on a remote count. Mirrors
 * `k8s.RemoteListPageSize * k8s.RemoteListMaxPages` in the backend.
 */
export const REMOTE_COUNT_CAP = 5000;

/** True while a fetch is in flight. */
export const resourceCountsLoading = signal(false);

/** Derived: total items with counts across the current signal value. */
export const resourceCountsTotal = computed(() => {
  const c = resourceCounts.value;
  if (!c) return 0;
  return Object.values(c).reduce((sum, n) => sum + n, 0);
});

/**
 * Returns the count for `kind` (plural lowercase, e.g. "deployments"),
 * or null if the store hasn't loaded yet.
 */
export function getCount(kind: string): number | null {
  const c = resourceCounts.value;
  if (!c) return null;
  return c[kind] ?? 0;
}

/** The cluster `resourceCounts` was last read from, or null before any read. */
let countsCluster: string | null = null;

/**
 * The count for `kind` as display text: a capped count under a truncated read
 * gets a trailing `+`. Null before the store has loaded.
 */
export function formatCount(kind: string): string | null {
  const n = getCount(kind);
  if (n === null) return null;
  return resourceCountsTruncated.value && n >= REMOTE_COUNT_CAP
    ? `${n}+`
    : String(n);
}

/** Counts from another cluster are wrong data for this one: drop them now. */
function dropCountsFromOtherCluster(cluster: string) {
  if (countsCluster !== cluster) {
    resourceCounts.value = null;
    resourceCountsTruncated.value = false;
    countsCluster = null;
  }
}

/** Test-only: return the store to its initial state. */
export function resetCountsForTest() {
  resourceCounts.value = null;
  resourceCountsTruncated.value = false;
  countsCluster = null;
}

let lastNs = "";
let lastCluster = "";
let abortController: AbortController | null = null;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * One counts read for `ns` on `cluster`, applied to the store.
 *
 * Split out of the debounced scheduler so the outcome handling is reachable
 * without a browser: the scheduler only exists when `IS_BROWSER` was true at
 * import, which a test process cannot arrange once any other module has
 * already loaded the store.
 *
 * Pinned to `cluster`, so the request and the refusal check describe the same
 * target. Rejects only with an abort, which the caller ignores.
 */
export async function fetchCounts(
  ns: string,
  cluster: string,
  signal?: AbortSignal,
): Promise<void> {
  const nsParam =
    ns && ns !== "all" ? `?namespace=${encodeURIComponent(ns)}` : "";
  dropCountsFromOtherCluster(cluster);
  try {
    const res = await api<Record<string, number>>(
      `/v1/resources/counts${nsParam}`,
      { method: "GET", signal, clusterId: cluster },
    );
    resourceCounts.value = res.data ?? {};
    resourceCountsTruncated.value = res.metadata?.truncated === true;
    countsCluster = cluster;
  } catch (err) {
    if ((err as Error)?.name === "AbortError") throw err;
    // Counts from another cluster are not stale data for this one, they are
    // wrong data: a failed read after a cluster switch shows nothing rather
    // than the previous cluster's numbers. A failed read on the same cluster
    // keeps its last counts.
    dropCountsFromOtherCluster(cluster);
  }
}

function scheduleCountsFetch(ns: string, cluster: string) {
  if (debounceTimer !== null) clearTimeout(debounceTimer);
  if (abortController) abortController.abort();
  // Do not label the previous cluster's numbers as the new cluster's while the
  // debounce and the read are pending.
  dropCountsFromOtherCluster(cluster);

  debounceTimer = setTimeout(() => {
    abortController = new AbortController();
    resourceCountsLoading.value = true;
    fetchCounts(ns, cluster, abortController.signal)
      .catch(() => {
        // Aborted by a newer schedule; that fetch owns the state now.
      })
      .finally(() => {
        resourceCountsLoading.value = false;
      });
  }, 150);
}

/**
 * Disposer returned by the module-level effect subscription.
 * Held at module scope so teardownCounts() can stop it.
 */
let effectDisposer: (() => void) | null = null;

// Wire the reactive side-effect only in the browser.
if (IS_BROWSER) {
  effectDisposer = effect(() => {
    const ns = selectedNamespace.value;
    const cluster = selectedCluster.value;
    if (ns === lastNs && cluster === lastCluster) return;
    lastNs = ns;
    lastCluster = cluster;
    scheduleCountsFetch(ns, cluster);
  });
}

/**
 * Tears down the module-level counts subscription and aborts any in-flight
 * fetch. Safe to call multiple times.
 *
 * NOTE: SecondaryNav is NOT the sole consumer of resourceCounts (WorkloadsDashboard,
 * NetworkingDashboard, StorageDashboard, ConfigDashboard, PDBsDashboard, and
 * HPAsDashboard also subscribe). Do NOT wire this into SecondaryNav's cleanup
 * unless you are certain it is the last consumer — prefer a refcount approach
 * or leave teardown to app-level shutdown.
 */
export function teardownCounts(): void {
  if (debounceTimer !== null) {
    clearTimeout(debounceTimer);
    debounceTimer = null;
  }
  if (abortController) {
    abortController.abort();
    abortController = null;
  }
  if (effectDisposer) {
    effectDisposer();
    effectDisposer = null;
  }
}
