---
status: pending
priority: p2
issue_id: "087"
tags: [code-review, frontend, security, websocket]
dependencies: []
---

# WS Proxy Path Too Permissive — Should Allowlist

## Problem Statement
The WebSocket proxy in `routes/ws/[...path].ts` accepts any path after `/ws/` and forwards it to the backend. It should allowlist known WebSocket endpoints (`v1/ws/resources`, `v1/ws/logs/*`, etc.) to prevent unintended proxying.

## Findings
- Current validation only checks for path traversal (`..`, `%2e`)
- Any path like `/ws/v1/admin/secret-endpoint` would be proxied
- Should match the same defense-in-depth approach as the BFF REST proxy

## Proposed Solutions

### Option A: Allowlist known WS paths (v1/ws/resources, v1/ws/logs/*, v1/ws/exec/*)
- **Pros:** Defense-in-depth, prevents access to unintended backend WS endpoints
- **Cons:** Must update allowlist when new WS endpoints are added
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/routes/ws/[...path].ts`

## Acceptance Criteria
- [ ] Only known WebSocket endpoint patterns are proxied
- [ ] Unknown paths return 404 without attempting backend connection
