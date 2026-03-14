---
status: pending
priority: p1
issue_id: "151"
tags: [code-review, concurrency, step-10]
dependencies: []
---

# Thread-Unsafe snapshotCheck/snapshotAvail Data Race in Storage Handler

## Problem Statement
The `Handler` struct in `storage/handler.go` uses two plain `bool` fields (`snapshotCheck`, `snapshotAvail`) that are read and written from concurrent HTTP handler goroutines without synchronization. This is a data race that `go test -race` would flag. Multiple concurrent requests can redundantly perform the CRD discovery check.

## Findings
- **Agents**: security-sentinel (HIGH-03), architecture-strategist (P1), performance-oracle (Finding 5), data-integrity-guardian (P2-FINDING-5), pattern-recognition-specialist (P1)
- **Location**: `backend/internal/storage/handler.go:23-24` (field declarations), `handler.go:162-184` (checkSnapshotCRDs)
- **Evidence**: Plain `bool` fields accessed from goroutines without mutex or atomic. The networking package correctly uses `sync.RWMutex` for its cached state.

## Proposed Solutions

### Option A: Use sync.Once (simplest)
- Replace the two booleans with a `sync.Once` and a single `snapshotAvail` result
- `sync.Once` guarantees exactly-once execution and thread safety
- **Pros**: Simplest, idiomatic Go, zero contention after first check
- **Cons**: Cannot re-check if CRDs are installed later (permanently cached)
- **Effort**: Small
- **Risk**: Low

### Option B: Use atomic.Bool pair
- Replace `bool` with `atomic.Bool` for lock-free thread safety
- **Pros**: Thread-safe, allows future invalidation
- **Cons**: Slightly more complex than sync.Once
- **Effort**: Small
- **Risk**: Low

## Recommended Action
Option A — `sync.Once` is the simplest correct fix. Re-checking can be added later if needed (see todo 158).

## Technical Details
- **Affected files**: `backend/internal/storage/handler.go`

## Acceptance Criteria
- [ ] `go test -race ./internal/storage/...` passes
- [ ] CRD check runs exactly once
- [ ] Concurrent requests do not race

## Work Log
- 2026-03-14: Identified by 5 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
