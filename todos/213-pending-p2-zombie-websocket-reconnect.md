---
status: pending
priority: p2
issue_id: "213"
tags: [code-review, frontend-races, security]
dependencies: []
---

# disconnectWS cannot cancel in-flight reconnect — zombie WebSocket after logout

## Problem Statement

In `ws.ts`, `scheduleReconnect` uses `setTimeout` with an async callback that calls
`refreshTokenForWS()` then `connectWS()`. If `disconnectWS()` is called while
`refreshTokenForWS()` is in-flight (timeout already fired), `clearTimeout` is a
no-op. The promise resolves and `connectWS()` creates a new WebSocket connection
after the user explicitly logged out.

**Location:** `frontend/lib/ws.ts:218-239` (pre-existing code, not new to this PR)

## Findings

- This is pre-existing code, not introduced by PR #46
- The reconnect timer callback captures no cancellation token
- After logout, a zombie WebSocket may authenticate with a stale token
- Security-adjacent: stale auth token used after explicit logout

## Proposed Solutions

### Option A: Cancellation token pattern
```typescript
let cancelToken = { canceled: false };
function scheduleReconnect() {
  const token = cancelToken;
  reconnectTimer = setTimeout(async () => {
    if (token.canceled) return;
    await refreshTokenForWS();
    if (token.canceled) return;
    connectWS();
  }, delay);
}
function disconnectWS() {
  cancelToken.canceled = true;
  cancelToken = { canceled: false };
  // ... rest of cleanup
}
```
- **Pros:** Simple, no new deps, ~8 lines
- **Cons:** None
- **Effort:** Small
- **Risk:** Low

## Recommended Action

Option A — add cancellation token. Fix separately from this PR since it's pre-existing.

## Acceptance Criteria

- [ ] Logout during reconnect backoff does not create zombie WebSocket
- [ ] No stale token authentication after explicit disconnect

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found by frontend-races reviewer during PR #46 review (pre-existing) |
