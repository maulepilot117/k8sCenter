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
import { SYNC_COLORS } from "@/components/ui/GitOpsBadges.tsx";
import type { AppListMetadata, NormalizedAppSet } from "@/lib/gitops-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

interface AppSetListResponse {
  applicationSets: NormalizedAppSet[];
  summary: AppListMetadata;
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

const APPSET_COLUMNS: Column<NormalizedAppSet>[] = [
  {
    key: "name",
    label: "Name",
    class: "w-[2fr]",
    render: (as) => (
      <div class="flex items-center gap-2">
        <StatusDot
          status={dotStatus(statusTone(as.status))}
          size={8}
        />
        <span class="font-medium text-brand hover:underline">{as.name}</span>
      </div>
    ),
  },
  {
    key: "namespace",
    label: "Namespace",
    class: "w-[120px]",
    render: (as) => (
      <span class="text-text-secondary text-xs">{as.namespace}</span>
    ),
  },
  {
    key: "generators",
    label: "Generators",
    class: "w-[1fr]",
    render: (as) => (
      <div class="flex flex-wrap gap-1">
        {as.generatorTypes.map((g) => (
          <span
            key={g}
            class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-accent/10 text-accent"
          >
            {g}
          </span>
        ))}
      </div>
    ),
  },
  {
    key: "apps",
    label: "Apps",
    class: "w-[70px] text-right",
    render: (as) => (
      <span class="tabular-nums text-text-primary font-medium">
        {as.generatedAppCount}
      </span>
    ),
  },
  {
    key: "sync",
    label: "Sync",
    class: "w-[1fr]",
    render: (as) => {
      const syncedCount = as.summary.synced;
      const oosCount = as.summary.outOfSync;
      return (
        <span class="text-xs">
          {syncedCount > 0 && (
            <span style={{ color: SYNC_COLORS.synced }}>
              {syncedCount} synced
            </span>
          )}
          {syncedCount > 0 && oosCount > 0 && (
            <span class="text-text-muted">,</span>
          )}
          {oosCount > 0 && (
            <span style={{ color: SYNC_COLORS.outofsync }}>
              {oosCount} out-of-sync
            </span>
          )}
          {syncedCount === 0 && oosCount === 0 && (
            <span class="text-text-muted">-</span>
          )}
        </span>
      );
    },
  },
  {
    key: "health",
    label: "Health",
    class: "w-[1fr]",
    render: (as) => {
      if (as.summary.degraded > 0) {
        return (
          <span class="text-xs" style={{ color: "var(--error)" }}>
            {as.summary.degraded} degraded
          </span>
        );
      }
      if (as.summary.progressing > 0) {
        return (
          <span class="text-xs" style={{ color: "var(--warning)" }}>
            {as.summary.progressing} progressing
          </span>
        );
      }
      if (as.generatedAppCount > 0) {
        return (
          <span class="text-xs" style={{ color: "var(--success)" }}>
            all healthy
          </span>
        );
      }
      return <span class="text-xs text-text-muted">-</span>;
    },
  },
  {
    key: "status",
    label: "Status",
    class: "w-[100px]",
    render: (as) => {
      const statusColor = STATUS_COLORS[as.status.toLowerCase()] ??
        "var(--text-secondary)";
      return <span style={{ color: statusColor }}>{as.status}</span>;
    },
  },
  {
    key: "age",
    label: "Age",
    class: "w-[80px]",
    render: (as) => (
      <span class="text-text-muted text-xs">
        {as.createdAt ? timeAgo(as.createdAt) : "-"}
      </span>
    ),
  },
];

export default function GitOpsAppSets() {
  const appSets = useSignal<NormalizedAppSet[]>([]);
  const summary = useSignal<AppListMetadata | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const res = await apiGet<AppSetListResponse>(
        "/v1/gitops/applicationsets",
      );
      appSets.value = Array.isArray(res.data.applicationSets)
        ? res.data.applicationSets
        : [];
      summary.value = res.data.summary ?? null;
      error.value = null;
    } catch {
      error.value = "Failed to load ApplicationSets";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    ["gitops-applicationsets", "applicationsets", ""],
  ], 1000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const filtered = appSets.value.filter((as) => {
    if (search.value) {
      const q = search.value.toLowerCase();
      return (
        as.name.toLowerCase().includes(q) ||
        as.namespace.toLowerCase().includes(q) ||
        as.generatorTypes.some((g) => g.toLowerCase().includes(q))
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
      <div class="flex items-center justify-between mb-1">
        <div class="flex items-center gap-2">
          <h1
            class="font-bold text-text-primary"
            style={{ fontSize: "24px", fontWeight: 700 }}
          >
            ApplicationSets
          </h1>
          {wsStatus.value === "connected" && (
            <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium text-success bg-success/10">
              <span class="w-1.5 h-1.5 rounded-full bg-success animate-pulse" />
              Live
            </span>
          )}
        </div>
        <div class="flex items-center gap-2">
          {!loading.value && (
            <>
              <a href="/tools/yaml-apply">
                <Button variant="primary">Create</Button>
              </a>
              <Button
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing.value}
              >
                {refreshing.value ? "Refreshing..." : "Refresh"}
              </Button>
            </>
          )}
        </div>
      </div>
      <p class="text-sm text-text-muted mb-6">
        Argo CD ApplicationSet generators and their managed applications.
      </p>

      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, generator..."
          />
        </div>
        <span class="text-xs text-text-muted">
          {filtered.length} of {appSets.value.length} applicationsets
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-error py-4">{error.value}</p>}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <DataTable
            columns={APPSET_COLUMNS}
            data={displayed}
            rowKey={(as) => as.id}
            onRowClick={(as) => {
              globalThis.location.href = "/gitops/applicationsets/" +
                encodeURIComponent(as.id);
            }}
          />
        </div>
      )}

      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div class="mt-4 flex items-center justify-between">
          <p class="text-sm text-text-muted">
            {filtered.length} applicationsets &middot; Page {page.value} of{" "}
            {totalPages}
          </p>
          <div class="flex gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                page.value--;
              }}
              disabled={page.value <= 1}
            >
              Previous
            </Button>
            <Button
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

      {!loading.value && !error.value && filtered.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {appSets.value.length === 0
              ? "No ApplicationSets found. ApplicationSets will appear here once created in Argo CD."
              : "No ApplicationSets match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}
