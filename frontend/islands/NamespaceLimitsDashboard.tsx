import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { limitsApi } from "@/lib/api.ts";
import type {
  LimitsStatus,
  NamespaceLimits,
  NamespaceSummary,
  ThresholdStatus,
} from "@/lib/limits-types.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import BarRow from "@/components/charts/BarRow.tsx";

const PAGE_SIZE = 50;
const PANEL_WIDTH = 400;

/** Map ThresholdStatus → glass tone */
function thresholdTone(status: ThresholdStatus): Tone {
  if (status === "critical") return "crit";
  if (status === "warning") return "warn";
  return "ok";
}

/** Bar color token keyed to threshold status */
function barColor(status: ThresholdStatus): string {
  if (status === "critical") return "var(--error)";
  if (status === "warning") return "var(--warning)";
  return "var(--success)";
}

export default function NamespaceLimitsDashboard() {
  const status = useSignal<LimitsStatus | null>(null);
  const summaries = useSignal<NamespaceSummary[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const search = useSignal("");
  const filterStatus = useSignal<string>("all");
  const page = useSignal(1);
  const refreshing = useSignal(false);

  // Slide-out panel state
  const selectedNamespace = useSignal<string | null>(null);
  const detailData = useSignal<NamespaceLimits | null>(null);
  const detailLoading = useSignal(false);
  const detailError = useSignal<string | null>(null);

  async function fetchData() {
    try {
      const [statusRes, summariesRes] = await Promise.all([
        limitsApi.status(),
        limitsApi.list(),
      ]);
      status.value = statusRes.data;
      summaries.value = Array.isArray(summariesRes.data)
        ? summariesRes.data
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load namespace limits data";
    }
  }

  async function fetchDetail(namespace: string) {
    detailLoading.value = true;
    detailError.value = null;
    try {
      const res = await limitsApi.get(namespace);
      detailData.value = res.data;
    } catch {
      detailError.value = "Failed to load namespace details";
    } finally {
      detailLoading.value = false;
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  // Fetch detail when namespace is selected
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (selectedNamespace.value) {
      fetchDetail(selectedNamespace.value);
    } else {
      detailData.value = null;
    }
  }, [selectedNamespace.value]);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  // Compute counts
  const withQuota = summaries.value.filter((s) => s.hasQuota).length;
  const warningCount = summaries.value.filter(
    (s) => s.status === "warning",
  ).length;
  const criticalCount = summaries.value.filter(
    (s) => s.status === "critical",
  ).length;
  const noQuotaCount = summaries.value.filter((s) => !s.hasQuota).length;

  // Filter and search
  const filtered = summaries.value.filter((s) => {
    if (filterStatus.value === "warning" && s.status !== "warning") {
      return false;
    }
    if (filterStatus.value === "critical" && s.status !== "critical") {
      return false;
    }
    if (filterStatus.value === "no-quota" && s.hasQuota) return false;
    if (filterStatus.value === "ok" && s.status !== "ok") return false;
    if (search.value) {
      return s.namespace.toLowerCase().includes(search.value.toLowerCase());
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  if (page.value > totalPages) page.value = totalPages;
  const displayed = filtered.slice(
    (page.value - 1) * PAGE_SIZE,
    page.value * PAGE_SIZE,
  );

  // KPI tile data
  const kpiTiles = [
    { label: "With Quota", value: withQuota, color: "var(--accent)" },
    { label: "Warning", value: warningCount, color: "var(--warning)" },
    { label: "Critical", value: criticalCount, color: "var(--error)" },
    { label: "No Quota", value: noQuotaCount, color: "var(--text-muted)" },
  ];

  return (
    <div style={{ display: "flex", height: "100%" }}>
      {/* Main content */}
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "0",
          marginRight: selectedNamespace.value ? `${PANEL_WIDTH}px` : 0,
        }}
      >
        {/* Page header */}
        <div
          style={{
            display: "flex",
            alignItems: "flex-start",
            justifyContent: "space-between",
            marginBottom: "4px",
          }}
        >
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            Namespace Limits
          </h1>
          {!loading.value && (
            <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
              <a href="/config/namespace-limits/new">
                <Button type="button" variant="primary">
                  Create Limits
                </Button>
              </a>
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing.value}
              >
                {refreshing.value ? "Refreshing..." : "Refresh"}
              </Button>
            </div>
          )}
        </div>
        <p
          style={{
            margin: "4px 0 20px",
            fontSize: "13px",
            color: "var(--text-muted)",
          }}
        >
          ResourceQuota and LimitRange management for namespaces.
        </p>

        {loading.value && (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              padding: "48px 0",
            }}
          >
            <Spinner />
          </div>
        )}

        {error.value && <ErrorBanner message={error.value} />}

        {!loading.value && !error.value && (
          <>
            {/* KPI summary tiles — WidgetShell glass, symmetric 2×2 */}
            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                gap: "12px",
                marginBottom: "20px",
              }}
            >
              {kpiTiles.map((tile) => (
                <WidgetShell
                  key={tile.label}
                  padding={16}
                  style={{ flex: "1 1 140px", minWidth: "130px" }}
                >
                  <div
                    style={{
                      fontSize: "11px",
                      fontWeight: 600,
                      textTransform: "uppercase",
                      letterSpacing: "0.05em",
                      color: "var(--text-muted)",
                      marginBottom: "6px",
                    }}
                  >
                    {tile.label}
                  </div>
                  <div
                    style={{
                      fontSize: "24px",
                      fontWeight: 700,
                      fontFamily: "var(--font-mono)",
                      color: tile.color,
                      lineHeight: 1,
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {tile.value}
                  </div>
                </WidgetShell>
              ))}
            </div>

            {/* Filters */}
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "12px",
                marginBottom: "12px",
              }}
            >
              <div style={{ maxWidth: "280px", flex: "1 1 auto" }}>
                <SearchBar
                  value={search.value}
                  onInput={(v) => {
                    search.value = v;
                    page.value = 1;
                  }}
                  placeholder="Search namespaces..."
                />
              </div>
              <select
                style={{
                  padding: "7px 12px",
                  fontSize: "13px",
                  color: "var(--text-primary)",
                  background: "var(--bg-elevated)",
                  border: "1px solid var(--border-primary)",
                  borderRadius: "9px",
                  outline: "none",
                  cursor: "pointer",
                }}
                value={filterStatus.value}
                onChange={(e) => {
                  filterStatus.value = (e.target as HTMLSelectElement).value;
                  page.value = 1;
                }}
              >
                <option value="all">All Status</option>
                <option value="ok">OK</option>
                <option value="warning">Warning</option>
                <option value="critical">Critical</option>
                <option value="no-quota">No Quota</option>
              </select>
            </div>

            {/* Table — solid data surface (bg-surface, not glass) */}
            <div
              style={{
                background: "var(--bg-surface)",
                border: "1px solid var(--border-primary)",
                borderRadius: "16px",
                overflow: "hidden",
              }}
            >
              <table style={{ width: "100%", borderCollapse: "collapse" }}>
                <thead>
                  <tr
                    style={{
                      background: "var(--bg-elevated)",
                      borderBottom: "1px solid var(--border-primary)",
                    }}
                  >
                    {[
                      "Namespace",
                      "CPU",
                      "Memory",
                      "Highest %",
                      "Status",
                      "Quotas",
                      "LimitRanges",
                    ].map((col) => (
                      <th
                        key={col}
                        style={{
                          padding: "10px 16px",
                          textAlign: "left",
                          fontSize: "11px",
                          fontWeight: 600,
                          textTransform: "uppercase",
                          letterSpacing: "0.05em",
                          color: "var(--text-muted)",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {displayed.length === 0 && (
                    <tr>
                      <td
                        colSpan={7}
                        style={{
                          padding: "48px 16px",
                          textAlign: "center",
                          fontSize: "13px",
                          color: "var(--text-muted)",
                        }}
                      >
                        No namespaces found
                      </td>
                    </tr>
                  )}
                  {displayed.map((s) => (
                    <tr
                      key={s.namespace}
                      style={{
                        borderBottom: "1px solid var(--border-primary)",
                        cursor: "pointer",
                        background: selectedNamespace.value === s.namespace
                          ? "color-mix(in srgb, var(--accent) 6%, transparent)"
                          : "transparent",
                        transition: "background 120ms ease",
                      }}
                      onClick={() => {
                        selectedNamespace.value =
                          selectedNamespace.value === s.namespace
                            ? null
                            : s.namespace;
                      }}
                    >
                      <td
                        style={{
                          padding: "10px 16px",
                          fontSize: "13px",
                          fontWeight: 500,
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--accent)",
                        }}
                      >
                        {s.namespace}
                      </td>
                      <td style={{ padding: "10px 16px", width: "120px" }}>
                        {s.cpuUsedPercent !== undefined
                          ? (
                            <BarRow
                              label=""
                              value={s.cpuUsedPercent}
                              max={100}
                              suffix={`${s.cpuUsedPercent.toFixed(0)}%`}
                              color={barColor(s.status)}
                              labelWidth={0}
                            />
                          )
                          : (
                            <span
                              style={{
                                fontSize: "13px",
                                color: "var(--text-muted)",
                              }}
                            >
                              -
                            </span>
                          )}
                      </td>
                      <td style={{ padding: "10px 16px", width: "120px" }}>
                        {s.memoryUsedPercent !== undefined
                          ? (
                            <BarRow
                              label=""
                              value={s.memoryUsedPercent}
                              max={100}
                              suffix={`${s.memoryUsedPercent.toFixed(0)}%`}
                              color={barColor(s.status)}
                              labelWidth={0}
                            />
                          )
                          : (
                            <span
                              style={{
                                fontSize: "13px",
                                color: "var(--text-muted)",
                              }}
                            >
                              -
                            </span>
                          )}
                      </td>
                      <td
                        style={{
                          padding: "10px 16px",
                          fontSize: "13px",
                          fontFamily: "var(--font-mono, monospace)",
                          fontVariantNumeric: "tabular-nums",
                          color: "var(--text-primary)",
                        }}
                      >
                        {s.hasQuota
                          ? `${s.highestUtilization.toFixed(1)}%`
                          : "-"}
                      </td>
                      <td style={{ padding: "10px 16px" }}>
                        <StatusBadge
                          label={s.status}
                          tone={thresholdTone(s.status)}
                        />
                      </td>
                      <td
                        style={{
                          padding: "10px 16px",
                          fontSize: "13px",
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-muted)",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        {s.quotaCount}
                      </td>
                      <td
                        style={{
                          padding: "10px 16px",
                          fontSize: "13px",
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-muted)",
                          fontVariantNumeric: "tabular-nums",
                        }}
                      >
                        {s.limitRangeCount}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Pagination */}
            {totalPages > 1 && (
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  marginTop: "16px",
                }}
              >
                <span
                  style={{ fontSize: "13px", color: "var(--text-muted)" }}
                >
                  Page {page.value} of {totalPages} ({filtered.length} total)
                </span>
                <div style={{ display: "flex", gap: "8px" }}>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={page.value === 1}
                    onClick={() => page.value--}
                  >
                    Previous
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={page.value === totalPages}
                    onClick={() => page.value++}
                  >
                    Next
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {/* Slide-out detail panel — glass surface */}
      {selectedNamespace.value && (
        <div
          class="glass"
          style={{
            position: "fixed",
            right: 0,
            top: 0,
            height: "100%",
            width: `${PANEL_WIDTH}px`,
            overflowY: "auto",
            borderLeft: "1px solid var(--border-primary)",
            zIndex: 40,
          }}
        >
          {/* Panel header */}
          <div
            style={{
              position: "sticky",
              top: 0,
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "16px 20px",
              borderBottom: "1px solid var(--border-primary)",
              background: "inherit",
              backdropFilter: "inherit",
            }}
          >
            <div>
              <h2
                style={{
                  margin: 0,
                  fontSize: "17px",
                  fontWeight: 650,
                  color: "var(--text-primary)",
                  fontFamily: "var(--font-mono, monospace)",
                }}
              >
                {selectedNamespace.value}
              </h2>
              <p
                style={{
                  margin: "2px 0 0",
                  fontSize: "12px",
                  color: "var(--text-muted)",
                }}
              >
                Namespace Details
              </p>
            </div>
            <button
              type="button"
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                width: "28px",
                height: "28px",
                borderRadius: "8px",
                border: "1px solid var(--border-primary)",
                background: "transparent",
                color: "var(--text-muted)",
                cursor: "pointer",
              }}
              onClick={() => {
                selectedNamespace.value = null;
              }}
              aria-label="Close panel"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 20 20"
                fill="currentColor"
              >
                <path d="M6.28 5.22a.75.75 0 00-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 101.06 1.06L10 11.06l3.72 3.72a.75.75 0 101.06-1.06L11.06 10l3.72-3.72a.75.75 0 00-1.06-1.06L10 8.94 6.28 5.22z" />
              </svg>
            </button>
          </div>

          <div style={{ padding: "20px" }}>
            {detailLoading.value && (
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  padding: "32px 0",
                }}
              >
                <Spinner />
              </div>
            )}

            {detailError.value && <ErrorBanner message={detailError.value} />}

            {!detailLoading.value && detailData.value && (
              <>
                {/* Quotas section */}
                <div style={{ marginBottom: "24px" }}>
                  <h3
                    style={{
                      margin: "0 0 12px",
                      fontSize: "11px",
                      fontWeight: 600,
                      textTransform: "uppercase",
                      letterSpacing: "0.05em",
                      color: "var(--text-muted)",
                    }}
                  >
                    ResourceQuotas ({detailData.value.quotas.length})
                  </h3>
                  {detailData.value.quotas.length === 0
                    ? (
                      <p
                        style={{ fontSize: "13px", color: "var(--text-muted)" }}
                      >
                        No quotas defined
                      </p>
                    )
                    : (
                      <div
                        style={{
                          display: "flex",
                          flexDirection: "column",
                          gap: "12px",
                        }}
                      >
                        {detailData.value.quotas.map((quota) => (
                          <WidgetShell key={quota.name} padding={14}>
                            <div
                              style={{
                                display: "flex",
                                alignItems: "center",
                                justifyContent: "space-between",
                                marginBottom: "10px",
                              }}
                            >
                              <span
                                style={{
                                  fontSize: "13px",
                                  fontWeight: 500,
                                  color: "var(--text-primary)",
                                  fontFamily: "var(--font-mono, monospace)",
                                }}
                              >
                                {quota.name}
                              </span>
                              <span
                                style={{
                                  fontSize: "11px",
                                  color: "var(--text-muted)",
                                }}
                              >
                                {quota.warnThreshold}% /{" "}
                                {quota.criticalThreshold}%
                              </span>
                            </div>
                            <div
                              style={{
                                display: "flex",
                                flexDirection: "column",
                                gap: "4px",
                              }}
                            >
                              {Object.entries(quota.utilization).map(
                                ([resource, util]) => (
                                  <BarRow
                                    key={resource}
                                    label={`${resource}: ${util.used}/${util.hard}`}
                                    value={util.percentage}
                                    max={100}
                                    suffix={`${util.percentage.toFixed(0)}%`}
                                    color={barColor(util.status)}
                                    labelWidth={120}
                                  />
                                ),
                              )}
                            </div>
                          </WidgetShell>
                        ))}
                      </div>
                    )}
                </div>

                {/* LimitRanges section */}
                <div>
                  <h3
                    style={{
                      margin: "0 0 12px",
                      fontSize: "11px",
                      fontWeight: 600,
                      textTransform: "uppercase",
                      letterSpacing: "0.05em",
                      color: "var(--text-muted)",
                    }}
                  >
                    LimitRanges ({detailData.value.limitRanges.length})
                  </h3>
                  {detailData.value.limitRanges.length === 0
                    ? (
                      <p
                        style={{ fontSize: "13px", color: "var(--text-muted)" }}
                      >
                        No limit ranges defined
                      </p>
                    )
                    : (
                      <div
                        style={{
                          display: "flex",
                          flexDirection: "column",
                          gap: "12px",
                        }}
                      >
                        {detailData.value.limitRanges.map((lr) => (
                          <WidgetShell key={lr.name} padding={14}>
                            <div
                              style={{
                                fontSize: "13px",
                                fontWeight: 500,
                                color: "var(--text-primary)",
                                fontFamily: "var(--font-mono, monospace)",
                                marginBottom: "10px",
                              }}
                            >
                              {lr.name}
                            </div>
                            {lr.limits.map((limit, i) => (
                              <div key={i} style={{ marginBottom: "10px" }}>
                                <div
                                  style={{
                                    fontSize: "11px",
                                    fontWeight: 600,
                                    textTransform: "uppercase",
                                    letterSpacing: "0.05em",
                                    color: "var(--text-muted)",
                                    marginBottom: "6px",
                                  }}
                                >
                                  {limit.type}
                                </div>
                                <div
                                  style={{
                                    display: "grid",
                                    gridTemplateColumns: "1fr 1fr",
                                    gap: "8px",
                                    fontSize: "12px",
                                  }}
                                >
                                  {limit.default && (
                                    <div>
                                      <span
                                        style={{ color: "var(--text-muted)" }}
                                      >
                                        Default:
                                      </span>
                                      <div
                                        style={{
                                          color: "var(--text-primary)",
                                          fontFamily:
                                            "var(--font-mono, monospace)",
                                        }}
                                      >
                                        {Object.entries(limit.default)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.defaultRequest && (
                                    <div>
                                      <span
                                        style={{ color: "var(--text-muted)" }}
                                      >
                                        Request:
                                      </span>
                                      <div
                                        style={{
                                          color: "var(--text-primary)",
                                          fontFamily:
                                            "var(--font-mono, monospace)",
                                        }}
                                      >
                                        {Object.entries(limit.defaultRequest)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.max && (
                                    <div>
                                      <span
                                        style={{ color: "var(--text-muted)" }}
                                      >
                                        Max:
                                      </span>
                                      <div
                                        style={{
                                          color: "var(--text-primary)",
                                          fontFamily:
                                            "var(--font-mono, monospace)",
                                        }}
                                      >
                                        {Object.entries(limit.max)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                  {limit.min && (
                                    <div>
                                      <span
                                        style={{ color: "var(--text-muted)" }}
                                      >
                                        Min:
                                      </span>
                                      <div
                                        style={{
                                          color: "var(--text-primary)",
                                          fontFamily:
                                            "var(--font-mono, monospace)",
                                        }}
                                      >
                                        {Object.entries(limit.min)
                                          .map(([k, v]) => `${k}: ${v}`)
                                          .join(", ")}
                                      </div>
                                    </div>
                                  )}
                                </div>
                              </div>
                            ))}
                          </WidgetShell>
                        ))}
                      </div>
                    )}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
