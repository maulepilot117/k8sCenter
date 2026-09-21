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
 * Seven families, three payload shapes, one rule: `detected` is `false` or
 * `""` when the feature is absent, and names the implementation otherwise. See
 * `featurePresent` in widget-state.ts, which is the only place that reads it.
 */
export const FAMILY_STATUS_KEYS = [
  "policies-status",
  "gitops-status",
  "certificates-status",
  "mesh-status",
  "external-secrets-status",
  "velero-status",
  // The seventh, and the one this list did not have when the mechanism
  // shipped. Security scanning is CRD-discovered exactly like the six above
  // it -- `/v1/scanning/status` answers with a `ScannerStatus` whose
  // `detected` is `""`, `"trivy"`, `"kubescape"` or `"both"`, the same string
  // shape the policy, GitOps and mesh families use -- and the route it
  // guards, `/v1/scanning/vulnerabilities`, has the same defect as every
  // other CRD-backed list: it answers 200 with an empty array whether no
  // scanner is installed or every image is clean. The two readings are
  // opposite and the list cannot tell them apart, so the status route is the
  // only honest signal (R1).
  "scanning-status",
  // The eighth, and the only one that is not a route called `/status`.
  //
  // VolumeSnapshot is CRD-discovered exactly like the seven above -- the
  // storage handler's `checkSnapshotCRDs` asks the discovery client for
  // `snapshot.storage.k8s.io/v1` behind a 5-minute cache, which is the same
  // check every other family's Discoverer performs -- but the storage family
  // mounts no `/status` route to publish the answer through. It publishes it
  // on the snapshot routes instead, as `metadata.available`.
  //
  // So this key reads `/v1/storage/snapshot-classes` and normalises that
  // metadata flag into the `{ detected }` shape `featurePresent` already
  // understands. Snapshot-CLASSES rather than snapshots: both carry the same
  // flag from the same function, and the classes route is a small,
  // cluster-scoped, non-impersonated list where the snapshots route is an
  // impersonated list of every VolumeSnapshot in the cluster. Asking the
  // cheap one for the cheap answer keeps this in the `discovery` cost class
  // honestly rather than by assertion.
  //
  // The flag is the CRD check alone -- a cluster with the CRDs installed and
  // no VolumeSnapshotClasses still answers `available: true` -- so this says
  // "snapshots are a thing here", never "snapshots are configured here".
  "snapshots-status",
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
  // Two more of the same, for the scaling widgets. The route kind is `hpas`
  // and `pdbs` -- the resource adapters' `Kind()`, which is NOT the
  // `horizontalpodautoscalers` / `poddisruptionbudgets` spelling the counts
  // route and the RBAC checks use. Requesting the long form gets a 404 for a
  // kind that is very much present.
  "hpas-list",
  "pdbs-list",
  // The reliability reads. `nodes-list` is the same generic list route again
  // -- and `nodes` is both the adapter's `Kind()` and the long resource name,
  // so this one does not have the `hpas`/`pdbs` trap. The other two are not
  // the generic route at all: `limits-namespaces` is the ResourceQuota /
  // LimitRange roll-up the limits family serves, and `storage-classes` is the
  // storage family's class inventory. Neither family is CRD-discovered, so
  // neither has a status key here.
  "nodes-list",
  "limits-namespaces",
  "storage-classes",
  // The first sources that are neither an informer read nor a discovery
  // route: two named, server-owned PromQL templates from the slug registry
  // (`backend/internal/monitoring/query_registry.go`). The widget names a
  // slug and nothing else -- no query text, no URL (D-8) -- and the raw
  // `/monitoring/query` routes stay admin-gated and unreachable from here
  // (R15).
  "top-consumers-cpu",
  "top-consumers-memory",
  // The third slug read, and the one whose key does NOT echo its slug tail.
  // It reads `cluster/storage-capacity`, but the widget that composes it is
  // itself `storage-capacity` -- a card declaring a source of its own name
  // beside a second one reads as a typo. Named for what the series carry
  // instead: per-PersistentVolumeClaim percent-of-capacity-used.
  "volume-capacity",
  // The security family's four reads. None of them is a discovery route --
  // those are the FAMILY_STATUS_KEYS below, and all three security widgets
  // declare one -- these are the data the cards render once the family is
  // known to be present.
  //
  // `policy-compliance-score` is `/v1/policies/compliance` and NOT named
  // `policy-compliance`, which is the widget's own id: a card declaring a
  // source of its own name reads as a typo, and the same reasoning named
  // `volume-capacity` above.
  "policy-compliance-score",
  // `/v1/policies/compliance/history`. The only source in this table behind
  // `middleware.RequireAdmin`, and the only one that can answer 503 by
  // design -- the handler refuses that way when the deployment has no
  // database. Declared OPTIONAL by the widget that reads it for exactly that
  // reason: both refusals leave the current score untouched, and a card gated
  // on this one would blank a gauge the compliance endpoint already answered,
  // permanently, for every non-admin.
  "policy-compliance-history",
  // `/v1/policies/violations`. Note the plural prefix: the routes are mounted
  // under `/v1/policies/...`, not `/v1/policy/...` (see `registerPolicyRoutes`
  // in backend/internal/server/routes.go). The project's own CLAUDE.md API
  // summary has this wrong.
  "policy-violations-list",
  // `/v1/scanning/vulnerabilities`, under the scanning prefix rather than a
  // security one. The second parameterized source, and parameterized because
  // the handler REQUIRES `?namespace=` and answers 400 without it -- there is
  // no cluster-wide vulnerability roll-up to ask instead.
  "vulnerability-reports",
  // The data-protection family's four reads: certificates, external secrets,
  // Velero backups and volume snapshots. Every one of them is the data half
  // of a CRD-discovered feature whose presence is answered by a status key
  // below -- never by whether the list came back empty, which all four routes
  // do on a cluster that runs none of this (R1).
  //
  // `certificates-list` is `/v1/certificates/certificates`, the full
  // inventory, NOT `/v1/certificates/expiring`. The expiring route returns
  // only what is already inside its warning threshold and carries a
  // pre-computed severity string instead of the per-certificate thresholds
  // behind it, so a card built on it could tell neither "every certificate is
  // healthy" from "cert-manager manages nothing", nor which threshold a
  // classification came from. See `certsExpiringView` in expiry.ts.
  "certificates-list",
  // `/v1/externalsecrets/externalsecrets` -- the list is nested under its own
  // segment, and a bare `/v1/externalsecrets` is the router group rather than
  // a route. Drift and sync state ride inline on each item, so the card needs
  // no second call.
  "external-secrets-list",
  "velero-backups-list",
  // `/v1/storage/snapshots`. Its sibling `snapshots-status` below reads the
  // CLASSES route for the availability flag rather than this one, so the
  // expensive list is issued once and only for its data.
  "snapshots-list",
  // The delivery family's one read, shared by BOTH GitOps cards.
  //
  // `/v1/gitops/applications`, whose envelope is `{ applications, summary }`
  // rather than a bare list -- so `read`, not `readList`, and the fetcher
  // keeps the envelope. Two widgets declaring the same key is the case the
  // per-cycle cache exists for: the app-health roll-up and the recent-syncs
  // list are one request, not two, however many copies of either an operator
  // places.
  //
  // `/v1/gitops/commits` is deliberately NOT a source. It requires a
  // repository URL AND a set of shas -- so it can only be asked after this
  // list is in hand -- and answers with a neutral empty shape when the
  // deployment has no Git provider token, which is the default. A card whose
  // rows depended on it would be blank on most clusters. See
  // `gitopsRecentSyncsView` in sync-state.ts.
  "gitops-applications",
  // The networking family's one read: `/v1/mesh/mtls`, and requested with NO
  // namespace, which that route treats as a cluster-scoped read (KTD4). That
  // is what keeps `mtls-coverage` parameterless: the cluster-wide posture is
  // the more useful default for an overview card and the one an operator
  // cannot reconstruct without visiting every namespace page in turn.
  "mesh-mtls",
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
  // Same route, same reasons.
  "hpas-list": "expensive",
  "pdbs-list": "expensive",
  // Same route, same reasons, and a node object is one of the larger ones the
  // route serves -- capacity, allocatable, images and a dozen conditions each.
  "nodes-list": "expensive",
  // Neither of these is the generic list route, and neither is cheap.
  //
  // `limits-namespaces` runs a SelfSubjectAccessReview PER NAMESPACE before it
  // answers -- `filterByRBAC` checks every namespace in the roll-up
  // individually -- so its cost scales with the cluster rather than with the
  // dashboard. `storage-classes` is the mildest source in this table: an
  // informer list with no access review at all. It is still not `cheap`,
  // because both routes sit under the backend's 30-request-per-minute YAML
  // bucket, shared with `/yaml/*` and `/wizards/*` -- a dashboard refreshing
  // them on the unmanaged path would spend an operator's YAML budget in
  // another tab. Classifying by the bucket rather than by the work is the
  // direction `sourceCost` already defaults in.
  "limits-namespaces": "expensive",
  "storage-classes": "expensive",
  // Prometheus, which is the definition of this class: seconds rather than
  // milliseconds, and a cost the backend pays per request. The slug handler
  // adds a SelfSubjectAccessReview in front of the query, so a refused caller
  // pays for the check and gets nothing.
  "top-consumers-cpu": "expensive",
  "top-consumers-memory": "expensive",
  "volume-capacity": "expensive",

  // The four security reads. None of them is `discovery`, although every
  // widget that reads them also declares a discovery status: that class is
  // for the status ROUTES, which answer out of a 5-minute cache and return a
  // handful of booleans. These four are data reads over the same CRDs, and
  // each one is expensive for a reason the shape does not show.
  //
  // `policy-compliance-score` and `policy-violations-list` both run
  // `filterViolationsByRBAC`, which issues a SelfSubjectAccessReview PER
  // NAMESPACE carrying a violation -- so their cost scales with the cluster
  // rather than with the dashboard, which is the same argument that put
  // `limits-namespaces` in this class. Both also sit under the backend's
  // 30-request-per-minute YAML bucket, shared with `/yaml/*` and
  // `/wizards/*`.
  //
  // `policy-compliance-history` is a PostgreSQL range query, which the class
  // definition names outright.
  //
  // `vulnerability-reports` is two access reviews plus a CRD list per
  // request, issued once per distinct namespace on the layout -- a cost that
  // grows with the dashboard rather than being fixed per page, which is the
  // argument that put `diagnostics-summary` here.
  "policy-compliance-score": "expensive",
  "policy-compliance-history": "expensive",
  "policy-violations-list": "expensive",
  "vulnerability-reports": "expensive",

  // The data-protection family's four reads. Every one of them is expensive,
  // and for the reasons that put their security-family neighbours here rather
  // than for their shape.
  //
  // `certificates-list` and `external-secrets-list` both run `filterByRBAC`,
  // which issues a SelfSubjectAccessReview PER NAMESPACE carrying an item --
  // the same argument that classified `limits-namespaces` and
  // `policy-violations-list`. Both also serialise whole normalised CRD
  // objects, a dozen fields each, for every object in the cluster.
  //
  // `velero-backups-list` is a live five-way dynamic list (backups, restores,
  // schedules and both location kinds) behind a 30-second cache, because the
  // handler's fetch is all-or-nothing; asking for backups pays for the rest.
  //
  // `snapshots-list` is the dearest of the four and the only one with no
  // server-side cache at all: an impersonated dynamic LIST of every
  // VolumeSnapshot in every namespace, on every request. It also shares the
  // storage routes' 30-request-per-minute YAML bucket with `/yaml/*` and
  // `/wizards/*`, as `storage-classes` above does.
  "certificates-list": "expensive",
  "external-secrets-list": "expensive",
  "velero-backups-list": "expensive",
  "snapshots-list": "expensive",

  // The delivery and networking reads. Both expensive, and the second is the
  // dearest source in this table.
  //
  // `gitops-applications` runs `filterAppsByRBAC`, a SelfSubjectAccessReview
  // per namespace carrying an application -- the argument that classified
  // `limits-namespaces`, `policy-violations-list` and `certificates-list` --
  // and it sits under the backend's 30-request-per-minute YAML bucket, shared
  // with `/yaml/*` and `/wizards/*`. Its 30-second server-side cache covers
  // only the CRD fetch; the access reviews and the serialisation of every
  // normalized application are paid per request.
  //
  // `mesh-mtls` is worse than any of them and the shape shows none of it. One
  // request runs an access review, an IMPERSONATED pod LIST across every
  // namespace (capped at `meshListCap` and flagged `truncated` when it bites),
  // a ReplicaSet list for owner resolution, a mesh-policy fetch, and -- when
  // Istio is part of the detected mesh -- a Prometheus range query to
  // cross-check the policy verdict against observed traffic. There is no
  // server-side cache on any of it. That is a Prometheus read AND a cost the
  // backend pays per request rather than amortising across viewers, which is
  // this class twice over.
  "gitops-applications": "expensive",
  "mesh-mtls": "expensive",

  "policies-status": "discovery",
  "gitops-status": "discovery",
  "certificates-status": "discovery",
  "mesh-status": "discovery",
  "external-secrets-status": "discovery",
  "velero-status": "discovery",
  "scanning-status": "discovery",
  // The eighth. `discovery` on the merits and not only to satisfy the class's
  // invariant: it reads the snapshot-CLASSES route, whose answer is the
  // 5-minute-cached CRD check plus a small cluster-scoped list, which is what
  // this class describes. The expensive snapshot read is `snapshots-list`
  // above, and it is a separate key precisely so this one can stay cheap.
  "snapshots-status": "discovery",
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

/**
 * Sources whose 404 is a refusal, not a missing object.
 *
 * The slug-query handler answers a caller who lacks the slug's declared grant
 * with 404 and the body "not found or forbidden" -- the SAME response it gives
 * for a slug that does not exist. That is deliberate: a 403 there would let
 * anyone enumerate the query catalog by watching which slugs come back
 * forbidden (F#29 of the 2026-05-22 audit). The opacity is a property of that
 * route, and the backend is not the thing to change.
 *
 * But the failure classifier in data.ts maps 403 and nothing else to the
 * permission state, so without this a user who simply lacks cluster-wide pod
 * read lands the top-consumers card in the plain error state -- "could not be
 * loaded", with a retry that will never work -- instead of the permission
 * state R2 built for exactly that person. The widget cannot fix it itself:
 * `resolveWidgetState` decides the outcome before `render` is ever called, so
 * by the time the widget runs, the choice has been made.
 *
 * So the widening is declared per source rather than applied to the status
 * code globally. A 404 from an ordinary resource route means the object is
 * gone, which is not a permission problem and must not read as one; a 404
 * from a route that has no other way to say "forbidden" is the only place the
 * reading is right. Two keys today, both pointing at the one handler that
 * refuses this way.
 *
 * The residual imprecision is named rather than hidden: a frontend asking a
 * backend too old to carry the slug also gets a 404, and this classifies that
 * as a permission problem too. Both mean "this build will not serve you this
 * query", the card is identical either way, and the alternative -- reading
 * every genuine refusal as a server error -- is wrong for the far more common
 * case.
 */
export const NOT_FOUND_IS_REFUSAL: ReadonlySet<string> = new Set([
  "top-consumers-cpu",
  "top-consumers-memory",
  // The third slug, and the same handler. Listed even though it is an
  // OPTIONAL source, where the two above are required ones: the widget reads
  // `errorKind` itself to choose between "your account may not read volume
  // usage" and "volume usage could not be read", and without this entry a
  // refused caller gets the second -- an invitation to wait for something
  // that will never arrive. Its declared grant is a cluster-scoped `list` on
  // persistentvolumeclaims, which is exactly the grant a namespace-scoped
  // operator does not have, so this is the common case on that card rather
  // than an edge one.
  "volume-capacity",
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
