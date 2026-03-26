import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { selectedCluster } from "@/lib/cluster.ts";

interface ClusterInfo {
  id: string;
  name: string;
  displayName: string;
  status: string;
  k8sVersion: string;
  nodeCount: number;
  isLocal: boolean;
}

/**
 * Cluster selector dropdown for the TopBar.
 * Replaces the static"local" cluster indicator.
 */
export default function ClusterSelector() {
  const clusters = useSignal<ClusterInfo[]>([]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    apiGet<ClusterInfo[]>("/v1/clusters")
      .then((res) => {
        if (Array.isArray(res.data)) {
          clusters.value = res.data;
        }
      })
      .catch(() => {
        // Fall back to showing just the local indicator
      });
  }, []);

  // If no clusters loaded or only local, show simple indicator
  if (clusters.value.length <= 1) {
    const status = clusters.value[0]?.status ?? "connected";
    return (
      <div class="flex items-center gap-1.5 rounded-md bg-elevated px-2.5 py-1 text-xs text-text-secondary bg-elevated text-text-secondary">
        <span
          class={`h-2 w-2 rounded-full ${
            status === "connected" ? "bg-success" : "bg-danger"
          }`}
        />
        {selectedCluster.value}
      </div>
    );
  }

  return (
    <select
      value={selectedCluster.value}
      onChange={(e) => {
        const newCluster = (e.target as HTMLSelectElement).value;
        if (newCluster !== selectedCluster.value) {
          selectedCluster.value = newCluster;
          // Reload to get clean state for the new cluster
          globalThis.location.reload();
        }
      }}
      class="rounded-md border border-border-primary bg-surface px-2.5 py-1 text-xs text-text-secondary focus:border-brand focus:ring-1 focus:ring-brand text-text-secondary"
    >
      {clusters.value.map((c) => (
        <option key={c.id} value={c.id}>
          {c.status === "connected" ? "\u{1F7E2}" : "\u{1F534}"}
          {""}
          {c.displayName || c.name}
          {c.k8sVersion ? ` (${c.k8sVersion})` : ""}
        </option>
      ))}
    </select>
  );
}
