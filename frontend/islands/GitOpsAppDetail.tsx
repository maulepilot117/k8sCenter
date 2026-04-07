import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import {
  HEALTH_COLORS,
  HealthStatusBadge,
  SYNC_COLORS,
  SyncStatusBadge,
  ToolBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import type {
  AppDetail,
  ManagedResource,
  RevisionEntry,
} from "@/lib/gitops-types.ts";
import { resourceHref } from "@/lib/k8s-links.ts";

export default function GitOpsAppDetail({ id }: { id: string }) {
  const detail = useSignal<AppDetail | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);
  const actionInFlight = useSignal(false);

  // Confirmation dialog state
  const confirmAction = useSignal<
    {
      title: string;
      message: string;
      label: string;
      danger: boolean;
      onConfirm: () => void;
    } | null
  >(null);

  async function fetchData() {
    try {
      const res = await apiGet<AppDetail>(
        `/v1/gitops/applications/${encodeURIComponent(id)}`,
      );
      detail.value = res.data;
      error.value = null;
    } catch {
      error.value = "Failed to load application";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  // Determine the CRD kind from the composite ID
  const toolPrefix = id.split(":")[0];
  const kind = toolPrefix === "argo"
    ? "applications"
    : toolPrefix === "flux-hr"
    ? "helmreleases"
    : "kustomizations";

  useWsRefetch(fetchData, [
    [`gitops-detail-${id}`, kind, ""],
  ], 3000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  async function performAction(
    action: string,
    body?: unknown,
  ) {
    actionInFlight.value = true;
    try {
      const res = await apiPost<{ message: string }>(
        `/v1/gitops/applications/${encodeURIComponent(id)}/${action}`,
        body,
      );
      showToast(res.data.message, "success");
      await fetchData();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Action failed";
      showToast(msg, "error");
    } finally {
      actionInFlight.value = false;
    }
  }

  function requestConfirm(
    title: string,
    message: string,
    label: string,
    danger: boolean,
    onConfirm: () => void,
  ) {
    confirmAction.value = { title, message, label, danger, onConfirm };
  }

  function handleSync() {
    const app = detail.value?.app;
    if (!app) return;
    const verb = app.tool === "argocd" ? "Sync" : "Reconcile";
    requestConfirm(
      `${verb} ${app.name}?`,
      `This will trigger a ${verb.toLowerCase()} for ${app.name}.`,
      verb,
      false,
      () => {
        confirmAction.value = null;
        performAction("sync");
      },
    );
  }

  function handleSuspend(suspend: boolean) {
    const app = detail.value?.app;
    if (!app) return;
    if (suspend) {
      requestConfirm(
        `Suspend ${app.name}?`,
        "This will pause all reconciliation. Drift from git will not be corrected until resumed.",
        "Suspend",
        true,
        () => {
          confirmAction.value = null;
          performAction("suspend", { suspend: true });
        },
      );
    } else {
      performAction("suspend", { suspend: false });
    }
  }

  function handleRollback(revision: string, deployedAt: string) {
    const app = detail.value?.app;
    if (!app) return;
    const shortRev = revision.length > 7 ? revision.slice(0, 7) : revision;
    const dateStr = deployedAt
      ? new Date(deployedAt).toLocaleString()
      : "unknown date";
    requestConfirm(
      `Roll back ${app.name}?`,
      `Roll back to revision ${shortRev} deployed at ${dateStr}. This cannot be undone automatically.`,
      "Rollback",
      true,
      () => {
        confirmAction.value = null;
        performAction("rollback", { revision });
      },
    );
  }

  if (!IS_BROWSER) return null;

  /* Loading state */
  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  /* Error state */
  if (error.value || !detail.value) {
    return (
      <div class="p-6">
        <a
          href="/gitops/applications"
          class="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to Applications
        </a>
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted mb-4">
            {error.value ?? "Application not found"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const { app, resources, history } = detail.value;
  const isArgo = app.tool === "argocd";
  const isSyncing = app.syncStatus === "progressing";

  return (
    <div class="p-6">
      {/* Back link */}
      <a
        href="/gitops/applications"
        class="text-sm text-brand hover:underline mb-4 inline-block"
      >
        &larr; Back to Applications
      </a>

      {/* Header */}
      <div class="flex items-center justify-between mb-6">
        <div class="flex items-center gap-3 flex-wrap">
          <h1 class="text-2xl font-bold text-text-primary">{app.name}</h1>
          <ToolBadge tool={app.tool as string} />
          <SyncStatusBadge status={app.syncStatus} />
          <HealthStatusBadge status={app.healthStatus} />
          {app.suspended && (
            <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium text-text-muted bg-bg-elevated">
              Suspended
            </span>
          )}
        </div>

        {/* Action buttons */}
        <div class="flex items-center gap-2">
          <Button
            type="button"
            variant="primary"
            onClick={handleSync}
            disabled={actionInFlight.value || isSyncing ||
              (app.suspended && !isArgo)}
            title={app.suspended && !isArgo
              ? "Resume before reconciling"
              : isSyncing
              ? "Sync in progress"
              : undefined}
          >
            {isArgo ? "Sync" : "Reconcile"}
          </Button>

          {app.suspended
            ? (
              <Button
                type="button"
                variant="ghost"
                onClick={() => handleSuspend(false)}
                disabled={actionInFlight.value}
              >
                Resume
              </Button>
            )
            : (
              <Button
                type="button"
                variant="ghost"
                onClick={() => handleSuspend(true)}
                disabled={actionInFlight.value}
              >
                Suspend
              </Button>
            )}

          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value || actionInFlight.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        </div>
      </div>

      {/* Source panel */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
        <h2 class="text-sm font-medium text-text-muted mb-3">Source</h2>
        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          {app.source.repoURL && (
            <div>
              <dt class="text-text-muted">Repository</dt>
              <dd class="text-text-primary">
                {/^https?:\/\//i.test(app.source.repoURL)
                  ? (
                    <a
                      href={app.source.repoURL}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-brand hover:underline break-all"
                    >
                      {app.source.repoURL}
                    </a>
                  )
                  : <span class="break-all">{app.source.repoURL}</span>}
              </dd>
            </div>
          )}
          {app.source.path && (
            <div>
              <dt class="text-text-muted">Path</dt>
              <dd class="font-mono text-text-primary">{app.source.path}</dd>
            </div>
          )}
          {app.source.targetRevision && (
            <div>
              <dt class="text-text-muted">Target Revision</dt>
              <dd class="font-mono text-text-primary">
                {app.source.targetRevision}
              </dd>
            </div>
          )}
          {app.source.chartName && (
            <div>
              <dt class="text-text-muted">Chart</dt>
              <dd class="text-text-primary">
                {app.source.chartName}
                {app.source.chartVersion ? ` v${app.source.chartVersion}` : ""}
              </dd>
            </div>
          )}
          {app.destinationNamespace && (
            <div>
              <dt class="text-text-muted">Destination Namespace</dt>
              <dd class="text-text-primary">{app.destinationNamespace}</dd>
            </div>
          )}
          {app.destinationCluster && (
            <div>
              <dt class="text-text-muted">Destination Cluster</dt>
              <dd class="text-text-primary">{app.destinationCluster}</dd>
            </div>
          )}
        </dl>
      </div>

      {/* Managed Resources table */}
      <div class="mb-6">
        <h2 class="text-lg font-semibold text-text-primary mb-3">
          Managed Resources
          {resources && resources.length > 0 && (
            <span class="text-sm font-normal text-text-muted ml-2">
              ({resources.length})
            </span>
          )}
        </h2>
        {resources && resources.length > 0
          ? (
            <div class="overflow-x-auto rounded-lg border border-border-primary">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary bg-surface">
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Kind
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Name
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Namespace
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Status
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Health
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-subtle">
                  {resources.map((r: ManagedResource, i: number) => {
                    const href = resourceHref(r.kind, r.namespace, r.name);
                    const syncColor = SYNC_COLORS[r.status.toLowerCase()] ??
                      "var(--text-secondary)";
                    const healthColor = r.health
                      ? (HEALTH_COLORS[r.health.toLowerCase()] ??
                        "var(--text-secondary)")
                      : undefined;
                    return (
                      <tr
                        key={`${r.kind}-${r.namespace}-${r.name}-${i}`}
                        class="hover:bg-hover/30"
                      >
                        <td class="px-3 py-2 text-text-secondary">{r.kind}</td>
                        <td class="px-3 py-2">
                          {href
                            ? (
                              <a
                                href={href}
                                class="text-brand hover:underline"
                              >
                                {r.name}
                              </a>
                            )
                            : <span class="text-text-primary">{r.name}</span>}
                        </td>
                        <td class="px-3 py-2 text-text-muted">
                          {r.namespace ?? "-"}
                        </td>
                        <td class="px-3 py-2">
                          <span style={{ color: syncColor }}>{r.status}</span>
                        </td>
                        <td class="px-3 py-2">
                          {r.health
                            ? (
                              <span style={{ color: healthColor }}>
                                {r.health}
                              </span>
                            )
                            : <span class="text-text-muted">-</span>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
          : (
            <div class="text-center py-8 rounded-lg border border-border-primary bg-bg-elevated">
              <p class="text-text-muted">No managed resources found.</p>
            </div>
          )}
      </div>

      {/* Revision History */}
      <div>
        <h2 class="text-lg font-semibold text-text-primary mb-3">
          Revision History
        </h2>
        {history && history.length > 0
          ? (
            <div class="overflow-x-auto rounded-lg border border-border-primary">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary bg-surface">
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Revision
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Status
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Message
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Deployed At
                    </th>
                    {isArgo && (
                      <th class="px-3 py-2 text-right text-xs font-medium text-text-muted">
                        Action
                      </th>
                    )}
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-subtle">
                  {history.map((h: RevisionEntry, i: number) => {
                    const syncColor = SYNC_COLORS[h.status.toLowerCase()] ??
                      "var(--text-secondary)";
                    return (
                      <tr key={`${h.revision}-${i}`} class="hover:bg-hover/30">
                        <td class="px-3 py-2 font-mono text-text-primary">
                          {h.revision.length > 7
                            ? h.revision.slice(0, 7)
                            : h.revision}
                        </td>
                        <td class="px-3 py-2">
                          <span style={{ color: syncColor }}>{h.status}</span>
                        </td>
                        <td class="px-3 py-2 text-text-secondary max-w-xs truncate">
                          {h.message ?? "-"}
                        </td>
                        <td class="px-3 py-2 text-text-muted">
                          {h.deployedAt
                            ? new Date(h.deployedAt).toLocaleString()
                            : "-"}
                        </td>
                        {isArgo && (
                          <td class="px-3 py-2 text-right">
                            <button
                              type="button"
                              onClick={() =>
                                handleRollback(h.revision, h.deployedAt)}
                              disabled={actionInFlight.value || isSyncing}
                              class="text-xs text-brand hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                              Rollback
                            </button>
                          </td>
                        )}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
          : (
            <div class="text-center py-8 rounded-lg border border-border-primary bg-bg-elevated">
              <p class="text-text-muted">
                Revision history not available for this application type.
              </p>
            </div>
          )}
      </div>

      {/* Confirmation dialog */}
      {confirmAction.value && (
        <ConfirmDialog
          title={confirmAction.value.title}
          message={confirmAction.value.message}
          confirmLabel={confirmAction.value.label}
          danger={confirmAction.value.danger}
          loading={actionInFlight.value}
          onConfirm={confirmAction.value.onConfirm}
          onCancel={() => {
            confirmAction.value = null;
          }}
        />
      )}
    </div>
  );
}
