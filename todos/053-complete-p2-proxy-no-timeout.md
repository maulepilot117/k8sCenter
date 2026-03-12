---
status: complete
priority: p2
issue_id: "053"
tags: [code-review, frontend, performance, reliability]
dependencies: ["045"]
---

# BFF Proxy Has No Request Timeout

## Problem Statement
The BFF proxy makes fetch requests to the backend with no timeout. If the backend is slow or hangs, Deno workers will be tied up indefinitely, eventually exhausting the connection pool.

## Findings
- `frontend/routes/api/[...path].ts:39-45` — `fetch(target, {...})` has no AbortSignal/timeout
- A stuck backend request will block the Deno worker forever
- Should use `AbortSignal.timeout(30000)` or similar

Flagged by: Performance Oracle (P2)

## Proposed Solutions

### Option A: Add AbortSignal.timeout
- **Pros**: Simple, built-in Deno/web API
- **Cons**: None
- **Effort**: Small
- **Risk**: Low

Add `signal: AbortSignal.timeout(30_000)` to the fetch options.

## Technical Details
- **Affected files**: `frontend/routes/api/[...path].ts`

## Acceptance Criteria
- [ ] Proxy requests timeout after 30 seconds
- [ ] Timeout returns 504 Gateway Timeout to client
