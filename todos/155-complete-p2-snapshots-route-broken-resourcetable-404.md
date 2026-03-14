---
status: pending
priority: p2
issue_id: "155"
tags: [code-review, bug, step-10]
dependencies: []
---

# Snapshots Route Page Broken — ResourceTable for volumesnapshots Returns 404

## Problem Statement
`frontend/routes/storage/snapshots.tsx` renders `<ResourceTable kind="volumesnapshots">` which fetches from `/api/v1/resources/volumesnapshots`. This endpoint does not exist — there is no resource handler registered for `volumesnapshots`. The actual snapshot data is served from the dedicated `/api/v1/storage/snapshots` endpoint. This page will produce 404 errors at runtime.

## Findings
- **Agents**: architecture-strategist (P2), pattern-recognition-specialist (P2)
- **Location**: `frontend/routes/storage/snapshots.tsx:14`

## Proposed Solutions
Replace the `ResourceTable` usage with a custom island that fetches from `/v1/storage/snapshots` and renders the snapshot data, similar to how `StorageOverview` displays drivers and classes.

- **Effort**: Medium
- **Risk**: Low

## Acceptance Criteria
- [ ] Snapshots page loads without 404 errors
- [ ] Displays VolumeSnapshot data from `/v1/storage/snapshots`
- [ ] Shows empty state when no snapshots or no CRDs

## Work Log
- 2026-03-14: Identified by 2 review agents

## Resources
- PR #16: https://github.com/maulepilot117/k8s_management/pull/16
