---
status: complete
priority: p3
issue_id: 250
tags: [consistency, frontend, code-review, phase4b]
---

# UserWizard minor inconsistencies with wizard pattern

## Problem Statement

UserWizard.tsx has several minor deviations from the patterns established by other wizard components in the codebase.

## Findings

Three inconsistencies identified:

(a) **Redundant createdUsername signal** — After successful user creation, a separate `createdUsername` signal stores the username for the success message. This is redundant since `form.value.username` is still available at that point.

(b) **Plain function instead of useCallback** — UserWizard uses a plain function for `updateField`, while other wizards (e.g., RoleBindingWizard) use `useCallback` for their field update helpers. This is a consistency issue, not a performance issue given the component's simplicity.

(c) **Different beforeunload guard condition** — UserWizard checks the username field to determine if the form is dirty for the beforeunload warning, while other wizards use a dedicated `dirty` signal. This produces slightly different behavior for the unsaved-changes guard.

## Technical Details

Affected files:
- `frontend/islands/UserWizard.tsx`

## Acceptance Criteria

- [ ] Remove `createdUsername` signal and use `form.value.username` in the success message
- [ ] Replace plain `updateField` function with `useCallback` to match other wizards
- [ ] Use a `dirty` signal for the beforeunload guard condition, consistent with other wizards
