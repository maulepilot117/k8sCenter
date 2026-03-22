---
status: pending
priority: p3
issue_id: "230"
tags: [code-review, backend, security, information-disclosure]
dependencies: []
---

# Setup Response Too Verbose

## Problem Statement

`handleSetupInit` returns the full User object including `k8sUsername`, `groups`, and `id` fields. The client only needs confirmation that the account was created. Exposing internal user structure is unnecessary information disclosure.

**Location:** `backend/internal/server/handle_setup.go` lines 109-113

## Proposed Solutions

### Option A: Return minimal response
- Return only `{"username": "admin", "created": true}` instead of the full User object.
- **Effort:** Low — change the response payload to a simple struct.
- **Risk:** Low — no client depends on the extra fields from setup.

## Acceptance Criteria

- [ ] Setup response contains only `username` and `created` fields
- [ ] Internal fields (id, k8sUsername, groups) are not exposed
- [ ] Frontend setup flow continues to work correctly

## Work Log

- 2026-03-22: Created from Phase 4A code review.
