---
name: Phase E 409 responses deviate from canonical error envelope by injecting endpoint-specific keys
status: pending
priority: p2
issue_id: 350
tags: [code-review, eso, phase-e, api-contract]
dependencies: []
---

## Problem Statement

The project canonical error shape (`pkg/api/types.go APIError`) is `{error: {code, message, detail}}`, produced by `httputil.WriteError`. Phase E adds three 409 responses that hand-roll JSON to inject extra keys at the same level under `error`:

1. `HandleForceSyncExternalSecret` 409 — `{error: {code, message, reason}}`
2. `handleBulkRefresh` 409 scope_changed — `{error: {code, message, reason, added, removed}}`
3. `handleBulkRefresh` 409 active_job_exists — `{error: {code, message, reason, jobId}}`

This is the first endpoint family in the codebase to extend the error envelope with arbitrary keys. Frontend consumes the extension via `ApiError.body.error.<key>` with `as string | undefined` casts (#352 covers the TS side).

Risks:
- Future endpoints will copy the pattern inconsistently.
- The shared TS `ApiError` class has no typed accessor — every consumer narrows by hand.
- A breaking change to the error shape (e.g., moving `reason` out of error) would silently fail TS type-checking because the extension keys are typed `Record<string, unknown>`.

## Findings

- api-contract reviewer (api-contract-1 medium, conf 0.90)

**Affected files:**
- `backend/internal/externalsecrets/actions.go` — 409 already_refreshing
- `backend/internal/externalsecrets/bulk.go` — 409 scope_changed + 409 active_job_exists
- `backend/pkg/api/types.go` — canonical APIError struct
- `backend/internal/httputil` — WriteError helper

## Proposed Solutions

### Option A — extend pkg/api.APIError to carry typed extensions (recommended)

```go
// pkg/api/types.go
type APIError struct {
    Code    int            `json:"code"`
    Message string         `json:"message"`
    Detail  string         `json:"detail,omitempty"`
    Reason  string         `json:"reason,omitempty"`           // NEW
    Extra   map[string]any `json:"extra,omitempty"`            // NEW — for added/removed/jobId
}
```

Then add `httputil.WriteErrorWithExtras(w, status, code, message, reason, extras)` that all four 409 sites use uniformly.

Frontend extends `ApiError` to expose `reason: string | undefined` directly (no body.error access for the common case).

### Option B — move extensions to a sibling data object on the 409

```json
{
  "error": {"code": 409, "message": "scope changed", "reason": "scope_changed"},
  "data": {"added": [...], "removed": [...]}
}
```

Keeps `error` clean. Requires frontend to read both `error` and `data` on 409, which is unusual.

### Option C — accept the deviation; document it

Less work but locks in inconsistency.

**Recommendation:** Option A. Promotes the pattern to a first-class API primitive that future endpoints can reuse cleanly.

## Acceptance Criteria

- [ ] `pkg/api.APIError` carries `reason` and `extra` fields.
- [ ] `httputil` exports `WriteErrorWithExtras` (or equivalent).
- [ ] All three 409 responses use the helper.
- [ ] Frontend `ApiError` exposes `reason` directly (typed `string | undefined`); body.error access still available for `extras`.
- [ ] Test: each 409 round-trips through ApiError correctly.
- [ ] Existing endpoints unaffected (additive change to APIError).
