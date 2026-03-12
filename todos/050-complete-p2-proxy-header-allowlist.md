---
status: complete
priority: p2
issue_id: "050"
tags: [code-review, frontend, security]
dependencies: ["045"]
---

# BFF Proxy Should Use Header Allowlist

## Problem Statement
The BFF proxy forwards ALL request headers to the backend, including potentially sensitive browser headers. It should use an allowlist of headers to forward.

## Findings
- `frontend/routes/api/[...path].ts:34` — `const headers = new Headers(ctx.req.headers)` then only deletes `host`
- Forwards Cookie (may include session cookies for other services), Referer, Origin, etc.
- Response headers also forwarded without filtering — hop-by-hop headers (Connection, Keep-Alive, Transfer-Encoding, Proxy-Authenticate, etc.) should be stripped

Flagged by: TypeScript Reviewer (P1), Security Sentinel (HIGH), Performance Oracle (P2)

## Proposed Solutions

### Option A: Allowlist approach
- **Pros**: Most secure, only known-needed headers pass through
- **Cons**: May miss legitimate headers
- **Effort**: Small
- **Risk**: Low

Forward only: Authorization, Content-Type, Accept, X-Requested-With, X-Cluster-ID. Strip hop-by-hop from response.

## Technical Details
- **Affected files**: `frontend/routes/api/[...path].ts`

## Acceptance Criteria
- [ ] Only allowlisted headers forwarded to backend
- [ ] Hop-by-hop headers stripped from response
- [ ] Authorization header still passes through correctly
