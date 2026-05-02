---
name: apierrorReason is a no-op stub; every non-typed patch error becomes "patch_error:unknown"
status: completed
priority: p1
issue_id: 338
tags: [code-review, eso, phase-e, reliability, audit-honesty]
dependencies: []
---

## Problem Statement

`bulk_worker.go:apierrorReason` discards its type assertion result with `_ = se` and unconditionally returns the literal string `"unknown"`. The wrapping `classifyPatchError` therefore emits `patch_error:unknown` for every error not specifically handled by the apierrors helpers (`IsForbidden` / `IsNotFound` / `IsConflict` / `errAlreadyRefreshing`).

Operationally: when 1000 targets fail because the API server throttled (429), got a transient timeout, or returned 503 during a rolling restart, every UID in the bulk job's `failed[]` is recorded with the same opaque reason. Operators cannot distinguish transient (worth retrying) from permanent (won't recover) failures, blocking automated retry tooling and human triage.

This violates the L8.2 "audit log honesty" contract called out in the Phase E plan.

## Findings

5 reviewers flagged this independently:
- correctness (P2, conf 0.95)
- maintainability (medium, conf 0.95)
- reliability (P1, conf 0.92)
- adversarial (low, conf 1.00)
- testing (P3, conf 0.95) — "no test pins classifyPatchError reason for a real *apierrors.StatusError"

**Affected files:**
- `backend/internal/externalsecrets/bulk_worker.go:224-238` (apierrorReason + classifyPatchError)

**Reads like a TODO that shipped as final code.** The function captures `se` then immediately discards it, signalling unfinished work.

## Proposed Solutions

### Option A — use apierrors.ReasonForError (recommended)

```go
func classifyPatchError(err error) string {
    if err == nil {
        return ""
    }
    reason := apierrors.ReasonForError(err)
    if reason == metav1.StatusReasonUnknown {
        return "patch_error"
    }
    return fmt.Sprintf("patch_error:%s", strings.ToLower(string(reason)))
}
```

Then delete `apierrorReason` entirely.

Optionally branch additionally on `apierrors.IsTimeout` / `IsServerTimeout` / `IsTooManyRequests` / `IsServiceUnavailable` to surface a `transient_*` prefix, which informs the retry decision in #340 (Phase E retry policy).

### Option B — delete the indirection

If we don't want a typed reason in audit, just inline `"patch_error"` and drop both helpers. Less informative but honest.

## Acceptance Criteria

- [ ] `classifyPatchError` returns a meaningful k8s reason for `*apierrors.StatusError` inputs.
- [ ] `apierrorReason` is either implemented correctly or deleted.
- [ ] Test asserts a real timeout/throttle error produces a non-`unknown` reason.
- [ ] Audit row Detail JSON for a transient-error scenario carries the real reason.
