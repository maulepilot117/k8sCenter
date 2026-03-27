import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiDelete, apiGet } from "@/lib/api.ts";
import { Card } from "@/components/ui/Card.tsx";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";

interface SnapshotInfo {
  name: string;
  namespace: string;
  volumeSnapshotClassName?: string;
  sourcePVC?: string;
  readyToUse: boolean;
  restoreSize?: string;
  errorMessage?: string;
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
  const deleteTarget = useSignal<SnapshotInfo | null>(null);
  const deleting = useSignal(false);

  const fetchSnapshots = () => {
    loading.value = true;
    apiGet<SnapshotInfo[]>("/v1/storage/snapshots")
      .then((resp) => {
        // resp = {data: [...], metadata: {available: bool, total: num}}
        // resp.data is the snapshot array, resp.metadata has the available flag
        snapshots.value = Array.isArray(resp.data) ? resp.data : [];
        const meta = (resp as unknown as Record<string, unknown>)
          .metadata as { available?: boolean } | undefined;
        available.value = meta?.available ?? true;
      })
      .catch((err) => {
        error.value = err instanceof Error
          ? err.message
          : "Failed to load snapshots";
      })
      .finally(() => {
        loading.value = false;
      });
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSnapshots();
  }, []);

  const handleDelete = async (snap: SnapshotInfo) => {
    if (deleting.value) return;
    deleting.value = true;
    const prev = snapshots.value;
    snapshots.value = snapshots.value.filter((s) =>
      !(s.name === snap.name && s.namespace === snap.namespace)
    );
    try {
      await apiDelete(`/v1/storage/snapshots/${snap.namespace}/${snap.name}`);
      showToast(`Deleted snapshot"${snap.name}"`, "success");
      deleteTarget.value = null;
    } catch (err) {
      snapshots.value = prev;
      showToast(
        err instanceof Error ? err.message : "Delete failed",
        "error",
      );
    } finally {
      deleting.value = false;
    }
  };

  if (!IS_BROWSER) {
    return <div class="p-6">Loading snapshots...</div>;
  }

  if (loading.value) {
    return (
      <div class="animate-pulse space-y-4">
        <div class="h-48 bg-elevated rounded" />
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="bg-danger-dim border border-danger rounded-lg p-4 text-danger">
        {error.value}
      </div>
    );
  }

  if (!available.value) {
    return (
      <div
        style={{
          background: "var(--error-dim)",
          border: "1px solid var(--error)",
          borderRadius: "var(--radius)",
          padding: "20px 24px",
          display: "flex",
          alignItems: "flex-start",
          gap: "14px",
        }}
      >
        <svg
          width="22"
          height="22"
          viewBox="0 0 24 24"
          fill="none"
          stroke="var(--error)"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          style={{ flexShrink: 0, marginTop: "2px" }}
        >
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="8" x2="12" y2="12" />
          <line x1="12" y1="16" x2="12.01" y2="16" />
        </svg>
        <div>
          <div
            style={{
              fontSize: "15px",
              fontWeight: 600,
              color: "var(--error)",
              marginBottom: "6px",
            }}
          >
            VolumeSnapshot CRDs Not Installed
          </div>
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-secondary)",
              margin: "0 0 12px 0",
              lineHeight: 1.5,
            }}
          >
            The{" "}
            <code
              style={{
                fontFamily: "var(--font-mono)",
                fontSize: "12px",
                padding: "1px 5px",
                background: "var(--bg-elevated)",
                borderRadius: "3px",
              }}
            >
              snapshot.storage.k8s.io
            </code>{" "}
            CRDs are required for VolumeSnapshot support. These CRDs are not
            included in Kubernetes by default and must be installed separately.
          </p>
          <a
            href="https://kubernetes-csi.github.io/docs/snapshot-controller.html"
            target="_blank"
            rel="noopener noreferrer"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--accent)",
              textDecoration: "none",
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path d="M6 3H3v10h10v-3" />
              <path d="M9 2h5v5" />
              <path d="M14 2L7 9" />
            </svg>
            View installation documentation
          </a>
        </div>
      </div>
    );
  }

  return (
    <div class="space-y-4">
      {/* Action buttons */}
      <div class="flex gap-2">
        <a
          href="/storage/snapshots/new"
          class="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium text-white hover:bg-brand/90"
        >
          Create Snapshot
        </a>
        <a
          href="/storage/snapshots/schedule"
          class="inline-flex items-center rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
        >
          Schedule Snapshots
        </a>
      </div>

      {snapshots.value.length === 0
        ? (
          <Card>
            <div class="p-6 text-center text-text-muted">
              <p class="text-lg font-medium">No Volume Snapshots</p>
              <p class="mt-2 text-sm">
                No VolumeSnapshot resources found in the cluster.
              </p>
            </div>
          </Card>
        )
        : (
          <Card title={`Volume Snapshots (${snapshots.value.length})`}>
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary">
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Name
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Namespace
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Source PVC
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Class
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Size
                    </th>
                    <th class="text-left py-2 px-3 font-medium text-text-muted">
                      Status
                    </th>
                    <th class="text-right py-2 px-3 font-medium text-text-muted">
                      Actions
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {snapshots.value.map((snap) => (
                    <tr
                      key={`${snap.namespace}/${snap.name}`}
                      class="border-b border-border-subtle"
                    >
                      <td class="py-2 px-3 font-mono text-xs text-text-secondary">
                        {snap.name}
                      </td>
                      <td class="py-2 px-3 text-xs text-text-secondary">
                        {snap.namespace}
                      </td>
                      <td class="py-2 px-3 font-mono text-xs text-text-secondary">
                        {snap.sourcePVC || "N/A"}
                      </td>
                      <td class="py-2 px-3 text-xs text-text-secondary">
                        {snap.volumeSnapshotClassName || "N/A"}
                      </td>
                      <td class="py-2 px-3 text-xs text-text-secondary">
                        {snap.restoreSize || "N/A"}
                      </td>
                      <td class="py-2 px-3">
                        {snap.errorMessage
                          ? (
                            <StatusBadge
                              status="Error"
                              variant="danger"
                            />
                          )
                          : (
                            <StatusBadge
                              status={snap.readyToUse ? "Ready" : "Pending"}
                              variant={snap.readyToUse ? "success" : "warning"}
                            />
                          )}
                        {snap.errorMessage && (
                          <p class="mt-1 text-xs text-red-500 max-w-xs truncate">
                            {snap.errorMessage}
                          </p>
                        )}
                      </td>
                      <td class="py-2 px-3 text-right">
                        <div class="flex items-center justify-end gap-2">
                          <a
                            href={snap.readyToUse
                              ? `/storage/snapshots/restore?ns=${snap.namespace}&name=${snap.name}&restoreSize=${
                                snap.restoreSize || ""
                              }&snapshotClass=${
                                snap.volumeSnapshotClassName || ""
                              }`
                              : undefined}
                            class={`rounded px-2 py-1 text-xs font-medium ${
                              snap.readyToUse
                                ? "text-brand hover:bg-brand/10"
                                : "text-text-muted cursor-not-allowed"
                            }`}
                            title={snap.readyToUse
                              ? "Restore to new PVC"
                              : "Snapshot not ready"}
                          >
                            Restore
                          </a>
                          <button
                            type="button"
                            onClick={() => {
                              deleteTarget.value = snap;
                            }}
                            class="rounded px-2 py-1 text-xs font-medium text-red-600 hover:bg-danger-dim text-danger"
                          >
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        )}

      {deleteTarget.value && (
        <ConfirmDialog
          title={`Delete ${deleteTarget.value.name}`}
          message={`This will permanently delete the snapshot"${deleteTarget.value.name}" in namespace"${deleteTarget.value.namespace}".`}
          confirmLabel="Delete"
          danger
          typeToConfirm={deleteTarget.value.name}
          loading={deleting.value}
          onConfirm={() => {
            if (deleteTarget.value) handleDelete(deleteTarget.value);
          }}
          onCancel={() => {
            deleteTarget.value = null;
          }}
        />
      )}
    </div>
  );
}
