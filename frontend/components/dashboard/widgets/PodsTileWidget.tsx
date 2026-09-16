import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardSummary,
  DashboardTrends,
} from "@/lib/dashboard/wire-types.ts";
import { lastDelta } from "@/lib/format.ts";

/**
 * Pod count, with the running count as its unit line.
 *
 * The unit is empty rather than "/0 running" when summary is absent, matching
 * the pre-registry tile: a hard zero would read as a real count.
 */
function PodsTile() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;
  const podCount = s?.pods.total ?? 0;

  return (
    <MetricTile
      label="Pods"
      value={String(podCount)}
      unit={s ? `/${s.pods.running} running` : ""}
      delta={lastDelta(t?.pods)}
      sparkData={t?.pods}
      sparkColor="var(--success)"
      href="/workloads/pods"
    />
  );
}

registerWidget({
  id: "pods-tile",
  title: "Pods",
  family: "workloads",
  scopes: ["overview"],
  sources: ["dashboard-summary", "dashboard-trends"],
  // The trend series only decorates this tile with a sparkline and a delta;
  // the headline number comes from the summary. Gating on trends would let a
  // slow or failed trend request blank a pod count the summary endpoint
  // already returned, which the pre-registry island never did.
  optionalSources: ["dashboard-trends"],
  minW: 2,
  minH: 2,
  defaultW: 3,
  defaultH: 3,
  modes: ["compact", "normal"],
  render: () => <PodsTile />,
});
