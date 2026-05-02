---
name: Per-target patch has no retry — transient API errors become permanent failed[] entries
status: completed
priority: p2
issue_id: 343
tags: [code-review, eso, phase-e, reliability, design-decision]
dependencies: [338]
---

## Problem Statement

`patchForceSync` (used by both single force-sync and bulk worker) makes one Patch attempt. Any transient apiserver error — etcd leader election blip, 429 from APF, 503 during apiserver rolling restart — goes straight into `job.Failed` and is never retried.

A 5000-target bulk run hitting a transient blip therefore reports dozens of false failures the operator must re-trigger by hand.

cert-manager `HandleRenew` (`certmanager/handler.go:603`) uses `maxRenewRetries = 3` for the same operation class. Phase E diverged without justification.

The patch is **idempotent** (`force-sync: <now>` collapses to one ESO reconcile regardless of how many times it lands), so retries on transient classifications are safe.

Depends on #338 because we need real reason values from `classifyPatchError` before we can branch on transient vs permanent.

## Findings

- reliability reviewer (rel-2 high conf 0.85; rel-9 low conf 0.70 for single force-sync)
- learnings-researcher (notes cert-manager precedent)

**Affected files:**
- `backend/internal/externalsecrets/actions.go:217-249` — `patchForceSync`
- `backend/internal/externalsecrets/bulk_worker.go:162-190` — bulk worker call site

## Proposed Solutions

### Option A — small retry helper around patchForceSync (recommended)

```go
const maxPatchRetries = 3
const retryBackoff = 200 * time.Millisecond

func (h *Handler) patchForceSyncWithRetry(ctx context.Context, ...) (string, error) {
    var lastErr error
    for attempt := 0; attempt <= maxPatchRetries; attempt++ {
        uid, err := h.patchForceSync(ctx, ...)
        if err == nil {
            return uid, nil
        }
        if !isRetryable(err) {
            return uid, err
        }
        lastErr = err
        if attempt < maxPatchRetries {
            select {
            case <-ctx.Done(): return uid, ctx.Err()
            case <-time.After(retryBackoff * time.Duration(1<<attempt)):  // exponential
            }
        }
    }
    return "", lastErr
}

func isRetryable(err error) bool {
    return apierrors.IsTimeout(err) ||
        apierrors.IsServerTimeout(err) ||
        apierrors.IsTooManyRequests(err) ||
        apierrors.IsServiceUnavailable(err)
}
```

Crucially do NOT retry on `Forbidden` / `NotFound` / `Conflict` / `errAlreadyRefreshing` — those are deterministic outcomes.

### Option B — defer retries to the operator

Document the no-retry behavior; rely on the user re-triggering. Saves code but worsens UX during apiserver hiccups.

**Recommendation:** Option A. Apply uniformly to single force-sync and bulk worker so behavior matches cert-manager.

## Acceptance Criteria

- [ ] `patchForceSync` retries up to 3 times on `IsTimeout/IsServerTimeout/IsTooManyRequests/IsServiceUnavailable` with exponential backoff.
- [ ] Non-retryable errors (Forbidden, NotFound, Conflict, already_refreshing) return immediately.
- [ ] Test: simulated transient 503 succeeds on retry 2, recorded as `succeeded`.
- [ ] Test: persistent 403 doesn't retry, recorded as `rbac_denied`.
- [ ] interCallDelay (200ms) accounts for the worst-case retry window so a single slow target doesn't exceed expected wall-clock dramatically.
