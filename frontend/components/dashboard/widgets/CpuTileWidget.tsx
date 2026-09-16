import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardSummary,
  DashboardTrends,
} from "@/lib/dashboard/wire-types.ts";
import { lastDelta } from "@/lib/format.ts";

/**
 * Cluster CPU utilisation, as a headline percentage with a sparkline.
 *
 * Lifted verbatim from the pre-registry DashboardV2 tile grid. MetricTile
 * already wraps itself in a WidgetShell, which is why no wrapper is added here
 * (spec D-9) and why the old tile grid was a bare div.
 */
function CpuTile() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;
  const pct = Math.round(s?.cpu?.percentage ?? 0);

  return (
    <MetricTile
      label="CPU"
      value={`${pct}`}
      unit="%"
      delta={lastDelta(t?.cpu)}
      sparkData={t?.cpu}
      sparkColor="var(--accent)"
      href="/cluster/nodes"
    />
  );
}

registerWidget({
  id: "cpu-tile",
  title: "CPU",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary", "dashboard-trends"],
  minW: 2,
  minH: 2,
  defaultW: 3,
  defaultH: 3,
  // A metric tile is a headline number and a sparkline at every size; there is
  // no larger rendering to give it, and inventing one would make the four
  // tiles inconsistent with each other.
  modes: ["compact", "normal"],
  render: () => <CpuTile />,
});
