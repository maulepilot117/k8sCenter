# Networking Overview Islands — Implementation Plan

**Spec:** `docs/superpowers/specs/2026-04-09-networking-overview-islands-design.md`
**Branch:** `feat/networking-overview-islands`
**Estimated Steps:** 6

---

## Architecture Overview

### Data Sourcing: CRD + ConfigMap Only (Phase A)

Research confirmed the Cilium agent REST API is Unix-socket only (`/var/run/cilium/cilium.sock`), not TCP port 9879. Rather than adding `pods/exec` RBAC and SPDY executor complexity, Phase A sources all data from Kubernetes CRDs and the existing cached ConfigMap. CRDs are updated in real-time by the Cilium operator and provide full live status for the most operationally useful subsystems.

| Island | Data Source | Live? | Method |
|---|---|---|---|
| BGP peer status | `CiliumBGPNodeConfig` CRD `.status.bgpInstances[].peers[]` | Yes — real-time peer state | Dynamic client |
| BGP config | `CiliumBGPClusterConfig` + `CiliumBGPPeerConfig` CRDs | Yes | Dynamic client |
| IPAM allocations | `CiliumNode` CRD `.spec.ipam` + `.status.ipam` | Yes — updates on every allocation | Dynamic client |
| Endpoint health | `CiliumEndpoint` CRDs `.status.state` | Yes — real-time state changes | Dynamic client |
| Encryption status | ConfigMap `enable-encryption` + `encryption-type` + `CiliumNode.spec.encryption.key` | Yes (config), basic (per-node) | Cached + dynamic client |
| Service mesh | ConfigMap `enable-envoy-config` | Detection only | Cached |
| ClusterMesh | ConfigMap `cluster-mesh-config` | Detection only | Cached |
| Node connectivity | **Deferred to Phase B** (requires agent health daemon) | — | — |

### Phase B (Deferred — Agent Exec)

Phase B adds a `CiliumAgentCollector` that execs `cilium-dbg status -o json` into agent pods for rich data not available in CRDs:

- **Encryption details**: WireGuard per-node public keys, handshake times, transfer stats
- **Service mesh details**: Envoy proxy redirect counts, L7 policy stats, envoy deployment mode
- **ClusterMesh details**: Connected remote clusters, sync status, heartbeat times
- **Node connectivity**: Full inter-node latency matrix (ICMP + HTTP probes)
- **Istio/Linkerd detection**: CRD group discovery + control plane health checks

Phase B also requires `pods/exec` RBAC (opt-in via Helm values) and a semaphore-limited concurrent executor.

---

## Revised Decisions from Code Review

| Finding | Resolution |
|---|---|
| 12 steps too many | Consolidated to 6 steps |
| `MeshDetector` as own file unnecessary | Folded into existing `Detector` in `detect.go` |
| `NotConfiguredCard` doesn't need own file | Inline in `Card.tsx` as a variant |
| NodeConnectivity + EndpointHealth share endpoint | Merged into single `ClusterHealth` island |
| Encryption + Mesh + ClusterMesh all ConfigMap-derived | Merged into single `CiliumSubsystems` island |
| Adaptive polling in `usePoll` is YAGNI for v1 | Cut — fixed 60s polling only. Adaptive deferred to Phase B. |
| `usePoll` return type should be `Signal` not `ReadonlySignal` | Changed to match codebase convention |
| `configured` auto-stop is fragile in generic hook | Added `shouldContinuePolling` callback instead |
| `adaptiveKey` naming confusing | Removed (deferred with adaptive polling) |
| Non-Cilium UX: 7 muted cards is terrible | Conditionally render Cilium rows only when CNI is Cilium |
| Rate limit: 9 islands exhaust 30 req/min bucket | New endpoints get separate read-only 60 req/min bucket |
| IPAM "always configured" wrong for non-Cilium | Returns `configured: false` when `CiliumNode` CRDs absent |
| `CniOverview` one-shot is inconsistent with "live" goal | Changed to 120s polling with refresh button |
| `usePoll` missing `IS_BROWSER` guard | Added to spec |
| Multi-cluster: new endpoints are local-only | Explicit `configured: false` when remote cluster selected |
| Cache invalidation on ConfigMap edit | `HandleUpdateCNIConfig` calls `InvalidateSubsystemCaches()` |
| Service account justification for CRD reads | CRD detection uses service account (not user-facing); handler RBAC via auth middleware |
| Design spec stale (references port 9879) | Update spec with SUPERSEDED notice in Step 1 |
| Use discriminated unions in TypeScript | Applied to all response types |
| Error backoff in `usePoll` | After 3 consecutive failures, pause polling. Resume on visibility change. |
| Helm RBAC for Cilium CRDs not in any step | Added to Step 3 |
| `CiliumConfig` naming collision | Renamed frontend island to `CiliumConfigEditor.tsx` |
| Namespace interaction with endpoint counts | Endpoint counts are cluster-wide (Cilium endpoints are cluster-scoped CRDs) |

---

## Step 1: Backend Types + CRD Readers

**Files:** 2 new, 1 modified
**Goal:** Define response types and CRD reading functions

### Create `backend/internal/networking/types.go`

Response structs for 3 endpoints. Use discriminated pattern: `Configured` field gates presence of other fields.

```go
// BGP
type CiliumBGPResponse struct {
    Configured bool            `json:"configured"`
    Config     *BGPConfig      `json:"config,omitempty"`
    Peers      []BGPPeerStatus `json:"peers,omitempty"`
}

type BGPConfig struct {
    ClusterConfigs []BGPClusterConfig `json:"clusterConfigs"`
    PeerConfigs    []BGPPeerConfig    `json:"peerConfigs"`
}

type BGPClusterConfig struct {
    Name         string            `json:"name"`
    NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

type BGPPeerConfig struct {
    Name        string `json:"name"`
    PeerASN     int64  `json:"peerASN"`
    PeerAddress string `json:"peerAddress"`
}

type BGPPeerStatus struct {
    Node             string `json:"node"`
    PeerAddress      string `json:"peerAddress"`
    PeerASN          int64  `json:"peerASN"`
    LocalASN         int64  `json:"localASN"`
    SessionState     string `json:"sessionState"` // established, active, connect, idle, opensent, openconfirm
    RoutesReceived   int    `json:"routesReceived"`
    RoutesAdvertised int    `json:"routesAdvertised"`
}

// IPAM
type CiliumIPAMResponse struct {
    Configured     bool       `json:"configured"`
    Mode           string     `json:"mode"`
    PodCIDRs       []string   `json:"podCIDRs"`
    Allocated      int        `json:"allocated"`
    Available      int        `json:"available"`
    Total          int        `json:"total"`
    ExhaustionRisk string     `json:"exhaustionRisk"` // none, medium, high
    PerNode        []NodeIPAM `json:"perNode,omitempty"`
}

type NodeIPAM struct {
    Node      string `json:"node"`
    Allocated int    `json:"allocated"`
    Available int    `json:"available"`
    PodCIDR   string `json:"podCIDR"`
}

// Subsystems (encryption, mesh, ClusterMesh, endpoint health)
type CiliumSubsystemsResponse struct {
    Configured bool              `json:"configured"`
    Encryption *EncryptionInfo   `json:"encryption,omitempty"`
    Mesh       *MeshInfo         `json:"mesh,omitempty"`
    ClusterMesh *ClusterMeshInfo `json:"clusterMesh,omitempty"`
    Endpoints  *EndpointCounts   `json:"endpoints,omitempty"`
}

type EncryptionInfo struct {
    Enabled        bool   `json:"enabled"`
    Mode           string `json:"mode"` // wireguard, ipsec
    NodesEncrypted int    `json:"nodesEncrypted"`
    NodesTotal     int    `json:"nodesTotal"`
}

type MeshInfo struct {
    Enabled bool   `json:"enabled"`
    Engine  string `json:"engine"` // cilium, none (istio/linkerd deferred to Phase B)
}

type ClusterMeshInfo struct {
    Enabled bool `json:"enabled"`
}

type EndpointCounts struct {
    Total         int `json:"total"`
    Ready         int `json:"ready"`
    NotReady      int `json:"notReady"`
    Disconnecting int `json:"disconnecting"`
    Waiting       int `json:"waiting"`
}
```

Exhaustion risk thresholds: `<75%` = none, `75-90%` = medium, `>90%` = high.

### Create `backend/internal/networking/cilium_crds.go`

CRD reading functions using dynamic client with impersonation for user-facing requests.

**GVR definitions:**
```go
var (
    bgpClusterConfigGVR = schema.GroupVersionResource{Group: "cilium.io", Version: "v2alpha1", Resource: "ciliumbgpclusterconfigs"}
    bgpPeerConfigGVR    = schema.GroupVersionResource{Group: "cilium.io", Version: "v2alpha1", Resource: "ciliumbgppeerconfigs"}
    bgpNodeConfigGVR    = schema.GroupVersionResource{Group: "cilium.io", Version: "v2alpha1", Resource: "ciliumbgpnodeconfigs"}
    ciliumNodeGVR       = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumnodes"}
    ciliumEndpointGVR   = schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: "ciliumendpoints"}
)
```

**Key functions:**
- `hasCRD(ctx, discoveryClient, gvr) bool` — precise CRD existence check by full GVR (not just API group)
- `readBGPClusterConfigs(ctx, dynamicClient) ([]BGPClusterConfig, error)`
- `readBGPPeerConfigs(ctx, dynamicClient) ([]BGPPeerConfig, error)`
- `readBGPNodeConfigs(ctx, dynamicClient) ([]BGPPeerStatus, error)` — extracts from `.status.bgpInstances[].peers[].peeringState`, `.routeCount[]`
- `readCiliumNodes(ctx, dynamicClient) ([]CiliumNodeInfo, error)` — extracts `.spec.ipam.podCIDRs`, `.spec.ipam.pool` (available), `.status.ipam.used` (allocated), `.spec.encryption.key`
- `aggregateEndpoints(ctx, dynamicClient) (EndpointCounts, error)` — counts by `.status.state` (ready, waiting-for-identity, not-ready, disconnecting, disconnected)

Uses `unstructured.Unstructured` + `NestedMap`/`NestedSlice` for field access.

### Modify `backend/internal/networking/detect.go`

Add mesh detection to existing `Detector`:
```go
func (d *Detector) DetectMesh() string {
    info := d.CachedInfo()
    if info != nil && info.Features.EnvoyEnabled {
        return "cilium"
    }
    return "none"
}
```

Add `EnvoyEnabled bool` to `CNIFeatures` struct (check `enable-envoy-config` key in ConfigMap, same pattern as existing feature checks).

### Update design spec

Add a SUPERSEDED notice at the top of the spec noting the agent API transport revision (CRD-only for Phase A, agent exec deferred to Phase B).

### Verification
- `go build ./internal/networking/...`
- `go vet ./internal/networking/...`

---

## Step 2: Backend Handlers + Route Wiring

**Files:** 3 modified
**Goal:** 3 new handler methods, route registration, cache invalidation

### Modify `backend/internal/networking/handler.go`

Add `Informers *k8s.InformerManager` field to `Handler` struct. Add singleflight group + cache for subsystem data.

**HandleCiliumBGP:**
1. `httputil.RequireUser(w, r)` — auth check
2. If CNI is not Cilium → `httputil.WriteData(w, CiliumBGPResponse{Configured: false})`
3. If remote cluster selected → same `configured: false`
4. `hasCRD(ctx, bgpClusterConfigGVR)` — if not → `configured: false`
5. Read CiliumBGPClusterConfig + CiliumBGPPeerConfig (config)
6. Read CiliumBGPNodeConfig (live peer status from `.status`)
7. Compose + return `CiliumBGPResponse`

**HandleCiliumIPAM:**
1. Auth check
2. If CNI is not Cilium → `configured: false`
3. If remote cluster → `configured: false`
4. `hasCRD(ctx, ciliumNodeGVR)` — if not → `configured: false`
5. Read CiliumNode CRDs, aggregate IPAM data
6. Compute exhaustion risk from allocated/total ratio
7. Compose + return `CiliumIPAMResponse`

**HandleCiliumSubsystems:**
1. Auth check
2. If CNI is not Cilium → `configured: false`
3. If remote cluster → `configured: false`
4. Singleflight + 30s cache (same pattern as `internal/policy/handler.go`)
5. Read encryption from ConfigMap (cached by Detector) + CiliumNode encryption keys
6. Read mesh from Detector's `DetectMesh()`
7. Read ClusterMesh from ConfigMap `cluster-mesh-config` key
8. Aggregate endpoint counts from CiliumEndpoint CRDs
9. Compose + return `CiliumSubsystemsResponse`

**Cache invalidation:** Add `InvalidateSubsystemCaches()` method. Called from existing `HandleUpdateCNIConfig` after successful ConfigMap update.

**CNI short-circuit pattern:** Each handler checks `h.Detector.CachedInfo()` for Cilium. Could also be route-level middleware on the `/cilium/*` group, but per-handler is simpler and more explicit.

### Modify `backend/internal/server/routes.go`

Add 3 new routes under the existing `/networking` group with a **separate read-only rate limiter** (60 req/min):

```go
// Inside registerNetworkingRoutes:
nr.Route("/cilium", func(cr chi.Router) {
    cr.Use(middleware.RateLimit(s.ReadRateLimiter)) // 60 req/min, separate bucket
    cr.Get("/bgp", h.HandleCiliumBGP)
    cr.Get("/ipam", h.HandleCiliumIPAM)
    cr.Get("/subsystems", h.HandleCiliumSubsystems)
})
```

### Modify `backend/cmd/kubecenter/main.go`

Wire `Informers` field:
```go
networkingHandler := &networking.Handler{
    K8sClient:    k8sClient,
    Detector:     cniDetector,
    HubbleClient: hubbleClient,
    Informers:    informerMgr,    // NEW
    AuditLogger:  auditLogger,
    Logger:       logger,
    ClusterID:    cfg.ClusterID,
}
```

### Verification
- `go build ./cmd/kubecenter/...`
- `go vet ./...`

---

## Step 3: Helm RBAC + Backend Tests

**Files:** 1 modified, 1 modified
**Goal:** Grant CRD read access to service account, add unit tests

### Modify Helm RBAC template

Add Cilium CRD read permissions to `helm/kubecenter/templates/`:

```yaml
- apiGroups: ["cilium.io"]
  resources: ["ciliumbgpclusterconfigs", "ciliumbgppeerconfigs", "ciliumbgpnodeconfigs", "ciliumnodes", "ciliumendpoints"]
  verbs: ["get", "list", "watch"]
```

No `pods/exec` needed for Phase A. Phase B will add that as opt-in.

### Modify `backend/internal/networking/networking_test.go`

Add tests for:
- **CRD readers:** Mock `unstructured.Unstructured` objects, verify field extraction for BGP (peeringState, routeCount), IPAM (allocated vs pool), Endpoints (state aggregation)
- **Handlers:** Verify `configured: false` short-circuit when CNI is not Cilium, when remote cluster is selected, when CRD is absent
- **IPAM exhaustion risk:** Test thresholds (<75% = none, 75-90% = medium, >90% = high)
- **Mesh detection:** Verify `DetectMesh()` returns "cilium" when `EnvoyEnabled`, "none" otherwise
- **Cache invalidation:** Verify `InvalidateSubsystemCaches()` clears the singleflight cache

### Verification
- `go test ./internal/networking/... -v`
- `make helm-lint`

---

## Step 4: Frontend Foundation + CniStatus Decomposition

**Files:** 5 new, 1 modified, 1 deleted
**Goal:** usePoll hook, types, decompose CniStatus, create NetworkOverview shell

### Create `frontend/lib/hooks/use-poll.ts`

Minimal polling hook — no adaptive polling for v1:

```typescript
import { useSignal } from "@preact/signals";
import type { Signal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";

interface UsePollOptions<T> {
  interval: number;
  shouldContinuePolling?: (data: T) => boolean; // false = stop polling
  enabled?: boolean;
}

interface UsePollResult<T> {
  data: Signal<T | null>;
  loading: Signal<boolean>;
  error: Signal<string | null>;
}

export function usePoll<T>(url: string, opts: UsePollOptions<T>): UsePollResult<T>
```

**Implementation rules:**
- `useSignal` for all state (codebase convention)
- `useRef<number | null>` for interval ID (follows `useWsRefetch.ts`)
- `IS_BROWSER` guard in `useEffect`
- `document.hidden` check on each tick (follows `DashboardV2.tsx`)
- `AbortController` created per effect, aborted on cleanup
- After each fetch: if `shouldContinuePolling` returns `false`, clear interval, stop
- Error clearing: `error.value = null` on every successful fetch
- Error backoff: track consecutive failures. After 3, pause polling. Resume on `document.visibilitychange` event
- Cleanup: clear interval + abort controller on unmount
- ~40 lines total

### Create `frontend/lib/cilium-types.ts`

TypeScript interfaces using discriminated unions:

```typescript
export type CiliumBGPResponse =
  | { configured: false }
  | { configured: true; config: BGPConfig; peers: BGPPeerStatus[] };

export type CiliumIPAMResponse =
  | { configured: false }
  | { configured: true; mode: string; podCIDRs: string[]; allocated: number; available: number; total: number; exhaustionRisk: string; perNode: NodeIPAM[] };

export type CiliumSubsystemsResponse =
  | { configured: false }
  | { configured: true; encryption: EncryptionInfo; mesh: MeshInfo; clusterMesh: ClusterMeshInfo; endpoints: EndpointCounts };

// Sub-types
export interface BGPConfig { clusterConfigs: BGPClusterConfig[]; peerConfigs: BGPPeerConfig[]; }
export interface BGPClusterConfig { name: string; nodeSelector?: Record<string, string>; }
export interface BGPPeerConfig { name: string; peerASN: number; peerAddress: string; }
export interface BGPPeerStatus { node: string; peerAddress: string; peerASN: number; localASN: number; sessionState: string; routesReceived: number; routesAdvertised: number; }
export interface NodeIPAM { node: string; allocated: number; available: number; podCIDR: string; }
export interface EncryptionInfo { enabled: boolean; mode: string; nodesEncrypted: number; nodesTotal: number; }
export interface MeshInfo { enabled: boolean; engine: string; }
export interface ClusterMeshInfo { enabled: boolean; }
export interface EndpointCounts { total: number; ready: number; notReady: number; disconnecting: number; waiting: number; }
```

### Create `frontend/islands/CniOverview.tsx`

Extract from `CniStatus.tsx` lines 198-332. Changes from original:
- Standalone island with own fetch (`GET /v1/networking/cni`)
- **Polls at 120s** (not one-shot) — consistent with "live status" goal
- Retains Refresh button (passes `?refresh=true`)
- Renders Overview card + Health card as fragment (each occupies own grid cell)
- `FeatureRow` helper stays with this island

### Create `frontend/islands/CiliumConfigEditor.tsx`

Extract from `CniStatus.tsx` lines 334-471. Renamed from `CiliumConfig` to avoid naming collision with existing Go struct.
- Self-contained: owns loading/error state
- Fetches `GET /v1/networking/cni/config`

### Create `frontend/islands/NetworkOverview.tsx`

Layout shell with Status/Configuration tabs and conditional Cilium grid:

```tsx
export default function NetworkOverview() {
  const cniInfo = useSignal<CNIInfo | null>(null);
  // Fetch CNI info to determine if Cilium

  return (
    <div>
      {/* Tab bar: Status | Configuration (config tab only if Cilium + editable) */}

      {activeTab === "status" && (
        <div class="grid gap-4 md:grid-cols-2">
          <CniOverview />        {/* Row 1: Overview + Health (fragment = 2 grid cells) */}

          {/* Cilium-specific rows — only rendered when CNI is Cilium */}
          {isCilium && (
            <>
              <BgpStatus />        {/* Row 2 left */}
              <IpamStatus />       {/* Row 2 right */}
              <CiliumSubsystems /> {/* Row 3: full-width encryption + mesh + ClusterMesh + endpoints */}
            </>
          )}
        </div>
      )}

      {activeTab === "config" && <CiliumConfigEditor />}
    </div>
  );
}
```

**Non-Cilium UX:** When CNI is Calico/Flannel, only row 1 (Overview + Health) renders. No wall of muted "Not Configured" cards. A subtle text note: "Additional subsystem details available for Cilium clusters."

### Modify `frontend/islands/NetworkingDashboard.tsx`

```diff
- import CniStatus from "@/islands/CniStatus.tsx";
+ import NetworkOverview from "@/islands/NetworkOverview.tsx";

- {isFlows ? <FlowViewer /> : isCni ? <CniStatus /> : (
+ {isFlows ? <FlowViewer /> : isCni ? <NetworkOverview /> : (
```

### Delete `frontend/islands/CniStatus.tsx`

After confirming all functionality extracted to CniOverview + CiliumConfigEditor.

### Verification
- `deno lint frontend/`
- `deno check frontend/islands/NetworkOverview.tsx`
- `grep -r "CniStatus" frontend/` — verify no remaining imports
- Remove "CNI Plugin" from SubNav tab label in `lib/constants.ts` (rename to "Overview" or remove if `/networking` is already the default)

---

## Step 5: Frontend Islands — BGP, IPAM, Subsystems

**Files:** 3 new, 1 modified
**Goal:** All new status islands

### Create `frontend/islands/BgpStatus.tsx`

- `usePoll<CiliumBGPResponse>("/v1/networking/cilium/bgp", { interval: 60000, shouldContinuePolling: (d) => d.configured })`
- If not configured → inline muted card: `<Card class="opacity-50" title="BGP Peering"><p class="text-text-muted">BGP is not configured.</p></Card>`
- Header: title + "X/Y Established" badge (green if all established, yellow if some, red if none)
- Peer list: each row shows address, ASN, route counts (↑advertised ↓received), colored dot for session state
  - Green: `established`
  - Yellow: `active`, `connect`, `opensent`, `openconfirm`
  - Red: `idle`

### Create `frontend/islands/IpamStatus.tsx`

- `usePoll<CiliumIPAMResponse>("/v1/networking/cilium/ipam", { interval: 60000, shouldContinuePolling: (d) => d.configured })`
- If not configured → inline muted card
- Shows: mode, pod CIDR, allocated/total with progress bar
- Exhaustion risk badge: none=green, medium=yellow, high=red
- Per-node list: show all nodes (no truncation for v1 — homelab has 3 nodes)

### Create `frontend/islands/CiliumSubsystems.tsx`

- `usePoll<CiliumSubsystemsResponse>("/v1/networking/cilium/subsystems", { interval: 60000, shouldContinuePolling: (d) => d.configured })`
- Full-width card (`md:col-span-2` applied by parent grid)
- **Four sections in a horizontal layout:**

  **Encryption:** mode badge (WireGuard/IPsec), "X/Y nodes encrypted"
  **Service Mesh:** engine badge ("Cilium" or "None"), enabled/disabled
  **ClusterMesh:** enabled/disabled badge
  **Endpoints:** total count + stacked bar (ready=green, notReady=yellow, disconnecting=red, waiting=gray) with colored dot legend

- Each section is compact — 2-3 lines of key-value pairs
- Sections for disabled features show "Disabled" in muted text (no separate muted card)

### Modify `frontend/islands/NetworkOverview.tsx`

Add imports for BgpStatus, IpamStatus, CiliumSubsystems and wire into the grid. The Step 4 version may have used placeholder comments — replace with actual components.

### Verification
- `deno lint frontend/`
- `deno check` all new files
- Manually verify the grid renders correctly in browser

---

## Step 6: E2E Test + Smoke Test

**Files:** 1 new, 1 modified
**Goal:** Verify the full feature works end-to-end

### Add Playwright E2E test

In `e2e/`:
- Navigate to `/networking`
- Verify `NetworkOverview` renders (no "CNI Plugin" heading)
- Verify Overview + Health cards visible
- If Cilium detected: verify BGP, IPAM, and Subsystems cards render
- Verify not-configured islands show appropriate muted state
- Navigate to Services tab and back to Overview — verify re-render without errors

### Smoke test against homelab

- Deploy to homelab cluster (Cilium 1.19.1)
- Verify BGP peers show live session state from `CiliumBGPNodeConfig`
- Verify IPAM shows correct allocation counts from `CiliumNode`
- Verify endpoint health counts match `kubectl get ciliumendpoints -A | wc -l`
- Verify encryption shows WireGuard enabled (from ConfigMap)
- Verify ClusterMesh shows "Disabled" (not configured in homelab)
- Navigate between tabs — verify no console errors, no leaked timers

### Modify E2E CI config if needed

Ensure the Kind cluster in CI has Cilium CRDs available (or the test gracefully handles their absence).

### Verification
- `make test-e2e`
- Manual homelab smoke test with `admin` / `admin123`

---

## Step-File Matrix

| Step | New Files | Modified Files | Deleted Files |
|---|---|---|---|
| 1 | `types.go`, `cilium_crds.go` | `detect.go`, spec doc | — |
| 2 | — | `handler.go`, `routes.go`, `main.go` | — |
| 3 | — | Helm RBAC template, `networking_test.go` | — |
| 4 | `use-poll.ts`, `cilium-types.ts`, `CniOverview.tsx`, `CiliumConfigEditor.tsx`, `NetworkOverview.tsx` | `NetworkingDashboard.tsx` | `CniStatus.tsx` |
| 5 | `BgpStatus.tsx`, `IpamStatus.tsx`, `CiliumSubsystems.tsx` | `NetworkOverview.tsx` | — |
| 6 | E2E test | E2E CI config (if needed) | — |

**Total:** ~11 new files, ~9 modified files, 1 deleted file

---

## RBAC Requirements (Phase A)

```yaml
# Helm ClusterRole addition — CRD read access only, no exec
- apiGroups: ["cilium.io"]
  resources: ["ciliumbgpclusterconfigs", "ciliumbgppeerconfigs", "ciliumbgpnodeconfigs", "ciliumnodes", "ciliumendpoints"]
  verbs: ["get", "list", "watch"]
```

Phase B will add `pods/exec` as opt-in via `values.yaml`:
```yaml
ciliumAgent:
  execEnabled: false  # Enable for rich agent-level data (requires pods/exec RBAC)
```

---

## Phase B Scope (Future)

| Feature | Requires | Data |
|---|---|---|
| Agent collector (`agent_collector.go`) | `pods/exec` RBAC, SPDY executor, errgroup, singleflight cache | All agent-derived data |
| Encryption details | Agent exec | WireGuard per-node keys, handshake times, transfer stats |
| Service mesh details | Agent exec | Envoy redirect counts, L7 stats, deployment mode |
| ClusterMesh details | Agent exec | Connected clusters, sync status, heartbeats |
| Node connectivity island | Agent exec | Inter-node latency matrix (ICMP + HTTP probes) |
| Istio/Linkerd detection | CRD discovery + control plane health | `mesh_detect.go` with istiod/linkerd checks |
| Adaptive polling | `usePoll` enhancement | `isStable` callback, fast/slow interval switching |
| `lastUpdated` footer | `usePoll` enhancement | "Last checked Xs ago" display per island |

---

## Risk Mitigation

| Risk | Mitigation |
|---|---|
| CiliumBGPNodeConfig CRD not present (older Cilium or BGP disabled) | `hasCRD()` check by full GVR returns `configured: false`. Graceful degradation. |
| CiliumNode CRD absent (non-Cilium cluster) | IPAM returns `configured: false`. Only Overview + Health cards render. |
| Rate limit exhaustion on tab mount | Separate 60 req/min bucket for `/cilium/*` read endpoints. |
| Stale ConfigMap cache after edit | `InvalidateSubsystemCaches()` called from `HandleUpdateCNIConfig`. |
| Remote cluster selected | All `/cilium/*` handlers return `configured: false` (informers are local-only). |
| 3+ consecutive poll failures | `usePoll` pauses polling. Resumes on visibility change or manual refresh. |
| CiliumEndpoint list too large (1000+ endpoints) | `aggregateEndpoints` only counts by state, never returns individual endpoints. |
