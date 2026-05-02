---
name: Phase E wire types not covered by Go-TS hash test — drift guard incomplete
status: completed
priority: p2
issue_id: 351
tags: [code-review, eso, phase-e, type-safety]
dependencies: []
---

## Problem Statement

Phase A's `TestExportedTypeShapeStability` (in `backend/internal/externalsecrets/types_hash_test.go`) hashes the exported field set of each Go wire type and pins the hash. A future Go-side rename forces a TS update — the documented Go-TS drift guard.

Phase E adds 5 new wire types with **no hash entry**:
- `BulkScopeResponse`
- `BulkScopeTarget`
- `BulkNamespaceCount`
- `BulkRefreshOutcome`
- `BulkRefreshJob` — *currently constructed via `bulkJobResponse() map[string]any`, dodging the hash test entirely even if added*

A Go-side rename of `BulkScopeResponse.VisibleCount` would compile and pass tests while silently breaking the dialog.

## Findings

- api-contract reviewer (api-contract-2 medium, conf 0.95)
- api-contract reviewer (api-contract-3 low, conf 0.85 — bulkJobResponse is map[string]any)

**Affected files:**
- `backend/internal/externalsecrets/types_hash_test.go` — needs entries for new types
- `backend/internal/externalsecrets/bulk.go:bulkJobResponse` — should return a typed struct
- `frontend/lib/eso-types.ts` — TS-side mirror

## Proposed Solutions

### Option A — typed BulkRefreshJobResponse struct + hash entries (recommended)

1. Add a typed struct for the GET job response:

```go
type BulkRefreshJobResponse struct {
    JobID       uuid.UUID                 `json:"jobId"`
    ClusterID   string                    `json:"clusterId"`
    RequestedBy string                    `json:"requestedBy"`
    Action      store.BulkRefreshAction   `json:"action"`
    ScopeTarget string                    `json:"scopeTarget"`
    TargetCount int                       `json:"targetCount"`
    CreatedAt   time.Time                 `json:"createdAt"`
    CompletedAt *time.Time                `json:"completedAt,omitempty"`
    Succeeded   []string                  `json:"succeeded"`
    Failed      []store.BulkRefreshOutcome `json:"failed"`
    Skipped     []store.BulkRefreshOutcome `json:"skipped"`
}

func toBulkRefreshJobResponse(j *store.ESOBulkRefreshJob) BulkRefreshJobResponse { ... }
```

`HandleGetBulkRefreshJob` returns this via `httputil.WriteData`.

2. Extend `types_hash_test.go` to cover the 5 new types.

3. Verify TS `BulkRefreshJob` interface in `eso-types.ts` is byte-equivalent.

### Option B — hash entries only, leave map[string]any

Half-fix; hash test wouldn't catch a typo in the map literal because there's no Go type to hash.

**Recommendation:** Option A. Removes the loophole entirely.

## Acceptance Criteria

- [ ] `BulkRefreshJobResponse` typed struct introduced; `bulkJobResponse` deleted.
- [ ] `TestExportedTypeShapeStability` extended with hashes for `BulkScopeResponse`, `BulkScopeTarget`, `BulkNamespaceCount`, `BulkRefreshOutcome`, `BulkRefreshJobResponse`.
- [ ] `frontend/lib/eso-types.ts` matches; a smoke test confirms a sample wire payload deserializes against the TS interface without `any` casts.
- [ ] Renaming any field in any of the 5 types causes the hash test to fail (verified manually before merge).
