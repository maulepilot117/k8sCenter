---
status: pending
priority: p1
issue_id: 234
tags: [security, code-review, phase4b]
---

# No allowlist validation for roles field in user creation

## Problem Statement

The `roles` field from the request body in user creation is passed through with no validation. Arbitrary role strings are accepted and stored (e.g. "superuser", "god"), which could lead to privilege confusion or future privilege escalation if role-based checks are added without corresponding validation at creation time.

## Findings

- The roles array from the JSON request body is stored as-is with no validation against known/valid roles.
- Any string value is accepted, including meaningless or potentially dangerous role names.
- No cap on the number of roles in the array, allowing unbounded storage.

## Technical Details

- **Affected file:** `backend/internal/server/handle_users.go`, lines 88-92

## Acceptance Criteria

- [ ] Validate the `roles` field against an allowlist of permitted roles (e.g. `"admin"`, `"viewer"`)
- [ ] Reject requests containing any role not in the allowlist with a 400 response and clear error message
- [ ] Cap the roles array length to a reasonable maximum (e.g. 10)
- [ ] Add unit tests covering invalid role rejection and array length cap
