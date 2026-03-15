---
status: pending
priority: p2
issue_id: "173"
tags: [code-review, data-integrity, step-11]
dependencies: []
---

# Pagination Cursor Can Skip Entries on Timestamp Collision

## Problem Statement
The cursor is based on `ReceivedAt.UnixNano()`. If two events share the same nanosecond timestamp, pagination skips one. The `nextID` auto-incrementing field exists but is unused in the cursor.

## Findings
- **Source**: Data Integrity review (P1-2)
- **Location**: `backend/internal/alerting/store.go:144,179`

## Proposed Solutions
### Option A: Composite cursor using receivedAt + ID (Recommended)
Encode both `receivedAt` nanos and `id` in the cursor. Use both for comparison.
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
