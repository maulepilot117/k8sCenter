import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { apiGet } from "@/lib/api.ts";
import type { AlertEvent } from "@/lib/k8s-types.ts";
import { LOCAL_CLUSTER_ID, selectedCluster } from "@/src/lib/cluster.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

const severityTone: Record<string, Tone> = {
  critical: "crit",
  warning: "warn",
  info: "info",
};

export default function AlertsPage() {
  const [activeTab, setActiveTab] = useState<"active" | "history">("active");
  const [activeAlerts, setActiveAlerts] = useState<AlertEvent[]>([]);
  const [historyAlerts, setHistoryAlerts] = useState<AlertEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Read by fetchHistory, which the tab effect and Load More both call.
  const [continueToken, setContinueToken, continueTokenRef] = useStateRef<
    string | null
  >(null);
  const [expandedRow, setExpandedRow] = useState<string | null>(null);
  const selected = useSignalValue(selectedCluster);
  // The selection is restored from localStorage, which the server cannot
  // read, so the remote-cluster notice waits for hydration.
  const isClient = useIsClient();
  // AbortController for in-flight fetchHistory requests (Finding #28).
  const historyAbortRef = useRef<AbortController | null>(null);

  const fetchActive = useCallback(() => {
    apiGet<AlertEvent[]>("/v1/alerts")
      .then((res) => {
        setActiveAlerts(res.data ?? []);
        setError(null);
      })
      .catch((err) => {
        setError(err.message ?? "Failed to fetch alerts");
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  const fetchHistory = useCallback(() => {
    // Abort any in-flight request before issuing a new one.
    historyAbortRef.current?.abort();
    const controller = new AbortController();
    historyAbortRef.current = controller;

    setLoading(true);
    const params = new URLSearchParams({ limit: "50" });
    if (continueTokenRef.current) {
      params.set("continue", continueTokenRef.current);
    }

    apiGet<{ items: AlertEvent[]; continue?: string }>(
      `/v1/alerts/history?${params}`,
      controller.signal,
    )
      .then((res) => {
        // Guard: ignore stale response if a newer fetch has already started.
        if (controller.signal.aborted) return;
        setHistoryAlerts(res.data?.items ?? []);
        setContinueToken(res.data?.continue ?? null);
        setError(null);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        setError(err.message ?? "Failed to fetch alert history");
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      });
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchActive();
  }, [fetchActive]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    if (activeTab === "history") {
      fetchHistory();
    }
    // Abort any pending history fetch when the tab changes away.
    return () => {
      historyAbortRef.current?.abort();
    };
  }, [activeTab, fetchHistory]);

  function formatTime(ts: string): string {
    if (!ts) return "N/A";
    const d = new Date(ts);
    return d.toLocaleString();
  }

  function toggleExpand(id: string) {
    setExpandedRow(expandedRow === id ? null : id);
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* /alerts and /alerts/history always read the local Alertmanager,
          whatever cluster is selected (R-8 KTD13). Say so, or a quiet local
          feed reads as a quiet remote cluster. */}
      {isClient && selected !== LOCAL_CLUSTER_ID && (
        <Alert variant="info">
          These alerts come from the local cluster's Alertmanager, not the
          selected remote cluster. Alert rules on the remote are managed on the
          Alert Rules page.
        </Alert>
      )}
      {/* Glass chrome: tab nav + refresh action */}
      <GlassCard padding={0}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "0 20px",
            borderBottom: "1px solid var(--border-subtle)",
          }}
        >
          <nav style={{ display: "flex", gap: "0" }}>
            {(["active", "history"] as const).map((tab) => (
              <button
                type="button"
                key={tab}
                onClick={() => {
                  setActiveTab(tab);
                }}
                style={{
                  padding: "14px 16px",
                  fontSize: "13px",
                  fontWeight: activeTab === tab ? 600 : 400,
                  color:
                    activeTab === tab ? "var(--accent)" : "var(--text-muted)",
                  background: "none",
                  border: "none",
                  borderBottom:
                    activeTab === tab
                      ? "2px solid var(--accent)"
                      : "2px solid transparent",
                  cursor: "pointer",
                  display: "flex",
                  alignItems: "center",
                  gap: "6px",
                  marginBottom: "-1px",
                }}
              >
                {tab === "active" ? "Active" : "History"}
                {tab === "active" && activeAlerts.length > 0 && (
                  <span
                    style={{
                      background: "var(--error-dim)",
                      color: "var(--error)",
                      fontSize: "11px",
                      fontWeight: 600,
                      padding: "1px 7px",
                      borderRadius: "6px",
                    }}
                  >
                    {activeAlerts.length}
                  </span>
                )}
              </button>
            ))}
          </nav>
          <Button
            variant="secondary"
            onClick={() => {
              if (activeTab === "active") fetchActive();
              else fetchHistory();
            }}
          >
            Refresh
          </Button>
        </div>

        {/* Solid data surface — tables stay opaque */}
        <div style={{ padding: "0" }}>
          {error && (
            <div style={{ padding: "12px 20px" }}>
              <ErrorBanner message={error} />
            </div>
          )}

          {loading ? (
            <div
              style={{
                textAlign: "center",
                padding: "48px",
                fontSize: "13px",
                color: "var(--text-muted)",
              }}
            >
              Loading...
            </div>
          ) : activeTab === "active" ? (
            activeAlerts.length === 0 ? (
              <div
                style={{
                  textAlign: "center",
                  padding: "48px 20px",
                  color: "var(--text-muted)",
                }}
              >
                <p
                  style={{
                    fontSize: "15px",
                    fontWeight: 600,
                    color: "var(--text-primary)",
                    margin: "0 0 6px",
                  }}
                >
                  No active alerts
                </p>
                <p style={{ fontSize: "13px", margin: 0 }}>
                  All clear — no alerts are currently firing.
                </p>
              </div>
            ) : (
              <AlertTable
                alerts={activeAlerts}
                expandedRow={expandedRow}
                onToggle={toggleExpand}
                showResolvedColumn={false}
                formatTime={formatTime}
              />
            )
          ) : (
            <div>
              <AlertTable
                alerts={historyAlerts}
                expandedRow={expandedRow}
                onToggle={toggleExpand}
                showResolvedColumn
                formatTime={formatTime}
              />
              {continueToken && (
                <div
                  style={{
                    display: "flex",
                    justifyContent: "center",
                    padding: "16px",
                  }}
                >
                  <Button variant="secondary" onClick={fetchHistory}>
                    Load More
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>
      </GlassCard>
    </div>
  );
}

function AlertTable({
  alerts,
  expandedRow,
  onToggle,
  showResolvedColumn,
  formatTime,
}: {
  alerts: AlertEvent[];
  expandedRow: string | null;
  onToggle: (id: string) => void;
  showResolvedColumn: boolean;
  formatTime: (ts: string) => string;
}) {
  if (alerts.length === 0) return null;

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full divide-y divide-border-primary">
        <thead className="bg-surface">
          <tr>
            <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Alert
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Severity
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Namespace
            </th>
            {showResolvedColumn && (
              <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
                Status
              </th>
            )}
            <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Started
            </th>
            {showResolvedColumn && (
              <th className="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
                Resolved
              </th>
            )}
          </tr>
        </thead>
        <tbody className="divide-y divide-border-primary">
          {alerts.map((alert) => (
            <Fragment key={alert.id}>
              <tr
                className="hover:bg-hover/50 cursor-pointer"
                onClick={() => onToggle(alert.id)}
              >
                <td className="px-4 py-3 text-sm font-medium text-text-primary">
                  {alert.alertName}
                </td>
                <td className="px-4 py-3">
                  <StatusBadge
                    label={alert.severity || "unknown"}
                    tone={severityTone[alert.severity] ?? "neutral"}
                  />
                </td>
                <td className="px-4 py-3 text-sm text-text-secondary">
                  {alert.namespace || "-"}
                </td>
                {showResolvedColumn && (
                  <td className="px-4 py-3">
                    <StatusBadge
                      label={alert.status}
                      tone={alert.status === "firing" ? "crit" : "ok"}
                    />
                  </td>
                )}
                <td className="px-4 py-3 text-sm text-text-secondary">
                  {formatTime(alert.startsAt)}
                </td>
                {showResolvedColumn && (
                  <td className="px-4 py-3 text-sm text-text-secondary">
                    {alert.resolvedAt ? formatTime(alert.resolvedAt) : "-"}
                  </td>
                )}
              </tr>
              {expandedRow === alert.id && (
                <tr>
                  <td
                    colSpan={showResolvedColumn ? 6 : 4}
                    className="px-4 py-3 bg-surface/30"
                  >
                    <div className="space-y-2 text-sm">
                      {alert.annotations?.summary && (
                        <p>
                          <span className="font-medium text-text-secondary">
                            Summary:
                          </span>
                          {""}
                          {alert.annotations.summary}
                        </p>
                      )}
                      {alert.annotations?.description && (
                        <p>
                          <span className="font-medium text-text-secondary">
                            Description:
                          </span>
                          {""}
                          {alert.annotations.description}
                        </p>
                      )}
                      <div className="flex flex-wrap gap-1 mt-2">
                        {Object.entries(alert.labels).map(([k, v]) => (
                          <span
                            key={k}
                            className="inline-flex items-center px-2 py-0.5 rounded text-xs bg-elevated text-text-secondary"
                          >
                            {k}={v}
                          </span>
                        ))}
                      </div>
                    </div>
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
}
