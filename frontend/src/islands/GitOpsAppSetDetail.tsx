import { useCallback, useEffect, useState } from "react";
import DetailShell from "@/components/k8s/DetailShell.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import {
  HealthStatusBadge,
  SyncStatusBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { RemoteRefreshHint } from "@/components/ui/LiveBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { apiDelete, apiGet, apiPost } from "@/lib/api.ts";
import type {
  AppSetCondition,
  AppSetDetail,
  NormalizedApp,
} from "@/lib/gitops-types.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { showToast } from "@/src/lib/toast-store.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

const GENERATOR_TYPES = [
  "list",
  "git",
  "clusters",
  "matrix",
  "merge",
  "pullRequest",
  "scmProvider",
  "clusterDecisionResource",
  "plugin",
];

function detectGeneratorType(gen: Record<string, unknown>): string {
  for (const t of GENERATOR_TYPES) {
    if (t in gen) return t;
  }
  const keys = Object.keys(gen);
  return keys.length > 0 ? keys[0] : "unknown";
}

function statusToTone(status: string): Tone {
  switch (status.toLowerCase()) {
    case "healthy":
      return "ok";
    case "error":
      return "crit";
    case "progressing":
      return "info";
    default:
      return "neutral";
  }
}

const APP_CAP = 200;

// ApplicationSet icon SVG
function AppSetIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      className="w-5 h-5"
      style={{ color: "var(--accent)" }}
      aria-hidden="true"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M9 3H5a2 2 0 0 0-2 2v4m6-6h10a2 2 0 0 1 2 2v4M9 3v18m0 0h10a2 2 0 0 0 2-2v-4M9 21H5a2 2 0 0 1-2-2v-4m0 0h18"
      />
    </svg>
  );
}

export default function GitOpsAppSetDetail({ id }: { id: string }) {
  const isClient = useIsClient();
  const [detail, setDetail] = useState<AppSetDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [actionInFlight, setActionInFlight] = useState(false);
  const [expandedGenerators, setExpandedGenerators] = useState<
    Record<number, boolean>
  >({});
  const [activeTab, setActiveTab] = useState("generators");

  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const res = await apiGet<AppSetDetail>(
        `/v1/gitops/applicationsets/${encodeURIComponent(id)}`,
      );
      setDetail(res.data);
      setError(null);
    } catch {
      setError("Failed to load ApplicationSet");
    }
  }, [id]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  useWsRefetch(
    fetchData,
    [[`gitops-appset-detail-${id}`, "applicationsets", ""]],
    3000,
  );

  const handleRefresh = useCallback(async () => {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }, [fetchData]);

  const handleRefreshAction = useCallback(async () => {
    setActionInFlight(true);
    try {
      const res = await apiPost<{ message: string }>(
        `/v1/gitops/applicationsets/${encodeURIComponent(id)}/refresh`,
      );
      showToast(res.data.message, "success");
      await fetchData();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Refresh failed";
      showToast(msg, "error");
    } finally {
      setActionInFlight(false);
    }
  }, [id, fetchData]);

  const handleDelete = useCallback(async () => {
    setActionInFlight(true);
    try {
      await apiDelete(`/v1/gitops/applicationsets/${encodeURIComponent(id)}`);
      showToast("ApplicationSet deleted", "success");
      globalThis.location.href = "/gitops/applicationsets";
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Delete failed";
      showToast(msg, "error");
    } finally {
      setActionInFlight(false);
      setDeleteConfirmOpen(false);
    }
  }, [id]);

  const toggleGenerator = useCallback((idx: number) => {
    setExpandedGenerators((prev) => ({ ...prev, [idx]: !prev[idx] }));
  }, []);

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error || !detail) {
    return (
      <div className="p-6">
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted mb-4">
            {error ?? "ApplicationSet not found"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const { appSet, generators, conditions, applications } = detail;
  const appList = applications ?? [];
  const isTruncated = appList.length > APP_CAP;
  const displayedApps = isTruncated ? appList.slice(0, APP_CAP) : appList;
  const hasErrorConditions = (conditions ?? []).some(
    (c) => c.status === "True" && c.type.toLowerCase().includes("error"),
  );

  const tabs = [
    { id: "generators", label: `Generators (${generators.length})` },
    { id: "template", label: "Template" },
    { id: "applications", label: `Applications (${appList.length})` },
    ...(conditions && conditions.length > 0
      ? [{ id: "conditions", label: `Conditions (${conditions.length})` }]
      : []),
  ];

  const actions = (
    <>
      <Button
        type="button"
        variant="ghost"
        onClick={handleRefreshAction}
        disabled={actionInFlight}
      >
        Refresh
      </Button>
      <a href="/tools/yaml-apply">
        <Button type="button" variant="ghost">
          Edit
        </Button>
      </a>
      <Button
        type="button"
        variant="ghost"
        onClick={() => {
          setDeleteConfirmOpen(true);
        }}
        disabled={actionInFlight}
      >
        <span style={{ color: "var(--error)" }}>Delete</span>
      </Button>
      <RemoteRefreshHint control="Reload" />
      <Button
        type="button"
        variant="ghost"
        onClick={handleRefresh}
        disabled={refreshing}
      >
        {refreshing ? "Refreshing..." : "Reload"}
      </Button>
    </>
  );

  return (
    <>
      <DetailShell
        icon={<AppSetIcon />}
        title={appSet.name}
        subtitle={`ApplicationSet · ${appSet.namespace}`}
        status={{ label: appSet.status, tone: statusToTone(appSet.status) }}
        actions={actions}
        tabs={tabs}
        active={activeTab}
        onTab={setActiveTab}
      >
        {/* Generators tab */}
        {activeTab === "generators" && (
          <div className="space-y-3">
            {generators.length > 0 ? (
              generators.map((gen, idx) => {
                const genType = detectGeneratorType(gen);
                const isExpanded = !!expandedGenerators[idx];
                return (
                  <div
                    // biome-ignore lint/suspicious/noArrayIndexKey: generators are plain objects with no id, and the expanded state is keyed by position
                    key={idx}
                    className="rounded-lg border border-border-primary bg-elevated"
                  >
                    <button
                      type="button"
                      className="w-full flex items-center justify-between px-4 py-3 text-left"
                      onClick={() => toggleGenerator(idx)}
                    >
                      <span className="inline-flex items-center gap-2">
                        <span className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-brand/10 text-brand">
                          {genType}
                        </span>
                        <span className="text-sm text-text-muted">
                          Generator {idx + 1}
                        </span>
                      </span>
                      <span className="text-text-muted text-xs">
                        {isExpanded ? "Collapse" : "Expand"}
                      </span>
                    </button>
                    {isExpanded && (
                      <div className="px-4 pb-4">
                        <pre className="text-xs text-text-secondary bg-surface rounded p-3 overflow-x-auto">
                          {JSON.stringify(gen, null, 2)}
                        </pre>
                      </div>
                    )}
                  </div>
                );
              })
            ) : (
              <div className="text-center py-6 rounded-lg border border-border-primary bg-elevated">
                <p className="text-text-muted">No generators defined.</p>
              </div>
            )}
          </div>
        )}

        {/* Template tab */}
        {activeTab === "template" && (
          <GlassCard>
            <h2
              className="text-sm font-medium mb-3"
              style={{ color: "var(--text-muted)" }}
            >
              Template
            </h2>
            <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
              {appSet.templateSource.repoURL && (
                <div>
                  <dt className="text-text-muted">Repository</dt>
                  <dd className="text-text-primary">
                    {/^https?:\/\//i.test(appSet.templateSource.repoURL) ? (
                      <a
                        href={appSet.templateSource.repoURL}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-brand hover:underline break-all"
                      >
                        {appSet.templateSource.repoURL}
                      </a>
                    ) : (
                      <span className="break-all">
                        {appSet.templateSource.repoURL}
                      </span>
                    )}
                  </dd>
                </div>
              )}
              {appSet.templateSource.path && (
                <div>
                  <dt className="text-text-muted">Path</dt>
                  <dd className="font-mono text-text-primary">
                    {appSet.templateSource.path}
                  </dd>
                </div>
              )}
              {appSet.templateSource.targetRevision && (
                <div>
                  <dt className="text-text-muted">Target Revision</dt>
                  <dd className="font-mono text-text-primary">
                    {appSet.templateSource.targetRevision}
                  </dd>
                </div>
              )}
              {appSet.templateSource.chartName && (
                <div>
                  <dt className="text-text-muted">Chart</dt>
                  <dd className="text-text-primary">
                    {appSet.templateSource.chartName}
                    {appSet.templateSource.chartVersion
                      ? ` v${appSet.templateSource.chartVersion}`
                      : ""}
                  </dd>
                </div>
              )}
              <div>
                <dt className="text-text-muted">Destination</dt>
                <dd className="text-text-primary">
                  {appSet.templateDestination || "-"}
                </dd>
              </div>
            </dl>
          </GlassCard>
        )}

        {/* Applications tab */}
        {activeTab === "applications" && (
          <div>
            {isTruncated && (
              <p className="text-xs mb-2" style={{ color: "var(--warning)" }}>
                Showing first {APP_CAP} of {appList.length} applications.
              </p>
            )}
            {displayedApps.length > 0 ? (
              <div className="overflow-x-auto rounded-lg border border-border-primary">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border-primary bg-surface">
                      <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Name
                      </th>
                      <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Sync
                      </th>
                      <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Health
                      </th>
                      <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Revision
                      </th>
                      <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Dest Namespace
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border-subtle">
                    {displayedApps.map((app: NormalizedApp) => (
                      <tr
                        key={app.id}
                        className="hover:bg-hover/30 cursor-pointer"
                        onClick={() => {
                          globalThis.location.href =
                            "/gitops/applications/" +
                            encodeURIComponent(app.id);
                        }}
                      >
                        <td className="px-3 py-2">
                          <span className="text-brand hover:underline font-medium">
                            {app.name}
                          </span>
                        </td>
                        <td className="px-3 py-2">
                          <SyncStatusBadge status={app.syncStatus} />
                        </td>
                        <td className="px-3 py-2">
                          <HealthStatusBadge status={app.healthStatus} />
                        </td>
                        <td className="px-3 py-2 font-mono text-xs text-text-secondary">
                          {app.currentRevision
                            ? app.currentRevision.slice(0, 7)
                            : "-"}
                        </td>
                        <td className="px-3 py-2 text-text-secondary text-xs">
                          {app.destinationNamespace || "-"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="text-center py-8 rounded-lg border border-border-primary bg-elevated">
                <p className="text-text-muted">
                  No generated applications found.
                </p>
              </div>
            )}
          </div>
        )}

        {/* Conditions tab */}
        {activeTab === "conditions" && conditions && conditions.length > 0 && (
          <div>
            {hasErrorConditions && (
              <span
                className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium mb-3"
                style={{
                  color: "var(--error)",
                  background: "var(--error-dim)",
                }}
              >
                errors
              </span>
            )}
            <div className="overflow-x-auto rounded-lg border border-border-primary">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border-primary bg-surface">
                    <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Type
                    </th>
                    <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Status
                    </th>
                    <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Message
                    </th>
                    <th className="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Reason
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border-subtle">
                  {conditions.map((c: AppSetCondition, i: number) => {
                    const isError =
                      c.type.toLowerCase().includes("error") &&
                      c.status === "True";
                    return (
                      <tr
                        // biome-ignore lint/suspicious/noArrayIndexKey: a condition type can repeat, so the key is the type plus position
                        key={`${c.type}-${i}`}
                        className="hover:bg-hover/30"
                      >
                        <td
                          className="px-3 py-2 font-medium"
                          style={{
                            color: isError
                              ? "var(--error)"
                              : "var(--text-primary)",
                          }}
                        >
                          {c.type}
                        </td>
                        <td
                          className="px-3 py-2"
                          style={{
                            color: isError
                              ? "var(--error)"
                              : "var(--text-secondary)",
                          }}
                        >
                          {c.status}
                        </td>
                        <td className="px-3 py-2 text-text-secondary max-w-md truncate">
                          {c.message ?? "-"}
                        </td>
                        <td className="px-3 py-2 text-text-muted">
                          {c.reason ?? "-"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </DetailShell>

      {/* Delete confirmation dialog — rendered outside DetailShell as a modal */}
      {deleteConfirmOpen && (
        <ConfirmDialog
          title={`Delete ${appSet.name}?`}
          message={
            appSet.preserveOnDeletion
              ? `This will delete the ApplicationSet. Child Applications (${appSet.generatedAppCount}) will be preserved as standalone resources.`
              : `This will also delete ${appSet.generatedAppCount} child Applications and all their managed resources.`
          }
          confirmLabel="Delete"
          danger
          typeToConfirm={appSet.name}
          loading={actionInFlight}
          onConfirm={handleDelete}
          onCancel={() => {
            setDeleteConfirmOpen(false);
          }}
        />
      )}
    </>
  );
}
