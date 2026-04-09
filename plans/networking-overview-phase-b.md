# Networking Overview Phase B — Cilium Agent Collector + Enrichment

**Spec:** This document (self-contained)
**Depends on:** Phase A (PR #157, merged), IPAM fix (PR #158, merged)
**Branch:** `feat/networking-phase-b`
**Estimated Steps:** 3

---

## Architecture Overview

### Problem

Phase A sources all networking data from CRDs and ConfigMaps. This covers the most operationally useful data (BGP peering state, IPAM allocation, encryption mode), but richer diagnostics are locked behind the Cilium agent's local status API — accessible only via `cilium-dbg status -o json` inside agent pods.

### Solution: Opt-In Agent Collector

A new `CiliumAgentCollector` execs `cilium-dbg status -o json` into cilium-agent pods using the service account's own credentials (not user impersonation). This is gated behind `ciliumAgent.execEnabled: false` (default off) in Helm values.

**Why service account instead of user impersonation:** The SPDY executor for non-interactive exec uses the `rest.Config` transport directly. User impersonation headers on the base config interfere with the SPDY upgrade handshake when the request is made from inside the cluster. More importantly, the agent collector is a system-level diagnostic — it collects the same data regardless of which user views the dashboard. Caching agent-collected data under a per-user key would defeat singleflight coalescing.

**Compensating controls for the impersonation exception:**
1. Opt-in via Helm (off by default) — no `pods/exec` in ClusterRole unless explicitly enabled
2. Pod label validation: only exec into pods matching `app.kubernetes.io/name=cilium-agent` or `k8s-app=cilium`
3. Namespace allowlist: only `kube-system` and `cilium` (reuses existing `ciliumSearchNamespaces`)
4. Hardcoded command: only `cilium-dbg status -o json` — never user-supplied
5. Audit logging: every exec invocation logged via both `slog` (operational) AND `audit.Logger` (PostgreSQL audit trail) with pod name, namespace, node, outcome, duration
6. Output size cap: 1 MB `io.LimitReader` on stdout buffer — if exceeded, the truncated JSON will fail to parse, the node is marked as failed in `agentNodeResult.Error`, and a warning is logged. The node falls back to CRD-only data.
7. Prometheus metrics: `kubecenter_cilium_agent_exec_total` (counter, labels: outcome), `kubecenter_cilium_agent_exec_duration_seconds` (histogram)

### Data Flow

```
┌──────────────────────────────────────────────────────────┐
│                    HTTP Request                           │
│   GET /api/v1/networking/cilium/subsystems                │
│   GET /api/v1/networking/cilium/connectivity              │
└──────────────┬───────────────────────────────────────────┘
               │
               ▼
┌──────────────────────────────────────────────────────────┐
│  Handler: singleflight + 30s cache (per endpoint)        │
│                                                          │
│  ┌─────────────────┐    ┌──────────────────────────┐     │
│  │ CRD Data (P.A)  │    │ Agent Data (P.B, opt-in) │     │
│  │ - ConfigMap      │    │ - singleflight "agent"   │     │
│  │ - CiliumNode     │    │ - 30s shared cache       │     │
│  │ - CiliumEndpoint │    │ - errgroup.SetLimit(5)   │     │
│  └────────┬────────┘    │ - 5s timeout per pod     │     │
│           │              │ - 30s outer timeout       │     │
│           │              └─────────┬────────────────┘     │
│           │                        │                      │
│           ▼                        ▼                      │
│  ┌─────────────────────────────────────────────────┐     │
│  │        Merge: CRD base + agent enrichment       │     │
│  │  (agent fields are additive, never replace CRD) │     │
│  └─────────────────────────────────────────────────┘     │
└──────────────────────────────────────────────────────────┘
```

**Key design: shared agent cache.** Both the subsystems enrichment and connectivity endpoint consume the same agent collection result. A single singleflight key `"agent-collect"` prevents duplicate exec bursts when both islands poll within the same 30s window. Both the agent cache and the subsystems cache use a 30s TTL — these are intentionally aligned. `InvalidateCaches()` clears both.

### Phase B Feature Matrix

| Feature | Backend | Frontend | Depends On |
|---|---|---|---|
| Agent collector | `agent_exec.go`, `agent_types.go` | — | Helm opt-in |
| Enriched encryption | Merge WireGuard peer data into subsystems response | `CiliumSubsystems.tsx` shows handshake times, transfer stats | Agent collector |
| Enriched service mesh | Merge Envoy proxy data into subsystems response | `CiliumSubsystems.tsx` shows deployment mode, redirect counts | Agent collector |
| Enriched ClusterMesh | Merge remote cluster list into subsystems response | `CiliumSubsystems.tsx` shows connected clusters table | Agent collector |
| Node connectivity | New endpoint from agent health data | New `NodeConnectivity.tsx` island | Agent collector |
| Last updated footer | — | Static timestamp in card footers, updated on each poll cycle | `usePoll` extension |

**Deferred to Phase C:** Istio/Linkerd detection (no test environment, needs per-mesh adapter pattern for deep integration), adaptive polling (fixed intervals sufficient for infrastructure diagnostics), full connectivity matrix (`cilium-dbg connectivity test` — destructive/long-running).

---

## Design Decisions

| Decision | Resolution | Rationale |
|---|---|---|
| Agent collector uses service account, not impersonation | Accepted exception with 7 compensating controls | SPDY transport + system-level diagnostic + cache coalescing |
| Shared agent cache for subsystems + connectivity | Single singleflight key `"agent-collect"`, 30s TTL (aligned with subsystems cache) | Prevents 2N execs on page load (N nodes × 2 endpoints) |
| `InvalidateCaches()` clears agent cache too | Agent collector gains `InvalidateCache()` method, called by handler | ConfigMap update should refresh all data sources |
| Outer timeout for collection | 30s hard cap via `context.WithTimeout` | Bounds worst case: 20-node cluster with all timeouts = ceil(20/5)×5s = 20s |
| Partial success handling | Per-node results; enrichment merges successful nodes, skips failed | Graceful degradation: 3/5 pods succeed → 3 nodes enriched, 2 CRD-only |
| `cilium-dbg` only (no `cilium` fallback) | Cilium renamed binary in v1.13 (mid-2023); all supported versions use `cilium-dbg` | YAGNI — add fallback only if someone reports it on an older cluster |
| `NodeConnectivity` visibility when exec disabled | Rendered in SSR with "exec disabled" placeholder | Fresh islands must appear in SSR output to be bundled (per project memory) |
| Polling intervals | Fixed: 30s for subsystems (matches cache TTL), 60s for connectivity | Backend cache is 30s; polling faster is wasted; this is a dashboard, not a trading terminal |
| Last updated display | Static timestamp updated on each fetch cycle (no ticking timer) | A 1s `setInterval` to show "12s ago" vs "13s ago" adds runtime cost for zero operational value |
| `pods/exec` RBAC | Conditional block in existing ClusterRole (cluster-wide) | Simpler than two namespace-scoped Roles + RoleBindings. Application-level controls (label validation, namespace allowlist) provide defense-in-depth. The alternative of namespace-scoped Roles was considered but rejected: the pod listing step already requires cluster-wide `pods:list`, and the Helm upgrade cleanly removes the rule on disable. |
| Non-Cilium connectivity endpoint | Returns `{ configured: false }` (same as all Cilium endpoints) | Consistent with Phase A pattern |
| Config wiring | `Config.CiliumAgent.ExecEnabled` → `KUBECENTER_CILIUMAGENT_EXECENABLED` | Follows koanf nested struct → underscore-joined env var convention |
| No `ExecEnabled` bool on Handler | Use `h.AgentCollector != nil` as sole gate | Single source of truth; redundant bool invites divergence |
| 1 MB output cap exceeded | Truncated JSON fails to parse → node marked as failed → CRD-only fallback | Logged as warning with pod name and output size |
| Agent result types unexported | `agentCollectionResult` / `agentNodeResult` are internal; only enrichment merge function consumes them | Keeps public API surface clean; JSON tags are for parsing cilium-dbg output, not HTTP responses |
| Connectivity response: flat interface | Simple struct with all fields (not discriminated union) | Matches existing BGP/IPAM response pattern; frontend checks `configured` then `nodes.length` |

---

## Agent Status JSON: Fields We Parse

Minimal subset of `cilium-dbg status -o json` output. We define our own structs — no Cilium API model imports.

```
{
  "encryption": {
    "mode": "Wireguard",                          → EncryptionInfo.Mode (enriched)
    "wireguard": {
      "interfaces": [{
        "name": "cilium_wg0",                     → per-node interface name
        "public-key": "abc123...",                 → WireGuardNode.PublicKey
        "listen-port": 51871,                      → WireGuardNode.ListenPort
        "peer-count": 2,                           → WireGuardNode.PeerCount
        "peers": [{
          "public-key": "def456...",               → WireGuardPeer.PublicKey
          "endpoint": "192.168.1.10:51871",        → WireGuardPeer.Endpoint
          "last-handshake-time": "2026-04-09T...", → WireGuardPeer.LastHandshake
          "transfer-rx": 12345678,                 → WireGuardPeer.TransferRx
          "transfer-tx": 87654321                  → WireGuardPeer.TransferTx
        }]
      }]
    }
  },
  "cluster-mesh": {
    "clusters": [{
      "name": "cluster-west",                     → RemoteCluster.Name
      "connected": true,                           → RemoteCluster.Connected
      "ready": true,                               → RemoteCluster.Ready
      "status": "ready",                           → RemoteCluster.Status
      "num-nodes": 5,                              → RemoteCluster.NumNodes
      "num-endpoints": 42,                         → RemoteCluster.NumEndpoints
      "num-shared-services": 12,                   → RemoteCluster.NumSharedServices
      "num-failures": 0,                           → RemoteCluster.NumFailures
      "last-failure": ""                           → RemoteCluster.LastFailure
    }]
  },
  "proxy": {
    "envoy-deployment-mode": "embedded",           → ProxyInfo.DeploymentMode
    "total-redirects": 150,                        → ProxyInfo.TotalRedirects
    "total-ports": 3                               → ProxyInfo.TotalPorts
  },
  "cluster": {
    "ciliumHealth": {
      "state": "Ok",                               → NodeHealth.State
      "msg": ""                                    → NodeHealth.Message
    }
  }
}
```

---

## Step 1: Backend — Agent Collector + Enrichment + Config + Helm

**Files:** 2 new, 5 modified
**Goal:** Complete backend: agent types, collector, enrichment merge, connectivity handler, config wiring, Helm plumbing

### Create `backend/internal/networking/agent_types.go`

Minimal Go structs for parsing `cilium-dbg status -o json`. All unexported — internal parsing only.

```go
// ciliumAgentStatus is a minimal subset of cilium-dbg's StatusResponse.
type ciliumAgentStatus struct {
    Encryption  *agentEncryption  `json:"encryption,omitempty"`
    ClusterMesh *agentClusterMesh `json:"cluster-mesh,omitempty"`
    Proxy       *agentProxy       `json:"proxy,omitempty"`
    Cluster     *agentCluster     `json:"cluster,omitempty"`
}

type agentEncryption struct {
    Mode      string          `json:"mode,omitempty"`
    Msg       string          `json:"msg,omitempty"`
    Wireguard *agentWireguard `json:"wireguard,omitempty"`
}

type agentWireguard struct {
    Interfaces []agentWGInterface `json:"interfaces"`
}

type agentWGInterface struct {
    Name       string         `json:"name,omitempty"`
    PublicKey  string         `json:"public-key,omitempty"`
    ListenPort int64         `json:"listen-port,omitempty"`
    PeerCount  int64         `json:"peer-count,omitempty"`
    Peers      []agentWGPeer `json:"peers"`
}

type agentWGPeer struct {
    PublicKey         string `json:"public-key,omitempty"`
    Endpoint          string `json:"endpoint,omitempty"`
    LastHandshakeTime string `json:"last-handshake-time,omitempty"`
    TransferRx        int64  `json:"transfer-rx,omitempty"`
    TransferTx        int64  `json:"transfer-tx,omitempty"`
}

type agentClusterMesh struct {
    Clusters []agentRemoteCluster `json:"clusters"`
}

type agentRemoteCluster struct {
    Name              string `json:"name,omitempty"`
    Connected         bool   `json:"connected,omitempty"`
    Ready             bool   `json:"ready,omitempty"`
    Status            string `json:"status,omitempty"`
    NumNodes          int64  `json:"num-nodes,omitempty"`
    NumEndpoints      int64  `json:"num-endpoints,omitempty"`
    NumSharedServices int64  `json:"num-shared-services,omitempty"`
    NumFailures       int64  `json:"num-failures,omitempty"`
    LastFailure       string `json:"last-failure,omitempty"`
}

type agentProxy struct {
    DeploymentMode string `json:"envoy-deployment-mode,omitempty"`
    TotalRedirects int64  `json:"total-redirects,omitempty"`
    TotalPorts     int64  `json:"total-ports,omitempty"`
}

type agentCluster struct {
    CiliumHealth *agentCiliumHealth `json:"ciliumHealth,omitempty"`
}

type agentCiliumHealth struct {
    State string `json:"state,omitempty"`
    Msg   string `json:"msg,omitempty"`
}

// agentCollectionResult holds parsed results from all agent pods (unexported — internal only).
type agentCollectionResult struct {
    nodes     []agentNodeResult
    collected time.Time
    partial   bool // true if some pods failed
}

type agentNodeResult struct {
    nodeName string
    podName  string
    status   *ciliumAgentStatus // nil if exec failed
    err      string
}
```

### Create `backend/internal/networking/agent_exec.go`

The `CiliumAgentCollector` struct:

```go
type CiliumAgentCollector struct {
    k8sClient    *k8s.ClientFactory
    auditLogger  audit.Logger
    logger       *slog.Logger
    // Singleflight + cache
    group     singleflight.Group
    cacheMu   sync.RWMutex
    cache     *agentCollectionResult
    cacheTime time.Time
    // Prometheus metrics
    execTotal    *prometheus.CounterVec   // labels: outcome
    execDuration prometheus.Histogram
}

const (
    agentCacheTTL      = 30 * time.Second
    agentOuterTimeout  = 30 * time.Second
    agentExecTimeout   = 5 * time.Second
    agentMaxConcurrent = 5
    agentMaxOutput     = 1 << 20 // 1 MB stdout cap
    agentContainer     = "cilium-agent"
)

var agentCommand = []string{"cilium-dbg", "status", "-o", "json"}

// Reuse ciliumSearchNamespaces from cilium.go — no duplicate constant.

var agentPodLabels = []string{
    "app.kubernetes.io/name=cilium-agent",
    "k8s-app=cilium",
}

// NewCiliumAgentCollector creates a collector for Cilium agent diagnostics.
func NewCiliumAgentCollector(k8sClient *k8s.ClientFactory, auditLogger audit.Logger, logger *slog.Logger) *CiliumAgentCollector {
    execTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "kubecenter_cilium_agent_exec_total",
        Help: "Total cilium-agent exec invocations by outcome",
    }, []string{"outcome"})
    execDuration := prometheus.NewHistogram(prometheus.HistogramOpts{
        Name:    "kubecenter_cilium_agent_exec_duration_seconds",
        Help:    "Duration of cilium-agent exec invocations",
        Buckets: []float64{0.5, 1, 2, 5, 10, 30},
    })
    prometheus.MustRegister(execTotal, execDuration)

    return &CiliumAgentCollector{
        k8sClient:    k8sClient,
        auditLogger:  auditLogger,
        logger:       logger,
        execTotal:    execTotal,
        execDuration: execDuration,
    }
}
```

**`Collect(ctx) (*agentCollectionResult, error)` method:**

1. Check cache under RLock — if fresh, return cached
2. Singleflight on key `"agent-collect"`
3. `context.WithTimeout(ctx, agentOuterTimeout)` — 30s hard cap
4. List pods: try each `ciliumSearchNamespaces` × `agentPodLabels` combo until pods found
5. Validate each pod: label match, `Running` phase, expected namespace
6. `errgroup.WithContext` + `g.SetLimit(agentMaxConcurrent)`
7. Per-pod: `context.WithTimeout(gCtx, agentExecTimeout)`, build exec request URL using existing `RESTClient().Post().Resource("pods").SubResource("exec").Param(...)` pattern from `pods.go:256`
8. Create `remotecommand.NewSPDYExecutor(baseConfig, "POST", execReq.URL())`
9. `executor.StreamWithContext(execCtx, StreamOptions{Stdout: io.LimitReader(&buf, agentMaxOutput), Stderr: &stderrBuf})`
10. Parse stdout JSON into `ciliumAgentStatus` — if parse fails (including due to 1MB truncation), log warning with pod name and output size, mark node as failed
11. Always return `nil` from `g.Go()` — errors captured per-node in results (graceful degradation)
12. Structured slog: `h.logger.Info("agent exec", "pod", podName, "node", nodeName, "duration", elapsed, "outcome", "success|timeout|parse_error|exec_error")`
13. Audit log: `h.auditLogger.Log(ctx, audit.Entry{Action: "cilium-agent-exec", Resource: podName, ...})`
14. Prometheus: increment `execTotal` with outcome label, observe `execDuration`
15. Store in cache, set `partial: true` if any node has non-empty `err`

**`InvalidateCache()` method:** Clears cache under write lock.

**`execInPod(ctx, namespace, podName, container, command) ([]byte, []byte, error)` helper:**

Uses `BaseConfig()` and `BaseClientset()` (service account credentials). Builds the exec URL using the `.Param()` chain pattern matching `pods.go:256-266`. Returns stdout, stderr, error.

### Extend response types in `backend/internal/networking/types.go`

Add agent-sourced fields to existing types (all `omitempty` — absent when exec disabled):

```go
// Extend EncryptionInfo
type EncryptionInfo struct {
    // Phase A fields (unchanged)
    Enabled        bool   `json:"enabled"`
    Mode           string `json:"mode"`
    NodesEncrypted int    `json:"nodesEncrypted"`
    NodesTotal     int    `json:"nodesTotal"`
    // Phase B fields (agent-sourced, optional)
    WireGuardNodes []WireGuardNode `json:"wireGuardNodes,omitempty"`
}

type WireGuardNode struct {
    NodeName   string          `json:"nodeName"`
    PublicKey  string          `json:"publicKey"`
    ListenPort int64          `json:"listenPort"`
    PeerCount  int64          `json:"peerCount"`
    Peers      []WireGuardPeer `json:"peers"`
}

type WireGuardPeer struct {
    PublicKey     string `json:"publicKey"`
    Endpoint      string `json:"endpoint"`
    LastHandshake string `json:"lastHandshake"`
    TransferRx    int64  `json:"transferRx"`
    TransferTx    int64  `json:"transferTx"`
}

// Extend MeshInfo
type MeshInfo struct {
    Enabled        bool   `json:"enabled"`
    Engine         string `json:"engine"`
    DeploymentMode string `json:"deploymentMode,omitempty"` // Phase B (agent)
    TotalRedirects int64  `json:"totalRedirects,omitempty"` // Phase B (agent)
    TotalPorts     int64  `json:"totalPorts,omitempty"`     // Phase B (agent)
}

// Extend ClusterMeshInfo
type ClusterMeshInfo struct {
    Enabled        bool            `json:"enabled"`
    RemoteClusters []RemoteCluster `json:"remoteClusters,omitempty"` // Phase B (agent)
}

type RemoteCluster struct {
    Name              string `json:"name"`
    Connected         bool   `json:"connected"`
    Ready             bool   `json:"ready"`
    Status            string `json:"status"`
    NumNodes          int64  `json:"numNodes"`
    NumEndpoints      int64  `json:"numEndpoints"`
    NumSharedServices int64  `json:"numSharedServices"`
    NumFailures       int64  `json:"numFailures"`
    LastFailure       string `json:"lastFailure,omitempty"`
}

// New: Connectivity response (flat interface — matches BGP/IPAM pattern)
type CiliumConnectivityResponse struct {
    Configured  bool               `json:"configured"`
    ExecEnabled bool               `json:"execEnabled,omitempty"` // omitempty: absent when configured=false
    Nodes       []NodeConnectivity `json:"nodes"`
    CollectedAt string             `json:"collectedAt,omitempty"`
    Partial     bool               `json:"partial,omitempty"`
}

type NodeConnectivity struct {
    NodeName    string `json:"nodeName"`
    HealthState string `json:"healthState"` // Ok, Warning, Failure
    Message     string `json:"message,omitempty"`
}
```

### Modify `backend/internal/networking/handler.go`

**Add to Handler struct:**
```go
AgentCollector *CiliumAgentCollector // nil when exec disabled
```

No `ExecEnabled` bool — use `h.AgentCollector != nil` as the sole gate.

**Extend `InvalidateCaches()`:**
```go
func (h *Handler) InvalidateCaches() {
    // ... existing cache clears ...

    // Clear agent cache if collector is enabled
    if h.AgentCollector != nil {
        h.AgentCollector.InvalidateCache()
    }

    atomic.AddUint64(&h.cacheGen, 1)
}
```

**Modify `fetchSubsystems()`:**

After the existing errgroup that fetches CRD data (CiliumNodes + CiliumEndpoints), add an agent enrichment step:

```go
// After g.Wait() for CRD data...

// Agent enrichment (opt-in, additive)
if h.AgentCollector != nil {
    agentResult, err := h.AgentCollector.Collect(ctx)
    if err != nil {
        h.Logger.Warn("agent collection failed, returning CRD-only data", "error", err)
    } else {
        mergeAgentIntoSubsystems(&resp, agentResult)
    }
}
```

**`mergeAgentIntoSubsystems(resp, agentResult)`:**
- Nil-guard all response sub-structs before merging (e.g., `if resp.Encryption == nil { return }`)
- Encryption: iterate agent nodes, build `WireGuardNode` slice from `encryption.wireguard.interfaces`
- Mesh: copy `proxy.DeploymentMode`, `proxy.TotalRedirects`, `proxy.TotalPorts`
- ClusterMesh: build `RemoteCluster` slice from `cluster-mesh.clusters`
- Only merge data from nodes where `agentNodeResult.status != nil` (skip failed nodes)

**New handler: `HandleCiliumConnectivity`:**
1. Auth check + `isCiliumLocal()` guard → if not Cilium: `CiliumConnectivityResponse{Configured: false, Nodes: []NodeConnectivity{}}`
2. If `h.AgentCollector == nil` → `CiliumConnectivityResponse{Configured: true, Nodes: []NodeConnectivity{}}`
3. Call `h.AgentCollector.Collect(ctx)` (uses shared cache)
4. Extract `cluster.ciliumHealth` from each node's agent status
5. Return `CiliumConnectivityResponse` with per-node health, `ExecEnabled: true`

### Modify `backend/internal/server/routes.go`

Add connectivity endpoint under existing `/cilium` group:

```go
cr.Get("/connectivity", h.HandleCiliumConnectivity)
```

### Modify `backend/internal/config/config.go`

Add to Config struct:

```go
type Config struct {
    // ... existing fields ...
    CiliumAgent CiliumAgentConfig
}

type CiliumAgentConfig struct {
    ExecEnabled bool `koanf:"execenabled"`
}
```

Env var: `KUBECENTER_CILIUMAGENT_EXECENABLED=true|false`

### Modify `helm/kubecenter/values.yaml`

```yaml
# -- Cilium agent deep diagnostics (requires pods/exec RBAC)
ciliumAgent:
  # -- Enable exec-based diagnostics for WireGuard details, ClusterMesh, Envoy stats
  execEnabled: false
```

### Modify `helm/kubecenter/templates/clusterrole.yaml`

Add conditional `pods/exec` rule:

```yaml
{{- if .Values.ciliumAgent.execEnabled }}
  # Cilium agent exec — runs cilium-dbg status for deep diagnostics
  # Cluster-wide scope: pod listing already requires cluster-wide pods:list.
  # Application-level controls (label validation, namespace allowlist, hardcoded command) provide defense-in-depth.
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
{{- end }}
```

### Modify `helm/kubecenter/templates/configmap-app.yaml`

```yaml
KUBECENTER_CILIUMAGENT_EXECENABLED: {{ .Values.ciliumAgent.execEnabled | quote }}
```

### Modify `backend/cmd/kubecenter/main.go`

Conditionally construct agent collector:

```go
var agentCollector *networking.CiliumAgentCollector
if cfg.CiliumAgent.ExecEnabled {
    agentCollector = networking.NewCiliumAgentCollector(k8sClient, auditLogger, logger)
    logger.Info("cilium agent exec collector enabled")
}

networkingHandler := &networking.Handler{
    // ... existing fields ...
    AgentCollector: agentCollector,
}
```

### Update `backend/internal/networking/detect.go`

Update the deferred comment:

```go
// DetectMesh returns the active service mesh engine based on cached feature detection.
// Returns "cilium" if Cilium's Envoy-based mesh is enabled, "none" otherwise.
// Istio/Linkerd detection is deferred to Phase C (needs per-mesh adapter + test environment).
```

### Backend unit tests in `backend/internal/networking/networking_test.go`

**Agent collector tests (4):**
- `TestAgentCollector_CacheHit` — verify cached result returned within TTL
- `TestAgentCollector_PodValidation` — reject pods not matching expected labels/namespace
- `TestAgentCollector_ParseMinimalJSON` — parse each agent type struct from sample JSON
- `TestAgentCollector_PartialFailure` — 3/5 pods succeed, result marked `partial: true`

**Enrichment tests (4):**
- `TestMergeAgentIntoSubsystems_WireGuard` — encryption enriched with peer data
- `TestMergeAgentIntoSubsystems_ClusterMesh` — remote cluster list populated
- `TestMergeAgentIntoSubsystems_Proxy` — deployment mode and redirect counts merged
- `TestMergeAgentIntoSubsystems_NilGuards` — merge with nil Encryption/MeshInfo/ClusterMeshInfo sub-structs does not panic

**Connectivity tests (2):**
- `TestHandleCiliumConnectivity_ExecDisabled` — returns `configured: true, nodes: []` when `AgentCollector == nil`
- `TestHandleCiliumConnectivity_NotCilium` — returns `configured: false` when CNI is not Cilium

### Verification
- `go build ./cmd/kubecenter/...`
- `go vet ./...`
- `go test ./internal/networking/... -v -count=1`
- `make helm-lint`
- `make helm-template` — verify conditional ClusterRole rule appears/disappears

---

## Step 2: Frontend — Types + Islands + Integration

**Files:** 1 new, 4 modified
**Goal:** Display agent-enriched data, node connectivity, last-updated timestamps

### Modify `frontend/lib/hooks/use-poll.ts`

Add `lastFetchedAt` signal to return type (non-breaking — existing callers ignore it):

```typescript
interface UsePollResult<T> {
    data: Signal<T | null>;
    loading: Signal<boolean>;
    error: Signal<string | null>;
    refetch: () => void;
    lastFetchedAt: Signal<Date | null>;  // NEW
}
```

Inside the fetch success path, set `lastFetchedAt.value = new Date()`.

### Modify `frontend/lib/cilium-types.ts`

Add Phase B enrichment types and connectivity response:

```typescript
// Extend existing types with optional agent-sourced fields
export interface EncryptionInfo {
    enabled: boolean;
    mode: string;
    nodesEncrypted: number;
    nodesTotal: number;
    wireGuardNodes?: WireGuardNode[];  // Phase B
}

export interface WireGuardNode {
    nodeName: string;
    publicKey: string;
    listenPort: number;
    peerCount: number;
    peers: WireGuardPeer[];
}

export interface WireGuardPeer {
    publicKey: string;
    endpoint: string;
    lastHandshake: string;
    transferRx: number;
    transferTx: number;
}

export interface MeshInfo {
    enabled: boolean;
    engine: string;
    deploymentMode?: string;    // Phase B (agent)
    totalRedirects?: number;    // Phase B (agent)
    totalPorts?: number;        // Phase B (agent)
}

export interface ClusterMeshInfo {
    enabled: boolean;
    remoteClusters?: RemoteCluster[];  // Phase B (agent)
}

export interface RemoteCluster {
    name: string;
    connected: boolean;
    ready: boolean;
    status: string;
    numNodes: number;
    numEndpoints: number;
    numSharedServices: number;
    numFailures: number;
    lastFailure?: string;
}

// Flat interface — matches existing BGP/IPAM response pattern
export interface CiliumConnectivityResponse {
    configured: boolean;
    execEnabled: boolean;
    nodes: NodeConnectivity[];
    collectedAt?: string;
    partial?: boolean;
}

export interface NodeConnectivity {
    nodeName: string;
    healthState: string;
    message?: string;
}
```

### Modify `frontend/islands/CiliumSubsystems.tsx`

Extend the existing four-section layout with Phase B data when present:

**Encryption section:**
- Existing: mode badge, "X/Y nodes encrypted"
- Phase B: if `wireGuardNodes` present, expandable detail showing per-node:
  - Public key (truncated to first 8 chars + `...`)
  - Peer count
  - For each peer: endpoint, last handshake (relative time), transfer rx/tx (human-readable bytes)

**Service Mesh section:**
- Existing: engine badge, enabled/disabled
- Phase B: if `deploymentMode` present, show "embedded"/"external" badge
- If `totalRedirects`/`totalPorts` present, show as key-value pairs

**ClusterMesh section:**
- Existing: enabled/disabled badge
- Phase B: if `remoteClusters` present, show table:
  - Cluster name, connected (green/red dot), nodes, endpoints, shared services, failures

**Endpoints section:** Unchanged (no agent enrichment needed)

**Last updated footer:** Inline static timestamp from `lastFetchedAt`:
```tsx
{lastFetchedAt.value && (
    <span class="text-xs text-[var(--text-muted)]">
        Updated {lastFetchedAt.value.toLocaleTimeString()}
    </span>
)}
```

No separate component file, no ticking timer. The timestamp updates on each poll cycle.

### Create `frontend/islands/NodeConnectivity.tsx`

New island rendered in `NetworkOverview.tsx` grid when CNI is Cilium:

```tsx
export default function NodeConnectivity() {
    const { data, loading, error, lastFetchedAt } = usePoll<CiliumConnectivityResponse>(
        "/v1/networking/cilium/connectivity",
        { interval: 60000 }
    );

    // If not configured → muted card (same pattern as BgpStatus)
    // If configured but nodes empty → placeholder card:
    //   "Enable ciliumAgent.execEnabled in Helm values for node connectivity data"
    // If configured + nodes present → node health table:
    //   NodeName | Health State (colored dot) | Message
    //   Green = Ok, Yellow = Warning, Red = Failure
    // Footer: inline static timestamp from lastFetchedAt
}
```

Grid placement: full-width row below Subsystems (`md:col-span-2`).

### Modify `frontend/islands/NetworkOverview.tsx`

Add `NodeConnectivity` to the Cilium-specific section:

```tsx
{isCilium && (
    <>
        <BgpStatus />
        <IpamStatus />
        <CiliumSubsystems />           {/* full-width */}
        <NodeConnectivity />            {/* full-width, NEW */}
    </>
)}
```

### Modify `frontend/islands/BgpStatus.tsx` and `IpamStatus.tsx`

Add inline last-updated footer to both islands. Minimal change — destructure `lastFetchedAt` from existing `usePoll` return and add the inline `<span>`.

### Verification
- `deno lint frontend/`
- `deno fmt --check frontend/`
- `deno check frontend/islands/CiliumSubsystems.tsx`
- `deno check frontend/islands/NodeConnectivity.tsx`
- `deno check frontend/islands/NetworkOverview.tsx`
- Manual browser check: verify grid layout renders correctly

---

## Step 3: E2E Tests + Smoke Test

**Files:** 1 new (possibly)
**Goal:** E2E validation and homelab smoke test

### Playwright E2E test in `e2e/`

Add test cases to existing networking E2E or create `networking-phase-b.spec.ts`:

- Navigate to `/networking` → Overview tab
- Verify CiliumSubsystems card renders (Phase A — regression check)
- Verify "Updated" timestamp appears on BGP, IPAM, Subsystems cards
- Verify NodeConnectivity card renders (either with data or placeholder)
- Verify no console errors on initial load and after 2 polling cycles

**CI consideration:** Kind cluster in CI does not have Cilium, so Cilium-specific islands will show `configured: false`. Test for graceful degradation (no errors, muted cards visible). For full validation, rely on homelab smoke test.

### Homelab smoke test

Deploy to homelab cluster (Cilium 1.19.1, WireGuard enabled):

1. **Without exec:** `ciliumAgent.execEnabled: false` (default)
   - Verify all Phase A islands still work (BGP, IPAM, Subsystems)
   - Verify "Updated" timestamps appear
   - Verify NodeConnectivity shows placeholder
   - Verify no new console errors

2. **With exec:** `helm upgrade --set ciliumAgent.execEnabled=true`
   - Verify ClusterRole now includes `pods/exec`
   - Verify Subsystems card shows WireGuard peer details (handshake times, transfer stats)
   - Verify Service Mesh section shows deployment mode
   - Verify ClusterMesh section shows "Disabled" (not configured in homelab)
   - Verify NodeConnectivity shows per-node health states
   - Verify endpoint counts match: `kubectl get ciliumendpoints -A --no-headers | wc -l`
   - Navigate between tabs — verify no console errors, no leaked timers
   - Check backend logs for structured agent exec entries
   - Check PostgreSQL audit log for exec entries

### Run full verification suite

```bash
npx tsc --noEmit          # frontend type-check (if applicable)
deno lint frontend/       # lint
deno fmt --check frontend/ # formatting
go vet ./...               # Go static analysis
go test ./internal/networking/... -v -count=1  # unit tests
make test-e2e              # Playwright
```

### Verification
- All commands above pass
- Homelab smoke test green

---

## Step-File Matrix

| Step | New Files | Modified Files |
|---|---|---|
| 1 | `agent_types.go`, `agent_exec.go` | `types.go`, `handler.go`, `routes.go`, `config.go`, `values.yaml`, `clusterrole.yaml`, `configmap-app.yaml`, `main.go`, `detect.go`, `networking_test.go` |
| 2 | `NodeConnectivity.tsx` | `use-poll.ts`, `cilium-types.ts`, `CiliumSubsystems.tsx`, `NetworkOverview.tsx`, `BgpStatus.tsx`, `IpamStatus.tsx` |
| 3 | E2E test file (maybe) | — |

**Total:** ~3 new files, ~16 modified files, 0 deleted files

---

## RBAC Requirements

### Phase B Additions (opt-in)

```yaml
# Only when ciliumAgent.execEnabled: true
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]
```

### Full Cilium RBAC (Phase A + B combined)

```yaml
# Phase A (already present)
- apiGroups: ["cilium.io"]
  resources: ["ciliumbgpclusterconfigs", "ciliumbgppeerconfigs", "ciliumbgpnodeconfigs", "ciliumnodes", "ciliumendpoints"]
  verbs: ["get", "list", "watch"]

# Phase B (conditional on ciliumAgent.execEnabled)
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]
```

---

## Risk Mitigation

| Risk | Mitigation |
|---|---|
| Service account with `pods/exec` is a privilege escalation | Opt-in via Helm (default off), pod label validation, namespace allowlist, hardcoded command, 1MB output cap, Prometheus metrics, audit trail |
| Agent exec hangs on unresponsive pod | 5s per-pod timeout + 30s outer timeout + errgroup context cancellation |
| All agent pods fail | Graceful degradation: subsystems returns CRD-only data, connectivity returns empty nodes list |
| 1 MB output cap exceeded | Truncated JSON fails to parse → node marked as failed → CRD-only fallback, warning logged |
| Large cluster (50+ nodes) saturates exec capacity | errgroup.SetLimit(5) caps concurrent execs; 30s outer timeout bounds wall time to 50s worst case |
| Singleflight thundering herd on cold cache | Singleflight key `"agent-collect"` coalesces concurrent requests |
| `lastFetchedAt` addition breaks existing usePoll callers | Added as new return field — destructuring callers that don't use it are unaffected |
| NodeConnectivity island not bundled by Fresh | Always rendered in SSR (even as placeholder) per project convention |
| Remote cluster selected | All Cilium endpoints return `configured: false` (unchanged from Phase A) |
| Agent cache stale after ConfigMap edit | `InvalidateCaches()` now clears agent cache too |
| Merge function crashes on nil sub-structs | Nil-guard checks on Encryption/MeshInfo/ClusterMeshInfo before merging |

---

## Phase C Scope (Future — Beyond Phase B)

| Feature | Requires | Notes |
|---|---|---|
| Istio/Linkerd detection | API group + Deployment checks (no exec) | Needs per-mesh adapter pattern, test environment, `DetectMesh()` return type change |
| Full node connectivity matrix | `cilium-dbg connectivity test` (destructive, long-running) | Requires separate opt-in, background job pattern |
| BPF map utilization | Agent exec + `cilium bpf map list` | Could inform capacity planning |
| DNS proxy cache | Agent exec + `cilium fqdn cache list` | Useful for DNS-based policy debugging |
| Service load-balancing view | Agent exec + `cilium service list` | Cross-references with Service topology |
| `cilium` binary fallback | Try `cilium` if `cilium-dbg` not found | For Cilium < 1.13 (EOL) — add only if reported |
| Adaptive polling | Dynamic interval based on health state | Fixed intervals are sufficient for now |
