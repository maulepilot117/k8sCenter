---
status: pending
priority: p2
issue_id: "286"
tags: [code-review, limits, lifecycle, shutdown]
dependencies: []
---

# Checker Missing Graceful Shutdown Wait

## Problem Statement

The `limitsChecker` is started but `Stop()` is never called during server shutdown. While context cancellation will stop it, the explicit `Stop()` with `wg.Wait()` ensures all in-flight checks complete before the process exits.

## Findings

**Source:** Architecture Strategist agent

**Location:** `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/cmd/kubecenter/main.go` (lines 652-654)

**Evidence:**
```go
limitsChecker := limits.NewChecker(limitsHandler, notifService, limits.DefaultCheckInterval, logger)
limitsChecker.Start(ctx)
// Missing: defer limitsChecker.Stop() or shutdown hook
```

**Impact:**
- In-flight notification dispatch may be interrupted during shutdown
- Potential for inconsistent state if check is mid-way when process exits

## Proposed Solutions

### Solution: Add Stop() to Shutdown Sequence

```go
// In main.go shutdown section
if limitsChecker != nil {
    limitsChecker.Stop()
}
```

**Pros:** Clean shutdown, consistent with other background goroutines  
**Cons:** None  
**Effort:** Small  
**Risk:** Low

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/cmd/kubecenter/main.go`

## Acceptance Criteria

- [ ] limitsChecker.Stop() called during server shutdown
- [ ] In-flight checks complete before process exits
- [ ] No panic or error on graceful shutdown

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Architecture strategist flagged |

## Resources

- PR #164 code review
