---
status: pending
priority: p2
issue_id: "085"
tags: [code-review, backend, websocket, concurrency]
dependencies: []
---

# Informer emitEvent Passes Object by Reference

## Problem Statement
The informer event handler passes the k8s object directly to the event callback (and through the hub's event channel). The informer may mutate or reuse this object on the next event. If the hub is still processing/serializing the previous event's object, this is a data race.

## Findings
- Informer callbacks receive a pointer to the object from the informer cache
- The object is passed through the hub event channel without deep copy
- Informer cache may update the same pointer on subsequent events
- Concurrent read (hub serializing) and write (informer updating) on the same object = data race

## Proposed Solutions

### Option A: Deep copy the object before passing to the event channel
- **Pros:** Eliminates the race, safe for concurrent processing
- **Cons:** Copy overhead per event (negligible for typical k8s objects)
- **Effort:** Small
- **Risk:** Low — use `runtime.DeepCopyObject()` from client-go

## Technical Details
- **Affected files:** `backend/internal/k8s/informers.go`

## Acceptance Criteria
- [ ] Objects passed to event callbacks are deep-copied from the informer cache
- [ ] `go test -race` passes
