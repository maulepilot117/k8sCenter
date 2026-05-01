---
name: Worker patches by name; pinned UID never enforced — ES delete/recreate races mis-attribute audit
status: completed
priority: p2
issue_id: 349
tags: [code-review, eso, phase-e, audit-integrity, race]
dependencies: []
---

## Problem Statement

`BulkScopeTarget` carries the pinned tuple `(Namespace, Name, UID)`. The worker passes `(Namespace, Name)` to `patchForceSync`, which Gets+Patches by name — and never compares the live `obj.GetUID()` against `target.UID`.

The author noticed and signalled the gap — `bulk_worker.go:189` discards the live UID with the comment:

```go
_ = uid // patchForceSync returns the live UID; we already pinned it at scope-resolve
```

But the pin is half-built: scope-resolve pins, the worker doesn't enforce.

**Race scenario:**
1. T=0: scope-resolve captures `target = {ns: apps, name: db-creds, uid: UID-A}`.
2. T+5s: ES deleted (operator or controller cleanup).
3. T+6s: ES recreated with same name, new UID-B (different secret store, different target).
4. T+10s: worker reaches the target, patches `apps/db-creds` (now backed by UID-B), records `succeeded` with the **pinned** UID-A.

**Audit trail says "succeeded UID-A" but the actual mutation hit UID-B.** Audit and reality diverge silently.

This breaks two contracts:
- L8.1 audit honesty (Detail JSON misrepresents what was patched).
- The scope-pin design intent (patch only the resources the operator confirmed).

## Findings

- adversarial reviewer (adv-5 medium, conf 0.92)

**Affected files:**
- `backend/internal/externalsecrets/bulk_worker.go:1936-1975` — worker patch loop
- `backend/internal/externalsecrets/actions.go:217-249` — patchForceSync (returns UID but caller can compare)

## Proposed Solutions

### Option A — enforce UID equality after Get (recommended)

Modify `patchForceSync` to accept an expected UID, or add a sibling `patchForceSyncPinned`:

```go
func (h *Handler) patchForceSyncPinned(ctx context.Context, client dynamic.Interface,
    ns, name, expectedUID string) (string, error) {

    obj, err := client.Resource(ExternalSecretGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
    if err != nil {
        return "", err
    }
    liveUID := string(obj.GetUID())
    if expectedUID != "" && liveUID != expectedUID {
        return liveUID, errUIDDrifted
    }
    // ... existing in-flight check + Patch
}
```

Worker handles `errUIDDrifted` as a new outcome reason:

```go
case errors.Is(err, errUIDDrifted):
    _ = w.store.AppendOutcome(ctx, msg.JobID, "", &store.BulkRefreshOutcome{
        UID: target.UID, Reason: "uid_drifted",
    }, nil)
```

Operators see "the ES at that name now has a different UID — skipped to avoid patching a resource you didn't confirm." Honest.

The single force-sync path (`HandleForceSyncExternalSecret`) doesn't pin (no targetUIDs), so it should keep using the un-pinned variant.

### Option B — patch by UID via field selector

`metav1.ListOptions{FieldSelector: "metadata.uid=<UID>"}` then patch. More complex (Patch by UID isn't a thing in dynamic client), and arguably less clear than the explicit equality check.

**Recommendation:** Option A.

## Acceptance Criteria

- [ ] Worker calls a UID-pinned variant; mismatch produces `failed: uid_drifted` outcome.
- [ ] Single force-sync (HandleForceSyncExternalSecret) still uses the un-pinned variant — no behavior change there.
- [ ] Test: ES deleted-and-recreated between scope-resolve and worker patch is recorded as `uid_drifted`, no patch issued against the new UID.
- [ ] Discarded `_ = uid` at bulk_worker.go is removed (variable now consumed by the equality check).
- [ ] Audit Detail's `succeeded[]` UIDs match the actually-patched objects.
