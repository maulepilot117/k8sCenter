---
status: complete
priority: p1
issue_id: "026"
tags: [code-review, security, authorization]
dependencies: []
---

# Task Endpoint Missing Ownership Check and Predictable IDs

## Problem Statement
`HandleGetTask` in `tasks.go:98-107` does not call `requireUser` and does not verify the requesting user owns the task. Any authenticated user can view any other user's drain task status and see internal error messages. Combined with predictable task IDs (sequential timestamp + counter at `tasks.go:51-67`), tasks are trivially enumerable, making this an information disclosure and authorization bypass vulnerability.

## Findings
- `tasks.go:98-107` — `HandleGetTask` has no `requireUser` call and no comparison of `task.User` against the requesting user's identity
- `tasks.go:51-67` — Task IDs are generated as `task-{timestamp}-{counter}`, which are sequential and predictable

Flagged by: Security Sentinel (Findings 3 and 4), Git History Analyzer.

## Proposed Solutions
### Option A: Add requireUser and ownership check
Add `requireUser` call at the start of `HandleGetTask` and verify `task.User == user.Username` before returning task details. Return 403 or 404 for non-owners.
- **Pros:** Simple fix, directly addresses the authorization gap
- **Cons:** Does not address predictable IDs
- **Effort:** Small (~5 lines)
- **Risk:** Low

### Option B: Ownership check plus cryptographic task IDs
Option A plus replace sequential task ID generation with `crypto/rand` UUIDs. This adds defense-in-depth by making task IDs unguessable.
- **Pros:** Fixes both the authorization gap and the enumeration vector
- **Cons:** Slightly more code; existing task references (if any) would use new ID format
- **Effort:** Small (~10 lines)
- **Risk:** Low

## Recommended Action


## Technical Details
- **Affected files:** `backend/internal/k8s/resources/tasks.go`
- **Components:** Task management, drain operations, authorization enforcement

## Acceptance Criteria
- [ ] Non-owner of a task receives 403 or 404 when requesting task status
- [ ] Task IDs are not sequential or predictable (use crypto/rand UUIDs)
- [ ] Test verifies ownership enforcement — user A cannot view user B's task
- [ ] `requireUser` is called in `HandleGetTask`

## Work Log
| Date | Action | Notes |
|------|--------|-------|
| 2026-03-12 | Created | From PR #3 code review |

## Resources
- PR: #3
