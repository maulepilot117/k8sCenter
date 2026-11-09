/** @jsxImportSource react */
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { SYNC_COLORS } from "@/components/ui/GitOpsBadges.tsx";
import { LiveBadge } from "@/components/ui/LiveBadge.tsx";
import ResourceTable, { type Column } from "@/components/ui/ResourceTable.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { SourceCoverageNotice } from "@/components/ui/SourceCoverageNotice.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { apiGet } from "@/lib/api.ts";
import {
  type AppListMetadata,
  GITOPS_SOURCE_LABELS,
  type NormalizedAppSet,
} from "@/lib/gitops-types.ts";
import type { SourceCoverage } from "@/lib/k8s-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface AppSetListResponse {
  applicationSets: NormalizedAppSet[];
  summary: AppListMetadata;
  /** Each list a remote cluster could not provide. Absent on local. */
  coverage?: SourceCoverage[];
}

const PAGE_SIZE = 100;

const STATUS_COLORS: Record<string, string> = {
  healthy: "var(--success)",
  error: "var(--error)",
  progressing: "var(--warning)",
};

function statusTone(
  status: string,
): "ok" | "warn" | "crit" | "info" | "neutral" {
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

function dotStatus(
  tone: "ok" | "warn" | "crit" | "info" | "neutral",
): "success" | "warning" | "error" | "info" | "neutral" {
  switch (tone) {
    case "ok":
      return "success";
    case "warn":
      return "warning";
    case "crit":
      return "error";
    case "info":
      return "info";
    default:
      return "neutral";
  }
}

const RT_COLUMNS: Column[] = [
  { key: "name", label: "Name", width: "2fr" },
  { key: "namespace", label: "Namespace", width: "120px" },
  { key: "generators", label: "Generators" },
  { key: "apps", label: "Apps", width: "70px", align: "right" },
  { key: "sync", label: "Sync" },
  { key: "health", label: "Health" },
  { key: "status", label: "Status", width: "100px" },
  { key: "age", label: "Age", width: "80px" },
];

export default function GitOpsAppSets() {
  const isClient = useIsClient();
  const [appSets, setAppSets] = useState<NormalizedAppSet[]>([]);
  const [coverage, setCoverage] = useState<SourceCoverage[] | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const res = await apiGet<AppSetListResponse>(
        "/v1/gitops/applicationsets",
      );
      setAppSets(
        Array.isArray(res.data.applicationSets) ? res.data.applicationSets : [],
      );
      setCoverage(res.data.coverage);
      setError(null);
    } catch {
      setError("Failed to load ApplicationSets");
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
    [["gitops-applicationsets", "applicationsets", ""]],
    1000,
  );

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  const filtered = appSets.filter((as) => {
    if (search) {
      const q = search.toLowerCase();
      return (
        as.name.toLowerCase().includes(q) ||
        as.namespace.toLowerCase().includes(q) ||
        as.generatorTypes.some((g) => g.toLowerCase().includes(q))
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
      <div className="flex items-center justify-between mb-1">
        <div className="flex items-center gap-2">
          <h1
            className="font-bold text-text-primary"
            style={{ fontSize: "24px", fontWeight: 700 }}
          >
            ApplicationSets
          </h1>
          <LiveBadge />
        </div>
        <div className="flex items-center gap-2">
          {!loading && (
            <>
              <a href="/tools/yaml-apply">
                <Button variant="primary">Create</Button>
              </a>
              <Button
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing}
              >
                {refreshing ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>
      <p className="text-sm text-text-muted mb-6">
        Argo CD ApplicationSet generators and their managed applications.
      </p>

      {/* A remote cluster that could not list the child applications still
          lists the ApplicationSets; name the missing list so their app
          counts are not read as zero. */}
      {!loading && !error && (
        <SourceCoverageNotice
          coverage={coverage}
          labels={GITOPS_SOURCE_LABELS}
        />
      )}

      <div className="mb-4 flex flex-wrap items-center gap-4">
        <div className="flex-1 max-w-xs">
          <SearchBar
            value={search}
            onInput={(v) => {
              setSearch(v);
              setPage(1);
            }}
            placeholder="Filter by name, namespace, generator..."
          />
        </div>
        <span className="text-xs text-text-muted">
          {filtered.length} of {appSets.length} applicationsets
        </span>
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
          rows={displayed.map((as) => ({
            id: as.id,
            cells: {
              name: (
                <div className="flex items-center gap-2">
                  <StatusDot
                    status={dotStatus(statusTone(as.status))}
                    size={8}
                  />
                  <span className="font-medium text-brand hover:underline">
                    {as.name}
                  </span>
                </div>
              ),
              namespace: (
                <span className="text-text-secondary text-xs">
                  {as.namespace}
                </span>
              ),
              generators: (
                <div className="flex flex-wrap gap-1">
                  {as.generatorTypes.map((g) => (
                    <span
                      key={g}
                      className="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent"
                    >
                      {g}
                    </span>
                  ))}
                </div>
              ),
              apps: (
                <span className="tabular-nums text-text-primary font-medium">
                  {as.generatedAppCount}
                </span>
              ),
              sync: (() => {
                const syncedCount = as.summary.synced;
                const oosCount = as.summary.outOfSync;
                return (
                  <span className="text-xs">
                    {syncedCount > 0 && (
                      <span style={{ color: SYNC_COLORS.synced }}>
                        {syncedCount} synced
                      </span>
                    )}
                    {syncedCount > 0 && oosCount > 0 && (
                      <span className="text-text-muted">,</span>
                    )}
                    {oosCount > 0 && (
                      <span style={{ color: SYNC_COLORS.outofsync }}>
                        {oosCount} out-of-sync
                      </span>
                    )}
                    {syncedCount === 0 && oosCount === 0 && (
                      <span className="text-text-muted">-</span>
                    )}
                  </span>
                );
              })(),
              health: (() => {
                if (as.summary.degraded > 0) {
                  return (
                    <span className="text-xs" style={{ color: "var(--error)" }}>
                      {as.summary.degraded} degraded
                    </span>
                  );
                }
                if (as.summary.progressing > 0) {
                  return (
                    <span
                      className="text-xs"
                      style={{ color: "var(--warning)" }}
                    >
                      {as.summary.progressing} progressing
                    </span>
                  );
                }
                if (as.generatedAppCount > 0) {
                  return (
                    <span
                      className="text-xs"
                      style={{ color: "var(--success)" }}
                    >
                      all healthy
                    </span>
                  );
                }
                return <span className="text-xs text-text-muted">-</span>;
              })(),
              status: (() => {
                const statusColor =
                  STATUS_COLORS[as.status.toLowerCase()] ??
                  "var(--text-secondary)";
                return <span style={{ color: statusColor }}>{as.status}</span>;
              })(),
              age: (
                <span className="text-text-muted text-xs">
                  {as.createdAt ? timeAgo(as.createdAt) : "-"}
                </span>
              ),
            },
            onClick: () => {
              globalThis.location.href = `/gitops/applicationsets/${encodeURIComponent(as.id)}`;
            },
          }))}
        />
      )}

      {!loading && !error && filtered.length > PAGE_SIZE && (
        <div className="mt-4 flex items-center justify-between">
          <p className="text-sm text-text-muted">
            {filtered.length} applicationsets &middot; Page {currentPage} of{" "}
            {totalPages}
          </p>
          <div className="flex gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                setPage(currentPage - 1);
              }}
              disabled={currentPage <= 1}
            >
              Previous
            </Button>
            <Button
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

      {!loading && !error && filtered.length === 0 && (
        <div className="text-center py-12 rounded-lg border border-border-primary bg-elevated">
          <p className="text-text-muted">
            {appSets.length === 0
              ? "No ApplicationSets found. ApplicationSets will appear here once created in Argo CD."
              : "No ApplicationSets match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}
