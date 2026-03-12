---
status: pending
priority: p3
issue_id: "060"
tags: [code-review, frontend, typescript]
dependencies: ["044"]
---

# UserInfo Type Doesn't Match Backend Response Shape

## Problem Statement
The `UserInfo` interface in `k8s-types.ts` doesn't match what the backend actually returns from `/auth/me`. This will cause type mismatches and potential runtime errors.

## Findings
- `frontend/lib/k8s-types.ts` — `UserInfo` type may have fields that don't match backend's user shape
- Backend `handle_auth.go` me handler returns `{user: {username, role, kubernetesUser, kubernetesGroups}, rbac: {...}}`

Flagged by: Data Integrity Guardian (MEDIUM), TypeScript Reviewer (P3)

## Proposed Solutions

### Option A: Align UserInfo with actual backend response
- **Pros**: Correct types prevent runtime errors
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/lib/k8s-types.ts`

## Acceptance Criteria
- [ ] UserInfo type matches backend /auth/me response shape
- [ ] No type assertion hacks needed when using the type
