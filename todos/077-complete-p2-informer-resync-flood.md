---
status: pending
priority: p2
issue_id: "077"
tags: [code-review, backend, websocket, performance]
dependencies: []
---

# Informer Resync Floods WebSocket Every 5 Minutes

## Problem Statement
Informers are configured with a 5-minute resync period. On each resync, the informer fires update events for every cached object (even if unchanged). Without resourceVersion comparison, the hub broadcasts duplicate MODIFIED events for all resources to all subscribed clients every 5 minutes.

## Findings
- `informers.go` sets 5-minute resync on all informer factories
- Update handler in informer event callbacks doesn't compare old vs. new resourceVersion
- Hub broadcasts all events without deduplication
- With 500 pods across all namespaces, that's 500 MODIFIED events every 5 minutes per subscribed client

## Proposed Solutions

### Option A: Compare resourceVersion in the update handler, skip if unchanged
- **Pros:** Eliminates resync noise at the source, minimal change
- **Cons:** None significant
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `backend/internal/k8s/informers.go`

## Acceptance Criteria
- [ ] Resync events with unchanged resourceVersion are suppressed
- [ ] Genuine modifications are still broadcast
