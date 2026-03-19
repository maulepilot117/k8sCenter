# feat: Dynamic Informer for CiliumNetworkPolicies

Add a dynamic informer for CiliumNetworkPolicy CRDs so list/get operations use the informer cache and WebSocket events stream to connected clients — matching the pattern used by all 31 built-in resource types.

## Problem Statement

CiliumNetworkPolicy handlers make direct dynamic client API calls for every list/get. This means no caching (every table load hits the API server) and no WebSocket events (the ResourceTable subscribes but never receives updates). This is the only resource type with this inconsistency.

---

## Implementation Plan

### Step A: Wire Up Dynamic Informer

**`backend/internal/k8s/informers.go`:**

1. Export the GVR constant here (avoids circular import with `resources` package):
   ```go
   var CiliumPolicyGVR = schema.GroupVersionResource{
       Group: "cilium.io", Version: "v2", Resource: "ciliumnetworkpolicies",
   }
   ```

2. Add `dynFactory` field to `InformerManager`:
   ```go
   type InformerManager struct {
       factory    informers.SharedInformerFactory
       dynFactory dynamicinformer.DynamicSharedInformerFactory // nil if no CRDs detected
       logger     *slog.Logger
   }
   ```

3. Accept `dynamic.Interface` in constructor (nil-safe). **Probe discovery** before registering:
   - Call `DiscoveryClient.ServerResourcesForGroupVersion("cilium.io/v2")`
   - If 404 → skip dynamic factory, log info, `dynFactory` stays nil
   - If found → create `DynamicSharedInformerFactory`, register `CiliumPolicyGVR`
   - Handle nil `dynClient` parameter gracefully (skip, don't panic)

4. `Start()` / `WaitForSync()`: guard with `if m.dynFactory != nil`

5. Add lister accessor:
   ```go
   func (m *InformerManager) CiliumNetworkPolicies() dynamiclister.DynamicResourceLister
   ```
   Returns nil if `dynFactory` is nil (CRD not installed).

6. Add to `RegisterEventHandlers`: append dynamic informer to the `specs` loop using same event handler pattern as typed informers. `*unstructured.Unstructured` implements `metav1.Object` and `runtime.Object.DeepCopyObject()` — existing `emitEvent` works unchanged.

**`backend/cmd/kubecenter/main.go`:**

Pass base dynamic client to constructor:
```go
informerMgr := k8s.NewInformerManager(baseCS, k8sClient.BaseDynamicClient(), logger)
```

**`backend/internal/websocket/events.go`:**

Add `"ciliumnetworkpolicies": true` to `allowedKinds`.

### Step B: Switch List/Get to Informer Cache

**`backend/internal/k8s/resources/cilium.go`:**

1. Update GVR reference to use `k8s.CiliumPolicyGVR` (remove local definition)

2. `HandleListCiliumPolicies`:
   - Check if `h.Informers.CiliumNetworkPolicies()` is nil → return 404: "CiliumNetworkPolicy CRD is not installed on this cluster"
   - Add `parseSelectorOrReject(w, params.LabelSelector)` (currently passes raw string to API server — informer lister needs a parsed `labels.Selector`)
   - Read from informer lister instead of dynamic client
   - Use existing `paginate[unstructured.Unstructured]` for client-side pagination (`*unstructured.Unstructured` implements `metav1.ObjectMetaAccessor`)

3. `HandleGetCiliumPolicy`:
   - Same nil-lister guard → 404
   - Read from `lister.Namespace(ns).Get(name)` instead of dynamic client

4. Write operations (create/update/delete) **unchanged** — continue using impersonating dynamic client

### Step C: Verify and Test

- `go vet` + `go test ./... -race` pass
- Manual test: verify WebSocket events flow for Cilium policy create/update/delete
- Manual test: verify list returns cached results (faster than direct API)
- Manual test on non-Cilium cluster: verify clean 404 response, no log noise

---

## Acceptance Criteria

- [ ] Dynamic informer factory created for `cilium.io/v2/ciliumnetworkpolicies`
- [ ] Discovery probe skips registration when CRD is absent (no reflector spin)
- [ ] List/Get handlers read from cache; return 404 when CRD not installed
- [ ] Create/Update/Delete unchanged (impersonating dynamic client)
- [ ] WebSocket events stream for CiliumNetworkPolicy changes
- [ ] `parseSelectorOrReject` used for label selector parsing
- [ ] GVR defined in `k8s` package, referenced from `resources/cilium.go`
- [ ] `go test ./... -race` passes
- [ ] Frontend receives live updates with no changes

## Files to Modify

| File | Purpose |
|------|---------|
| `backend/internal/k8s/informers.go` | Add dynamic factory, discovery probe, lister, event handlers |
| `backend/cmd/kubecenter/main.go` | Pass dynamic client to NewInformerManager |
| `backend/internal/websocket/events.go` | Add ciliumnetworkpolicies to allowedKinds |
| `backend/internal/k8s/resources/cilium.go` | Switch list/get to informer cache, use shared GVR |
