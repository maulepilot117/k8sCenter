import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { wsStatus } from "@/lib/ws.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { type Column, DataTable } from "@/components/ui/DataTable.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import {
  HEALTH_COLORS,
  HealthStatusBadge,
  SYNC_COLORS,
  SyncStatusBadge,
  ToolBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import type {
  AppListResponse,
  GitOpsStatus,
  HealthStatus,
  NormalizedApp,
} from "@/lib/gitops-types.ts";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";

const PAGE_SIZE = 100;

const HEALTH_DOT_STATUS: Record<
  HealthStatus,
  "success" | "error" | "info" | "neutral"
> = {
  healthy: "success",
  degraded: "error",
  progressing: "info",
  suspended: "neutral",
  unknown: "neutral",
};

const COLUMNS: Column<NormalizedApp>[] = [
  {
    key: "name",
    label: "Name",
    render: (app) => (
      <div class="flex items-start gap-2">
        <div class="mt-0.5 shrink-0">
          <StatusDot
            status={HEALTH_DOT_STATUS[app.healthStatus] ?? "neutral"}
            size={8}
          />
        </div>
        <div>
          <div class="font-mono font-medium text-text-primary">{app.name}</div>
          <div class="text-xs text-text-muted">
            {app.namespace} &middot; {app.kind}
          </div>
        </div>
      </div>
    ),
  },
  {
    key: "tool",
    label: "Tool",
    class: "w-[100px]",
    render: (app) => <ToolBadge tool={app.tool} />,
  },
  {
    key: "sync",
    label: "Sync",
    class: "w-[120px]",
    render: (app) => <SyncStatusBadge status={app.syncStatus} />,
  },
  {
    key: "health",
    label: "Health",
    class: "w-[120px]",
    render: (app) => <HealthStatusBadge status={app.healthStatus} />,
  },
  {
    key: "source",
    label: "Source",
    render: (app) => (
      <span class="text-xs text-text-secondary truncate block max-w-[200px]">
        {app.source.chartName
          ? app.source.chartName
          : app.source.repoURL ?? "-"}
      </span>
    ),
  },
  {
    key: "revision",
    label: "Revision",
    class: "w-[90px]",
    render: (app) => (
      <span class="font-mono text-xs text-text-secondary">
        {app.currentRevision ? app.currentRevision.slice(0, 7) : "-"}
      </span>
    ),
  },
  {
    key: "destns",
    label: "Dest Namespace",
    class: "w-[120px]",
    render: (app) => (
      <span class="text-xs text-text-secondary">
        {app.destinationNamespace || "-"}
      </span>
    ),
  },
  {
    key: "resources",
    label: "Resources",
    class: "w-[80px] text-right",
    render: (app) => (
      <span class="text-xs text-text-secondary">
        {app.managedResourceCount}
      </span>
    ),
  },
];

export default function GitOpsApplications() {
  const status = useSignal<GitOpsStatus | null>(null);
  const applications = useSignal<NormalizedApp[]>([]);
  const summary = useSignal<AppListResponse["summary"] | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const filterTool = useSignal<string>("all");
  const filterSync = useSignal<string>("all");
  const filterHealth = useSignal<string>("all");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const [statusRes, appsRes] = await Promise.all([
        apiGet<GitOpsStatus>("/v1/gitops/status"),
        apiGet<AppListResponse>("/v1/gitops/applications"),
      ]);
      status.value = statusRes.data;
      applications.value = Array.isArray(appsRes.data.applications)
        ? appsRes.data.applications
        : [];
      summary.value = appsRes.data.summary ?? null;
      error.value = null;
    } catch {
      error.value = "Failed to load GitOps data";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    ["gitops-applications", "applications", ""],
    ["gitops-kustomizations", "kustomizations", ""],
    ["gitops-helmreleases", "helmreleases", ""],
  ], 1000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const noEngine = status.value && !status.value.detected;

  const nsByNs = filterByNamespace(applications.value, selectedNamespace.value);
  const filtered = nsByNs.filter((app) => {
    if (filterTool.value !== "all" && app.tool !== filterTool.value) {
      return false;
    }
    if (filterSync.value !== "all" && app.syncStatus !== filterSync.value) {
      return false;
    }
    if (
      filterHealth.value !== "all" && app.healthStatus !== filterHealth.value
    ) {
      return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
      return (
        app.name.toLowerCase().includes(q) ||
        app.namespace.toLowerCase().includes(q) ||
        app.kind.toLowerCase().includes(q) ||
        (app.source.repoURL ?? "").toLowerCase().includes(q) ||
        (app.source.chartName ?? "").toLowerCase().includes(q)
      );
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
  );

  return (
    <div class="p-6">
      {/* Page header */}
      <div class="flex items-center justify-between mb-1">
        <div class="flex items-center gap-2">
          <h1 class="text-2xl font-bold text-text-primary">Applications</h1>
          {wsStatus.value === "connected" && (
            <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
              <span class="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
              Live
            </span>
          )}
        </div>
        {!loading.value && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>
      <p class="text-sm text-text-muted mb-6">
        {filtered.length} of {applications.value.length}{" "}
        applications &mdash; Argo CD &amp; Flux.
      </p>

      {/* Summary counts */}
      {summary.value && !noEngine && !loading.value && (
        <div class="mb-6 flex flex-wrap gap-3">
          <SummaryBadge
            label="Synced"
            count={summary.value.synced}
            color={SYNC_COLORS.synced}
          />
          <SummaryBadge
            label="Out of Sync"
            count={summary.value.outOfSync}
            color={SYNC_COLORS.outofsync}
          />
          <SummaryBadge
            label="Degraded"
            count={summary.value.degraded}
            color={HEALTH_COLORS.degraded}
          />
          <SummaryBadge
            label="Progressing"
            count={summary.value.progressing}
            color={SYNC_COLORS.progressing}
          />
          <SummaryBadge
            label="Suspended"
            count={summary.value.suspended}
            color="var(--text-muted)"
          />
        </div>
      )}

      {/* Tool status banner */}
      {status.value && !noEngine && (
        <div class="mb-6 rounded-lg border border-border-primary p-4 flex items-center gap-4 bg-bg-elevated">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-text-primary">
              Tools detected:
            </span>
            {(status.value.detected === "argocd" ||
              status.value.detected === "both") && <ToolBadge tool="argocd" />}
            {(status.value.detected === "fluxcd" ||
              status.value.detected === "both") && <ToolBadge tool="fluxcd" />}
          </div>
          <span class="text-xs text-text-muted ml-auto">
            Last checked: {new Date(status.value.lastChecked).toLocaleString()}
          </span>
        </div>
      )}

      {/* No engine state */}
      {noEngine && !loading.value && (
        <div class="mb-6 rounded-lg border border-border-primary p-6 text-center bg-bg-elevated">
          <p class="text-lg font-medium text-text-primary mb-2">
            No GitOps tool detected
          </p>
          <p class="text-sm text-text-muted mb-4">
            Install Argo CD or Flux to enable GitOps application management.
          </p>
          <div class="flex justify-center gap-4">
            <a
              href="https://argo-cd.readthedocs.io/en/stable/getting_started/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Argo CD &rarr;
            </a>
            <a
              href="https://fluxcd.io/docs/installation/"
              target="_blank"
              rel="noopener noreferrer"
              class="text-sm text-brand hover:underline"
            >
              Install Flux &rarr;
            </a>
          </div>
        </div>
      )}

      {/* Filters */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, repo..."
          />
        </div>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterTool.value}
          onChange={(e) => {
            filterTool.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
          }}
        >
          <option value="all">All Tools</option>
          <option value="argocd">Argo CD</option>
          <option value="fluxcd">Flux</option>
        </select>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterSync.value}
          onChange={(e) => {
            filterSync.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
          }}
        >
          <option value="all">All Sync</option>
          <option value="synced">Synced</option>
          <option value="outofsync">Out of Sync</option>
          <option value="progressing">Progressing</option>
          <option value="stalled">Stalled</option>
          <option value="failed">Failed</option>
        </select>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterHealth.value}
          onChange={(e) => {
            filterHealth.value = (e.target as HTMLSelectElement).value;
            page.value = 1;
          }}
        >
          <option value="all">All Health</option>
          <option value="healthy">Healthy</option>
          <option value="degraded">Degraded</option>
          <option value="progressing">Progressing</option>
          <option value="suspended">Suspended</option>
        </select>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="rounded-lg border border-border-primary overflow-hidden">
          <DataTable
            columns={COLUMNS}
            data={displayed}
            rowKey={(app) => app.id}
            onRowClick={(app) => {
              globalThis.location.href = "/gitops/applications/" +
                encodeURIComponent(app.id);
            }}
          />
        </div>
      )}

      {/* Pagination */}
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div class="mt-4 flex items-center justify-between">
          <p class="text-sm text-text-muted">
            {filtered.length} applications &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div class="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value--;
              }}
              disabled={page.value <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                page.value++;
              }}
              disabled={page.value >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {!loading.value && !error.value && filtered.length === 0 &&
        !noEngine && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {applications.value.length === 0
              ? "No applications found. Applications will appear here once deployed via GitOps."
              : "No applications match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}

/** Inline colored count badge for the summary row. */
function SummaryBadge(
  { label, count, color }: { label: string; count: number; color: string },
) {
  return (
    <span
      class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      <span class="font-bold">{count}</span>
      {label}
    </span>
  );
}
