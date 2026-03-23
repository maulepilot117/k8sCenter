---
status: pending
priority: p3
issue_id: 247
tags: [testing, code-review, phase4b]
---

# No unit tests for handleCreateUser or RoleBindingInput.Validate()

## Problem Statement

The new user creation handler and RoleBindingInput validation logic lack unit test coverage. Key code paths including success, conflict, and input rejection are untested.

## Findings

The following scenarios have no test coverage:

- `handleCreateUser` 201 happy path (successful user creation)
- `handleCreateUser` 409 duplicate username conflict
- `handleCreateUser` 400 rejection of `system:` username prefix
- `handleCreateUser` 400 rejection of `system:masters` group
- `RoleBindingInput.Validate()` edge cases (empty subjects, invalid kinds, boundary values)

## Technical Details

Affected files:
- `backend/internal/server/handle_users.go` — handleCreateUser handler
- `backend/internal/wizard/rolebinding.go` — RoleBindingInput.Validate()

## Acceptance Criteria

- [ ] Add httptest integration tests for handleCreateUser covering 201, 409, and 400 responses
- [ ] Add unit tests for RoleBindingInput.Validate() covering edge cases and boundary conditions
- [ ] All new tests pass with `go test -race`
