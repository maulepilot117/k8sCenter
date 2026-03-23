---
status: complete
priority: p2
issue_id: 243
tags: [frontend, functional-gap, code-review, phase4b]
---

# UserWizard has no UI for assigning admin role

## Problem Statement

The UserWizard initializes `roles` as an empty array and provides no UI element to modify it. All users created via the wizard get zero roles. The backend supports role assignment, but the frontend has no toggle or checkbox to grant the admin role.

## Findings

The `roles` field is set to `[]` on line 30 and is never updated by any form element in the wizard steps. This means administrators cannot assign the "admin" role when creating new users through the UI, despite the backend supporting it.

## Technical Details

- **File:** `frontend/islands/UserWizard.tsx`, line 30
- `roles` is initialized as `[]` in the form state and has no corresponding UI control
- The backend POST /api/v1/users endpoint accepts and stores the `roles` array

## Acceptance Criteria

- [ ] An "Admin" toggle or checkbox is added to the Account step of the UserWizard
- [ ] When toggled on, the `roles` array includes `"admin"` in the request payload
- [ ] When toggled off (default), `roles` is empty
- [ ] The Review step displays whether the user will be an admin
- [ ] The toggle is visually clear about what admin access grants
