import { NetworkTile } from "@/components/ui/NetworkTile.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import type { DashboardTrends } from "@/lib/dashboard/wire-types.ts";
import { percentile } from "@/lib/format.ts";

/** Fallback period label, matching the island's initial time-range tab. */
const DEFAULT_RANGE = "1h";

/**
 * Cluster network throughput: RX/TX p95 over the active window, derived from
 * the trend series so the value tracks whichever time-range tab is selected.
 *
 * `period` is only a label, and it comes in through `params.range` because a
 * widget has no access to the island's tab state.
 *
 * **Contract for the unit that renders this**: pass the range the displayed
 * trend data actually belongs to, not the tab the user just clicked. The
 * pre-registry island kept two separate signals for exactly this reason — they
 * diverge while a tab-switch fetch is in flight, and labelling the old data
 * with the new window states a window the data has not caught up to yet.
 */
function NetworkTileWidget({ params }: WidgetProps) {
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;

  return (
    <NetworkTile
      rxP95={percentile(t?.networkRx, 95)}
      txP95={percentile(t?.networkTx, 95)}
      rxData={t?.networkRx}
      txData={t?.networkTx}
      period={params.range ?? DEFAULT_RANGE}
      href="/cluster/nodes"
    />
  );
}

registerWidget({
  id: "network-tile",
  title: "Network I/O",
  family: "networking",
  scopes: ["overview"],
  // Trends only: the p95 values are derived from the series, and nothing here
  // reads the instantaneous summary.
  sources: ["dashboard-trends"],
  minW: 2,
  minH: 2,
  defaultW: 3,
  defaultH: 3,
  modes: ["compact", "normal"],
  render: (props) => <NetworkTileWidget {...props} />,
});
