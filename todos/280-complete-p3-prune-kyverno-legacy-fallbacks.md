---
status: complete
priority: p3
issue_id: 280
tags: [cleanup, policy, code-review, pr-163]
dependencies: [277]
---

## Problem Statement

PR #163 added several "legacy schema" fallbacks in `kyverno.go` that may be speculative — untested branches hedging against Kyverno versions the project does not actually support. Each untested branch is a liability.

## Findings

Fallbacks added in PR #163:
1. `status.ready` bool fallback (after `status.conditions[Ready]`) — Kyverno removed this field years ago; no modern cluster emits it.
2. Per-result `resources[]` override on PolicyReports (after top-level `scope`) — serves `ClusterPolicyReport` shapes but no test confirms.
3. String-typed timestamp fallback (after `{seconds, nanos}` object) — dead code unless we ingest older reports.
4. Legacy flat `match.resources.kinds` (after `match.any/all`) — required since Kyverno v1.8.

Code-simplicity-reviewer agent: "Each fallback is a branch you don't test. Delete anything you can't point at a live cluster emitting."

## Proposed Solutions

### Option A — Delete all unproven fallbacks
Drop branches 1, 3, 4. Keep branch 2 only if `ClusterPolicyReport` is confirmed to produce the per-result shape.
- Pros: Fewer branches, clearer code, tests can cover the whole file.
- Cons: Breaks support for hypothetical old Kyverno clusters.
- Effort: Small.
- Risk: Low, assuming the project's supported minimum is Kyverno 1.11+.

### Option B — Keep fallbacks, cover with fixture tests (depends on todo 277)
Add test fixtures for the legacy shapes to turn each fallback into tested code.
- Pros: No regression risk.
- Cons: More test fixtures to maintain for shapes we don't actually use.
- Effort: Small (on top of 261).

## Recommended Action

## Technical Details

Declare minimum supported Kyverno version in `docs/` or `README` before making cuts.

## Acceptance Criteria

- [ ] Either all fallback branches are covered by tests, or the unreachable ones are deleted.
- [ ] Minimum supported Kyverno version is documented.

## Work Log

_2026-04-10_: Identified during `/review` on PR #163 by code-simplicity-reviewer.

## Resources

- maulepilot117/k8sCenter#163
- Related: todo 277 (parser fixture tests)
