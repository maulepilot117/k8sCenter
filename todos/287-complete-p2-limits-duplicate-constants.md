---
status: pending
priority: p2
issue_id: "287"
tags: [code-review, limits, wizard, dry]
dependencies: []
---

# Duplicate Annotation Constants Between Packages

## Problem Statement

The same annotation keys are defined in two places:
- `limits/types.go` (exported)
- `wizard/namespace_limits.go` (unexported)

This creates drift risk if the keys change in one place but not the other.

## Findings

**Source:** Pattern Recognition + Architecture agents

**Location:**
- `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/limits/types.go` (lines 21-24)
- `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/wizard/namespace_limits.go` (lines 15-16)

**Evidence:**
```go
// limits/types.go
AnnotationWarnThreshold     = "k8scenter.io/warn-threshold"
AnnotationCriticalThreshold = "k8scenter.io/critical-threshold"

// wizard/namespace_limits.go
annotationWarnThreshold     = "k8scenter.io/warn-threshold"
annotationCriticalThreshold = "k8scenter.io/critical-threshold"
```

## Proposed Solutions

### Solution: Import from limits Package

```go
// wizard/namespace_limits.go
import "github.com/kubecenter/kubecenter/internal/limits"

// Use limits.AnnotationWarnThreshold instead of local constant
```

**Pros:** Single source of truth  
**Cons:** Creates import dependency  
**Effort:** Small  
**Risk:** Low

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/wizard/namespace_limits.go`

## Acceptance Criteria

- [ ] Single definition of annotation constants
- [ ] Wizard imports from limits package

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Pattern recognition flagged |

## Resources

- PR #164 code review
