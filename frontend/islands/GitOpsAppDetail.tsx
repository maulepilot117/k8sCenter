import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { api, apiGet, apiPost } from "@/lib/api.ts";
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
  CommitInfo,
  CommitsResponse,
  ManagedResource,
  RevisionEntry,
} from "@/lib/gitops-types.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import DetailShell from "@/components/k8s/DetailShell.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";

/** Map syncStatus string → DetailShell tone */
const SYNC_TONE: Record<string, Tone> = {
  synced: "ok",
  outofsync: "crit",
  progressing: "info",
  stalled: "warn",
  failed: "crit",
  unknown: "neutral",
};

/** GitOps icon — minimal SVG */
function GitOpsIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      style={{ color: "var(--accent)" }}
    >
      <circle cx="12" cy="12" r="3" />
      <circle cx="12" cy="5" r="2" />
      <circle cx="12" cy="19" r="2" />
      <line x1="12" y1="7" x2="12" y2="9" />
      <line x1="12" y1="15" x2="12" y2="17" />
    </svg>
  );
}

export default function GitOpsAppDetail({ id }: { id: string }) {
  const detail = useSignal<AppDetail | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);
  const actionInFlight = useSignal(false);
  const commits = useSignal<Record<string, CommitInfo>>({});
  const activeTab = useSignal("overview");

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

  // Async commit message enrichment — fetches after detail loads
  useEffect(() => {
    if (!IS_BROWSER || !detail.value) return;

    const app = detail.value.app;
    const repoURL = app.source?.repoURL;
    if (!repoURL || !repoURL.includes("://")) return;

    // Collect SHAs: current revision + history revisions
    const shas = new Set<string>();
    if (app.currentRevision) shas.add(app.currentRevision);
    for (const h of detail.value.history ?? []) {
      if (h.revision) shas.add(h.revision);
    }
    if (shas.size === 0) return;

    const controller = new AbortController();
    const shaList = [...shas].slice(0, 50).join(",");
    const url = `/v1/gitops/commits?repoURL=${
      encodeURIComponent(repoURL)
    }&shas=${shaList}`;

    api<CommitsResponse>(url, { method: "GET", signal: controller.signal })
      .then((res) => {
        if (res.data?.commits) {
          commits.value = res.data.commits;
        }
      })
      .catch(() => {
        // Graceful degradation — commit enrichment is optional
      });

    return () => controller.abort();
  }, [detail.value?.app?.id]);

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
  const compareBase = /^https?:\/\//i.test(app.source.repoURL ?? "")
    ? app.source.repoURL!.replace(/\.git$/, "")
    : null;

  const syncLabel = {
    synced: "Synced",
    outofsync: "Out of Sync",
    progressing: "Progressing",
    stalled: "Stalled",
    failed: "Failed",
    unknown: "Unknown",
  }[app.syncStatus] ?? app.syncStatus;

  const syncTone: Tone = SYNC_TONE[app.syncStatus] ?? "neutral";

  /* ── Action buttons (passed to DetailShell as `actions` prop) ─── */
  const actionButtons = (
    <>
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
    </>
  );

  /* ── Overview tab ─────────────────────────────────────────────── */
  const overviewPanel = (
    <GlassCard>
      <div class="flex items-center gap-2 flex-wrap mb-4">
        <ToolBadge tool={app.tool as string} />
        <SyncStatusBadge status={app.syncStatus} />
        <HealthStatusBadge status={app.healthStatus} />
        {app.suspended && (
          <span
            class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
            style={{
              color: "var(--text-muted)",
              background: "var(--bg-elevated)",
            }}
          >
            Suspended
          </span>
        )}
      </div>
      <h2
        class="text-xs font-semibold uppercase tracking-wider mb-3"
        style={{ color: "var(--text-muted)" }}
      >
        Source
      </h2>
      <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-sm">
        {app.source.repoURL && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Repository</dt>
            <dd style={{ color: "var(--text-primary)" }}>
              {/^https?:\/\//i.test(app.source.repoURL)
                ? (
                  <a
                    href={app.source.repoURL}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="hover:underline break-all"
                    style={{ color: "var(--accent)" }}
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
            <dt style={{ color: "var(--text-muted)" }}>Path</dt>
            <dd class="font-mono" style={{ color: "var(--text-primary)" }}>
              {app.source.path}
            </dd>
          </div>
        )}
        {app.source.targetRevision && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Target Revision</dt>
            <dd class="font-mono" style={{ color: "var(--text-primary)" }}>
              {app.source.targetRevision}
            </dd>
          </div>
        )}
        {app.source.chartName && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Chart</dt>
            <dd style={{ color: "var(--text-primary)" }}>
              {app.source.chartName}
              {app.source.chartVersion ? ` v${app.source.chartVersion}` : ""}
            </dd>
          </div>
        )}
        {app.destinationNamespace && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>
              Destination Namespace
            </dt>
            <dd style={{ color: "var(--text-primary)" }}>
              {app.destinationNamespace}
            </dd>
          </div>
        )}
        {app.destinationCluster && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Destination Cluster</dt>
            <dd style={{ color: "var(--text-primary)" }}>
              {app.destinationCluster}
            </dd>
          </div>
        )}
      </dl>
    </GlassCard>
  );

  /* ── Resources tab ────────────────────────────────────────────── */
  const resourcesPanel = resources && resources.length > 0
    ? (
      <div
        class="overflow-x-auto rounded-lg border"
        style={{ borderColor: "var(--border-primary)" }}
      >
        <table class="w-full text-sm">
          <thead>
            <tr
              class="border-b"
              style={{
                borderColor: "var(--border-primary)",
                background: "var(--bg-surface)",
              }}
            >
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Kind
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Name
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Namespace
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Status
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
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
                  <td
                    class="px-3 py-2"
                    style={{ color: "var(--text-secondary)" }}
                  >
                    {r.kind}
                  </td>
                  <td class="px-3 py-2">
                    {href
                      ? (
                        <a
                          href={href}
                          class="hover:underline"
                          style={{ color: "var(--accent)" }}
                        >
                          {r.name}
                        </a>
                      )
                      : (
                        <span style={{ color: "var(--text-primary)" }}>
                          {r.name}
                        </span>
                      )}
                  </td>
                  <td class="px-3 py-2" style={{ color: "var(--text-muted)" }}>
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
                      : <span style={{ color: "var(--text-muted)" }}>-</span>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    )
    : (
      <div
        class="text-center py-8 rounded-lg border"
        style={{
          borderColor: "var(--border-primary)",
          background: "var(--bg-elevated)",
        }}
      >
        <p style={{ color: "var(--text-muted)" }}>
          No managed resources found.
        </p>
      </div>
    );

  /* ── History tab ──────────────────────────────────────────────── */
  const historyPanel = history && history.length > 0
    ? (
      <div
        class="overflow-x-auto rounded-lg border"
        style={{ borderColor: "var(--border-primary)" }}
      >
        <table class="w-full text-sm">
          <thead>
            <tr
              class="border-b"
              style={{
                borderColor: "var(--border-primary)",
                background: "var(--bg-surface)",
              }}
            >
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Revision
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Status
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Message
              </th>
              <th
                class="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Deployed At
              </th>
              {isArgo && (
                <th
                  class="px-3 py-2 text-right text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Action
                </th>
              )}
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {history.map((h: RevisionEntry, i: number) => {
              const syncColor = SYNC_COLORS[h.status.toLowerCase()] ??
                "var(--text-secondary)";
              const ci = commits.value[h.revision];
              const commitUrl = ci?.webUrl?.startsWith("https://")
                ? ci.webUrl
                : undefined;
              const shortSha = h.revision.length > 7
                ? h.revision.slice(0, 7)
                : h.revision;
              const prevRevision = i > 0 ? history[i - 1].revision : null;
              return (
                <tr key={`${h.revision}-${i}`} class="hover:bg-hover/30">
                  <td
                    class="px-3 py-2 font-mono"
                    style={{ color: "var(--text-primary)" }}
                  >
                    {commitUrl
                      ? (
                        <a
                          href={commitUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          class="hover:underline"
                          style={{ color: "var(--accent)" }}
                        >
                          {shortSha}
                        </a>
                      )
                      : shortSha}
                    {compareBase && prevRevision && (
                      <a
                        href={`${compareBase}/compare/${prevRevision}...${h.revision}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        class="ml-2 text-xs hover:underline"
                        style={{ color: "var(--text-muted)" }}
                        title="Compare with previous deployment"
                      >
                        diff
                      </a>
                    )}
                  </td>
                  <td class="px-3 py-2">
                    <span style={{ color: syncColor }}>{h.status}</span>
                  </td>
                  <td
                    class="px-3 py-2 max-w-xs"
                    style={{ color: "var(--text-secondary)" }}
                  >
                    {ci
                      ? (
                        <div class="min-w-0">
                          <div
                            class="truncate text-sm"
                            style={{ color: "var(--text-primary)" }}
                          >
                            {commitUrl
                              ? (
                                <a
                                  href={commitUrl}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  class="hover:underline"
                                >
                                  {ci.title}
                                </a>
                              )
                              : ci.title}
                          </div>
                          <div
                            class="text-xs truncate"
                            style={{ color: "var(--text-muted)" }}
                          >
                            {ci.authorName}
                          </div>
                        </div>
                      )
                      : (h.message ?? "-")}
                  </td>
                  <td class="px-3 py-2" style={{ color: "var(--text-muted)" }}>
                    {h.deployedAt
                      ? new Date(h.deployedAt).toLocaleString()
                      : "-"}
                  </td>
                  {isArgo && (
                    <td class="px-3 py-2 text-right">
                      <button
                        type="button"
                        onClick={() => handleRollback(h.revision, h.deployedAt)}
                        disabled={actionInFlight.value || isSyncing}
                        class="text-xs hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
                        style={{ color: "var(--accent)" }}
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
      <div
        class="text-center py-8 rounded-lg border"
        style={{
          borderColor: "var(--border-primary)",
          background: "var(--bg-elevated)",
        }}
      >
        <p style={{ color: "var(--text-muted)" }}>
          Revision history not available for this application type.
        </p>
      </div>
    );

  return (
    <>
      <DetailShell
        icon={<GitOpsIcon />}
        title={app.name}
        subtitle={`${
          isArgo ? "Argo CD" : "Flux"
        } · ${app.namespace} · ${app.kind}`}
        status={{ label: syncLabel, tone: syncTone }}
        actions={actionButtons}
        tabs={[
          { id: "overview", label: "Overview" },
          { id: "resources", label: `Resources (${resources?.length ?? 0})` },
          { id: "history", label: "History" },
        ]}
        active={activeTab.value}
        onTab={(id) => {
          activeTab.value = id;
        }}
      >
        {activeTab.value === "overview" && overviewPanel}
        {activeTab.value === "resources" && resourcesPanel}
        {activeTab.value === "history" && historyPanel}
      </DetailShell>

      {/* Confirmation dialog — floats above DetailShell */}
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
    </>
  );
}
