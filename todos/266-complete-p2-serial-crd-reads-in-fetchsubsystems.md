---
status: pending
priority: p2
issue_id: 266
tags: [code-review, performance, networking, pr-157]
dependencies: []
---

# Serial CRD Reads in fetchSubsystems Could Be Parallel

## Problem Statement

`fetchSubsystems()` calls `readCiliumNodes()` and `aggregateEndpoints()` serially. These are independent API calls that could be parallelized with `sync.WaitGroup` or `errgroup`, reducing latency by ~50%.

## Findings

**Location:** `backend/internal/networking/handler.go:448-497`

```go
nodes, err := readCiliumNodes(ctx, dynClient)  // serial
// ... use nodes ...
endpoints, err := aggregateEndpoints(ctx, dynClient)  // serial, independent
```

The dashboard-summary endpoint already uses `sync.WaitGroup` for parallel fetches. This should follow the same pattern.

## Proposed Solutions

### Option A: Use errgroup for parallel fetch
**Pros:** ~50% latency reduction, consistent with dashboard pattern
**Cons:** Slightly more complex code
**Effort:** Small
**Risk:** Low

## Acceptance Criteria

- [ ] readCiliumNodes and aggregateEndpoints run concurrently
- [ ] Errors from either are handled correctly
- [ ] Overall latency reduced

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Dashboard-summary uses WaitGroup pattern |
