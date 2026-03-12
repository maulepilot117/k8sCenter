---
status: complete
priority: p1
issue_id: "005"
tags: [code-review, security]
dependencies: []
---

# No Request Body Size Limit on Auth Endpoints

## Problem Statement
`handleSetupInit` and `handleLogin` call `json.NewDecoder(r.Body).Decode(...)` without limiting body size. An attacker can send a multi-gigabyte payload, exhausting server memory. Combined with no max password length, a 10 MB password would also cause expensive Argon2id processing.

## Findings
- **Source**: Security agent (Findings 3, 4), Patterns agent
- **Files**: `backend/internal/server/routes.go:108,167`

## Proposed Solutions

### Option A: http.MaxBytesReader + password length limit (Recommended)
Wrap `r.Body` with `http.MaxBytesReader(w, r.Body, 4096)` for auth endpoints. Add max password length validation (128 chars) and max username length (253 chars, per k8s limit).
- **Effort**: Small
- **Risk**: Low

## Acceptance Criteria
- [ ] Auth endpoints reject bodies >4KB with 413
- [ ] Password length capped at 128 chars
- [ ] Username length capped at 253 chars
- [ ] Username validated against k8s-compatible regex
