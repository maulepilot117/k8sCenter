---
name: Worker queue-full path (Enqueue→503 + synthetic Complete) has no test coverage
status: pending
priority: p2
issue_id: 354
tags: [code-review, eso, phase-e, testing]
dependencies: []
---

## Problem Statement

`fakeWorker.full bool` exists in `bulk_test.go` (lines 1310-1318) — a test seam — but no test sets it `true`. The handler's queue-full branch in `bulk.go:1004-1012` therefore has zero coverage:

```go
if err := h.BulkWorker.Enqueue(...); err != nil {
    h.Logger.Error("enqueue bulk job", "error", err)
    _ = h.BulkJobStore.Complete(r.Context(), job.ID)  // SYNTHETIC CLEANUP
    httputil.WriteError(w, http.StatusServiceUnavailable, "worker queue full", "")
    return
}
```

This path is doubly important because:
1. It's the Insert-then-Enqueue ordering issue (#345 / rel-6 / adv-4): if `Complete` here also fails, the row stays IN-PROGRESS forever, blocking subsequent same-scope POSTs with 409 active_job_exists until restart.
2. The 503 response shape is unverified — does it carry the standard error envelope? Frontend has no test for it.

The fake's `f.full` read happens **before** `f.mu.Lock()`, which is also a race trap if tests evolve to mutate `full` concurrently with `Enqueue`.

## Findings

- testing reviewer (T-2 P1 conf 0.95; T-11 P3 conf 0.85 for the lock issue)
- adversarial reviewer (adv-4 medium conf 0.80 cascade)
- reliability reviewer (rel-6 low conf 0.70)

**Affected files:**
- `backend/internal/externalsecrets/bulk_test.go` — fakeWorker + new test
- `backend/internal/externalsecrets/bulk.go:1004-1012` — handler queue-full branch

## Proposed Solutions

### Option A — TestBulkRefresh_QueueFull (recommended)

```go
func TestBulkRefresh_QueueFull(t *testing.T) {
    // Setup: handler with worker.full = true
    // ...
    // POST refresh-all
    // Assert: 503 status
    // Assert: response body matches canonical error envelope
    // Assert: job row exists with completed_at set (synthetic Complete fired)
    // Assert: subsequent same-scope POST returns 202 (NOT 409 active_job_exists)
}
```

Also test the cascade case from adv-4: queue-full + Complete-fails → orphan IN-PROGRESS row → next POST returns 409 active_job_exists. This is the failure mode CompleteOrphans was designed to recover from; verify the test catches it.

### Option B — Fix the lock ordering in fakeWorker as well

Since you're touching the fake, move `if f.full` after `f.mu.Lock()` to avoid the race trap. Two-line change.

## Acceptance Criteria

- [ ] `TestBulkRefresh_QueueFull` exists and verifies 503 + synthetic Complete + clean retry path.
- [ ] Test for the cascade: when both Enqueue and Complete fail, the orphan row is detected by the next CompleteOrphans run (covered by #353's CompleteOrphans test).
- [ ] `fakeWorker.Enqueue` reads `f.full` under the mutex (race-safe).
- [ ] No regression in existing test count.
