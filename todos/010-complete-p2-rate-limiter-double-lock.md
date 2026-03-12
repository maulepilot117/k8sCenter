---
status: complete
priority: p2
issue_id: "010"
tags: [code-review, performance, quality]
dependencies: []
---

# Rate Limiter Allow + RetryAfter Acquires Lock Twice

## Problem Statement
`Allow()` and `RetryAfter()` each acquire the mutex independently. The middleware calls both in sequence when a request is denied, creating a race window where the bucket could reset between calls.

## Findings
- **Source**: Performance agent (MODERATE-1), Simplicity agent, Patterns agent
- **File**: `backend/internal/server/middleware/ratelimit.go:43-67`

## Proposed Solutions

### Option A: Combined method (Recommended)
`func (rl *RateLimiter) Check(ip string) (allowed bool, retryAfterSec int)` — single lock acquisition, returns both values.
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Single method returns both allowed status and retry-after
- [ ] Only one lock acquisition per rate limit check
- [ ] Tests updated
