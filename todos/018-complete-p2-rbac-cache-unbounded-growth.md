---
status: complete
priority: p2
issue_id: "018"
tags: [code-review, performance, security, rbac]
dependencies: []
---

# RBAC Cache Unbounded Growth

## Problem Statement
The RBAC permission cache (`SelfSubjectRulesReview` results cached for 60s) has no upper bound on entries. With many distinct users, cache could grow unboundedly in memory.

## Findings
- **Agent**: performance-oracle (re-review of PR #2)
- **Location**: `backend/internal/auth/rbac.go`
- **Evidence**: Cache map grows with each unique user+namespace combination, no eviction beyond TTL

## Proposed Solutions

### Option A: Add max entries with LRU eviction
- Cap cache at e.g. 1000 entries, evict least-recently-used
- **Pros**: Bounded memory
- **Cons**: Adds LRU complexity
- **Effort**: Small (use a simple LRU or `sync.Map` with periodic sweep)
- **Risk**: Low

### Option B: Periodic full cache flush
- Sweep entire cache every N minutes instead of per-entry TTL
- **Pros**: Simpler than LRU
- **Cons**: Cache misses after flush
- **Effort**: Small
- **Risk**: Low

### Option C: Defer (accept for MVP)
- Single-cluster, likely <50 concurrent users
- **Pros**: No code change
- **Effort**: None

## Recommended Action
Option C for MVP. Single-cluster with few users. Add LRU when multi-cluster or >100 users.

## Acceptance Criteria
- [ ] Triage decision made
- [ ] If implementing: cache has bounded size with eviction policy

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Acceptable for MVP scale |

## Resources
- PR #2: feat/step-2-auth
