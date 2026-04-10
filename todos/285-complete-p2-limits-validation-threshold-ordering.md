---
status: pending
priority: p2
issue_id: "285"
tags: [code-review, limits, wizard, validation]
dependencies: []
---

# Missing Validation: Threshold and LimitRange Ordering

## Problem Statement

The wizard validation allows semantically invalid configurations:
1. `WarnThreshold > CriticalThreshold` (e.g., warn=95%, critical=80%)
2. LimitRange values without proper ordering constraints

## Findings

**Source:** Data Integrity Guardian + Security Sentinel agents

**Location:** `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/wizard/namespace_limits.go` (lines 110-127)

**Evidence - Threshold Ordering:**
```go
if n.Quota.WarnThreshold > 0 && (n.Quota.WarnThreshold < 1 || n.Quota.WarnThreshold > 100) {
    errs = append(errs, FieldError{Field: "quota.warnThreshold", Message: "must be between 1 and 100"})
}
// No check that WarnThreshold < CriticalThreshold
```

**Evidence - LimitRange Ordering:**
```go
// Validates individual quantities but not relationships:
// - ContainerMin should be <= ContainerDefault <= ContainerMax
// - ContainerDefaultRequest should be <= ContainerDefault
// - ContainerDefaultRequest should be >= ContainerMin
```

**Impact:**
- Users can create configurations that Kubernetes will reject
- Confusing errors at apply time instead of preview time

## Proposed Solutions

### Solution: Add Cross-Field Validation

```go
// Threshold ordering
if n.Quota.WarnThreshold > 0 && n.Quota.CriticalThreshold > 0 {
    if n.Quota.WarnThreshold >= n.Quota.CriticalThreshold {
        errs = append(errs, FieldError{
            Field:   "quota.warnThreshold",
            Message: "must be less than critical threshold",
        })
    }
}

// LimitRange ordering (for CPU and Memory)
for _, res := range []string{"cpu", "memory"} {
    min := parseOrZero(n.Limits.ContainerMin, res)
    max := parseOrZero(n.Limits.ContainerMax, res)
    def := parseOrZero(n.Limits.ContainerDefault, res)
    req := parseOrZero(n.Limits.ContainerDefaultRequest, res)
    
    if min.Cmp(max) > 0 {
        errs = append(errs, FieldError{
            Field:   fmt.Sprintf("limits.containerMin.%s", res),
            Message: "must be less than or equal to max",
        })
    }
    if def.Cmp(max) > 0 {
        errs = append(errs, FieldError{
            Field:   fmt.Sprintf("limits.containerDefault.%s", res),
            Message: "must be less than or equal to max",
        })
    }
    // ... similar for other relationships
}
```

**Pros:** Catches errors early, better UX  
**Cons:** More validation code  
**Effort:** Medium  
**Risk:** Low

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/wizard/namespace_limits.go`
- `backend/internal/wizard/namespace_limits_test.go`

**Required Relationships:**
1. WarnThreshold < CriticalThreshold
2. ContainerMin <= ContainerMax (for CPU and Memory)
3. ContainerMin <= ContainerDefault <= ContainerMax
4. ContainerMin <= ContainerDefaultRequest <= ContainerDefault

## Acceptance Criteria

- [ ] Validation fails for WarnThreshold >= CriticalThreshold
- [ ] Validation fails for ContainerMin > ContainerMax
- [ ] Validation fails for ContainerDefault > ContainerMax
- [ ] Test coverage for all ordering constraints

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Both data integrity and security flagged |

## Resources

- PR #164 code review
