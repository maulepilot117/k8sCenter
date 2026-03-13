---
status: pending
priority: p3
issue_id: "095"
tags: [code-review, frontend, data-integrity]
dependencies: []
---

# No resourceVersion Tracking in Frontend Event Handling

## Problem Statement
When processing WebSocket MODIFIED events, the frontend replaces objects by UID without checking resourceVersion. If events arrive out of order (possible with reconnection), an older version could overwrite a newer one.

## Findings
- `ResourceTable.tsx:94-98` replaces by UID match without version check
- Out-of-order delivery is unlikely but possible during reconnection
- Standard k8s practice is to compare resourceVersion before applying updates

## Proposed Solutions
### Option A: Compare resourceVersion before applying MODIFIED events
- **Effort:** Small
- **Risk:** Low
