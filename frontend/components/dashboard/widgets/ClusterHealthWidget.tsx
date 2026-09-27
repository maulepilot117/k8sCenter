import Gauge from "@/components/charts/Gauge.tsx";
import { CheckItem } from "@/components/ui/CheckItem.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import type { CoveredSummary } from "@/lib/dashboard/coverage.ts";
import {
  coverageFor,
  coverageMessage,
  formatHealthScore,
  shouldRenderHealth,
} from "@/lib/dashboard/coverage.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import { healthStatusColor } from "@/lib/score-color.ts";

/**
 * Cluster health: a score gauge plus a three-item readiness checklist.
 *
 * Lifted from the pre-registry DashboardV2 card along with its derived
 * scalars. The positional flex wrapper the island put around it is gone --
 * placement belongs to the grid, not the widget.
 */
function ClusterHealth({ mode }: WidgetProps) {
  const s = dashboardData.state<CoveredSummary>("dashboard-summary").data;

  // The island wrote `s?.nodes.total ?? info?.nodeCount ?? 0`, but that
  // cluster-info fallback cannot fire here: WidgetHost guarantees the summary
  // is non-null before render, and `0 ?? x` is `0` under nullish coalescing,
  // so the second operand is unreachable for every value nodes.total can take.
  const nodeCount = s?.nodes.total ?? 0;
  const nodesReady = s?.nodes.ready ?? 0;
  // Workloads degraded: approximate from pods failed, as the island did.
  const workloadsDegraded = s?.pods.failed ?? 0;
  const criticalAlerts = s?.alerts.critical ?? 0;

  const health = s?.health;
  // No score is drawn unless one exists AND its inputs were complete. The
  // backend answers `score: null` when no weighted signal resolved, and this
  // used to print that as a confident gauge at 0 -- the manufactured reading
  // AE3 forbids, inverted. See `shouldRenderHealth`.
  const scored = shouldRenderHealth(s);
  const healthStatus = health?.status ?? "unknown";
  const healthColor = healthStatusColor(healthStatus);
  const healthLabel =
    healthStatus === "unknown" ? "UNKNOWN" : healthStatus.toUpperCase();
  const healthCoverage = coverageFor(s, "health");
  const unscoredReason = healthCoverage
    ? coverageMessage(healthCoverage)
    : "Too few health signals resolved to score this cluster.";

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
        {/* Gauge ring, or the reason there is none. Same footprint either
            way, so the checklist does not jump when a score arrives. */}
        <div style={{ flexShrink: 0 }}>
          {scored ? (
            <Gauge
              value={health?.score ?? 0}
              size={140}
              thickness={12}
              color={healthColor}
              label={formatHealthScore(s)}
              sublabel={healthLabel}
            />
          ) : (
            <div
              data-testid="health-unscored"
              title={unscoredReason}
              style={{
                width: "140px",
                height: "140px",
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                justifyContent: "center",
                gap: "4px",
                textAlign: "center",
                color: "var(--text-muted)",
              }}
            >
              <span
                style={{
                  fontSize: "30px",
                  fontWeight: 750,
                  color: "var(--text-primary)",
                }}
              >
                {formatHealthScore(s)}
              </span>
              <span
                style={{
                  fontSize: "11px",
                  fontWeight: 600,
                  textTransform: "uppercase",
                  letterSpacing: "0.05em",
                }}
              >
                Not scored
              </span>
              <span style={{ fontSize: "11px", lineHeight: 1.4 }}>
                {unscoredReason}
              </span>
            </div>
          )}
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
  // Summary only. Declaring cluster-info gated the card on an endpoint whose
  // value it never read -- handleClusterInfo 500s whenever the discovery call
  // is unreachable, while the summary keeps serving from the informer cache,
  // and the island drew the gauge right through that.
  sources: ["dashboard-summary"],
  minW: 3,
  minH: 4,
  defaultW: 4,
  defaultH: 6,
  // compact drops the three CheckItems and shows the gauge alone; normal is
  // the current rendering. There is no third thing to show.
  modes: ["compact", "normal"],
  render: (props) => <ClusterHealth {...props} />,
});
