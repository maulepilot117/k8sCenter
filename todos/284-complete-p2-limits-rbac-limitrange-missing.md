---
status: pending
priority: p2
issue_id: "284"
tags: [code-review, limits, rbac, security]
dependencies: []
---

# Missing RBAC Check for LimitRange Resources

## Problem Statement

The `HandleGetNamespace` function only checks RBAC permissions for `resourcequotas` but the response also includes `LimitRange` data. A user with permission to view ResourceQuotas but not LimitRanges in a namespace would still receive LimitRange details.

Additionally, the `filterByRBAC` function filters by checking only `resourcequotas` permissions. If a namespace has only LimitRanges (no ResourceQuotas), users without `resourcequotas` permission would be incorrectly denied the ability to see those LimitRanges.

## Findings

**Source:** Security Sentinel agent

**Location:** 
- `/Users/Chris.White/Documents/code-projects/k8sCenter/backend/internal/limits/handler.go` (lines 97-107, 239-252)

**Evidence:**
```go
// HandleGetNamespace - only checks resourcequotas
allowed, err := h.AccessChecker.CanAccess(r.Context(), user.Username, user.KubernetesGroups, "get", "resourcequotas", namespace)
// ... no check for "limitranges"

// filterByRBAC - same issue
allowed, err := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "resourcequotas", s.Namespace)
```

**Impact:**
- Information disclosure of LimitRange configurations to users who may not have permission to view them
- Incorrect authorization decisions leading to either over-exposure or under-exposure of namespace limit data

## Proposed Solutions

### Solution A: Check Both Resources, Return Partial Data

```go
quotaAllowed, _ := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "resourcequotas", namespace)
limitRangeAllowed, _ := h.AccessChecker.CanAccess(ctx, user.Username, user.KubernetesGroups, "get", "limitranges", namespace)

detail := &NamespaceLimits{Namespace: namespace}
if quotaAllowed {
    detail.Quotas = h.fetchQuotas(namespace)
}
if limitRangeAllowed {
    detail.LimitRanges = h.fetchLimitRanges(namespace)
}
```

**Pros:** Granular, respects individual resource permissions  
**Cons:** More API calls, partial responses may confuse UI  
**Effort:** Medium  
**Risk:** Low

### Solution B: Require Either Permission

Allow access if user has permission for either resource type.

```go
quotaAllowed, _ := h.AccessChecker.CanAccess(...)
limitRangeAllowed, _ := h.AccessChecker.CanAccess(...)
if !quotaAllowed && !limitRangeAllowed {
    httputil.WriteError(w, http.StatusForbidden, "access denied", "")
    return
}
```

**Pros:** More permissive, fewer denied requests  
**Cons:** Still shows data user may not be authorized for  
**Effort:** Small  
**Risk:** Low

### Solution C: Keep Current Behavior, Document

Document that resourcequotas permission is the gating permission for the limits feature.

**Pros:** No code changes  
**Cons:** Doesn't fix the security concern  
**Effort:** Small  
**Risk:** Medium

## Recommended Action

_To be filled during triage_

## Technical Details

**Affected Files:**
- `backend/internal/limits/handler.go`

**Components:**
- HandleGetNamespace handler
- filterByRBAC function

## Acceptance Criteria

- [ ] LimitRange data only shown to users with limitranges permission
- [ ] Or: documented that resourcequotas permission gates all limits data
- [ ] Test verifies RBAC behavior for both resource types

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-10 | Created from code review | Security agent flagged |

## Resources

- PR #164 code review
