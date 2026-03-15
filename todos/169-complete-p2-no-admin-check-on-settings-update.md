---
status: pending
priority: p2
issue_id: "169"
tags: [code-review, security, authorization, step-11]
dependencies: []
---

# No Admin Authorization Check on Settings Update

## Problem Statement
`HandleUpdateSettings` and `HandleTestEmail` only require authentication (any valid JWT), not admin privileges. Any authenticated user can modify SMTP credentials, enable/disable alerting, change rate limits, or set `TLSInsecure: true`.

## Findings
- **Source**: Security review (H2)
- **Location**: `backend/internal/alerting/handler.go` HandleUpdateSettings, HandleTestEmail

## Proposed Solutions
### Option A: Add admin role check
Add an admin check before settings mutation (when RBAC roles are implemented). For now, this is acceptable since all authenticated users are admins (local auth only).
- **Effort**: Small (deferred to Step 12 OIDC/LDAP when roles exist)

## Resources
- PR: #17
