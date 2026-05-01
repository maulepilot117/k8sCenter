---
name: Empty POST body bypasses scope-pin — server re-resolves scope freshly without operator confirmation
status: pending
priority: p1
issue_id: 340
tags: [code-review, eso, phase-e, correctness, design-decision]
dependencies: []
---

## Problem Statement

`handleBulkRefresh` in `bulk.go:319-341` only validates `body.TargetUIDs` against the freshly-resolved scope when `r.ContentLength > 0` AND `len(body.TargetUIDs) > 0`. A POST with no body (or with `targetUIDs: []`) falls through to "use whatever scope resolves now" semantics.

**Race scenario:**
1. T=0: operator GETs `/refresh-scope` → server returns 47 ESes
2. T=0.5s: another admin (or controller) creates a 48th ES referencing the same store
3. T=1s: operator clicks Confirm; client POSTs an empty body
4. Server re-resolves scope → 48 ESes → enqueues job patching all 48

The 48th ES — which the operator never saw, never confirmed, and may have explicitly chosen to skip — gets force-synced anyway.

**Why this matters:** the scope-pin contract was the central correctness guard the feature was designed around. The plan documented `compareUIDs` as the protection against this exact race (`bulk.go:668-670` comment). When a caller skips the pin, the protection collapses silently.

The current frontend dialog passes `targetUIDs` from the scope response, so this is mostly a direct-API-call risk. But: there's nothing in the route/handler signature stopping a future frontend regression, and an agent calling the endpoint with no body would also bypass.

## Findings

- adversarial reviewer (severity high, conf 0.85, finding adv-1)

**Affected files:**
- `backend/internal/externalsecrets/bulk.go:319-341`

**Plan reference:** §688 documents the optional-targetUIDs design, but doesn't specify what should happen when omitted. Implementation chose "permissive — re-resolve."

## Proposed Solutions

### Option A — require non-empty TargetUIDs (recommended)

Reject any POST without explicit pin:

```go
var body bulkRefreshRequest
if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
    httputil.WriteError(w, http.StatusBadRequest, "invalid body", "")
    return
}
if len(body.TargetUIDs) == 0 {
    httputil.WriteError(w, http.StatusBadRequest,
        "targetUIDs required — call refresh-scope first", "")
    return
}
```

This forces the two-step flow: GET refresh-scope → POST refresh-all with `{targetUIDs: [...]}`. Aligns with the design intent.

**Trade-off:** breaks any existing API consumer that POSTs without a body. Today's only consumer is the dialog which already passes `targetUIDs`, so this is non-breaking in practice.

### Option B — document the loophole as opt-in

Keep current behavior; document that omitting `targetUIDs` means "use latest scope." Add a frontend test that confirms the dialog always sends `targetUIDs`. Less safe, but preserves agent ergonomics for callers that genuinely want the loose semantics.

## Acceptance Criteria

- [ ] Decision documented: A or B (or some hybrid like "empty body returns 400 unless `?confirm=latest` is passed").
- [ ] Test: POST with no body + Option A returns 400 with reason "targetUIDs required".
- [ ] Test: POST with `{targetUIDs: []}` returns 400 (covers explicit-empty case).
- [ ] Frontend dialog continues to work end-to-end after the change.
- [ ] Plan §688 updated to reflect the chosen semantics so future readers don't re-discover this.
