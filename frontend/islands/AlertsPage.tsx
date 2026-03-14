import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface AlertEvent {
  id: string;
  fingerprint: string;
  status: string;
  alertName: string;
  namespace: string;
  severity: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  startsAt: string;
  endsAt?: string;
  receivedAt: string;
  resolvedAt?: string;
}

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
      <div class="border-b border-slate-200 dark:border-slate-700">
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
                  ? "border-blue-500 text-blue-600 dark:text-blue-400"
                  : "border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400"
              }`}
            >
              {tab === "active" ? "Active" : "History"}
              {tab === "active" && activeAlerts.value.length > 0 && (
                <span class="ml-2 bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400 text-xs px-2 py-0.5 rounded-full">
                  {activeAlerts.value.length}
                </span>
              )}
            </button>
          ))}
        </nav>
      </div>

      {error.value && (
        <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 text-red-700 dark:text-red-400 rounded-lg p-4 text-sm">
          {error.value}
        </div>
      )}

      {loading.value
        ? (
          <div class="text-slate-500 dark:text-slate-400 text-sm py-8 text-center">
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
        <div class="text-center py-12 text-slate-500 dark:text-slate-400">
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
      <table class="min-w-full divide-y divide-slate-200 dark:divide-slate-700">
        <thead class="bg-slate-50 dark:bg-slate-800">
          <tr>
            <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
              Alert
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
              Severity
            </th>
            <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
              Namespace
            </th>
            {showResolvedColumn && (
              <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                Status
              </th>
            )}
            <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
              Started
            </th>
            {showResolvedColumn && (
              <th class="px-4 py-3 text-left text-xs font-medium text-slate-500 dark:text-slate-400 uppercase">
                Resolved
              </th>
            )}
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-200 dark:divide-slate-700">
          {alerts.map((alert) => (
            <>
              <tr
                key={alert.id}
                class="hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer"
                onClick={() => onToggle(alert.id)}
              >
                <td class="px-4 py-3 text-sm font-medium text-slate-900 dark:text-white">
                  {alert.alertName}
                </td>
                <td class="px-4 py-3">
                  <StatusBadge
                    status={severityColor[alert.severity] ?? "default"}
                  >
                    {alert.severity || "unknown"}
                  </StatusBadge>
                </td>
                <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
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
                <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
                  {formatTime(alert.startsAt)}
                </td>
                {showResolvedColumn && (
                  <td class="px-4 py-3 text-sm text-slate-600 dark:text-slate-300">
                    {alert.resolvedAt ? formatTime(alert.resolvedAt) : "-"}
                  </td>
                )}
              </tr>
              {expandedRow === alert.id && (
                <tr key={`${alert.id}-detail`}>
                  <td
                    colSpan={showResolvedColumn ? 6 : 4}
                    class="px-4 py-3 bg-slate-50 dark:bg-slate-800/30"
                  >
                    <div class="space-y-2 text-sm">
                      {alert.annotations?.summary && (
                        <p>
                          <span class="font-medium text-slate-700 dark:text-slate-300">
                            Summary:
                          </span>{" "}
                          {alert.annotations.summary}
                        </p>
                      )}
                      {alert.annotations?.description && (
                        <p>
                          <span class="font-medium text-slate-700 dark:text-slate-300">
                            Description:
                          </span>{" "}
                          {alert.annotations.description}
                        </p>
                      )}
                      <div class="flex flex-wrap gap-1 mt-2">
                        {Object.entries(alert.labels).map(([k, v]) => (
                          <span
                            key={k}
                            class="inline-flex items-center px-2 py-0.5 rounded text-xs bg-slate-200 dark:bg-slate-700 text-slate-700 dark:text-slate-300"
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
