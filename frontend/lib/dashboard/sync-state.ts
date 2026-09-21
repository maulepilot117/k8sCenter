/**
 * The derived state behind the three delivery-and-mesh widgets:
 * `gitops-app-health`, `gitops-recent-syncs` and `mtls-coverage`.
 *
 * What the GitOps pair share is a roll-up that has to COLLAPSE two independent
 * fields into one verdict per application. The list route reports `syncStatus`
 * and `healthStatus` separately, and the server's own summary
 * (`computeMetadata` in backend/internal/gitops/handler.go) counts them in two
 * separate switches -- so an application that is out of sync AND degraded
 * increments `outOfSync` and `degraded` both, and its counters deliberately do
 * not partition the fleet. That is right for the applications page, which
 * shows the same application under either filter, and wrong for a dashboard
 * card, which prints the numbers side by side and invites the reader to add
 * them up. So this module does not reuse that summary: it assigns each
 * application exactly one bucket, worst wins, and the buckets sum to the total.
 *
 * This is the one place in the feature that deliberately departs from U7's
 * "prefer the server-computed status over re-deriving one" convention, and it
 * departs from it narrowly: the STATUSES are still the server's, verbatim and
 * unrecomputed. Only the fold from two fields to one is done here, because
 * there is no server-side answer to fold to.
 *
 * What is NOT here is availability. Whether Argo CD, Flux CD, Istio or Linkerd
 * is installed at all is answered by that family's own discovery route
 * (`gitops-status`, `mesh-status`) and resolved by the shell before `render`
 * is called (KTD1, R1). Both list routes answer 200 with an empty result on a
 * cluster running none of it, so every function below is reached only on a
 * cluster that runs the feature -- which is what lets an empty list here mean
 * "nothing to report" and say so plainly.
 *
 * Total, in the sense the rest of lib/dashboard is total: every argument may
 * be null, missing or the wrong shape, and none of it throws. One unreadable
 * item degrades to a counter rather than blanking a card describing the
 * readable rest.
 *
 * Pure: no DOM, no fetch, no signals (D-10).
 */

import { list, obj, str } from "./narrow.ts";

// --------------------------------------------------------------------------
// Shared readers
// --------------------------------------------------------------------------
//
// Private copies, as in `expiry.ts`, `severity.ts`, `pressure.ts` and four
// other modules here. Hoisting them is a real cleanup and it is a cross-cutting
// one -- seven modules and their tests -- so it does not belong inside a widget
// unit.

/**
 * The list carried on a route envelope's field, or null when the body is not
 * that envelope at all.
 *
 * A MISSING or null field is an empty list, not an unreadable one, and the
 * difference is load-bearing in both directions. Go marshals a nil slice as
 * `null`, and `/v1/gitops/applications` builds its result with
 * `var out []NormalizedApp` -- so a caller whose RBAC filter drops everything
 * gets `"applications": null`, which means zero applications and must read as
 * such. A field that is present and NOT a list is something this build cannot
 * parse, which must stay distinguishable from empty: an unreadable fleet
 * rendered as an empty one is a card reporting health it never saw.
 */
function envelopeList(data: unknown, field: string): unknown[] | null {
  const body = obj(data);
  if (body === null) return null;
  const value = body[field];
  if (value === undefined || value === null) return [];
  // An absent field is an empty list; a present field that is not a list
  // is the backend saying it could not give us one. Same contract as
  // narrow.ts's `list`, applied one level into an envelope.
  return list(value);
}

// --------------------------------------------------------------------------
// Links
// --------------------------------------------------------------------------

/** The GitOps applications page, which both GitOps cards link to (R7). */
export const GITOPS_APPLICATIONS_PAGE_HREF = "/gitops/applications";

/** The mesh mTLS posture page (R7). */
export const MESH_MTLS_PAGE_HREF = "/networking/mesh/mtls";

/**
 * A GitOps application's detail page, from its composite id.
 *
 * The id is the feature's own `{toolPrefix}:{namespace}:{name}` convention --
 * `argo:argocd:api`, `flux-hr:flux-system:redis` -- so it carries colons by
 * construction and lands here as a path segment. Encoded for the same reason
 * `GitOpsApplications.tsx` encodes it when it navigates: it works raw today and
 * would stop the first time a segment arrives that does not.
 */
export function gitopsAppHref(id: string): string {
  return `${GITOPS_APPLICATIONS_PAGE_HREF}/${encodeURIComponent(id)}`;
}

// --------------------------------------------------------------------------
// GitOps application health
// --------------------------------------------------------------------------

/**
 * The one bucket an application lands in, worst first.
 *
 * Six, mutually exclusive, and ordered by what an operator should look at
 * first:
 *
 * - `degraded` -- the deployed workload is unhealthy. Worst, and it outranks
 *   `outofsync` deliberately: a drifted manifest is a difference between git
 *   and the cluster, while a degraded application is something that is running
 *   and broken right now.
 * - `outofsync` -- git and the cluster disagree. Folds in `failed` and
 *   `stalled`, which is exactly the grouping the server's own summary uses, so
 *   this card and the applications page never disagree about the same cluster.
 * - `progressing` -- a reconcile is in flight, from either field. Not a fault.
 * - `suspended` -- reconciliation is switched off. Not a fault either, but not
 *   `synced`: nothing is checking, so "git and the cluster agree" is a claim
 *   no one is making.
 * - `unknown` -- a status this build did not recognise, or an application it
 *   could not read a status from. Ranks ABOVE `synced` because an unreadable
 *   state is not a healthy one; rounding it toward `synced` is the same defect
 *   class as rendering an absent operator as good news.
 * - `synced` -- synced and healthy. The only bucket that claims anything good.
 */
export const APP_HEALTH_BUCKETS = [
  "degraded",
  "outofsync",
  "progressing",
  "suspended",
  "unknown",
  "synced",
] as const;
export type AppHealthBucket = (typeof APP_HEALTH_BUCKETS)[number];

/** The buckets the card turns into rows: the ones an operator acts on. */
const ATTENTION_BUCKETS: readonly AppHealthBucket[] = ["degraded", "outofsync"];

/** The three sync statuses the server counts as out-of-sync. Verbatim from
 * `computeMetadata`; splitting them here would put this card at odds with the
 * applications page over the same application. */
const OUT_OF_SYNC_STATUSES = new Set(["outofsync", "failed", "stalled"]);

function bucketRank(bucket: AppHealthBucket): number {
  return APP_HEALTH_BUCKETS.indexOf(bucket);
}

/**
 * The single bucket an application belongs in.
 *
 * Reads the payload's own `syncStatus` and `healthStatus` and nothing else --
 * no re-derivation from conditions, no thresholds. The ordering of the tests
 * below IS the severity ordering documented on `APP_HEALTH_BUCKETS`.
 */
export function classifyApp(entry: unknown): AppHealthBucket {
  const item = obj(entry);
  if (item === null) return "unknown";

  const sync = str(item.syncStatus);
  const health = str(item.healthStatus);

  if (health === "degraded") return "degraded";
  if (OUT_OF_SYNC_STATUSES.has(sync)) return "outofsync";
  if (sync === "progressing" || health === "progressing") return "progressing";
  if (health === "suspended") return "suspended";
  if (sync === "synced" && health === "healthy") return "synced";
  return "unknown";
}

export interface AppHealthRow {
  /** The feature's composite id, and the row's link target. */
  id: string;
  name: string;
  namespace: string;
  /** `argocd`, `fluxcd`, or whatever else the payload named. Carried per row
   * so the rendering can badge each one: the roll-up spans both tools, and an
   * operator reading "3 out of sync" needs to know which tool to open. */
  tool: string;
  syncStatus: string;
  healthStatus: string;
  bucket: AppHealthBucket;
  message: string;
  href: string;
}

/** One tool's share of the fleet. Presentation only; the counts above are the
 * roll-up and they span both tools. */
export interface ToolTally {
  tool: string;
  total: number;
}

export interface GitOpsAppHealthView {
  /** The body was the route's envelope. False means this build could not parse
   * it, which is emphatically not "no applications". */
  readable: boolean;
  /** Applications this card could read. `counts` partitions exactly this. */
  total: number;
  counts: Readonly<Record<AppHealthBucket, number>>;
  /** Applications in a bucket worth opening the page for. */
  attention: number;
  tools: ToolTally[];
  /** The worst applications, capped at the caller's limit. */
  rows: AppHealthRow[];
  /** Entries that named no application. Reported rather than dropped: a card
   * silently describing fewer applications than the cluster has is the failure
   * mode this counter exists to make visible. */
  unreadableRows: number;
}

function emptyCounts(): Record<AppHealthBucket, number> {
  return {
    degraded: 0,
    outofsync: 0,
    progressing: 0,
    suspended: 0,
    unknown: 0,
    synced: 0,
  };
}

function emptyAppHealthView(readable: boolean): GitOpsAppHealthView {
  return {
    readable,
    total: 0,
    counts: emptyCounts(),
    attention: 0,
    tools: [],
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * Rolls `/v1/gitops/applications` into one bucket per application.
 *
 * `data` is the route's envelope -- `{ applications, summary }` -- and NOT the
 * bare list. The `summary` half is deliberately unused: see this module's
 * header for why its counters cannot be added up.
 */
export function gitopsAppHealthView(
  data: unknown,
  limit: number,
): GitOpsAppHealthView {
  const items = envelopeList(data, "applications");
  if (items === null) return emptyAppHealthView(false);

  const view = emptyAppHealthView(true);
  const counts = emptyCounts();
  const toolTotals = new Map<string, number>();
  const rows: AppHealthRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const bucket = classifyApp(item);
    counts[bucket]++;

    const tool = str(item.tool);
    toolTotals.set(tool, (toolTotals.get(tool) ?? 0) + 1);

    if (!ATTENTION_BUCKETS.includes(bucket)) continue;

    const id = str(item.id);
    rows.push({
      id,
      name,
      namespace: str(item.namespace),
      tool,
      syncStatus: str(item.syncStatus),
      healthStatus: str(item.healthStatus),
      bucket,
      message: str(item.message),
      href: gitopsAppHref(id),
    });
  }

  rows.sort(
    (a, b) =>
      bucketRank(a.bucket) - bucketRank(b.bucket) ||
      a.name.localeCompare(b.name) ||
      a.namespace.localeCompare(b.namespace),
  );

  view.counts = counts;
  view.attention = ATTENTION_BUCKETS.reduce((n, b) => n + counts[b], 0);
  view.tools = [...toolTotals.entries()]
    .map(([tool, total]) => ({ tool, total }))
    .sort((a, b) => a.tool.localeCompare(b.tool));
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// Recent syncs
// --------------------------------------------------------------------------

/** A bare commit sha, at the lengths the backend's own `shaPattern` accepts. */
const SHA_PATTERN = /^[0-9a-f]{7,40}$/i;

/** How many characters of a sha a row shows. Seven is what `git log --oneline`
 * and both tools' own UIs print. */
const SHA_DISPLAY_LENGTH = 7;

/**
 * A revision, shortened for a four-line card, or "" when there is none.
 *
 * Three shapes reach this, and truncating blindly would mangle two of them:
 *
 * - A bare sha -- Argo CD's `status.sync.revision` -- shortens to seven.
 * - Flux's `{branch}@sha1:{sha}`, where a blind seven-character truncation
 *   would print `main@sh`. The branch is the useful half, so it is kept whole
 *   and only the sha is shortened.
 * - A tag or chart version -- `v1.4.2`, `18.1.5` -- which is already short and
 *   means nothing truncated. Left exactly as it arrived.
 *
 * NOT a commit MESSAGE. `/v1/gitops/commits` is what carries those, and it
 * needs a repository URL and a set of shas to answer, so it cannot be this
 * card's data source (see `gitopsRecentSyncsView`).
 */
export function shortRevision(value: unknown): string {
  const raw = str(value).trim();
  if (raw === "") return "";

  const at = raw.lastIndexOf("@");
  if (at > 0) {
    const head = raw.slice(0, at);
    let tail = raw.slice(at + 1);
    // `sha1:` / `sha256:` -- the digest algorithm Flux names before the digest.
    const colon = tail.indexOf(":");
    if (colon !== -1) tail = tail.slice(colon + 1);
    return SHA_PATTERN.test(tail)
      ? `${head}@${tail.slice(0, SHA_DISPLAY_LENGTH)}`
      : raw;
  }

  return SHA_PATTERN.test(raw) ? raw.slice(0, SHA_DISPLAY_LENGTH) : raw;
}

export interface RecentSyncRow {
  id: string;
  name: string;
  namespace: string;
  tool: string;
  syncStatus: string;
  healthStatus: string;
  /** The payload's `lastSyncTime`, verbatim, for a `<time datetime>`. */
  syncedAt: string;
  /** The same instant in epoch milliseconds, which is what the ranking uses.
   * Never null on a row -- an application with no readable sync time is not
   * ranked at all. */
  syncedAtMs: number;
  /** The shortened revision, or "" when the payload carried none. */
  revision: string;
  message: string;
  href: string;
}

export interface GitOpsRecentSyncsView {
  readable: boolean;
  /** Applications this card could read, whether or not they have ever synced. */
  total: number;
  /** Applications carrying no readable `lastSyncTime`. Reported rather than
   * ranked: putting an application that has never synced at the top of a
   * "recent syncs" list, or at the bottom dated the epoch, are both
   * inventions. */
  untimed: number;
  rows: RecentSyncRow[];
  unreadableRows: number;
}

function emptyRecentSyncsView(readable: boolean): GitOpsRecentSyncsView {
  return { readable, total: 0, untimed: 0, rows: [], unreadableRows: 0 };
}

/**
 * Recent sync activity, off the applications payload alone.
 *
 * Commit enrichment is deliberately NOT a source here. `/v1/gitops/commits`
 * requires a repository URL and a set of shas -- so it can only be asked AFTER
 * this list is in hand -- and it answers with a neutral empty shape when the
 * deployment has no Git provider token configured, which is the default. A
 * card whose rows depended on it would be blank on most clusters. Everything a
 * row shows therefore comes off the applications payload: which application,
 * managed by which tool, synced when, to which revision, and with what result.
 * Enrichment, if it is ever added, layers a commit title onto a row that
 * already reads correctly without one.
 */
export function gitopsRecentSyncsView(
  data: unknown,
  limit: number,
): GitOpsRecentSyncsView {
  const items = envelopeList(data, "applications");
  if (items === null) return emptyRecentSyncsView(false);

  const view = emptyRecentSyncsView(true);
  const rows: RecentSyncRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const syncedAt = str(item.lastSyncTime);
    const ms = syncedAt === "" ? Number.NaN : Date.parse(syncedAt);
    if (!Number.isFinite(ms)) {
      view.untimed++;
      continue;
    }

    const id = str(item.id);
    rows.push({
      id,
      name,
      namespace: str(item.namespace),
      tool: str(item.tool),
      syncStatus: str(item.syncStatus),
      healthStatus: str(item.healthStatus),
      syncedAt,
      syncedAtMs: ms,
      revision: shortRevision(item.currentRevision),
      message: str(item.message),
      href: gitopsAppHref(id),
    });
  }

  rows.sort(
    (a, b) => b.syncedAtMs - a.syncedAtMs || a.name.localeCompare(b.name),
  );
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// mTLS coverage
// --------------------------------------------------------------------------

/**
 * The postures a workload can be in, worst first.
 *
 * The first four are the backend's own `MTLSState`. `unknown` is this module's
 * name for a fifth value this build does not recognise, and it is counted
 * apart from all four rather than rounded into one: folding it into `active`
 * would report an unreadable posture as an enforced one, and folding it into
 * `inactive` would raise an alarm about something nobody can act on.
 *
 * `unmeshed` is an opt-out, not a failure -- a workload outside the mesh has
 * no mTLS posture to enforce -- which is why it is outside the coverage
 * fraction entirely rather than sitting in its denominator. See `meshed`.
 */
export const MTLS_POSTURES = [
  "inactive",
  "mixed",
  "active",
  "unmeshed",
  "unknown",
] as const;
export type MtlsPosture = (typeof MTLS_POSTURES)[number];

/** The postures that are inside the mesh and therefore inside the fraction. */
const MESHED_POSTURES: readonly MtlsPosture[] = ["active", "mixed", "inactive"];

/** The postures a row is worth showing for: meshed and not strict. */
const NON_STRICT_POSTURES: readonly MtlsPosture[] = ["inactive", "mixed"];

function postureOf(value: unknown): MtlsPosture {
  const raw = str(value);
  return (MTLS_POSTURES as readonly string[]).includes(raw) && raw !== "unknown"
    ? (raw as MtlsPosture)
    : "unknown";
}

/**
 * A coverage fraction as a whole percent, or null when there is nothing to
 * report.
 *
 * Plain rounding is wrong at both ends and wrong in the reassuring direction
 * at one of them. One strict workload in three hundred is 0.33%, which
 * `Math.round` prints as `0` -- a card claiming NOTHING enforces strict mTLS
 * while something does. 299 of 300 is 99.67%, which rounds to `100` -- a card
 * claiming a fully strict mesh that has a permissive workload in it, which is
 * exactly the false reassurance this release exists to prevent.
 *
 * So 0 and 100 are reserved for the absolutes and everything else is clamped
 * into 1..99. The residual imprecision is a percent, and it is spent buying
 * back the only two values a reader treats as categorical.
 */
export function coveragePercent(value: number | null): number | null {
  if (value === null || !Number.isFinite(value)) return null;
  if (value <= 0) return 0;
  if (value >= 1) return 100;
  return Math.min(99, Math.max(1, Math.round(value * 100)));
}

export interface MtlsRow {
  namespace: string;
  workload: string;
  /** Authoritative only when `confident` is true. */
  workloadKind: string;
  mesh: string;
  posture: MtlsPosture;
  /** The raw `state` string, for a badge that maps the backend's own values. */
  state: string;
  /** `policy`, `metric` or `default` -- where the verdict came from. */
  source: string;
  /** False when the workload kind was inferred from a ReplicaSet name rather
   * than an owner reference. The mesh pages mark these; so does the card. */
  confident: boolean;
}

export interface MtlsCoverageView {
  readable: boolean;
  /** Every workload the route returned, meshed or not. */
  total: number;
  /** Workloads inside the mesh: the coverage fraction's denominator. */
  meshed: number;
  counts: Readonly<Record<MtlsPosture, number>>;
  /**
   * Strict workloads as a fraction of meshed ones, or null when none is
   * meshed.
   *
   * Null rather than zero, and the distinction is the card's: "no workload is
   * meshed" and "no meshed workload enforces strict mTLS" are different
   * sentences about different clusters, and only the second is a finding.
   */
  coverage: number | null;
  percent: number | null;
  /** The meshed, non-strict workloads: what keeps the fraction below one. */
  rows: MtlsRow[];
  unreadableRows: number;
  /**
   * The route's partial-failure keys, sorted.
   *
   * The pod list is capped server-side and the Prometheus cross-check can
   * fail, and either leaves a posture table that is real but incomplete. A
   * percentage computed over a truncated list and printed without that caveat
   * is this card's own version of absence-as-good-news, so the keys are
   * carried up for the card to disclose.
   */
  partial: string[];
}

function emptyPostureCounts(): Record<MtlsPosture, number> {
  return { inactive: 0, mixed: 0, active: 0, unmeshed: 0, unknown: 0 };
}

function emptyMtlsView(readable: boolean): MtlsCoverageView {
  return {
    readable,
    total: 0,
    meshed: 0,
    counts: emptyPostureCounts(),
    coverage: null,
    percent: null,
    rows: [],
    unreadableRows: 0,
    partial: [],
  };
}

/**
 * Rolls `/v1/mesh/mtls` into a cluster-wide coverage posture.
 *
 * `data` is the route's envelope -- `{ status, workloads, errors }`. The
 * `status` half is not read here: availability is the shell's decision, taken
 * from `mesh-status` before `render` is called (KTD1), and a second copy of
 * that reading inside the card is a second place for it to disagree.
 *
 * The request behind this carries no namespace, which the route treats as a
 * cluster-scoped read (KTD4). The cluster-wide posture is the more useful
 * default for an overview card, and it is the one an operator cannot
 * reconstruct from the per-namespace pages without visiting all of them.
 */
export function mtlsCoverageView(
  data: unknown,
  limit: number,
): MtlsCoverageView {
  const items = envelopeList(data, "workloads");
  if (items === null) return emptyMtlsView(false);

  const view = emptyMtlsView(true);
  const counts = emptyPostureCounts();
  const rows: MtlsRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.workload);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const posture = postureOf(item.state);
    counts[posture]++;

    if (!NON_STRICT_POSTURES.includes(posture)) continue;

    rows.push({
      namespace: str(item.namespace),
      workload: name,
      workloadKind: str(item.workloadKind),
      mesh: str(item.mesh),
      posture,
      state: str(item.state),
      source: str(item.source),
      confident: item.workloadKindConfident === true,
    });
  }

  rows.sort(
    (a, b) =>
      MTLS_POSTURES.indexOf(a.posture) - MTLS_POSTURES.indexOf(b.posture) ||
      a.namespace.localeCompare(b.namespace) ||
      a.workload.localeCompare(b.workload),
  );

  const meshed = MESHED_POSTURES.reduce((n, p) => n + counts[p], 0);
  const coverage = meshed === 0 ? null : counts.active / meshed;

  view.counts = counts;
  view.meshed = meshed;
  view.coverage = coverage;
  view.percent = coveragePercent(coverage);
  view.rows = rows.slice(0, Math.max(0, limit));

  const errors = obj(obj(data)?.errors);
  view.partial = errors === null ? [] : Object.keys(errors).sort();
  return view;
}
