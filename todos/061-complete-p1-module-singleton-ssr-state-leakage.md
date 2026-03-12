---
status: complete
priority: p1
issue_id: "061"
tags: [code-review, frontend, security, architecture]
dependencies: []
---

# Module-Level Singletons Risk Cross-Request State Leakage in SSR

## Problem Statement
Several `lib/` modules use module-level variables (`accessToken`, `refreshPromise`, `currentUser` signal, `toasts` signal) that are process-global singletons in the Deno server. If any SSR code path ever imports and reads these, it would see state from a different user's request — a security vulnerability.

Currently safe because islands use `IS_BROWSER` guards, but this is a convention, not an architectural boundary. A future developer importing `useAuth()` in a server component would silently leak user state across requests.

## Findings
- `frontend/lib/api.ts:5-8` — `accessToken` and `refreshPromise` are module-level
- `frontend/lib/auth.ts:6-7` — `currentUser` and `isLoading` are module-level signals
- `frontend/lib/ws.ts:22-29` — WebSocket state is module-level
- `frontend/components/ui/Toast.tsx:12` — `toasts` signal is module-level
- No lint rule or import restriction prevents server-side imports of these modules

Flagged by: Architecture Strategist (P1-3)

## Proposed Solutions

### Option A: Add prominent comments + move to `islands/lib/` directory
- **Pros**: Explicit boundary, easy to enforce in review
- **Cons**: Slightly unusual directory structure
- **Effort**: Small
- **Risk**: Low

### Option B: Guard all exports with IS_BROWSER runtime checks
- **Pros**: Fails fast if misused server-side
- **Cons**: Runtime check overhead, doesn't prevent import
- **Effort**: Small
- **Risk**: Low

## Technical Details
- **Affected files**: `frontend/lib/api.ts`, `frontend/lib/auth.ts`, `frontend/lib/ws.ts`, `frontend/components/ui/Toast.tsx`

## Acceptance Criteria
- [ ] Client-only modules clearly marked (directory or comments)
- [ ] Server-side import of auth/api state would fail or warn
- [ ] No cross-request state leakage possible
