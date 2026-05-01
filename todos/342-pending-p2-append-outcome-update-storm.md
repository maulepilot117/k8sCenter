---
name: AppendOutcome issues 5000 UPDATEs per max-size job; JSONB string concat causes O(n²) rewrites
status: pending
priority: p2
issue_id: 342
tags: [code-review, eso, phase-e, performance, db-pattern]
dependencies: []
---

## Problem Statement

`ESOBulkJobStore.AppendOutcome` runs one UPDATE per target inside the worker's per-target loop:

- `succeeded`: `array_append(succeeded, $2)` — TEXT[] append
- `failed` / `skipped`: `failed = failed || $2::jsonb` — JSONB concat with string-concatenated `[<entry>]` wrapper

At `maxBulkTargets = 5000` that's 5000 sequential UPDATEs against the same row. Issues:

1. **Tuple bloat**: every UPDATE creates a new heap tuple (MVCC); the row may be HOT-updated but at this scale autovacuum has to keep up. A 5000-target job creates ~5000 dead tuples for one row.

2. **JSONB write amplification**: `failed = failed || $2::jsonb` rewrites the entire JSONB column each call. Cumulative cost across 5000 entries is O(n²) — worst case ~12.5M units of toast write churn when failures pile up.

3. **String concatenation pattern is brittle**: `"[" + string(entry) + "]"` works because `json.Marshal` output is well-formed and inputs are server-controlled, but bypasses pgx typed parameters and is fragile to future schema additions.

## Findings

3 reviewers flagged this:
- correctness (P3, conf 0.70 — string concat brittleness)
- performance (perf-1 P2 conf 0.85, perf-2 P3 conf 0.70)
- data-migrations (low, conf 0.65, append-outcome-jsonb-string-concat)

**Affected files:**
- `backend/internal/store/eso_bulk_jobs.go:135-152` (AppendOutcome)
- `backend/internal/externalsecrets/bulk_worker.go:162-201` (per-target call site)

## Proposed Solutions

### Option A — batch outcomes in the worker (recommended for perf)

Accumulate outcomes in worker memory; flush every N targets (50) or every 1-2 seconds. The dialog polls at 2s, so a 1-2s flush keeps real-time progress visibility.

```go
type pendingOutcomes struct {
    succeeded []string
    failed    []store.BulkRefreshOutcome
    skipped   []store.BulkRefreshOutcome
}

func (w *BulkWorker) flush(ctx context.Context, jobID uuid.UUID, p *pendingOutcomes) {
    // Single UPDATE that appends all three slices at once
    if len(p.succeeded)+len(p.failed)+len(p.skipped) == 0 { return }
    // ... one Exec with array_cat for succeeded and jsonb concat for failed/skipped
}
```

Reduces 5000 round-trips to ~100 (every 50 targets).

### Option B — typed JSONB without string wrap (recommended regardless)

Replace string concatenation with `jsonb_build_array`:

```go
_, err := s.pool.Exec(ctx, `
    UPDATE eso_bulk_refresh_jobs
    SET failed = failed || jsonb_build_array($2::jsonb)
    WHERE id = $1`, id, entry)  // entry is the json.Marshal'd object directly
```

Same for `skipped`. Cleaner and pgx handles types properly.

### Option C — child outcomes table (deferred unless profiling demands it)

Split into `eso_bulk_refresh_job_outcomes(job_id, kind, uid, reason)`. Append-only INSERT, no rewrites, supports paging large `failed[]` arrays in the UI. Bigger refactor; track as follow-up if Option A doesn't move the needle.

**Recommendation:** Ship Option A + Option B together. Option C as a future-watch item.

## Acceptance Criteria

- [ ] Worker batches outcomes; per-job UPDATE count reduced to O(targetCount/50) under steady state.
- [ ] JSONB writes use `jsonb_build_array` instead of string-wrapping.
- [ ] Bench (manual or `go test -bench` against a Postgres test container) shows 5000-target job's DB write time reduced by 5-10x.
- [ ] Frontend polling still surfaces progress with at most 2s lag.
- [ ] No regression in `TestBulkRefresh_*` tests.
