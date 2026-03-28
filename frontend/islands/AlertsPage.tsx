import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import type { AlertEvent } from "@/lib/k8s-types.ts";

const severityColor: Record<string, string> = {
  critical: "danger",
  warning: "warning",
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
    loading.value = true;
    const params = new URLSearchParams({ limit: "50" });
    if (continueToken.value) params.set("continue", continueToken.value);

    apiGet<{ items: AlertEvent[]; continue?: string }>(
      `/v1/alerts/history?${params}`,
    )
      .then((res) => {
        historyAlerts.value = res.data?.items ?? [];
        continueToken.value = res.data?.continue ?? null;
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch alert history";
      })
      .finally(() => {
        loading.value = false;
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
    <div class="space-y-4">
      {/* Tabs */}
      <div class="border-b border-border-primary">
        <nav class="-mb-px flex space-x-8">
          {(["active", "history"] as const).map((tab) => (
            <button
              type="button"
              key={tab}
              onClick={() => {
                activeTab.value = tab;
              }}
              class={`py-2 px-1 border-b-2 text-sm font-medium ${
                activeTab.value === tab
                  ? "border-accent text-accent"
                  : "border-transparent text-text-muted hover:text-text-secondary text-text-muted"
              }`}
            >
              {tab === "active" ? "Active" : "History"}
              {tab === "active" && activeAlerts.value.length > 0 && (
                <span class="ml-2 bg-danger-dim text-danger text-xs px-2 py-0.5 rounded-full">
                  {activeAlerts.value.length}
                </span>
              )}
            </button>
          ))}
        </nav>
      </div>

      {error.value && <ErrorBanner message={error.value} />}

      {loading.value
        ? (
          <div class="text-text-muted text-sm py-8 text-center">
            Loading...
          </div>
        )
        : activeTab.value === "active"
        ? (
          <AlertTable
            alerts={activeAlerts.value}
            expandedRow={expandedRow.value}
            onToggle={toggleExpand}
            showResolvedColumn={false}
            formatTime={formatTime}
          />
        )
        : (
          <div class="space-y-4">
            <AlertTable
              alerts={historyAlerts.value}
              expandedRow={expandedRow.value}
              onToggle={toggleExpand}
              showResolvedColumn
              formatTime={formatTime}
            />
            {continueToken.value && (
              <div class="flex justify-center">
                <Button variant="secondary" onClick={fetchHistory}>
                  Load More
                </Button>
              </div>
            )}
          </div>
        )}

      {!loading.value &&
        activeTab.value === "active" &&
        activeAlerts.value.length === 0 && (
        <div class="text-center py-12 text-text-muted">
          <p class="text-lg font-medium">No active alerts</p>
          <p class="text-sm mt-1">
            All clear — no alerts are currently firing.
          </p>
        </div>
      )}

      <div class="flex justify-end">
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
                    status={severityColor[alert.severity] ?? "default"}
                  >
                    {alert.severity || "unknown"}
                  </StatusBadge>
                </td>
                <td class="px-4 py-3 text-sm text-text-secondary">
                  {alert.namespace || "-"}
                </td>
                {showResolvedColumn && (
                  <td class="px-4 py-3">
                    <StatusBadge
                      status={alert.status === "firing" ? "danger" : "success"}
                    >
                      {alert.status}
                    </StatusBadge>
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
