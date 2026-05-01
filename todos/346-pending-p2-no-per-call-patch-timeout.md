---
name: Per-target Patch has no per-call timeout — wedged apiserver pins worker indefinitely
status: pending
priority: p2
issue_id: 346
tags: [code-review, eso, phase-e, reliability, safe-auto]
dependencies: []
---

## Problem Statement

`patchForceSync` uses the worker's parent ctx with no `WithTimeout` wrap. If kube-apiserver is unresponsive (network partition, etcd lockup), each Patch blocks until the underlying TCP timeout fires (often 60s+).

Across 5000 targets, this stretches one bad job to hours. Combined with the single-worker design, it head-of-line blocks every other queued bulk job for that duration.

The handler-level `fetchTimeout = 10s` already exists for SA-mode list calls in `handler.go:37`. Phase E should use a similar bound for individual Patches.

## Findings

- reliability reviewer (rel-5 medium conf 0.80)

**Affected files:**
- `backend/internal/externalsecrets/bulk_worker.go:143-166` — worker patch loop
- `backend/internal/externalsecrets/actions.go:217-249` — patchForceSync helper

## Proposed Solutions

### Option A — wrap each Get+Patch in WithTimeout (recommended)

```go
const perPatchTimeout = 10 * time.Second

func (h *Handler) patchForceSync(ctx context.Context, client dynamic.Interface, ns, name string) (string, error) {
    ctx, cancel := context.WithTimeout(ctx, perPatchTimeout)
    defer cancel()
    // ... existing Get + in-flight check + Patch
}
```

Combined with retry policy from #343, this bounds the wall-clock blast radius of a degraded apiserver (3 retries × 10s = 30s worst-case per target instead of multiple minutes).

### Option B — per-job overall budget

Wrap the worker's processJob in a budget like `targetCount * (interCallDelay + perPatchTimeout)` and abort early. More complex; harder to tune.

**Recommendation:** Option A.

## Acceptance Criteria

- [ ] Each Get+Patch in patchForceSync runs under a 10-second timeout.
- [ ] Test: synthetic apiserver hang causes the patch to return `context.DeadlineExceeded` after 10s, not block indefinitely.
- [ ] When combined with #343's retry helper, total per-target time is bounded at ~30s worst case.
- [ ] `inFlightWindow` is unaffected (still 30s on `status.refreshTime`).
