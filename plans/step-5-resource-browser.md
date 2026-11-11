# Step 5: Resource Browser — Implementation Plan

> **Stack note (2026-11-11):** this plan predates the frontend's move to React 19 islands on Astro. Its Preact references describe the stack at the time; the app no longer uses Preact.

## Overview

Add a real-time resource browser to KubeCenter: a Go WebSocket hub backed by informer events, a generic sortable/filterable/paginated table island in the frontend, and route pages for all 19 sidebar navigation entries. This is the step that transforms KubeCenter from a dashboard into a management tool.

**Scope:** Read-only tables with real-time updates. Row actions (delete, scale, restart) are deferred to Step 6 (Resource Detail).

---

## Architecture Decisions

These decisions were made based on research into gorilla/websocket, Fresh 2.x limitations, client-go informer patterns, and open-source k8s dashboard precedent (Headlamp, Kubernetes Dashboard).

### D1: WebSocket Transport — Fresh WebSocket Proxy Route

**Decision:** Add a dedicated WebSocket proxy route at `frontend/routes/ws/[...path].ts` using Deno's native `Deno.upgradeWebSocket()`. The browser connects to `ws://localhost:5173/ws/v1/resources`, Fresh upgrades the connection and opens a second WebSocket to the Go backend, relaying messages bidirectionally.

**Why not direct backend connection:** Avoids exposing the Go backend URL to the browser, keeps the single-origin BFF security model, avoids CORS reconfiguration, and keeps JWT tokens out of query strings (visible in logs/history). The Fresh proxy can inject the access token from the browser's cookie-based session.

**Why not SSE:** WebSocket supports bidirectional communication needed for subscribe/unsubscribe messages. SSE would require a separate REST mechanism for subscriptions.

**Trade-offs:** Double connections (browser↔Fresh↔Go) add ~1ms latency per message. Acceptable for k8s event update rates.

### D2: WebSocket Authentication — Token as First Message

**Decision:** After WebSocket upgrade, the client sends `{"type":"auth","token":"<accessToken>"}` within 5 seconds. Server validates the JWT and responds with `{"type":"auth_ok"}` or closes with code 4001. The Fresh proxy reads the access token from the auth state and injects it.

**Why not HTTP header auth:** Browser `WebSocket` API does not support custom headers. The Fresh proxy could add headers, but keeping auth in-band makes the protocol self-contained and testable.

### D3: Secrets — No Real-Time Updates

**Decision:** The secrets table uses REST polling only (no WebSocket subscription). Secrets are intentionally excluded from the informer cache for security. The table shows a subtle "Updates on refresh" indicator.

**Why:** Adding a per-user watch goroutine for secrets would break the "informers for read" architecture, add significant complexity, and require careful handling of secret value masking in the watch stream.

### D4: chi Timeout Middleware — Move to Sub-Group

**Decision:** Move `chimw.Timeout` from the global middleware chain to the REST API route group only. The WebSocket route group omits it. This is the cleanest solution since chi contexts can only shorten timeouts, never extend them.

### D5: Namespace State — Shared Signal Module

**Decision:** Create `frontend/lib/namespace.ts` exporting `selectedNamespace = signal("all")`. TopBar writes to it, all resource table islands read from it. URL query param `?ns=` syncs bidirectionally with the signal.

**Resolves:** Todo 062 (namespace selector not shared).

### D6: Client-Side Pagination and Sort

**Decision:** Resource tables fetch the full list for the current namespace via REST (informer cache makes this cheap), then paginate and sort client-side. WebSocket events update the in-memory collection. This eliminates sort/filter round-trips and keeps pagination consistent with real-time updates.

**When this breaks:** Namespaces with 1000+ resources of a single kind. Fall back to server-side pagination with continue tokens in that case (existing backend support).

### D7: Step 5 is Read-Only

**Decision:** Tables link to future detail pages (Step 6) but expose no write actions (delete, scale, etc.) in the table rows. This dramatically reduces island complexity.

---

## Implementation Phases

### Phase 1: Prerequisites and Infrastructure (Backend + Frontend Foundation)

#### 1.1: Add gorilla/websocket dependency

```bash
cd backend && go get github.com/gorilla/websocket@v1.5.3
```

**File:** `backend/go.mod` — adds `github.com/gorilla/websocket v1.5.3`

#### 1.2: Move Timeout middleware to sub-group

**File:** `backend/internal/server/server.go`

Remove `chimw.Timeout` from line 89 (global middleware chain). Instead, apply it inside `registerRoutes()` only to the REST route groups. The WebSocket route group will not have it.

Before:
```go
s.Router.Use(chimw.Timeout(deps.Config.Server.RequestTimeout))
```

After: Remove from global chain. Apply in `registerRoutes()`:
```go
// Inside /api/v1 route
r.Use(chimw.Timeout(s.Config.Server.RequestTimeout))
// But NOT on the /ws route group
```

#### 1.3: Create WebSocket hub (`backend/internal/websocket/`)

Three files following the gorilla/websocket canonical hub pattern, adapted for topic-based fan-out.

**`backend/internal/websocket/hub.go`:**
```go
type Hub struct {
    clients       map[*Client]bool
    subscriptions map[subKey]map[*Client]bool  // {kind, namespace} → clients
    register      chan *Client
    unregister    chan *Client
    events        chan ResourceEvent  // fed by informer event handlers
    logger        *slog.Logger
}

type subKey struct {
    Kind      string
    Namespace string  // empty = all namespaces
}
```

- Single `Run(ctx)` goroutine with `select` on register/unregister/events channels
- On event: look up `subscriptions[{event.Kind, event.Namespace}]` AND `subscriptions[{event.Kind, ""}]` (all-ns subscribers)
- Non-blocking send to client's `send` channel; if full, unregister + close (slow client detection)
- Graceful shutdown: on ctx.Done(), iterate all clients, send `CloseGoingAway`, close connections
- Events channel buffer: 1024 (prevents informer event handlers from blocking)
- Method: `Subscribe(client, kind, namespace) error` — checks RBAC via `AccessChecker`, adds to map
- Method: `Unsubscribe(client, kind, namespace)` — removes from map

**`backend/internal/websocket/client.go`:**
```go
type Client struct {
    hub   *Hub
    conn  *websocket.Conn
    send  chan []byte  // buffered: 256
    user  *auth.User
    subs  map[subKey]bool  // tracks this client's active subscriptions
}
```

- `readPump()` — reads JSON messages (auth, subscribe, unsubscribe), 5s auth timeout
- `writePump()` — writes from `send` channel, ping/pong keepalive (54s ping, 60s pong wait)
- Read limit: 64KB (subscribe messages are small)
- Panic recovery via `defer` in both pumps (chi's Recoverer doesn't cover hijacked connections)

**`backend/internal/websocket/events.go`:**
```go
type ResourceEvent struct {
    Type      string `json:"eventType"`  // ADDED, MODIFIED, DELETED
    Kind      string `json:"kind"`       // deployments, pods, etc.
    Namespace string `json:"namespace"`
    Name      string `json:"name"`
    Object    any    `json:"object"`     // full k8s object (same as REST response)
}

// Wire message types (client ↔ server)
type WSMessage struct {
    Type string          `json:"type"`  // auth, auth_ok, subscribe, unsubscribe, event, error
    // ... fields vary by type
}
```

Message protocol:
```
Client → Server:
  {"type":"auth","token":"<jwt>"}
  {"type":"subscribe","id":"sub-1","kind":"pods","namespace":"default"}
  {"type":"unsubscribe","id":"sub-1"}

Server → Client:
  {"type":"auth_ok"}
  {"type":"subscribed","id":"sub-1"}
  {"type":"error","id":"sub-1","code":403,"message":"RBAC: forbidden"}
  {"type":"event","id":"sub-1","eventType":"MODIFIED","object":{...}}
```

#### 1.4: Wire informer event handlers to the hub

**File:** `backend/internal/k8s/informers.go`

Add a method `RegisterEventHandlers(eventCh chan<- ResourceEvent)` that calls `AddEventHandler` on each informer (except secrets). Each handler:
- Skips `isInInitialList` events (suppresses initial sync flood)
- Handles `cache.DeletedFinalStateUnknown` wrapper on deletes
- Type-asserts the object and extracts kind/namespace/name
- Non-blocking send to `eventCh` (drop event if channel full, log warning)
- Does NOT deep-copy the object (we serialize to JSON immediately in the hub)

Call this method **before** `informerMgr.Start()` in `main.go`.

Resource types to register (17 of 18 — excluding secrets):
```
Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs,
Services, Ingresses, NetworkPolicies, ConfigMaps, PVCs,
Namespaces, Nodes, Events,
Roles, ClusterRoles, RoleBindings, ClusterRoleBindings
```

#### 1.5: Register WebSocket route

**File:** `backend/internal/server/routes.go`

Add a new route group inside `/api/v1` for WebSocket, with auth middleware but WITHOUT CSRF or Timeout:

```go
// WebSocket routes — auth required, no CSRF (GET upgrade), no timeout (long-lived)
r.Group(func(ws chi.Router) {
    // Note: Auth is handled in-band (first message), not via middleware,
    // because the WebSocket upgrade is a GET that may not have Bearer header.
    ws.Get("/ws/resources", s.handleWSResources)
})
```

**File:** `backend/internal/server/handle_ws.go` (new)

Handler that:
1. Validates `Origin` header against allowed origins
2. Calls `upgrader.Upgrade(w, r, nil)` with `CheckOrigin` configured
3. Creates `Client` struct
4. Starts readPump and writePump goroutines
5. Logs connection lifecycle events via slog

#### 1.6: Wire hub into Server

**File:** `backend/internal/server/server.go`

Add `Hub *websocket.Hub` to `Server` and `Deps` structs.

**File:** `backend/cmd/kubecenter/main.go`

Create hub, register informer event handlers, start hub goroutine — all before `informerMgr.Start()`:

```go
hub := websocket.NewHub(logger, accessChecker)
informerMgr.RegisterEventHandlers(hub.Events())
go hub.Run(ctx)
informerMgr.Start(ctx)
```

#### 1.7: Create shared namespace signal (resolves todo 062)

**File:** `frontend/lib/namespace.ts` (new, client-only)

```typescript
/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * Shared namespace state consumed by TopBar and all resource table islands.
 */
import { signal } from "@preact/signals";

/** Currently selected namespace. "all" = all namespaces. */
export const selectedNamespace = signal<string>("all");
```

**File:** `frontend/islands/TopBar.tsx`

Replace local `const selectedNs = useSignal("all")` with import from `@/lib/namespace.ts`.

#### 1.8: Create WebSocket client module

**File:** `frontend/lib/ws.ts` (new, client-only)

```typescript
/**
 * Client-only module — MUST NOT be imported in server-rendered components.
 * WebSocket client with auth, subscribe/unsubscribe, reconnect with backoff.
 */
```

Key features:
- `connectWS()` — opens WebSocket to `/ws/v1/resources`, sends auth message
- `subscribe(id, kind, namespace, onEvent)` — sends subscribe message, registers callback
- `unsubscribe(id)` — sends unsubscribe message, removes callback
- Reconnection: exponential backoff (1s base, 30s max, ±20% jitter)
- Visibility-aware: pause reconnect when tab hidden (`document.hidden`)
- On reconnect: re-send all active subscriptions
- Connection state signal: `wsStatus = signal<"connecting"|"connected"|"disconnected">("disconnected")`
- Uses `getAccessToken()` from `@/lib/api.ts` for auth message

#### 1.9: Create WebSocket proxy route in Fresh

**File:** `frontend/routes/ws/[...path].ts` (new)

Resolves todo 063. Handles WebSocket upgrade from browser, opens a second WebSocket to the Go backend, and relays messages bidirectionally:

```typescript
export const handler = define.handlers({
  GET(ctx) {
    if (ctx.req.headers.get("upgrade") !== "websocket") {
      return new Response("Expected WebSocket", { status: 400 });
    }
    // Validate path (same SSRF protection as BFF proxy)
    const backendPath = ctx.params.path;
    if (!backendPath.startsWith("v1/") || /\.\.|\/{2}|%2e/i.test(backendPath)) {
      return new Response("Invalid path", { status: 400 });
    }

    const { socket: clientSocket, response } = Deno.upgradeWebSocket(ctx.req);
    // Open backend WebSocket and relay messages
    const backendWsUrl = BACKEND_URL.replace("http", "ws") + "/api/" + backendPath;
    // ... relay logic
    return response;
  },
});
```

---

### Phase 2: Frontend UI Components

#### 2.1: Create DataTable component

**File:** `frontend/components/ui/DataTable.tsx` (SSR-safe, no signals)

Generic table component with:
- Typed column definitions: `{ key, label, sortable?, render?, width? }`
- Header row with sort indicators (▲/▼)
- Sticky header (`sticky top-0`)
- Row highlight support (for real-time update animations)
- Responsive: horizontal scroll on small screens
- Loading skeleton (N placeholder rows with `animate-pulse`)
- Empty state slot
- Tailwind styling consistent with existing Card/Button components

```typescript
interface Column<T> {
  key: string;
  label: string;
  sortable?: boolean;
  render?: (item: T) => preact.ComponentChildren;
  width?: string;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  sortField?: string;
  sortOrder?: "asc" | "desc";
  onSort?: (field: string) => void;
  loading?: boolean;
  emptyMessage?: string;
  rowKey: (item: T) => string;
  onRowClick?: (item: T) => void;
  highlightedKeys?: Set<string>;  // for real-time update flash
}
```

#### 2.2: Create Pagination component

**File:** `frontend/components/ui/Pagination.tsx`

- Page size selector (25, 50, 100)
- Previous/Next buttons
- Page indicator: "1-50 of 142"
- Total count badge (updates in real-time)

#### 2.3: Create StatusBadge component

**File:** `frontend/components/ui/StatusBadge.tsx`

Maps k8s resource states to color-coded badges using theme tokens:
- Success (green): Running, Ready, Active, Available, Succeeded, Bound
- Warning (amber): Pending, ContainerCreating, Terminating, NotReady
- Danger (red): Failed, Error, CrashLoopBackOff, ImagePullBackOff, OOMKilled, Unknown
- Info (blue): Completed

#### 2.4: Create SearchBar component

**File:** `frontend/components/ui/SearchBar.tsx`

- Name search input (client-side substring filter, 300ms debounce)
- Label selector input (sent to backend as `?labelSelector=`)
- Validation: inline error for invalid label selectors
- Clear button

---

### Phase 3: Resource Table Island and Column Definitions

#### 3.1: Define column configurations

**File:** `frontend/lib/resource-columns.ts` (new)

Column definitions for each resource type, matching `kubectl get` output:

```typescript
// Deployments: Name, Namespace, Ready, Up-to-date, Available, Age
// Pods: Name, Namespace, Status, Ready, Restarts, Node, Age
// Services: Name, Namespace, Type, Cluster IP, Ports, Age
// Nodes: Name, Status, Roles, Version, Age
// StatefulSets: Name, Namespace, Ready, Age
// DaemonSets: Name, Namespace, Desired, Current, Ready, Available, Age
// Jobs: Name, Namespace, Completions, Duration, Age
// CronJobs: Name, Namespace, Schedule, Suspend, Active, Last Schedule, Age
// Ingresses: Name, Namespace, Class, Hosts, Ports, Age
// NetworkPolicies: Name, Namespace, Pod Selector, Age
// PVCs: Name, Namespace, Status, Volume, Capacity, Access Modes, StorageClass, Age
// ConfigMaps: Name, Namespace, Data (count), Age
// Secrets: Name, Namespace, Type, Data (count), Age
// Namespaces: Name, Status, Age
// Events: Type, Reason, Object, Message, Count, Last Seen
// Roles: Name, Namespace, Age
// ClusterRoles: Name, Age
// RoleBindings: Name, Namespace, Role, Age
// ClusterRoleBindings: Name, Role, Age
```

Include helper functions:
- `formatAge(timestamp)` — returns "3d 4h", "5m", "12s" style relative time
- `formatPorts(ports[])` — "80/TCP, 443/TCP"
- `podStatus(pod)` — derives display status from pod phase + conditions + container statuses

#### 3.2: Define TypeScript types for k8s resources

**File:** `frontend/lib/k8s-types.ts` (extend existing)

Add minimal TypeScript interfaces for the k8s objects returned by the REST API. These don't need to be exhaustive — only the fields used in table columns:

```typescript
interface K8sMetadata {
  name: string;
  namespace?: string;
  creationTimestamp: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  uid: string;
  resourceVersion: string;
}

interface K8sDeployment {
  metadata: K8sMetadata;
  spec: { replicas?: number; /* ... */ };
  status: { readyReplicas?: number; updatedReplicas?: number; availableReplicas?: number; replicas?: number; };
}

// ... similar for Pod, Service, Node, etc.
```

#### 3.3: Create ResourceTable island

**File:** `frontend/islands/ResourceTable.tsx` (new)

The core island. Props are serializable (Fresh island requirement):

```typescript
interface ResourceTableProps {
  kind: string;              // "deployments", "pods", etc.
  clusterScoped?: boolean;   // true for nodes, namespaces, clusterroles, clusterrolebindings
  supportsWebSocket?: boolean;  // false for secrets
  defaultSort?: string;      // default sort column key
  defaultOrder?: "asc" | "desc";
  pageSize?: number;         // default 50
}
```

Behavior:
1. **On mount:** Read URL query params (`?ns=`, `?sort=`, `?search=`, `?label=`)
2. **Subscribe to `selectedNamespace` signal** (skip for cluster-scoped resources)
3. **REST fetch:** `apiGet("/v1/resources/{kind}")` or `apiGet("/v1/resources/{kind}/{namespace}")`
4. **Store full result in local signal** (array of resources)
5. **If `supportsWebSocket`:** Connect via `ws.ts`, subscribe to `{kind, namespace}`
6. **On WS event:** Update local array (ADDED=push, MODIFIED=replace by uid, DELETED=remove by uid)
7. **Client-side sort/filter/paginate** over the local array
8. **URL sync:** `history.replaceState()` on sort/filter changes
9. **On namespace change:** Unsubscribe old, fetch new, subscribe new
10. **On unmount:** Unsubscribe, clean up WS (via useEffect cleanup)

States:
- **Loading:** DataTable with skeleton rows
- **Empty:** EmptyState component ("No {kind} found in {namespace}")
- **RBAC denied:** EmptyState with lock icon ("You don't have permission to list {kind}")
- **Error:** Error message with retry button
- **Data + WS disconnected:** Data shown with "Live updates unavailable" subtle indicator
- **Data + WS connected:** Normal view with real-time updates

Row highlights:
- ADDED: brief green-tinted background fade (1.5s)
- MODIFIED: brief yellow-tinted background fade (1.5s)
- DELETED: fade-out opacity (300ms), then remove

#### 3.4: Create EventStream island

**File:** `frontend/islands/EventStream.tsx` (new)

Specialized island for `/cluster/events` — distinct from ResourceTable because Events have unique UX:
- Most recent 200 events displayed (not paginated like other resources)
- Auto-scroll to newest, pause on user scroll-up with "New events ▼" button
- Color-coded by event type: Normal (info), Warning (amber)
- Columns: Type (badge), Reason, Object (kind/name), Message, Count, Last Seen
- WS subscription with throttle: max 10 events/second rendered (batch with `requestAnimationFrame`)
- Filter by: event type (Normal/Warning), involved object kind, namespace

---

### Phase 4: Route Pages

Create route files for all 19 sidebar navigation entries. Each route is a thin wrapper that renders the ResourceTable or EventStream island with the appropriate props.

#### Route file pattern (one file per resource type):

```typescript
// frontend/routes/workloads/deployments.tsx
import { define } from "@/utils.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

export default define.page(function DeploymentsPage() {
  return (
    <div class="p-6">
      <h1 class="text-2xl font-semibold text-gray-900 dark:text-white mb-6">Deployments</h1>
      <ResourceTable kind="deployments" defaultSort="name" />
    </div>
  );
});
```

#### Route files to create (19 total):

**Cluster:**
- `frontend/routes/cluster/nodes.tsx` — `<ResourceTable kind="nodes" clusterScoped />`
- `frontend/routes/cluster/namespaces.tsx` — `<ResourceTable kind="namespaces" clusterScoped />`
- `frontend/routes/cluster/events.tsx` — `<EventStream />`

**Workloads:**
- `frontend/routes/workloads/deployments.tsx`
- `frontend/routes/workloads/statefulsets.tsx`
- `frontend/routes/workloads/daemonsets.tsx`
- `frontend/routes/workloads/pods.tsx`
- `frontend/routes/workloads/jobs.tsx`
- `frontend/routes/workloads/cronjobs.tsx`

**Networking:**
- `frontend/routes/networking/services.tsx`
- `frontend/routes/networking/ingresses.tsx`
- `frontend/routes/networking/networkpolicies.tsx`

**Storage:**
- `frontend/routes/storage/pvcs.tsx`

**Config:**
- `frontend/routes/config/configmaps.tsx`
- `frontend/routes/config/secrets.tsx` — `<ResourceTable kind="secrets" supportsWebSocket={false} />`

**Access Control:**
- `frontend/routes/rbac/roles.tsx`
- `frontend/routes/rbac/clusterroles.tsx` — `<ResourceTable kind="clusterroles" clusterScoped />`
- `frontend/routes/rbac/rolebindings.tsx`
- `frontend/routes/rbac/clusterrolebindings.tsx` — `<ResourceTable kind="clusterrolebindings" clusterScoped />`

---

### Phase 5: Testing and Polish

#### 5.1: Backend tests

**File:** `backend/internal/websocket/hub_test.go` (new)

- Test subscribe/unsubscribe fan-out
- Test slow client detection (full send channel → client removed)
- Test RBAC denial on subscribe
- Test auth timeout (no auth message within 5s)
- Test event filtering by kind+namespace
- Test graceful shutdown closes all clients

**File:** `backend/internal/k8s/informers_test.go` (extend or new)

- Test event handler registration
- Test `isInInitialList` events are skipped
- Test `DeletedFinalStateUnknown` handling

#### 5.2: Frontend tests

**File:** `frontend/lib/ws_test.ts` (new)

- Test reconnection backoff timing
- Test auth message sent on connect
- Test re-subscribe on reconnect
- Test visibility-aware pause

**File:** `frontend/lib/resource-columns_test.ts` (new)

- Test `formatAge()` with various timestamps
- Test `podStatus()` derivation logic

#### 5.3: Integration smoke test against homelab

Per the pre-merge requirement:
1. Start backend + frontend against homelab k3s cluster
2. Navigate to `/workloads/deployments` — verify real deployments appear
3. Navigate to `/workloads/pods` — verify pods with correct status badges
4. Navigate to `/cluster/nodes` — verify 3 nodes shown
5. Change namespace selector — verify table updates
6. Create a test deployment via kubectl — verify it appears in real-time via WebSocket
7. Delete the test deployment — verify it disappears
8. Verify `/config/secrets` works without WebSocket (polling mode)
9. Verify `/cluster/events` shows live events
10. Verify sort and search work correctly

---

## File Summary

### New Files (Backend — 5 files)
```
backend/internal/websocket/
├── hub.go              # Central hub goroutine, topic-based fan-out
├── client.go           # Per-connection client, read/write pumps
├── events.go           # Event types, message protocol
└── hub_test.go         # Hub unit tests

backend/internal/server/
└── handle_ws.go        # WebSocket upgrade handler
```

### Modified Files (Backend — 4 files)
```
backend/go.mod                          # Add gorilla/websocket v1.5.3
backend/internal/server/server.go       # Add Hub to Server/Deps, move Timeout
backend/internal/server/routes.go       # Add WS route group
backend/internal/k8s/informers.go       # Add RegisterEventHandlers method
backend/cmd/kubecenter/main.go          # Create hub, wire event handlers
```

### New Files (Frontend — 15 files)
```
frontend/lib/
├── namespace.ts                # Shared namespace signal
├── ws.ts                       # WebSocket client with reconnect
├── resource-columns.ts         # Column definitions for 19 resource types
└── ws_test.ts                  # WebSocket client tests

frontend/routes/ws/
└── [...path].ts                # WebSocket proxy route

frontend/components/ui/
├── DataTable.tsx               # Generic sortable table
├── Pagination.tsx              # Page controls
├── StatusBadge.tsx             # K8s status color badges
└── SearchBar.tsx               # Name search + label selector

frontend/islands/
├── ResourceTable.tsx           # Main resource browser island
└── EventStream.tsx             # Live event feed island

frontend/routes/
├── workloads/deployments.tsx   # (+ 5 more workload routes)
├── networking/services.tsx     # (+ 2 more networking routes)
├── cluster/nodes.tsx           # (+ 2 more cluster routes)
├── storage/pvcs.tsx
├── config/configmaps.tsx       # (+ secrets)
└── rbac/roles.tsx              # (+ 3 more RBAC routes)
```

### Modified Files (Frontend — 2 files)
```
frontend/islands/TopBar.tsx             # Use shared namespace signal
frontend/lib/k8s-types.ts              # Add k8s resource type interfaces
```

**Total: ~40 files** (20 new backend+frontend, ~19 route pages, ~4 modified)

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Fresh WS proxy adds latency | Low — ~1ms per message | Acceptable for k8s event rates; can switch to direct connection later |
| High event churn from Events informer overwhelms clients | Medium | Throttle event fan-out to 10/s per client in the hub |
| `chimw.Timeout` restructuring breaks existing tests | Low | Run `make test-backend` after the change |
| Large namespaces (1000+ pods) degrade client-side sort | Medium | Detect and fall back to server-side pagination |
| WebSocket reconnect storms after backend restart | Low | Jitter on reconnect delay prevents thundering herd |

---

## Acceptance Criteria

From the original plan, plus additions from research:

- [ ] Deployment list shows all deployments with status, replicas, age
- [ ] Pod list shows pods with phase, restarts, node, age
- [ ] Service list shows services with type, cluster IP, ports
- [ ] Node list shows nodes with status, roles, version
- [ ] All 19 sidebar nav entries have working route pages
- [ ] Tables sort by clicking column headers (client-side)
- [ ] Tables filter by name search (client-side substring, debounced)
- [ ] Tables filter by label selector (backend query param)
- [ ] WebSocket delivers real-time updates (create a pod via kubectl, see it appear)
- [ ] Namespace selector filters all namespace-scoped tables
- [ ] Namespace selector disabled/hidden on cluster-scoped pages
- [ ] Pagination works with configurable page size (25/50/100)
- [ ] URL preserves sort/filter/namespace state across navigation
- [ ] Secrets table works without WebSocket (REST only, "Updates on refresh" indicator)
- [ ] Events page shows live events with auto-scroll
- [ ] WebSocket reconnects with exponential backoff on disconnection
- [ ] RBAC denied shows appropriate empty state (not a spinner)
- [ ] Loading skeletons shown during initial data fetch
- [ ] Real-time row highlights: green flash for ADDED, yellow for MODIFIED, fade-out for DELETED
- [ ] `make test-backend` passes with new WebSocket tests
- [ ] Smoke tested against homelab k3s cluster

---

## Dependencies

- **gorilla/websocket v1.5.3** — Go WebSocket library (to be added to go.mod)
- **Todo 062** — Namespace shared signal (resolved in Phase 1.7)
- **Todo 063** — WebSocket proxy route (resolved in Phase 1.9)
- **Steps 1-4** — Complete (backend skeleton, auth, resources, frontend skeleton)

---

## Estimated Scope

- **Backend:** ~800 lines Go (hub, client, events, handler, tests, wiring)
- **Frontend:** ~1200 lines TypeScript (ws client, resource table, event stream, data table, 19 route pages, types, columns)
- **Total:** ~2000 lines of new code
