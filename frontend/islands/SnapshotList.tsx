import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";

interface SnapshotInfo {
  name: string;
  namespace: string;
  volumeSnapshotClassName?: string;
  sourcePVC?: string;
  readyToUse: boolean;
  restoreSize?: string;
  createdAt: string;
}

interface SnapshotResponse {
  data: SnapshotInfo[];
  metadata: { total: number; available: boolean };
}

export default function SnapshotList() {
  const snapshots = useSignal<SnapshotInfo[]>([]);
  const available = useSignal(true);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    loading.value = true;
    apiGet<SnapshotResponse>("/v1/storage/snapshots")
      .then((resp) => {
        if (resp.data && Array.isArray(resp.data.data)) {
          snapshots.value = resp.data.data;
          available.value = resp.data.metadata?.available ?? true;
        }
      })
      .catch((err) => {
        error.value = err instanceof Error
          ? err.message
          : "Failed to load snapshots";
      })
      .finally(() => {
        loading.value = false;
      });
  }, []);

  if (!IS_BROWSER) {
    return <div class="p-6">Loading snapshots...</div>;
  }

  if (loading.value) {
    return (
      <div class="animate-pulse space-y-4">
        <div class="h-48 bg-slate-200 dark:bg-slate-700 rounded" />
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-red-700 dark:text-red-300">
        {error.value}
      </div>
    );
  }

  if (!available.value) {
    return (
      <Card>
        <div class="p-6 text-center text-slate-500 dark:text-slate-400">
          <p class="text-lg font-medium">VolumeSnapshot CRDs Not Installed</p>
          <p class="mt-2 text-sm">
            Install the snapshot.storage.k8s.io CRDs to enable VolumeSnapshot
            support.
          </p>
        </div>
      </Card>
    );
  }

  if (snapshots.value.length === 0) {
    return (
      <Card>
        <div class="p-6 text-center text-slate-500 dark:text-slate-400">
          <p class="text-lg font-medium">No Volume Snapshots</p>
          <p class="mt-2 text-sm">
            No VolumeSnapshot resources found in the cluster.
          </p>
        </div>
      </Card>
    );
  }

  return (
    <Card title={`Volume Snapshots (${snapshots.value.length})`}>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Name
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Namespace
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Source PVC
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Class
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Size
              </th>
              <th class="text-left py-2 px-3 font-medium text-slate-500 dark:text-slate-400">
                Ready
              </th>
            </tr>
          </thead>
          <tbody>
            {snapshots.value.map((snap) => (
              <tr
                key={`${snap.namespace}/${snap.name}`}
                class="border-b border-slate-100 dark:border-slate-800"
              >
                <td class="py-2 px-3 font-mono text-xs text-slate-700 dark:text-slate-300">
                  {snap.name}
                </td>
                <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                  {snap.namespace}
                </td>
                <td class="py-2 px-3 font-mono text-xs text-slate-600 dark:text-slate-400">
                  {snap.sourcePVC || "N/A"}
                </td>
                <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                  {snap.volumeSnapshotClassName || "N/A"}
                </td>
                <td class="py-2 px-3 text-xs text-slate-600 dark:text-slate-400">
                  {snap.restoreSize || "N/A"}
                </td>
                <td class="py-2 px-3">
                  <StatusBadge
                    status={snap.readyToUse ? "Ready" : "Pending"}
                    variant={snap.readyToUse ? "success" : "warning"}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
