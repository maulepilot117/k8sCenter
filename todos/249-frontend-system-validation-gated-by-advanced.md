---
status: complete
priority: p3
issue_id: 249
tags: [validation, frontend, code-review, phase4b]
---

# Frontend system: validation gated by showAdvanced toggle

## Problem Statement

The frontend validation that rejects usernames with the `system:` prefix only runs when the `showAdvanced` toggle is enabled. While the backend validates this authoritatively regardless of the toggle state, the frontend defense-in-depth check is incomplete.

## Findings

- In UserWizard.tsx at line 85, the `system:` prefix validation is conditionally applied based on the `showAdvanced` signal
- If a user somehow submits a `system:` prefixed username without toggling advanced options, the frontend will not catch it
- The backend rejects `system:` prefixed usernames unconditionally, so this is not a security issue — purely a defense-in-depth gap

## Technical Details

Affected files:
- `frontend/islands/UserWizard.tsx` — line 85, system: prefix validation conditional

## Acceptance Criteria

- [ ] Move `system:` prefix validation outside the `showAdvanced` gate so it runs unconditionally on the username field
- [ ] Verify backend still rejects `system:` prefixed usernames as the authoritative check
