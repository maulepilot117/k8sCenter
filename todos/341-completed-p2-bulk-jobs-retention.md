---
name: eso_bulk_refresh_jobs has no retention sweep — completed rows accumulate forever
status: completed
priority: p2
issue_id: 341
tags: [code-review, eso, phase-e, retention, dos-mitigation]
dependencies: []
---

## Problem Statement

Migration 000013 creates `eso_bulk_refresh_jobs` with no retention policy. Completed rows live forever — `CompleteOrphans` only flips `completed_at` on startup; rows are never DELETEd.

eso_sync_history (Phase C migration 000011) has 90-day retention via `RunRetention` on a 1h ticker. Phase E should match.

**Storage profile per row:**
- `target_uids TEXT[]` up to 5000 entries (~180KB after TOAST)
- `failed JSONB` and `skipped JSONB` arrays growing per-target

**Realistic load:** an admin running daily cluster-wide bulk refreshes drives unbounded growth. With queueDepth=32 cycling at ~30s/job + Phase E's 5000-target cap, the upper bound is huge.

Combined with #339 (X-Cluster-ID lets cluster_id be polluted), an admin can fill the table from many cluster IDs.

## Findings

4 reviewers flagged this:
- security (low, conf 0.70, sec-eso-phase-e-4)
- data-migrations (low, conf 0.72, no-retention-on-bulk-jobs)
- deployment-verification (caveat in monitoring section)
- learnings-researcher (recommendation)

**Affected files:**
- `backend/internal/store/eso_bulk_jobs.go` — needs `Cleanup` method
- `backend/cmd/kubecenter/main.go` — needs to wire retention goroutine

## Proposed Solutions

### Option A — mirror ESOHistoryStore.Cleanup pattern (recommended)

1. Add to `eso_bulk_jobs.go`:

```go
func (s *ESOBulkJobStore) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
    if retentionDays < 1 {
        return 0, fmt.Errorf("retention days must be at least 1, got %d", retentionDays)
    }
    cleanupCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
    defer cancel()
    tag, err := s.pool.Exec(cleanupCtx, `
        DELETE FROM eso_bulk_refresh_jobs
        WHERE completed_at IS NOT NULL
          AND completed_at < NOW() - $1 * INTERVAL '1 day'`,
        retentionDays)
    if err != nil {
        return 0, fmt.Errorf("cleanup eso_bulk_refresh_jobs: %w", err)
    }
    return tag.RowsAffected(), nil
}
```

2. In `main.go`, alongside the existing `esoPoller.RunRetention(ctx)` ticker, add a sibling goroutine that calls `esoBulkStore.Cleanup(ctx, 30)` every hour (30-day retention is plenty — bulk jobs are far less interesting historically than per-ES sync history).

3. Wrap with the same `defer recover()` + 5-min timeout as ESOHistoryStore.Cleanup.

### Option B — coalesce into the existing retention goroutine

Add Cleanup() to the same goroutine that calls `esoPoller.RunRetention`. Slightly less code; tightly couples two unrelated retention windows.

## Acceptance Criteria

- [ ] `ESOBulkJobStore.Cleanup(ctx, retentionDays)` deletes rows where `completed_at < NOW() - retentionDays`.
- [ ] Retention runs hourly with panic recovery + bounded ctx.
- [ ] Retention period is 30 days for bulk-refresh jobs (or document why a different window).
- [ ] Test asserts Cleanup is a no-op when no rows exceed retention.
- [ ] Test asserts Cleanup deletes a synthetic 31-day-old completed row.
- [ ] CLAUDE.md (Phase 14 entry) documents the retention window.
