---
status: complete
priority: p1
issue_id: 274
tags: [security, rbac, policy, code-review, pr-163]
dependencies: []
---

## Problem Statement

PR #163 introduced per-policy `ViolationCount` aggregation for Kyverno inside `doFetch` (`backend/internal/policy/handler.go`), which mutates the shared pre-RBAC cache. The count therefore reflects cluster-wide violation totals regardless of which user is asking — a user who cannot see namespace X still sees the X-namespace violations contribute to the count on a policy they *can* read.

This is a cross-RBAC information leak: the count becomes a low-bandwidth side channel that reveals the existence of violations in namespaces the user has no access to. It is also inconsistent with the `/policies/violations` response, which *is* filtered.

## Findings

- `handler.go` `doFetch`: `kr.policies[i].ViolationCount = c` is set on the slice stored in `h.cachedData`. The same cache is returned to every caller via `fetchPoliciesAndViolations`.
- `filterPoliciesByRBAC` runs per-request in `HandleListPolicies` but only drops rows — it does not recompute `ViolationCount`, so the cluster-wide number is returned verbatim.
- Gatekeeper's `ViolationCount` is populated inside `normalizeGatekeeper` from `status.violations` (constraint-local, not a cross-namespace aggregate), so it does not have the same problem for Gatekeeper — but any future work that aggregates post-cache inherits this pattern.

## Proposed Solutions

### Option A — Compute counts per-request after RBAC filtering (smallest fix)
In `HandleListPolicies`, after `filterPoliciesByRBAC` and `filterViolationsByRBAC`, build a `map[matchKey]int` from the filtered violations and write counts onto a local copy of the filtered policies before responding.
- Pros: minimal change, keeps cache user-agnostic, fixes the leak.
- Cons: doesn't help other endpoints that might later use `ViolationCount` off the cache; still needs the composite-ID→k8s-name conversion (or `MatchKey`).
- Effort: Small.
- Risk: Low.

### Option B — Don't cache `ViolationCount` at all; compute on the wire
Move count aggregation into a small helper called by each handler after filtering. Keep `NormalizedPolicy.ViolationCount` as a response-only field populated per request.
- Pros: Single code path, symmetric with Gatekeeper (which could adopt the same helper), prevents future regressions.
- Cons: Slightly more refactor; Gatekeeper currently populates `ViolationCount` at normalization time and that behavior changes.
- Effort: Small–Medium.
- Risk: Low (need to confirm Gatekeeper callers tolerate the move).

### Option C — Add `NormalizedPolicy.MatchKey` and compute counts post-filter (combines with #260)
Implement Option A using the `MatchKey` field from todo 276 so the engine-aware `kyvernoK8sName` reverse lookup disappears entirely.
- Pros: Fixes the leak and the type smell in one pass; unifies Kyverno and Gatekeeper count paths.
- Cons: Touches more files.
- Effort: Medium.
- Risk: Low.

## Recommended Action

## Technical Details

**Affected files:**
- `backend/internal/policy/handler.go` — `doFetch`, `HandleListPolicies`
- `backend/internal/policy/kyverno.go` — only if Option C

**Regression test to add:**
- Two users with disjoint namespace RBAC, same cluster-scoped policy with violations spread across both namespaces. Assert each user's `ViolationCount` differs and matches only their visible violations.

## Acceptance Criteria

- [ ] `ViolationCount` on the response reflects only violations the requesting user can see via RBAC.
- [ ] Cached data in `h.cachedData.policies` carries the service-account-visible data only (no per-request mutations).
- [ ] Table-driven unit test covers the two-user disjoint-namespace case.
- [ ] `go vet ./...` and `go test ./internal/policy/...` pass.

## Work Log

_2026-04-10_: Discovered during `/review` on PR #163 by data-integrity-guardian agent. Confirmed by reading handler.go — the mutation is on the cached slice, not a per-request copy.

## Resources

- maulepilot117/k8sCenter#163 — the PR that introduced this regression
- `backend/internal/policy/handler.go:145-180` — the aggregation loop
- Related: todo 276 (MatchKey field refactor), todo 275 (move aggregation into adapter)
