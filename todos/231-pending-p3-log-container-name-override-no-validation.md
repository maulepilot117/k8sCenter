---
status: pending
priority: p3
issue_id: "231"
tags: [code-review, backend, validation, websocket, logs]
dependencies: []
---

# Log Container Name Override Without Validation

## Problem Statement

The filter message received over WebSocket can override the URL container parameter without re-validating the container name against `ValidateK8sName`. This bypasses the initial URL parameter validation, potentially allowing invalid container names to reach the Kubernetes API.

**Location:** `backend/internal/server/handle_ws_logs.go` lines 117-120

## Proposed Solutions

### Option A: Validate filter.Container
- Apply `ValidateK8sName` to `filter.Container` when it overrides the URL parameter, rejecting invalid names with an error message over the WebSocket.
- **Effort:** Low — add one validation call.
- **Risk:** Low.

## Acceptance Criteria

- [ ] Container name from filter messages is validated against `ValidateK8sName`
- [ ] Invalid container names are rejected with an appropriate error message
- [ ] Valid container name overrides continue to work

## Work Log

- 2026-03-22: Created from Phase 4A code review.
