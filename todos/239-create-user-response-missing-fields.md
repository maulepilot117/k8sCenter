---
status: complete
priority: p2
issue_id: 239
tags: [api, code-review, phase4b]
---

# Create user response omits id and k8sGroups

## Problem Statement

The POST response for user creation returns username, k8sUsername, and roles but omits the user's `id` (needed by the frontend for delete and password-change operations) and `k8sGroups`. This forces the frontend to make an extra list call to retrieve the id before performing follow-up operations.

## Findings

After creating a user, the response payload does not include the `id` or `k8sGroups` fields. The frontend UserManager and UserWizard components need the id to construct URLs for DELETE and PUT password endpoints. Without it, an additional GET /users call is required.

## Technical Details

- **File:** `backend/internal/server/handle_users.go`, lines 119-125
- The response struct literal includes `username`, `k8sUsername`, and `roles` but not `id` or `k8sGroups`.

## Acceptance Criteria

- [ ] POST /api/v1/users response includes `id` field (the user's unique identifier)
- [ ] POST /api/v1/users response includes `k8sGroups` field
- [ ] Frontend can use the returned id directly for subsequent delete/password operations without an extra list call
- [ ] Unit test verifies the response contains all expected fields
