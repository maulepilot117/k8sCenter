---
status: pending
priority: p2
issue_id: "270"
tags: [code-review, performance, backend]
dependencies: []
---

# fetchAll Loads All 3 Resource Types Even When Only 1 Is Needed

## Problem Statement

Every list and status endpoint calls `fetchAll()`, which lists Providers, Alerts, AND Receivers from the K8s API, then caches all three. When a user visits the Providers tab, the backend fetches alerts and receivers too (discarding them). When any single CRD changes, `InvalidateCache()` clears all three types, forcing a re-fetch of everything.

Additionally, each frontend island calls `/status` alongside its list endpoint. The status endpoint does full fetchAll + triple RBAC filtering just to return three integer counts, which is redundant with the list data.

## Findings

**Identified by:** Performance Oracle

**Evidence:**
- `handler.go:265-297` (HandleListProviders) — calls fetchAll, uses only `data.providers`
- `handler.go:241-260` (HandleStatus) — calls fetchAll + triple RBAC filtering for counts
- `cmd/kubecenter/main.go:525-545` — any CRD watch invalidates entire cache
- Frontend islands each call both `/status` AND their list endpoint

**Impact at scale:**
- 3x K8s API calls on every cache invalidation instead of 1
- Triple RBAC filtering on status endpoint (up to 3N SelfSubjectAccessReview calls for N namespaces)
- Redundant status fetch from each island

## Proposed Solutions

### Option A: Split into per-resource caches (Recommended)
- 3 independent caches with own singleflight groups and TTL
- CRD watch callbacks invalidate only the relevant cache
- Each list endpoint fetches only its resource type
- Make status endpoint lightweight (CRD existence check only)
- **Effort:** Medium
- **Risk:** Low

### Option B: Remove /status call from islands
- Derive availability from list endpoint success/failure
- Quick win, reduces redundant work
- **Effort:** Small
- **Risk:** Low

## Recommended Action

Option B first (quick win), Option A as follow-up.

## Acceptance Criteria

- [ ] Each list endpoint only fetches its own resource type
- [ ] Cache invalidation is per-resource-type
- [ ] Status endpoint is lightweight (no full fetch + RBAC filter)
- [ ] No functional regression

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Same pattern as gitops/policy — codebase-wide optimization opportunity |

## Resources

- PR: #153
- File: backend/internal/notification/handler.go
