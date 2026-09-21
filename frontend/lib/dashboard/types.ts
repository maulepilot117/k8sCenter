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

/**
 * The discovery routes that say whether a CRD-discovered feature is installed.
 *
 * These are data sources like any other, but they are named apart because they
 * are the only ones that answer a question about the CLUSTER rather than about
 * a workload: is cert-manager here at all? A family's list endpoint cannot
 * answer it -- the backend returns 200 with an empty array whether the
 * operator is absent or merely has nothing to report, so a widget inferring
 * absence from an empty list renders "no expiring certificates" on a cluster
 * with no cert-manager, which is the exact failure R1 forbids.
 *
 * Six families, three payload shapes, one rule: `detected` is `false` or `""`
 * when the feature is absent, and names the implementation otherwise. See
 * `featurePresent` in widget-state.ts, which is the only place that reads it.
 */
export const FAMILY_STATUS_KEYS = [
  "policies-status",
  "gitops-status",
  "certificates-status",
  "mesh-status",
  "external-secrets-status",
  "velero-status",
] as const;
export type FamilyStatusKey = (typeof FAMILY_STATUS_KEYS)[number];

/** Every distinct backend read the dashboard performs. A widget declares which
 * it needs; the cache in data.ts fetches each key at most once per cycle. */
export const DATA_SOURCE_KEYS = [
  "dashboard-summary",
  "dashboard-trends",
  "cluster-info",
  "recent-events",
  // The first source whose response depends on a widget's own parameters:
  // one read per namespace rather than one per page. The cache keys it by
  // source AND parameters (see `sourceKeyFor` in params.ts), so two
  // diagnostics widgets pointed at different namespaces are two entries and
  // two pointed at the same one are still a single fetch.
  "diagnostics-summary",
  // Batch counts for every informer-tracked kind. Read by the workload
  // roll-up as its visibility oracle rather than only as a source of totals:
  // the route omits a kind the caller cannot list instead of zeroing it, and
  // it is the only route that answers "may this account see this kind at
  // all". See `ResourceCounts` in wire-types.ts.
  "resource-counts",
  // Four reads of the generic list route, one per kind. Separate keys rather
  // than one parameterized source: the kind is fixed by the widget, not
  // chosen by the user, so there is nothing to carry in a cache key and
  // nothing for the server to re-authorize (D-8).
  "deployments-list",
  "statefulsets-list",
  "daemonsets-list",
  "pods-list",
  ...FAMILY_STATUS_KEYS,
] as const;
export type DataSourceKey = (typeof DATA_SOURCE_KEYS)[number];

/**
 * What a source costs the backend to answer, which is what decides whether
 * the refresh scheduler may issue it on the tick with everything else.
 *
 * Three classes, not two (KTD5):
 *
 * - `cheap` -- served from the backend's informer cache. The read is a map
 *   lookup in a process that already holds the objects; several at once cost
 *   nothing worth managing.
 * - `discovery` -- a CRD discovery route. Dearer than an informer read and
 *   cheaper than a range query: a live API-server call behind a 5-minute
 *   cache, and three of the six share the backend's 30-request-per-minute
 *   YAML bucket with `/yaml/*` and `/wizards/*`. Named apart because it backs
 *   most of the growing catalog, and scheduled under the expensive policy.
 * - `expensive` -- a Prometheus, Hubble or PostgreSQL read. Seconds rather
 *   than milliseconds, and a cost the backend pays per request rather than
 *   amortising across viewers.
 *
 * The classification is declarative here and consumed in one place --
 * `data.ts`, which bounds how many non-cheap reads are on the wire at once
 * and gives each one an offset inside the refresh interval. Widgets never see
 * it; it is not part of the render contract.
 */
export const SOURCE_COSTS = ["cheap", "discovery", "expensive"] as const;
export type SourceCost = (typeof SOURCE_COSTS)[number];

/**
 * The cost of each declared source.
 *
 * `dashboard-trends` is the one entry worth reading twice. It sits beside
 * three informer reads on the same dashboard and looks like them, but
 * `HandleDashboardTrends` in backend/internal/k8s/resources/dashboard.go runs
 * Prometheus range queries and its own comment calls them multi-second -- it
 * was split out of the summary handler precisely so they would not eat that
 * endpoint's 1-second Prometheus budget. Under KTD5 that is expensive, and
 * classifying it by its neighbours rather than by what it does would leave
 * the only expensive source the dashboard has today on the unmanaged path.
 */
export const SOURCE_COST: Readonly<Record<DataSourceKey, SourceCost>> = {
  "dashboard-summary": "cheap",
  "cluster-info": "cheap",
  "recent-events": "cheap",
  "dashboard-trends": "expensive",
  // Informer-backed like the three above it, and still not cheap. Three
  // reasons, none of which the list shape shows: the handler runs a
  // SelfSubjectAccessReview before it reads anything (60s-cached, which is
  // exactly the refresh interval, so most ticks pay for one); the route sits
  // under the backend's 30-request-per-minute YAML bucket, shared with
  // `/yaml/*` and `/wizards/*`; and it is the only source issued once per
  // distinct parameter value, so its cost grows with the dashboard rather
  // than being fixed per page. That is "a cost the backend pays per request
  // rather than amortising across viewers", which is this class.
  "diagnostics-summary": "expensive",
  // The five list-shaped reads. Informer-backed, and still not cheap, for the
  // two reasons the diagnostics entry above gives and one of their own:
  // every one of them runs a SelfSubjectAccessReview before it reads
  // anything, and every one of them serialises whole Kubernetes objects --
  // up to the route's 500-item page cap -- rather than a handful of numbers.
  // `pods-list` is the extreme case: a page of 500 pod specs is orders of
  // magnitude more bytes than the entire dashboard summary. A read whose cost
  // the backend pays per request rather than amortising across viewers is
  // this class, and that is what these are.
  "resource-counts": "expensive",
  "deployments-list": "expensive",
  "statefulsets-list": "expensive",
  "daemonsets-list": "expensive",
  "pods-list": "expensive",

  "policies-status": "discovery",
  "gitops-status": "discovery",
  "certificates-status": "discovery",
  "mesh-status": "discovery",
  "external-secrets-status": "discovery",
  "velero-status": "discovery",
};

/**
 * The cost of a source, defaulting to `expensive` for anything unlisted.
 *
 * Expensive, never cheap: a contributor who adds a source to the catalog and
 * forgets `SOURCE_COST` gets a read that is throttled harder than it needs to
 * be, which is a slower dashboard. The other default gets a read that joins
 * the stampede KTD5 exists to prevent, which is a slower cluster. Takes a
 * plain string so a key arriving from a stored layout, rather than from the
 * union, still classifies.
 */
export function sourceCost(key: string): SourceCost {
  return SOURCE_COST[key as DataSourceKey] ?? "expensive";
}

/**
 * Sources whose response depends on the selected time range. Re-ensuring one
 * of these under a new range refetches; the others do not.
 *
 * Deliberately a hand-written set rather than anything derived: a unit adding
 * a range-backed source adds its key here, in the same edit that adds it to
 * `DATA_SOURCE_KEYS` and `SOURCE_COST`. Range-sensitivity does not follow
 * from cost -- a cheap source could take a window and an expensive one need
 * not -- so inferring it would be wrong in both directions.
 */
export const RANGE_SENSITIVE_KEYS: ReadonlySet<string> = new Set([
  "dashboard-trends",
]);

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
   * The CRD-discovered family this widget needs installed, if any.
   *
   * Declaring it is what buys the widget an explicit "not installed on this
   * cluster" state, resolved by WidgetHost before `render` is ever called --
   * so the widget body never learns it is unavailable, and never has to
   * decide whether its own empty list means "nothing to report" or "no
   * operator". Deciding that once here, rather than in every widget that
   * reads a CRD-backed endpoint, is the mitigation the design spec named for
   * the render contract itself (KTD1).
   *
   * The key is fetched and required alongside `sources`: it cannot be listed
   * in `optionalSources`, because a widget that renders before its family
   * status has landed is the case this field exists to prevent. A widget that
   * omits this field behaves exactly as it did before availability existed.
   */
  familyStatus?: FamilyStatusKey;
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
