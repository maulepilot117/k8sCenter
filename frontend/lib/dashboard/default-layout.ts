import type { DashboardLayoutConfig } from "./types.ts";
import { DASHBOARD_COLUMNS, DASHBOARD_LAYOUT_SCHEMA_VERSION } from "./types.ts";

/**
 * The layout every user starts with, and the one "Reset" restores.
 *
 * Sizes are in grid units: 12 columns, 40px rows with 16px gaps, so h rows
 * span 56h - 16 px. They are tuned to what each widget renders today rather
 * than translated from the old flex rows, which a fixed-row grid cannot
 * reproduce exactly:
 *
 *  - Row 1: health on the left half; the four metric tiles as an equal 2x2 on
 *    the right, each tall enough for its value and sparkline.
 *  - Row 2: the utilization chart beside the pod-status donut.
 *  - Row 3: nodes and alerts at their natural height, with recent events twice
 *    as tall so ten events fit before the card scrolls.
 *
 * Cards fill their cell and scroll their body when content is taller, so a
 * size here is the widget's height, not a minimum.
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
      w: 6,
      h: 6,
    },
    { instanceId: "d-cpu-tile", id: "cpu-tile", x: 6, y: 0, w: 3, h: 3 },
    { instanceId: "d-memory-tile", id: "memory-tile", x: 9, y: 0, w: 3, h: 3 },
    { instanceId: "d-pods-tile", id: "pods-tile", x: 6, y: 3, w: 3, h: 3 },
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
      h: 4,
    },
    { instanceId: "d-pod-status", id: "pod-status", x: 7, y: 6, w: 5, h: 4 },

    // Row 3 -- nodes, events, alerts.
    { instanceId: "d-nodes", id: "nodes", x: 0, y: 10, w: 4, h: 5 },
    {
      instanceId: "d-recent-events",
      id: "recent-events",
      x: 4,
      y: 10,
      w: 5,
      h: 10,
    },
    {
      instanceId: "d-active-alerts",
      id: "active-alerts",
      x: 9,
      y: 10,
      w: 3,
      h: 5,
    },
  ],
};
