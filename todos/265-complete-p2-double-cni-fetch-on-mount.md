---
status: pending
priority: p2
issue_id: 265
tags: [code-review, performance, frontend, pr-157]
dependencies: []
---

# Double CNI Fetch on Mount

## Problem Statement

When the NetworkOverview page loads, two separate components both fetch `/v1/networking/cni`:
1. `NetworkOverview.tsx:22` — to determine if Cilium (for showing subsystem islands)
2. `CniOverview.tsx:63` — to get full CNI info for the Overview card

This is 2 redundant API calls on every page load.

## Findings

**Location:**
- `frontend/islands/NetworkOverview.tsx:22` — `apiGet<CNIInfo>("/v1/networking/cni")`
- `frontend/islands/CniOverview.tsx:63` — `fetchCNI()` → `apiGet<CNIInfo>("/v1/networking/cni")`

## Proposed Solutions

### Option A: Pass CNI name from NetworkOverview to children as prop
**Pros:** Single fetch, children don't need their own
**Cons:** CniOverview still needs full info for details display
**Effort:** Medium
**Risk:** Low

### Option B: Accept as-is (both are cached on backend anyway)
**Pros:** No change needed, backend responds quickly
**Cons:** Unnecessary network request
**Effort:** None
**Risk:** None

## Recommended Action

Option B for now — low impact. Option A when optimizing.

## Acceptance Criteria

- [ ] Decision documented on whether to consolidate

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Low priority, backend caches CNI detection |
