---
status: complete
priority: p2
issue_id: 277
tags: [testing, policy, code-review, pr-163]
dependencies: []
---

## Problem Statement

The Kyverno parser has now broken silently twice against modern Kyverno schemas (initial ship in Phase 8B, then the bugs caught in PR #163). There is no unit-test fixture corpus for the parser — `go test ./internal/policy/...` reports `no test files`. Every upstream Kyverno minor release is a latent outage with no CI signal.

## Findings

- `backend/internal/policy/` has zero `_test.go` files.
- PR #163 fixes: `status.conditions[type=Ready]`, `match.any/all` as slice, PolicyReport `scope` vs per-result `resources[]`, `{seconds, nanos}` timestamp, Kyverno `ViolationCount` aggregation. None of these have regression tests.

## Proposed Solutions

### Option A — Table-driven tests with embedded YAML fixtures
Create `backend/internal/policy/kyverno_test.go` with fixtures from real cluster data:
- A modern Kyverno 1.11+ `ClusterPolicy` (with conditions, `match.any`, title annotation).
- A legacy flat `match.resources.kinds` ClusterPolicy.
- A modern `PolicyReport` with top-level `scope` and `{seconds,nanos}` timestamps.
- A legacy `ClusterPolicyReport` with per-result `resources[]`.
Assert `NormalizeKyvernoPolicy` and `extractKyvernoViolations` produce the expected `NormalizedPolicy`/`NormalizedViolation`.

Fixture source: dump from homelab with `kubectl get clusterpolicy -o yaml` etc.

- Pros: Directly prevents the regressions this PR fixed. Cheap.
- Cons: Needs to be maintained as Kyverno releases new versions (feature, not bug — that's the point).
- Effort: Small–Medium.
- Risk: None.

### Option B — Generative tests with unstructured maps
Build `unstructured.Unstructured` programmatically instead of YAML fixtures.
- Pros: Tighter control, no YAML parsing in tests.
- Cons: Diverges from real cluster shapes; defeats the purpose (the bugs we fixed were about real-world shapes differing from assumed shapes).
- Effort: Small.
- Risk: Tests pass but real cluster still breaks.

## Recommended Action

## Technical Details

**Affected files:**
- New: `backend/internal/policy/kyverno_test.go`
- New: `backend/internal/policy/testdata/kyverno/*.yaml` (fixtures)

**Minimum test cases:**
1. ClusterPolicy with conditions.Ready=True → `Ready: true`
2. ClusterPolicy with conditions.Ready=False → `Ready: false`
3. ClusterPolicy with no conditions but legacy `status.ready=true` → `Ready: true`
4. ClusterPolicy with `match.any[].resources.kinds` → `TargetKinds` populated, deduped
5. PolicyReport with top-level `scope` → violations carry that scope's kind/name/namespace
6. PolicyReport with `{seconds,nanos}` timestamp → RFC3339 output
7. ClusterPolicyReport with per-result `resources[]` → fallback path

## Acceptance Criteria

- [ ] `go test ./internal/policy/...` runs and covers NormalizeKyvernoPolicy + extractKyvernoViolations.
- [ ] Each bug fix in PR #163 has a corresponding regression test.
- [ ] Fixtures are real-shaped (captured from a cluster or Kyverno docs), not synthesized.

## Work Log

_2026-04-10_: Discovered during `/review` on PR #163 by architecture-strategist. Second silent regression on the same surface is the motivator.

## Resources

- maulepilot117/k8sCenter#163
- `backend/internal/policy/kyverno.go`
