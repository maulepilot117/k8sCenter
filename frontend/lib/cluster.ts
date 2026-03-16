import { signal } from "@preact/signals";

/**
 * Currently selected cluster ID.
 * All API calls include this as the X-Cluster-ID header.
 * Defaults to "local" (the cluster k8sCenter is deployed in).
 */
export const selectedCluster = signal("local");
