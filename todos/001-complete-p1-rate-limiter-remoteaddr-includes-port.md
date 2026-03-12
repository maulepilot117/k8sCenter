---
status: complete
priority: p1
issue_id: "001"
tags: [code-review, security, bug]
dependencies: []
---

# Rate Limiter Uses RemoteAddr Including Port — Completely Ineffective

## Problem Statement
The rate limiter in `middleware/ratelimit.go:108` uses `r.RemoteAddr` as the rate limit key. `RemoteAddr` includes the port (e.g., `192.168.1.1:54321`). Since each TCP connection has a different ephemeral port, every request gets its own bucket, making rate limiting completely non-functional.

This means the 5 req/min limit on login and setup endpoints provides zero brute-force protection.

## Findings
- **Source**: Security agent (Finding 11), confirmed by Patterns agent
- **File**: `backend/internal/server/middleware/ratelimit.go:108`
- **Evidence**: `ip := r.RemoteAddr` — Go's `http.Request.RemoteAddr` is `host:port`

## Proposed Solutions

### Option A: Parse IP with net.SplitHostPort (Recommended)
Strip the port using `net.SplitHostPort(r.RemoteAddr)`.
- **Pros**: Simple, correct, standard library
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Rate limiter keys by IP only (no port)
- [ ] Test verifies same IP with different ports shares a bucket
- [ ] Existing rate limiter tests still pass

## Resources
- PR: #2
