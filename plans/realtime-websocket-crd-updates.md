# feat: Real-Time WebSocket Updates for GitOps & Policy CRDs

## Overview

Add real-time WebSocket event streaming for GitOps (Argo CD, Flux CD) and Policy (Kyverno, Gatekeeper) CRD resources. Currently these pages use REST-only fetch-on-mount with manual refresh. This extends the existing informer → hub → WebSocket pipeline to CRDs.

## Simplified Approach (post-review)

Three reviewers (DHH, Kieran, Simplicity) agreed on key simplifications:

1. **No separate CRDWatchManager** — extend `InformerManager` with `WatchCRD`/`StopCRD` methods (~40 lines)
2. **No Debouncer component** — hub already has 1024-buffer + resync fallback; frontend already debounces re-fetches
3. **No normalize.go files** — normalizers already exist as standalone functions, just export them (capitalize)
4. **Re-fetch everywhere on frontend** — all 5 islands do debounced REST re-fetch on WS event (no client-side merge)
5. **No LiveBadge component** — inline 3-5 lines of JSX
6. **2 phases, not 3** — tests ship with code

### Key technical decisions from review:
- Use `dynamicinformer.ForResource().Informer().AddEventHandler(ResourceEventHandlerDetailedFuncs)` (not `cache.NewInformer` which lacks `isInInitialList`)
- Widen `AccessChecker` interface to include `CanAccessGroupResource` (already exists on concrete type)
- Co-locate API group with `RegisterAllowedKind` registration (not a static map)
- Fix singleflight cache race as a standalone preparatory change
- Explicitly scope out Gatekeeper dynamic constraints (watch `constrainttemplates` only, not per-constraint CRDs)

---

## Phase 1: Backend

### Step 1: Fix singleflight cache invalidation race (standalone bug fix)

**Files:** `gitops/handler.go`, `policy/handler.go`

The `invalidateCache()` method calls `fetchGroup.Forget("fetch")` which can race with an in-flight singleflight fetch that repopulates cache with stale data. Fix by removing the `Forget` call — nil cache + zero time is sufficient.

### Step 2: Export existing normalizer functions

**Files:** `gitops/argocd.go`, `gitops/flux.go`, `policy/kyverno.go`, `policy/gatekeeper.go`

Simply capitalize: `normalizeArgoApp` → `NormalizeArgoApp`, etc. These are already standalone functions with the right signatures.

### Step 3: Add CRD watch support to InformerManager

**File:** `k8s/informers.go`

Add `WatchCRD(ctx, gvr, kind, normalizer, cb)` and `StopCRD(gvr)` methods. Each creates a dynamic informer via `dynamicinformer.NewDynamicSharedInformerFactory`, registers `ResourceEventHandlerDetailedFuncs` (for `isInInitialList` support), deep-copies + normalizes before calling the event callback. Individual per-GVR factories enable independent lifecycle.

### Step 4: Extend WebSocket hub for CRD RBAC

**Files:** `websocket/hub.go`, `websocket/events.go`, `websocket/client.go`

- Add `CanAccessGroupResource` to `AccessChecker` interface
- Add `sync.RWMutex` to protect `allowedKinds` map
- Extend `RegisterAllowedKind` to accept API group: `RegisterAllowedKind(kind, apiGroup string)`
- Update `handleSubscribe` and `revalidateSubscriptions` to use group-aware RBAC for CRD kinds
- Raise `maxSubscriptions` from 20 to 25

### Step 5: Wire discovery callbacks + cache invalidation

**Files:** `gitops/discovery.go`, `policy/discovery.go`, `cmd/kubecenter/main.go`

- Add `OnChange` callback to discoverers (fires when tool availability changes)
- In `main.go`: when GitOps discovery detects Argo CD → call `informerMgr.WatchCRD(...)` + `RegisterAllowedKind(...)`
- On each CRD event: call handler's `InvalidateCache()`

### Step 6: Update Helm chart RBAC

**File:** `helm/kubecenter/templates/clusterrole.yaml`

Add list+watch for all CRD API groups (no-ops if CRDs don't exist).

## Phase 2: Frontend

### Step 7: Add WS subscriptions to GitOps islands

**Files:** `islands/GitOpsApplications.tsx`, `islands/GitOpsAppDetail.tsx`

Subscribe to CRD kinds, on any event → debounced REST re-fetch. Guard against remote clusters. Unsubscribe on unmount.

### Step 8: Add WS subscriptions to Policy islands

**Files:** `islands/PolicyDashboard.tsx`, `islands/ViolationBrowser.tsx`, `islands/ComplianceDashboard.tsx`

Same pattern: subscribe, debounced re-fetch on event, cleanup on unmount.

## File Change Summary

| File | Action |
|------|--------|
| `backend/internal/gitops/handler.go` | MODIFY — fix cache invalidation race, add InvalidateCache |
| `backend/internal/policy/handler.go` | MODIFY — fix cache invalidation race, add InvalidateCache |
| `backend/internal/gitops/argocd.go` | MODIFY — export NormalizeArgoApp |
| `backend/internal/gitops/flux.go` | MODIFY — export normalizers |
| `backend/internal/policy/kyverno.go` | MODIFY — export normalizers |
| `backend/internal/policy/gatekeeper.go` | MODIFY — export normalizers |
| `backend/internal/k8s/informers.go` | MODIFY — add WatchCRD/StopCRD |
| `backend/internal/websocket/hub.go` | MODIFY — extend AccessChecker interface |
| `backend/internal/websocket/events.go` | MODIFY — mutex, group-aware registration |
| `backend/internal/websocket/client.go` | MODIFY — group-aware subscribe, maxSubscriptions |
| `backend/internal/gitops/discovery.go` | MODIFY — add OnChange callback |
| `backend/internal/policy/discovery.go` | MODIFY — add OnChange callback |
| `backend/cmd/kubecenter/main.go` | MODIFY — wire CRD watches + callbacks |
| `helm/kubecenter/templates/clusterrole.yaml` | MODIFY — add CRD RBAC |
| `frontend/islands/GitOpsApplications.tsx` | MODIFY — add WS subscription |
| `frontend/islands/GitOpsAppDetail.tsx` | MODIFY — add WS subscription |
| `frontend/islands/PolicyDashboard.tsx` | MODIFY — add WS subscription |
| `frontend/islands/ViolationBrowser.tsx` | MODIFY — add WS subscription |
| `frontend/islands/ComplianceDashboard.tsx` | MODIFY — add WS subscription |

**Total: 0 new files, 19 modified files across 2 phases**
