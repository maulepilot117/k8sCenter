---
status: pending
priority: p1
issue_id: "211"
tags: [code-review, security, rbac]
dependencies: []
---

# Missing RBAC "get" check on CronJob before trigger

## Problem Statement

`HandleTriggerCronJob` checks `create` permission on `jobs` (correct — it creates
a Job), but reads the CronJob from the informer cache **without** checking `get`
permission on `cronjobs`. The informer cache is populated using the service
account's broad read access, so this leaks the CronJob's full job template spec
(container images, env vars, volume mounts, commands) to a user who may have
`create jobs` permission but NOT `get cronjobs` permission.

**Location:** `backend/internal/k8s/resources/jobs.go:322-384`

The informer read at line 335 (`h.Informers.CronJobs().CronJobs(ns).Get(name)`)
returns the full CronJob object without any RBAC gate.

## Findings

- Every other handler that reads from the informer checks the appropriate `get`
  permission first (e.g., HandleGetJob checks `get` on `jobs`)
- The triggered Job's spec is a copy of the CronJob template, so the created Job
  also exposes the template (but creating a Job already requires `create jobs`)
- The OwnerReference also confirms the CronJob's UID

## Proposed Solutions

### Option A: Add "get" check on cronjobs before informer read
Add one line before line 335:
```go
if !h.checkAccess(w, r, user, "get", kindCronJob, ns) {
    return
}
```
- **Pros:** Simple, follows existing pattern, defense-in-depth
- **Cons:** None
- **Effort:** Small (1 line)
- **Risk:** None

## Recommended Action

Option A — add the RBAC check. This is a one-line fix.

## Acceptance Criteria

- [ ] `HandleTriggerCronJob` checks both `get cronjobs` and `create jobs`
- [ ] A user with `create jobs` but NOT `get cronjobs` gets 403

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found by security-sentinel agent during PR #46 review |
