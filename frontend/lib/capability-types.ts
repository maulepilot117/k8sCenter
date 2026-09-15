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
 *
 * This array MUST stay in lockstep with validReasonCodes in
 * backend/internal/server/handle_capabilities.go. That cross-language check
 * is made from the Go side only: backend/internal/server/capability_parity_test.go
 * reads this file's source text and diffs it against the live Go values.
 * capability-types_test.ts cannot see the Go source, so it instead checks
 * that this array and the ReasonCode union type derived from it stay
 * self-consistent — a real but narrower guarantee.
 */
export const REASON_CODES = [
  "ok",
  "unsupported_platform",
  "discovery_missing",
  "discovery_unavailable",
  "unreachable",
  "stale_observation",
  "forbidden",
  "authz_unknown",
  "authz_namespace_scoped",
  "cluster_unknown",
  "credentials_invalid",
  "db_unavailable",
] as const;

export type ReasonCode = (typeof REASON_CODES)[number];

/**
 * Operation ids currently published by the capabilities endpoint.
 *
 * This array MUST stay in lockstep with capabilityOperations in
 * backend/internal/server/handle_capabilities.go — same parity-test pairing
 * as REASON_CODES above (Go-side test cross-checks this file's source text;
 * the TS-side test only checks this array against the CapabilityOperationId
 * union it derives).
 */
export const CAPABILITY_OPERATION_IDS = [
  "yaml.validate",
  "yaml.diff",
  "yaml.export",
  "yaml.apply",
  "dashboard.summary",
  "resources.counts",
  "pod.exec",
  "logs.stream",
  "logs.search",
  "flows.stream",
  "eso.write",
] as const;

export type CapabilityOperationId = (typeof CAPABILITY_OPERATION_IDS)[number];

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
   * verb/resource.
   *
   * `null` carries TWO distinct meanings, and `reasonCode` is what tells
   * them apart:
   *
   * - `authz_unknown` — the SAR could not be evaluated (it errored, or no
   *   AccessChecker is wired). Nothing was learned.
   * - `authz_namespace_scoped` — the SAR WAS evaluated and returned "no",
   *   but the probe is issued cluster-wide (empty namespace) while the
   *   operation is namespaced, so the denial only proves this identity
   *   lacks the permission in EVERY namespace. An ordinary namespaced Role
   *   (edit/admin in one namespace) answers exactly that way and can still
   *   perform the operation where it actually works, so namespace-scoped
   *   access is NOT ruled out.
   *
   * In both cases `null` means "indeterminate" and MUST NOT be rendered as
   * permitted — no affordance may be enabled, and no "you have access"
   * copy shown, on the strength of a `null`. Render it as unknown (and, for
   * `authz_namespace_scoped`, as "may be available in your namespaces"),
   * never as `true` and never silently as `false` either: a definite
   * `false` is only ever reported with `forbidden`, on a cluster-scoped
   * operation where the cluster-wide question was exact.
   */
  authorized: boolean | null;
  /**
   * RFC3339 timestamp of the reachability observation this row is based on
   * ("now" for local — no probe cycle involved — or the cluster record's
   * last-probed time for remote). This is NOT the weakest-freshness input
   * overall: `authorized` can come from a SelfSubjectAccessReview verdict
   * cached for up to 60s (accessCacheTTL, internal/k8s/resources/access.go),
   * so on the local path `observedAt` can read "now" while the authorization
   * input it sits next to is up to a minute old.
   */
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
