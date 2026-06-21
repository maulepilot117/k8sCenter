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
import { IS_BROWSER } from "fresh/runtime";
import { api } from "@/lib/api.ts";
import { selectedCluster } from "@/lib/cluster.ts";
import { selectedNamespace } from "@/lib/namespace.ts";

/** Raw count map from the backend batch endpoint. null = not yet loaded. */
export const resourceCounts = signal<Record<string, number> | null>(null);

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

let lastNs = "";
let lastCluster = "";
let abortController: AbortController | null = null;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleCountsFetch(ns: string, _cluster: string) {
  if (debounceTimer !== null) clearTimeout(debounceTimer);
  if (abortController) abortController.abort();

  debounceTimer = setTimeout(() => {
    abortController = new AbortController();
    resourceCountsLoading.value = true;

    const nsParam = ns && ns !== "all"
      ? `?namespace=${encodeURIComponent(ns)}`
      : "";

    api<Record<string, number>>(`/v1/resources/counts${nsParam}`, {
      method: "GET",
      signal: abortController.signal,
    })
      .then((res) => {
        resourceCounts.value = res.data ?? {};
      })
      .catch((err) => {
        if (err.name === "AbortError") return;
        // On error keep stale data; just stop showing loading state.
      })
      .finally(() => {
        resourceCountsLoading.value = false;
      });
  }, 150);
}

// Wire the reactive side-effect only in the browser.
if (IS_BROWSER) {
  effect(() => {
    const ns = selectedNamespace.value;
    const cluster = selectedCluster.value;
    if (ns === lastNs && cluster === lastCluster) return;
    lastNs = ns;
    lastCluster = cluster;
    scheduleCountsFetch(ns, cluster);
  });
}
