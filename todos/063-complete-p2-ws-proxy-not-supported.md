---
status: pending
priority: p2
issue_id: "063"
tags: [code-review, frontend, architecture]
dependencies: []
---

# WebSocket Connections Won't Work Through BFF Proxy

## Problem Statement
The BFF proxy at `routes/api/[...path].ts` handles HTTP methods but not WebSocket upgrades. The `ws.ts` client connects to `/api/v1/ws/resources` which would hit this proxy, but `fetch()` doesn't perform WebSocket upgrades — the backend's 400/426 response would be proxied back.

## Findings
- `frontend/routes/api/[...path].ts` — Only handles GET/POST/PUT/DELETE/PATCH
- `frontend/lib/ws.ts:44` — Connects to `${WS_URL}/api/v1/ws/resources`
- WebSocket upgrade requires special handling in Deno (not a regular fetch)

Flagged by: Architecture Strategist (P2-5)

## Proposed Solutions

### Option A: Add dedicated WebSocket proxy route
- **Pros**: Clean separation, works with BFF pattern
- **Cons**: More complex proxy code
- **Effort**: Medium
- **Risk**: Low

Create `routes/api/v1/ws/[...path].ts` using Deno's native WebSocket APIs.

### Option B: Document that WS bypasses BFF in production
- **Pros**: Simpler, let Step 5 handle it
- **Cons**: CORS and auth implications
- **Effort**: Small
- **Risk**: Medium

## Technical Details
- **Affected files**: `frontend/routes/api/[...path].ts`, `frontend/lib/ws.ts`

## Acceptance Criteria
- [ ] WebSocket connections can reach the backend (either through proxy or documented bypass)
- [ ] TODO comment in proxy acknowledging the gap
