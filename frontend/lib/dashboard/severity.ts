/**
 * The derived state behind the three security widgets: `policy-compliance`,
 * `policy-violations` and `vulnerability-severity`.
 *
 * All three rank or count by severity, and none of them can read the answer
 * off a field. A compliance score has to be told apart from an unpoliced
 * cluster that scores 100 by arithmetic accident; a violations list has to be
 * ordered by a label whose value set neither Kyverno nor Gatekeeper
 * constrains; a CVE roll-up has to rank workloads by their WORST finding
 * rather than by how many they have. None of that is a field read, so it
 * lives here under test rather than inside the components (D-10, KTD8).
 *
 * They share a module because they share one judgement: what a severity label
 * is worth. `severityRank` is the single place that turns a string into an
 * order, and a violations card and a vulnerability card that disagreed about
 * whether "high" outranks "medium" -- or about where an unrecognised label
 * belongs -- would be two cards on one dashboard contradicting each other.
 *
 * What is NOT here is availability. Whether a policy engine or a scanner is
 * installed at all is answered by that family's own discovery route and
 * resolved by the shell before `render` is called, so every function below is
 * reached only on a cluster that runs the feature (KTD1, R1). That is what
 * lets an empty list here mean "nothing to report" and say so plainly: the
 * other reading -- "no operator" -- is not representable at this point.
 *
 * Total, in the sense the rest of lib/dashboard is total: every argument may
 * be null, missing or the wrong shape, and none of it throws. One unreadable
 * item degrades to a counter rather than blanking a card describing the
 * readable rest.
 */

// --------------------------------------------------------------------------
// Shared
// --------------------------------------------------------------------------

/**
 * The severity labels this dashboard ranks, worst first.
 *
 * Deliberately the same four the rest of the app already colours --
 * `SEVERITY_COLORS` in lib/badge-colors.ts, which `PolicyBadges.tsx` and
 * `ScanBadges.tsx` both render from. A fifth label here that had no colour
 * there would render as muted grey beside a ranking that placed it above
 * "low", which reads as a bug in the colour rather than in the list.
 */
export const SEVERITY_ORDER = ["critical", "high", "medium", "low"] as const;
export type KnownSeverity = (typeof SEVERITY_ORDER)[number];

/**
 * The label given to a severity that is missing or not a string.
 *
 * Not a fallback to "low": a finding whose rating nobody recorded is not a
 * finding that was rated low, and sorting it in among the genuinely
 * low-severity ones hides it. It sorts after every known label instead, with
 * its own bucket, so the reader can see there are some and that we do not know
 * what they are.
 */
export const UNRANKED_SEVERITY = "unknown";

/**
 * A severity label reduced to the form the maps and the ranking key on.
 *
 * Case and surrounding space are dropped because the producers disagree about
 * both: Trivy emits `CRITICAL`, Kyverno's `policies.kyverno.io/severity`
 * annotation is conventionally lowercase, and a hand-written Gatekeeper
 * constraint can carry whatever its author typed. They describe the same
 * thing and have to rank the same.
 */
export function normalizeSeverity(value: unknown): string {
  if (typeof value !== "string") return UNRANKED_SEVERITY;
  const trimmed = value.trim().toLowerCase();
  return trimmed === "" ? UNRANKED_SEVERITY : trimmed;
}

/**
 * Where a severity sits in the ranking. Lower is worse.
 *
 * Anything outside the four known labels returns `SEVERITY_ORDER.length`,
 * which sorts it after all of them. Total on purpose: both policy engines let
 * an author write any string into a severity field and Trivy emits `UNKNOWN`
 * for a CVE with no assigned rating, so a lookup that threw would blank a card
 * over one malformed row, and one that returned `undefined` or `NaN` would
 * sort the unrated finding to the top of a ranking of what to fix first --
 * `NaN` comparisons are all false, so it would land wherever the input order
 * happened to put it.
 */
export function severityRank(value: unknown): number {
  const idx = (SEVERITY_ORDER as readonly string[]).indexOf(
    normalizeSeverity(value),
  );
  return idx === -1 ? SEVERITY_ORDER.length : idx;
}

/**
 * Comparator over severities, worst first.
 *
 * Two unranked labels tie on rank, so they break on the label itself. Any
 * total order would do; the requirement is only that it is the SAME order on
 * every render, because a card that reshuffles on a refresh which changed
 * nothing reads as a cluster that changed.
 */
export function compareSeverity(a: unknown, b: unknown): number {
  const delta = severityRank(a) - severityRank(b);
  if (delta !== 0) return delta;
  return normalizeSeverity(a).localeCompare(normalizeSeverity(b));
}

/** How many of something carry one severity label. */
export interface SeverityBucket {
  severity: string;
  count: number;
}

/**
 * Buckets built from an already-counted map, in rank order.
 *
 * The four known labels are ALWAYS present, at zero when nothing carries them:
 * zero is a fact about the cluster and has to be representable, or a card
 * cannot tell "no criticals" apart from "nothing was counted". Anything else
 * the payload carried is appended after them, in the comparator's order.
 */
function bucketsFrom(counts: ReadonlyMap<string, number>): SeverityBucket[] {
  const known: SeverityBucket[] = SEVERITY_ORDER.map((severity) => ({
    severity,
    count: counts.get(severity) ?? 0,
  }));
  const extra: SeverityBucket[] = [...counts.entries()]
    .filter(([severity]) => severityRank(severity) === SEVERITY_ORDER.length)
    .map(([severity, count]) => ({ severity, count }))
    .sort((a, b) => compareSeverity(a.severity, b.severity));
  return [...known, ...extra];
}

/**
 * The severity breakdown of a list of labels.
 *
 * See `bucketsFrom` for why the empty known buckets are kept and where an
 * unrecognised label goes.
 */
export function severityBreakdown(
  values: readonly unknown[],
): SeverityBucket[] {
  const counts = new Map<string, number>();
  for (const value of values) {
    const key = normalizeSeverity(value);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return bucketsFrom(counts);
}

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/** A finite number, or null. Null rather than zero: a count we could not read
 * is not a count of none, and every consumer below branches on the
 * difference instead of reporting a guess as a fact. */
function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

/** A finite count, defaulting to zero. Used only where the field is a count
 * the server always writes, so a missing one is a shape change rather than an
 * unknown quantity. */
function count(value: unknown): number {
  return num(value) ?? 0;
}

// --------------------------------------------------------------------------
// policy-compliance
// --------------------------------------------------------------------------

/** The full page the compliance card links to (R7). */
export const COMPLIANCE_PAGE_HREF = "/security/compliance";

/**
 * The history window the card asks for and reports.
 *
 * Pinned to the `days=` the `policy-compliance-history` fetcher sends in
 * data.ts. The two have to agree: the card's copy says "over 30 days", and a
 * fetcher asking for 7 would put a truthful number under a false label.
 */
export const COMPLIANCE_HISTORY_DAYS = 30;

export interface ComplianceHistoryView {
  /** The history endpoint answered. False covers both ways it does not: a
   * non-admin caller (403) and a deployment with no database (503). */
  available: boolean;
  /** Change in score across the window, or null when fewer than two snapshots
   * are readable. One snapshot is a point, not a trend. */
  delta: number | null;
  days: number;
}

export interface ComplianceView {
  /** The compliance endpoint answered with something this build can read.
   * False is NOT a score of zero -- see `policyComplianceView`. */
  readable: boolean;
  /** 0..100, clamped so the gauge cannot overdraw its arc. */
  score: number;
  pass: number;
  fail: number;
  warn: number;
  /** How many policies the score was computed over. */
  total: number;
  /**
   * The cluster has at least one policy.
   *
   * `computeCompliance` in backend/internal/policy/handler.go returns 100 when
   * nothing is weighted, which is arithmetically correct and editorially a
   * lie: a cluster running Kyverno with no ClusterPolicy is not a compliant
   * cluster, it is an unpoliced one. This is what lets the card refuse to draw
   * that 100 -- the same absence-as-good-news failure R1 removes at the
   * feature level, one level down.
   */
  governed: boolean;
  /** Failing policies per severity, in rank order. */
  failuresBySeverity: SeverityBucket[];
  history: ComplianceHistoryView;
}

/**
 * The compliance card's state, from the score payload and the history payload.
 *
 * The history argument is the response of an OPTIONAL source, so it is null
 * whenever that read has not landed or has failed. That is deliberate and it
 * is the whole reason the source is optional: the current score is present and
 * worth rendering either way, so a widget gated on the history endpoint would
 * blank a number the compliance endpoint already returned -- on every
 * non-admin account, and on every deployment without a database, permanently.
 * The card drops the trend line and keeps the gauge instead.
 */
export function policyComplianceView(
  data: unknown,
  history: unknown,
): ComplianceView {
  const body = obj(data);
  const score = num(body?.score);
  const empty: ComplianceView = {
    readable: false,
    score: 0,
    pass: 0,
    fail: 0,
    warn: 0,
    total: 0,
    governed: false,
    failuresBySeverity: bucketsFrom(new Map()),
    history: historyView(history),
  };
  if (body === null || score === null) return empty;

  const bySeverity = obj(body.bySeverity);
  const failures = new Map<string, number>();
  for (const [severity, counts] of Object.entries(bySeverity ?? {})) {
    const fail = count(obj(counts)?.fail);
    if (fail > 0) {
      const key = normalizeSeverity(severity);
      failures.set(key, (failures.get(key) ?? 0) + fail);
    }
  }

  const total = count(body.total);
  return {
    readable: true,
    score: Math.max(0, Math.min(100, score)),
    pass: count(body.pass),
    fail: count(body.fail),
    warn: count(body.warn),
    total,
    governed: total > 0,
    failuresBySeverity: bucketsFrom(failures),
    history: historyView(history),
  };
}

function historyView(history: unknown): ComplianceHistoryView {
  if (!Array.isArray(history)) {
    return { available: false, delta: null, days: COMPLIANCE_HISTORY_DAYS };
  }
  const scores = history
    .map((point) => num(obj(point)?.score))
    .filter((s): s is number => s !== null);
  return {
    available: true,
    delta: scores.length >= 2 ? scores[scores.length - 1] - scores[0] : null,
    days: COMPLIANCE_HISTORY_DAYS,
  };
}

// --------------------------------------------------------------------------
// policy-violations
// --------------------------------------------------------------------------

/** The full page the violations card links to (R7). */
export const VIOLATIONS_PAGE_HREF = "/security/violations";

export interface ViolationRow {
  policy: string;
  severity: string;
  namespace: string;
  kind: string;
  name: string;
  engine: string;
  /** The violation blocks admission rather than being recorded and allowed. */
  blocking: boolean;
}

export interface ViolationsView {
  /** The violations endpoint answered with something this build can read. An
   * EMPTY list is readable -- that is the point of the card. */
  readable: boolean;
  /** Every violation the endpoint returned, readable or not. */
  total: number;
  /** How many of them block admission. */
  blocking: number;
  bySeverity: SeverityBucket[];
  /** The worst ones, capped at the caller's limit. */
  rows: ViolationRow[];
  /** Entries with no readable identity, which are counted but not rendered. */
  unreadableRows: number;
}

/**
 * The violations card's state.
 *
 * A `null` body is an EMPTY list, not an unreadable one.
 * `filterViolationsByRBAC` in backend/internal/policy/handler.go builds its
 * result with `var filtered []NormalizedViolation`, so an engine with nothing
 * to report -- and a user who can see no namespace -- both serialise as JSON
 * `null` rather than `[]`. Reading that as unreadable would put "we could not
 * read this" on the healthiest possible cluster. (The fetcher normalises it
 * too, because a source whose data is null never renders at all; this is the
 * second line of the same defence.)
 *
 * The ranking is re-derived here rather than taken from the response order.
 * The handler already sorts by the same weights, so the two agree -- this is a
 * guard against that changing silently, and the only place the order is
 * specified in a form a test can hold.
 */
export function policyViolationsView(
  data: unknown,
  limit: number,
): ViolationsView {
  if (data === null || data === undefined) {
    return {
      readable: true,
      total: 0,
      blocking: 0,
      bySeverity: bucketsFrom(new Map()),
      rows: [],
      unreadableRows: 0,
    };
  }
  if (!Array.isArray(data)) {
    return {
      readable: false,
      total: 0,
      blocking: 0,
      bySeverity: bucketsFrom(new Map()),
      rows: [],
      unreadableRows: 0,
    };
  }

  const rows: ViolationRow[] = [];
  let unreadableRows = 0;
  // Counted over EVERY entry, not only the renderable ones: a violation whose
  // identity we cannot read is still a violation, and leaving it out of the
  // header count would under-report the cluster. Its severity falls into the
  // unranked bucket when that is unreadable too.
  for (const entry of data) {
    const body = obj(entry);
    const name = str(body?.name);
    if (body === null || name === "") {
      unreadableRows++;
      continue;
    }
    rows.push({
      policy: str(body.policy),
      severity: normalizeSeverity(body.severity),
      namespace: str(body.namespace),
      kind: str(body.kind),
      name,
      engine: str(body.engine),
      blocking: body.blocking === true,
    });
  }

  rows.sort(
    (a, b) =>
      compareSeverity(a.severity, b.severity) ||
      a.policy.localeCompare(b.policy) ||
      a.namespace.localeCompare(b.namespace) ||
      a.name.localeCompare(b.name),
  );

  return {
    readable: true,
    total: data.length,
    blocking: rows.filter((r) => r.blocking).length,
    bySeverity: severityBreakdown(data.map((e) => obj(e)?.severity)),
    rows: rows.slice(0, Math.max(0, limit)),
    unreadableRows,
  };
}

// --------------------------------------------------------------------------
// vulnerability-severity
// --------------------------------------------------------------------------

/** The full page the vulnerability card links to (R7). */
export const VULNERABILITIES_PAGE_HREF = "/security/vulnerabilities";

/** A row deep-links to its workload's CVE detail. Every segment is encoded:
 * a Kubernetes name cannot contain a slash, but this is where a stored value
 * becomes a URL, and the narrow fix costs nothing. */
export function workloadHref(
  namespace: string,
  kind: string,
  name: string,
): string {
  return (
    `${VULNERABILITIES_PAGE_HREF}/${encodeURIComponent(namespace)}` +
    `/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`
  );
}

export interface VulnWorkloadRow {
  namespace: string;
  kind: string;
  name: string;
  critical: number;
  high: number;
  medium: number;
  low: number;
  /** Every finding on this workload, across the four severities. */
  findings: number;
  scanner: string;
}

export interface VulnerabilityView {
  /** The scanning endpoint answered with something this build can read. An
   * empty workload list is readable -- a scanned, clean namespace. */
  readable: boolean;
  /** Workloads the scanner has a report for. */
  scanned: number;
  /** How many of them carry at least one finding. */
  affected: number;
  /** Every finding, across the four severities. */
  findings: number;
  bySeverity: SeverityBucket[];
  /** The worst workloads, capped at the caller's limit. */
  rows: VulnWorkloadRow[];
  /** Entries with no readable identity, which are not rendered. */
  unreadableRows: number;
}

/**
 * The vulnerability card's state.
 *
 * The breakdown prefers `summary.severity`, which the handler computes over
 * everything it returned, and falls back to re-adding the workload list only
 * when that is absent. That is U7's convention -- a status the server already
 * computed is not re-derived in the browser -- and here it is also the only
 * version that stays correct if the route ever starts paginating: a sum of the
 * page would then quietly describe the page rather than the namespace.
 *
 * Workloads rank by their WORST finding, not by how many they have. Nine
 * hundred low-severity findings are not a reason to patch something before the
 * one workload with a critical, and a ranking on the total would say they are.
 */
export function vulnerabilitySeverityView(
  data: unknown,
  limit: number,
): VulnerabilityView {
  const body = obj(data);
  if (body === null) {
    return {
      readable: false,
      scanned: 0,
      affected: 0,
      findings: 0,
      bySeverity: bucketsFrom(new Map()),
      rows: [],
      unreadableRows: 0,
    };
  }

  const entries = Array.isArray(body.vulnerabilities)
    ? body.vulnerabilities
    : [];
  const rows: VulnWorkloadRow[] = [];
  let unreadableRows = 0;
  for (const entry of entries) {
    const item = obj(entry);
    const name = str(item?.name);
    if (item === null || name === "") {
      unreadableRows++;
      continue;
    }
    const totals = obj(item.total);
    const critical = count(totals?.critical);
    const high = count(totals?.high);
    const medium = count(totals?.medium);
    const low = count(totals?.low);
    rows.push({
      namespace: str(item.namespace),
      kind: str(item.kind),
      name,
      critical,
      high,
      medium,
      low,
      findings: critical + high + medium + low,
      scanner: str(item.scanner),
    });
  }

  const summary = obj(obj(body.summary)?.severity);
  const fromSummary =
    summary !== null &&
    SEVERITY_ORDER.some((severity) => num(summary[severity]) !== null);
  const counts = new Map<string, number>();
  for (const severity of SEVERITY_ORDER) {
    counts.set(
      severity,
      fromSummary
        ? count(summary?.[severity])
        : rows.reduce((sum, row) => sum + row[severity], 0),
    );
  }

  const ranked = rows
    .filter((row) => row.findings > 0)
    .sort(
      (a, b) =>
        b.critical - a.critical ||
        b.high - a.high ||
        b.medium - a.medium ||
        b.low - a.low ||
        a.namespace.localeCompare(b.namespace) ||
        a.name.localeCompare(b.name),
    );

  return {
    readable: true,
    scanned: entries.length,
    affected: ranked.length,
    findings: [...counts.values()].reduce((sum, n) => sum + n, 0),
    bySeverity: bucketsFrom(counts),
    rows: ranked.slice(0, Math.max(0, limit)),
    unreadableRows,
  };
}
