/** @jsxImportSource react */
import { clusterLabel, useClusterNames } from "@/src/lib/cluster-label.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

/**
 * Names the cluster a live session (a log tail, a shell) is connected to.
 * A session is pinned to the cluster it opened on and keeps it across
 * reconnects, so the badge takes that pinned id, never the current
 * selection.
 */
export function SessionClusterBadge({ clusterId }: { clusterId: string }) {
  const names = useClusterNames([clusterId]);
  const label = clusterLabel(clusterId, useSignalValue(names));
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-text-secondary bg-elevated"
      title={`Connected to ${label}`}
      data-testid="session-cluster-badge"
    >
      On {label}
    </span>
  );
}
