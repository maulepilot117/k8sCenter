---
status: pending
priority: p3
issue_id: "092"
tags: [code-review, frontend, websocket, security]
dependencies: []
---

# Unbounded Reconnect on Auth Failure

## Problem Statement
`ws.ts` reconnects with exponential backoff on any disconnection, including auth failures. If the JWT is permanently invalid (user revoked), the client will retry forever, generating unnecessary load.

## Findings
- No distinction between transient failures (network) and permanent failures (auth revoked)
- Backend close code 4001 (auth failure) should stop reconnection
- Current backoff maxes at 30s but never gives up

## Proposed Solutions
### Option A: Stop reconnecting on auth-related close codes (4001, 4003)
- **Effort:** Small
- **Risk:** Low
