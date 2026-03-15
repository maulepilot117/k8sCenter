---
status: complete
priority: p3
issue_id: "023"
tags: [code-review, quality, naming]
dependencies: []
---

# JWT Claims Use snake_case While API Uses camelCase

## Problem Statement
JWT custom claims use `k8s_user` and `k8s_groups` (snake_case) while all other JSON tags in the codebase use camelCase. JWT registered claims are conventionally snake_case (RFC 7519), so this is defensible, but custom claims could follow the project's camelCase convention.

## Findings
- **Agent**: pattern-recognition-specialist
- **Location**: `backend/internal/auth/jwt.go`, TokenClaims struct
- **Evidence**: `json:"k8s_user"` and `json:"k8s_groups"` vs camelCase everywhere else

## Proposed Solutions

### Option A: Change to camelCase (`k8sUser`, `k8sGroups`)
- **Pros**: Consistent with project convention
- **Cons**: Breaking change if tokens already in the wild (not yet — MVP)
- **Effort**: Small

### Option B: Keep snake_case (follow JWT convention)
- **Pros**: Matches JWT ecosystem conventions
- **Effort**: None

## Recommended Action
Either is fine. Decide during triage — if changed, do it before tokens are issued in production.

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Cosmetic, no functional impact |

## Resources
- PR #2: feat/step-2-auth
