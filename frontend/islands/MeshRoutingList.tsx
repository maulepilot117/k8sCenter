import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { meshApi } from "@/lib/mesh-api.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { KindBadge, MeshBadge } from "@/components/ui/MeshBadges.tsx";
import type { RoutingResponse, TrafficRoute } from "@/lib/mesh-types.ts";

const PAGE_SIZE = 100;

/** Warning-level error keys (metric / truncation issues). All other keys
 *  (`pods`, `policies`, per-CRD fetch failures like `istio/VirtualService`)
 *  render as error-level. Mirrors the classification in `MTLSPosture.tsx`. */
const WARN_KEYS = new Set(["prometheus-cross-check", "truncated"]);

export default function MeshRoutingList() {
  const routes = useSignal<TrafficRoute[]>([]);
  const errors = useSignal<Record<string, string>>({});
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const filterMesh = useSignal<"all" | "istio" | "linkerd">("all");
  const filterNamespace = useSignal("");
  const search = useSignal("");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      // Namespace filter is applied client-side as a substring match below.
      // Server-side `?namespace=` is exact-match only and would change UX;
      // upgrading to a hybrid (exact -> server, otherwise client) is tracked
      // for Phase D alongside the topology overlay.
      const res = await meshApi.routes();
      const data = res.data as RoutingResponse;
      routes.value = Array.isArray(data.routes) ? data.routes : [];
      errors.value = data.errors ?? {};
      error.value = null;
    } catch {
      error.value = "Failed to load mesh routing data";
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

  const filtered = routes.value.filter((r) => {
    if (filterMesh.value !== "all" && r.mesh !== filterMesh.value) return false;
    if (
      filterNamespace.value &&
      !(r.namespace ?? "").toLowerCase().includes(
        filterNamespace.value.toLowerCase(),
      )
    ) {
      return false;
    }
    if (search.value) {
      const q = search.value.toLowerCase();
      return r.name.toLowerCase().includes(q) ||
        (r.namespace ?? "").toLowerCase().includes(q) ||
        r.kind.toLowerCase().includes(q);
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  // Clamp on read instead of writing to the signal during render. Filter
  // changes already reset to page 1; this guard handles the post-refresh
  // case where data shrinks while page.value is still on a higher page.
  const currentPage = Math.min(page.value, totalPages);
  const displayed = filtered.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE,
  );

  const istioCount = routes.value.filter((r) => r.mesh === "istio").length;
  const linkerdCount = routes.value.filter((r) => r.mesh === "linkerd").length;
  const totalCount = routes.value.length;

  const errorKeys = Object.keys(errors.value);

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">Mesh Routing</h1>
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
        Traffic-routing resources across Istio and Linkerd.
      </p>

      {
        /* Partial-failure banners — non-dismissible. Severity-classified to
          match the plan and `MTLSPosture.tsx`: prometheus-cross-check /
          truncated render as warnings; pods / policies / per-CRD fetch
          failures (`istio/VirtualService`, etc.) render as errors. */
      }
      {!loading.value && errorKeys.length > 0 && (
        <div class="mb-4 flex flex-col gap-2">
          {errorKeys.map((key) => {
            const isWarn = WARN_KEYS.has(key);
            const color = isWarn ? "var(--warning)" : "var(--danger)";
            return (
              <div
                key={key}
                class="rounded-lg px-4 py-3 text-sm flex items-start gap-3"
                style={{
                  backgroundColor:
                    `color-mix(in srgb, ${color} 12%, transparent)`,
                  border:
                    `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
                }}
              >
                <span class="font-medium shrink-0" style={{ color }}>
                  {isWarn ? "Notice" : "Error"}
                </span>
                <span class="text-text-primary">
                  <span class="font-medium">{key}:</span> {errors.value[key]}
                </span>
              </div>
            );
          })}
        </div>
      )}

      {/* Summary count strip */}
      {!loading.value && !error.value && routes.value.length > 0 && (
        <div class="mb-4 flex flex-wrap gap-3">
          <SummaryChip label="Istio" count={istioCount} />
          <SummaryChip label="Linkerd" count={linkerdCount} />
          <SummaryChip label="Total" count={totalCount} />
        </div>
      )}

      {/* Filter row */}
      <div class="mb-4 flex flex-wrap items-center gap-4">
        <div class="flex-1 max-w-xs">
          <SearchBar
            value={search.value}
            onInput={(v) => {
              search.value = v;
              page.value = 1;
            }}
            placeholder="Filter by name, namespace, kind..."
          />
        </div>
        <select
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary"
          value={filterMesh.value}
          onChange={(e) => {
            filterMesh.value = (e.target as HTMLSelectElement).value as
              | "all"
              | "istio"
              | "linkerd";
            page.value = 1;
          }}
        >
          <option value="all">All Meshes</option>
          <option value="istio">Istio</option>
          <option value="linkerd">Linkerd</option>
        </select>
        <input
          type="text"
          class="rounded border border-border-primary px-2 py-1.5 text-sm bg-bg-base text-text-primary w-36"
          placeholder="Namespace..."
          value={filterNamespace.value}
          onInput={(e) => {
            filterNamespace.value = (e.target as HTMLInputElement).value;
            page.value = 1;
          }}
        />
        <span class="text-xs text-text-muted">
          {filtered.length} of {totalCount} routes
        </span>
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {/* Table */}
      {!loading.value && !error.value && filtered.length > 0 && (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-border-primary bg-surface">
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Name
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Mesh
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Kind
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Namespace
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Hosts
                </th>
                <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                  Destinations
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border-subtle">
              {displayed.map((r) => {
                const hosts = r.hosts ?? [];
                const hostsDisplay = hosts.length > 2
                  ? `${hosts.slice(0, 2).join(", ")} +${hosts.length - 2} more`
                  : hosts.join(", ") || "—";
                const destCount = (r.destinations ?? []).length;
                return (
                  <tr
                    key={r.id}
                    class="hover:bg-hover/30 cursor-pointer"
                    onClick={() => {
                      globalThis.location.href = "/networking/mesh/routing/" +
                        encodeURIComponent(r.id);
                    }}
                  >
                    <td class="px-3 py-2">
                      <div class="font-medium text-text-primary">{r.name}</div>
                    </td>
                    <td class="px-3 py-2">
                      <MeshBadge mesh={r.mesh} />
                    </td>
                    <td class="px-3 py-2">
                      <KindBadge kind={r.kind} />
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {r.namespace ?? "—"}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs max-w-[220px] truncate">
                      {hostsDisplay}
                    </td>
                    <td class="px-3 py-2 text-text-secondary text-xs">
                      {destCount}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div class="mt-4 flex items-center justify-between">
          <p class="text-sm text-text-muted">
            {filtered.length} routes &middot; Page {currentPage} of {totalPages}
          </p>
          <div class="flex gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                if (currentPage > 1) page.value = currentPage - 1;
              }}
              disabled={currentPage <= 1}
            >
              Previous
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                if (currentPage < totalPages) page.value = currentPage + 1;
              }}
              disabled={currentPage >= totalPages}
            >
              Next
            </Button>
          </div>
        </div>
      )}

      {/* Empty states */}
      {!loading.value && !error.value && filtered.length === 0 && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            {routes.value.length === 0
              ? "No routes found. Routes will appear once a mesh is installed and resources exist."
              : "No routes match your filters."}
          </p>
        </div>
      )}
    </div>
  );
}

function SummaryChip({ label, count }: { label: string; count: number }) {
  return (
    <span class="inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium text-text-secondary bg-bg-elevated border border-border-primary">
      <span class="font-bold text-text-primary">{count}</span>
      {label}
    </span>
  );
}
