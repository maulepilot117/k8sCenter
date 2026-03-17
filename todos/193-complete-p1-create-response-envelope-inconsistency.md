---
status: pending
priority: p1
issue_id: "193"
tags: [code-review, architecture, api]
dependencies: []
---

# Create Response Envelope Inconsistency

## Problem Statement

`HandleCreateCiliumPolicy` bypasses the standard `api.Response` envelope used by every other handler. It returns `map[string]any{"data": ..., "warnings": ...}` via `writeJSON` instead of using `writeCreated(w, created)`. This breaks the API contract and could cause frontend parsing issues in `lib/api.ts`.

## Findings

- **cilium.go:230-233**: Custom `writeJSON` with non-standard envelope
- Every other create handler uses `writeCreated(w, created)` which wraps in `api.Response{Data: created}`
- The `warnings` field is a novel concept not present in the `api.Response` struct
- Found by: Architecture, Pattern Recognition, Simplicity reviewers

## Proposed Solutions

### Option A: Use writeCreated, embed warnings in data
Return warnings inside the data field: `writeCreated(w, map[string]any{"resource": created, "warnings": warnings})`
- Pros: Keeps standard envelope
- Cons: Nests data one level deeper
- Effort: Small
- Risk: Low

### Option B: Use writeCreated, return warnings via response header
Set `X-Policy-Warnings` header with JSON-encoded warnings, use `writeCreated(w, created)` for body.
- Pros: Clean separation, standard envelope preserved
- Cons: Headers are less discoverable
- Effort: Small
- Risk: Low

## Acceptance Criteria
- [ ] Create endpoint returns standard `api.Response` envelope
- [ ] Frontend can parse create response consistently with other resources
- [ ] Warnings are still surfaced to the user

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `backend/internal/k8s/resources/cilium.go:230-233`
- Pattern: `backend/internal/k8s/resources/handler.go:156` (`writeCreated`)
