---
status: pending
priority: p2
issue_id: "075"
tags: [code-review, frontend, websocket, auth]
dependencies: []
---

# JWT Not Refreshed on WebSocket Reconnect

## Problem Statement
When the WebSocket reconnects (after disconnect or visibility change), `ws.ts` sends the original JWT token which may have expired (15min TTL). The backend will reject the auth message and the WS connection will fail, triggering infinite reconnect loops with a stale token.

## Findings
- `ws.ts` stores the token at connection time and reuses it on reconnect
- JWT access tokens expire after 15 minutes
- WebSocket connections can be long-lived (hours), so reconnect after sleep/visibility change will use an expired token
- No mechanism to fetch a fresh token before reconnect

## Proposed Solutions

### Option A: Call the REST refresh endpoint before each WS reconnect
- **Pros:** Always sends a valid token, leverages existing refresh flow
- **Cons:** Adds a network round-trip before WS reconnect
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/lib/ws.ts`, `frontend/lib/api.ts`

## Acceptance Criteria
- [ ] WS reconnect uses a fresh JWT token (not the original one)
- [ ] If token refresh fails, WS stops reconnecting (don't loop with bad token)
