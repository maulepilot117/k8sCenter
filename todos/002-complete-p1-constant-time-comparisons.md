---
status: complete
priority: p1
issue_id: "002"
tags: [code-review, security]
dependencies: []
---

# Non-Constant-Time Comparisons for Password Hash and Setup Token

## Problem Statement
Two security-sensitive comparisons use Go's `!=` operator instead of `crypto/subtle.ConstantTimeCompare`:
1. Password hash in `local.go:72` — `hex.EncodeToString(hash) != stored.PasswordHash`
2. Setup token in `routes.go:130` — `req.SetupToken != s.Config.Auth.SetupToken`

While the dummy hash for unknown users mitigates username enumeration, known-user password hash comparison and setup token comparison remain vulnerable to timing attacks.

## Findings
- **Source**: Security agent (Findings 1, 2), Architecture agent, Patterns agent
- **Files**: `backend/internal/auth/local.go:72`, `backend/internal/server/routes.go:130`

## Proposed Solutions

### Option A: Use crypto/subtle.ConstantTimeCompare (Recommended)
Compare raw byte slices with `subtle.ConstantTimeCompare`.
- For password hash: compare `hash` bytes with `hex.DecodeString(stored.PasswordHash)` bytes
- For setup token: `subtle.ConstantTimeCompare([]byte(req.SetupToken), []byte(s.Config.Auth.SetupToken))`
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Password hash comparison uses `subtle.ConstantTimeCompare` on raw bytes
- [ ] Setup token comparison uses `subtle.ConstantTimeCompare`
- [ ] Tests still pass
