---
status: pending
priority: p3
issue_id: "302"
tags: [code-review, scanning, performance, pr-167]
dependencies: []
---

# Reduce vulnerability detail endpoint timeout from 30s to 10s

## Problem Statement

`HandleVulnerabilityDetail` uses a 30s context timeout. For a label-selector List against a single namespace, an apiserver taking >5s is already broken. 30s means a degraded apiserver ties up handler goroutines for 30s × concurrent requests, amplifying a partial outage into a full one.

The neighboring `diagnostics` package uses 15s. No rationale for 30s.

**Why it matters:** Failure-mode resilience; matches or improves on neighboring endpoints.

## Findings

### Performance Oracle + Pattern Recognition Reviewers

**File:** `backend/internal/scanning/handler.go:302`

```go
ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
```

**Compare:** `backend/internal/diagnostics/handler.go:66` uses 15s.

## Proposed Solutions

### Option A: Drop to 10s (Recommended)

```go
ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
```

**Pros:** Faster fail-fast; matches expectations for healthy apiserver; limits goroutine hold time
**Cons:** None expected; can be raised if CI/homelab sees timeouts
**Effort:** Trivial
**Risk:** Low

### Option B: Match diagnostics at 15s

**Pros:** Consistency with neighbor
**Cons:** Still generous
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/scanning/handler.go:302`

## Acceptance Criteria

- [ ] Timeout reduced to 10s (or 15s)
- [ ] Existing tests still pass

## Work Log

## Resources

- PR #167
