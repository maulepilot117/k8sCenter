---
status: pending
priority: p2
issue_id: "084"
tags: [code-review, frontend, websocket, performance]
dependencies: []
---

# No Debouncing/Batching of WebSocket Events on Frontend

## Problem Statement
Each WebSocket event triggers an immediate signal update in `ResourceTable.tsx`, causing a Preact re-render per event. During high-churn scenarios (deployment rollout, node drain), dozens of events per second trigger dozens of re-renders, degrading UI performance.

## Findings
- `ResourceTable.tsx:86-108` — each WS event directly mutates `items.value`
- Each mutation triggers `useComputed` recalculation (filter + sort) and full re-render
- During a 100-pod deployment rollout, ~200 events (ADDED+MODIFIED) fire in seconds
- No batching, debouncing, or requestAnimationFrame gating

## Proposed Solutions

### Option A: Batch WS events and apply in requestAnimationFrame
- **Pros:** Renders at most once per frame (~60fps), smooth UX even under high churn
- **Cons:** Slight delay (16ms) before events appear
- **Effort:** Small
- **Risk:** Low

## Technical Details
- **Affected files:** `frontend/islands/ResourceTable.tsx`, potentially `frontend/lib/ws.ts`

## Acceptance Criteria
- [ ] Multiple rapid WS events are batched into a single signal update
- [ ] UI remains responsive during high-churn scenarios (100+ events/sec)
