/**
 * Shared types and bounds for the personal dashboard builder.
 *
 * This module is pure: no DOM, no fetch, no signals. Everything that needs a
 * unit test in this feature lives behind it, because the repo has no component
 * test harness (see the design spec, D-10).
 */
import type { VNode } from "preact";

/** The three sizes a widget can render at. Ordered smallest to largest. */
export const DISPLAY_MODES = ["compact", "normal", "expanded"] as const;
export type DisplayMode = (typeof DISPLAY_MODES)[number];

/** Which dashboard a widget may appear on. P6 adds more; the field exists now
 * so per-category dashboards are routing rather than a second mechanism. */
export const DASHBOARD_SCOPES = ["overview"] as const;
export type DashboardScope = (typeof DASHBOARD_SCOPES)[number];

/** Grouping in the catalog palette. Presentation only; carries no behavior. */
export const WIDGET_FAMILIES = [
  "cluster",
  "workloads",
  "reliability",
  "security",
  "delivery",
  "data-protection",
  "networking",
  "platform",
] as const;
export type WidgetFamily = (typeof WIDGET_FAMILIES)[number];

/** Every distinct backend read the dashboard performs. A widget declares which
 * it needs; the cache in data.ts fetches each key at most once per cycle. */
export const DATA_SOURCE_KEYS = [
  "dashboard-summary",
  "dashboard-trends",
  "cluster-info",
  "recent-events",
] as const;
export type DataSourceKey = (typeof DATA_SOURCE_KEYS)[number];

/** Grid geometry. Twelve divides into halves, thirds and quarters, which is
 * what the pre-registry three-row layout already approximated. */
export const DASHBOARD_COLUMNS = 12;
export const DASHBOARD_ROW_HEIGHT = 40;
export const DASHBOARD_GRID_GAP = 16;
export const DASHBOARD_MAX_ITEMS = 40;
export const DASHBOARD_LAYOUT_SCHEMA_VERSION = 1;

/** One widget placement. instanceId exists because a parameterized widget may
 * legitimately appear twice -- diagnostics for prod beside diagnostics for
 * staging -- so identity cannot be the widget id. */
export interface LayoutItem {
  instanceId: string;
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  params?: Record<string, string>;
}

/** The persisted envelope. Mirrors the Go DashboardLayoutConfig in P3. */
export interface DashboardLayoutConfig {
  schemaVersion: number;
  scope: DashboardScope;
  columns: number;
  items: LayoutItem[];
}

/** What a widget's render function receives. */
export interface WidgetProps {
  mode: DisplayMode;
  params: Record<string, string>;
}

export interface WidgetDef {
  /** Stable kebab-case. Never reused after retirement. */
  id: string;
  title: string;
  family: WidgetFamily;
  scopes: DashboardScope[];
  sources: DataSourceKey[];
  /** Smallest the editor will let the user resize this widget. */
  minW: number;
  minH: number;
  /** Size used when the widget is added from the palette. */
  defaultW: number;
  defaultH: number;
  /** Which modes this widget actually implements. Must include "normal". */
  modes: DisplayMode[];
  render(props: WidgetProps): VNode;
}
