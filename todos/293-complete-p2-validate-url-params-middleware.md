---
status: pending
priority: p2
issue_id: "293"
tags: [code-review, scanning, validation, pr-167]
dependencies: []
---

# Add ValidateURLParams middleware and tighten validWorkloadName

## Problem Statement

The new detail route `/scanning/vulnerabilities/{namespace}/{kind}/{name}` doesn't use the shared `resources.ValidateURLParams` middleware that neighboring routes (limits, velero, topology, diagnostics, policy) all apply. The handler does its own regex validation, duplicating the middleware's job.

Worse: `validWorkloadName` allows up to 253 chars (DNS-1123 subdomain), but Kubernetes **label values** are capped at 63 characters. A 64-253 char name passes validation, then the label selector at `trivy.go:202` gets rejected by the API server with a validation error that gets wrapped into a generic 500.

**Why it matters:** Bad UX (generic 500 instead of 400), duplication of validation logic, inconsistency with rest of codebase.

## Findings

### Architecture Strategist

**File:** `backend/internal/server/routes.go:521`

```go
sr.Get("/vulnerabilities/{namespace}/{kind}/{name}", h.HandleVulnerabilityDetail)
```

Missing `.With(resources.ValidateURLParams)` chain that neighbors use:
- `limits` routes.go:535: `lr.With(resources.ValidateURLParams).Get(...)`
- `velero` routes.go:551
- `topology` routes.go:404
- `diagnostics` routes.go:435
- `policy` routes.go:449

### Security Sentinel

**File:** `backend/internal/scanning/handler.go:259`

```go
var validWorkloadName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)
```

Allows up to 253 chars — k8s label values max at 63. Name `a` × 100 passes regex, then API server rejects the label selector.

## Proposed Solutions

### Option A: Use shared middleware + remove handler regex (Recommended)

```go
// routes.go
sr.With(resources.ValidateURLParams).Get("/vulnerabilities/{namespace}/{kind}/{name}", h.HandleVulnerabilityDetail)
```

Then delete `validWorkloadName` from `handler.go`. Keep `validWorkloadKind` since it isn't covered by the shared middleware (or add an allowlist map for kinds).

**Pros:** Matches project convention; single source of truth for name validation; automatically handles future changes
**Cons:** Need to verify `ValidateURLParams` actually validates `{name}` URL param (check implementation at `backend/internal/k8s/resources/handler.go`)
**Effort:** Small
**Risk:** Low

### Option B: Keep handler regex but tighten to 63 chars

```go
var validWorkloadName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,61}[a-z0-9])?$`)
```

**Pros:** Minimal change
**Cons:** Still duplicates validation logic, doesn't match project convention
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/server/routes.go:521`
- `backend/internal/scanning/handler.go:256-259`
- Compare: `backend/internal/k8s/resources/handler.go` (ValidateURLParams implementation)

## Acceptance Criteria

- [ ] Route uses `resources.ValidateURLParams` middleware (if it validates `{name}`)
- [ ] Invalid names > 63 chars return 400 with clear message (not 500)
- [ ] No duplicate validation in handler

## Work Log

<!-- Dated record -->

## Resources

- PR #167
