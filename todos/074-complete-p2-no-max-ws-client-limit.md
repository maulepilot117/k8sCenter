---
status: pending
priority: p2
issue_id: "074"
tags: [code-review, backend, security, websocket, dos]
dependencies: []
---

# No Maximum Client Limit on WebSocket Hub

## Problem Statement
The WebSocket hub has no limit on the number of concurrent connected clients. An attacker can open thousands of connections, exhausting server memory and goroutines (each client spawns readPump + writePump goroutines).

## Findings
- `hub.go` register channel accepts unlimited clients
- Each client allocates a send channel buffer and spawns 2 goroutines
- No connection limit check in `handle_ws.go` before upgrading

## Proposed Solutions

### Option A: Add max connection count in hub, reject new connections when full
- **Pros:** Simple, effective DoS mitigation
- **Cons:** Legitimate users may be rejected under heavy load
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/websocket/hub.go`, `backend/internal/server/handle_ws.go`

## Acceptance Criteria
- [ ] Hub enforces a configurable maximum client count
- [ ] Connections beyond the limit receive an appropriate HTTP error before upgrade
