---
status: complete
priority: p2
issue_id: "015"
tags: [code-review, security, auth]
dependencies: []
---

# JWT Missing `jti` Claim — No Individual Token Revocation

## Problem Statement
Access tokens lack a unique `jti` (JWT ID) claim. Without it, individual access tokens cannot be revoked or tracked. If a token is compromised, it remains valid until expiry (15 min). This is a defense-in-depth concern — refresh token rotation mitigates most scenarios.

## Findings
- **Agent**: security-sentinel (re-review of PR #2)
- **Location**: `backend/internal/auth/jwt.go`
- **Evidence**: `TokenClaims` struct has no `jti` field; `IssueAccessToken` doesn't generate one

## Proposed Solutions

### Option A: Add jti with in-memory revocation set
- Add `jti` (UUID) to claims, maintain a small revocation set checked in auth middleware
- **Pros**: Enables immediate token revocation
- **Cons**: Memory overhead, complexity for 15-min tokens
- **Effort**: Medium
- **Risk**: Low

### Option B: Add jti for audit trail only (no revocation check)
- Add `jti` to claims for logging/tracing, don't check revocation
- **Pros**: Simple, improves audit trail
- **Cons**: Doesn't enable revocation
- **Effort**: Small
- **Risk**: Low

## Recommended Action
Option B for MVP — add jti for audit correlation. Revocation can be added later if needed.

## Technical Details
- **Affected files**: `backend/internal/auth/jwt.go`
- **Components**: JWT issuance, token claims

## Acceptance Criteria
- [ ] `jti` field added to `TokenClaims`
- [ ] Unique ID generated per access token
- [ ] `jti` included in audit log entries

## Work Log
| Date | Action | Learnings |
|------|--------|-----------|
| 2026-03-12 | Created from re-review | Security sentinel flagged as medium |

## Resources
- PR #2: feat/step-2-auth
