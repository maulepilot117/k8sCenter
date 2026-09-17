import { NetworkTile } from "@/components/ui/NetworkTile.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { DashboardTrends } from "@/lib/dashboard/wire-types.ts";
import { percentile } from "@/lib/format.ts";

/** Fallback period label, matching the island's initial time-range tab. */
const DEFAULT_RANGE = "1h";

/**
 * Cluster network throughput: RX/TX p95 over the active window, derived from
 * the trend series so the value tracks whichever time-range tab is selected.
 *
 * The period label is the range the displayed series was fetched under, read
 * from the same cache entry as the series, not the tab the user just clicked.
 * The two diverge while a tab-switch fetch is in flight, and labelling the old
 * data with the new window states a window the data has not caught up to yet.
 * Reading it here rather than taking it as a param means no host -- the flex
 * shell today, the grid in P2 -- has to know this widget needs it.
 */
function NetworkTileWidget() {
  const trends = dashboardData.state<DashboardTrends>("dashboard-trends");
  const t = trends.data;

  return (
    <NetworkTile
      rxP95={percentile(t?.networkRx, 95)}
      txP95={percentile(t?.networkTx, 95)}
      rxData={t?.networkRx}
      txData={t?.networkTx}
      period={trends.range ?? DEFAULT_RANGE}
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
  render: () => <NetworkTileWidget />,
});
