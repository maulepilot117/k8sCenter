---
status: pending
priority: p1
issue_id: "107"
tags: [code-review, bug, step-7]
dependencies: []
---

# Apply Response Type Mismatch Between Backend and Frontend

## Problem Statement

Frontend `ApplyResponse` expects `summary.total/created/configured/unchanged/failed` but backend returns flat `Applied` and `Failed` fields. The `ApplyResults` component destructures `response.summary` which will be `undefined`, causing the results display to break silently.

## Findings

- **Backend**: `backend/internal/yaml/applier.go` lines 32-36 — `ApplyResponse` has flat `Applied` and `Failed` fields, no `Summary` struct.
- **Frontend**: `frontend/islands/YamlApplyPage.tsx` lines 20-27 — `ApplyResults` destructures `response.summary` which does not exist in the backend response shape.

## Recommendation

Either update the backend `ApplyResponse` to include a `Summary` struct with `total`, `created`, `configured`, `unchanged`, and `failed` counts, or update the frontend to compute summary statistics from the `Applied` and `Failed` results arrays.
