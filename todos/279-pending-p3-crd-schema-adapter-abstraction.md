---
status: pending
priority: p3
issue_id: 279
tags: [architecture, debt, crd, code-review, pr-163]
dependencies: []
---

## Problem Statement

Every CRD integration (`policy/kyverno.go`, `policy/gatekeeper.go`, `gitops/argocd.go`, `gitops/flux.go`, Trivy, Kubescape) parses unstructured CRD data against an implicit schema snapshot hard-coded at the time of writing. When upstream renames or restructures a field, the parser silently returns empty/wrong values — as happened twice now with Kyverno.

## Findings

- No version negotiation: parsers assume one shape, with ad-hoc inline fallbacks added after bugs are discovered (PR #163 is the pattern).
- No CI signal when upstream schemas drift.
- `internal/k8s/crd_discovery.go` *does* enumerate CRD versions — that information is not currently plumbed into the parsers.

## Proposed Solutions

### Option A — Versioned adapter pattern per CRD
For each integration, define:
```go
type KyvernoAdapter interface {
    NormalizePolicy(*unstructured.Unstructured) NormalizedPolicy
    ExtractViolations(*unstructured.Unstructured) []NormalizedViolation
}
```
with per-major-version implementations selected based on what `CRDDiscovery` reports. Unsupported versions log a clear warning.
- Pros: Explicit versioning, clear support matrix, easy to add a new upstream release.
- Cons: More files, more tests.
- Effort: Large (across all integrations).
- Risk: Medium — refactor touches many files.

### Option B — Fixture corpus in CI + accept inline fallbacks
Keep the current single-parser-with-fallbacks approach but add a CI corpus of real CRD dumps per supported upstream version. Regression tests run every PR.
- Pros: Cheap, minimal code churn.
- Cons: Doesn't prevent silent support drop for newer upstream versions — just catches regressions on known versions.
- Effort: Medium.
- Risk: Low.

## Recommended Action

## Technical Details

**Scope:** `backend/internal/policy/`, `backend/internal/gitops/`, `backend/internal/k8s/crd_discovery.go` (reference).

**Supported upstream matrix to declare:**
- Kyverno: versions currently tested
- Gatekeeper: versions currently tested
- Argo CD: versions currently tested
- Flux CD: versions currently tested

## Acceptance Criteria

- [ ] Each integration declares its supported upstream versions in code.
- [ ] CI runs fixture-based tests for each supported version.
- [ ] Unsupported versions produce a clear warning at discovery time.

## Work Log

_2026-04-10_: Identified during `/review` on PR #163 as a systemic risk across CRD integrations.

## Resources

- maulepilot117/k8sCenter#163
- `backend/internal/k8s/crd_discovery.go`
