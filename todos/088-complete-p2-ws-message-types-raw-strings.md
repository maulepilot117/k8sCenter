---
status: pending
priority: p2
issue_id: "088"
tags: [code-review, frontend, websocket, maintainability]
dependencies: []
---

# Frontend WebSocket Message Types Are Raw Strings

## Problem Statement
WebSocket message types (subscribe, unsubscribe, auth_ok, event, error, etc.) are hardcoded as string literals throughout `ws.ts` and `ResourceTable.tsx`. Typos won't be caught, and adding new message types requires searching for all string occurrences.

## Findings
- `ws.ts` uses string literals: `"subscribe"`, `"auth_ok"`, `"error"`, `"event"`
- `ResourceTable.tsx` uses `"ADDED"`, `"MODIFIED"`, `"DELETED"`, `"RBAC_DENIED"`
- No shared constants or enum for message types
- Typo in a message type string would silently fail

## Proposed Solutions

### Option A: Define constants for all WS message types in ws.ts and export
- **Pros:** Type-safe, centralized, IDE autocomplete
- **Cons:** Minor refactor
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/lib/ws.ts`, `frontend/islands/ResourceTable.tsx`

## Acceptance Criteria
- [ ] All WS message types use named constants
- [ ] No string literal message types in application code
