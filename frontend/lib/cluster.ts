import { effect, signal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

/**
 * Currently selected cluster ID.
 * All API calls include this as the X-Cluster-ID header.
 * Persisted to localStorage so it survives page reloads.
 * Defaults to "local" (the cluster k8sCenter is deployed in).
 */
const stored = IS_BROWSER
  ? localStorage.getItem("k8scenter.selectedCluster")
  : null;
export const selectedCluster = signal(stored ?? "local");

// Persist selection changes to localStorage
if (IS_BROWSER) {
  effect(() => {
    localStorage.setItem("k8scenter.selectedCluster", selectedCluster.value);
  });
}
