/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Card } from "@/components/ui/Card.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { apiDelete, apiGet } from "@/lib/api.ts";
import ScheduledSnapshotWizard from "@/src/islands/ScheduledSnapshotWizard.tsx";
import SnapshotWizard from "@/src/islands/SnapshotWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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

export default function SnapshotList() {
  const isClient = useIsClient();
  const [snapshots, setSnapshots] = useState<SnapshotInfo[]>([]);
  const [available, setAvailable] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<SnapshotInfo | null>(null);
  const [deleting, setDeleting, deletingRef] = useStateRef(false);
  const [snapshotWizardOpen, setSnapshotWizardOpen] = useState(false);
  const [scheduleWizardOpen, setScheduleWizardOpen] = useState(false);

  const fetchSnapshots = () => {
    setLoading(true);
    apiGet<SnapshotInfo[]>("/v1/storage/snapshots")
      .then((resp) => {
        // resp = {data: [...], metadata: {available: bool, total: num}}
        // resp.data is the snapshot array, resp.metadata has the available flag
        setSnapshots(Array.isArray(resp.data) ? resp.data : []);
        const meta = (resp as unknown as Record<string, unknown>).metadata as
          | { available?: boolean }
          | undefined;
        setAvailable(meta?.available ?? true);
      })
      .catch((err) => {
        setError(
          err instanceof Error ? err.message : "Failed to load snapshots",
        );
      })
      .finally(() => {
        setLoading(false);
      });
  };

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchSnapshots` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchSnapshots();
  }, []);

  const handleDelete = async (snap: SnapshotInfo) => {
    if (deletingRef.current) return;
    setDeleting(true);
    const prev = snapshots;
    setSnapshots(
      snapshots.filter(
        (s) => !(s.name === snap.name && s.namespace === snap.namespace),
      ),
    );
    try {
      await apiDelete(`/v1/storage/snapshots/${snap.namespace}/${snap.name}`);
      showToast(`Deleted snapshot"${snap.name}"`, "success");
      setDeleteTarget(null);
    } catch (err) {
      setSnapshots(prev);
      showToast(err instanceof Error ? err.message : "Delete failed", "error");
    } finally {
      setDeleting(false);
    }
  };

  if (!isClient) {
    return <div className="p-6">Loading snapshots...</div>;
  }

  if (loading) {
    return (
      <div className="animate-pulse space-y-4">
        <div className="h-48 bg-elevated rounded" />
      </div>
    );
  }

  if (error) {
    return <ErrorBanner message={error} />;
  }

  if (!available) {
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
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
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
              strokeWidth="1.5"
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
    <div className="space-y-4">
      {/* Action buttons */}
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => setSnapshotWizardOpen(true)}
          className="inline-flex items-center rounded-md bg-brand px-4 py-2 text-sm font-medium hover:bg-brand/90"
          style={{
            color: "var(--bg-base)",
            fontFamily: "inherit",
            border: "none",
            cursor: "pointer",
          }}
        >
          Create Snapshot
        </button>
        <button
          type="button"
          onClick={() => setScheduleWizardOpen(true)}
          className="inline-flex items-center rounded-md border border-border-primary px-4 py-2 text-sm font-medium text-text-secondary hover:bg-hover"
          style={{
            fontFamily: "inherit",
            cursor: "pointer",
            background: "transparent",
          }}
        >
          Schedule Snapshots
        </button>
      </div>

      {snapshotWizardOpen && (
        <SnapshotWizard onClose={() => setSnapshotWizardOpen(false)} />
      )}
      {scheduleWizardOpen && (
        <ScheduledSnapshotWizard onClose={() => setScheduleWizardOpen(false)} />
      )}

      {snapshots.length === 0 ? (
        <Card>
          <div className="p-6 text-center text-text-muted">
            <p className="text-lg font-medium">No Volume Snapshots</p>
            <p className="mt-2 text-sm">
              No VolumeSnapshot resources found in the cluster.
            </p>
          </div>
        </Card>
      ) : (
        <Card title={`Volume Snapshots (${snapshots.length})`}>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border-primary">
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Name
                  </th>
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Namespace
                  </th>
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Source PVC
                  </th>
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Class
                  </th>
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Size
                  </th>
                  <th className="text-left py-2 px-3 font-medium text-text-muted">
                    Status
                  </th>
                  <th className="text-right py-2 px-3 font-medium text-text-muted">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody>
                {snapshots.map((snap) => (
                  <tr
                    key={`${snap.namespace}/${snap.name}`}
                    className="border-b border-border-subtle"
                  >
                    <td className="py-2 px-3 font-mono text-xs text-text-secondary">
                      {snap.name}
                    </td>
                    <td className="py-2 px-3 text-xs text-text-secondary">
                      {snap.namespace}
                    </td>
                    <td className="py-2 px-3 font-mono text-xs text-text-secondary">
                      {snap.sourcePVC || "N/A"}
                    </td>
                    <td className="py-2 px-3 text-xs text-text-secondary">
                      {snap.volumeSnapshotClassName || "N/A"}
                    </td>
                    <td className="py-2 px-3 text-xs text-text-secondary">
                      {snap.restoreSize || "N/A"}
                    </td>
                    <td className="py-2 px-3">
                      {snap.errorMessage ? (
                        <StatusBadge label="Error" tone="crit" />
                      ) : (
                        <StatusBadge
                          label={snap.readyToUse ? "Ready" : "Pending"}
                          tone={snap.readyToUse ? "ok" : "warn"}
                        />
                      )}
                      {snap.errorMessage && (
                        <p className="mt-1 text-xs text-error max-w-xs truncate">
                          {snap.errorMessage}
                        </p>
                      )}
                    </td>
                    <td className="py-2 px-3 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <a
                          href={
                            snap.readyToUse
                              ? `/storage/snapshots/restore?ns=${snap.namespace}&name=${snap.name}&restoreSize=${
                                  snap.restoreSize || ""
                                }&snapshotClass=${
                                  snap.volumeSnapshotClassName || ""
                                }`
                              : undefined
                          }
                          className={`rounded px-2 py-1 text-xs font-medium ${
                            snap.readyToUse
                              ? "text-brand hover:bg-brand/10"
                              : "text-text-muted cursor-not-allowed"
                          }`}
                          title={
                            snap.readyToUse
                              ? "Restore to new PVC"
                              : "Snapshot not ready"
                          }
                        >
                          Restore
                        </a>
                        <button
                          type="button"
                          onClick={() => setDeleteTarget(snap)}
                          className="rounded px-2 py-1 text-xs font-medium hover:bg-danger-dim text-danger"
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

      {deleteTarget && (
        <ConfirmDialog
          title={`Delete ${deleteTarget.name}`}
          message={`This will permanently delete the snapshot"${deleteTarget.name}" in namespace"${deleteTarget.namespace}".`}
          confirmLabel="Delete"
          danger
          typeToConfirm={deleteTarget.name}
          loading={deleting}
          onConfirm={() => {
            if (deleteTarget) handleDelete(deleteTarget);
          }}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  );
}
