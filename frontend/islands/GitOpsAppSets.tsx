import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { wsStatus } from "@/lib/ws.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
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
  error: "var(--danger)",
  progressing: "var(--warning)",
};

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
          <h1 class="text-2xl font-bold text-text-primary">ApplicationSets</h1>
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

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Generators
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Apps
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Sync
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Health
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Status
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Age
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((as) => {
                const statusColor = STATUS_COLORS[as.status.toLowerCase()] ??
                  "var(--text-secondary)";
                const syncedCount = as.summary.synced;
                const oosCount = as.summary.outOfSync;
                return (
                  <tr
                    key={as.id}
                    class="hover:bg-hover/30 cursor-pointer"
                    onClick={() => {
                      globalThis.location.href = "/gitops/applicationsets/" +
                        encodeURIComponent(as.id);
                    }}
                  >
                    <td class="px-3 py-2">
                      <div class="font-medium text-brand hover:underline">
                        {as.name}
                      </div>
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {as.namespace}
                    </td>
                    <td class="px-3 py-2">
                      <div class="flex flex-wrap gap-1">
                        {as.generatorTypes.map((g) => (
                          <span
                            key={g}
                            class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium bg-brand/10 text-brand"
                          >
                            {g}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td class="px-3 py-2 text-text-primary font-medium">
                      {as.generatedAppCount}
                    </td>
                    <td class="px-3 py-2 text-xs">
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
                    </td>
                    <td class="px-3 py-2 text-xs">
                      {as.summary.degraded > 0
                        ? (
                          <span style={{ color: "var(--danger)" }}>
                            {as.summary.degraded} degraded
                          </span>
                        )
                        : as.summary.progressing > 0
                        ? (
                          <span style={{ color: "var(--warning)" }}>
                            {as.summary.progressing} progressing
                          </span>
                        )
                        : as.generatedAppCount > 0
                        ? (
                          <span style={{ color: "var(--success)" }}>
                            all healthy
                          </span>
                        )
                        : <span class="text-text-muted">-</span>}
                    </td>
                    <td class="px-3 py-2">
                      <span style={{ color: statusColor }}>{as.status}</span>
                    </td>
                    <td class="px-3 py-2 text-text-muted text-xs">
                      {as.createdAt ? timeAgo(as.createdAt) : "-"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
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
