/** @jsxImportSource react */
import { wsStatus } from "@/lib/ws.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

/**
 * Says, under a remote selection only, that the page does not update by
 * itself. Remote clusters have no informers and `useWsRefetch` subscribes to
 * none of their kinds, so the operator has to re-fetch (R-8 U14). Renders
 * nothing on the local cluster, where pages without a Live badge stay as
 * they were.
 *
 * `control` names the page's re-fetch button, for the pages where that
 * button is not called Refresh.
 */
export function RemoteRefreshHint({
  control = "Refresh",
}: {
  control?: string;
}) {
  const cluster = useSignalValue(selectedCluster);
  if (cluster === LOCAL_CLUSTER_ID) return null;
  return (
    <span
      className="inline-flex items-center self-center rounded-full px-2 py-0.5 text-xs font-medium text-text-muted bg-elevated"
      title={`Live updates are off for a remote cluster. Use ${control} to see changes.`}
    >
      {control} to update
    </span>
  );
}

/**
 * The page-header badge saying whether a list updates by itself.
 *
 * The resource socket stays connected on every page, because the
 * notification bell holds it open, but it only carries the LOCAL cluster's
 * informer events. A remote page therefore never reads "Live" whatever the
 * socket's state, and shows `RemoteRefreshHint` instead (R-8 U14).
 */
export function LiveBadge() {
  const cluster = useSignalValue(selectedCluster);
  const status = useSignalValue(wsStatus);
  if (cluster !== LOCAL_CLUSTER_ID) return <RemoteRefreshHint />;
  if (status !== "connected") return null;
  return (
    <span className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
      <span className="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
      Live
    </span>
  );
}
