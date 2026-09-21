/**
 * The derived state behind the four data-protection widgets: `certs-expiring`,
 * `eso-health`, `velero-backups` and `snapshot-health`.
 *
 * What the four share is a roll-up that ranks by badness rather than counting
 * by category. A dashboard card is four lines tall; the rows it can afford to
 * show have to be the ones worth opening it for, so "which of these is worst"
 * is the judgement every function below makes, and it is not a field read
 * (D-10, KTD8).
 *
 * What they do NOT share is a definition of "soon". Certificate expiry is
 * classified against thresholds the SERVER resolved per certificate -- walking
 * the annotation chain from the certificate up to its issuer and then to the
 * cluster-issuer, and falling back to cert-manager's own defaults when the
 * resolved pair contradicts itself (`resolveCertThresholdsDetailed` in
 * backend/internal/certmanager/thresholds.go). Those numbers arrive on the
 * payload. This module reads them; it does not know what the defaults are and
 * must not learn, because a browser-side copy of 30/7 would silently
 * contradict every cluster that annotates its issuers -- and would do it
 * quietly, in the direction of "this certificate is fine".
 *
 * What is NOT here is availability. Whether cert-manager, ESO, Velero or the
 * snapshot CRDs are installed at all is answered by that family's own
 * discovery route and resolved by the shell before `render` is called, so
 * every function below is reached only on a cluster that runs the feature
 * (KTD1, R1). That is what lets an empty list here mean "nothing to report"
 * and say so plainly: the other reading -- "no operator" -- is not
 * representable at this point.
 *
 * Total, in the sense the rest of lib/dashboard is total: every argument may
 * be null, missing or the wrong shape, and none of it throws. One unreadable
 * item degrades to a counter rather than blanking a card describing the
 * readable rest.
 */
import { getPhaseCategory } from "@/lib/velero-types.ts";

// --------------------------------------------------------------------------
// Shared readers
// --------------------------------------------------------------------------

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/**
 * A finite number, or null.
 *
 * Null rather than zero throughout this module: a day count we could not read
 * is not a certificate expiring today, and an error count we could not read is
 * not an absence of errors. Every consumer branches on the difference instead
 * of reporting a guess as a fact.
 */
function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function count(value: unknown): number {
  const n = num(value);
  return n !== null && n > 0 ? Math.floor(n) : 0;
}

/** The payload of a list route, or null when the body is not a list at all.
 * Not `[]`: a body this build cannot read has to stay distinguishable from an
 * empty one, which is the whole difference between "nothing to report" and
 * "we could not tell". */
function list(data: unknown): unknown[] | null {
  return Array.isArray(data) ? data : null;
}

/** Every path segment is encoded. A Kubernetes name cannot contain a slash,
 * but these are where a payload value becomes a URL and the narrow fix costs
 * nothing. */
function seg(value: string): string {
  return encodeURIComponent(value);
}

// --------------------------------------------------------------------------
// Expiry classification
// --------------------------------------------------------------------------

/** Worst first, which is also the order the certificate card stacks them in. */
export const EXPIRY_CLASSES = ["critical", "warning", "healthy"] as const;
export type ExpiryClass = (typeof EXPIRY_CLASSES)[number];

/**
 * Where a certificate sits against the thresholds the server resolved for it.
 *
 * The boundaries are the backend's own, verbatim: `HandleListExpiring` drops a
 * certificate when `*days > warn`, calls the remainder "warning", and promotes
 * to "critical" when `*days <= crit`. Both comparisons are inclusive, so a
 * certificate with exactly `crit` days left is critical and one with exactly
 * `warn` days left is warning. A card using `<` on either edge would disagree
 * with the certificates page about the same certificate on the same day.
 *
 * `warnDays` and `criticalDays` are arguments rather than constants because
 * they are per-certificate facts the payload carries. The annotation
 * resolution chain and its conflict handling live on the server; a certificate
 * whose resolved pair contradicted itself arrives here already carrying
 * cert-manager's defaults and a `thresholdConflict` flag, and honouring the
 * flag by recomputing anything would put this card at odds with both the
 * server and the certificates page.
 *
 * Returns null when any of the three values is unreadable, or when a threshold
 * is not positive. Null rather than "healthy": a certificate whose expiry we
 * could not read is not a certificate that is fine, and folding it into the
 * healthy bucket is the same defect class as rendering an absent operator as
 * good news.
 *
 * `crit >= warn` cannot reach here from a well-behaved server -- that is
 * exactly the conflict it resolves away -- but if it ever did, the critical
 * test runs first and the certificate reads as critical. Erring loud.
 */
export function classifyExpiry(
  daysRemaining: unknown,
  warnDays: unknown,
  criticalDays: unknown,
): ExpiryClass | null {
  const days = num(daysRemaining);
  const warn = num(warnDays);
  const crit = num(criticalDays);
  if (days === null || warn === null || crit === null) return null;
  if (warn <= 0 || crit <= 0) return null;
  if (days <= crit) return "critical";
  if (days <= warn) return "warning";
  return "healthy";
}

// --------------------------------------------------------------------------
// certs-expiring
// --------------------------------------------------------------------------

/** The full page the certificate card links to (R7). */
export const CERTIFICATES_PAGE_HREF = "/security/certificates";

export function certificateHref(namespace: string, name: string): string {
  return `${CERTIFICATES_PAGE_HREF}/${seg(namespace)}/${seg(name)}`;
}

export interface CertExpiryRow {
  namespace: string;
  name: string;
  daysRemaining: number;
  class: ExpiryClass;
  /** The thresholds the SERVER resolved for this certificate, carried through
   * so the card can attribute its own classification rather than implying a
   * cluster-wide rule that may not exist. */
  warnDays: number;
  criticalDays: number;
  /** The server found this certificate's resolved critical threshold no
   * stricter than its warning one and fell back to cert-manager's defaults.
   * Presentational here: the classification above already reflects it. */
  thresholdConflict: boolean;
}

export interface CertExpiryView {
  /** The certificates route answered with something this build can read. An
   * empty list is readable -- a cluster running cert-manager with no
   * certificates. */
  readable: boolean;
  /** Certificates the payload named. */
  total: number;
  critical: number;
  warning: number;
  healthy: number;
  /** Certificates whose expiry or thresholds the payload did not carry. Not
   * healthy, and counted apart so the card can say so. */
  unclassified: number;
  /** critical + warning: everything at or inside its own warning threshold. */
  expiring: number;
  /** How many of the expiring rows carry a server-flagged threshold conflict,
   * so the card can explain a classification an operator's annotation would
   * not predict. */
  conflicted: number;
  /** Worst first, soonest first inside a class, capped at the caller's limit.
   * Healthy certificates are never rows -- the card is about what is expiring,
   * and a list of things that are fine is the empty state wearing a disguise. */
  rows: CertExpiryRow[];
  /** Entries with no readable identity, which are not rendered. */
  unreadableRows: number;
}

function emptyCertView(readable: boolean): CertExpiryView {
  return {
    readable,
    total: 0,
    critical: 0,
    warning: 0,
    healthy: 0,
    unclassified: 0,
    expiring: 0,
    conflicted: 0,
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * The certificate card's state.
 *
 * Reads `/v1/certificates/certificates` rather than `/v1/certificates/expiring`
 * for two reasons the shorter route cannot serve. The expiring route returns
 * only certificates already at or inside their warning threshold, and it
 * carries a pre-computed `severity` string instead of the thresholds behind
 * it -- so a card built on it could not tell "cert-manager is installed and
 * every certificate is healthy" from "cert-manager is installed and manages
 * nothing", and could not show an operator WHICH threshold a classification
 * came from. Both readings matter on a card whose whole job is to avoid
 * reporting absence as good news, and the full list answers both.
 */
export function certsExpiringView(
  data: unknown,
  limit: number,
): CertExpiryView {
  const items = list(data);
  if (items === null) return emptyCertView(false);

  const view = emptyCertView(true);
  const rows: CertExpiryRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const warn = num(item.warningThresholdDays);
    const crit = num(item.criticalThresholdDays);
    const days = num(item.daysRemaining);
    const cls = classifyExpiry(days, warn, crit);
    if (cls === null || days === null || warn === null || crit === null) {
      view.unclassified++;
      continue;
    }

    if (cls === "healthy") {
      view.healthy++;
      continue;
    }

    const conflict = item.thresholdConflict === true;
    if (cls === "critical") view.critical++;
    else view.warning++;
    if (conflict) view.conflicted++;

    rows.push({
      namespace: str(item.namespace),
      name,
      daysRemaining: days,
      class: cls,
      warnDays: warn,
      criticalDays: crit,
      thresholdConflict: conflict,
    });
  }

  view.expiring = view.critical + view.warning;
  rows.sort((a, b) => {
    const byClass =
      EXPIRY_CLASSES.indexOf(a.class) - EXPIRY_CLASSES.indexOf(b.class);
    if (byClass !== 0) return byClass;
    return a.daysRemaining - b.daysRemaining;
  });
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// eso-health
// --------------------------------------------------------------------------

/** The full page the ExternalSecret card links to (R7). */
export const EXTERNAL_SECRETS_PAGE_HREF = "/external-secrets/external-secrets";

export function externalSecretHref(namespace: string, name: string): string {
  return `${EXTERNAL_SECRETS_PAGE_HREF}/${seg(namespace)}/${seg(name)}`;
}

/**
 * The four drift readings of an ExternalSecret on the list endpoint, worst
 * first. Four, not three, and none of them is a synonym for another.
 *
 * `Drifted` and `InSync` are observations. `Unknown` is ESO saying it could
 * not determine drift for this one -- the provider does not populate
 * `syncedResourceVersion`, the target Secret was deleted, the account cannot
 * read it -- which is a state an operator can act on and is emphatically not
 * "in sync". `unobserved` is this module's name for the field being absent,
 * which the wire contract defines as the poller not having reached this
 * ExternalSecret yet and states explicitly must be read as "neither InSync nor
 * Drifted" (see `LastObservedDriftStatus` in
 * backend/internal/externalsecrets/types.go).
 *
 * Collapsing either of the last two into `InSync` is the failure this ordering
 * exists to prevent: it would report a secret whose drift nobody has checked
 * as a secret known to be clean.
 */
export const ESO_DRIFT_STATES = [
  "Drifted",
  "Unknown",
  "unobserved",
  "InSync",
] as const;
export type EsoDriftState = (typeof ESO_DRIFT_STATES)[number];

/**
 * The lifecycle states ESO derives, worst first.
 *
 * `SyncFailed` is a secret that is not being delivered. `Stale` is one whose
 * last successful sync is older than its resolved threshold -- delivered once,
 * possibly wrong now. `Drifted` is one whose synced Secret someone edited
 * underneath the operator. `Refreshing` is in flight. `Unknown` is a status
 * this build did not recognise, which ranks above `Synced` because an
 * unreadable state is not a healthy one.
 */
const ESO_STATUS_ORDER = [
  "SyncFailed",
  "Stale",
  "Drifted",
  "Refreshing",
  "Unknown",
  "Synced",
] as const;

function esoStatusRank(status: string): number {
  const idx = (ESO_STATUS_ORDER as readonly string[]).indexOf(status);
  // An unrecognised status sorts with "Unknown" rather than last: a state this
  // build has never heard of is at least as suspect as one it has.
  return idx === -1 ? ESO_STATUS_ORDER.indexOf("Unknown") : idx;
}

function esoDriftState(value: unknown): EsoDriftState {
  const raw = str(value);
  return (ESO_DRIFT_STATES as readonly string[]).includes(raw)
    ? (raw as EsoDriftState)
    : "unobserved";
}

export interface EsoRow {
  namespace: string;
  name: string;
  status: string;
  drift: EsoDriftState;
  store: string;
}

export interface EsoHealthView {
  /** The external-secrets route answered with something this build can read. */
  readable: boolean;
  total: number;
  synced: number;
  failing: number;
  stale: number;
  /** Everything that is not `Synced`, which is what the card leads with. */
  unhealthy: number;
  /** One count per drift state, including the two that are not answers. */
  drift: Record<EsoDriftState, number>;
  /** Worst first, capped at the caller's limit. A healthy ExternalSecret is
   * never a row. */
  rows: EsoRow[];
  unreadableRows: number;
}

function emptyEsoView(readable: boolean): EsoHealthView {
  return {
    readable,
    total: 0,
    synced: 0,
    failing: 0,
    stale: 0,
    unhealthy: 0,
    drift: { Drifted: 0, Unknown: 0, unobserved: 0, InSync: 0 },
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * The ExternalSecret card's state.
 *
 * Both halves come off the one list read: the server derives `status` for each
 * ExternalSecret and overlays the poller's last-observed drift onto it, and it
 * carries `lastObservedDriftStatus` beside it. Nothing here re-derives either
 * -- U7's convention, and here it is also the only version that can be right,
 * since drift resolution needs an impersonated read of the target Secret that
 * the browser cannot perform.
 */
export function esoHealthView(data: unknown, limit: number): EsoHealthView {
  const items = list(data);
  if (items === null) return emptyEsoView(false);

  const view = emptyEsoView(true);
  const rows: EsoRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const status = str(item.status) || "Unknown";
    const drift = esoDriftState(item.lastObservedDriftStatus);
    view.drift[drift]++;

    if (status === "Synced") {
      view.synced++;
      continue;
    }
    view.unhealthy++;
    if (status === "SyncFailed") view.failing++;
    if (status === "Stale") view.stale++;

    rows.push({
      namespace: str(item.namespace),
      name,
      status,
      drift,
      store: str(obj(item.storeRef)?.name),
    });
  }

  rows.sort((a, b) => {
    const byStatus = esoStatusRank(a.status) - esoStatusRank(b.status);
    if (byStatus !== 0) return byStatus;
    return a.name.localeCompare(b.name);
  });
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// velero-backups
// --------------------------------------------------------------------------

/** The full page the backup card links to (R7). There is no per-backup detail
 * route -- the backups table opens a drawer -- so rows are not links. */
export const BACKUPS_PAGE_HREF = "/backup/backups";

/**
 * The four outcomes a Velero phase reduces to, worst first.
 *
 * The ordering is the card's headline: a cluster whose last backup failed
 * while another is running is a cluster with a failed backup, and a roll-up
 * that led with "in progress" would read as reassurance. `other` sits above
 * `completed` rather than below it for the same reason `Unknown` outranks
 * `Synced` above -- a phase this build does not recognise is not a success.
 */
export const BACKUP_OUTCOMES = [
  "failed",
  "inProgress",
  "other",
  "completed",
] as const;
export type BackupOutcome = (typeof BACKUP_OUTCOMES)[number];

/**
 * A Velero phase reduced to an outcome, via the same `getPhaseCategory` the
 * backups, restores and schedules tables colour their rows with.
 *
 * Reusing it rather than keeping a second opinion is the point: the dashboard
 * and the backups page must never disagree about the same row. It also means
 * `PartiallyFailed` counts as a failure here, because that function classifies
 * anything containing "failed" as an error -- which is the reading an operator
 * wants from a backup roll-up anyway.
 */
export function backupOutcome(phase: string): BackupOutcome {
  switch (getPhaseCategory(phase)) {
    case "error":
      return "failed";
    // `getPhaseCategory` reserves this for a partial failure it can reach only
    // if the "failed" test above it ever narrows. Mapped rather than dropped
    // so this switch does not silently start answering "other".
    case "warning":
      return "failed";
    case "progress":
      return "inProgress";
    case "success":
      return "completed";
    default:
      return "other";
  }
}

export interface BackupRow {
  name: string;
  namespace: string;
  phase: string;
  outcome: BackupOutcome;
  startTime: string;
  errors: number;
  warnings: number;
}

export interface BackupsView {
  /** The backups route answered with something this build can read. */
  readable: boolean;
  total: number;
  counts: Record<BackupOutcome, number>;
  /** The worst outcome present, and null exactly when there are no backups.
   * Null rather than "completed": a cluster with no backups at all is not a
   * cluster whose backups are fine. */
  worst: BackupOutcome | null;
  /** Worst first, newest first inside an outcome, capped at the caller's
   * limit. A completed backup is never a row. */
  rows: BackupRow[];
  /** The start time of the most recent completed backup, "" when none. */
  lastCompleted: string;
  unreadableRows: number;
}

function emptyBackupsView(readable: boolean): BackupsView {
  return {
    readable,
    total: 0,
    counts: { failed: 0, inProgress: 0, other: 0, completed: 0 },
    worst: null,
    rows: [],
    lastCompleted: "",
    unreadableRows: 0,
  };
}

/**
 * The backup card's state.
 *
 * Start times are RFC 3339 strings straight off the wire and are compared as
 * strings. That is correct for this format and only this format -- RFC 3339 in
 * UTC sorts lexicographically -- and it avoids parsing a date this card never
 * does arithmetic on. A backup with no start time sorts last within its
 * outcome rather than first, which is where a backup that has not started
 * belongs.
 */
export function backupsView(data: unknown, limit: number): BackupsView {
  const items = list(data);
  if (items === null) return emptyBackupsView(false);

  const view = emptyBackupsView(true);
  const rows: BackupRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const phase = str(item.phase);
    const outcome = backupOutcome(phase);
    const startTime = str(item.startTime);
    view.counts[outcome]++;

    if (outcome === "completed") {
      if (startTime > view.lastCompleted) view.lastCompleted = startTime;
      continue;
    }

    rows.push({
      name,
      namespace: str(item.namespace),
      phase,
      outcome,
      startTime,
      errors: count(item.errors),
      warnings: count(item.warnings),
    });
  }

  view.worst =
    view.total === 0
      ? null
      : (BACKUP_OUTCOMES.find((o) => view.counts[o] > 0) ?? null);

  rows.sort((a, b) => {
    const byOutcome =
      BACKUP_OUTCOMES.indexOf(a.outcome) - BACKUP_OUTCOMES.indexOf(b.outcome);
    if (byOutcome !== 0) return byOutcome;
    if (a.startTime !== b.startTime) return a.startTime < b.startTime ? 1 : -1;
    return a.name.localeCompare(b.name);
  });
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}

// --------------------------------------------------------------------------
// snapshot-health
// --------------------------------------------------------------------------

/** The full page the snapshot card links to (R7). There is no per-snapshot
 * detail route, so rows are not links. */
export const SNAPSHOTS_PAGE_HREF = "/storage/snapshots";

/** Worst first. A snapshot that is not ready yet is not a failure, and a card
 * that coloured it like one would cry wolf on every fresh snapshot. */
export const SNAPSHOT_OUTCOMES = ["error", "pending", "ready"] as const;
export type SnapshotOutcome = (typeof SNAPSHOT_OUTCOMES)[number];

export interface SnapshotRow {
  namespace: string;
  name: string;
  outcome: SnapshotOutcome;
  sourcePVC: string;
  errorMessage: string;
}

export interface SnapshotHealthView {
  /** The snapshots route answered with something this build can read. */
  readable: boolean;
  total: number;
  counts: Record<SnapshotOutcome, number>;
  /** The worst outcome present, and null exactly when there are no snapshots.
   * Null rather than "ready", for the reason `BackupsView.worst` is null. */
  worst: SnapshotOutcome | null;
  /** Worst first, capped at the caller's limit. A ready snapshot is never a
   * row. */
  rows: SnapshotRow[];
  unreadableRows: number;
}

function emptySnapshotView(readable: boolean): SnapshotHealthView {
  return {
    readable,
    total: 0,
    counts: { error: 0, pending: 0, ready: 0 },
    worst: null,
    rows: [],
    unreadableRows: 0,
  };
}

/**
 * The snapshot card's state.
 *
 * `readyToUse` and `status.error.message` are independent fields on the
 * VolumeSnapshot, and a snapshot can carry both -- a snapshot that succeeded
 * after a retry, or a controller that set one and not the other. An error
 * nobody surfaced is the worse failure, so the error wins over the ready flag.
 */
export function snapshotHealthView(
  data: unknown,
  limit: number,
): SnapshotHealthView {
  const items = list(data);
  if (items === null) return emptySnapshotView(false);

  const view = emptySnapshotView(true);
  const rows: SnapshotRow[] = [];

  for (const entry of items) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      view.unreadableRows++;
      continue;
    }
    view.total++;

    const errorMessage = str(item.errorMessage);
    const outcome: SnapshotOutcome =
      errorMessage !== ""
        ? "error"
        : item.readyToUse === true
          ? "ready"
          : "pending";
    view.counts[outcome]++;

    if (outcome === "ready") continue;

    rows.push({
      namespace: str(item.namespace),
      name,
      outcome,
      sourcePVC: str(item.sourcePVC),
      errorMessage,
    });
  }

  view.worst =
    view.total === 0
      ? null
      : (SNAPSHOT_OUTCOMES.find((o) => view.counts[o] > 0) ?? null);

  rows.sort((a, b) => {
    const byOutcome =
      SNAPSHOT_OUTCOMES.indexOf(a.outcome) -
      SNAPSHOT_OUTCOMES.indexOf(b.outcome);
    if (byOutcome !== 0) return byOutcome;
    return a.name.localeCompare(b.name);
  });
  view.rows = rows.slice(0, Math.max(0, limit));
  return view;
}
