/**
 * Types matching backend/internal/server/handle_capabilities.go (U8).
 *
 * GET /api/v1/capabilities/{clusterID} answers "what can I actually do
 * against cluster X right now?" with six independent dimensions per
 * operation, never collapsed into one boolean — see D3 in
 * docs/plans/2026-09-10-release-c-remote-workflow-impl.md and
 * .superpowers/sdd/2026-09-10-release-c-remote-workflow-impl/u8-brief.md.
 *
 * This module is types-only: no fetch calls, no islands, no components.
 * The client that calls this endpoint is a later unit (U11b).
 */

/**
 * The complete set of machine-readable reasons a capability row is not
 * plain "ok". A `switch` over `ReasonCode` with no `default` case makes an
 * unhandled member a compile error under `deno check` — that exhaustiveness
 * is the point: a UI must render `unreachable` and `forbidden` differently
 * from `unsupported_platform` (see U11b), so a silently-ignored new code
 * would be a real regression, not a cosmetic one.
 */
export type ReasonCode =
  | "ok"
  | "unsupported_platform"
  | "discovery_missing"
  | "discovery_unavailable"
  | "unreachable"
  | "stale_observation"
  | "forbidden"
  | "authz_unknown"
  | "cluster_unknown"
  | "credentials_invalid"
  | "db_unavailable";

/** Operation ids currently published by the capabilities endpoint. */
export type CapabilityOperationId =
  | "yaml.validate"
  | "yaml.diff"
  | "yaml.export"
  | "yaml.apply"
  | "dashboard.summary"
  | "resources.counts"
  | "pod.exec"
  | "logs.stream"
  | "logs.search"
  | "flows.stream"
  | "eso.write";

/**
 * One operation's six-dimension capability row.
 *
 * `platformSupported`, `discoveryPresent`, `reachable`, and `authorized` are
 * deliberately independent — `unreachable` and `forbidden` must never be
 * rendered as "not supported": a cluster that is temporarily down, or an
 * identity that currently lacks RBAC, is a different (and often recoverable)
 * situation from an operation k8sCenter has genuinely never implemented for
 * that target class.
 */
export interface Capability {
  /** e.g. "yaml.apply" — see {@link CapabilityOperationId}. */
  operation: CapabilityOperationId;
  /** Human-readable label for display, e.g. "Apply YAML". */
  label: string;
  /**
   * k8sCenter has implemented this operation for this target class (local
   * vs remote). Static per operation x class — does not depend on whether
   * the target happens to be resolvable right now.
   */
  platformSupported: boolean;
  /**
   * The target's discovery actually contains the group/resource this
   * operation needs. `null` when the operation needs no specific GVR (most
   * operations — see D3/A2) or when discovery could not be evaluated.
   */
  discoveryPresent: boolean | null;
  /**
   * Last observation says the target answers. `null` when unknown or stale
   * (older than 3x the 60s probe interval) — never guessed.
   */
  reachable: boolean | null;
  /**
   * This identity's SAR verdict for the operation's representative
   * verb/resource. `null` when the SAR could not be evaluated.
   */
  authorized: boolean | null;
  /** RFC3339 timestamp of the weakest-freshness input contributing to this row. */
  observedAt: string;
  /** Why this row is not plain "ok". Always a member of {@link ReasonCode}. */
  reasonCode: ReasonCode;
}

/** Body of GET /api/v1/capabilities/{clusterID}. */
export interface CapabilitiesResponse {
  /** Normalized cluster id ("local" for the local cluster). */
  clusterId: string;
  capabilities: Capability[];
}
