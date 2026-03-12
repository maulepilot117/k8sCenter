---
status: complete
priority: p3
issue_id: "013"
tags: [code-review, architecture]
dependencies: []
---

# Add Provider Field to User and RefreshSession for OIDC/LDAP Readiness

## Problem Statement
When OIDC/LDAP arrives in Step 12, the refresh flow needs to know which provider authenticated the user to look them up correctly. Neither `User` nor `RefreshSession` carries a `Provider` field.

## Proposed Solutions
Add `Provider string` to `User` struct and `RefreshSession`. Store in JWT claims. One-line addition now vs migration later.
- **Effort**: Small

## Acceptance Criteria
- [ ] `User.Provider` field added (defaults to "local")
- [ ] `RefreshSession` stores provider type
- [ ] JWT claims include provider
