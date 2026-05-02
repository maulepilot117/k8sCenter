---
name: CompleteOrphans only tested via in-memory fake — real SQL has no integration coverage
status: deferred
defer_reason: "Repo lacks Postgres integration-test harness (testcontainers / dockertest). Adding that scaffolding is out of scope for Phase E follow-up. SQL semantic regressions surface in the E2E suite which exercises bulk-refresh against real Postgres via Helm. Track as future follow-up under test-infrastructure work."
priority: p2
issue_id: 353
tags: [code-review, eso, phase-e, testing, integration]
dependencies: []
---

## Problem Statement

`TestBulkRefresh_CompleteOrphans` exercises the in-memory `fakeBulkJobStore.CompleteOrphans` (a 4-line loop). The real implementation in `eso_bulk_jobs.go`:

```go
UPDATE eso_bulk_refresh_jobs SET completed_at = NOW() WHERE completed_at IS NULL
```

has zero test coverage.

**Plan scenario #8** ("Job survives backend restart") explicitly requires the SQL to actually work on a real DB. The fake test can't catch:
- Missing index issues
- pgxpool connection handling under load
- Transaction-isolation surprises (e.g., concurrent worker writes during reaping)
- RowsAffected reporting

Same gap applies to `FindActive` (plain SELECT against the partial index — index actually used?) and `AppendOutcome` (3 branches: succeededUID / failed / skipped — JSONB string concat actually produces valid JSON?).

## Findings

- testing reviewer (T-3 P1 conf 0.88)
- data-migrations (testing gaps)
- deployment-verification (smoke step)

**Affected files:**
- `backend/internal/store/eso_bulk_jobs.go` (real impl)
- `backend/internal/store/eso_bulk_jobs_test.go` (does not yet exist)

## Proposed Solutions

### Option A — pgxtest-based integration tests (recommended)

The repo's existing pattern (look at `audit/store_test.go` and similar) likely uses a Postgres test container or testcontainers-go. Add a sibling `eso_bulk_jobs_test.go`:

- `TestESOBulkJobStore_CompleteOrphans_DeletesOnlyInProgress` — seed 2 rows, one with `completed_at` set; assert only the IN-PROGRESS one flips.
- `TestESOBulkJobStore_FindActive_UsesPartialIndex` — seed mixed rows; assert query plan uses the index (or just assert correct return).
- `TestESOBulkJobStore_AppendOutcome_AllBranches` — succeededUID / failed JSONB / skipped JSONB; verify the resulting row's columns deserialize cleanly.
- `TestESOBulkJobStore_Insert_Idempotent` — inserting same UUID twice errors (PK constraint).
- `TestESOBulkJobStore_Cleanup_RespectsRetention` — required by #341.

### Option B — sqlmock-based unit tests

Lighter dependencies; runs in any CI. Doesn't catch SQL syntax errors that postgres-only types like `JSONB` produce.

**Recommendation:** Option A. Phase E touches PG-specific features (JSONB, partial index, array_append) that need real PG to validate.

## Acceptance Criteria

- [ ] `eso_bulk_jobs_test.go` exists with at least 5 integration tests covering all CRUD methods.
- [ ] Tests run against the same PG fixture other store tests use (or a dedicated test container).
- [ ] Tests catch the SQL syntax/semantic regression: e.g., remove the `array_append` and ensure a test fails.
- [ ] CI run includes the new tests; total backend test runtime increase < 30s.
