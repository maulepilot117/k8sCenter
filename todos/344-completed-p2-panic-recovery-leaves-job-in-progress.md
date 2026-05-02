---
name: Panicking bulk job stays IN-PROGRESS until next-startup CompleteOrphans
status: completed
priority: p2
issue_id: 344
tags: [code-review, eso, phase-e, reliability, safe-auto]
dependencies: []
---

## Problem Statement

`BulkWorker.run` wraps each job in `defer recover()` (correctly catching panics so the goroutine survives). But the recover branch only logs — it does NOT call `w.store.Complete(ctx, msg.JobID)`.

**Failure mode:**
- Panicking job's row stays `completed_at = NULL` until the next process restart triggers `CompleteOrphans`.
- Polling dialog spins indefinitely (2s polls forever, no terminal state).
- `FindActive` blocks every subsequent same-scope POST with 409 `active_job_exists` until restart.

A single panic effectively jams that scope's bulk-refresh path for a process lifetime.

## Findings

- reliability reviewer (rel-3 medium, conf 0.88)
- safe_auto fix — small deterministic change

**Affected files:**
- `backend/internal/externalsecrets/bulk_worker.go:120-132`

## Proposed Solutions

### Option A — Complete + synthetic outcome on panic (recommended)

```go
func() {
    defer func() {
        if r := recover(); r != nil {
            w.logger.Error("eso bulk worker panic recovered",
                "jobId", msg.JobID, "panic", r)
            // Complete with background ctx so a cancelled parent doesn't block cleanup.
            cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            _ = w.store.AppendOutcome(cleanupCtx, msg.JobID, "", &store.BulkRefreshOutcome{
                UID: "", Reason: "worker_panic",
            }, nil)
            _ = w.store.Complete(cleanupCtx, msg.JobID)
        }
    }()
    w.processJob(ctx, msg)
}()
```

The synthetic `failed[]` entry with `Reason: "worker_panic"` makes the cause visible in audit Detail JSON; otherwise operators see a `completed_at`-set row with partial outcomes and no explanation.

### Option B — Complete only, no synthetic outcome

Less informative but minimal. The panic reason lives only in logs.

**Recommendation:** Option A. The audit trail value of an explicit panic marker outweighs the few extra lines.

## Acceptance Criteria

- [ ] `defer recover()` calls `Complete()` so the row terminates.
- [ ] AppendOutcome adds a `worker_panic` failed entry visible in audit Detail.
- [ ] Cleanup uses a fresh background ctx so a SIGTERM mid-panic doesn't block the cleanup itself.
- [ ] Test: synthetic panic in patchForceSync (via test seam) causes job to reach `completed_at` set, with `failed[].reason == "worker_panic"`.
- [ ] Subsequent same-scope POST returns 202 (not 409 active_job_exists) after the panicked job completes.
