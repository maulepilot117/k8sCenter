import { useCallback, useEffect, useState } from "react";
import DetailShell from "@/components/k8s/DetailShell.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import {
  HEALTH_COLORS,
  HealthStatusBadge,
  SYNC_COLORS,
  SyncStatusBadge,
  ToolBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { RemoteRefreshHint } from "@/components/ui/LiveBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { api, apiGet, apiPost } from "@/lib/api.ts";
import type {
  AppDetail,
  CommitInfo,
  CommitsResponse,
  ManagedResource,
  RevisionEntry,
} from "@/lib/gitops-types.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

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
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
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
  const isClient = useIsClient();
  const [detail, setDetail] = useState<AppDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [actionInFlight, setActionInFlight] = useState(false);
  const [commits, setCommits] = useState<Record<string, CommitInfo>>({});
  const [activeTab, setActiveTab] = useState("overview");
  const detailRef = useLatest(detail);

  // Confirmation dialog state
  const [confirmAction, setConfirmAction] = useState<{
    title: string;
    message: string;
    label: string;
    danger: boolean;
    onConfirm: () => void;
  } | null>(null);

  const fetchData = useCallback(async () => {
    try {
      const res = await apiGet<AppDetail>(
        `/v1/gitops/applications/${encodeURIComponent(id)}`,
      );
      setDetail(res.data);
      setError(null);
    } catch {
      setError("Failed to load application");
    }
  }, [id]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  // Async commit message enrichment — fetches after detail loads
  const appId = detail?.app?.id;
  // biome-ignore lint/correctness/useExhaustiveDependencies: refetch the commits only when a different application loads, not on every refresh of the same one
  useEffect(() => {
    const loaded = detailRef.current;
    if (!IS_BROWSER || !loaded) return;

    const app = loaded.app;
    const repoURL = app.source?.repoURL;
    if (!repoURL?.includes("://")) return;

    // Collect SHAs: current revision + history revisions
    const shas = new Set<string>();
    if (app.currentRevision) shas.add(app.currentRevision);
    for (const h of loaded.history ?? []) {
      if (h.revision) shas.add(h.revision);
    }
    if (shas.size === 0) return;

    const controller = new AbortController();
    const shaList = [...shas].slice(0, 50).join(",");
    const url = `/v1/gitops/commits?repoURL=${encodeURIComponent(
      repoURL,
    )}&shas=${shaList}`;

    api<CommitsResponse>(url, { method: "GET", signal: controller.signal })
      .then((res) => {
        if (res.data?.commits) {
          setCommits(res.data.commits);
        }
      })
      .catch(() => {
        // Graceful degradation — commit enrichment is optional
      });

    return () => controller.abort();
  }, [appId]);

  // Determine the CRD kind from the composite ID
  const toolPrefix = id.split(":")[0];
  const kind =
    toolPrefix === "argo"
      ? "applications"
      : toolPrefix === "flux-hr"
        ? "helmreleases"
        : "kustomizations";

  useWsRefetch(fetchData, [[`gitops-detail-${id}`, kind, ""]], 3000);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  async function performAction(action: string, body?: unknown) {
    setActionInFlight(true);
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
      setActionInFlight(false);
    }
  }

  function requestConfirm(
    title: string,
    message: string,
    label: string,
    danger: boolean,
    onConfirm: () => void,
  ) {
    setConfirmAction({ title, message, label, danger, onConfirm });
  }

  function handleSync() {
    const app = detail?.app;
    if (!app) return;
    const verb = app.tool === "argocd" ? "Sync" : "Reconcile";
    requestConfirm(
      `${verb} ${app.name}?`,
      `This will trigger a ${verb.toLowerCase()} for ${app.name}.`,
      verb,
      false,
      () => {
        setConfirmAction(null);
        performAction("sync");
      },
    );
  }

  function handleSuspend(suspend: boolean) {
    const app = detail?.app;
    if (!app) return;
    if (suspend) {
      requestConfirm(
        `Suspend ${app.name}?`,
        "This will pause all reconciliation. Drift from git will not be corrected until resumed.",
        "Suspend",
        true,
        () => {
          setConfirmAction(null);
          performAction("suspend", { suspend: true });
        },
      );
    } else {
      performAction("suspend", { suspend: false });
    }
  }

  function handleRollback(revision: string, deployedAt: string) {
    const app = detail?.app;
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
        setConfirmAction(null);
        performAction("rollback", { revision });
      },
    );
  }

  if (!isClient) return null;

  /* Loading state */
  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  /* Error state */
  if (error || !detail) {
    return (
      <div className="p-6">
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted mb-4">
            {error ?? "Application not found"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const { app, resources, history } = detail;
  const isArgo = app.tool === "argocd";
  const isSyncing = app.syncStatus === "progressing";
  const compareBase = /^https?:\/\//i.test(app.source.repoURL ?? "")
    ? app.source.repoURL?.replace(/\.git$/, "")
    : null;

  const syncLabel =
    {
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
        disabled={actionInFlight || isSyncing || (app.suspended && !isArgo)}
        title={
          app.suspended && !isArgo
            ? "Resume before reconciling"
            : isSyncing
              ? "Sync in progress"
              : undefined
        }
      >
        {isArgo ? "Sync" : "Reconcile"}
      </Button>

      {app.suspended ? (
        <Button
          type="button"
          variant="ghost"
          onClick={() => handleSuspend(false)}
          disabled={actionInFlight}
        >
          Resume
        </Button>
      ) : (
        <Button
          type="button"
          variant="ghost"
          onClick={() => handleSuspend(true)}
          disabled={actionInFlight}
        >
          Suspend
        </Button>
      )}

      <RemoteRefreshHint />
      <Button
        type="button"
        variant="ghost"
        onClick={handleRefresh}
        disabled={refreshing || actionInFlight}
      >
        {refreshing ? "Refreshing..." : "Refresh"}
      </Button>
    </>
  );

  /* ── Overview tab ─────────────────────────────────────────────── */
  const overviewPanel = (
    <GlassCard>
      <div className="flex items-center gap-2 flex-wrap mb-4">
        <ToolBadge tool={app.tool as string} />
        <SyncStatusBadge status={app.syncStatus} />
        <HealthStatusBadge status={app.healthStatus} />
        {app.suspended && (
          <span
            className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
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
        className="text-xs font-semibold uppercase tracking-wider mb-3"
        style={{ color: "var(--text-muted)" }}
      >
        Source
      </h2>
      <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-sm">
        {app.source.repoURL && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Repository</dt>
            <dd style={{ color: "var(--text-primary)" }}>
              {/^https?:\/\//i.test(app.source.repoURL) ? (
                <a
                  href={app.source.repoURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="hover:underline break-all"
                  style={{ color: "var(--accent)" }}
                >
                  {app.source.repoURL}
                </a>
              ) : (
                <span className="break-all">{app.source.repoURL}</span>
              )}
            </dd>
          </div>
        )}
        {app.source.path && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Path</dt>
            <dd className="font-mono" style={{ color: "var(--text-primary)" }}>
              {app.source.path}
            </dd>
          </div>
        )}
        {app.source.targetRevision && (
          <div>
            <dt style={{ color: "var(--text-muted)" }}>Target Revision</dt>
            <dd className="font-mono" style={{ color: "var(--text-primary)" }}>
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
  // Per-resource health is only shown when the source reports it. Argo CD 3
  // no longer persists it in the Application (controller.resource.health.persist
  // defaults to false) and Flux inventories never carry it, so a column of
  // dashes would only look like missing data.
  const anyHealth = (resources ?? []).some((r) => r.health);
  const resourcesPanel =
    resources && resources.length > 0 ? (
      <div className="space-y-2">
        <div
          className="overflow-x-auto rounded-lg border"
          style={{ borderColor: "var(--border-primary)" }}
        >
          <table className="w-full text-sm">
            <thead>
              <tr
                className="border-b"
                style={{
                  borderColor: "var(--border-primary)",
                  background: "var(--bg-surface)",
                }}
              >
                <th
                  className="px-3 py-2 text-left text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Kind
                </th>
                <th
                  className="px-3 py-2 text-left text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Name
                </th>
                <th
                  className="px-3 py-2 text-left text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Namespace
                </th>
                <th
                  className="px-3 py-2 text-left text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Status
                </th>
                {anyHealth && (
                  <th
                    className="px-3 py-2 text-left text-xs font-medium"
                    style={{ color: "var(--text-muted)" }}
                  >
                    Health
                  </th>
                )}
              </tr>
            </thead>
            <tbody className="divide-y divide-border-subtle">
              {resources.map((r: ManagedResource, i: number) => {
                const href = resourceHref(r.kind, r.namespace, r.name);
                const syncColor =
                  SYNC_COLORS[r.status.toLowerCase()] ??
                  "var(--text-secondary)";
                const healthColor = r.health
                  ? (HEALTH_COLORS[r.health.toLowerCase()] ??
                    "var(--text-secondary)")
                  : undefined;
                return (
                  <tr
                    // biome-ignore lint/suspicious/noArrayIndexKey: managed resources have no unique id, so the key is the identity plus position
                    key={`${r.kind}-${r.namespace}-${r.name}-${i}`}
                    className="hover:bg-hover/30"
                  >
                    <td
                      className="px-3 py-2"
                      style={{ color: "var(--text-secondary)" }}
                    >
                      {r.kind}
                    </td>
                    <td className="px-3 py-2">
                      {href ? (
                        <a
                          href={href}
                          className="hover:underline"
                          style={{ color: "var(--accent)" }}
                        >
                          {r.name}
                        </a>
                      ) : (
                        <span style={{ color: "var(--text-primary)" }}>
                          {r.name}
                        </span>
                      )}
                    </td>
                    <td
                      className="px-3 py-2"
                      style={{ color: "var(--text-muted)" }}
                    >
                      {r.namespace ?? "-"}
                    </td>
                    <td className="px-3 py-2">
                      <span style={{ color: syncColor }}>{r.status}</span>
                    </td>
                    {anyHealth && (
                      <td className="px-3 py-2">
                        {r.health ? (
                          <span style={{ color: healthColor }}>{r.health}</span>
                        ) : (
                          <span style={{ color: "var(--text-muted)" }}>-</span>
                        )}
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {!anyHealth && isArgo && (
          <p className="text-xs" style={{ color: "var(--text-muted)" }}>
            Argo CD isn't reporting per-resource health in this Application.
            Argo CD 3 leaves it out unless controller.resource.health.persist is
            enabled; the application's overall health is shown above.
          </p>
        )}
      </div>
    ) : (
      <div
        className="text-center py-8 rounded-lg border"
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
  const historyPanel =
    history && history.length > 0 ? (
      <div
        className="overflow-x-auto rounded-lg border"
        style={{ borderColor: "var(--border-primary)" }}
      >
        <table className="w-full text-sm">
          <thead>
            <tr
              className="border-b"
              style={{
                borderColor: "var(--border-primary)",
                background: "var(--bg-surface)",
              }}
            >
              <th
                className="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Revision
              </th>
              <th
                className="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Status
              </th>
              <th
                className="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Message
              </th>
              <th
                className="px-3 py-2 text-left text-xs font-medium"
                style={{ color: "var(--text-muted)" }}
              >
                Deployed At
              </th>
              {isArgo && (
                <th
                  className="px-3 py-2 text-right text-xs font-medium"
                  style={{ color: "var(--text-muted)" }}
                >
                  Action
                </th>
              )}
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {history.map((h: RevisionEntry, i: number) => {
              const syncColor =
                SYNC_COLORS[h.status.toLowerCase()] ?? "var(--text-secondary)";
              const ci = commits[h.revision];
              const commitUrl = ci?.webUrl?.startsWith("https://")
                ? ci.webUrl
                : undefined;
              const shortSha =
                h.revision.length > 7 ? h.revision.slice(0, 7) : h.revision;
              const prevRevision = i > 0 ? history[i - 1].revision : null;
              return (
                <tr
                  // biome-ignore lint/suspicious/noArrayIndexKey: a revision can repeat in the history, so the key is the revision plus position
                  key={`${h.revision}-${i}`}
                  className="hover:bg-hover/30"
                >
                  <td
                    className="px-3 py-2 font-mono"
                    style={{ color: "var(--text-primary)" }}
                  >
                    {commitUrl ? (
                      <a
                        href={commitUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="hover:underline"
                        style={{ color: "var(--accent)" }}
                      >
                        {shortSha}
                      </a>
                    ) : (
                      shortSha
                    )}
                    {compareBase && prevRevision && (
                      <a
                        href={`${compareBase}/compare/${prevRevision}...${h.revision}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="ml-2 text-xs hover:underline"
                        style={{ color: "var(--text-muted)" }}
                        title="Compare with previous deployment"
                      >
                        diff
                      </a>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <span style={{ color: syncColor }}>{h.status}</span>
                  </td>
                  <td
                    className="px-3 py-2 max-w-xs"
                    style={{ color: "var(--text-secondary)" }}
                  >
                    {ci ? (
                      <div className="min-w-0">
                        <div
                          className="truncate text-sm"
                          style={{ color: "var(--text-primary)" }}
                        >
                          {commitUrl ? (
                            <a
                              href={commitUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="hover:underline"
                            >
                              {ci.title}
                            </a>
                          ) : (
                            ci.title
                          )}
                        </div>
                        <div
                          className="text-xs truncate"
                          style={{ color: "var(--text-muted)" }}
                        >
                          {ci.authorName}
                        </div>
                      </div>
                    ) : (
                      (h.message ?? "-")
                    )}
                  </td>
                  <td
                    className="px-3 py-2"
                    style={{ color: "var(--text-muted)" }}
                  >
                    {h.deployedAt
                      ? new Date(h.deployedAt).toLocaleString()
                      : "-"}
                  </td>
                  {isArgo && (
                    <td className="px-3 py-2 text-right">
                      <button
                        type="button"
                        onClick={() => handleRollback(h.revision, h.deployedAt)}
                        disabled={actionInFlight || isSyncing}
                        className="text-xs hover:underline disabled:opacity-50 disabled:cursor-not-allowed"
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
    ) : (
      <div
        className="text-center py-8 rounded-lg border"
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
        active={activeTab}
        onTab={setActiveTab}
      >
        {activeTab === "overview" && overviewPanel}
        {activeTab === "resources" && resourcesPanel}
        {activeTab === "history" && historyPanel}
      </DetailShell>

      {/* Confirmation dialog — floats above DetailShell */}
      {confirmAction && (
        <ConfirmDialog
          title={confirmAction.title}
          message={confirmAction.message}
          confirmLabel={confirmAction.label}
          danger={confirmAction.danger}
          loading={actionInFlight}
          onConfirm={confirmAction.onConfirm}
          onCancel={() => {
            setConfirmAction(null);
          }}
        />
      )}
    </>
  );
}
