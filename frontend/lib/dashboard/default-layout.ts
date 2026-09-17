import type { DashboardLayoutConfig, LayoutItem } from "./types.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";

/**
 * The layout every user starts with, and the one "Reset" restores.
 *
 * These coordinates translate the pre-registry flex rows onto the twelve-column
 * grid: row 1 was flex 2:3 (5/7, the tiles a 2x2 block in the right-hand 7),
 * row 2 was 3:2 (7/5), and row 3 was 2:3:2 (4/5/3).
 *
 * Nothing renders these coordinates yet. D5b keeps the flex rows so the page
 * stays visually identical; the grid is P2's, where placement becomes
 * user-visible and editable. Until then this is the source of each slot's
 * instance, and the unit test is what keeps it renderable.
 */
export const DEFAULT_OVERVIEW_LAYOUT: DashboardLayoutConfig = {
  schemaVersion: DASHBOARD_LAYOUT_SCHEMA_VERSION,
  scope: "overview",
  columns: DASHBOARD_COLUMNS,
  items: [
    // Row 1 -- health on the left, the 2x2 tile block on the right.
    {
      instanceId: "d-cluster-health",
      id: "cluster-health",
      x: 0,
      y: 0,
      w: 5,
      h: 6,
    },
    { instanceId: "d-cpu-tile", id: "cpu-tile", x: 5, y: 0, w: 4, h: 3 },
    { instanceId: "d-memory-tile", id: "memory-tile", x: 9, y: 0, w: 3, h: 3 },
    { instanceId: "d-pods-tile", id: "pods-tile", x: 5, y: 3, w: 4, h: 3 },
    {
      instanceId: "d-network-tile",
      id: "network-tile",
      x: 9,
      y: 3,
      w: 3,
      h: 3,
    },

    // Row 2 -- the utilization chart beside the pod-status donut.
    {
      instanceId: "d-resource-utilization",
      id: "resource-utilization",
      x: 0,
      y: 6,
      w: 7,
      h: 6,
    },
    { instanceId: "d-pod-status", id: "pod-status", x: 7, y: 6, w: 5, h: 6 },

    // Row 3 -- nodes, events, alerts.
    { instanceId: "d-nodes", id: "nodes", x: 0, y: 12, w: 4, h: 6 },
    {
      instanceId: "d-recent-events",
      id: "recent-events",
      x: 4,
      y: 12,
      w: 5,
      h: 6,
    },
    {
      instanceId: "d-active-alerts",
      id: "active-alerts",
      x: 9,
      y: 12,
      w: 3,
      h: 6,
    },
  ],
};

/**
 * The default layout's placement of a widget id.
 *
 * Throws rather than returning undefined: the only caller is the fixed flex
 * shell, and a slot naming an id the layout lacks is a programming error that
 * must fail loudly, not render a silent gap.
 */
export function defaultItem(id: string): LayoutItem {
  const item = DEFAULT_OVERVIEW_LAYOUT.items.find((i) => i.id === id);
  if (!item) throw new Error(`default layout has no widget ${id}`);
  return item;
}
