import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import type { Tone } from "@/components/ui/glass/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import type { AlertEvent } from "@/lib/k8s-types.ts";
import GlassCard from "@/components/ui/GlassCard.tsx";

const severityTone: Record<string, Tone> = {
  critical: "crit",
  warning: "warn",
  info: "info",
};

export default function AlertsPage() {
  const activeTab = useSignal<"active" | "history">("active");
  const activeAlerts = useSignal<AlertEvent[]>([]);
  const historyAlerts = useSignal<AlertEvent[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const continueToken = useSignal<string | null>(null);
  const expandedRow = useSignal<string | null>(null);
  // AbortController for in-flight fetchHistory requests (Finding #28).
  const historyAbortRef = useRef<AbortController | null>(null);

  function fetchActive() {
    apiGet<AlertEvent[]>("/v1/alerts")
      .then((res) => {
        activeAlerts.value = res.data ?? [];
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch alerts";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  function fetchHistory() {
    // Abort any in-flight request before issuing a new one.
    historyAbortRef.current?.abort();
    const controller = new AbortController();
    historyAbortRef.current = controller;

    loading.value = true;
    const params = new URLSearchParams({ limit: "50" });
    if (continueToken.value) params.set("continue", continueToken.value);

    apiGet<{ items: AlertEvent[]; continue?: string }>(
      `/v1/alerts/history?${params}`,
      controller.signal,
    )
      .then((res) => {
        // Guard: ignore stale response if a newer fetch has already started.
        if (controller.signal.aborted) return;
        historyAlerts.value = res.data?.items ?? [];
        continueToken.value = res.data?.continue ?? null;
        error.value = null;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        error.value = err.message ?? "Failed to fetch alert history";
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          loading.value = false;
        }
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchActive();
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    if (activeTab.value === "history") {
      fetchHistory();
    }
    // Abort any pending history fetch when the tab changes away.
    return () => {
      historyAbortRef.current?.abort();
    };
  }, [activeTab.value]);

  function formatTime(ts: string): string {
    if (!ts) return "N/A";
    const d = new Date(ts);
    return d.toLocaleString();
  }

  function toggleExpand(id: string) {
    expandedRow.value = expandedRow.value === id ? null : id;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
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
                  activeTab.value = tab;
                }}
                style={{
                  padding: "14px 16px",
                  fontSize: "13px",
                  fontWeight: activeTab.value === tab ? 600 : 400,
                  color: activeTab.value === tab
                    ? "var(--accent)"
                    : "var(--text-muted)",
                  background: "none",
                  border: "none",
                  borderBottom: activeTab.value === tab
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
                {tab === "active" && activeAlerts.value.length > 0 && (
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
                    {activeAlerts.value.length}
                  </span>
                )}
              </button>
            ))}
          </nav>
          <Button
            variant="secondary"
            onClick={() => {
              if (activeTab.value === "active") fetchActive();
              else fetchHistory();
            }}
          >
            Refresh
          </Button>
        </div>

        {/* Solid data surface — tables stay opaque */}
        <div style={{ padding: "0" }}>
          {error.value && (
            <div style={{ padding: "12px 20px" }}>
              <ErrorBanner message={error.value} />
            </div>
          )}

          {loading.value
            ? (
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
            )
            : activeTab.value === "active"
            ? (
              activeAlerts.value.length === 0
                ? (
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
                )
                : (
                  <AlertTable
                    alerts={activeAlerts.value}
                    expandedRow={expandedRow.value}
                    onToggle={toggleExpand}
                    showResolvedColumn={false}
                    formatTime={formatTime}
                  />
                )
            )
            : (
              <div>
                <AlertTable
                  alerts={historyAlerts.value}
                  expandedRow={expandedRow.value}
                  onToggle={toggleExpand}
                  showResolvedColumn
                  formatTime={formatTime}
                />
                {continueToken.value && (
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

function AlertTable(
  { alerts, expandedRow, onToggle, showResolvedColumn, formatTime }: {
    alerts: AlertEvent[];
    expandedRow: string | null;
    onToggle: (id: string) => void;
    showResolvedColumn: boolean;
    formatTime: (ts: string) => string;
  },
) {
  if (alerts.length === 0) return null;

  return (
    <div class="overflow-x-auto">
      <table class="min-w-full divide-y divide-border-primary">
        <thead class="bg-surface">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Alert
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Severity
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Namespace
            </th>
            {showResolvedColumn && (
              <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
                Status
              </th>
            )}
            <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
              Started
            </th>
            {showResolvedColumn && (
              <th class="px-4 py-3 text-left text-xs font-medium text-text-muted uppercase">
                Resolved
              </th>
            )}
          </tr>
        </thead>
        <tbody class="divide-y divide-border-primary">
          {alerts.map((alert) => (
            <>
              <tr
                key={alert.id}
                class="hover:bg-hover/50 cursor-pointer"
                onClick={() => onToggle(alert.id)}
              >
                <td class="px-4 py-3 text-sm font-medium text-text-primary">
                  {alert.alertName}
                </td>
                <td class="px-4 py-3">
                  <StatusBadge
                    label={alert.severity || "unknown"}
                    tone={severityTone[alert.severity] ?? "neutral"}
                  />
                </td>
                <td class="px-4 py-3 text-sm text-text-secondary">
                  {alert.namespace || "-"}
                </td>
                {showResolvedColumn && (
                  <td class="px-4 py-3">
                    <StatusBadge
                      label={alert.status}
                      tone={alert.status === "firing" ? "crit" : "ok"}
                    />
                  </td>
                )}
                <td class="px-4 py-3 text-sm text-text-secondary">
                  {formatTime(alert.startsAt)}
                </td>
                {showResolvedColumn && (
                  <td class="px-4 py-3 text-sm text-text-secondary">
                    {alert.resolvedAt ? formatTime(alert.resolvedAt) : "-"}
                  </td>
                )}
              </tr>
              {expandedRow === alert.id && (
                <tr key={`${alert.id}-detail`}>
                  <td
                    colSpan={showResolvedColumn ? 6 : 4}
                    class="px-4 py-3 bg-surface/30"
                  >
                    <div class="space-y-2 text-sm">
                      {alert.annotations?.summary && (
                        <p>
                          <span class="font-medium text-text-secondary">
                            Summary:
                          </span>
                          {""}
                          {alert.annotations.summary}
                        </p>
                      )}
                      {alert.annotations?.description && (
                        <p>
                          <span class="font-medium text-text-secondary">
                            Description:
                          </span>
                          {""}
                          {alert.annotations.description}
                        </p>
                      )}
                      <div class="flex flex-wrap gap-1 mt-2">
                        {Object.entries(alert.labels).map(([k, v]) => (
                          <span
                            key={k}
                            class="inline-flex items-center px-2 py-0.5 rounded text-xs bg-elevated text-text-secondary"
                          >
                            {k}={v}
                          </span>
                        ))}
                      </div>
                    </div>
                  </td>
                </tr>
              )}
            </>
          ))}
        </tbody>
      </table>
    </div>
  );
}
