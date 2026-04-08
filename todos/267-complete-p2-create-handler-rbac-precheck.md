---
status: pending
priority: p2
issue_id: "267"
tags: [code-review, security, backend, consistency]
dependencies: []
---

# Create Handlers Missing Explicit RBAC Pre-Check

## Problem Statement

The three Create handlers (HandleCreateProvider, HandleCreateAlert, HandleCreateReceiver) do not perform an explicit `CanAccessGroupResource("create", ...)` check before attempting the K8s API call. All Update, Delete, and Suspend handlers in the same file DO perform this check. This is an inconsistency, not a vulnerability — impersonation enforces real RBAC regardless.

## Findings

**Identified by:** Security Sentinel, Architecture Strategist, Pattern Recognition (all 3 flagged)

**Evidence:**
- `handler.go:300-335` (HandleCreateProvider) — no RBAC pre-check
- `handler.go:506-541` (HandleCreateAlert) — no RBAC pre-check
- `handler.go:712-747` (HandleCreateReceiver) — no RBAC pre-check
- `handler.go:347` (HandleUpdateProvider) — HAS RBAC pre-check
- `handler.go:396` (HandleDeleteProvider) — HAS RBAC pre-check

**Impact:**
- Unauthorized creates still reach K8s API server (wasted call)
- Error message quality differs (K8s 403 vs handler-level friendly message)
- Audit log may contain internal K8s error details

## Proposed Solutions

### Option A: Add pre-check to all 3 Create handlers (Recommended)
Add 6 lines per handler:
```go
can, err := h.AccessChecker.CanAccessGroupResource(r.Context(), user.KubernetesUsername, user.KubernetesGroups, "create", "notification.toolkit.fluxcd.io", "providers", input.Namespace)
if err != nil || !can {
    httputil.WriteError(w, http.StatusForbidden, "you do not have permission to create providers in this namespace", "")
    return
}
```
- **Effort:** Small (18 lines total)
- **Risk:** None

## Acceptance Criteria

- [ ] All 3 Create handlers have explicit CanAccessGroupResource("create", ...) checks
- [ ] Error messages match the friendly pattern used by Update/Delete/Suspend
- [ ] Tests pass

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Consistency finding, not a security vulnerability |

## Resources

- PR: #153
- File: backend/internal/notification/handler.go
