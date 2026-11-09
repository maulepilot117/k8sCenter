/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/Button.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import StatusBadge from "@/components/ui/glass/StatusBadge.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { apiGet, apiPost } from "@/lib/api.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

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
  const [status, setStatus] = useState<MonitoringStatusData | null>(null);
  const [loading, setLoading] = useState(true);
  const [rescanning, setRescanning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isClient = useIsClient();

  function handleRescan() {
    setRescanning(true);
    apiPost<MonitoringStatusData>("/v1/monitoring/rediscover")
      .then((res) => {
        setStatus(res.data);
        setError(null);
      })
      .catch((err) => {
        setError(err.message ?? "Re-scan failed");
      })
      .finally(() => {
        setRescanning(false);
      });
  }

  useEffect(() => {
    setLoading(true);
    apiGet<MonitoringStatusData>("/v1/monitoring/status")
      .then((res) => {
        setStatus(res.data);
        setError(null);
      })
      .catch((err) => {
        setError(err.message ?? "Failed to fetch monitoring status");
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex items-center justify-center p-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error) {
    return <ErrorBanner message={error} />;
  }

  const s = status;
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
              label={s.prometheus.available ? "Running" : "Unavailable"}
              tone={s.prometheus.available ? "ok" : "crit"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
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
              label={s.grafana.available ? "Running" : "Unavailable"}
              tone={s.grafana.available ? "ok" : "crit"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
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
              label={
                s.dashboards.provisioned ? "Provisioned" : "Not provisioned"
              }
              tone={s.dashboards.provisioned ? "ok" : "neutral"}
            />
          }
          style={{ flex: "1 1 240px", minWidth: "200px" }}
        >
          <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
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
            disabled={rescanning}
          >
            {rescanning ? "Scanning..." : "Re-scan Cluster"}
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
