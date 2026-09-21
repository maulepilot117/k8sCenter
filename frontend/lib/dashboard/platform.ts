/**
 * The derived state behind the five platform widgets: `cluster-status`,
 * `notifications-feed`, `audit-activity`, `saved-views` and
 * `pinned-resources`.
 *
 * What these five share with the rest of lib/dashboard is a roll-up that ranks
 * by badness rather than counting by category -- a dashboard card is four
 * lines tall, so the rows it can afford to show have to be the ones worth
 * opening it for -- and totality: every argument may be null, missing or the
 * wrong shape, and none of it throws (D-10, KTD8).
 *
 * What they do NOT share with the CRD-backed families is the shell's
 * availability gate. None of these five is CRD-discovered, so none of them
 * declares a `familyStatus`: the thing that can be missing here is the
 * DEPLOYMENT's database rather than an operator in the cluster, and the routes
 * say so with a 503 that `ABSENT_STATUSES` in types.ts maps to the same
 * unavailable state (R1). By the time a function below is called, the read has
 * succeeded.
 *
 * So the one absence these functions carry themselves is an unreadable body.
 * "You have no pinned resources" and "this build could not read your pins" are
 * opposite sentences and only the first is reassuring, which is why every view
 * here reports `readable` rather than flattening a body it cannot parse into
 * an empty list.
 */
import { RESOURCE_DETAIL_PATHS } from "@/lib/constants.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { list, num, obj, str } from "./narrow.ts";
import { coverage } from "./page-coverage.ts";

// --------------------------------------------------------------------------
// Shared readers
//
// The same four this module's neighbours use (expiry.ts, sync-state.ts).
// Copied rather than hoisted for the reason those two kept their own: they are
// four lines each, and a shared "readers" module would be imported by every
// view file in lib/dashboard for no behaviour.
// --------------------------------------------------------------------------

// --------------------------------------------------------------------------
// cluster-status
// --------------------------------------------------------------------------

/**
 * The multi-cluster registry page.
 *
 * `/settings/clusters`, NOT `/admin/clusters` -- the latter appears in
 * lib/notif-action.ts and is not a route this build serves.
 */
export const CLUSTERS_PAGE_HREF = "/settings/clusters";

/**
 * Worst first, which is also the order the card stacks its rows in.
 *
 * The first four are `ClusterStatus` in backend/internal/k8s/cluster_prober.go
 * verbatim, whose own docstring calls those four constants the sole typed
 * source of truth. `unknown` is this module's addition and is NOT a fifth
 * backend state: it is where a status string this build does not recognise
 * lands, including the empty one a row carries before the prober has ever run.
 *
 * It ranks above `connected` deliberately. A state we could not read is not
 * evidence a cluster is reachable, and reporting it as reachable is the one
 * error an operator will not go looking for.
 */
export const CLUSTER_STATES = [
  "error",
  "blocked",
  "disconnected",
  "unknown",
  "connected",
] as const;
export type ClusterState = (typeof CLUSTER_STATES)[number];

const CLUSTER_STATE_SET: ReadonlySet<string> = new Set(CLUSTER_STATES);

export interface ClusterRow {
  id: string;
  name: string;
  state: ClusterState;
  /** The prober's own explanation, or "" when it had none. */
  statusMessage: string;
  isLocal: boolean;
  /** Null when the record carried no readable count, which is not zero nodes. */
  nodeCount: number | null;
  version: string;
}

export interface ClusterStatusView {
  /** False when the body was not a list. An empty list is readable -- a
   * deployment with a database always has at least the local cluster, but a
   * registry that has been emptied is still a registry. */
  readable: boolean;
  /** Registered clusters this account can see, including the local one. */
  total: number;
  counts: Record<ClusterState, number>;
  /** The worst state present, or null when nothing readable was returned. */
  worst: ClusterState | null;
  /** Worst first, capped. Ties keep the route's own order, which is local
   * first and then by name. */
  rows: ClusterRow[];
  /** Entries with no readable identity, which are not rendered. */
  unreadableRows: number;
}

function emptyClusterView(readable: boolean): ClusterStatusView {
  return {
    readable,
    total: 0,
    counts: {
      error: 0,
      blocked: 0,
      disconnected: 0,
      unknown: 0,
      connected: 0,
    },
    worst: null,
    rows: [],
    unreadableRows: 0,
  };
}

function clusterState(value: unknown): ClusterState {
  const raw = str(value);
  return CLUSTER_STATE_SET.has(raw) ? (raw as ClusterState) : "unknown";
}

/**
 * The fleet's reachability, as the registry last recorded it.
 *
 * These are the PROBER's verdicts rather than live checks -- ClusterProber
 * writes each cluster's status every 60s -- so the card reports what the
 * registry believes, which is the same thing the clusters page shows.
 */
export function clusterStatusView(
  data: unknown,
  limit: number,
): ClusterStatusView {
  const items = list(data);
  if (items === null) return emptyClusterView(false);

  const view = emptyClusterView(true);
  const rows: ClusterRow[] = [];

  for (const item of items) {
    const rec = obj(item);
    const id = str(rec?.id);
    const name = str(rec?.name) || str(rec?.displayName) || id;
    if (rec === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    const state = clusterState(rec.status);
    view.counts[state]++;
    view.total++;
    rows.push({
      id: id === "" ? name : id,
      name,
      state,
      statusMessage: str(rec.statusMessage),
      isLocal: rec.isLocal === true,
      nodeCount: num(rec.nodeCount),
      version: str(rec.k8sVersion),
    });
  }

  // A stable sort by rank, so ties keep the order the route returned: the
  // query is `ORDER BY is_local DESC, name ASC`, and re-sorting equals by name
  // here would silently disagree with the clusters page about two clusters in
  // the same state.
  const rank = (s: ClusterState) => CLUSTER_STATES.indexOf(s);
  rows.sort((a, b) => rank(a.state) - rank(b.state));

  view.worst = rows.length === 0 ? null : rows[0].state;
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// notifications-feed
// --------------------------------------------------------------------------

/**
 * The in-app feed every authenticated account can open.
 *
 * `/notifications`, not `/admin/notifications` -- the admin surface adds
 * channel and rule management and is gated, and this card is offered to
 * everyone.
 */
export const NOTIFICATIONS_PAGE_HREF = "/notifications";

export interface NotificationRow {
  id: string;
  severity: string;
  source: string;
  title: string;
  message: string;
  createdAt: string;
}

export interface NotificationsFeedView {
  /** False when the envelope carried no list. */
  readable: boolean;
  /**
   * Unread notifications this account can see -- the SERVER's count of the
   * whole unread population, not the length of the page below it. The compact
   * rendering is only this number, so a page length here would under-report
   * on exactly the account that most needs the badge.
   */
  unread: number;
  /** `unread` exceeds what this page carried. */
  truncated: boolean;
  counts: { critical: number; warning: number; info: number; other: number };
  /** Worst first, capped. */
  rows: NotificationRow[];
  unreadableRows: number;
}

/** Worst first, matching the severity vocabulary in lib/notif-center-types.ts.
 * Anything else is `other`, which ranks last: an unrecognised severity is not
 * a critical one, and promoting it would make the card shout about a build
 * mismatch. */
const NOTIF_SEVERITIES = ["critical", "warning", "info"] as const;

function emptyFeedView(readable: boolean): NotificationsFeedView {
  return {
    readable,
    unread: 0,
    truncated: false,
    counts: { critical: 0, warning: 0, info: 0, other: 0 },
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * The unread feed, and how much of it this page carried.
 *
 * `data` is the ENVELOPE the fetcher keeps -- `{ items, total }` -- rather
 * than a bare list, because the count beside the rows has to be the server's
 * and not this page's. See `unread-notifications` in data.ts.
 */
export function notificationsFeedView(
  data: unknown,
  limit: number,
): NotificationsFeedView {
  const envelope = obj(data);
  const items = list(envelope?.items);
  if (items === null) return emptyFeedView(false);

  const view = emptyFeedView(true);
  const rows: NotificationRow[] = [];

  for (const item of items) {
    const rec = obj(item);
    const title = str(rec?.title);
    if (rec === null || title === "") {
      view.unreadableRows++;
      continue;
    }
    const severity = str(rec.severity);
    if ((NOTIF_SEVERITIES as readonly string[]).includes(severity)) {
      view.counts[severity as (typeof NOTIF_SEVERITIES)[number]]++;
    } else {
      view.counts.other++;
    }
    rows.push({
      id: str(rec.id),
      severity,
      source: str(rec.source),
      title,
      message: str(rec.message),
      createdAt: str(rec.createdAt),
    });
  }

  const rank = (s: string) => {
    const at = (NOTIF_SEVERITIES as readonly string[]).indexOf(s);
    return at === -1 ? NOTIF_SEVERITIES.length : at;
  };
  rows.sort((a, b) => rank(a.severity) - rank(b.severity));

  // The same rule `page-coverage.ts` applies to the generic list route, and
  // the same fallback: a total that is missing or not a finite number reports
  // the page as complete, because a card claiming to be a sample of an unknown
  // population tells the reader nothing they can act on. NaN rather than a
  // guess, so `coverage` itself makes that call rather than this line.
  const cov = coverage(
    { items, total: num(envelope?.total) ?? Number.NaN },
    items.length - view.unreadableRows,
  );
  view.unread = cov.total;
  view.rows = rows.slice(0, Math.max(0, limit));
  view.truncated = cov.total > view.rows.length;
  return view;
}

// --------------------------------------------------------------------------
// audit-activity
// --------------------------------------------------------------------------

/**
 * The audit log page.
 *
 * `/settings/audit`, NOT `/admin/audit` -- the latter appears in
 * lib/notif-action.ts and is not a route this build serves.
 */
export const AUDIT_PAGE_HREF = "/settings/audit";

export interface AuditRow {
  timestamp: string;
  user: string;
  action: string;
  result: string;
  /** `kind/namespace/name`, or `kind/name` for a cluster-scoped object, or ""
   * when the entry named no object (a login, say). */
  target: string;
  detail: string;
}

export interface AuditActivityView {
  readable: boolean;
  total: number;
  /** The three `audit.Result` values, counted over everything the page
   * carried. A page rather than the whole log: the route is paginated and
   * this card asks for one small page. */
  counts: { success: number; failure: number; denied: number; other: number };
  rows: AuditRow[];
  unreadableRows: number;
}

const AUDIT_RESULTS = ["success", "failure", "denied"] as const;

function emptyAuditView(readable: boolean): AuditActivityView {
  return {
    readable,
    total: 0,
    counts: { success: 0, failure: 0, denied: 0, other: 0 },
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * The most recent audited actions, in the order the route returned them.
 *
 * Deliberately NOT re-sorted. The handler orders by timestamp descending and
 * the audit page renders that order; ranking by result here would put a denied
 * action from yesterday above a successful one from a minute ago and make the
 * two surfaces disagree about the same rows.
 */
export function auditActivityView(
  data: unknown,
  limit: number,
): AuditActivityView {
  const items = list(data);
  if (items === null) return emptyAuditView(false);

  const view = emptyAuditView(true);

  for (const item of items) {
    const rec = obj(item);
    const action = str(rec?.action);
    if (rec === null || action === "") {
      view.unreadableRows++;
      continue;
    }
    const result = str(rec.result);
    if ((AUDIT_RESULTS as readonly string[]).includes(result)) {
      view.counts[result as (typeof AUDIT_RESULTS)[number]]++;
    } else {
      view.counts.other++;
    }
    view.total++;
    if (view.rows.length < Math.max(0, limit)) {
      const kind = str(rec.resourceKind);
      const namespace = str(rec.resourceNamespace);
      const name = str(rec.resourceName);
      view.rows.push({
        timestamp: str(rec.timestamp),
        user: str(rec.user),
        action,
        result,
        target: [kind, namespace, name].filter((s) => s !== "").join("/"),
        detail: str(rec.detail),
      });
    }
  }

  return view;
}

// --------------------------------------------------------------------------
// saved-views and pinned-resources
// --------------------------------------------------------------------------

/**
 * Where an operator with neither a saved view nor a pin is sent.
 *
 * Neither preference has a page of its own: a saved view is created from the
 * dropdown above a resource table and a pin from the control on a resource
 * detail page, so "where you create one" is a resource list. Pods is the list
 * every cluster has and the one both affordances are reachable from.
 */
export const PREFERENCES_START_HREF = "/workloads/pods";

/** The list page for a stored resource kind, or null when this build routes no
 * such kind. Both preferences store the ADAPTER SLUG (already plural), which
 * is the form `RESOURCE_DETAIL_PATHS` is keyed by -- so no pluralisation is
 * attempted here, for the reason `resourceHref` gives about pins. */
function kindListHref(kind: string): string | null {
  return RESOURCE_DETAIL_PATHS[kind.toLowerCase()] ?? null;
}

export interface SavedViewRow {
  id: string;
  name: string;
  kind: string;
  /** "" means the view spans every namespace. */
  namespace: string;
  /** Null when this build routes no page for the kind, which renders as text
   * rather than as a link that would 404. */
  href: string | null;
}

export interface SavedViewsView {
  readable: boolean;
  total: number;
  rows: SavedViewRow[];
  unreadableRows: number;
}

/**
 * The caller's saved table views, across every cluster.
 *
 * Not filtered by the active cluster, unlike the pins below. A saved view
 * describes a SCOPE -- a kind, a namespace, a filter -- which a different
 * cluster could in principle satisfy, where a pin names one object in one
 * cluster and cannot be opened from anywhere else. That distinction is
 * `pinsForActiveCluster`'s in src/lib/pin-store.ts, and it is honoured here
 * rather than re-decided.
 */
export function savedViewsView(data: unknown, limit: number): SavedViewsView {
  const items = list(data);
  if (items === null) {
    return { readable: false, total: 0, rows: [], unreadableRows: 0 };
  }

  const view: SavedViewsView = {
    readable: true,
    total: 0,
    rows: [],
    unreadableRows: 0,
  };

  for (const item of items) {
    const rec = obj(item);
    const name = str(rec?.name);
    if (rec === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;
    if (view.rows.length < Math.max(0, limit)) {
      const config = obj(rec.config);
      const kind = str(config?.resourceKind);
      view.rows.push({
        id: str(rec.id),
        name,
        kind,
        namespace: str(config?.namespace),
        href: kind === "" ? null : kindListHref(kind),
      });
    }
  }

  return view;
}

export interface PinnedRow {
  id: string;
  name: string;
  /** The stored display kind ("Deployment"), falling back to the slug. */
  kind: string;
  namespace: string;
  href: string | null;
}

export interface PinnedResourcesView {
  readable: boolean;
  /** Pins addressed to the ACTIVE cluster. */
  total: number;
  /**
   * Pins the caller holds on other clusters, which are withheld rather than
   * shown. Counted because withholding them silently would say "you have no
   * pinned resources" to someone holding twenty.
   */
  otherClusters: number;
  rows: PinnedRow[];
  unreadableRows: number;
}

/**
 * The caller's pinned objects on the cluster being viewed.
 *
 * A pin names one object in one cluster, so a pin stored against another
 * cluster cannot be opened from here and is filtered out -- the rule
 * `pinsForActiveCluster` states in src/lib/pin-store.ts, applied to the same
 * payload. A record carrying no cluster at all is treated as belonging to
 * another one: it is not evidence of this one.
 *
 * Like the sidebar, this shows STORED IDENTITY ONLY. It does not fetch the
 * objects to see whether they still exist or whether the caller can still read
 * them; that happens on the detail page, which is one click away.
 */
export function pinnedResourcesView(
  data: unknown,
  clusterId: string,
  limit: number,
): PinnedResourcesView {
  const items = list(data);
  if (items === null) {
    return {
      readable: false,
      total: 0,
      otherClusters: 0,
      rows: [],
      unreadableRows: 0,
    };
  }

  const view: PinnedResourcesView = {
    readable: true,
    total: 0,
    otherClusters: 0,
    rows: [],
    unreadableRows: 0,
  };

  for (const item of items) {
    const rec = obj(item);
    const config = obj(rec?.config);
    const name = str(config?.name) || str(rec?.name);
    if (rec === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    if (str(rec.clusterId) !== clusterId) {
      view.otherClusters++;
      continue;
    }
    view.total++;
    if (view.rows.length < Math.max(0, limit)) {
      const kind = str(config?.resourceKind);
      const namespace = str(config?.namespace);
      view.rows.push({
        id: str(rec.id),
        name,
        kind: str(config?.displayKind) || kind,
        namespace,
        href: kind === "" ? null : resourceHref(kind, namespace, name),
      });
    }
  }

  return view;
}
