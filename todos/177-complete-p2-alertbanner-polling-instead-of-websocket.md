---
status: pending
priority: p2
issue_id: "177"
tags: [code-review, pattern-consistency, performance, step-11]
dependencies: ["164"]
---

# AlertBanner Uses 30s Polling Instead of WebSocket

## Problem Statement
The AlertBanner polls `GET /v1/alerts` every 30 seconds via `setInterval`, despite the PR adding "alerts" to WS `allowedKinds` and broadcasting events from the webhook handler. This creates N requests/30s for N users and adds up to 30s latency for alert visibility. The existing ResourceTable uses WebSocket for real-time updates.

## Findings
- **Source**: Pattern Recognition review (Finding 4), Performance review (Finding 6), Architecture review
- **Location**: `frontend/islands/AlertBanner.tsx:32`

## Proposed Solutions
### Option A: Subscribe to WS kind "alerts" (Recommended)
Use the existing `lib/ws.ts` subscribe mechanism for kind "alerts". Fetch initial state via REST, then update via WS events.
- **Effort**: Medium (depends on fixing todo 164 first)
- **Risk**: Low

## Resources
- PR: #17
