---
status: pending
priority: p2
issue_id: "206"
tags: [code-review, architecture, validation]
dependencies: []
---

# Scale validation mismatch: frontend 0-100 vs backend 0-1000

## Problem Statement

The frontend scale dialog uses `max="100"` on the number input, but the backend
validates replicas in the range 0-1000. Users who bypass the frontend (dev tools,
API direct) can scale up to 1000, but the UI silently clips at 100. This is
confusing UX — either the frontend limit should match the backend, or both should
agree on a single maximum.

**Location:** `frontend/islands/ResourceTable.tsx:619` and `backend/internal/k8s/resources/deployments.go:197`

## Findings

- Backend: `if req.Replicas < 0 || req.Replicas > 1000` (deployments.go:197, statefulsets.go:174)
- Frontend: `<input type="number" min="0" max="100" ...>` (ResourceTable.tsx:619)
- The HTML `max` attribute is advisory — the form still submits values >100

## Proposed Solutions

### Option A: Align frontend to 1000
- **Pros:** Consistent with backend, power users can scale high
- **Cons:** 1000 replicas is rarely needed in UI
- **Effort:** Small
- **Risk:** Low

### Option B: Keep 100 as frontend soft limit with backend as hard limit
- **Pros:** Good UX default, backend still protects
- **Cons:** Current behavior, no code change needed, but add a note/tooltip
- **Effort:** Small
- **Risk:** Low

## Recommended Action

Option A — align frontend max to 1000 for consistency.

## Acceptance Criteria

- [ ] Frontend scale input max matches backend validation limit
- [ ] Both Deployment and StatefulSet scale endpoints agree on the limit

## Work Log

| Date | Action | Notes |
|------|--------|-------|
| 2026-03-19 | Created | Found during PR #46 code review |
