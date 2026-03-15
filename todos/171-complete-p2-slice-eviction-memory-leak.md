---
status: pending
priority: p2
issue_id: "171"
tags: [code-review, performance, memory-leak, step-11]
dependencies: []
---

# History Slice Eviction Retains Old Backing Array

## Problem Statement
`s.history = s.history[len(s.history)-maxHistoryEntries:]` creates a new slice header but the old backing array (with evicted entries) is never freed. Over time with frequent writes, the backing array grows unboundedly while only 10K entries are "visible".

## Findings
- **Source**: Performance review (Finding 7)
- **Location**: `backend/internal/alerting/store.go:87-93,210-214`

## Proposed Solutions
### Option A: Copy to fresh slice on eviction (Recommended)
```go
trimmed := make([]AlertEvent, maxHistoryEntries)
copy(trimmed, s.history[len(s.history)-maxHistoryEntries:])
s.history = trimmed
```
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
