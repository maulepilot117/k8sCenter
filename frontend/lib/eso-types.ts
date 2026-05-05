/**
 * External Secrets Operator (ESO) types — mirrors backend/internal/externalsecrets/types.go.
 *
 * The Phase A Go-side hash test (`types_hash_test.go`) pins the exported field
 * shape of each backend type. Any drift between these TS interfaces and that
 * pinned hash means one side needs to be updated.
 *
 * Phase A surface is read-only observatory. Phase D will populate the
 * threshold-resolved fields (StaleAfterMinutes, AlertOnRecovery, etc.); they
 * are typed as optional / nullable here so the wire shape is final from day
 * one.
 */

export type Status =
  | "Synced"
  | "SyncFailed"
  | "Refreshing"
  | "Stale"
  | "Drifted"
  | "Unknown";

export type DriftStatus = "InSync" | "Drifted" | "Unknown";

/** Reason codes accompanying DriftStatus="Unknown" on detail responses.
 * Operationally distinct states the drift resolver collapses into Unknown:
 * the synced Secret has no resourceVersion (provider didn't populate it),
 * the Secret was deleted, the user lacks `get secret` perm, etc. */
export type DriftUnknownReason =
  | "no_synced_rv"
  | "no_target_name"
  | "secret_deleted"
  | "rbac_denied"
  | "transient_error"
  | "client_error";

/** Layer of the resolution chain that supplied an annotation-resolved value.
 * Phase D's resolver populates these per-key on the ExternalSecret. */
export type ThresholdSource =
  | "default"
  | "externalsecret"
  | "secretstore"
  | "clustersecretstore";

export interface ESOStatus {
  detected: boolean;
  namespace?: string;
  version?: string;
  lastChecked: string;
}

export interface StoreRef {
  name: string;
  /** "SecretStore" or "ClusterSecretStore". */
  kind: string;
}

export interface ExternalSecret {
  namespace: string;
  name: string;
  uid: string;
  status: Status;
  driftStatus?: DriftStatus;
  /** Reason when driftStatus is "Unknown". Detail endpoint only. */
  driftUnknownReason?: DriftUnknownReason;
  readyReason?: string;
  readyMessage?: string;
  storeRef: StoreRef;
  targetSecretName?: string;
  /** Duration string, e.g. "1h", "30m". */
  refreshInterval?: string;
  lastSyncTime?: string;
  syncedResourceVersion?: string;
  /** Phase D fields — undefined / null in Phase A responses. */
  staleAfterMinutes?: number | null;
  staleAfterMinutesSource?: ThresholdSource;
  alertOnRecovery?: boolean | null;
  alertOnRecoverySource?: ThresholdSource;
  alertOnLifecycle?: boolean | null;
  alertOnLifecycleSource?: ThresholdSource;
  /**
   * Phase C: poller's last-observed drift state, populated on the list
   * response only (the detail endpoint resolves DriftStatus live and
   * leaves this field undefined). Stale by up to 90s — 60s poller
   * cycle + 30s handler cache. The detail page is source of truth.
   */
  lastObservedDriftStatus?: DriftStatus;
}

export interface ClusterExternalSecret {
  name: string;
  uid: string;
  status: Status;
  readyReason?: string;
  readyMessage?: string;
  storeRef: StoreRef;
  targetSecretName?: string;
  refreshInterval?: string;
  /** Selector clauses rendered as "k=v" strings. */
  namespaceSelectors?: string[];
  /** Static namespace list (alternative to selector). */
  namespaces?: string[];
  provisionedNamespaces?: string[];
  failedNamespaces?: string[];
  externalSecretBaseName?: string;
}

export interface SecretStore {
  /** Empty for ClusterSecretStore. */
  namespace?: string;
  name: string;
  uid: string;
  scope: "Namespaced" | "Cluster";
  status: Status;
  ready: boolean;
  readyReason?: string;
  readyMessage?: string;
  /** Provider family ("vault", "aws", "gcp", "azurekv", "kubernetes", etc.) — empty when no provider key is set. */
  provider: string;
  /** Raw spec.provider.<provider> sub-object, surfaced verbatim. Treat as
   * read-only infrastructure addressing info, not credentials. */
  providerSpec?: Record<string, unknown>;
  /** Phase D — annotation-set thresholds inherited by ESes referencing this store. */
  staleAfterMinutes?: number | null;
  alertOnRecovery?: boolean | null;
  alertOnLifecycle?: boolean | null;
}

export interface PushSecret {
  namespace: string;
  name: string;
  uid: string;
  status: Status;
  readyReason?: string;
  readyMessage?: string;
  storeRefs: StoreRef[];
  sourceSecretName?: string;
  refreshInterval?: string;
  lastSyncTime?: string;
}

// --- Phase E bulk refresh types --------------------------------------------

export type BulkRefreshAction =
  | "refresh_store"
  | "refresh_cluster_store"
  | "refresh_namespace";

export interface BulkScopeTarget {
  namespace: string;
  name: string;
  uid: string;
}

export interface BulkNamespaceCount {
  namespace: string;
  count: number;
}

/**
 * GET refresh-scope payload. The dialog uses totalCount/visibleCount to render
 * the "Showing only resources you can refresh" RBAC notice.
 */
export interface BulkScopeResponse {
  action: BulkRefreshAction;
  scopeTarget: string;
  totalCount: number;
  totalNamespaces: number;
  visibleCount: number;
  /** True when the SA-view total exceeds what the user can refresh. */
  restricted: boolean;
  targets: BulkScopeTarget[];
  byNamespace: BulkNamespaceCount[];
}

export interface BulkRefreshOutcome {
  uid: string;
  reason: string;
}

/** Phase F — per-store rate + cost-tier metrics. */
export interface CostEstimate {
  billingProvider: string;
  currency?: string;
  usdPerMillion?: number;
  estimated24h?: number;
  /** ISO8601 — date the rate-card snapshot was last hand-revised. */
  lastUpdated?: string;
}

export interface StoreMetrics {
  /** sum(rate(externalsecret_sync_calls_total[5m])) projected to per-minute.
   *  Null when Prometheus has no series yet OR is offline (see `error`). */
  ratePerMin: number | null;
  /** Total requests in the last 24h. Same null semantics as ratePerMin. */
  last24h: number | null;
  /** Suppressed for self-hosted / unknown providers. */
  cost?: CostEstimate | null;
  /** Populated on degradation; HTTP is still 200 in that case. */
  error?: string;
  /** ISO8601 sample timestamp; "" when degraded. */
  windowEnd?: string;
}

// --- Phase H wizard types ---------------------------------------------------

/**
 * The 12 SecretStore provider keys the wizard recognizes. Mirrors the Go
 * `SecretStoreProvider` enum in `backend/internal/wizard/secretstore.go`.
 *
 * "awsps" is a synthetic UX discriminator — ESO v1 has no such provider key.
 * Both AWS Secrets Manager and AWS Parameter Store live under spec.provider.aws;
 * the backend injects service: ParameterStore when the wizard sends "awsps".
 *
 * Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud
 * Vault, Alibaba KMS, custom webhook) ship as YAML templates only (Phase H
 * Unit 20) and are not in this set.
 */
export type SecretStoreProvider =
  | "vault"
  | "aws"
  | "awsps"
  | "azurekv"
  | "gcpsm"
  | "kubernetes"
  | "akeyless"
  | "doppler"
  | "onepasswordsdk"
  | "bitwardensecretsmanager"
  | "conjur"
  | "infisical";

/**
 * Set of provider keys that have a fully-implemented guided form.
 * Single edit point as Unit 19 sub-PRs ship per-provider forms.
 * A provider NOT in this set is shown as "coming soon" in the picker.
 */
export const READY_SECRET_STORE_PROVIDERS = new Set<SecretStoreProvider>([
  "vault",
]);

// --- Phase G path-discovery types ------------------------------------------

/**
 * Response shape for GET /externalsecrets/stores/{ns}/{name}/paths.
 * - `supported=false` → frontend renders a free-text path field.
 * - `paths` may be absent (RBAC denied) or empty (namespace has no Secrets).
 *   Callers must treat absent and [] identically.
 * - `truncated=true` → result capped at the server-side limit; user should
 *   narrow the prefix to see more results.
 */
export interface PathDiscoveryResponse {
  supported: boolean;
  provider?: string;
  paths?: string[];
  truncated?: boolean;
}

/** GET /externalsecrets/bulk-refresh-jobs/{jobId} payload. */
export interface BulkRefreshJob {
  jobId: string;
  clusterId: string;
  requestedBy: string;
  action: BulkRefreshAction;
  scopeTarget: string;
  targetCount: number;
  createdAt: string;
  /** When non-null, the worker has finished processing the job. */
  completedAt?: string;
  succeeded: string[];
  failed: BulkRefreshOutcome[];
  skipped: BulkRefreshOutcome[];
}
