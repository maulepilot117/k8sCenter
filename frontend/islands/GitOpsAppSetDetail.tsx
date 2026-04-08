import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useCallback, useEffect } from "preact/hooks";
import { apiDelete, apiGet, apiPost } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { showToast } from "@/islands/ToastProvider.tsx";
import {
  HealthStatusBadge,
  SyncStatusBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import type {
  AppSetCondition,
  AppSetDetail,
  NormalizedApp,
} from "@/lib/gitops-types.ts";

const STATUS_COLORS: Record<string, string> = {
  healthy: "var(--success)",
  error: "var(--danger)",
  progressing: "var(--warning)",
};

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

const APP_CAP = 200;

export default function GitOpsAppSetDetail({ id }: { id: string }) {
  const detail = useSignal<AppSetDetail | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);
  const actionInFlight = useSignal(false);
  const expandedGenerators = useSignal<Record<number, boolean>>({});
  const conditionsExpanded = useSignal(false);

  const deleteConfirmOpen = useSignal(false);

  const fetchData = useCallback(async () => {
    try {
      const res = await apiGet<AppSetDetail>(
        `/v1/gitops/applicationsets/${encodeURIComponent(id)}`,
      );
      detail.value = res.data;
      error.value = null;
    } catch {
      error.value = "Failed to load ApplicationSet";
    }
  }, [id]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    [`gitops-appset-detail-${id}`, "applicationsets", ""],
  ], 3000);

  const handleRefresh = useCallback(async () => {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }, [fetchData]);

  const handleRefreshAction = useCallback(async () => {
    actionInFlight.value = true;
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
      actionInFlight.value = false;
    }
  }, [id, fetchData]);

  const handleDelete = useCallback(async () => {
    actionInFlight.value = true;
    try {
      await apiDelete(`/v1/gitops/applicationsets/${encodeURIComponent(id)}`);
      showToast("ApplicationSet deleted", "success");
      globalThis.location.href = "/gitops/applicationsets";
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Delete failed";
      showToast(msg, "error");
    } finally {
      actionInFlight.value = false;
      deleteConfirmOpen.value = false;
    }
  }, [id]);

  const toggleGenerator = useCallback((idx: number) => {
    expandedGenerators.value = {
      ...expandedGenerators.value,
      [idx]: !expandedGenerators.value[idx],
    };
  }, []);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value || !detail.value) {
    return (
      <div class="p-6">
        <a
          href="/gitops/applicationsets"
          class="text-sm text-brand hover:underline mb-4 inline-block"
        >
          &larr; Back to ApplicationSets
        </a>
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted mb-4">
            {error.value ?? "ApplicationSet not found"}
          </p>
          <Button type="button" variant="ghost" onClick={handleRefresh}>
            Retry
          </Button>
        </div>
      </div>
    );
  }

  const { appSet, generators, conditions, applications } = detail.value;
  const statusColor = STATUS_COLORS[appSet.status.toLowerCase()] ??
    "var(--text-secondary)";
  const appList = applications ?? [];
  const isTruncated = appList.length > APP_CAP;
  const displayedApps = isTruncated ? appList.slice(0, APP_CAP) : appList;
  const hasErrorConditions = (conditions ?? []).some(
    (c) => c.status === "True" && c.type.toLowerCase().includes("error"),
  );

  return (
    <div class="p-6">
      <a
        href="/gitops/applicationsets"
        class="text-sm text-brand hover:underline mb-4 inline-block"
      >
        &larr; Back to ApplicationSets
      </a>

      {/* Header */}
      <div class="flex items-center justify-between mb-6">
        <div class="flex items-center gap-3 flex-wrap">
          <h1 class="text-2xl font-bold text-text-primary">{appSet.name}</h1>
          <span class="text-sm text-text-muted">{appSet.namespace}</span>
          <span
            class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
            style={{
              color: statusColor,
              backgroundColor:
                `color-mix(in srgb, ${statusColor} 15%, transparent)`,
            }}
          >
            {appSet.status}
          </span>
        </div>
        <div class="flex items-center gap-2">
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefreshAction}
            disabled={actionInFlight.value}
          >
            Refresh
          </Button>
          <a href="/tools/yaml-apply">
            <Button type="button" variant="ghost">Edit</Button>
          </a>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              deleteConfirmOpen.value = true;
            }}
            disabled={actionInFlight.value}
          >
            <span class="text-danger">Delete</span>
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Reload"}
          </Button>
        </div>
      </div>

      {/* Generators panel */}
      <div class="mb-6">
        <h2 class="text-lg font-semibold text-text-primary mb-3">
          Generators
          <span class="text-sm font-normal text-text-muted ml-2">
            ({generators.length})
          </span>
        </h2>
        {generators.length > 0
          ? (
            <div class="space-y-3">
              {generators.map((gen, idx) => {
                const genType = detectGeneratorType(gen);
                const isExpanded = !!expandedGenerators.value[idx];
                return (
                  <div
                    key={idx}
                    class="rounded-lg border border-border-primary bg-bg-elevated"
                  >
                    <button
                      type="button"
                      class="w-full flex items-center justify-between px-4 py-3 text-left"
                      onClick={() => toggleGenerator(idx)}
                    >
                      <span class="inline-flex items-center gap-2">
                        <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-brand/10 text-brand">
                          {genType}
                        </span>
                        <span class="text-sm text-text-muted">
                          Generator {idx + 1}
                        </span>
                      </span>
                      <span class="text-text-muted text-xs">
                        {isExpanded ? "Collapse" : "Expand"}
                      </span>
                    </button>
                    {isExpanded && (
                      <div class="px-4 pb-4">
                        <pre class="text-xs text-text-secondary bg-surface rounded p-3 overflow-x-auto">
                          {JSON.stringify(gen, null, 2)}
                        </pre>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )
          : (
            <div class="text-center py-6 rounded-lg border border-border-primary bg-bg-elevated">
              <p class="text-text-muted">No generators defined.</p>
            </div>
          )}
      </div>

      {/* Template panel */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
        <h2 class="text-sm font-medium text-text-muted mb-3">Template</h2>
        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          {appSet.templateSource.repoURL && (
            <div>
              <dt class="text-text-muted">Repository</dt>
              <dd class="text-text-primary">
                {/^https?:\/\//i.test(appSet.templateSource.repoURL)
                  ? (
                    <a
                      href={appSet.templateSource.repoURL}
                      target="_blank"
                      rel="noopener noreferrer"
                      class="text-brand hover:underline break-all"
                    >
                      {appSet.templateSource.repoURL}
                    </a>
                  )
                  : (
                    <span class="break-all">
                      {appSet.templateSource.repoURL}
                    </span>
                  )}
              </dd>
            </div>
          )}
          {appSet.templateSource.path && (
            <div>
              <dt class="text-text-muted">Path</dt>
              <dd class="font-mono text-text-primary">
                {appSet.templateSource.path}
              </dd>
            </div>
          )}
          {appSet.templateSource.targetRevision && (
            <div>
              <dt class="text-text-muted">Target Revision</dt>
              <dd class="font-mono text-text-primary">
                {appSet.templateSource.targetRevision}
              </dd>
            </div>
          )}
          {appSet.templateSource.chartName && (
            <div>
              <dt class="text-text-muted">Chart</dt>
              <dd class="text-text-primary">
                {appSet.templateSource.chartName}
                {appSet.templateSource.chartVersion
                  ? ` v${appSet.templateSource.chartVersion}`
                  : ""}
              </dd>
            </div>
          )}
          <div>
            <dt class="text-text-muted">Destination</dt>
            <dd class="text-text-primary">
              {appSet.templateDestination || "-"}
            </dd>
          </div>
        </dl>
      </div>

      {/* Generated Applications table */}
      <div class="mb-6">
        <h2 class="text-lg font-semibold text-text-primary mb-3">
          Generated Applications
          <span class="text-sm font-normal text-text-muted ml-2">
            ({appList.length})
          </span>
        </h2>
        {isTruncated && (
          <p class="text-xs text-warning mb-2">
            Showing first {APP_CAP} of {appList.length} applications.
          </p>
        )}
        {displayedApps.length > 0
          ? (
            <div class="overflow-x-auto rounded-lg border border-border-primary">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary bg-surface">
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Name
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Sync
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Health
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Revision
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Dest Namespace
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-subtle">
                  {displayedApps.map((app: NormalizedApp) => (
                    <tr
                      key={app.id}
                      class="hover:bg-hover/30 cursor-pointer"
                      onClick={() => {
                        globalThis.location.href = "/gitops/applications/" +
                          encodeURIComponent(app.id);
                      }}
                    >
                      <td class="px-3 py-2">
                        <span class="text-brand hover:underline font-medium">
                          {app.name}
                        </span>
                      </td>
                      <td class="px-3 py-2">
                        <SyncStatusBadge status={app.syncStatus} />
                      </td>
                      <td class="px-3 py-2">
                        <HealthStatusBadge status={app.healthStatus} />
                      </td>
                      <td class="px-3 py-2 font-mono text-xs text-text-secondary">
                        {app.currentRevision
                          ? app.currentRevision.slice(0, 7)
                          : "-"}
                      </td>
                      <td class="px-3 py-2 text-text-secondary text-xs">
                        {app.destinationNamespace || "-"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
          : (
            <div class="text-center py-8 rounded-lg border border-border-primary bg-bg-elevated">
              <p class="text-text-muted">
                No generated applications found.
              </p>
            </div>
          )}
      </div>

      {/* Conditions panel */}
      {conditions && conditions.length > 0 && (
        <div class="mb-6">
          <button
            type="button"
            class="flex items-center gap-2 mb-3"
            onClick={() => {
              conditionsExpanded.value = !conditionsExpanded.value;
            }}
          >
            <h2 class="text-lg font-semibold text-text-primary">
              Conditions
              <span class="text-sm font-normal text-text-muted ml-2">
                ({conditions.length})
              </span>
            </h2>
            {hasErrorConditions && (
              <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium text-danger bg-danger/10">
                errors
              </span>
            )}
            <span class="text-text-muted text-xs">
              {conditionsExpanded.value ? "Collapse" : "Expand"}
            </span>
          </button>
          {conditionsExpanded.value && (
            <div class="overflow-x-auto rounded-lg border border-border-primary">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary bg-surface">
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Type
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Status
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Message
                    </th>
                    <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                      Reason
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-subtle">
                  {conditions.map((c: AppSetCondition, i: number) => {
                    const isError = c.type.toLowerCase().includes("error") &&
                      c.status === "True";
                    return (
                      <tr key={`${c.type}-${i}`} class="hover:bg-hover/30">
                        <td
                          class={`px-3 py-2 font-medium ${
                            isError ? "text-danger" : "text-text-primary"
                          }`}
                        >
                          {c.type}
                        </td>
                        <td
                          class={`px-3 py-2 ${
                            isError ? "text-danger" : "text-text-secondary"
                          }`}
                        >
                          {c.status}
                        </td>
                        <td class="px-3 py-2 text-text-secondary max-w-md truncate">
                          {c.message ?? "-"}
                        </td>
                        <td class="px-3 py-2 text-text-muted">
                          {c.reason ?? "-"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Delete confirmation dialog */}
      {deleteConfirmOpen.value && (
        <ConfirmDialog
          title={`Delete ${appSet.name}?`}
          message={appSet.preserveOnDeletion
            ? `This will delete the ApplicationSet. Child Applications (${appSet.generatedAppCount}) will be preserved as standalone resources.`
            : `This will also delete ${appSet.generatedAppCount} child Applications and all their managed resources.`}
          confirmLabel="Delete"
          danger
          typeToConfirm={appSet.name}
          loading={actionInFlight.value}
          onConfirm={handleDelete}
          onCancel={() => {
            deleteConfirmOpen.value = false;
          }}
        />
      )}
    </div>
  );
}
