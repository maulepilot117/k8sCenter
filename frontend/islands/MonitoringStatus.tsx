import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";

interface ComponentStatus {
  available: boolean;
  url?: string;
  detectionMethod?: string;
  lastChecked: string;
}

interface DashboardStatus {
  provisioned: boolean;
  count: number;
  error?: string;
}

interface MonitoringStatusData {
  prometheus: ComponentStatus;
  grafana: ComponentStatus;
  dashboards: DashboardStatus;
  hasOperator: boolean;
}

export default function MonitoringStatus() {
  const status = useSignal<MonitoringStatusData | null>(null);
  const loading = useSignal(true);
  const rescanning = useSignal(false);
  const error = useSignal<string | null>(null);

  function fetchStatus() {
    loading.value = true;
    apiGet<MonitoringStatusData>("/v1/monitoring/status")
      .then((res) => {
        status.value = res.data;
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Failed to fetch monitoring status";
      })
      .finally(() => {
        loading.value = false;
      });
  }

  function handleRescan() {
    rescanning.value = true;
    apiPost<MonitoringStatusData>("/v1/monitoring/rediscover")
      .then((res) => {
        status.value = res.data;
        error.value = null;
      })
      .catch((err) => {
        error.value = err.message ?? "Re-scan failed";
      })
      .finally(() => {
        rescanning.value = false;
      });
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchStatus();
  }, []);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex items-center justify-center p-12">
        <div class="h-6 w-6 animate-spin rounded-full border-2 border-border-primary border-t-brand" />
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="rounded-lg border border-danger bg-danger-dim p-4 text-sm text-danger">
        {error.value}
      </div>
    );
  }

  const s = status.value;
  if (!s) return null;

  return (
    <div class="space-y-6">
      {/* Header with rescan */}
      <div class="flex items-center justify-between">
        <h2 class="text-lg font-semibold text-text-primary">
          Monitoring Status
        </h2>
        <Button
          variant="secondary"
          onClick={handleRescan}
          disabled={rescanning.value}
        >
          {rescanning.value ? "Scanning..." : "Re-scan Cluster"}
        </Button>
      </div>

      {/* Component cards */}
      <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {/* Prometheus */}
        <div class="rounded-lg border border-border-primary bg-surface p-4">
          <div class="flex items-center justify-between">
            <h3 class="font-medium text-text-primary">
              Prometheus
            </h3>
            <StatusBadge
              status={s.prometheus.available ? "Running" : "Unavailable"}
            />
          </div>
          {s.prometheus.url && (
            <p class="mt-2 text-sm text-text-muted">
              <span class="font-medium">URL:</span> {s.prometheus.url}
            </p>
          )}
          {s.prometheus.detectionMethod && (
            <p class="mt-1 text-sm text-text-muted">
              <span class="font-medium">Detected via:</span>
              {""}
              {s.prometheus.detectionMethod}
            </p>
          )}
          <p class="mt-1 text-xs text-text-muted">
            Last checked: {s.prometheus.lastChecked}
          </p>
        </div>

        {/* Grafana */}
        <div class="rounded-lg border border-border-primary bg-surface p-4">
          <div class="flex items-center justify-between">
            <h3 class="font-medium text-text-primary">Grafana</h3>
            <StatusBadge
              status={s.grafana.available ? "Running" : "Unavailable"}
            />
          </div>
          {s.grafana.url && (
            <p class="mt-2 text-sm text-text-muted">
              <span class="font-medium">URL:</span> {s.grafana.url}
            </p>
          )}
          {s.grafana.detectionMethod && (
            <p class="mt-1 text-sm text-text-muted">
              <span class="font-medium">Detected via:</span>
              {""}
              {s.grafana.detectionMethod}
            </p>
          )}
          <p class="mt-1 text-xs text-text-muted">
            Last checked: {s.grafana.lastChecked}
          </p>
        </div>

        {/* Dashboards */}
        <div class="rounded-lg border border-border-primary bg-surface p-4">
          <div class="flex items-center justify-between">
            <h3 class="font-medium text-text-primary">
              Dashboards
            </h3>
            <StatusBadge
              status={s.dashboards.provisioned
                ? "Provisioned"
                : "Not provisioned"}
            />
          </div>
          <p class="mt-2 text-sm text-text-muted">
            <span class="font-medium">Count:</span> {s.dashboards.count}
          </p>
          {s.dashboards.error && (
            <p class="mt-1 text-sm text-red-500">{s.dashboards.error}</p>
          )}
        </div>
      </div>

      {/* Operator info */}
      <div class="rounded-lg border border-border-primary bg-surface p-4">
        <p class="text-sm text-text-secondary">
          <span class="font-medium">Prometheus Operator:</span>
          {""}
          {s.hasOperator
            ? "Detected (ServiceMonitor CRD found)"
            : "Not detected"}
        </p>
      </div>
    </div>
  );
}
