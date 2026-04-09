# Networking Overview Islands — Design Spec

**Date:** 2026-04-09
**Status:** Draft
**Scope:** Phase A (Cilium-focused). Phase B (Calico/Flannel/generic) planned as follow-up.

## Summary

Expand the Networking Overview tab from a single CNI status island into a priority grid of independent islands showing live operational status for BGP peering, service mesh, encryption, ClusterMesh, IPAM, node connectivity, and endpoint health. Each island polls its own backend endpoint, uses detect-then-poll to skip unconfigured features, and supports adaptive polling for session-sensitive subsystems.

## Goals

- Remove the "CNI Plugin" heading from the overview page
- Surface live Cilium agent data (not just ConfigMap detection) for each subsystem
- Detect service mesh regardless of implementation (Cilium native, Istio, Linkerd)
- Gracefully degrade for non-Cilium CNIs and unreachable agents
- Minimize unnecessary polling — unconfigured features never poll

## Non-Goals

- Phase B: Calico/Flannel-specific backend support (future spec)
- Individual endpoint detail views (the overview shows aggregates only)
- WebSocket push for Cilium agent data (polling is sufficient given agent API latency)

---

## Architecture

### Approach: Per-Subsystem Endpoints + Detect-Then-Poll

Each subsystem gets its own backend endpoint. Frontend islands poll independently with their own intervals. If the backend reports `configured: false`, the island renders a muted state and stops polling.

**Why not a single aggregated endpoint?** One slow/dead agent would block all subsystem data. Independent endpoints keep failures isolated and allow different polling cadences.

### Backend: Cilium Agent Client

New `agent_client.go` provides an HTTP client that talks directly to cilium-agent pods on port 9879 (in-cluster, no port-forwarding needed).

**Communication pattern:**
1. List cilium-agent pods from informer cache
2. For each pod, HTTP GET to `pod-ip:9879/v1/{path}`
3. Per-request timeout: 5s
4. Aggregate results across nodes
5. Cache with singleflight + 30s TTL

If an agent pod is unreachable, that node is marked `"status": "unreachable"` in the response. Other nodes' data returns normally.

### Backend: Service Mesh Detection

New `mesh_detect.go` probes for three mesh implementations:

| Engine | Detection Method |
|---|---|
| Cilium native | `enable-envoy-config: "true"` in cilium-config ConfigMap |
| Istio | `networking.istio.io` CRD group present |
| Linkerd | `linkerd.io` CRD group present |

Returns the first detected engine. Cilium native mesh data comes from the agent API `/v1/status` (proxy section). Istio/Linkerd data comes from their respective CRDs and control plane pods.

---

## API Design

All endpoints require JWT auth and use impersonated clients for CRD reads.

### `GET /api/v1/networking/cilium/bgp`

**Detection:** `CiliumBGPClusterConfig` CRD exists.

**Agent API:** `/v1/bgp/peers` on each agent.

**Response:**
```json
{
  "configured": true,
  "config": {
    "clusterConfigs": [{ "name": "...", "nodeSelector": {...}, "bgpInstances": [...] }],
    "peerConfigs": [{ "name": "...", "peerASN": 65001, "peerAddress": "10.0.0.1" }]
  },
  "peers": [
    {
      "node": "node-1",
      "peerAddress": "10.0.0.1",
      "peerASN": 65001,
      "localASN": 65000,
      "sessionState": "established",
      "uptimeNs": 86400000000000,
      "routesReceived": 8,
      "routesAdvertised": 12,
      "lastError": ""
    }
  ]
}
```

### `GET /api/v1/networking/cilium/mesh`

**Detection:** Envoy enabled in ConfigMap OR Istio/Linkerd CRDs present.

**Response:**
```json
{
  "configured": true,
  "engine": "cilium",
  "envoy": {
    "enabled": true,
    "mode": "daemonset",
    "version": "1.28.0"
  },
  "mtls": {
    "enabled": true,
    "mode": "strict"
  },
  "l7Policies": 4,
  "proxyRedirects": 28
}
```

When engine is `"istio"` or `"linkerd"`, the response shape adapts:
```json
{
  "configured": true,
  "engine": "istio",
  "controlPlane": {
    "version": "1.22.0",
    "namespace": "istio-system",
    "healthy": true
  },
  "mtls": {
    "enabled": true,
    "mode": "strict"
  },
  "sidecars": {
    "injected": 45,
    "total": 52
  }
}
```

### `GET /api/v1/networking/cilium/encryption`

**Detection:** `enable-encryption` key in cilium-config ConfigMap.

**Agent API:** `/v1/debuginfo` (encryption section).

**Response:**
```json
{
  "configured": true,
  "mode": "wireguard",
  "status": "active",
  "nodesEncrypted": { "ready": 3, "total": 3 },
  "keyRotation": {
    "status": "healthy",
    "lastRotation": "2026-04-08T12:00:00Z"
  },
  "perNode": [
    { "node": "node-1", "status": "active", "publicKey": "abc..." }
  ]
}
```

### `GET /api/v1/networking/cilium/clustermesh`

**Detection:** `cluster-mesh-config` key in cilium-config ConfigMap.

**Agent API:** `/v1/clustermesh/status` on each agent.

**Response:**
```json
{
  "configured": true,
  "clusters": [
    {
      "name": "cluster-2",
      "status": "connected",
      "numNodes": 5,
      "numEndpoints": 120,
      "lastHeartbeat": "2026-04-09T10:30:00Z",
      "synced": { "nodes": true, "endpoints": true, "identities": true, "services": true }
    }
  ]
}
```

### `GET /api/v1/networking/cilium/ipam`

**Detection:** Always configured (every CNI has IPAM).

**Sources:** `CiliumNode` CRDs + agent API `/v1/ipam`.

**Response:**
```json
{
  "configured": true,
  "mode": "cluster-pool",
  "podCIDRs": ["10.244.0.0/16"],
  "allocated": 187,
  "available": 65349,
  "total": 65536,
  "exhaustionRisk": "none",
  "perNode": [
    { "node": "node-1", "allocated": 64, "available": 192, "podCIDR": "10.244.0.0/24" },
    { "node": "node-2", "allocated": 58, "available": 198, "podCIDR": "10.244.1.0/24" },
    { "node": "node-3", "allocated": 65, "available": 191, "podCIDR": "10.244.2.0/24" }
  ]
}
```

### `GET /api/v1/networking/cilium/health`

**Detection:** Always configured (agent healthz always available).

**Agent API:** `/v1/healthz` on each agent (includes inter-node probe results).

**Response:**
```json
{
  "configured": true,
  "nodes": [
    {
      "name": "node-1",
      "status": "healthy",
      "probes": [
        { "target": "node-2", "icmpLatencyNs": 400000, "httpLatencyNs": 1200000, "status": "ok" },
        { "target": "node-3", "icmpLatencyNs": 600000, "httpLatencyNs": 1500000, "status": "ok" }
      ]
    }
  ],
  "endpoints": {
    "total": 142,
    "ready": 138,
    "notReady": 3,
    "disconnecting": 1,
    "waiting": 0
  }
}
```

---

## Frontend Design

### Island Decomposition

The existing `CniStatus.tsx` (472 lines) is decomposed into focused islands:

| Island | File | Replaces |
|---|---|---|
| `CniOverview.tsx` | Overview + Health cards | `CniStatus` lines 198-301 |
| `CiliumConfig.tsx` | Configuration tab | `CniStatus` lines 334-471 |
| `BgpStatus.tsx` | BGP peering card | New |
| `EncryptionStatus.tsx` | Encryption details card | New |
| `ServiceMeshStatus.tsx` | Service mesh card | New |
| `ClusterMeshStatus.tsx` | ClusterMesh card | New |
| `IpamStatus.tsx` | IPAM utilization card | New |
| `NodeConnectivity.tsx` | Latency matrix card | New |
| `EndpointHealth.tsx` | Endpoint state bar | New |
| `NetworkOverview.tsx` | Priority grid layout shell | Replaces `CniStatus` import in `NetworkingDashboard` |

`CniStatus.tsx` is deleted after decomposition.

### Layout: Priority Grid

`NetworkOverview.tsx` renders a 2-column responsive grid ordered by operational priority:

| Row | Left | Right |
|---|---|---|
| 1 | CNI Overview | Health |
| 2 | BGP Status | Encryption |
| 3 | Service Mesh | ClusterMesh |
| 4 | IPAM | Node Connectivity |
| 5 | Endpoint Health (full-width, `col-span-2`) | — |

Not-configured islands render as muted cards (dimmed, no polling) to maintain grid structure.

### Shared Polling Hook: `usePoll`

```typescript
// lib/hooks/use-poll.ts
function usePoll<T>(url: string, opts: {
  interval: number;
  adaptiveKey?: (data: T) => boolean;  // true = healthy → slow poll
  fastInterval?: number;                // default 5000ms
  enabled?: boolean;                    // false = no polling
}): {
  data: Signal<T | null>;
  loading: Signal<boolean>;
  error: Signal<string | null>;
  lastUpdated: Signal<number>;
}
```

**Behavior:**
- Initial fetch on mount
- If response has `configured: false`, sets `enabled = false` internally
- Adaptive mode: checks `adaptiveKey(data)` after each poll. Healthy = `interval`, unhealthy = `fastInterval`
- Cleanup on unmount (clears timer)
- Exposes `lastUpdated` for "Last checked Xs ago" display

### Not-Configured Component

```tsx
// components/ui/NotConfiguredCard.tsx
function NotConfiguredCard({ title, message }: { title: string; message: string })
```

Renders a dimmed card with the subsystem title and a one-line message. Maintains grid slot to prevent layout shift.

### Polling Intervals

| Island | Base Interval | Adaptive | Fast Interval |
|---|---|---|---|
| CniOverview | One-shot | No | — |
| BgpStatus | 60s | Yes (session state) | 5s |
| EncryptionStatus | 60s | No | — |
| ServiceMeshStatus | 60s | No | — |
| ClusterMeshStatus | 60s | Yes (connection state) | 5s |
| IpamStatus | 60s | No | — |
| NodeConnectivity | 60s | No | — |
| EndpointHealth | 60s | No | — |

### Tab Structure

The existing Status/Configuration tabs remain:

- **Status tab** → `NetworkOverview` (the priority grid with all islands)
- **Configuration tab** → `CiliumConfig` (extracted from old `CniStatus`, Cilium-only)

---

## Edge Cases

### Agent Unreachable
Per-node agent failures return `"status": "unreachable"` for that node. Other nodes return normally. Frontend shows a yellow warning indicator on affected rows. No error banner — partial data is still useful.

### Non-Cilium CNIs
All `/networking/cilium/*` endpoints return `{ configured: false }`. The overview degrades to just the Overview + Health cards (row 1). Phase B adds CNI-specific backends for Calico and Flannel.

### Large Clusters (>20 nodes)
- **Node Connectivity matrix:** Capped at 20 nodes. Beyond that, show summary stats (min/max/avg latency per node) with an expandable detail.
- **IPAM per-node:** Show top 5 most-utilized nodes by default, expandable for full list.
- **Endpoint Health:** Always aggregated counts, never individual endpoint listing.

### Polling Lifecycle
1. Island mounts → initial fetch
2. Check `configured` in response
3. If `false` → render `NotConfiguredCard`, no timer
4. If `true` → start polling at base interval
5. Adaptive islands: after each response, evaluate health key. Unhealthy → switch to fast interval. Healthy → switch to slow.
6. Navigate away from Overview tab → `useEffect` cleanup clears all timers
7. Navigate back → fresh initial fetch, timers restart

---

## Backend Package Structure

```
backend/internal/networking/
├── handler.go           # Existing + 6 new handler methods
├── detect.go            # Existing CNI/feature detection (reused)
├── cilium.go            # Existing ConfigMap management
├── hubble_client.go     # Existing Hubble gRPC client
├── agent_client.go      # NEW: HTTP client for cilium-agent REST API (port 9879)
├── mesh_detect.go       # NEW: Istio/Linkerd CRD detection + Cilium Envoy detection
├── types.go             # NEW: Response structs for all 6 endpoints
└── networking_test.go   # Existing + new tests
```

### Route Registration

```go
// In routes.go, under /api/v1/networking group:
r.Get("/cilium/bgp", netHandler.HandleCiliumBGP)
r.Get("/cilium/mesh", netHandler.HandleCiliumMesh)
r.Get("/cilium/encryption", netHandler.HandleCiliumEncryption)
r.Get("/cilium/clustermesh", netHandler.HandleCiliumClusterMesh)
r.Get("/cilium/ipam", netHandler.HandleCiliumIPAM)
r.Get("/cilium/health", netHandler.HandleCiliumHealth)
```

---

## Testing

### Backend
- **Unit tests** for agent client: mock HTTP server, verify request paths, timeout handling, partial failure aggregation
- **Unit tests** for mesh detection: mock CRD discovery for each engine type
- **Unit tests** for each handler: verify `configured: false` short-circuit, RBAC enforcement, response shape
- **Integration test** for detect-then-poll: verify ConfigMap flags correctly gate agent queries

### Frontend
- **Component tests** for each island: verify loading state, configured state, not-configured state, error state
- **usePoll hook test:** verify adaptive interval switching, cleanup on unmount, enabled flag behavior
- **E2E test:** Navigate to Networking Overview, verify islands render with mock data

---

## Phase B (Future)

Extend the overview tab for non-Cilium CNIs:

- **Calico:** Felix status, BIRD BGP peering, IPPool utilization, Typha health via calico-node API
- **Flannel:** VXLAN/host-gw backend status, subnet allocation from node annotations
- **Generic:** Basic connectivity health via pod-to-pod probes, IPAM from node annotations

Phase B reuses the same frontend islands, `usePoll` hook, and `NetworkOverview` grid. Backend adds `/networking/calico/*` and `/networking/flannel/*` endpoint groups. The `NetworkOverview` layout conditionally renders the right set of islands based on detected CNI.

---

## Security

- All endpoints require JWT authentication
- CRD reads use impersonated Kubernetes clients (RBAC enforced)
- Agent API calls use the backend's service account (agent API is not user-facing)
- No secrets exposed in responses (WireGuard public keys are safe, private keys never leave the agent)
- Rate limiting: same 30 req/min bucket as existing networking endpoints
