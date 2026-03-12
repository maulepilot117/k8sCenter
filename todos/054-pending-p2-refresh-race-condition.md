---
status: pending
priority: p2
issue_id: "054"
tags: [code-review, frontend, auth, data-integrity]
dependencies: []
---

# Token Refresh Race Condition on Concurrent 401s

## Problem Statement
When multiple API calls return 401 simultaneously, each will independently trigger a token refresh attempt. While `frontend/lib/api.ts` has a `refreshPromise` guard to deduplicate concurrent refreshes, the implementation may have edge cases where the promise resolves but the new token isn't yet stored before subsequent retries fire.

## Findings
- `frontend/lib/api.ts` — Uses `refreshPromise` singleton to deduplicate
- Race window: Between refresh response and `setAccessToken()` call, other retries may use the old (expired) token
- Multiple 401s could cause a logout cascade if the refresh itself gets a 401

Flagged by: Data Integrity Guardian (HIGH)

## Proposed Solutions

### Option A: Ensure token is set before resolving refresh promise
- **Pros**: Closes the race window
- **Cons**: Minor refactor
- **Effort**: Small
- **Risk**: Low

Make the refresh promise resolve AFTER `setAccessToken()` completes, and have all waiters read the new token from the store.

## Technical Details
- **Affected files**: `frontend/lib/api.ts`

## Acceptance Criteria
- [ ] Concurrent 401s result in only one refresh call
- [ ] All retried requests use the new token
- [ ] Failed refresh results in clean logout, not infinite loop
