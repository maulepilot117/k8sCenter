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
