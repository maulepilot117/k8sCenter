---
status: complete
priority: p1
issue_id: "288"
tags: [code-review, bug, concurrency, pr-166]
dependencies: []
---

# Goroutine Leak on Shutdown Timeout

## Problem Statement

When the audit logger shutdown times out (after 5 seconds), `Close()` returns but the `asyncWriter` goroutine continues running. This is a goroutine leak that can cause undefined behavior and resource exhaustion.

**Why it matters:** The caller assumes shutdown is complete when `Close()` returns, but the writer goroutine may still be processing entries or holding database connections.

## Findings

### Code Simplicity Reviewer Analysis

**File:** `backend/internal/audit/sqlite_logger.go`
**Lines:** 101-118

```go
func (l *PostgresLogger) Close() {
    l.stopOnce.Do(func() {
        close(l.stopCh)
        
        done := make(chan struct{})
        go func() {
            l.wg.Wait()
            close(done)
        }()

        select {
        case <-done:
            l.logger.Info("audit logger shutdown complete")
        case <-time.After(flushTimeout):  // Returns here on timeout
            l.logger.Warn("audit logger shutdown timed out, some entries may be lost")
        }
        // BUT: wg.Wait() goroutine is still running!
        // AND: asyncWriter may still be processing!
    })
}
```

When timeout fires:
1. `Close()` returns to caller
2. `wg.Wait()` goroutine continues waiting
3. `asyncWriter` may still be inserting to database
4. Database connections may be held after app thinks it's shutdown

## Proposed Solutions

### Option A: Use Channel-Close Pattern (Recommended)

Replace `stopCh` with closing `entryCh` directly. This is simpler and uses Go's built-in channel semantics:

```go
func (l *PostgresLogger) asyncWriter() {
    defer l.wg.Done()
    for e := range l.entryCh {  // Exits when channel closed AND drained
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        if err := l.store.Insert(ctx, e); err != nil {
            l.logger.Error("failed to persist audit entry", ...)
        }
        cancel()
    }
}

func (l *PostgresLogger) Close() {
    l.stopOnce.Do(func() {
        close(l.entryCh)  // Signal no more writes
        l.wg.Wait()       // Wait for drain (blocking)
        l.logger.Info("audit logger shutdown complete")
    })
}
```

**Pros:** Simpler code, eliminates `stopCh`, uses idiomatic Go patterns, no goroutine leak
**Cons:** Callers must not call `Log()` after `Close()` (panic on closed channel)
**Effort:** Small (15-20 LOC change)
**Risk:** Low - simpler is better

### Option B: Add Context Cancellation to Writer

Pass a context to `asyncWriter` that gets cancelled on shutdown:

```go
func (l *PostgresLogger) asyncWriter(ctx context.Context) {
    defer l.wg.Done()
    for {
        select {
        case e := <-l.entryCh:
            // ... insert with timeout
        case <-ctx.Done():
            // Drain remaining and exit
            for len(l.entryCh) > 0 {
                e := <-l.entryCh
                // ... insert
            }
            return
        }
    }
}
```

**Pros:** Consistent with other async patterns in codebase
**Cons:** More complex, still needs drain logic
**Effort:** Medium
**Risk:** Medium

### Option C: Blocking Shutdown (No Timeout)

Simply wait for `wg.Wait()` without timeout:

```go
func (l *PostgresLogger) Close() {
    l.stopOnce.Do(func() {
        close(l.stopCh)
        l.wg.Wait()  // Block until done
        l.logger.Info("audit logger shutdown complete")
    })
}
```

**Pros:** Simple, no leak, guarantees all entries processed
**Cons:** Could hang if database is unreachable (but individual inserts have 5s timeout, so worst case is 1000*5s = ~83 minutes)
**Effort:** Trivial (remove timeout code)
**Risk:** Low

## Recommended Action

<!-- Filled during triage -->

## Technical Details

**Affected files:**
- `backend/internal/audit/sqlite_logger.go`

**Components:** Audit logging, shutdown sequence

## Acceptance Criteria

- [ ] `Close()` does not return until `asyncWriter` goroutine has exited
- [ ] No goroutine leak detectable in tests
- [ ] All pending audit entries are either persisted or logged as lost
- [ ] Unit test verifies shutdown completes cleanly

## Work Log

| Date | Author | Action | Notes |
|------|--------|--------|-------|
| 2026-04-11 | Code Review | Created | Found by code-simplicity-reviewer agent |

## Resources

- PR: https://github.com/maulepilot117/k8sCenter/pull/166
- Related: Shutdown sequence in `backend/cmd/kubecenter/main.go:745-748`
