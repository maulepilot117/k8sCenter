---
status: pending
priority: p2
issue_id: "290"
tags: [code-review, reliability, data-integrity, pr-166]
dependencies: []
---

# No Retry for Failed Database Inserts

## Problem Statement

When a PostgreSQL insert fails (transient error, connection reset, brief outage), the audit entry is logged as an error and permanently discarded. There is no retry mechanism.

**Why it matters:** Transient database errors are common (connection pool exhaustion, brief network blips). Each failure causes permanent audit data loss.

## Findings

### Data Integrity Guardian Analysis

**File:** `backend/internal/audit/sqlite_logger.go`
**Lines:** 56-60

```go
if err := l.store.Insert(ctx, e); err != nil {
    l.logger.Error("failed to persist audit entry", "error", err, "action", e.Action, "user", e.User)
}
// Entry is now gone - no retry queue, no dead letter queue
```

**Scenario:**
1. Database connection pool temporarily exhausted
2. Insert fails with timeout
3. Entry logged to error log but lost from PostgreSQL
4. Entry WAS written to slog, creating split-brain between log aggregator and database

## Proposed Solutions

### Option A: Exponential Backoff Retry (Recommended)

Add retry with exponential backoff for transient errors:

```go
func (l *PostgresLogger) insertWithRetry(ctx context.Context, e Entry) error {
    backoff := 100 * time.Millisecond
    for attempt := 0; attempt < 3; attempt++ {
        err := l.store.Insert(ctx, e)
        if err == nil {
            return nil
        }
        if !isRetryable(err) {
            return err
        }
        time.Sleep(backoff)
        backoff *= 2
    }
    return fmt.Errorf("insert failed after 3 attempts")
}

func isRetryable(err error) bool {
    // Connection errors, timeouts, etc.
    return errors.Is(err, context.DeadlineExceeded) ||
           strings.Contains(err.Error(), "connection")
}
```

**Pros:** Handles transient failures, simple implementation
**Cons:** Adds latency under failure conditions, could slow drain
**Effort:** Small
**Risk:** Low

### Option B: Re-queue Failed Entries

Put failed entries back in the channel:

```go
if err := l.store.Insert(ctx, e); err != nil {
    l.logger.Error("failed to persist audit entry", "error", err)
    select {
    case l.entryCh <- e:  // Re-queue
    default:
        l.logger.Error("re-queue failed, buffer full", "action", e.Action)
    }
}
```

**Pros:** Simple, reuses existing buffer
**Cons:** Could cause infinite loop on persistent errors, out-of-order
**Effort:** Trivial
**Risk:** High (infinite loop potential)

### Option C: Dead Letter Queue

Write failed entries to a separate file for later recovery:

```go
if err := l.store.Insert(ctx, e); err != nil {
    l.logger.Error("failed to persist audit entry", "error", err)
    l.writeDeadLetter(e)  // Append to file
}
```

**Pros:** Never loses data, can replay later
**Cons:** Requires file I/O, replay mechanism
**Effort:** Medium
**Risk:** Low

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/audit/sqlite_logger.go`

**Components:** Audit logging, database resilience

## Acceptance Criteria

- [ ] Transient database errors do not cause permanent audit entry loss
- [ ] Retry attempts are logged
- [ ] Persistent failures are still logged as errors
- [ ] No infinite retry loops

## Work Log

| Date | Author | Action | Notes |
|------|--------|--------|-------|
| 2026-04-11 | Code Review | Created | Found by data-integrity-guardian agent |

## Resources

- PR: https://github.com/maulepilot117/k8sCenter/pull/166
