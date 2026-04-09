---
status: pending
priority: p2
issue_id: 260
tags: [code-review, dead-code, networking, pr-157]
dependencies: []
---

# Dead Code: Unused Informers Field on Handler

## Problem Statement

The `Informers *k8s.InformerManager` field was added to the networking Handler struct and injected in `main.go`, but is never referenced in any handler method. This is dead code that adds confusion about the Handler's dependencies.

## Findings

**Location:** 
- `backend/internal/networking/handler.go:33` — field declaration
- `backend/cmd/kubecenter/main.go:307` — injection

The field is not used by any of the 3 new handlers (HandleCiliumBGP, HandleCiliumIPAM, HandleCiliumSubsystems) or any existing handlers.

## Proposed Solutions

### Option A: Remove the field and injection (Recommended)
**Pros:** Clean, no dead code
**Cons:** None — can re-add when Phase B needs it
**Effort:** Small
**Risk:** None

## Acceptance Criteria

- [ ] `Informers` field removed from Handler struct
- [ ] `Informers: informerMgr` injection removed from main.go
- [ ] Build passes

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-09 | Found during PR #157 review | Likely premature addition for Phase B |
