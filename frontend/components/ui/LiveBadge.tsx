import { wsStatus } from "@/lib/ws.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";

/**
 * The page-header badge saying whether a list updates by itself.
 *
 * The resource socket stays connected on every page, because the
 * notification bell holds it open, but it only carries the LOCAL cluster's
 * informer events. Remote clusters have no informers, and `useWsRefetch`
 * subscribes to nothing under a remote selection. A remote page therefore
 * never reads "Live" whatever the socket's state, and says the operator has
 * to refresh instead (R-8 U14).
 */
export function LiveBadge() {
  if (selectedCluster.value !== LOCAL_CLUSTER_ID) {
    return (
      <span
        class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium text-text-muted bg-bg-elevated"
        title="Live updates are off for a remote cluster. Use Refresh to see changes."
      >
        Refresh to update
      </span>
    );
  }
  if (wsStatus.value !== "connected") return null;
  return (
    <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
      <span class="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
      Live
    </span>
  );
}
