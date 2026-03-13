---
status: pending
priority: p3
issue_id: "094"
tags: [code-review, frontend, performance]
dependencies: []
---

# age() Function Recalculates Date.now() Per Cell

## Problem Statement
The `age()` function in `resource-columns.ts` calls `Date.now()` for every cell render. In a table with 100 rows, `Date.now()` is called 100 times per render cycle instead of once.

## Findings
- `age()` at line 78 calls `Date.now()` each invocation
- Millisecond precision isn't needed for "5d" / "3h" display
- Could capture `Date.now()` once per render cycle

## Proposed Solutions
### Option A: Accept as-is — Date.now() is trivially fast
- **Effort:** None
- **Risk:** None — nanosecond operation, not worth optimizing
