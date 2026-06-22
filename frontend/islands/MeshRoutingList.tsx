import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { meshApi } from "@/lib/mesh-api.ts";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { KindBadge, MeshBadge } from "@/components/ui/MeshBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
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

  // Read the global namespace picker in the synchronous render path so the
  // signal subscription triggers a re-render when the picker changes.
  const globalNs = selectedNamespace.value;

  const filtered = filterByNamespace(routes.value, globalNs).filter((r) => {
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
    <div style={{ padding: "24px" }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "4px",
        }}
      >
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          Mesh Routing
        </h1>
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
      <p
        style={{
          fontSize: "13px",
          color: "var(--text-muted)",
          marginTop: "4px",
          marginBottom: "24px",
        }}
      >
        Traffic-routing resources across Istio and Linkerd.
      </p>

      {
        /* Partial-failure banners — non-dismissible. Severity-classified to
          match the plan and `MTLSPosture.tsx`: prometheus-cross-check /
          truncated render as warnings; pods / policies / per-CRD fetch
          failures (`istio/VirtualService`, etc.) render as errors. */
      }
      {!loading.value && errorKeys.length > 0 && (
        <div
          style={{
            marginBottom: "16px",
            display: "flex",
            flexDirection: "column",
            gap: "8px",
          }}
        >
          {errorKeys.map((key) => {
            const isWarn = WARN_KEYS.has(key);
            const color = isWarn ? "var(--warning)" : "var(--error)";
            return (
              <div
                key={key}
                style={{
                  borderRadius: "9px",
                  padding: "12px 16px",
                  fontSize: "13px",
                  display: "flex",
                  alignItems: "flex-start",
                  gap: "12px",
                  backgroundColor:
                    `color-mix(in srgb, ${color} 12%, transparent)`,
                  border:
                    `1px solid color-mix(in srgb, ${color} 30%, transparent)`,
                }}
              >
                <span style={{ color, fontWeight: 600, flexShrink: 0 }}>
                  {isWarn ? "Notice" : "Error"}
                </span>
                <span style={{ color: "var(--text-primary)" }}>
                  <span style={{ fontWeight: 600 }}>{key}:</span>{" "}
                  {errors.value[key]}
                </span>
              </div>
            );
          })}
        </div>
      )}

      {/* Summary count strip */}
      {!loading.value && !error.value && routes.value.length > 0 && (
        <div
          style={{
            marginBottom: "16px",
            display: "flex",
            flexWrap: "wrap",
            gap: "12px",
          }}
        >
          <SummaryChip label="Istio" count={istioCount} />
          <SummaryChip label="Linkerd" count={linkerdCount} />
          <SummaryChip label="Total" count={totalCount} />
        </div>
      )}

      {/* Filter row */}
      <div
        style={{
          marginBottom: "16px",
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "16px",
        }}
      >
        <div style={{ flex: 1, maxWidth: "320px" }}>
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
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            background: "var(--bg-surface)",
            padding: "6px 12px",
            fontSize: "13px",
            color: "var(--text-primary)",
            fontFamily: "inherit",
          }}
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
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            background: "var(--bg-surface)",
            padding: "6px 12px",
            fontSize: "13px",
            color: "var(--text-primary)",
            fontFamily: "inherit",
            width: "144px",
          }}
          placeholder="Namespace..."
          value={filterNamespace.value}
          onInput={(e) => {
            filterNamespace.value = (e.target as HTMLInputElement).value;
            page.value = 1;
          }}
        />
        <span style={{ fontSize: "11px", color: "var(--text-muted)" }}>
          {filtered.length} of {totalCount} routes
        </span>
      </div>

      {loading.value && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            padding: "48px 0",
          }}
        >
          <Spinner />
        </div>
      )}

      {error.value && (
        <p
          style={{ fontSize: "13px", color: "var(--error)", padding: "16px 0" }}
        >
          {error.value}
        </p>
      )}

      {/* Table */}
      {!loading.value && !error.value && filtered.length > 0 && (
        <div
          style={{
            borderRadius: "9px",
            border: "1px solid var(--border-primary)",
            overflowX: "auto",
          }}
        >
          <table
            style={{
              width: "100%",
              fontSize: "13px",
              borderCollapse: "collapse",
            }}
          >
            <thead>
              <tr
                style={{
                  borderBottom: "1px solid var(--border-primary)",
                  background: "var(--bg-surface)",
                }}
              >
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Name
                </th>
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Mesh
                </th>
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Kind
                </th>
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Namespace
                </th>
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Hosts
                </th>
                <th
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    textTransform: "uppercase",
                    letterSpacing: "0.07em",
                    color: "var(--text-muted)",
                    padding: "10px 12px",
                    textAlign: "left",
                  }}
                >
                  Destinations
                </th>
              </tr>
            </thead>
            <tbody>
              {displayed.map((r) => {
                const hosts = r.hosts ?? [];
                const hostsDisplay = hosts.length > 2
                  ? `${hosts.slice(0, 2).join(", ")} +${hosts.length - 2} more`
                  : hosts.join(", ") || "—";
                const destCount = (r.destinations ?? []).length;
                return (
                  <RoutingRow
                    key={r.id}
                    r={r}
                    hostsDisplay={hostsDisplay}
                    destCount={destCount}
                  />
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {!loading.value && !error.value && filtered.length > PAGE_SIZE && (
        <div
          style={{
            marginTop: "16px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <p
            style={{
              fontSize: "13px",
              color: "var(--text-muted)",
              fontVariantNumeric: "tabular-nums",
            }}
          >
            {filtered.length} routes &middot; Page {currentPage} of {totalPages}
          </p>
          <div style={{ display: "flex", gap: "8px" }}>
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
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "48px 24px" }}>
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>
              {routes.value.length === 0
                ? "No routes found. Routes will appear once a mesh is installed and resources exist."
                : "No routes match your filters."}
            </p>
          </div>
        </WidgetShell>
      )}
    </div>
  );
}

/** Individual routing row with hover state via mouse events. */
function RoutingRow(
  { r, hostsDisplay, destCount }: {
    r: TrafficRoute;
    hostsDisplay: string;
    destCount: number;
  },
) {
  const hovered = useSignal(false);

  return (
    <tr
      style={{
        borderBottom: "1px solid var(--border-primary)",
        cursor: "pointer",
        background: hovered.value
          ? "color-mix(in srgb, var(--accent) 5%, transparent)"
          : "transparent",
      }}
      onMouseEnter={() => {
        hovered.value = true;
      }}
      onMouseLeave={() => {
        hovered.value = false;
      }}
      onClick={() => {
        globalThis.location.href = "/networking/mesh/routing/" +
          encodeURIComponent(r.id);
      }}
    >
      <td style={{ padding: "10px 12px" }}>
        <div style={{ fontWeight: 500, color: "var(--text-primary)" }}>
          {r.name}
        </div>
      </td>
      <td style={{ padding: "10px 12px" }}>
        <MeshBadge mesh={r.mesh} />
      </td>
      <td style={{ padding: "10px 12px" }}>
        <KindBadge kind={r.kind} />
      </td>
      <td
        style={{
          padding: "10px 12px",
          color: "var(--text-secondary)",
          fontSize: "12px",
        }}
      >
        {r.namespace ?? "—"}
      </td>
      <td
        style={{
          padding: "10px 12px",
          color: "var(--text-secondary)",
          fontSize: "12px",
          maxWidth: "220px",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {hostsDisplay}
      </td>
      <td
        style={{
          padding: "10px 12px",
          color: "var(--text-secondary)",
          fontSize: "12px",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {destCount}
      </td>
    </tr>
  );
}

function SummaryChip({ label, count }: { label: string; count: number }) {
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        gap: "6px",
        padding: "6px 12px",
        borderRadius: "9px",
        border: "1px solid var(--border-primary)",
        background: "transparent",
        color: "var(--text-muted)",
        fontSize: "13px",
      }}
    >
      <span
        style={{
          color: "var(--text-primary)",
          fontWeight: 700,
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {count}
      </span>
      {label}
    </span>
  );
}
