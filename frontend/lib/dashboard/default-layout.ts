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

/** One widget in a pre-registry flex row. */
export interface FlexSlot {
  id: string;
  /** CSS `flex` shorthand. Omitted for a tile inside the 2x2 block. */
  flex?: string;
  minWidth?: string;
  /** Skeleton height while loading: the flex slot has no height to fill. */
  placeholder: string;
}

/** A row cell: one widget, or the 2x2 metric tile block that shares a cell. */
export type FlexCell = FlexSlot | { flex: string; tiles: FlexSlot[] };

/**
 * The pre-registry flex rows, as data, so the shell renders them in a loop
 * and a unit test can hold them to DEFAULT_OVERVIEW_LAYOUT. Written as JSX,
 * a widget added to the layout but not given a slot would pass every unit
 * test and simply not appear.
 *
 * Temporary by design: P2 renders DEFAULT_OVERVIEW_LAYOUT through the grid
 * and deletes this.
 */
export const OVERVIEW_FLEX_ROWS: readonly FlexCell[][] = [
  [
    {
      id: "cluster-health",
      flex: "2 1 320px",
      minWidth: "280px",
      placeholder: "200px",
    },
    {
      flex: "3 1 380px",
      tiles: [
        { id: "cpu-tile", placeholder: "120px" },
        { id: "memory-tile", placeholder: "120px" },
        { id: "pods-tile", placeholder: "120px" },
        { id: "network-tile", placeholder: "120px" },
      ],
    },
  ],
  [
    {
      id: "resource-utilization",
      flex: "3 1 380px",
      minWidth: "280px",
      placeholder: "160px",
    },
    {
      id: "pod-status",
      flex: "2 1 240px",
      minWidth: "200px",
      placeholder: "160px",
    },
  ],
  [
    { id: "nodes", flex: "2 1 260px", minWidth: "220px", placeholder: "200px" },
    {
      id: "recent-events",
      flex: "3 1 300px",
      minWidth: "240px",
      placeholder: "200px",
    },
    {
      id: "active-alerts",
      flex: "2 1 240px",
      minWidth: "200px",
      placeholder: "200px",
    },
  ],
];

/** Every widget id the flex rows render, in render order. */
export function flexSlotIds(rows: readonly FlexCell[][]): string[] {
  return rows
    .flat()
    .flatMap((c) => ("tiles" in c ? c.tiles : [c]).map((s) => s.id));
}

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
