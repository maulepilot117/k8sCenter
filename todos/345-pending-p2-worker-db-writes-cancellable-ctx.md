---
name: Worker DB writes thread cancellable parent ctx — SIGTERM loses outcomes after PATCH already shipped
status: pending
priority: p2
issue_id: 345
tags: [code-review, eso, phase-e, reliability, safe-auto, audit-integrity]
dependencies: []
---

## Problem Statement

`BulkWorker.processJob` receives the application Start ctx and threads it directly to `AppendOutcome`, `Complete`, `Get`, and `auditBulkJob`. On SIGTERM the parent ctx cancels:

1. `patchForceSync` may have **already succeeded** (PATCH round-tripped to apiserver, controller observed it).
2. The follow-up `AppendOutcome` sees `context canceled` and silently fails (return value discarded with `_ =`).
3. Restart-orphan reaper closes the row, leaving `succeeded[]` missing the patches that DID happen in cluster.

**Operator-visible result:** sees `47/50` with 3 "unknown" while cluster state is actually `50/50`. Audit row claims the job completed with fewer successful patches than actually shipped — diverges from reality, defeats forensic accuracy.

## Findings

2 reviewers flagged this:
- reliability (rel-4 medium conf 0.85, rel-10 low conf 0.70 on swallowed errors)
- adversarial (adv-10 low conf 0.80)

**Affected files:**
- `backend/internal/externalsecrets/bulk_worker.go:138-219` (entire processJob loop)

## Proposed Solutions

### Option A — decouple DB write ctx from worker lifecycle ctx (recommended)

Use `context.WithoutCancel(ctx)` (Go 1.21+) — preserves request-scoped values but ignores cancellation:

```go
func (w *BulkWorker) processJob(ctx context.Context, msg BulkJobMessage) {
    // ...
    for _, target := range msg.Targets {
        // ctx-cancel still aborts the LOOP (don't start new patches after shutdown)
        select { case <-ctx.Done(): return; default: }

        uid, err := w.handler.patchForceSync(ctx, dynClient, target.Namespace, target.Name)

        // DB writes use a non-cancellable derived ctx with a bounded timeout
        // so a successful patch always gets recorded regardless of shutdown timing.
        dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
        // ... AppendOutcome with dbCtx, log errors instead of discarding
        cancel()
    }
}
```

### Option B — fall back to context.Background()

If we don't want Go 1.21 dependency: `context.WithTimeout(context.Background(), 5*time.Second)`. Loses request values (cluster_id from middleware) but those aren't read in the DB layer anyway.

### Option C — log every error return

Independent of A/B, replace `_ = w.store.AppendOutcome(...)` with explicit error logging:

```go
if err := w.store.AppendOutcome(...); err != nil {
    w.logger.Warn("append outcome failed",
        "jobId", msg.JobID, "uid", target.UID, "error", err)
}
```

11 sites in the worker currently discard errors silently — we lose any signal that writes are failing.

**Recommendation:** A + C together. The audit-honesty win is significant.

## Acceptance Criteria

- [ ] DB writes (`AppendOutcome`, `Complete`, `Get` for audit) use a ctx that survives parent cancellation.
- [ ] Each DB write has a 5-second timeout so a hung pgxpool can't pin the worker.
- [ ] All `_ = w.store.X(...)` calls log errors instead of swallowing.
- [ ] Test: synthetic SIGTERM after a successful patch records the success in `succeeded[]` and lands `completed_at` non-null.
- [ ] Test: pgxpool failure during AppendOutcome produces a log entry.
