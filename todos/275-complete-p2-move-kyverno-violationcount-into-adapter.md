---
status: complete
priority: p2
issue_id: 275
tags: [architecture, policy, code-review, pr-163]
dependencies: [274]
---

## Problem Statement

`ViolationCount` aggregation for Kyverno lives in `backend/internal/policy/handler.go` (HTTP layer), while Gatekeeper populates it inside its normalizer (`gatekeeper.go`). The HTTP handler is engine-aware via `if p.Engine == EngineKyverno` checks and an extra `kyvernoK8sName` helper — cross-cutting logic in the wrong layer.

Handler code should not special-case engines. Each adapter should return fully populated `NormalizedPolicy` values.

## Findings

- `handler.go doFetch` (~L150-180): runs `kyvernoCounts` map build and writes `ViolationCount` into policies.
- `handler.go computeCompliance` (~L450): has `if p.Engine == EngineKyverno { lookupKey = kyvernoK8sName(p.ID) }` — same asymmetry.
- `gatekeeper.go:141-155`: sets `ViolationCount` at normalization time.
- Architect agent flag: "Moving it is ~15 lines and removes the handler's engine-awareness."

## Proposed Solutions

### Option A — Aggregate inside `listKyvernoPoliciesAndViolations` wrapper
Introduce a small wrapper in `kyverno.go` that calls `listKyvernoPolicies` + `listKyvernoViolations` and returns policies with `ViolationCount` already populated. Handler stays engine-agnostic.
- Pros: Symmetric with Gatekeeper, kills the engine switch in `computeCompliance`, local change.
- Cons: Still needs reconciliation with todo 274 — if we compute counts pre-cache, they are not RBAC-filtered. This option only makes sense if counts are service-account-wide (current semantics, which todo 274 rejects).
- Effort: Small.
- Risk: Blocks on todo 274 decision.

### Option B — Do it together with todo 274 and todo 276
Fold this into the post-filter, per-request aggregation using `MatchKey` (todo 276). The engine-awareness disappears because there are no engine special-cases — every policy has a `MatchKey`, every violation has a `Policy` that equals it.
- Pros: Solves all three todos in one refactor; smallest final code footprint.
- Cons: Biggest single change.
- Effort: Medium.
- Risk: Low.

## Recommended Action

## Technical Details

**Affected files:**
- `backend/internal/policy/handler.go`
- `backend/internal/policy/kyverno.go`
- `backend/internal/policy/types.go` (if adopting MatchKey)

## Acceptance Criteria

- [ ] No `if p.Engine == EngineKyverno` branches in `handler.go`.
- [ ] `kyvernoK8sName` helper removed from `handler.go` (or moved to `kyverno.go` if still needed internally).
- [ ] `computeCompliance` has no engine-specific lookup.

## Work Log

_2026-04-10_: Discovered during `/review` on PR #163 by architecture-strategist and code-simplicity-reviewer.

## Resources

- Related: todo 274 (the P1 this should be fixed alongside), todo 276 (MatchKey field).
