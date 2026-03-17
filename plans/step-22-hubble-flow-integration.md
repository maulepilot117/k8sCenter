# Step 22: Hubble Flow Integration

## Overview

Add network flow visibility by bridging Cilium Hubble Relay's gRPC API to HTTP. Users view network flows filtered by namespace and verdict in the browser.

## Revised Approach (per reviewer feedback)

- **No new package** — add `hubble_client.go` to existing `networking/` package
- **Vendor proto files** — copy `observer.proto` and `flow.proto`, generate Go stubs. Do NOT import `cilium/cilium` (massive dependency tree for 2 RPCs)
- **Minimal FlowRecord** — only fields the table renders (10 fields, not 14)
- **RBAC via `get pods`** — not `list ciliumnetworkpolicies` (flow visibility = pod observability)
- **MVP filters only** — namespace (required), verdict (optional), limit (optional)
- **Manual refresh** — no auto-refresh for MVP
- **No dedicated status endpoint** — extend existing CNI status with relay connectivity

## Implementation

### Backend

#### 1. Vendor Hubble Proto Files

```
backend/internal/networking/hubbleproto/
├── flow.proto          # copied from cilium/cilium/api/v1/flow/
├── observer.proto      # copied from cilium/cilium/api/v1/observer/
├── flow.pb.go          # generated
├── observer.pb.go      # generated
└── observer_grpc.pb.go # generated
```

Only new Go dependency: `google.golang.org/grpc` (lightweight, well-maintained).

#### 2. Hubble Client (`backend/internal/networking/hubble_client.go`)

Add to existing `networking/` package:

```go
type HubbleClient struct {
    conn *grpc.ClientConn
}

func NewHubbleClient(relayAddr string) (*HubbleClient, error)
func (c *HubbleClient) GetFlows(ctx, namespace, verdict string, limit int) ([]FlowRecord, error)
func (c *HubbleClient) Status(ctx) (*HubbleStatus, error)
func (c *HubbleClient) Close()

type FlowRecord struct {
    Time         time.Time `json:"time"`
    Verdict      string    `json:"verdict"`
    DropReason   string    `json:"dropReason,omitempty"`
    Direction    string    `json:"direction"`
    SrcNamespace string    `json:"srcNamespace"`
    SrcPod       string    `json:"srcPod"`
    DstNamespace string    `json:"dstNamespace"`
    DstPod       string    `json:"dstPod"`
    Protocol     string    `json:"protocol"`
    DstPort      uint32    `json:"dstPort,omitempty"`
}
```

#### 3. Extend Detector + Handler (`networking/detect.go`, `networking/handler.go`)

- `Detect()`: if Hubble enabled, discover `hubble-relay` service, create `HubbleClient`
- Add `HubbleClient` field to existing `Handler` struct
- New handler method: `HandleHubbleFlows(w, r)` — validates namespace, checks RBAC (`get pods` in namespace), queries flows, returns JSON
- Extend `HandleCNIStatus()` response to include `hubbleRelay: { connected: bool }` from `HubbleClient.Status()`

#### 4. Route Registration (`routes.go`)

Add inside existing `registerNetworkingRoutes`:
```go
if h.HubbleClient != nil {
    nr.Get("/hubble/flows", h.HandleHubbleFlows)
}
```

Single endpoint: `GET /api/v1/networking/hubble/flows?namespace=default&verdict=DROPPED&limit=100`

### Frontend

#### 5. Flow Viewer (`frontend/islands/FlowViewer.tsx`)

- Namespace dropdown + verdict selector (All/Forwarded/Dropped) + Refresh button
- Table: Time, Direction, Source (ns/pod), Destination (ns/pod:port), Protocol, Verdict badge
- Color coding: green=Forwarded, red=Dropped, yellow=Audit
- Empty state when Hubble is not available

#### 6. Route + Nav (`frontend/routes/networking/flows.tsx`, `frontend/lib/constants.ts`)

- Route page rendering FlowViewer island
- Sidebar entry under Networking: "Network Flows"

## Acceptance Criteria

- [ ] Hubble Relay auto-discovered from Cilium namespace
- [ ] `GET /api/v1/networking/hubble/flows` returns filtered flows as JSON
- [ ] Frontend flow table with namespace/verdict filters and refresh button
- [ ] RBAC enforced (`get pods` in requested namespace)
- [ ] Graceful degradation when Hubble not available (empty state, no errors)
- [ ] CNI status response includes `hubbleRelay.connected`
- [ ] No `cilium/cilium` Go module dependency (vendored protos only)
- [ ] Works on homelab Cilium 1.19

## Files Changed

| File | Change |
|------|--------|
| `networking/hubbleproto/*.proto` | Vendored proto files + generated Go |
| `networking/hubble_client.go` | New — gRPC client, FlowRecord type |
| `networking/detect.go` | Add Hubble Relay discovery |
| `networking/handler.go` | Add HandleHubbleFlows, extend CNI status |
| `server/routes.go` | Register hubble flow route in networking group |
| `cmd/kubecenter/main.go` | Wire HubbleClient into networking Handler |
| `frontend/islands/FlowViewer.tsx` | New — flow table with filters |
| `frontend/routes/networking/flows.tsx` | New — route page |
| `frontend/lib/constants.ts` | Add nav entry |

## References

- Hubble Observer API: `observer.proto` (cilium/cilium repo)
- Existing CNI detection: `backend/internal/networking/detect.go:218-236`
- Existing monitoring proxy pattern: `backend/internal/monitoring/handler.go`
- Hubble UI reference: `github.com/cilium/hubble-ui/backend/internal/flow_stream`
