/**
 * Pure derivations for the compliance dashboard's violations preview.
 *
 * The dashboard loads the full `/v1/policies/violations` list (same endpoint
 * as the Violations browser) and derives two compact views from it:
 *   - failingPolicies(): grouped "which policies are failing, by impact"
 *   - worstResources(): the most severe individual violating resources
 *
 * Namespace scoping mirrors the backend `computeCompliance` semantics — a
 * namespace view counts only violations whose namespace === scope — so the
 * preview stays consistent with the score shown above it.
 */
import { SEVERITY_ORDER } from "@/lib/badge-colors.ts";
import type { NormalizedViolation } from "@/lib/policy-types.ts";

/** Lower rank = more severe. Unknown severities sort last. */
export function severityRank(severity: string): number {
  const i = (SEVERITY_ORDER as readonly string[]).indexOf(severity);
  return i === -1 ? SEVERITY_ORDER.length : i;
}

/**
 * Filter violations to the active namespace, matching the score's scoping.
 * "all" (or empty) returns every violation unchanged.
 */
export function scopeViolations(
  violations: NormalizedViolation[],
  ns: string,
): NormalizedViolation[] {
  if (!ns || ns === "all") return violations;
  return violations.filter((v) => v.namespace === ns);
}

/** One row of the "Failing Policies" summary. */
export interface PolicyGroup {
  policy: string;
  severity: string;
  engine: string;
  blocking: boolean;
  count: number;
}

/**
 * Group violations by policy, counting offending resources. Sorted blocking
 * (enforced) first, then by resource count desc, then policy name for
 * stability. `severity` is the most severe seen across the group.
 */
export function failingPolicies(
  violations: NormalizedViolation[],
  limit: number,
): PolicyGroup[] {
  const groups = new Map<string, PolicyGroup>();
  for (const v of violations) {
    const existing = groups.get(v.policy);
    if (existing) {
      existing.count++;
      existing.blocking = existing.blocking || v.blocking;
      if (severityRank(v.severity) < severityRank(existing.severity)) {
        existing.severity = v.severity;
      }
    } else {
      groups.set(v.policy, {
        policy: v.policy,
        severity: v.severity,
        engine: v.engine,
        blocking: v.blocking,
        count: 1,
      });
    }
  }
  return [...groups.values()]
    .sort((a, b) =>
      Number(b.blocking) - Number(a.blocking) ||
      b.count - a.count ||
      a.policy.localeCompare(b.policy)
    )
    .slice(0, limit);
}

/**
 * Top-N individual violations, most severe first, blocking breaking ties.
 * Does not mutate the input.
 */
export function worstResources(
  violations: NormalizedViolation[],
  limit: number,
): NormalizedViolation[] {
  return [...violations]
    .sort((a, b) =>
      severityRank(a.severity) - severityRank(b.severity) ||
      Number(b.blocking) - Number(a.blocking)
    )
    .slice(0, limit);
}
