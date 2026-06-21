import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet, apiPost } from "@/lib/api.ts";
import { StatusBadge } from "@/components/ui/StatusBadge.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";

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
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <ErrorBanner message={error.value} />;
  }

  const s = status.value;
  if (!s) return null;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
      {/* Component cards — glass chrome widgets */}
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "16px",
        }}
      >
        {/* Prometheus */}
        <WidgetShell
          title="Prometheus"
          action={
            <StatusBadge
              status={s.prometheus.available ? "Running" : "Unavailable"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div
            style={{ display: "flex", flexDirection: "column", gap: "6px" }}
          >
            {s.prometheus.url && (
              <p
                style={{
                  fontSize: "13px",
                  color: "var(--text-muted)",
                  margin: 0,
                }}
              >
                <span style={{ fontWeight: 500 }}>URL:</span> {s.prometheus.url}
              </p>
            )}
            {s.prometheus.detectionMethod && (
              <p
                style={{
                  fontSize: "13px",
                  color: "var(--text-muted)",
                  margin: 0,
                }}
              >
                <span style={{ fontWeight: 500 }}>Detected via:</span>{" "}
                {s.prometheus.detectionMethod}
              </p>
            )}
            <p
              style={{
                fontSize: "12px",
                color: "var(--text-muted)",
                margin: 0,
              }}
            >
              Last checked: {s.prometheus.lastChecked}
            </p>
          </div>
        </WidgetShell>

        {/* Grafana */}
        <WidgetShell
          title="Grafana"
          action={
            <StatusBadge
              status={s.grafana.available ? "Running" : "Unavailable"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div
            style={{ display: "flex", flexDirection: "column", gap: "6px" }}
          >
            {s.grafana.url && (
              <p
                style={{
                  fontSize: "13px",
                  color: "var(--text-muted)",
                  margin: 0,
                }}
              >
                <span style={{ fontWeight: 500 }}>URL:</span> {s.grafana.url}
              </p>
            )}
            {s.grafana.detectionMethod && (
              <p
                style={{
                  fontSize: "13px",
                  color: "var(--text-muted)",
                  margin: 0,
                }}
              >
                <span style={{ fontWeight: 500 }}>Detected via:</span>{" "}
                {s.grafana.detectionMethod}
              </p>
            )}
            <p
              style={{
                fontSize: "12px",
                color: "var(--text-muted)",
                margin: 0,
              }}
            >
              Last checked: {s.grafana.lastChecked}
            </p>
          </div>
        </WidgetShell>

        {/* Dashboards */}
        <WidgetShell
          title="Dashboards"
          action={
            <StatusBadge
              status={s.dashboards.provisioned
                ? "Provisioned"
                : "Not provisioned"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div
            style={{ display: "flex", flexDirection: "column", gap: "6px" }}
          >
            <p
              style={{
                fontSize: "13px",
                color: "var(--text-muted)",
                margin: 0,
              }}
            >
              <span style={{ fontWeight: 500 }}>Count:</span>{" "}
              {s.dashboards.count}
            </p>
            {s.dashboards.error && (
              <p
                style={{
                  fontSize: "13px",
                  color: "var(--error)",
                  margin: 0,
                }}
              >
                {s.dashboards.error}
              </p>
            )}
          </div>
        </WidgetShell>
      </div>

      {/* Operator info + rescan action */}
      <WidgetShell
        title="Prometheus Operator"
        action={
          <Button
            variant="secondary"
            onClick={handleRescan}
            disabled={rescanning.value}
          >
            {rescanning.value ? "Scanning..." : "Re-scan Cluster"}
          </Button>
        }
      >
        <p
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            margin: 0,
          }}
        >
          {s.hasOperator
            ? "Detected (ServiceMonitor CRD found)"
            : "Not detected"}
        </p>
      </WidgetShell>
    </div>
  );
}
