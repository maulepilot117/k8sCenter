import { useEffect, useState } from "react";
import BarRow from "@/components/charts/BarRow.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { SearchBar } from "@/components/ui/SearchBar.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { limitsApi } from "@/lib/api.ts";
import type {
  LimitsStatus,
  NamespaceLimits,
  NamespaceSummary,
  ThresholdStatus,
} from "@/lib/limits-types.ts";
import NamespaceLimitsWizard from "@/src/islands/NamespaceLimitsWizard.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

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
  const isClient = useIsClient();
  const [, setStatus] = useState<LimitsStatus | null>(null);
  const [summaries, setSummaries] = useState<NamespaceSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [filterStatus, setFilterStatus] = useState("all");
  const [page, setPage] = useState(1);
  const [refreshing, setRefreshing] = useState(false);

  const [wizardOpen, setWizardOpen] = useState(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") === "create"
    ) {
      setWizardOpen(true);
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, []);

  // Slide-out panel state
  const [selectedNamespace, setSelectedNamespace] = useState<string | null>(
    null,
  );
  const [detailData, setDetailData] = useState<NamespaceLimits | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  async function fetchData() {
    try {
      const [statusRes, summariesRes] = await Promise.all([
        limitsApi.status(),
        limitsApi.list(),
      ]);
      setStatus(statusRes.data);
      setSummaries(Array.isArray(summariesRes.data) ? summariesRes.data : []);
      setError(null);
    } catch {
      setError("Failed to load namespace limits data");
    }
  }

  async function fetchDetail(namespace: string) {
    setDetailLoading(true);
    setDetailError(null);
    try {
      const res = await limitsApi.get(namespace);
      setDetailData(res.data);
    } catch {
      setDetailError("Failed to load namespace details");
    } finally {
      setDetailLoading(false);
    }
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: loads once on mount; `fetchData` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, []);

  // Fetch detail when namespace is selected
  // biome-ignore lint/correctness/useExhaustiveDependencies: refetches when the selection changes; `fetchDetail` is recreated every render
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (selectedNamespace) {
      fetchDetail(selectedNamespace);
    } else {
      setDetailData(null);
    }
  }, [selectedNamespace]);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  const filtered = summaries.filter((s) => {
    if (filterStatus === "warning" && s.status !== "warning") {
      return false;
    }
    if (filterStatus === "critical" && s.status !== "critical") {
      return false;
    }
    if (filterStatus === "no-quota" && s.hasQuota) return false;
    if (filterStatus === "ok" && s.status !== "ok") return false;
    if (search) {
      return s.namespace.toLowerCase().includes(search.toLowerCase());
    }
    return true;
  });

  const totalPages = Math.ceil(filtered.length / PAGE_SIZE) || 1;
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);

  if (!isClient) return null;

  // Compute counts
  const withQuota = summaries.filter((s) => s.hasQuota).length;
  const warningCount = summaries.filter((s) => s.status === "warning").length;
  const criticalCount = summaries.filter((s) => s.status === "critical").length;
  const noQuotaCount = summaries.filter((s) => !s.hasQuota).length;

  const currentPage = Math.min(page, totalPages);
  const displayed = filtered.slice(
    (currentPage - 1) * PAGE_SIZE,
    currentPage * PAGE_SIZE,
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
      {/* Floating wizard modal — rendered above everything when open */}
      {wizardOpen && (
        <NamespaceLimitsWizard onClose={() => setWizardOpen(false)} />
      )}

      {/* Main content */}
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "0",
          marginRight: selectedNamespace ? `${PANEL_WIDTH}px` : 0,
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
          {!loading && (
            <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
              <Button
                type="button"
                variant="primary"
                onClick={() => setWizardOpen(true)}
              >
                Create Limits
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={handleRefresh}
                disabled={refreshing}
              >
                {refreshing ? "Refreshing..." : "Refresh"}
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

        {loading && (
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

        {error && <ErrorBanner message={error} />}

        {!loading && !error && (
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
                  value={search}
                  onInput={(v) => {
                    setSearch(v);
                    setPage(1);
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
                value={filterStatus}
                onChange={(e) => {
                  setFilterStatus(e.currentTarget.value);
                  setPage(1);
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
                        background:
                          selectedNamespace === s.namespace
                            ? "color-mix(in srgb, var(--accent) 6%, transparent)"
                            : "transparent",
                        transition: "background 120ms ease",
                      }}
                      onClick={() => {
                        setSelectedNamespace(
                          selectedNamespace === s.namespace
                            ? null
                            : s.namespace,
                        );
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
                        {s.cpuUsedPercent !== undefined ? (
                          <BarRow
                            label={`${s.namespace} CPU`}
                            value={s.cpuUsedPercent}
                            max={100}
                            suffix={`${s.cpuUsedPercent.toFixed(0)}%`}
                            color={barColor(s.status)}
                            labelWidth={0}
                          />
                        ) : (
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
                        {s.memoryUsedPercent !== undefined ? (
                          <BarRow
                            label={`${s.namespace} memory`}
                            value={s.memoryUsedPercent}
                            max={100}
                            suffix={`${s.memoryUsedPercent.toFixed(0)}%`}
                            color={barColor(s.status)}
                            labelWidth={0}
                          />
                        ) : (
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
                <span style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                  Page {currentPage} of {totalPages} ({filtered.length} total)
                </span>
                <div style={{ display: "flex", gap: "8px" }}>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={currentPage === 1}
                    onClick={() => setPage(currentPage - 1)}
                  >
                    Previous
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    disabled={currentPage === totalPages}
                    onClick={() => setPage(currentPage + 1)}
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
      {selectedNamespace && (
        <div
          className="glass"
          style={{
            // Below the top bar, not under it: the bar (z-index 50) overlays
            // the content column's top strip, and at top: 0 it covered the
            // panel header and swallowed clicks on its close button.
            position: "fixed",
            right: 0,
            top: "var(--topbar-height, 56px)",
            height: "calc(100% - var(--topbar-height, 56px))",
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
                {selectedNamespace}
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
                setSelectedNamespace(null);
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
            {detailLoading && (
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

            {detailError && <ErrorBanner message={detailError} />}

            {!detailLoading && detailData && (
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
                    ResourceQuotas ({detailData.quotas.length})
                  </h3>
                  {detailData.quotas.length === 0 ? (
                    <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                      No quotas defined
                    </p>
                  ) : (
                    <div
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: "12px",
                      }}
                    >
                      {detailData.quotas.map((quota) => (
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
                              {quota.warnThreshold}% / {quota.criticalThreshold}
                              %
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
                    LimitRanges ({detailData.limitRanges.length})
                  </h3>
                  {detailData.limitRanges.length === 0 ? (
                    <p style={{ fontSize: "13px", color: "var(--text-muted)" }}>
                      No limit ranges defined
                    </p>
                  ) : (
                    <div
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: "12px",
                      }}
                    >
                      {detailData.limitRanges.map((lr) => (
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
                            // biome-ignore lint/suspicious/noArrayIndexKey: a LimitRange may hold several entries of one type and the list is never reordered
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
