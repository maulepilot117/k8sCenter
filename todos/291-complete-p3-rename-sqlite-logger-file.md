---
status: pending
priority: p3
issue_id: "291"
tags: [code-review, naming, cleanup, pr-166]
dependencies: []
---

# Rename sqlite_logger.go to postgres_logger.go

## Problem Statement

The file `backend/internal/audit/sqlite_logger.go` contains `PostgresLogger` type and PostgreSQL-specific code. The filename is a misleading artifact from a previous refactor.

**Why it matters:** Confusing for maintainers who expect SQLite code based on filename.

## Findings

### Code Simplicity Reviewer Analysis

**File:** `backend/internal/audit/sqlite_logger.go`

```go
// PostgresLogger implements the Logger interface with persistent PostgreSQL storage.
type PostgresLogger struct { ... }
```

The file contains:
- `PostgresLogger` struct
- PostgreSQL-specific persistence
- No SQLite code whatsoever

## Proposed Solutions

### Option A: Rename File (Recommended)

```bash
git mv backend/internal/audit/sqlite_logger.go backend/internal/audit/postgres_logger.go
```

**Pros:** Correct naming, no code changes
**Cons:** Git rename tracking
**Effort:** Trivial
**Risk:** None

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/audit/sqlite_logger.go` -> `backend/internal/audit/postgres_logger.go`

## Acceptance Criteria

- [ ] File renamed to postgres_logger.go
- [ ] All imports still work (Go uses package name, not filename)
- [ ] Tests still pass

## Work Log

| Date | Author | Action | Notes |
|------|--------|--------|-------|
| 2026-04-11 | Code Review | Created | Found by code-simplicity-reviewer, pattern-recognition-specialist agents |

## Resources

- PR: https://github.com/maulepilot117/k8sCenter/pull/166
