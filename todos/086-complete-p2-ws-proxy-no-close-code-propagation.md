---
status: pending
priority: p2
issue_id: "086"
tags: [code-review, frontend, websocket, data-integrity]
dependencies: []
---

# WS Proxy Doesn't Propagate Close Codes

## Problem Statement
The WebSocket proxy in `routes/ws/[...path].ts` doesn't propagate close codes or reasons between the client and backend connections. When the backend closes with a specific code (e.g., 4001 for auth failure), the frontend client sees a generic close, losing diagnostic information.

## Findings
- Proxy `onclose` handlers close the other side but don't forward the close code/reason
- Backend uses custom close codes (4001 auth timeout, etc.) that are meaningful to the client
- Frontend `ws.ts` reconnect logic may make different decisions based on close codes

## Proposed Solutions

### Option A: Forward close code and reason in proxy onclose handlers
- **Pros:** Preserves diagnostic info, enables smart reconnect logic
- **Cons:** Minimal change
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/routes/ws/[...path].ts`

## Acceptance Criteria
- [ ] Close codes from backend are forwarded to the browser client
- [ ] Close codes from browser are forwarded to the backend
