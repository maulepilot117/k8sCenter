/**
 * Wire types for tracked apply and change receipts (Release E).
 *
 * Every interface mirrors the JSON tags of the Go type named in its doc
 * comment, field for field:
 *   - backend/internal/changes/types.go     (ApplyTracking, ReceiptView, ...)
 *   - backend/internal/changes/handler.go   (ReceiptDetail, VerificationView, ...)
 *   - backend/internal/changes/redaction.go (ReceiptObjectView, CheckView, ...)
 *   - backend/internal/gitops/types.go      (Ownership*, ObjectRef, OwnedByApp)
 *   - backend/internal/diagnostics          (SourceRef, check status/severity/reasons)
 *   - backend/internal/store/change_receipts.go (ReceiptState, VerificationState)
 *
 * Enumerations the backend defines as a fixed set are string-literal unions,
 * so a renamed backend value is a compile error at every `switch` rather than
 * a silent `undefined` branch. Reason codes are different: U20 makes the
 * reason sets open by contract (a newer server may add one), so those are
 * `Open<Known>` — the known codes autocomplete, an unknown one still type
 * checks and consumers must fall through to a neutral rendering.
 *
 * The closed unions are a compile-time contract only. The Go fields are plain
 * strings with no validation at marshal time, so a constant added on the
 * server arrives as a value the union does not name until this file is
 * edited. Give every `switch` over one a `default` that renders neutrally,
 * and extend the union in the same PR that adds the Go constant.
 *
 * The backend's unredacted `diagnostics.CheckResult` and
 * `changes.VerificationResult` are deliberately NOT mirrored: no endpoint
 * serves them. Every HTTP response carries `CheckView` and
 * `VerificationView`, which are redacted per reader, so a caller cannot type
 * a response with the unredacted shape.
 *
 * Go `time.Time` is an RFC 3339 string on the wire; `*time.Time` with
 * `omitempty` is an optional string. Go slices that the handlers always
 * construct (never nil) are non-optional arrays here.
 */

/**
 * A string union that stays open: `Known` values autocomplete, any other
 * string is still assignable. `Record<never, never>` rather than `{}` keeps
 * the intersection from being collapsed to `string` and from tripping the
 * banned-types lint.
 */
export type Open<Known extends string> =
  | Known
  | (string & Record<never, never>);

// --- Receipt and verification state (store) --------------------------------

/** ReceiptState mirrors store.ReceiptState: the execution state of a receipt. */
export type ReceiptState =
  | "previewed"
  | "applying"
  | "applied"
  | "partial"
  | "failed"
  | "unknown";

/**
 * VerificationState mirrors store.VerificationState. It advances independently
 * of ReceiptState: `applied` + `pending` is legal and must not be merged into
 * one badge.
 */
export type VerificationState =
  | "pending"
  | "verifying"
  | "verified"
  | "inconclusive"
  | "verification_failed";

/** ApplyAction mirrors the changes.Action* constants (legacy ApplyResult.action). */
export type ApplyAction = "created" | "configured" | "unchanged" | "failed";

/** ReceiptErrorClass mirrors the changes.ErrorClass* constants recorded for failed objects. */
export type ReceiptErrorClass =
  | "conflict"
  | "forbidden"
  | "invalid"
  | "not_found"
  | "indeterminate"
  | "other";

/** ReceiptAccess is ReceiptDetail.access: how the caller qualified to read the receipt. */
export type ReceiptAccess = "owner" | "grantee" | "admin";

/** RedactionReason mirrors changes.RedactionForbidden / RedactionSecretFiltered. */
export type RedactionReason = "forbidden" | "secret-filtered";

// --- Checks (diagnostics + changes reasons) --------------------------------

/** CheckStatus mirrors diagnostics.CheckStatus. */
export type CheckStatus = "pass" | "warn" | "fail" | "inconclusive";

/** CheckSeverity mirrors diagnostics.Severity. */
export type CheckSeverity = "critical" | "warning" | "info";

/**
 * Reason codes of a check: the diagnostics.Reason* set (U20) plus the
 * changes.Reason* codes Release E adds. Open by contract.
 */
export type CheckReason = Open<
  // diagnostics
  | "ok"
  | "finding"
  | "permission_denied"
  | "source_unavailable"
  | "timed_out"
  | "internal_error"
  // changes
  | "rollout_in_progress"
  | "kind_not_supported"
  | "not_found"
  | "target_recreated"
  | "read_forbidden"
  | "window_expired"
  | "identity_unknown"
  | "outcome_unrecorded"
  | "strategy_not_supported">;

/** SourceRef mirrors diagnostics.SourceRef: the object a check observed. */
export interface SourceRef {
  clusterId: string;
  group?: string;
  version?: string;
  resource: string;
  kind: string;
  namespace?: string;
  name: string;
  uid?: string;
}

/**
 * CheckView mirrors changes.CheckView: a check after read-time redaction. A
 * redacted check keeps only checkId, status, severity, reason and observedAt.
 */
export interface CheckView {
  checkId: string;
  status: CheckStatus;
  severity: CheckSeverity;
  reason: CheckReason;
  redacted?: boolean;
  redactionReason?: RedactionReason;
  message?: string;
  detail?: string;
  remediation?: string;
  source?: SourceRef;
  evidence?: Record<string, string>;
  observedAt: string;
}

// --- GitOps ownership (gitops) ---------------------------------------------

/** OwnershipController mirrors gitops.OwnershipController. */
export type OwnershipController = "none" | "argocd" | "fluxcd" | "both";

/** OwnershipConfidence mirrors gitops.OwnershipConfidence. */
export type OwnershipConfidence =
  | "confirmed"
  | "conflicting"
  | "unknown"
  | "forbidden"
  | "unavailable";

/**
 * OwnershipEvidenceKind mirrors gitops.OwnershipEvidenceKind. Only the first
 * two are authoritative; the rest are hints writable by anyone who can write
 * the object and never raise confidence to `confirmed`.
 */
export type OwnershipEvidenceKind =
  | "argo-status-resource"
  | "flux-inventory-entry"
  | "argo-tracking-annotation"
  | "instance-label"
  | "managed-by-label"
  | "field-manager"
  | "flux-owner-label";

/** GitOpsTool mirrors gitops.Tool; the empty string is gitops.ToolNone. */
export type GitOpsTool = "" | "argocd" | "fluxcd" | "both";

/**
 * Reason codes of an OwnershipResult: the reason* set in gitops/ownership.go
 * plus changes.OwnershipReasonAppsRedacted. Open by contract.
 */
export type OwnershipReason = Open<
  | "confirmed-argo-status"
  | "confirmed-flux-inventory"
  | "both-claim"
  | "argo-list-forbidden"
  | "flux-list-forbidden"
  | "argo-unavailable"
  | "flux-unavailable"
  | "argo-destination-unverified"
  | "flux-remote-kubeconfig"
  | "flux-helmrelease-no-inventory"
  | "search-bound-exhausted"
  | "partial-visibility"
  | "hints-only"
  | "no-controller-installed"
  | "no-evidence"
  | "apps-redacted"
>;

/** AppSource mirrors gitops.AppSource: where an application's manifests come from. */
export interface AppSource {
  repoURL?: string;
  path?: string;
  targetRevision?: string;
  chartName?: string;
  chartVersion?: string;
}

/** OwnershipEvidence mirrors gitops.OwnershipEvidence: one observation about one object. */
export interface OwnershipEvidence {
  kind: OwnershipEvidenceKind;
  tool: GitOpsTool;
  /** Composite app id, only when the evidence confirmed ownership. */
  appId?: string;
  /** Hint payload, length-capped, never trusted. */
  rawValue?: string;
  note?: string;
}

/** ObjectRef mirrors gitops.ObjectRef: a live object identified for ownership and verification. */
export interface ObjectRef {
  clusterId: string;
  group?: string;
  version?: string;
  resource?: string;
  kind: string;
  namespace?: string;
  name: string;
  uid?: string;
}

/** OwnedByApp mirrors gitops.OwnedByApp: a confirming application. */
export interface OwnedByApp {
  /** Composite id, "argo:ns:name" or "flux-ks:ns:name". */
  appId: string;
  tool: GitOpsTool;
  kind: string;
  namespace: string;
  name: string;
  source: AppSource;
  suspended: boolean;
}

/** OwnershipResult mirrors gitops.OwnershipResult: the answer for exactly one ObjectRef. */
export interface OwnershipResult {
  object: ObjectRef;
  controller: OwnershipController;
  confidence: OwnershipConfidence;
  reason: OwnershipReason;
  /** Confirming applications only; never populated from hints. */
  apps?: OwnedByApp[];
  /** Everything observed, hints included, each labelled. */
  evidence?: OwnershipEvidence[];
  /** Always "group-kind-namespace-name" in Release E. */
  identityBasis: string;
  /** Always false in Release E: neither controller records a UID. */
  uidConfirmed: boolean;
  /** Always false in Release E (Q4 unresolved): never infer write capability. */
  writableGitSource: boolean;
  observedAt: string;
}

/**
 * OwnershipView mirrors changes.OwnershipView: a stored ownership entry after
 * read-time re-authorization. `redactedApps` is the count of confirming
 * applications removed because the caller may not currently get them.
 */
export interface OwnershipView extends OwnershipResult {
  redactedApps?: number;
}

/** OwnershipObjectRef mirrors changes.OwnershipObjectRef: one object in an ownership request. */
export interface OwnershipObjectRef {
  group?: string;
  version?: string;
  kind: string;
  namespace?: string;
  name: string;
}

/** OwnershipRequest mirrors changes.OwnershipRequest: the POST /v1/changes/ownership body. */
export interface OwnershipRequest {
  /** Optional; when present it must equal the request's X-Cluster-ID. */
  clusterId?: string;
  objects: OwnershipObjectRef[];
}

/** OwnershipResponse mirrors changes.OwnershipResponse; results are in request order. */
export interface OwnershipResponse {
  clusterId: string;
  results: OwnershipResult[];
}

// --- Tracked apply (changes) -----------------------------------------------

/** VerificationLink mirrors changes.VerificationLink: where to poll and where it stands. */
export interface VerificationLink {
  state: VerificationState;
  url: string;
}

/** TrackedObjectRef mirrors changes.TrackedObjectRef: the reference (never the content) of a recorded object. */
export interface TrackedObjectRef {
  index: number;
  group?: string;
  version?: string;
  resource?: string;
  uid?: string;
}

/**
 * ApplyTracking mirrors changes.ApplyTracking: the additive block of a
 * tracked apply response.
 *
 * Three counts describe documents without a recorded success. `notAttempted`
 * were provably never sent to the cluster (safe to re-preview and retry).
 * `unrecorded` have no recorded outcome although the mutation window was
 * open: the cluster MAY hold them, so never retry blind.
 */
export interface ApplyTracking {
  operationId: string;
  receiptUrl: string;
  state: ReceiptState;
  clusterId: string;
  clusterGeneration: string;
  contentDigest: string;
  recordedThrough: number;
  notAttempted: number;
  unrecorded: number;
  /** The response is a replay of an earlier attempt under the same operation id. */
  replayed: boolean;
  containsSecret: boolean;
  /** Operation id of the receipt this apply repairs; a link only. */
  repairOf?: string;
  objects: TrackedObjectRef[];
  verification: VerificationLink;
  warnings: string[];
}

// --- Receipt reads (changes) -----------------------------------------------

/** ReceiptView mirrors changes.ReceiptView: the envelope of a receipt. */
export interface ReceiptView {
  operationId: string;
  receiptUrl: string;
  ownerUsername: string;
  state: ReceiptState;
  clusterId: string;
  clusterGeneration: string;
  /** The target cluster was re-registered since the receipt was written. */
  targetGenerationChanged: boolean;
  /** Blank for a Secret-bearing receipt read by anyone but its owner. */
  contentDigest: string;
  documentCount: number;
  recordedThrough: number;
  force: boolean;
  containsSecret: boolean;
  repairOf?: string;
  verification: VerificationLink;
  createdAt: string;
  mutationStartedAt?: string;
  completedAt?: string;
  verifiedAt?: string;
}

/**
 * ReceiptObjectView mirrors changes.ReceiptObjectView: one recorded object.
 * Every field except `index`, `redacted` and `reason` is absent when
 * `redacted` is true.
 */
export interface ReceiptObjectView {
  index: number;
  redacted?: boolean;
  reason?: RedactionReason;
  group?: string;
  version?: string;
  resource?: string;
  kind?: string;
  namespace?: string;
  name?: string;
  uid?: string;
  action?: ApplyAction;
  /** Never emitted for a Secret-bearing receipt or a Secret object. */
  error?: string;
  errorClass?: ReceiptErrorClass;
}

/** ReceiptSummary mirrors changes.ReceiptSummary: outcome counts computed before redaction. */
export interface ReceiptSummary {
  total: number;
  created: number;
  configured: number;
  unchanged: number;
  failed: number;
  /** Submitted documents without a recorded outcome. */
  notRecorded: number;
}

/** ReceiptDetail mirrors changes.ReceiptDetail: the GET /v1/changes/{id} body. */
export interface ReceiptDetail extends ReceiptView {
  access: ReceiptAccess;
  summary: ReceiptSummary;
  objects: ReceiptObjectView[];
  redactedObjects: number;
  checks: CheckView[];
  redactedChecks: number;
  ownership: OwnershipView[];
  redactedOwnership: number;
}

/** VerificationView mirrors changes.VerificationView: the GET /v1/changes/{id}/verification body. */
export interface VerificationView {
  state: VerificationState;
  checks: CheckView[];
  redactedChecks: number;
  /** Non-zero only while `state` is "verifying". */
  retryAfterSeconds?: number;
}

// --- Tracked-apply refusals (changes) --------------------------------------

/**
 * TrackedApplyRefusalReason mirrors the changes.ReasonOperation* and
 * ReasonReceiptStoreUnavailable error codes (409 and 503).
 */
export type TrackedApplyRefusalReason =
  | "operation_id_conflict"
  | "operation_in_flight"
  | "operation_id_reused"
  | "receipt_store_unavailable";
