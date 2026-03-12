---
status: complete
priority: p2
issue_id: "049"
tags: [code-review, frontend, security]
dependencies: []
---

# Missing Security Headers in Frontend Middleware

## Problem Statement
The frontend middleware at `frontend/routes/_middleware.ts` only sets default state values but adds no security headers. The CLAUDE.md explicitly requires Content Security Policy headers, and standard security headers (X-Frame-Options, X-Content-Type-Options, Strict-Transport-Security, Referrer-Policy) are missing.

## Findings
- `frontend/routes/_middleware.ts` — Only sets `ctx.state.title` and `ctx.state.user`, no response header manipulation
- CLAUDE.md Security Rules: "Content Security Policy headers. Strict CSP that allows only same-origin scripts, the Monaco CDN, and Grafana iframe sources."
- Missing: CSP, X-Frame-Options (DENY), X-Content-Type-Options (nosniff), Referrer-Policy, Permissions-Policy

Flagged by: Security Sentinel (MEDIUM), TypeScript Reviewer (P2)

## Proposed Solutions

### Option A: Add security headers in _middleware.ts
- **Pros**: Single location, applies to all routes
- **Cons**: None significant
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/routes/_middleware.ts`

## Acceptance Criteria
- [ ] CSP header set with same-origin scripts, styles
- [ ] X-Frame-Options: DENY
- [ ] X-Content-Type-Options: nosniff
- [ ] Referrer-Policy: strict-origin-when-cross-origin
- [ ] Headers present on all page responses
