import Gauge from "@/components/charts/Gauge.tsx";
import { CheckItem, type CheckItemProps } from "@/components/ui/CheckItem.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import type { CoveredSummary } from "@/lib/dashboard/coverage.ts";
import {
  type CoverageSection,
  formatHealthScore,
  healthSignal,
  healthUnscoredReason,
  shouldRenderHealth,
  withheldReason,
} from "@/lib/dashboard/coverage.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import { healthStatusColor } from "@/lib/score-color.ts";

type CheckStatus = CheckItemProps["status"];

/**
 * Signals whose `skipped` still means the underlying list WAS read.
 *
 * `computeClusterHealth` skips workloads when nothing desires replicas and
 * pods when no pod is Running or Pending (`health.go`) -- the informer answered,
 * there was just nothing to score. A cluster whose every pod has Failed is
 * exactly that case, so hiding `pods.failed` behind a dash there would hide
 * the one number that matters. Alerts skip for the opposite reason (no
 * Alertmanager, or no permission to query it): nothing was observed.
 */
const SKIP_STILL_OBSERVED: ReadonlySet<string> = new Set(["workloads", "pods"]);

/**
 * A checklist row's value, or "—" when what backs it was not observed.
 *
 * Checked against both disclosures the summary carries: the section's coverage
 * row (remote) and the health signal that reads the same source (local). An
 * unobserved value is never "0" with a green dot -- that is a clean bill of
 * health for something nobody looked at. A value that could not be read takes
 * `warning` ("look here"); one the backend deliberately skipped -- nothing to
 * evaluate, such as alerting with no Alertmanager -- takes `neutral`, because
 * there is nothing to look at.
 */
function checkValue(
  s: CoveredSummary | null,
  section: CoverageSection,
  signal: string,
  observed: { value: string; status: CheckStatus },
): { value: string; status: CheckStatus; reason?: string } {
  const withheld = withheldReason(s, section);
  if (withheld !== null) {
    return { value: "—", status: "warning", reason: withheld };
  }
  const sig = healthSignal(s, signal);
  if (sig?.status === "unknown") {
    return { value: "—", status: "warning", reason: sig.reason };
  }
  if (sig?.status === "skipped" && !SKIP_STILL_OBSERVED.has(signal)) {
    return { value: "—", status: "neutral", reason: sig.reason };
  }
  return observed;
}

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
  const unscoredReason = healthUnscoredReason(s);

  // `pods.failed` comes from the same pod list the `pods` signal reads
  // (`aggregateCounts` / `handleDashboardSummary`), so that is the signal
  // that vouches for it -- not `workloads`, which reads Deployments et al.
  const nodesCheck = checkValue(s, "nodes", "nodes", {
    value: `${nodesReady} / ${nodeCount}`,
    status: nodesReady === nodeCount && nodeCount > 0 ? "success" : "warning",
  });
  const degradedCheck = checkValue(s, "pods", "pods", {
    value: String(workloadsDegraded),
    status: workloadsDegraded > 0 ? "warning" : "success",
  });
  const alertsCheck = checkValue(s, "alerts", "alerts", {
    value: String(criticalAlerts),
    status: criticalAlerts > 0 ? "error" : "success",
  });

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
            <div title={nodesCheck.reason}>
              <CheckItem
                label="Nodes ready"
                value={nodesCheck.value}
                status={nodesCheck.status}
              />
            </div>
            <div title={degradedCheck.reason}>
              <CheckItem
                label="Workloads degraded"
                value={degradedCheck.value}
                status={degradedCheck.status}
              />
            </div>
            <div title={alertsCheck.reason}>
              <CheckItem
                label="Critical alerts"
                value={alertsCheck.value}
                status={alertsCheck.status}
              />
            </div>
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
