---
status: pending
priority: p3
issue_id: "093"
tags: [code-review, frontend, performance]
dependencies: []
---

# Column Render Functions Create Objects Per Call

## Problem Statement
Several column render functions in `resource-columns.ts` create new arrays/objects on every call (e.g., `badge()` creates a new VNode per render). With 100 rows x 7 columns, that's 700 object allocations per render cycle.

## Findings
- `badge()` calls `h("span", {...}, text)` creating a new VNode each time
- Filter/map operations in port columns create intermediate arrays
- Impact is minimal for typical table sizes but could matter at scale

## Proposed Solutions
### Option A: Accept as-is — Preact's VDOM diffing handles this efficiently
- **Effort:** None
- **Risk:** None — premature optimization concern
