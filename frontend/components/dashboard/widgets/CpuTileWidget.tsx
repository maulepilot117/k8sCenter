import { MetricTile } from "@/components/ui/MetricTile.tsx";
import { withheldReason } from "@/lib/dashboard/coverage.ts";
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
  // A local cluster without Prometheus reports no usage at all; the backend's
  // placeholder percentage of 0 must not read as an idle cluster.
  const observed = withheldReason(s, "cpu") === null;
  const pct = Math.round(s?.cpu?.percentage ?? 0);

  return (
    <MetricTile
      label="CPU"
      value={observed ? `${pct}` : "—"}
      unit={observed ? "%" : undefined}
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
  // The trend series only decorates this tile with a sparkline and a delta;
  // the headline number comes from the summary. Gating on trends would let a
  // slow or failed trend request blank a CPU percentage the summary endpoint
  // already returned, which the pre-registry island never did.
  optionalSources: ["dashboard-trends"],
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
