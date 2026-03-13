---
status: pending
priority: p1
issue_id: "108"
tags: [code-review, bug, step-7]
dependencies: []
---

# Export Endpoint Content-Type Mismatch Causes JSON Parse Error

## Problem Statement

The export endpoint (`HandleExport`) returns `Content-Type: text/yaml` with raw YAML bytes, but the frontend calls `apiGet<string>()` which goes through `api()` that calls `res.json()` on success. This throws a JSON parse error because the response body is not valid JSON.

## Findings

- **Backend**: `backend/internal/yaml/handler.go` lines 245-248 — returns raw YAML with `Content-Type: text/yaml`.
- **Frontend**: `frontend/islands/ResourceDetail.tsx` lines 390-396 — uses `apiGet<string>()` which expects a JSON response.

## Recommendation

Either wrap the YAML in a JSON envelope on the backend (e.g., `{"data": "<yaml string>"}`), or use a raw `fetch` call on the frontend for the export endpoint that reads the response as text instead of JSON.
