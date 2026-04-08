---
status: pending
priority: p2
issue_id: "268"
tags: [code-review, correctness, backend, frontend]
dependencies: []
---

# Receiver Suspend May Not Be Valid (Plan vs Implementation Conflict)

## Problem Statement

The plan doc (`plans/flux-notifications.md` line 23) explicitly states: "Suspend: Provider and Alert only (not Receiver) — Receiver CRD v1 does not have spec.suspend field." Yet the implementation includes full SuspendReceiver support: backend function, handler, route, and frontend toggle.

If Flux Receiver v1 genuinely lacks `spec.suspend`, this is dead code that will either silently add a meaningless field or error at runtime. If the CRD does support it, the plan doc is wrong.

## Findings

**Identified by:** Code Simplicity, Performance Oracle

**Evidence:**
- `plans/flux-notifications.md:23` — "Receiver CRD v1 does not have spec.suspend field"
- `flux_notifications.go:715-734` — SuspendReceiver function exists
- `handler.go:832-878` — HandleSuspendReceiver handler exists
- `routes.go:478` — POST /receivers/{namespace}/{name}/suspend route registered
- `FluxReceivers.tsx:234-253` — Frontend suspend toggle exists

## Proposed Solutions

### Option A: Verify against actual Flux CRD and resolve
- Check `kubectl get crd receivers.notification.toolkit.fluxcd.io -o json | jq '.spec.versions[].schema.openAPIV3Schema.properties.spec.properties.suspend'`
- If suspend IS supported: update plan doc, keep code
- If suspend is NOT supported: remove SuspendReceiver, HandleSuspendReceiver, route, frontend toggle (~85 lines saved)
- **Effort:** Small
- **Risk:** None

## Acceptance Criteria

- [ ] Verified whether Flux Receiver v1 CRD supports spec.suspend
- [ ] Either plan doc or implementation corrected to match reality

## Work Log

| Date | Action | Learnings |
|------|--------|-----------|
| 2026-04-08 | Created from PR #153 review | Plan says no, code says yes — need to check the actual CRD |

## Resources

- PR: #153
- Flux Notification Controller docs: https://fluxcd.io/flux/components/notification/receivers/
