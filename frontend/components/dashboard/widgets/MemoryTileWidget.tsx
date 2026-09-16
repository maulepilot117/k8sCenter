import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardSummary,
  DashboardTrends,
} from "@/lib/dashboard/wire-types.ts";
import { lastDelta } from "@/lib/format.ts";

/**
 * Cluster memory utilisation. Follows CpuTileWidget exactly; only the label,
 * source field, series and spark colour differ.
 */
function MemoryTile() {
  const s = dashboardData.state<DashboardSummary>("dashboard-summary").data;
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;
  const pct = Math.round(s?.memory?.percentage ?? 0);

  return (
    <MetricTile
      label="Memory"
      value={`${pct}`}
      unit="%"
      delta={lastDelta(t?.memory)}
      sparkData={t?.memory}
      // var(--accent-secondary), matching the live call site. The plan says
      // var(--info); using it would have silently recoloured the tile.
      sparkColor="var(--accent-secondary)"
      href="/cluster/nodes"
    />
  );
}

registerWidget({
  id: "memory-tile",
  title: "Memory",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-summary", "dashboard-trends"],
  // The trend series only decorates this tile with a sparkline and a delta;
  // the headline number comes from the summary. Gating on trends would let a
  // slow or failed trend request blank a memory percentage the summary endpoint
  // already returned, which the pre-registry island never did.
  optionalSources: ["dashboard-trends"],
  minW: 2,
  minH: 2,
  defaultW: 3,
  defaultH: 3,
  modes: ["compact", "normal"],
  render: () => <MemoryTile />,
});
