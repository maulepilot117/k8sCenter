---
name: TestBulkWorker_MixedOutcomes bypasses processJob — worker switch branches untested
status: pending
priority: p2
issue_id: 352
tags: [code-review, eso, phase-e, testing, false-confidence]
dependencies: []
---

## Problem Statement

`TestBulkWorker_MixedOutcomes` in `bulk_test.go:1612-1716` reimplements the classify-and-append loop inline rather than driving the real `BulkWorker.processJob`. The test comment is explicit:

> "Drive the per-target loop directly to avoid w.k8s dependency"

Because tests don't wire a real `ClientFactory`, the test reaches into the handler's `dynForUserOverride` and runs its own switch over `errors.Is(err, errAlreadyRefreshing)` / `apierrors.IsForbidden(err)` / etc.

**What this leaves uncovered:**
- `IsConflict` → `optimistic_lock` branch (plan AE5 #6 explicitly named)
- `interCallDelay` throttle (200ms between targets)
- `classifyPatchError` fallback (the path #338 fixes)
- Ctx-cancel mid-loop (the path #345 fixes)
- Post-loop `Complete` + `Get` + `auditBulkJob` + `InvalidateCache`
- Real `worker.Enqueue` → run() loop

We have green tests but the actual production worker path has no end-to-end coverage.

## Findings

- testing reviewer (T-1 P1, conf 0.92)
- maintainability reviewer (echoed)
- learnings-researcher (recommendation)

**Affected files:**
- `backend/internal/externalsecrets/bulk_test.go:1612-1716`
- `backend/internal/externalsecrets/bulk_worker.go:processJob`

## Proposed Solutions

### Option A — refactor processJob to accept dynamic.Interface (recommended)

Decouple processJob from `K8sClient.DynamicClientForUser`:

```go
type DynForUserFunc func(username string, groups []string) (dynamic.Interface, error)

type BulkWorker struct {
    // ...
    dynForUser DynForUserFunc  // production: w.k8s.DynamicClientForUser; tests: closure
}

func (w *BulkWorker) processJob(ctx context.Context, msg BulkJobMessage) {
    dynClient, err := w.dynForUser(msg.Username, msg.Groups)
    // ... rest unchanged
}
```

Test wires the same fake dynamic client used elsewhere; real worker loop drives the test.

### Option B — accept the gap; add separate integration test against kind/envtest

Skip the refactor; add a test that runs against a real (or fake-but-typed) kube-apiserver. Heavier infrastructure but tests the real wiring.

**Recommendation:** Option A. The seam is small and unlocks several pending tests (#352, #353, #354).

## Acceptance Criteria

- [ ] `processJob` accepts an injectable `dynForUser` function.
- [ ] `TestBulkWorker_MixedOutcomes` rewritten to drive `processJob` directly with a fake dynamic client.
- [ ] New test: `IsConflict` from the fake produces `failed: optimistic_lock`.
- [ ] New test: ctx-cancel mid-loop leaves `completed_at = NULL` (verifies orphan-reaper contract).
- [ ] New test: confirms `interCallDelay` is honored (use a controlled time source if available, or assert minimum elapsed).
- [ ] New test: `auditBulkJob` is called with the final state after the loop completes.
- [ ] No regression in existing test count.
