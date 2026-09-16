import Gauge from "@/components/charts/Gauge.tsx";
import { CheckItem } from "@/components/ui/CheckItem.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import type {
  ClusterInfoData,
  DashboardSummary,
} from "@/lib/dashboard/wire-types.ts";
import { healthStatusColor } from "@/lib/score-color.ts";

/**
 * Cluster health: a score gauge plus a three-item readiness checklist.
 *
 * Lifted from the pre-registry DashboardV2 card along with its derived
 * scalars. The positional flex wrapper the island put around it is gone --
 * placement belongs to the grid, not the widget.
 */
function ClusterHealth({ mode }: WidgetProps) {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;
  const info = dashboardData.state<ClusterInfoData>("cluster-info").data;

  const nodeCount = s?.nodes.total ?? info?.nodeCount ?? 0;
  const nodesReady = s?.nodes.ready ?? 0;
  // Workloads degraded: approximate from pods failed, as the island did.
  const workloadsDegraded = s?.pods.failed ?? 0;
  const criticalAlerts = s?.alerts.critical ?? 0;

  const health = s?.health;
  const healthScore = health?.score ?? 0;
  const healthStatus = health?.status ?? "unknown";
  const healthColor = healthStatusColor(healthStatus);
  const healthLabel =
    healthStatus === "unknown" ? "UNKNOWN" : healthStatus.toUpperCase();

  return (
    <WidgetShell title="Cluster Health">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "24px",
          flexWrap: "wrap",
        }}
      >
        {/* Gauge ring */}
        <div style={{ flexShrink: 0 }}>
          <Gauge
            value={healthScore}
            size={140}
            thickness={12}
            color={healthColor}
            label={`${healthScore}`}
            sublabel={healthLabel}
          />
        </div>

        {/* Checklist. Dropped in compact: the gauge alone is the headline, and
            three label/value rows do not fit a small box legibly. */}
        {mode !== "compact" && (
          <div style={{ flex: 1, minWidth: "160px" }}>
            <CheckItem
              label="Nodes ready"
              value={`${nodesReady} / ${nodeCount}`}
              status={
                nodesReady === nodeCount && nodeCount > 0
                  ? "success"
                  : "warning"
              }
            />
            <CheckItem
              label="Workloads degraded"
              value={workloadsDegraded > 0 ? String(workloadsDegraded) : "0"}
              status={workloadsDegraded > 0 ? "warning" : "success"}
            />
            <CheckItem
              label="Critical alerts"
              value={criticalAlerts > 0 ? String(criticalAlerts) : "0"}
              status={criticalAlerts > 0 ? "error" : "success"}
            />
          </div>
        )}
      </div>
    </WidgetShell>
  );
}

registerWidget({
  id: "cluster-health",
  title: "Cluster Health",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary", "cluster-info"],
  minW: 3,
  minH: 4,
  defaultW: 4,
  defaultH: 6,
  // compact drops the three CheckItems and shows the gauge alone; normal is
  // the current rendering. There is no third thing to show.
  modes: ["compact", "normal"],
  render: (props) => <ClusterHealth {...props} />,
});
