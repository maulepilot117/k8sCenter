---
status: complete
resolution: "Validated by Step 12 — OIDC and LDAP providers now implement the AuthProvider interface, confirming it was not YAGNI."
priority: p3
issue_id: "020"
tags: [code-review, quality, architecture]
dependencies: []
---

# AuthProvider Interface — YAGNI Consideration

## Problem Statement
The `AuthProvider` interface currently has only one implementation (`LocalProvider`). The interface was designed for future OIDC/LDAP providers. The simplicity reviewer flagged this as potential YAGNI, but the plan explicitly calls for OIDC/LDAP in Step 12.

## Findings
- **Agent**: code-simplicity-reviewer (re-review of PR #2)
- **Location**: `backend/internal/auth/provider.go`
- **Evidence**: Single implementation, interface defined before second consumer exists

## Proposed Solutions

### Option A: Keep interface (recommended)
- OIDC/LDAP providers are planned for Step 12
- Interface enables testing with mocks
- **Pros**: Ready for planned expansion, testable
- **Cons**: Slight YAGNI until Step 12
- **Effort**: None (keep as-is)

### Option B: Remove interface, add when needed
- Delete interface, use `*LocalProvider` directly
- **Pros**: Simpler today
- **Cons**: Refactor needed at Step 12
- **Effort**: Small to remove, Small to re-add later

## Recommended Action
Option A — keep it. Step 12 is planned and the interface is already well-defined. This is intentional design-for-change, not speculative abstraction.

## Acceptance Criteria
- [ ] Triage decision: keep or remove

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Intentional per plan, not YAGNI |

## Resources
- PR #2: feat/step-2-auth
- Plan: Step 12 (OIDC/LDAP auth)
