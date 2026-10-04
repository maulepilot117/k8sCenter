/**
 * Backup assurance (Release F) — wire contract for /v1/velero/assurance/*
 * plus the pure display helpers the recovery-readiness page renders with.
 *
 * The shapes mirror backend/internal/velero/assurance_handler.go and
 * assurance_policies.go (U35) exactly. The helpers live here, not in the
 * island, so they can be unit-tested without mounting anything (U36b).
 *
 * Honesty rule (plan Design Decision §7): backup freshness is not
 * demonstrated recoverability. Nothing in this module may describe a backup
 * as recoverable, restorable, protected or guaranteed, and an `unknown`
 * collection is never described as "no backups".
 */

/** The persistent banner the page opens with (plan §7, rule 1). Verbatim. */
export const HONESTY_TEXT =
  "Backup assurance observes what Velero reported. It does not verify that a restore would succeed. No restore has been attempted.";

export type AssuranceCondition =
  | "overdue"
  | "failed"
  | "partially_failed"
  | "paused"
  | "never_run"
  | "location_unavailable"
  | "collection_unknown";

/** Every condition, in the order the API reports them. */
export const ASSURANCE_CONDITIONS: readonly AssuranceCondition[] = [
  "overdue",
  "failed",
  "partially_failed",
  "paused",
  "never_run",
  "location_unavailable",
  "collection_unknown",
];

export type AssuranceExceptionState = "open" | "resolved";
export type AssuranceSeverity = "info" | "warning" | "critical";
export type AssuranceScopeKind = "schedule" | "namespace" | "cluster";

/**
 * The status endpoint's collection verdict: five of the R3 six states.
 * `forbidden` is a per-request state the page derives from a 403.
 */
export type AssuranceCollection =
  | "ok"
  | "stale"
  | "empty"
  | "unknown"
  | "unavailable";

/** The R3 six-state vocabulary, used verbatim on the page. */
export type SurfaceState = AssuranceCollection | "forbidden";

export type CollectionSource = "this_replica" | "lease_holder" | "none";

/** Existence of the schedule a row names: never `not_found` on a failed read. */
export type ScheduleExistence = "found" | "not_found" | "unknown";

export type FreshnessOutcome =
  | "success"
  | "partial"
  | "failure"
  | "in_flight"
  | "unknown";

export interface AssuranceOpenCounts {
  total: number;
  /** Every condition is present, zero included. */
  byCondition: Record<AssuranceCondition, number>;
}

export interface AssuranceLease {
  holder: string;
  fence: number;
  expiresAt: string;
  expired: boolean;
}

/** Admin-only collector state; absent from the status body for everyone else. */
export interface AssuranceRuntime {
  holder: string;
  lastTickAt: string | null;
  lastRunAt: string | null;
  /** This replica's last completed run; "" before the first. */
  lastCollection: "ok" | "failed" | "";
  findingCount: number;
  lastError?: string;
  lastErrorAt: string | null;
  leaseHeld: boolean;
  lease: AssuranceLease | null;
  deliveryBacklog: { pending: number; failed: number };
}

/** GET /v1/velero/assurance/status */
export interface AssuranceStatus {
  enabled: boolean;
  collection: AssuranceCollection;
  collectionSource: CollectionSource;
  /** Admins: every policy. Others: policies in namespaces they may see. */
  policyCount: number;
  open: AssuranceOpenCounts;
  runtime?: AssuranceRuntime;
}

export interface AssuranceSubject {
  kind: AssuranceScopeKind;
  namespace: string;
  name: string;
  uid: string;
}

export interface AssuranceDetail {
  lastOutcome?: FreshnessOutcome;
  lastSuccessAt?: string;
  expectedRunAt?: string;
  expectedRunKnown: boolean;
  cronParseError?: string;
  /** "paused" when a paused schedule is holding the condition open. */
  suppressedBy?: string;
  resolutionReason?: string;
  // Admin only — absent for everyone else.
  storageLocation?: string;
  bslMessage?: string;
  failureReason?: string;
}

/** One row of GET /v1/velero/assurance/exceptions */
export interface AssuranceException {
  id: string;
  policyId: string;
  subject: AssuranceSubject;
  condition: AssuranceCondition;
  state: AssuranceExceptionState;
  severity: AssuranceSeverity;
  openedAt: string;
  lastObservedAt: string;
  resolvedAt: string | null;
  observationCount: number;
  lastSuccessAt: string | null;
  /** Schedule subjects only. */
  subjectStatus: ScheduleExistence | null;
  /** "schedule not found" when subjectStatus is not_found. */
  subjectNote?: string;
  /** "not computable" when the next expected run could not be computed. */
  expectedRunNote?: string;
  detail: AssuranceDetail;
}

export type TreatPartialAs = "success" | "failure";

/** One row of GET /v1/velero/assurance/policies (admin only). */
export interface AssurancePolicy {
  id: string;
  scopeKind: AssuranceScopeKind;
  scopeNamespace: string;
  scopeName: string;
  maxAgeSeconds: number;
  graceSeconds: number;
  treatPartialAs: TreatPartialAs;
  alertOnPaused: boolean;
  enabled: boolean;
  createdBy: string;
  createdAt: string;
  updatedBy: string;
  updatedAt: string | null;
  revision: number;
  /** Schedule scope only. */
  scheduleStatus: ScheduleExistence | null;
  scheduleNote?: string;
}

/** POST /v1/velero/assurance/policies body. */
export interface AssurancePolicyCreate {
  scopeKind: AssuranceScopeKind;
  scopeNamespace: string;
  scopeName: string;
  maxAgeSeconds: number;
  graceSeconds: number;
  treatPartialAs: TreatPartialAs;
  alertOnPaused: boolean;
  enabled: boolean;
}

/** PUT /v1/velero/assurance/policies/{id} body. Scope is immutable. */
export interface AssurancePolicyUpdate {
  revision: number;
  maxAgeSeconds: number;
  graceSeconds: number;
  treatPartialAs: TreatPartialAs;
  alertOnPaused: boolean;
  enabled: boolean;
}

/** DELETE /v1/velero/assurance/policies/{id}?confirm=true response body. */
export interface AssurancePolicyDeleted {
  id: string;
  deleted: true;
  discardedOpenExceptions: number;
}

/** One entry of `error.extra.fieldErrors` on a 400 invalid_policy. */
export interface AssuranceFieldError {
  field: string;
  message: string;
}

/** The handler's floor for maxAgeSeconds (store.AssuranceMinMaxAge). */
export const MIN_MAX_AGE_SECONDS = 300;
/** The handler's default grace for a new policy. */
export const DEFAULT_GRACE_SECONDS = 3600;
/** Page size for the exception list; the API caps limit at 500. */
export const EXCEPTION_PAGE_SIZE = 50;

/**
 * The `reason` codes the page branches on. Anything else falls through to
 * the generic error copy.
 */
export const ASSURANCE_REASONS = {
  remote: "remote_assurance_unsupported",
  database: "database_unavailable",
  confirmationRequired: "confirmation_required",
  revisionConflict: "revision_conflict",
  invalidPolicy: "invalid_policy",
  policyExists: "policy_exists",
  policyNotFound: "policy_not_found",
  scopeImmutable: "scope_immutable",
} as const;

/**
 * The page's surface state. A failed request outranks the status body: 403
 * is `forbidden`, 503 (no database / collector not wired) is `unavailable`.
 * Otherwise the server's verdict is used verbatim — this function never
 * upgrades a non-ok collection, and never turns `unknown` into `empty`.
 */
export function surfaceStateFor(
  status: AssuranceStatus | null,
  httpStatus?: number,
): SurfaceState {
  if (httpStatus === 403) return "forbidden";
  if (httpStatus === 503) return "unavailable";
  if (!status) return "unknown";
  if (!status.enabled) return "unavailable";
  return status.collection;
}

/** Whether counts may be read as current: only an ok collection qualifies. */
export function countsAreCurrent(state: SurfaceState): boolean {
  return state === "ok";
}

const CONDITION_LABELS: Record<AssuranceCondition, string> = {
  overdue: "Overdue",
  failed: "Failed",
  partially_failed: "Partially failed",
  paused: "Paused",
  never_run: "Never run",
  location_unavailable: "Location unavailable",
  collection_unknown: "Collection unknown",
};

const CONDITION_EXPLANATIONS: Record<AssuranceCondition, string> = {
  overdue:
    "No successful backup was observed within the policy's maximum age plus grace.",
  failed: "The most recent finished backup run ended in Failed.",
  partially_failed:
    "The most recent finished backup run ended in PartiallyFailed: some items were not backed up.",
  paused:
    "The schedule is paused, so Velero is not starting new backups for it.",
  never_run:
    "No backup run has been observed for this schedule since it was created, and the policy's maximum age plus grace has passed.",
  location_unavailable:
    "The backup storage location this schedule writes to is not reported as Available.",
  collection_unknown:
    "Velero could not be observed — backup state is unknown. Whether backups exist, and how recent they are, cannot be told from here.",
};

export function conditionLabel(c: AssuranceCondition): string {
  return CONDITION_LABELS[c] ?? c;
}

export function conditionExplanation(c: AssuranceCondition): string {
  return CONDITION_EXPLANATIONS[c] ?? "";
}

/** The six-state word plus the sentence the freshness row shows for it. */
export function surfaceStateCopy(state: SurfaceState): {
  word: string;
  sentence: string;
} {
  switch (state) {
    case "ok":
      return {
        word: "ok",
        sentence: "Velero was observed on the last evaluation.",
      };
    case "stale":
      return {
        word: "stale",
        sentence:
          "The last successful evaluation is older than three collection intervals. Counts may be out of date.",
      };
    case "empty":
      return {
        word: "empty",
        sentence:
          "Nothing is being evaluated: no freshness policy applies, so no backup state is reported.",
      };
    case "unknown":
      return {
        word: "unknown",
        sentence: "Backup state is unknown — Velero could not be observed.",
      };
    case "forbidden":
      return {
        word: "forbidden",
        sentence: "You do not have permission to view backup exceptions.",
      };
    case "unavailable":
      return {
        word: "unavailable",
        sentence:
          "Backup assurance requires PostgreSQL and a running collector; it is not available on this installation.",
      };
  }
}

export function collectionSourceLabel(src: CollectionSource): string {
  switch (src) {
    case "this_replica":
      return "judged from this replica's last collection";
    case "lease_holder":
      return "judged from the replica currently collecting";
    case "none":
      return "no collection to judge from";
  }
}

export function outcomeLabel(o: FreshnessOutcome | undefined): string {
  switch (o) {
    case "success":
      return "Completed";
    case "partial":
      return "PartiallyFailed";
    case "failure":
      return "Failed";
    case "in_flight":
      return "In progress";
    default:
      return "Unknown";
  }
}

/** Seconds as a compact duration: 90000 → "1d 1h", 300 → "5m". */
export function formatSeconds(total: number): string {
  if (!Number.isFinite(total) || total <= 0) return "0m";
  const d = Math.floor(total / 86400);
  const h = Math.floor((total % 86400) / 3600);
  const m = Math.floor((total % 3600) / 60);
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h) parts.push(`${h}h`);
  if (m && !d) parts.push(`${m}m`);
  if (parts.length === 0) parts.push(`${total}s`);
  return parts.join(" ");
}

/** "schedule ns/name", "namespace ns", or "cluster". */
export function scopeLabel(
  kind: AssuranceScopeKind,
  namespace: string,
  name: string,
): string {
  switch (kind) {
    case "schedule":
      return `schedule ${namespace}/${name}`;
    case "namespace":
      return `namespace ${namespace}`;
    case "cluster":
      return "cluster";
  }
}

/** Groups rows by condition, in ASSURANCE_CONDITIONS order, dropping empties. */
export function groupByCondition(
  rows: AssuranceException[],
): Array<[AssuranceCondition, AssuranceException[]]> {
  const groups = new Map<AssuranceCondition, AssuranceException[]>();
  for (const r of rows) {
    const list = groups.get(r.condition);
    if (list) list.push(r);
    else groups.set(r.condition, [r]);
  }
  const out: Array<[AssuranceCondition, AssuranceException[]]> = [];
  for (const c of ASSURANCE_CONDITIONS) {
    const list = groups.get(c);
    if (list) out.push([c, list]);
  }
  // A condition this client does not know yet (mixed-version rollout) is
  // still shown rather than silently dropped.
  for (const [c, list] of groups) {
    if (!ASSURANCE_CONDITIONS.includes(c)) out.push([c, list]);
  }
  return out;
}
