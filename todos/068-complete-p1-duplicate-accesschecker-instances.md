---
status: pending
priority: p1
issue_id: "068"
tags: [code-review, backend, architecture, memory-leak]
dependencies: []
---

# Duplicate AccessChecker Instances — Unbounded Cache Growth

## Problem Statement
`main.go:71` creates an AccessChecker with a cache sweeper goroutine, but `server.go:77` creates a second AccessChecker without a sweeper. The second instance's cache grows unboundedly (no cleanup). Additionally, duplicate RBAC API calls are made because the two instances don't share cache.

## Findings
- `main.go` creates `resources.NewAccessChecker(clientFactory)` which starts a sweeper goroutine
- `server.go` creates another `resources.NewAccessChecker(deps.ClientFactory)` independently
- The server.go instance has no sweeper, so expired entries accumulate forever
- Both instances make independent SelfSubjectAccessReview calls for the same user/resource combinations

## Proposed Solutions

### Option A: Pass the single AccessChecker through Deps struct
- **Pros:** Single source of truth, sweeper runs, shared cache
- **Cons:** Minor refactor to Deps struct
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/cmd/kubecenter/main.go`, `backend/internal/server/server.go`
- **Components:** AccessChecker, server dependencies

## Acceptance Criteria
- [ ] Only one AccessChecker instance exists in the application
- [ ] Sweeper goroutine runs and cleans expired cache entries
- [ ] RBAC checks use a shared cache across all handlers
