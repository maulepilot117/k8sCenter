---
status: pending
priority: p3
issue_id: "096"
tags: [code-review, security, websocket, deployment]
dependencies: []
---

# JWT Sent Over Unencrypted WebSocket — No TLS Enforcement

## Problem Statement
The WebSocket auth protocol sends the JWT as the first message in plaintext. In development (no TLS), this exposes the token to network sniffing. The code doesn't enforce or check for WSS (secure WebSocket) in production.

## Findings
- `ws.ts` constructs WS URL without checking protocol (ws:// vs wss://)
- Auth token sent as first message in plaintext over the WebSocket
- CLAUDE.md mandates "TLS everywhere" — should enforce wss:// in production
- Development mode (localhost) is acceptable without TLS

## Proposed Solutions
### Option A: Check for wss:// in production, warn or refuse ws:// connections
- **Effort:** Small
- **Risk:** Low
