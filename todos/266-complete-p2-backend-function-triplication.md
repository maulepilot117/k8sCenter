---
status: pending
priority: p2
issue_id: "266"
tags: [code-review, quality, backend, duplication]
dependencies: []
---

# Backend Suspend/List/Delete Functions Are Byte-For-Byte Identical

## Problem Statement

`flux_notifications.go` contains 9 functions (3 Suspend, 3 List, 3 Delete) that are structurally identical except for the GVR constant and error message prefix. Additionally, the 15 handler methods in `handler.go` (~480 lines) follow the same CRUD boilerplate pattern repeated 3x per resource type.

## Findings

**Identified by:** Code Simplicity, Pattern Recognition

**Evidence:**
- `SuspendProvider` (lines 500-518), `SuspendAlert` (622-641), `SuspendReceiver` (715-734) — byte-for-byte identical except GVR
- `ListProviders` (294-305), `ListAlerts` (306-317), `ListReceivers` (318-333) — identical except GVR + normalizer
- `DeleteProvider` (495-497), `DeleteAlert` (617-619), `DeleteReceiver` (710-712) — trivial one-liners
- Handler CRUD methods: 15 methods with ~80% structural overlap (~480 lines)
- Validation functions share ~60% identical name/namespace validation boilerplate

## Proposed Solutions

### Option A: Generic helper functions (Recommended)
- `suspendResource(ctx, dynClient, gvr, ns, name, suspend)` — single function replaces 3
- `listResources[T](ctx, dynClient, gvr, normalize)` — generic replaces 3
- `deleteResource(ctx, dynClient, gvr, ns, name)` — single replaces 3
- `validateNameAndNamespace(name, ns)` — extract shared validation prefix
- **Estimated savings:** ~120 lines in flux_notifications.go
- **Effort:** Small
- **Risk:** Low

### Option B: Generic handler factory (more aggressive)
- `crudConfig` struct per resource kind + parameterized `handleCreate`, `handleUpdate`, etc.
- **Estimated savings:** ~350 lines in handler.go
- **Effort:** Medium
- **Risk:** Low (but adds indirection)

## Recommended Action

Option A first (quick win), Option B as follow-up if desired.

## Technical Details

**Affected files:**
- `backend/internal/notification/flux_notifications.go`
- `backend/internal/notification/handler.go`

## Acceptance Criteria

- [ ] Single `suspendResource` function replaces 3 identical suspend functions
- [ ] Single `listResources[T]` generic replaces 3 identical list functions
- [ ] Inline or unify delete functions
- [ ] Extract `validateNameAndNamespace` helper
- [ ] All existing tests pass

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Go generics can clean up the list pattern nicely |

## Resources

- PR: #153
- File: backend/internal/notification/flux_notifications.go
