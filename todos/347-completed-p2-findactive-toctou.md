---
name: FindActive TOCTOU — plain SELECT permits two active jobs for same scope
status: completed
priority: p2
issue_id: 347
tags: [code-review, eso, phase-e, concurrency, db-design]
dependencies: []
---

## Problem Statement

`handleBulkRefresh` checks `BulkJobStore.FindActive` for an existing in-flight job, then `Insert`s a new row if none exists. These are two separate SQL statements with no row lock between them.

`FindActive` (`eso_bulk_jobs.go:2205`) issues a plain `SELECT`, not `SELECT ... FOR UPDATE`. The migration's partial index `idx_eso_bulk_refresh_jobs_active (cluster_id, action, scope_target) WHERE completed_at IS NULL` is **non-UNIQUE** so the DB doesn't enforce one-job-per-scope either.

**Race:** two near-simultaneous POSTs both observe no active row, both Insert. The single worker processes them serially — every ES gets force-synced **twice** within ~30s.

For Vault / AWS Secrets Manager / Azure Key Vault providers with per-secret rate quotas, double fan-out can throttle real production workloads.

The plan §691 explicitly specified `SELECT ... FOR UPDATE`:
> "Concurrency limit: at most one bulk-refresh job in flight per (cluster_id, scope_target) — enforced by a SELECT ... FOR UPDATE against the jobs table."

Implementation diverged without justification.

## Findings

- adversarial reviewer (adv-2 medium conf 0.90)
- learnings-researcher (notes plan §691)

**Affected files:**
- `backend/internal/store/eso_bulk_jobs.go` — FindActive + Insert
- `backend/internal/store/migrations/000013_create_eso_bulk_refresh_jobs.up.sql` — index definition
- `backend/internal/externalsecrets/bulk.go:952-991` — handler call site

## Proposed Solutions

### Option A — UNIQUE partial index (recommended, simplest)

Migration follow-up:

```sql
DROP INDEX IF EXISTS idx_eso_bulk_refresh_jobs_active;
CREATE UNIQUE INDEX idx_eso_bulk_refresh_jobs_active_unique
  ON eso_bulk_refresh_jobs (cluster_id, action, scope_target)
  WHERE completed_at IS NULL;
```

Insert path becomes:

```go
err := s.pool.QueryRow(ctx, `INSERT INTO eso_bulk_refresh_jobs (...) VALUES (...)
    ON CONFLICT ON CONSTRAINT idx_eso_bulk_refresh_jobs_active_unique DO NOTHING
    RETURNING id`, ...).Scan(&id)
if errors.Is(err, pgx.ErrNoRows) {
    // duplicate active — race detected, return the existing job's id
    existing, _ := s.FindActive(ctx, clusterID, action, scopeTarget)
    return existing, errAlreadyActive
}
```

Database enforces the invariant; race window collapses to "first-write-wins."

### Option B — wrap FindActive + Insert in transaction with FOR UPDATE

Honor the plan literally:

```go
tx, _ := s.pool.Begin(ctx)
defer tx.Rollback(ctx)
// SELECT ... FOR UPDATE on a per-scope advisory lock OR on the row itself
// (tricky without an existing row — pg_advisory_xact_lock with hash of scope is a clean fit)
pg_advisory_xact_lock(hashtext(scopeTarget || action || clusterID))
// ... FindActive, then Insert if needed
tx.Commit(ctx)
```

Heavier; useful if the index approach has surprising downsides.

**Recommendation:** Option A. Schema-level enforcement is cheap and obvious.

## Acceptance Criteria

- [ ] Concurrent same-scope INSERTs cannot both succeed (DB-enforced).
- [ ] Race-loser receives the existing job's id (frontend `active_job_exists` recovery still works).
- [ ] Test: `go test ... -race -count=10` with parallel goroutines POSTing the same scope produces exactly one job row.
- [ ] Migration follow-up (e.g., `000014_unique_active_bulk_jobs.up.sql`) deployed cleanly against staging before merge.
- [ ] Plan §691 closes out as "shipped via UNIQUE partial index" — document the deviation from `SELECT FOR UPDATE`.
