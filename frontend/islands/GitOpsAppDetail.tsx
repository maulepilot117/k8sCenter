import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
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
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";

/** Irregular plurals for kind → RESOURCE_DETAIL_PATHS lookup. */
const KIND_PLURALS: Record<string, string> = {
  ingress: "ingresses",
  endpointslice: "endpointslices",
  networkpolicy: "networkpolicies",
};

function resourceHref(
  kind: string,
  namespace?: string,
  name?: string,
): string | null {
  const lower = kind.toLowerCase();
  const plural = KIND_PLURALS[lower] ?? lower + "s";
  const basePath = RESOURCE_DETAIL_PATHS[plural];
  if (!basePath || !name) return null;
  return namespace
    ? `${basePath}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
    : `${basePath}/${encodeURIComponent(name)}`;
}

export default function GitOpsAppDetail({ id }: { id: string }) {
  const detail = useSignal<AppDetail | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);

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

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
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
        <Button
          type="button"
          variant="ghost"
          onClick={handleRefresh}
          disabled={refreshing.value}
        >
          {refreshing.value ? "Refreshing..." : "Refresh"}
        </Button>
      </div>

      {/* Source panel */}
      <div class="rounded-lg border border-border-primary bg-bg-elevated p-4 mb-6">
        <h2 class="text-sm font-medium text-text-muted mb-3">Source</h2>
        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          {app.source.repoURL && (
            <div>
              <dt class="text-text-muted">Repository</dt>
              <dd class="text-text-primary">
                <a
                  href={app.source.repoURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-brand hover:underline break-all"
                >
                  {app.source.repoURL}
                </a>
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
    </div>
  );
}
