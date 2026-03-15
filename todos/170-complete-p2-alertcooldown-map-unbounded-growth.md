---
status: pending
priority: p2
issue_id: "170"
tags: [code-review, performance, memory-leak, step-11]
dependencies: []
---

# alertCooldown Map Grows Without Bound — Memory Leak

## Problem Statement
The `alertCooldown` map in the notifier stores `fingerprint -> time.Time` entries that are never cleaned up. In clusters with high label cardinality (pod-level alerts), this grows indefinitely (~150KB/day low cardinality, up to 15MB/day high cardinality).

## Findings
- **Source**: Performance review (Finding 1)
- **Location**: `backend/internal/alerting/notifier.go:73,197-208`

## Proposed Solutions
### Option A: Periodic cleanup in Run() loop (Recommended)
Add a ticker in `Run()` that sweeps entries older than `cooldownDuration` every 15 minutes.
- **Effort**: Small
- **Risk**: None

## Resources
- PR: #17
