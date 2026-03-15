---
status: complete
priority: p3
issue_id: "017"
tags: [code-review, performance, auth]
dependencies: []
---

# GetUserByID O(n) Linear Scan

## Problem Statement
`GetUserByID` iterates over all users to find a match. For a local auth provider with a handful of admin users this is negligible, but if user count grows it becomes inefficient.

## Findings
- **Agent**: performance-oracle (re-review of PR #2)
- **Location**: `backend/internal/auth/local.go`
- **Evidence**: Linear scan through `users` map values

## Proposed Solutions

### Option A: Add ID-indexed map
- Maintain a second `map[string]*storedUser` keyed by ID
- **Pros**: O(1) lookup
- **Cons**: Dual-map maintenance
- **Effort**: Small
- **Risk**: Low

### Option B: Defer (accept for MVP)
- Local auth will have <100 users typically; O(n) is fine
- **Pros**: No code change
- **Effort**: None

## Recommended Action
Option B. Local provider is not the performance bottleneck. Revisit if/when switching to database-backed user store.

## Acceptance Criteria
- [ ] Triage decision made

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Performance oracle flagged, acceptable for MVP |

## Resources
- PR #2: feat/step-2-auth
