---
status: pending
priority: p2
issue_id: "076"
tags: [code-review, frontend, websocket, race-condition]
dependencies: []
---

# WS Proxy Auth Message Race Condition

## Problem Statement
In `routes/ws/[...path].ts`, the frontend WS proxy creates the backend WS connection in the `onopen` handler. Messages sent by the browser client before the backend connection is established are silently dropped — including the critical auth token message.

## Findings
- `ws/[...path].ts` creates backend connection in clientSocket.onopen
- Browser sends auth token immediately after connection opens
- Backend WS connection may not be ready yet (still doing TCP handshake)
- Auth message is dropped → backend times out waiting for auth → connection closed

## Proposed Solutions

### Option A: Queue messages until backend connection is open
- **Pros:** No messages lost, handles any ordering
- **Cons:** Small added complexity, queue needs cleanup
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/routes/ws/[...path].ts`

## Acceptance Criteria
- [ ] Auth message is reliably delivered to backend even if backend connection is still establishing
- [ ] No messages are silently dropped during connection setup
