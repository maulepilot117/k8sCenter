---
status: pending
priority: p1
issue_id: 236
tags: [security, performance, code-review, phase4b]
---

# User creation endpoint lacks rate limiting (Argon2id CPU exhaustion)

## Problem Statement

The POST /api/v1/users endpoint has no rate limiter. Each call triggers Argon2id hashing (64MB memory, CPU-intensive). A compromised admin token could trigger rapid concurrent requests, exhausting server CPU and memory. This contrasts with the login and setup endpoints, which both use `middleware.RateLimit`.

## Findings

- POST /api/v1/users is registered without any rate limiting middleware.
- Argon2id hashing is computationally expensive by design (64MB memory per hash).
- Rapid concurrent requests could cause CPU exhaustion and denial of service.
- Login (`/auth/login`) and setup (`/setup/init`) endpoints both apply `middleware.RateLimit(s.RateLimiter)` — this endpoint should follow the same pattern.

## Technical Details

- **Affected file:** `backend/internal/server/routes.go`, lines 113-119

## Acceptance Criteria

- [ ] Add `middleware.RateLimit(s.RateLimiter)` to the POST /api/v1/users handler, or apply a dedicated rate limiter for user management endpoints
- [ ] Verify rate limiting is applied per-IP consistent with existing auth endpoint behavior
- [ ] Add a test confirming rate limiting triggers on rapid POST /api/v1/users requests
