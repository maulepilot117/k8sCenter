---
status: pending
priority: p1
issue_id: "069"
tags: [code-review, backend, frontend, websocket, bug]
dependencies: []
---

# PVC Kind String Mismatch — Real-time Updates Silently Broken

## Problem Statement
The informer emits events with kind `"persistentvolumeclaims"` (set in informers.go) but the frontend subscribes to `"pvcs"` (the route kind). PVC real-time WebSocket updates are silently broken — no events are ever delivered to the PVC table.

## Findings
- `backend/internal/k8s/informers.go:202` registers PVC informer with kind string `"persistentvolumeclaims"`
- `frontend/routes/storage/pvcs.tsx` renders `<ResourceTable kind="pvcs" />`
- `frontend/lib/ws.ts` subscribes with `kind="pvcs"`
- Backend hub matches subscriptions by kind string — `"pvcs" !== "persistentvolumeclaims"` so events never match
- No error is raised; the PVC table silently falls back to REST-only polling

## Proposed Solutions

### Option A: Normalize kind strings in the backend hub (add a mapping)
- **Pros:** Frontend uses short names consistently, backend handles the translation
- **Cons:** Another mapping to maintain
- **Effort:** Small
- **Risk:** Low

### Option B: Use full resource names everywhere (frontend uses "persistentvolumeclaims")
- **Pros:** No mapping needed, matches k8s conventions
- **Cons:** Breaks frontend URL patterns, less user-friendly
- **Effort:** Medium
- **Risk:** Medium — requires frontend route changes

## Technical Details
- **Affected files:** `backend/internal/k8s/informers.go`, `backend/internal/websocket/hub.go` or frontend routes
- **Components:** Informer event emission, WebSocket hub subscription matching

## Acceptance Criteria
- [ ] PVC table receives real-time WebSocket updates when PVCs are created/modified/deleted
- [ ] All 19 resource types have consistent kind string mapping between frontend and backend
