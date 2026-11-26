/**
 * Wire types for the Rubrik Security Cloud backup provider (preview).
 *
 * Mirrors the Go shapes in backend/internal/rubrik/handler.go. The endpoint
 * that serves them, GET /v1/rubrik/status, answers 404 while the provider is
 * switched off (rubrik.enabled), so a 404 is the only "disabled" signal.
 */

/**
 * The selected cluster's Rubrik state. Non-admins never see the three
 * set-up states: those collapse to `not_set_up` for them.
 */
export type RubrikClusterState =
  | "mapped"
  | "not_configured"
  | "not_mapped"
  | "mapping_invalid"
  | "not_set_up"
  | "admin_only"
  | "unavailable";

/**
 * Health of the RSC connection. Failures are reported by class only; RSC's
 * own error text is never returned.
 */
export type RubrikConnectionHealth =
  | "not_configured"
  | "secret_required"
  | "ok"
  | "auth_failed"
  | "unreachable"
  | "failing"
  | "unknown"
  | "unavailable";

/** One cluster mapping as admins see it. */
export interface RubrikMapping {
  clusterId: string;
  rscClusterId: string;
  rscClusterName: string;
  state: RubrikClusterState;
  updatedBy: string;
  updatedAt: string;
}

/** The selected cluster's Rubrik state; `mapping` is set for admins only. */
export interface RubrikClusterStatus {
  id: string;
  state: RubrikClusterState;
  mapping?: RubrikMapping;
}

/** The stored connection as admins see it. The client secret is never sent. */
export interface RubrikConnection {
  configured: boolean;
  tenantUrl?: string;
  clientId?: string;
  hasSecret: boolean;
  revision?: number;
  updatedBy?: string;
  updatedAt?: string;
  health: RubrikConnectionHealth;
  lastSuccessAt?: string;
  lastFailureAt?: string;
  lastFailureKind?: string;
}

/** GET /v1/rubrik/status. `connection` is set for admins only. */
export interface RubrikStatus {
  enabled: boolean;
  preview: boolean;
  writesEnabled: boolean;
  cluster: RubrikClusterStatus;
  connection?: RubrikConnection;
}

// ---------------------------------------------------------------------------
// Reads (backend/internal/rubrik/reads.go and model.go)

export type RubrikCompliance =
  | "unknown"
  | "in_compliance"
  | "out_of_compliance";

/** RSC's job status as the reads normalize it. */
export type RubrikJobState =
  | "running"
  | "succeeded"
  | "failed"
  | "partial"
  | "canceled"
  | "unknown";

/** How current a part of a read answer is. `error` is the failed fetch's class. */
export interface RubrikFreshness {
  fetchedAt: string;
  stale: boolean;
  error?: string;
}

export interface RubrikSlaRef {
  id: string;
  name: string;
}

/**
 * The write controls the UI may offer, computed by the server from the
 * user's grants, the writes setting, open operations and the admin-only
 * rules. The UI shows controls from these and never re-derives them.
 */
export interface RubrikActionFlags {
  canSnapshot: boolean;
  canChangeSla: boolean;
  canRestore: boolean;
}

export interface RubrikObject extends RubrikActionFlags {
  family: "namespace" | "protectionSet";
  id: string;
  /** The namespace for a per-namespace object, the set name for a set. */
  name: string;
  setType?: string;
  coverage: { all?: boolean; namespaces?: string[]; unresolved?: boolean };
  configuredSla: RubrikSlaRef;
  effectiveSla: RubrikSlaRef;
  slaAssignment?: string;
  newestSnapshot?: string;
  relic?: boolean;
  mayHoldClusterScoped: boolean;
  compliance: RubrikCompliance;
}

export interface RubrikNamespaceRow extends RubrikActionFlags {
  namespace: string;
  protected: boolean;
  compliance: RubrikCompliance;
  lastSnapshot?: string;
  objectIds: string[];
  /** The objects an action on the namespace may target (R15). */
  actionTargets: string[];
  /** More objects cover the namespace than the user may see. */
  hiddenObjects?: boolean;
}

/** GET /v1/rubrik/namespaces. */
export interface RubrikNamespaceList {
  noAccess: boolean;
  namespaces: RubrikNamespaceRow[];
  objects: RubrikObject[];
  freshness?: RubrikFreshness;
}

export interface RubrikSnapshot {
  id: string;
  time: string;
  expires?: string;
  expired?: boolean;
  onDemand?: boolean;
  corrupted?: boolean;
  quarantined?: boolean;
}

export interface RubrikJob {
  id: string;
  objectId: string;
  type: string;
  status: RubrikJobState;
  startTime?: string;
  lastUpdated?: string;
  message?: string;
  failureReason?: string;
}

export interface RubrikObjectDetail extends RubrikObject {
  snapshots: RubrikSnapshot[];
  snapshotsFreshness?: RubrikFreshness;
  snapshotsError?: string;
  jobs: RubrikJob[];
  jobsFreshness?: RubrikFreshness;
  jobsError?: string;
}

/** GET /v1/rubrik/namespaces/{namespace}. */
export interface RubrikNamespaceDetail {
  namespace: RubrikNamespaceRow;
  objects: RubrikObjectDetail[];
  freshness: RubrikFreshness;
}

export interface RubrikSlaDomain {
  id: string;
  name: string;
  /** The most frequent schedule's interval in nanoseconds, 0 when unknown. */
  frequency: number;
}

/** GET /v1/rubrik/sla-domains. */
export interface RubrikSlaDomainList {
  noAccess: boolean;
  slaDomains: RubrikSlaDomain[];
  freshness?: RubrikFreshness;
}

// ---------------------------------------------------------------------------
// Writes (backend/internal/rubrik/writes.go and restore.go)

export type RubrikOperationKind = "snapshot" | "sla_assign" | "restore";

export type RubrikOperationState =
  | "applying"
  | "running"
  | "succeeded"
  | "failed"
  | "partial"
  | "unknown";

/** A write as the write endpoints and GET /v1/rubrik/operations/{id} return it. */
export interface RubrikOperation {
  id: string;
  kind: RubrikOperationKind;
  clusterId: string;
  namespace: string;
  objectId: string;
  state: RubrikOperationState;
  jobFamily?: string;
  jobId?: string;
  /** Why a failed, partial or unknown operation ended so. */
  message?: string;
  createdAt: string;
  updatedAt: string;
  finishedAt?: string;
  /** The request repeated an earlier one and was answered from its record. */
  replayed?: boolean;
  /** For an SLA removal: whether another visible object still protects. */
  stillProtected?: boolean;
  /** The class of a failed job refresh; the state is the last recorded. */
  refreshError?: string;
}

/** GET /v1/rubrik/namespaces/{namespace}/restore-targets?objectId=. */
export interface RubrikRestoreTargets {
  objectId: string;
  sourceNamespace: string;
  /** A protection set restores only into the namespaces it protects. */
  inPlaceOnly: boolean;
  targets: string[];
  /** Why no restore from the object may be offered; targets is then empty. */
  refusal?: string;
  overwriteNotice: string;
}
