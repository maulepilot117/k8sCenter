---
status: pending
priority: p3
issue_id: "058"
tags: [code-review, frontend, security]
dependencies: []
---

# LoginForm Missing maxLength on Inputs

## Problem Statement
The login form inputs have no `maxLength` attribute, allowing arbitrarily long input that could cause UI issues or be used for denial of service against the Argon2id hashing (very long passwords are expensive to hash).

## Findings
- `frontend/islands/LoginForm.tsx` — username and password inputs have no maxLength
- Backend Argon2id hashing is CPU-intensive; long passwords amplify this

Flagged by: TypeScript Reviewer (P3)

## Proposed Solutions

### Option A: Add maxLength attributes
- **Pros**: Simple, standard practice
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

Add `maxLength={255}` to both username and password inputs.

## Technical Details
- **Affected files**: `frontend/islands/LoginForm.tsx`

## Acceptance Criteria
- [ ] Username input has maxLength
- [ ] Password input has maxLength
