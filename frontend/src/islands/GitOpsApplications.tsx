/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import {
  HEALTH_COLORS,
  HealthStatusBadge,
  SYNC_COLORS,
  SyncStatusBadge,
  ToolBadge,
} from "@/components/ui/GitOpsBadges.tsx";
import { LiveBadge } from "@/components/ui/LiveBadge.tsx";
import ResourceTable, { type Column } from "@/components/ui/ResourceTable.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { SourceCoverageNotice } from "@/components/ui/SourceCoverageNotice.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { apiGet } from "@/lib/api.ts";
import {
  type AppListResponse,
  GITOPS_SOURCE_LABELS,
  type GitOpsStatus,
  type HealthStatus,
  type NormalizedApp,
} from "@/lib/gitops-types.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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

const RT_COLUMNS: Column[] = [
  { key: "name", label: "Name" },
  { key: "tool", label: "Tool", width: "100px" },
  { key: "sync", label: "Sync", width: "120px" },
  { key: "health", label: "Health", width: "120px" },
  { key: "source", label: "Source" },
  { key: "revision", label: "Revision", width: "90px" },
  { key: "destns", label: "Dest Namespace", width: "130px" },
  { key: "resources", label: "Resources", width: "80px", align: "right" },
];

export default function GitOpsApplications() {
  const isClient = useIsClient();
  const namespace = useSignalValue(selectedNamespace);
  const [status, setStatus] = useState<GitOpsStatus | null>(null);
  const [applications, setApplications] = useState<NormalizedApp[]>([]);
  const [summary, setSummary] = useState<AppListResponse["summary"] | null>(
    null,
  );
  const [coverage, setCoverage] = useState<AppListResponse["coverage"]>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [filterTool, setFilterTool] = useState("all");
  const [filterSync, setFilterSync] = useState("all");
  const [filterHealth, setFilterHealth] = useState("all");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const [statusRes, appsRes] = await Promise.all([
        apiGet<GitOpsStatus>("/v1/gitops/status"),
        apiGet<AppListResponse>("/v1/gitops/applications"),
      ]);
      setStatus(statusRes.data);
      setApplications(
        Array.isArray(appsRes.data.applications)
          ? appsRes.data.applications
          : [],
      );
      setSummary(appsRes.data.summary ?? null);
      setCoverage(appsRes.data.coverage);
      setError(null);
    } catch {
      setError("Failed to load GitOps data");
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  useWsRefetch(
    fetchData,
    [
      ["gitops-applications", "applications", ""],
      ["gitops-kustomizations", "kustomizations", ""],
      ["gitops-helmreleases", "helmreleases", ""],
    ],
    1000,
  );

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  const noEngine = status && !status.detected;

  const nsByNs = filterByNamespace(applications, namespace);
  const filtered = nsByNs.filter((app) => {
    if (filterTool !== "all" && app.tool !== filterTool) {
      return false;
    }
    if (filterSync !== "all" && app.syncStatus !== filterSync) {
      return false;
    }
    if (filterHealth !== "all" && app.healthStatus !== filterHealth) {
      return false;
    }
    if (search) {
      const q = search.toLowerCase();
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
  const currentPage = Math.min(page, totalPages);
  // Keep the stored page in range as well, so a list that shrinks and grows
  // back stays on the clamped page instead of returning to the old one.
  if (page > totalPages) setPage(totalPages);
  const displayed = filtered.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE,
  );

  return (
    <div className="p-6">
      {/* Page header */}
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <h1 className="text-2xl font-bold text-text-primary">Applications</h1>
          <LiveBadge />
        </div>
        {!loading && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing}
          >
            {refreshing ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>
      <p className="text-sm text-text-muted mb-6">
        {filtered.length} of {applications.length} applications &mdash; Argo CD
        &amp; Flux.
      </p>

      {/* A remote list that could not load one source still lists the
          rest; name the missing source so it is not read as empty. */}
      {!loading && !error && (
        <SourceCoverageNotice
          coverage={coverage}
          labels={GITOPS_SOURCE_LABELS}
        />
      )}

      {/* Summary counts */}
      {summary && !noEngine && !loading && (
        <div className="mb-6 flex flex-wrap gap-3">
          <SummaryBadge
            label="Synced"
            count={summary.synced}
            color={SYNC_COLORS.synced}
          />
          <SummaryBadge
            label="Out of Sync"
            count={summary.outOfSync}
            color={SYNC_COLORS.outofsync}
          />
          <SummaryBadge
            label="Degraded"
            count={summary.degraded}
            color={HEALTH_COLORS.degraded}
          />
          <SummaryBadge
            label="Progressing"
            count={summary.progressing}
            color={SYNC_COLORS.progressing}
          />
          <SummaryBadge
            label="Suspended"
            count={summary.suspended}
            color="var(--text-muted)"
          />
        </div>
      )}

      {/* Tool status banner */}
      {status && !noEngine && (
        <div className="mb-6 rounded-lg border border-border-primary p-4 flex items-center gap-4 bg-elevated">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-text-primary">
              Tools detected:
            </span>
            {(status.detected === "argocd" || status.detected === "both") && (
              <ToolBadge tool="argocd" />
            )}
            {(status.detected === "fluxcd" || status.detected === "both") && (
              <ToolBadge tool="fluxcd" />
            )}
          </div>
          <span className="text-xs text-text-muted ml-auto">
            Last checked: {new Date(status.lastChecked).toLocaleString()}
          </span>
        </div>
      )}

      {/* No engine state */}
      {noEngine && !loading && (
        <div className="mb-6 rounded-lg border border-border-primary p-6 text-center bg-elevated">
          <p className="text-lg font-medium text-text-primary mb-2">
            No GitOps tool detected
          </p>
          <p className="text-sm text-text-muted mb-4">
            Install Argo CD or Flux to enable GitOps application management.
          </p>
          <div className="flex justify-center gap-4">
            <a
              href="https://argo-cd.readthedocs.io/en/stable/getting_started/"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-brand hover:underline"
            >
              Install Argo CD &rarr;
            </a>
            <a
              href="https://fluxcd.io/docs/installation/"
              target="_blank"
              rel="noopener noreferrer"
              className="text-sm text-brand hover:underline"
            >
              Install Flux &rarr;
            </a>
          </div>
        </div>
      )}

      {/* Filters */}
      <div className="mb-4 flex flex-wrap items-center gap-4">
        <div className="flex-1 max-w-xs">
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
              setPage(1);
            }}
            placeholder="Filter by name, namespace, repo..."
          />
        </div>
        <select
          className="rounded border border-border-primary px-2 py-1.5 text-sm bg-base text-text-primary"
          value={filterTool}
          onChange={(e) => {
            setFilterTool(e.currentTarget.value);
            setPage(1);
          }}
        >
          <option value="all">All Tools</option>
          <option value="argocd">Argo CD</option>
          <option value="fluxcd">Flux</option>
        </select>
        <select
          className="rounded border border-border-primary px-2 py-1.5 text-sm bg-base text-text-primary"
          value={filterSync}
          onChange={(e) => {
            setFilterSync(e.currentTarget.value);
            setPage(1);
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
          className="rounded border border-border-primary px-2 py-1.5 text-sm bg-base text-text-primary"
          value={filterHealth}
          onChange={(e) => {
            setFilterHealth(e.currentTarget.value);
            setPage(1);
          }}
        >
          <option value="all">All Health</option>
          <option value="healthy">Healthy</option>
          <option value="degraded">Degraded</option>
          <option value="progressing">Progressing</option>
          <option value="suspended">Suspended</option>
        </select>
      </div>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {error && (
        <p className="text-sm py-4" style={{ color: "var(--error)" }}>
          {error}
        </p>
      )}

      {!loading && !error && filtered.length > 0 && (
        <ResourceTable
          columns={RT_COLUMNS}
          rows={displayed.map((app) => ({
            id: app.id,
            cells: {
              name: (
                <div className="flex items-start gap-2">
                  <div className="mt-0.5 shrink-0">
                    <StatusDot
                      status={HEALTH_DOT_STATUS[app.healthStatus] ?? "neutral"}
                      size={8}
                    />
                  </div>
                  <div>
                    <div className="font-mono font-medium text-text-primary">
                      {app.name}
                    </div>
                    <div className="text-xs text-text-muted">
                      {app.namespace} &middot; {app.kind}
                    </div>
                  </div>
                </div>
              ),
              tool: <ToolBadge tool={app.tool} />,
              sync: <SyncStatusBadge status={app.syncStatus} />,
              health: <HealthStatusBadge status={app.healthStatus} />,
              source: (
                <span className="text-xs text-text-secondary truncate block max-w-[200px]">
                  {app.source.chartName
                    ? app.source.chartName
                    : (app.source.repoURL ?? "-")}
                </span>
              ),
              revision: (
                <span className="font-mono text-xs text-text-secondary">
                  {app.currentRevision ? app.currentRevision.slice(0, 7) : "-"}
                </span>
              ),
              destns: (
                <span className="text-xs text-text-secondary">
                  {app.destinationNamespace || "-"}
                </span>
              ),
              resources: (
                <span className="text-xs text-text-secondary">
                  {app.managedResourceCount}
                </span>
              ),
            },
            onClick: () => {
              globalThis.location.href = `/gitops/applications/${encodeURIComponent(app.id)}`;
            },
          }))}
        />
      )}

      {/* Pagination */}
      {!loading && !error && filtered.length > PAGE_SIZE && (
        <div className="mt-4 flex items-center justify-between">
          <p className="text-sm text-text-muted">
            {filtered.length} applications &middot; Page {currentPage} of{" "}
            {totalPages}
          </p>
          <div className="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage - 1);
              }}
              disabled={currentPage <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setPage(currentPage + 1);
              }}
              disabled={currentPage >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {!loading && !error && filtered.length === 0 && !noEngine && (
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted">
            {applications.length === 0
              ? "No applications found. Applications will appear here once deployed via GitOps."
              : "No applications match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}

/** Inline colored count badge for the summary row. */
function SummaryBadge({
  label,
  count,
  color,
}: {
  label: string;
  count: number;
  color: string;
}) {
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      <span className="font-bold">{count}</span>
      {label}
    </span>
  );
}
