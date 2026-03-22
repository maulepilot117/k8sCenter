---
status: complete
priority: p2
issue_id: "227"
tags: [code-review, backend, duplication, refactor, websocket]
dependencies: []
---

# WebSocket Handler Duplication Between Logs and Flows

## Problem Statement

`handle_ws_logs.go` and `handle_ws_flows.go` share ~80 lines of identical WebSocket boilerplate code: upgrade logic, auth message handling, ping/pong setup, and read pump goroutine. Constants (`writeWait`, `pongWait`, `pingPeriod`, `maxMessageSize`) are also duplicated across both files.

**Location:** `backend/internal/server/handle_ws_logs.go` and `backend/internal/server/handle_ws_flows.go`

## Proposed Solutions

### Option A: Extract shared `wsAuthAndStream` helper
- Create a shared helper function that handles the common WS lifecycle: upgrade, read auth message, validate JWT, set up ping/pong, and return the authenticated connection. Each handler then only implements its specific streaming logic.
- **Effort:** Medium — extract common code, define a callback interface for the streaming part.
- **Risk:** Low — both handlers follow the same pattern.

### Option B: Extract shared constants and utility functions
- At minimum, move the duplicated constants and small utility functions (upgrade, auth, ping/pong setup) to a shared file, keeping the main handler logic separate.
- **Effort:** Low — less refactoring, still removes duplication.
- **Risk:** Low.

## Acceptance Criteria

- [ ] WebSocket constants are defined in a single location
- [ ] Upgrade, auth, and ping/pong boilerplate is not duplicated
- [ ] Both log streaming and flow streaming continue to work correctly
- [ ] Adding a new WebSocket handler in the future requires minimal boilerplate

## Work Log

- 2026-03-22: Created from Phase 4A code review.
