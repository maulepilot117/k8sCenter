---
status: complete
priority: p3
issue_id: "012"
tags: [code-review, security]
dependencies: []
---

# Refresh Endpoint Missing Rate Limit and Audit Logging

## Problem Statement
`/api/v1/auth/refresh` has no rate limiting (login and setup do). Refresh and logout handlers don't log audit entries on failure.

## Proposed Solutions
Apply rate limiter to refresh endpoint. Add audit logging for refresh success/failure and logout failure.
- **Effort**: Small

## Acceptance Criteria
- [ ] Rate limiter applied to `/auth/refresh`
- [ ] Audit entries for refresh success/failure
- [ ] Audit entry for failed setup token (currently only logger.Warn)
