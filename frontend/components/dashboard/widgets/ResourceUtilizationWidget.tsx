import { ResourceAreaChart } from "@/components/charts/ResourceAreaChart.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { DashboardTrends } from "@/lib/dashboard/wire-types.ts";

/**
 * One legend key: a colour swatch and a series name.
 *
 * The island wrote this structure out twice, identical apart from the colour
 * and the label. Collapsing it is the one change this extraction makes to the
 * markup, and it renders the same DOM.
 */
function LegendKey({ color, label }: { color: string; label: string }) {
  return (
    <span
      style={{
        display: "flex",
        alignItems: "center",
        gap: "5px",
        fontSize: "12px",
        color: "var(--text-muted)",
      }}
    >
      <span
        style={{
          display: "inline-block",
          width: "8px",
          height: "8px",
          borderRadius: "2px",
          background: color,
        }}
      />
      {label}
    </span>
  );
}

/**
 * CPU and memory utilisation over the selected window, as a stacked area
 * chart with a two-key legend in the shell's action slot.
 *
 * Lifted from the pre-registry DashboardV2 card. The positional flex wrapper
 * is gone -- placement belongs to the grid, not the widget.
 */
function ResourceUtilization() {
  const t = dashboardData.state<DashboardTrends>("dashboard-trends").data;

  return (
    <WidgetShell
      title="Resource Utilization"
      action={
        <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
          <LegendKey color="var(--accent)" label="CPU" />
          <LegendKey color="var(--accent-secondary)" label="Memory" />
        </div>
      }
    >
      <ResourceAreaChart cpuData={t?.cpu ?? null} memData={t?.memory ?? null} />
    </WidgetShell>
  );
}

registerWidget({
  id: "resource-utilization",
  title: "Resource Utilization",
  family: "cluster",
  scopes: ["overview"],
  sources: ["dashboard-trends"],
  minW: 4,
  minH: 4,
  defaultW: 7,
  defaultH: 6,
  // Normal only -- one rendering in the island; see NodesWidget. A chart
  // needs width to be readable, but "expanded" would have to render
  // something different to be worth declaring.
  modes: ["normal"],
  render: () => <ResourceUtilization />,
});
