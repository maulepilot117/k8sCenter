import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { apiGet } from "@/lib/api.ts";
import { filterByNamespace, selectedNamespace } from "@/lib/namespace.ts";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import type {
  GatewayAPIStatus,
  GatewayAPISummary,
  GatewayClassSummary,
  GatewayListData,
  GatewayResourceKind,
  GatewaySummary,
  HTTPRouteSummary,
  KindSummary,
  RouteSummary,
} from "@/lib/gateway-types.ts";

const KIND_LABELS: Record<GatewayResourceKind, string> = {
  gatewayclasses: "Gateway Classes",
  gateways: "Gateways",
  httproutes: "HTTP Routes",
  grpcroutes: "gRPC Routes",
  tcproutes: "TCP Routes",
  tlsroutes: "TLS Routes",
  udproutes: "UDP Routes",
};

const SUMMARY_KEYS: Record<GatewayResourceKind, keyof GatewayAPISummary> = {
  gatewayclasses: "gatewayClasses",
  gateways: "gateways",
  httproutes: "httpRoutes",
  grpcroutes: "grpcRoutes",
  tcproutes: "tcpRoutes",
  tlsroutes: "tlsRoutes",
  udproutes: "udpRoutes",
};

function formatAge(ageStr: string): string {
  if (!ageStr) return "—";
  const date = new Date(ageStr);
  if (isNaN(date.getTime())) {
    // Already a relative string like "2d" from the backend
    return ageStr;
  }
  const now = Date.now();
  const diffMs = now - date.getTime();
  if (diffMs < 0) return "0s";
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

function getAcceptedStatus(
  conditions?: { type: string; status: string }[],
): boolean {
  if (!conditions || conditions.length === 0) return false;
  const accepted = conditions.find(
    (c) => c.type === "Accepted" || c.type === "Programmed",
  );
  return accepted?.status === "True";
}

function GatewayStatusBadge(
  { conditions }: { conditions?: { type: string; status: string }[] },
) {
  const ok = getAcceptedStatus(conditions);
  return (
    <span
      style={{
        display: "inline-flex",
        alignItems: "center",
        borderRadius: "9999px",
        padding: "2px 8px",
        fontSize: "12px",
        fontWeight: 500,
        background: ok
          ? "color-mix(in srgb, var(--success) 15%, transparent)"
          : "color-mix(in srgb, var(--error) 15%, transparent)",
        color: ok ? "var(--success)" : "var(--error)",
      }}
    >
      {ok ? "Accepted" : "Not Ready"}
    </span>
  );
}

export default function GatewayAPIDashboard() {
  const status = useSignal<GatewayAPIStatus | null>(null);
  const summary = useSignal<GatewayAPISummary | null>(null);
  const loading = useSignal(true);
  const error = useSignal("");
  const activeKind = useSignal<GatewayResourceKind | null>(null);
  const listData = useSignal<GatewayListData | null>(null);
  const listLoading = useSignal(false);
  const search = useSignal("");

  async function fetchList(kind: GatewayResourceKind) {
    listLoading.value = true;
    try {
      let endpoint: string;
      switch (kind) {
        case "gatewayclasses":
          endpoint = "/v1/gateway/gatewayclasses";
          break;
        case "gateways":
          endpoint = "/v1/gateway/gateways";
          break;
        case "httproutes":
          endpoint = "/v1/gateway/httproutes";
          break;
        default:
          endpoint = `/v1/gateway/routes?kind=${kind}`;
          break;
      }
      const res = await apiGet<unknown[]>(endpoint);
      const items = Array.isArray(res.data) ? res.data : [];
      // deno-lint-ignore no-explicit-any
      listData.value = { kind, items } as any;
    } catch {
      listData.value = { kind, items: [] } as GatewayListData;
    }
    listLoading.value = false;
  }

  function handleCardClick(kind: GatewayResourceKind) {
    activeKind.value = kind;
    search.value = "";
    fetchList(kind);
  }

  // URL state sync
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (activeKind.value) {
      const url = new URL(globalThis.location.href);
      url.searchParams.set("kind", activeKind.value);
      globalThis.history.replaceState(null, "", url.toString());
    } else {
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("kind");
      globalThis.history.replaceState(null, "", url.toString());
    }
  }, [activeKind.value]);

  // Initial load
  useEffect(() => {
    if (!IS_BROWSER) return;
    const params = new URLSearchParams(globalThis.location.search);
    const kindParam = params.get("kind") as GatewayResourceKind | null;

    Promise.all([
      apiGet<GatewayAPIStatus>("/v1/gateway/status"),
      apiGet<GatewayAPISummary>("/v1/gateway/summary"),
    ]).then(([statusRes, summaryRes]) => {
      status.value = statusRes.data ?? null;
      summary.value = summaryRes.data ?? null;

      if (
        kindParam && status.value?.available &&
        Object.keys(KIND_LABELS).includes(kindParam)
      ) {
        activeKind.value = kindParam;
        fetchList(kindParam);
      }
      loading.value = false;
    }).catch(() => {
      error.value = "Failed to load Gateway API status";
      loading.value = false;
    });
  }, []);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div
        style={{ display: "flex", justifyContent: "center", padding: "48px 0" }}
      >
        <Spinner />
      </div>
    );
  }

  if (error.value) {
    return (
      <p style={{ fontSize: "14px", color: "var(--error)", padding: "16px 0" }}>
        {error.value}
      </p>
    );
  }

  if (!status.value?.available) {
    return (
      <WidgetShell style={{ textAlign: "center" }}>
        <h3
          style={{
            fontSize: "18px",
            fontWeight: 500,
            color: "var(--text-primary)",
            margin: "0 0 8px 0",
          }}
        >
          Gateway API Not Detected
        </h3>
        <p style={{ fontSize: "14px", color: "var(--text-muted)", margin: 0 }}>
          No Gateway API CRDs found in this cluster. Install Gateway API to
          manage Gateways, HTTPRoutes, and other resources.
        </p>
      </WidgetShell>
    );
  }

  // List mode
  if (activeKind.value) {
    return (
      <div>
        <button
          type="button"
          onClick={() => {
            activeKind.value = null;
            listData.value = null;
            search.value = "";
          }}
          style={{
            display: "inline-flex",
            alignItems: "center",
            gap: "4px",
            marginBottom: "16px",
            fontSize: "14px",
            color: "var(--text-muted)",
            background: "transparent",
            border: "none",
            cursor: "pointer",
            padding: 0,
            transition: "color 0.15s",
          }}
          onMouseEnter={(e) => {
            (e.currentTarget as HTMLButtonElement).style.color =
              "var(--text-primary)";
          }}
          onMouseLeave={(e) => {
            (e.currentTarget as HTMLButtonElement).style.color =
              "var(--text-muted)";
          }}
        >
          <svg
            style={{ width: "16px", height: "16px" }}
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M17 10a.75.75 0 01-.75.75H5.612l4.158 3.96a.75.75 0 11-1.04 1.08l-5.5-5.25a.75.75 0 010-1.08l5.5-5.25a.75.75 0 111.04 1.08L5.612 9.25H16.25A.75.75 0 0117 10z"
              clip-rule="evenodd"
            />
          </svg>
          Back to overview
        </button>

        <h2
          style={{
            fontSize: "20px",
            fontWeight: 600,
            color: "var(--text-primary)",
            margin: "0 0 16px 0",
          }}
        >
          {KIND_LABELS[activeKind.value]}
        </h2>

        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            alignItems: "center",
            gap: "16px",
            marginBottom: "16px",
          }}
        >
          <div style={{ flex: 1, maxWidth: "320px" }}>
            <SearchBar
              value={search.value}
              onInput={(v) => {
                search.value = v;
              }}
              placeholder="Filter by name or namespace..."
            />
          </div>
          {listData.value && (() => {
            const ns = selectedNamespace.value;
            const totalItems = listData.value.items.length;
            const visibleCount = activeKind.value === "gatewayclasses"
              ? totalItems
              : filterByNamespace(
                listData.value.items as { namespace?: string }[],
                ns,
              ).length;
            return (
              <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
                {ns !== "all"
                  ? `${visibleCount} of ${totalItems} total`
                  : `${totalItems} total`}
              </span>
            );
          })()}
        </div>

        {listLoading.value && (
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

        {!listLoading.value && listData.value &&
          renderTable(
            activeKind.value,
            listData.value,
            search.value,
            selectedNamespace.value,
          )}
      </div>
    );
  }

  // Overview mode
  const installedKinds = (status.value.installedKinds ?? []) as string[];
  const allKinds = Object.keys(KIND_LABELS) as GatewayResourceKind[];
  const visibleKinds = allKinds.filter((k) => installedKinds.includes(k));

  if (visibleKinds.length === 0) {
    return (
      <WidgetShell style={{ textAlign: "center" }}>
        <h3
          style={{
            fontSize: "18px",
            fontWeight: 500,
            color: "var(--text-primary)",
            margin: "0 0 8px 0",
          }}
        >
          No Gateway API Resources
        </h3>
        <p style={{ fontSize: "14px", color: "var(--text-muted)", margin: 0 }}>
          Gateway API CRDs are installed but no resource kinds were detected.
        </p>
      </WidgetShell>
    );
  }

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(200px, 1fr))",
        gap: "16px",
      }}
    >
      {visibleKinds.map((kind) => {
        const kindSummary: KindSummary = summary.value
          ? summary.value[SUMMARY_KEYS[kind]]
          : { total: 0, healthy: 0, degraded: 0 };
        return (
          <button
            type="button"
            key={kind}
            onClick={() => handleCardClick(kind)}
            style={{
              background: "transparent",
              border: "none",
              cursor: "pointer",
              width: "100%",
              padding: 0,
            }}
          >
            <WidgetShell
              title={KIND_LABELS[kind]}
              style={{ textAlign: "left" }}
            >
              <div
                style={{
                  fontSize: "24px",
                  fontWeight: 700,
                  color: "var(--text-primary)",
                  fontVariantNumeric: "tabular-nums",
                  lineHeight: 1.2,
                }}
              >
                {kindSummary.total}
              </div>
              <div
                style={{
                  marginTop: "6px",
                  fontSize: "12px",
                  display: "flex",
                  gap: "8px",
                  flexWrap: "wrap",
                }}
              >
                {kindSummary.healthy > 0 && (
                  <span style={{ color: "var(--success)" }}>
                    {kindSummary.healthy} healthy
                  </span>
                )}
                {kindSummary.degraded > 0 && (
                  <span style={{ color: "var(--error)" }}>
                    {kindSummary.degraded} degraded
                  </span>
                )}
                {kindSummary.healthy === 0 && kindSummary.degraded === 0 &&
                  kindSummary.total > 0 && (
                  <span style={{ color: "var(--text-muted)" }}>
                    {kindSummary.total} total
                  </span>
                )}
              </div>
            </WidgetShell>
          </button>
        );
      })}
    </div>
  );
}

function filterBySearch<
  T extends { name: string; namespace?: string },
>(items: T[], query: string): T[] {
  if (!query) return items;
  const q = query.toLowerCase();
  return items.filter(
    (item) =>
      item.name.toLowerCase().includes(q) ||
      (item.namespace && item.namespace.toLowerCase().includes(q)),
  );
}

function getDetailHref(
  kind: GatewayResourceKind,
  item: { name: string; namespace?: string },
): string {
  switch (kind) {
    case "gatewayclasses":
      return `/networking/gateway-api/gatewayclasses/${item.name}`;
    case "gateways":
      return `/networking/gateway-api/gateways/${item.namespace}/${item.name}`;
    case "httproutes":
      return `/networking/gateway-api/httproutes/${item.namespace}/${item.name}`;
    default:
      return `/networking/gateway-api/${kind}/${item.namespace}/${item.name}`;
  }
}

const thStyle: JSX.CSSProperties = {
  fontSize: "11px",
  fontWeight: 600,
  textTransform: "uppercase",
  letterSpacing: "0.07em",
  color: "var(--text-muted)",
  padding: "10px 12px",
  textAlign: "left",
};

const tdStyle: JSX.CSSProperties = {
  padding: "8px 12px",
  color: "var(--text-primary)",
  fontSize: "14px",
};

const tdSecStyle: JSX.CSSProperties = {
  padding: "8px 12px",
  color: "var(--text-secondary)",
  fontSize: "14px",
};

const tableWrapStyle: JSX.CSSProperties = {
  overflowX: "auto",
  borderRadius: "9px",
  border: "1px solid var(--border-primary)",
};

const theadRowStyle: JSX.CSSProperties = {
  borderBottom: "1px solid var(--border-primary)",
  background: "var(--surface)",
};

const tbodyRowStyle: JSX.CSSProperties = {
  borderBottom: "1px solid var(--border-subtle)",
  cursor: "pointer",
};

import type { JSX } from "preact";

function renderTable(
  kind: GatewayResourceKind,
  data: GatewayListData,
  query: string,
  ns: string,
) {
  switch (kind) {
    case "gatewayclasses":
      // Cluster-scoped — do NOT namespace-filter.
      return renderGatewayClassesTable(
        filterBySearch(
          data.items as GatewayClassSummary[],
          query,
        ),
      );
    case "gateways":
      return renderGatewaysTable(
        filterBySearch(
          filterByNamespace(data.items as GatewaySummary[], ns),
          query,
        ),
      );
    case "httproutes":
      return renderHTTPRoutesTable(
        filterBySearch(
          filterByNamespace(data.items as HTTPRouteSummary[], ns),
          query,
        ),
      );
    default:
      // grpcroutes, tcproutes, tlsroutes, udproutes — all namespaced.
      return renderRoutesTable(
        kind,
        filterBySearch(
          filterByNamespace(data.items as RouteSummary[], ns),
          query,
        ),
      );
  }
}

function EmptyTable({ message }: { message: string }) {
  return (
    <div
      style={{
        textAlign: "center",
        padding: "48px 0",
        borderRadius: "9px",
        border: "1px solid var(--border-primary)",
        background: "var(--bg-elevated)",
      }}
    >
      <p style={{ color: "var(--text-muted)", margin: 0 }}>{message}</p>
    </div>
  );
}

function renderGatewayClassesTable(items: GatewayClassSummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No gateway classes found." />;
  }
  return (
    <div style={tableWrapStyle}>
      <table
        style={{ width: "100%", fontSize: "14px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr style={theadRowStyle}>
            <th style={thStyle}>Name</th>
            <th style={thStyle}>Controller</th>
            <th style={thStyle}>Description</th>
            <th style={thStyle}>Status</th>
            <th style={thStyle}>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              key={item.name}
              style={tbodyRowStyle}
              onClick={() => {
                globalThis.location.href = getDetailHref(
                  "gatewayclasses",
                  item,
                );
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background =
                  "color-mix(in srgb, var(--hover) 30%, transparent)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background = "";
              }}
            >
              <td style={tdStyle}>
                <a
                  href={getDetailHref("gatewayclasses", item)}
                  style={{
                    fontWeight: 500,
                    color: "var(--brand)",
                    textDecoration: "none",
                  }}
                  onMouseEnter={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "underline";
                  }}
                  onMouseLeave={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "none";
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td style={tdSecStyle}>{item.controllerName}</td>
              <td
                style={{
                  ...tdSecStyle,
                  maxWidth: "200px",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {item.description || "—"}
              </td>
              <td style={tdStyle}>
                <GatewayStatusBadge conditions={item.conditions} />
              </td>
              <td style={tdSecStyle}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderGatewaysTable(items: GatewaySummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No gateways found." />;
  }
  return (
    <div style={tableWrapStyle}>
      <table
        style={{ width: "100%", fontSize: "14px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr style={theadRowStyle}>
            <th style={thStyle}>Name</th>
            <th style={thStyle}>Namespace</th>
            <th style={thStyle}>Class</th>
            <th style={thStyle}>Listeners</th>
            <th style={thStyle}>Addresses</th>
            <th style={thStyle}>Status</th>
            <th style={thStyle}>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              style={tbodyRowStyle}
              onClick={() => {
                globalThis.location.href = getDetailHref("gateways", item);
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background =
                  "color-mix(in srgb, var(--hover) 30%, transparent)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background = "";
              }}
            >
              <td style={tdStyle}>
                <a
                  href={getDetailHref("gateways", item)}
                  style={{
                    fontWeight: 500,
                    color: "var(--brand)",
                    textDecoration: "none",
                  }}
                  onMouseEnter={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "underline";
                  }}
                  onMouseLeave={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "none";
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td style={tdSecStyle}>{item.namespace}</td>
              <td style={tdSecStyle}>{item.gatewayClassName}</td>
              <td style={{ ...tdSecStyle, fontVariantNumeric: "tabular-nums" }}>
                {item.listeners?.length ?? 0}
              </td>
              <td
                style={{
                  ...tdSecStyle,
                  maxWidth: "200px",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  fontSize: "12px",
                }}
              >
                {(item.addresses ?? []).join(", ") || "—"}
              </td>
              <td style={tdStyle}>
                <GatewayStatusBadge conditions={item.conditions} />
              </td>
              <td style={tdSecStyle}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderHTTPRoutesTable(items: HTTPRouteSummary[]) {
  if (items.length === 0) {
    return <EmptyTable message="No HTTP routes found." />;
  }
  return (
    <div style={tableWrapStyle}>
      <table
        style={{ width: "100%", fontSize: "14px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr style={theadRowStyle}>
            <th style={thStyle}>Name</th>
            <th style={thStyle}>Namespace</th>
            <th style={thStyle}>Hostnames</th>
            <th style={thStyle}>Parents</th>
            <th style={thStyle}>Backends</th>
            <th style={thStyle}>Status</th>
            <th style={thStyle}>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              style={tbodyRowStyle}
              onClick={() => {
                globalThis.location.href = getDetailHref("httproutes", item);
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background =
                  "color-mix(in srgb, var(--hover) 30%, transparent)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background = "";
              }}
            >
              <td style={tdStyle}>
                <a
                  href={getDetailHref("httproutes", item)}
                  style={{
                    fontWeight: 500,
                    color: "var(--brand)",
                    textDecoration: "none",
                  }}
                  onMouseEnter={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "underline";
                  }}
                  onMouseLeave={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "none";
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td style={tdSecStyle}>{item.namespace}</td>
              <td
                style={{
                  ...tdSecStyle,
                  maxWidth: "200px",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  fontSize: "12px",
                }}
              >
                {(item.hostnames ?? []).join(", ") || "—"}
              </td>
              <td style={{ ...tdSecStyle, fontVariantNumeric: "tabular-nums" }}>
                {item.parentRefs?.length ?? 0}
              </td>
              <td style={{ ...tdSecStyle, fontVariantNumeric: "tabular-nums" }}>
                {item.backendCount ?? 0}
              </td>
              <td style={tdStyle}>
                <GatewayStatusBadge conditions={item.conditions} />
              </td>
              <td style={tdSecStyle}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function renderRoutesTable(
  kind: GatewayResourceKind,
  items: RouteSummary[],
) {
  if (items.length === 0) {
    return (
      <EmptyTable
        message={`No ${KIND_LABELS[kind]?.toLowerCase() ?? "routes"} found.`}
      />
    );
  }
  return (
    <div style={tableWrapStyle}>
      <table
        style={{ width: "100%", fontSize: "14px", borderCollapse: "collapse" }}
      >
        <thead>
          <tr style={theadRowStyle}>
            <th style={thStyle}>Name</th>
            <th style={thStyle}>Namespace</th>
            <th style={thStyle}>Parents</th>
            <th style={thStyle}>Status</th>
            <th style={thStyle}>Age</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              key={`${item.namespace}/${item.name}`}
              style={tbodyRowStyle}
              onClick={() => {
                globalThis.location.href = getDetailHref(kind, item);
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background =
                  "color-mix(in srgb, var(--hover) 30%, transparent)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLTableRowElement).style.background = "";
              }}
            >
              <td style={tdStyle}>
                <a
                  href={getDetailHref(kind, item)}
                  style={{
                    fontWeight: 500,
                    color: "var(--brand)",
                    textDecoration: "none",
                  }}
                  onMouseEnter={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "underline";
                  }}
                  onMouseLeave={(e) => {
                    (e.currentTarget as HTMLAnchorElement).style
                      .textDecoration = "none";
                  }}
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.name}
                </a>
              </td>
              <td style={tdSecStyle}>{item.namespace}</td>
              <td style={{ ...tdSecStyle, fontVariantNumeric: "tabular-nums" }}>
                {item.parentRefs?.length ?? 0}
              </td>
              <td style={tdStyle}>
                <GatewayStatusBadge conditions={item.conditions} />
              </td>
              <td style={tdSecStyle}>{formatAge(item.age)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
