---
status: complete
priority: p3
issue_id: 248
tags: [validation, frontend, code-review, phase4b]
---

# No frontend subject count enforcement (backend caps at 50)

## Problem Statement

The backend wizard validation caps RoleBinding subjects at 50, but the frontend allows unlimited subject additions via the "Add Subject" button. Additionally, the RBAC CRUD endpoint has no subject count validation at all, meaning direct API calls bypass the wizard limit.

## Findings

- Backend wizard validation in RoleBindingInput.Validate() enforces a 50-subject cap
- Frontend RoleBindingWizard.tsx "Add Subject" button has no limit, allowing users to add arbitrarily many subjects before hitting the backend rejection
- The RBAC CRUD handler (direct PUT/POST to resources endpoint) does not validate subject count, so the 50-subject cap can be bypassed entirely via the CRUD path

## Technical Details

Affected files:
- `frontend/islands/RoleBindingWizard.tsx` — Add Subject button needs limit enforcement

## Acceptance Criteria

- [ ] Disable "Add Subject" button in RoleBindingWizard.tsx when subject count reaches 50
- [ ] Show user-facing message indicating the limit when the cap is reached
- [ ] Add server-side subject count validation in RBAC CRUD handlers to match wizard validation
