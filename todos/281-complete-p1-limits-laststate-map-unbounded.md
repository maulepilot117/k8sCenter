---
status: pending
priority: p1
issue_id: "281"
tags: [code-review, limits, memory-leak, performance]
dependencies: []
---

# Limits Checker lastState Map Unbounded Growth

## Problem Statement

The `Checker` struct's `lastState` map grows without bound. When quotas or resources are deleted, their keys remain in the map indefinitely, causing memory leaks in long-running deployments.

## Findings

**Source:** Performance Oracle + Data Integrity Guardian agents

**Location:** `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/limits/checker.go` (lines 25-36, 124-128)

**Evidence:**
```go
type Checker struct {
    // ...
    lastState map[string]ThresholdStatus // key: "namespace:quotaName:resource"
}

func (c *Checker) dispatchIfChanged(...) {
    c.mu.Lock()
    c.lastState[key] = current  // Only ever adds, never removes
    c.mu.Unlock()
}
```

**Impact:**
- Over months of operation with frequent quota churn, this map can grow to thousands of stale entries
- Each entry is approximately 64-128 bytes (string key + status enum)
- Memory leak proportional to cluster activity

## Proposed Solutions

### Solution A: Prune After Each Check Cycle
After each check cycle, remove entries for quotas that no longer exist.

```go
func (c *Checker) check(ctx context.Context) {
    quotas, err := c.handler.Informers.ResourceQuotas().List(labels.Everything())
    // ... existing quota iteration ...
    
    // Prune stale entries
    currentKeys := make(map[string]struct{}, len(quotas)*8)
    for _, quota := range quotas {
        for resName := range utilization {
            currentKeys[stateKey(quota.Namespace, quota.Name, resName)] = struct{}{}
        }
    }
    
    c.mu.Lock()
    for key := range c.lastState {
        if _, exists := currentKeys[key]; !exists {
            delete(c.lastState, key)
        }
    }
    c.mu.Unlock()
}
```

**Pros:** Clean removal after each check, simple logic  
**Cons:** Extra iteration per check cycle  
**Effort:** Small  
**Risk:** Low

### Solution B: Rebuild Map Each Cycle
Create a fresh map on each check cycle instead of accumulating.

```go
func (c *Checker) check(ctx context.Context) {
    newState := make(map[string]ThresholdStatus)
    
    for _, quota := range quotas {
        for resName, util := range utilization {
            key := stateKey(...)
            current := computeStatus(util.Percentage, warn, critical)
            
            c.mu.RLock()
            previous := c.lastState[key]
            c.mu.RUnlock()
            
            if current != previous && current != ThresholdOK {
                c.dispatchNotification(...)
            }
            newState[key] = current
        }
    }
    
    c.mu.Lock()
    c.lastState = newState
    c.mu.Unlock()
}
```

**Pros:** Guaranteed bounded size, no stale entries possible  
**Cons:** More allocations per cycle (but map is short-lived)  
**Effort:** Small  
**Risk:** Low

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/limits/checker.go`

**Components:**
- Checker background goroutine

## Acceptance Criteria

- [ ] lastState map does not grow unboundedly
- [ ] Entries for deleted quotas are removed
- [ ] No memory leak observable over extended operation
- [ ] Test verifies map pruning behavior

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Both performance and data integrity agents flagged this |

## Resources

- PR #164 code review
