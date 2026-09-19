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
/**
 * The furthest down the grid a widget may reach.
 *
 * This is a containment bound, not a design limit -- 200 rows is an 8000px
 * dashboard, far past anything usable. It is declared here because the server
 * enforces it on save: without a client copy the editor would happily let a
 * user drag past it and then surface a rejected save citing a cap the client
 * never mentioned. Pinned against the Go `maxDashboardRows` by
 * TestContractParity in backend/internal/preferences/parity_test.go.
 *
 * `moveItem` and `resizeItem` clamp against it, so no single drag or resize
 * can cross it. Compaction is NOT clamped and can still stack past it --
 * forty six-row widgets in one column reach row 240 -- because pinning y
 * during compaction would produce overlapping items, which is a worse layout
 * than a tall one. A layout that reaches the cap that way is refused on save;
 * the editor surfacing that before the round trip belongs with the save path.
 */
export const DASHBOARD_MAX_ROWS = 200;
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
  /** Every source this widget reads. The cache fetches all of them. */
  sources: DataSourceKey[];
  /**
   * The subset of `sources` the widget can render without.
   *
   * Every source a widget lists is another way for it to disappear, because
   * WidgetHost renders only once the sources it depends on have data. A metric
   * tile needs its summary value but merely decorates with a trend series, so
   * gating the whole tile on the trend endpoint makes a slow or failed
   * secondary request blank a number the primary endpoint already returned.
   * The pre-registry island fetched under Promise.allSettled and rendered
   * whatever arrived, so gating on everything is also a fidelity break.
   *
   * A source named here may be null at render time, and the widget must
   * tolerate that. Sources NOT named here are guaranteed non-null when
   * `render` is called. Omitted means every source is required.
   */
  optionalSources?: DataSourceKey[];
  /**
   * Smallest the editor will let the user resize this widget.
   *
   * The server enforces these too, so they are a cross-language contract, not
   * just editor behaviour: a layout carrying a smaller placement is refused on
   * save. `TestContractParity` in backend/internal/preferences/parity_test.go
   * pins every pair, so changing one here without changing the Go catalog
   * fails a test rather than producing saves the editor cannot explain.
   */
  minW: number;
  minH: number;
  /** Size used when the widget is added from the palette. */
  defaultW: number;
  defaultH: number;
  /**
   * The parameters this widget accepts, as key -> the closed set of values.
   *
   * Omitted (the case for every widget today) means the widget takes no
   * parameters, and the server refuses a stored placement carrying any. An
   * empty value array means the legal values are not knowable ahead of time --
   * a namespace name -- so only the generic length and control-character
   * bounds apply; that is deliberately different from omitting the key.
   *
   * This exists so a parameterized widget declares its surface in one place
   * rather than the server accepting whatever a client sends. Adding one here
   * requires the matching entry in the Go catalog, which
   * `TestContractParity` enforces by failing the moment a widget stops being
   * parameterless.
   */
  params?: Readonly<Record<string, readonly string[]>>;
  /** Which modes this widget actually implements. Must include "normal". */
  modes: DisplayMode[];
  render(props: WidgetProps): VNode;
}
