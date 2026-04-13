# Gateway API Dashboard — Implementation Plan

**Spec:** `docs/superpowers/specs/2026-04-12-gateway-api-design.md`
**Branch:** `feat/gateway-api-dashboard`

---

## Phase 1: Backend Package Foundation (types, discovery, normalize)

**Files to create:** 3 | **Files to modify:** 0

### Step 1.1: `backend/internal/gateway/types.go`

Define all GVR constants and normalized Go structs.

**GVR constants** (package-level `var` block, match certmanager pattern):

| Constant | Group | Version | Resource |
|---|---|---|---|
| `GatewayClassGVR` | `gateway.networking.k8s.io` | `v1` | `gatewayclasses` |
| `GatewayGVR` | `gateway.networking.k8s.io` | `v1` | `gateways` |
| `HTTPRouteGVR` | `gateway.networking.k8s.io` | `v1` | `httproutes` |
| `GRPCRouteGVR` | `gateway.networking.k8s.io` | `v1` | `grpcroutes` |
| `TCPRouteGVR` | `gateway.networking.k8s.io` | `v1alpha2` | `tcproutes` |
| `TLSRouteGVR` | `gateway.networking.k8s.io` | `v1alpha2` | `tlsroutes` |
| `UDPRouteGVR` | `gateway.networking.k8s.io` | `v1alpha2` | `udproutes` |

Note: TLSRoute may also be at `v1` (Gateway API >= v1.5). Discovery should check `v1` first, fall back to `v1alpha2`.

**API group constant:**
```go
const APIGroup = "gateway.networking.k8s.io"
```

**Cache TTL constant:**
```go
const cacheTTL = 30 * time.Second
```

**Structs to define** (all with `json:"camelCase"` tags, `omitempty` for optional fields):

```
GatewayAPIStatus
  Available     bool
  Version       string       // "v1", "v1beta1"
  InstalledKinds []string    // which CRDs are present
  LastChecked   time.Time

Condition
  Type               string
  Status             string
  Reason             string
  Message            string
  LastTransitionTime *time.Time

ParentRefSummary
  Group     string
  Kind      string
  Name      string
  Namespace string
  SectionName string
  Status    string  // "Accepted", "Not Accepted", ""

BackendRefDetail
  Group     string
  Kind      string
  Name      string
  Namespace string
  Port      *int
  Weight    *int
  Resolved  bool

RouteSummary  (shared across all route kinds in list views + Gateway's attachedRoutes)
  Kind       string
  Name       string
  Namespace  string
  Hostnames  []string
  ParentRefs []ParentRefSummary
  Conditions []Condition
  Age        time.Time

GatewayClassSummary
  Name           string
  ControllerName string
  Description    string
  Conditions     []Condition
  Age            time.Time

ListenerSummary
  Name              string
  Port              int
  Protocol          string
  Hostname          string
  AttachedRouteCount int

ListenerDetail (extends ListenerSummary)
  TLSMode           string
  CertificateRef    string
  AllowedRoutes     string
  Conditions        []Condition

GatewaySummary
  Name           string
  Namespace      string
  GatewayClassName string
  Listeners      []ListenerSummary
  Addresses      []string
  AttachedRouteCount int
  Conditions     []Condition
  Age            time.Time

GatewayDetail (extends GatewaySummary)
  Listeners      []ListenerDetail  // overrides with richer data
  AttachedRoutes []RouteSummary

HTTPRouteSummary
  Name         string
  Namespace    string
  Hostnames    []string
  ParentRefs   []ParentRefSummary
  BackendCount int
  Conditions   []Condition
  Age          time.Time

HTTPRouteMatch
  PathType  string
  PathValue string
  Headers   []string  // "name=value" pairs
  Method    string
  QueryParams []string

HTTPRouteFilter
  Type    string
  Details string  // human-readable summary

HTTPRouteRule
  Matches     []HTTPRouteMatch
  Filters     []HTTPRouteFilter
  BackendRefs []BackendRefDetail

HTTPRouteDetail (extends HTTPRouteSummary)
  Rules      []HTTPRouteRule
  ParentRefs []ParentRefDetail  // overrides with richer data

ParentRefDetail (extends ParentRefSummary)
  GatewayConditions []Condition

GRPCRouteMatch
  Service string
  Method  string
  Headers []string

GRPCRouteRule
  Matches     []GRPCRouteMatch
  BackendRefs []BackendRefDetail

GRPCRouteDetail
  Name       string
  Namespace  string
  ParentRefs []ParentRefDetail
  Rules      []GRPCRouteRule
  Conditions []Condition
  Age        time.Time

TCPRouteDetail / UDPRouteDetail
  Name       string
  Namespace  string
  ParentRefs []ParentRefDetail
  BackendRefs []BackendRefDetail
  Conditions []Condition
  Age        time.Time

TLSRouteDetail
  Name       string
  Namespace  string
  Hostnames  []string
  ParentRefs []ParentRefDetail
  BackendRefs []BackendRefDetail
  Conditions []Condition
  Age        time.Time
```

**namespacedResource interface** (for RBAC filtering, same as certmanager):
```go
type namespacedResource interface {
    getNamespace() string
}
```

Implement `getNamespace()` on: GatewaySummary, HTTPRouteSummary, RouteSummary (all namespaced types).

### Step 1.2: `backend/internal/gateway/discovery.go`

Follow `certmanager/discovery.go` pattern exactly.

**Struct:**
```go
type Discoverer struct {
    k8sClient *k8s.ClientFactory
    logger    *slog.Logger
    mu        sync.RWMutex
    status    GatewayAPIStatus
}
```

**Constructor:** `NewDiscoverer(k8sClient *k8s.ClientFactory, logger *slog.Logger) *Discoverer`

**Methods:**
- `Status() GatewayAPIStatus` — returns cached status, re-probes if stale (5 min)
- `IsAvailable() bool` — convenience wrapper
- `Probe() GatewayAPIStatus` — queries discovery API

**Probe logic:**
1. Call `disco.ServerResourcesForGroupVersion("gateway.networking.k8s.io/v1")`
2. If error or nil → not available, return
3. Scan `APIResources` for each Kind (skip sub-resources containing `/`)
4. Require Gateway + GatewayClass as minimum for `Detected = true`
5. Record which standard kinds are present (HTTPRoute, GRPCRoute)
6. Separately probe `gateway.networking.k8s.io/v1alpha2` for experimental kinds (TCPRoute, TLSRoute, UDPRoute)
7. Check if TLSRoute is at `v1` as well (Gateway API >= v1.5)
8. Build `InstalledKinds` string slice from all detected kinds

### Step 1.3: `backend/internal/gateway/normalize.go`

Follow `certmanager/normalize.go` pattern — pure functions converting `*unstructured.Unstructured` to typed structs.

**Functions to implement:**

```
normalizeGatewayClass(u) → GatewayClassSummary
normalizeGateway(u) → GatewaySummary
normalizeGatewayDetail(u) → GatewayDetail
normalizeHTTPRoute(u) → HTTPRouteSummary
normalizeHTTPRouteDetail(u) → HTTPRouteDetail
normalizeGRPCRouteDetail(u) → GRPCRouteDetail
normalizeRoute(u, kind) → RouteSummary  (generic for any route type in list views)
normalizeTCPRouteDetail(u) → TCPRouteDetail
normalizeTLSRouteDetail(u) → TLSRouteDetail
normalizeUDPRouteDetail(u) → UDPRouteDetail
```

**Shared helpers** (same as certmanager):
- `stringFrom(m map[string]any, key string) string`
- `intFrom(m map[string]any, key string) int`
- `extractConditions(obj map[string]any, path ...string) []Condition`
- `extractParentRefs(obj *unstructured.Unstructured) []ParentRefSummary` — handle defaults (Group defaults to `gateway.networking.k8s.io`, Kind defaults to `Gateway`)
- `extractBackendRefs(slice []any) []BackendRefDetail` — handle defaults (Group defaults to `""`, Kind defaults to `Service`)

**Key extraction paths:**
- Gateway listeners: `spec.listeners[]` → name, port, protocol, hostname, tls.mode, tls.certificateRefs, allowedRoutes
- Gateway status listeners: `status.listeners[]` → attachedRoutes count, conditions
- Gateway addresses: `status.addresses[].value`
- HTTPRoute rules: `spec.rules[]` → matches (path, headers, method, queryParams), filters (type + config), backendRefs
- Route status: `status.parents[]` → parentRef + conditions (NOT top-level `status.conditions`)

### Verification checkpoint:
```bash
cd backend && go vet ./internal/gateway/...
```

---

## Phase 2: Backend Handler + Route Registration

**Files to create:** 1 | **Files to modify:** 3

### Step 2.1: `backend/internal/gateway/handler.go`

Follow `certmanager/handler.go` pattern exactly.

**Handler struct:**
```go
type Handler struct {
    K8sClient     *k8s.ClientFactory
    Discoverer    *Discoverer
    AccessChecker *resources.AccessChecker
    Logger        *slog.Logger

    fetchGroup singleflight.Group
    cacheMu    sync.RWMutex
    cache      *cachedData
    cacheGen   uint64
}
```

No `AuditLogger` or `NotifService` needed — this is read-only (no mutations).

**Constructor:** `NewHandler(k8sClient, discoverer, accessChecker, logger) *Handler`

**cachedData struct:**
```go
type cachedData struct {
    gatewayClasses []GatewayClassSummary
    gateways       []GatewaySummary
    httpRoutes     []HTTPRouteSummary
    grpcRoutes     []RouteSummary
    tcpRoutes      []RouteSummary
    tlsRoutes      []RouteSummary
    udpRoutes      []RouteSummary
    fetchedAt      time.Time
}
```

**Cache methods** (copy from certmanager):
- `getCached(ctx) (*cachedData, error)` — singleflight + 30s TTL + generation counter
- `fetchAll(ctx, gen) (*cachedData, error)` — `errgroup.WithContext`, parallel fetch of all kinds via `BaseDynamicClient()`, skip unavailable experimental kinds gracefully (check `Discoverer.Status().InstalledKinds`), only write cache if `cacheGen` unchanged
- `InvalidateCache()` — increment `cacheGen`, nil cache

**RBAC helper:**
```go
func (h *Handler) canAccess(ctx, user, verb, resource, namespace) bool
```
Uses `AccessChecker.CanAccessGroupResource` with group `"gateway.networking.k8s.io"`.

**RBAC filtering** — reuse the generic `filterByRBAC[T namespacedResource]()` pattern from certmanager. GatewayClass is cluster-scoped — check with `canAccess(ctx, user, "list", "gatewayclasses", "")`.

**List handlers** (14 endpoints):

| Handler method | Route | Logic |
|---|---|---|
| `HandleStatus` | `GET /status` | Return `Discoverer.Status()` |
| `HandleListGatewayClasses` | `GET /gatewayclasses` | getCached → filter RBAC → return |
| `HandleListGateways` | `GET /gateways` | getCached → filter RBAC → return |
| `HandleListHTTPRoutes` | `GET /httproutes` | getCached → filter RBAC → return |
| `HandleListGRPCRoutes` | `GET /grpcroutes` | getCached → filter RBAC → return |
| `HandleListTCPRoutes` | `GET /tcproutes` | getCached → filter RBAC → return |
| `HandleListTLSRoutes` | `GET /tlsroutes` | getCached → filter RBAC → return |
| `HandleListUDPRoutes` | `GET /udproutes` | getCached → filter RBAC → return |

**Detail handlers** (7 endpoints) — these bypass cache, use impersonating client:

| Handler method | Route | Logic |
|---|---|---|
| `HandleGetGatewayClass` | `GET /gatewayclasses/{name}` | Impersonating Get + normalize + no relationships needed |
| `HandleGetGateway` | `GET /gateways/{namespace}/{name}` | Impersonating Get + resolve attached routes |
| `HandleGetHTTPRoute` | `GET /httproutes/{namespace}/{name}` | Impersonating Get + resolve parents & backends |
| `HandleGetGRPCRoute` | `GET /grpcroutes/{namespace}/{name}` | Impersonating Get + resolve parents & backends |
| `HandleGetTCPRoute` | `GET /tcproutes/{namespace}/{name}` | Impersonating Get + resolve parents & backends |
| `HandleGetTLSRoute` | `GET /tlsroutes/{namespace}/{name}` | Impersonating Get + resolve parents & backends |
| `HandleGetUDPRoute` | `GET /udproutes/{namespace}/{name}` | Impersonating Get + resolve parents & backends |

**Relationship resolution in detail handlers:**

Gateway detail → attached routes:
1. After fetching the Gateway, list all installed route kinds (parallel via WaitGroup, 2s timeout)
2. Filter routes whose `spec.parentRefs` match this Gateway (name + namespace)
3. Return as `AttachedRoutes []RouteSummary`

Route detail → parent gateways + backend services:
1. After fetching the route, extract `spec.parentRefs`
2. For each parentRef, fetch the Gateway object (parallel, 2s timeout)
3. If resolved, include Gateway conditions; if not, set `resolved: false`
4. For HTTP/GRPC routes, extract `spec.rules[].backendRefs`; for TCP/TLS/UDP, extract `spec.backendRefs`
5. For each backendRef pointing to a Service, fetch the Service (parallel, 2s timeout)
6. Set `resolved: true/false` based on whether the Service was found

Use `sync.WaitGroup` (not errgroup) for relationship resolution — failure of one lookup should not cancel others.

### Step 2.2: Modify `backend/internal/server/server.go`

Add to `Server` struct:
```go
GatewayHandler *gateway.Handler
```

Add to `Deps` struct:
```go
GatewayHandler *gateway.Handler
```

Add to `New()` function:
```go
if deps.GatewayHandler != nil {
    s.GatewayHandler = deps.GatewayHandler
}
```

### Step 2.3: Modify `backend/internal/server/routes.go`

Add nil-guarded registration in the authenticated router section (near certmanager registration):
```go
if s.GatewayHandler != nil {
    s.registerGatewayRoutes(ar)
}
```

Add `registerGatewayRoutes` method:
```go
func (s *Server) registerGatewayRoutes(ar chi.Router) {
    h := s.GatewayHandler
    ar.Route("/gateway", func(gr chi.Router) {
        gr.Get("/status", h.HandleStatus)
        gr.Get("/gatewayclasses", h.HandleListGatewayClasses)
        gr.With(resources.ValidateURLParams).Get("/gatewayclasses/{name}", h.HandleGetGatewayClass)
        gr.Get("/gateways", h.HandleListGateways)
        gr.With(resources.ValidateURLParams).Get("/gateways/{namespace}/{name}", h.HandleGetGateway)
        gr.Get("/httproutes", h.HandleListHTTPRoutes)
        gr.With(resources.ValidateURLParams).Get("/httproutes/{namespace}/{name}", h.HandleGetHTTPRoute)
        gr.Get("/grpcroutes", h.HandleListGRPCRoutes)
        gr.With(resources.ValidateURLParams).Get("/grpcroutes/{namespace}/{name}", h.HandleGetGRPCRoute)
        gr.Get("/tcproutes", h.HandleListTCPRoutes)
        gr.With(resources.ValidateURLParams).Get("/tcproutes/{namespace}/{name}", h.HandleGetTCPRoute)
        gr.Get("/tlsroutes", h.HandleListTLSRoutes)
        gr.With(resources.ValidateURLParams).Get("/tlsroutes/{namespace}/{name}", h.HandleGetTLSRoute)
        gr.Get("/udproutes", h.HandleListUDPRoutes)
        gr.With(resources.ValidateURLParams).Get("/udproutes/{namespace}/{name}", h.HandleGetUDPRoute)
    })
}
```

### Step 2.4: Modify `backend/cmd/kubecenter/main.go`

Add initialization near certmanager init:
```go
gwDisc := gateway.NewDiscoverer(k8sClient, logger)
gwHandler := gateway.NewHandler(k8sClient, gwDisc, accessChecker, logger)
```

Add to Deps struct literal:
```go
GatewayHandler: gwHandler,
```

### Verification checkpoint:
```bash
cd backend && go vet ./internal/gateway/... && go vet ./internal/server/... && go vet ./cmd/kubecenter/...
```

---

## Phase 3: Frontend Types + Dashboard Island

**Files to create:** 3 | **Files to modify:** 2

### Step 3.1: `frontend/lib/gateway-types.ts`

TypeScript interfaces mirroring Go types. Follow `frontend/lib/certmanager-types.ts` pattern.

```typescript
/** Gateway API types matching backend/internal/gateway/types.go */

export interface GatewayAPIStatus { available: boolean; version: string; installedKinds: string[]; }

export interface Condition { type: string; status: string; reason: string; message: string; lastTransitionTime?: string; }

export interface ParentRefSummary { group: string; kind: string; name: string; namespace: string; sectionName: string; status: string; }
export interface ParentRefDetail extends ParentRefSummary { gatewayConditions: Condition[]; }

export interface BackendRefDetail { group: string; kind: string; name: string; namespace: string; port?: number; weight?: number; resolved: boolean; }

export interface RouteSummary { kind: string; name: string; namespace: string; hostnames: string[]; parentRefs: ParentRefSummary[]; conditions: Condition[]; age: string; }

export interface GatewayClassSummary { name: string; controllerName: string; description: string; conditions: Condition[]; age: string; }

export interface ListenerSummary { name: string; port: number; protocol: string; hostname: string; attachedRouteCount: number; }
export interface ListenerDetail extends ListenerSummary { tlsMode: string; certificateRef: string; allowedRoutes: string; conditions: Condition[]; }

export interface GatewaySummary { name: string; namespace: string; gatewayClassName: string; listeners: ListenerSummary[]; addresses: string[]; attachedRouteCount: number; conditions: Condition[]; age: string; }
export interface GatewayDetail extends GatewaySummary { listeners: ListenerDetail[]; attachedRoutes: RouteSummary[]; }

export interface HTTPRouteSummary { name: string; namespace: string; hostnames: string[]; parentRefs: ParentRefSummary[]; backendCount: number; conditions: Condition[]; age: string; }
export interface HTTPRouteMatch { pathType: string; pathValue: string; headers: string[]; method: string; queryParams: string[]; }
export interface HTTPRouteFilter { type: string; details: string; }
export interface HTTPRouteRule { matches: HTTPRouteMatch[]; filters: HTTPRouteFilter[]; backendRefs: BackendRefDetail[]; }
export interface HTTPRouteDetail extends HTTPRouteSummary { rules: HTTPRouteRule[]; parentRefs: ParentRefDetail[]; }

export interface GRPCRouteMatch { service: string; method: string; headers: string[]; }
export interface GRPCRouteRule { matches: GRPCRouteMatch[]; backendRefs: BackendRefDetail[]; }
export interface GRPCRouteDetail { name: string; namespace: string; parentRefs: ParentRefDetail[]; rules: GRPCRouteRule[]; conditions: Condition[]; age: string; }

export interface TCPRouteDetail { name: string; namespace: string; parentRefs: ParentRefDetail[]; backendRefs: BackendRefDetail[]; conditions: Condition[]; age: string; }
export interface TLSRouteDetail { name: string; namespace: string; hostnames: string[]; parentRefs: ParentRefDetail[]; backendRefs: BackendRefDetail[]; conditions: Condition[]; age: string; }
export interface UDPRouteDetail { name: string; namespace: string; parentRefs: ParentRefDetail[]; backendRefs: BackendRefDetail[]; conditions: Condition[]; age: string; }
```

### Step 3.2: `frontend/routes/networking/gateway-api.tsx`

Dashboard route. Follow `routes/security/certificates.tsx` pattern:

```tsx
import { define } from "@/utils.ts";
import SubNav from "@/islands/SubNav.tsx";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import GatewayAPIDashboard from "@/islands/GatewayAPIDashboard.tsx";

const section = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

export default define.page(function GatewayAPIPage(ctx) {
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath={ctx.url.pathname} />
      <GatewayAPIDashboard />
    </>
  );
});
```

### Step 3.3: `frontend/islands/GatewayAPIDashboard.tsx`

Main dashboard island with overview cards + list views.

**State:**
- `status: Signal<GatewayAPIStatus | null>` — CRD availability
- `loading: Signal<boolean>`
- `error: Signal<string>`
- `activeKind: Signal<string | null>` — which list view is active (null = overview)
- `listData: Signal<any[]>` — current list data
- `search: Signal<string>` — filter text

**Flow:**
1. On mount, fetch `GET /api/v1/gateway/status`
2. If `!status.available`, render "Gateway API not installed" state with guidance
3. If available, render overview cards for each installed kind
4. Each card shows: icon, kind name, count, health summary (e.g., "3 Programmed, 1 Not Ready")
5. Clicking a card sets `activeKind` and fetches the list endpoint
6. List view renders a kind-specific table (columns vary by kind)
7. "Back" button returns to overview

**Overview cards** (only show for installed kinds):
- GatewayClasses — count + accepted/pending
- Gateways — count + programmed/not programmed
- HTTPRoutes — count + accepted/not accepted
- GRPCRoutes — count
- TCPRoutes — count
- TLSRoutes — count
- UDPRoutes — count

**List table columns per kind:**

| Kind | Columns |
|---|---|
| GatewayClass | Name, Controller, Description, Accepted, Age |
| Gateway | Name, Namespace, Class, Listeners, Addresses, Programmed, Age |
| HTTPRoute | Name, Namespace, Hostnames, Parents, Backends, Accepted, Age |
| GRPCRoute | Name, Namespace, Parents, Accepted, Age |
| TCPRoute | Name, Namespace, Parents, Backends, Accepted, Age |
| TLSRoute | Name, Namespace, Hostnames, Parents, Backends, Age |
| UDPRoute | Name, Namespace, Parents, Backends, Age |

Each row is clickable — navigates to detail page.

**Styling:** Use CSS custom property tokens exclusively. Follow CertificatesList.tsx patterns:
- Card: `rounded-lg border border-border-primary bg-bg-elevated p-5`
- Table: `rounded-lg border border-border-primary`, `bg-surface` header, `divide-y divide-border-subtle`
- Status badges: `text-success` for healthy, `text-warning` for degraded, `text-danger` for failed

### Step 3.4: Modify `frontend/lib/constants.ts`

Add Gateway API tab to the network section's `tabs` array (after EndpointSlices):
```ts
{ label: "Gateway API", href: "/networking/gateway-api" },
```

No `kind`/`count` — the dashboard fetches its own counts from the dedicated status endpoint.

### Step 3.5: Modify `frontend/islands/CommandPalette.tsx`

Add quick action to the actions array:
```ts
{ label: "View Gateway API", href: "/networking/gateway-api" },
```

### Verification checkpoint:
```bash
cd frontend && deno fmt --check && deno lint
```

---

## Phase 4: Frontend Detail Routes + Detail Islands (Part 1: Gateway + GatewayClass)

**Files to create:** 4

### Step 4.1: `frontend/routes/networking/gateway-api/gatewayclasses/[name].tsx`

Cluster-scoped detail route (no namespace param):
```tsx
export default define.page(function GatewayClassDetailPage(ctx) {
  const { name } = ctx.params;
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath="/networking/gateway-api" />
      <GatewayClassDetail name={name} />
    </>
  );
});
```

### Step 4.2: `frontend/routes/networking/gateway-api/[kind]/[ns]/[name].tsx`

Parameterized detail route for all namespaced kinds:
```tsx
export default define.page(function GatewayResourceDetailPage(ctx) {
  const { kind, ns, name } = ctx.params;
  return (
    <>
      <SubNav tabs={section.tabs ?? []} currentPath="/networking/gateway-api" />
      <GatewayResourceDetail kind={kind} namespace={ns} name={name} />
    </>
  );
});
```

Where `GatewayResourceDetail` is a dispatcher island that renders the appropriate detail component based on `kind`.

### Step 4.3: `frontend/islands/GatewayClassDetail.tsx`

**Props:** `{ name: string }`
**API:** `GET /api/v1/gateway/gatewayclasses/${name}`

**Panels:**
- Header: name + controller name
- Description text
- Conditions table: Type, Status, Reason, Message, Last Transition
- Supported features (if available in the object)

### Step 4.4: `frontend/islands/GatewayDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/gateways/${namespace}/${name}`

**Panels:**
- Header: name + namespace + gateway class (linked to GatewayClass detail)
- Addresses list
- Listeners table: Name, Port, Protocol, Hostname, TLS Mode, Certificate Ref, Attached Routes, Status
- Attached Routes table: Kind, Name, Namespace, Hostnames (each row links to route detail)
- Conditions table

### Verification checkpoint:
```bash
cd frontend && deno fmt --check && deno lint
```

---

## Phase 5: Frontend Detail Islands (Part 2: Routes)

**Files to create:** 5

### Step 5.1: `frontend/islands/GatewayResourceDetail.tsx`

Dispatcher island that renders the correct detail component based on `kind` prop:
```tsx
switch (kind) {
  case "httproutes": return <HTTPRouteDetail namespace={namespace} name={name} />;
  case "grpcroutes": return <GRPCRouteDetail namespace={namespace} name={name} />;
  case "tcproutes":  return <TCPRouteDetail namespace={namespace} name={name} />;
  case "tlsroutes":  return <TLSRouteDetail namespace={namespace} name={name} />;
  case "udproutes":  return <UDPRouteDetail namespace={namespace} name={name} />;
  default: return <div>Unknown resource kind</div>;
}
```

### Step 5.2: `frontend/islands/GatewayHTTPRouteDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/httproutes/${namespace}/${name}`

**Panels:**
- Header: name + namespace
- Hostnames list
- Parent Gateways table: Name, Namespace, Status (each links to Gateway detail). Show Gateway conditions inline.
- Rules section (one card per rule):
  - Matches: path type/value, method, headers, query params
  - Filters: type + details
  - Backend Refs table: Service Name, Namespace, Port, Weight, Resolved status (linked to service detail if resolved)
- Conditions table

### Step 5.3: `frontend/islands/GatewayGRPCRouteDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/grpcroutes/${namespace}/${name}`

**Panels:**
- Header: name + namespace
- Parent Gateways table (linked)
- Rules section:
  - Matches: Service, Method, Headers
  - Backend Refs table (linked)
- Conditions table

### Step 5.4: `frontend/islands/GatewayTCPRouteDetail.tsx`

Simpler layout (no rules, just backendRefs):
- Header + Parent Gateways table + Backend Refs table + Conditions

### Step 5.5: `frontend/islands/GatewayTLSRouteDetail.tsx`

Same as TCP but with Hostnames panel.

### Step 5.6: `frontend/islands/GatewayUDPRouteDetail.tsx`

Same structure as TCPRouteDetail.

**Note:** TCP/TLS/UDP detail islands share very similar structure. Consider extracting a shared `SimpleRouteDetail` component that all three use, parameterized by whether to show hostnames.

### Verification checkpoint:
```bash
cd frontend && deno fmt --check && deno lint
```

---

## Phase 6: Integration Testing + Full Verification

### Step 6.1: Backend type checking
```bash
cd backend && go vet ./... && go build ./...
```

### Step 6.2: Frontend linting + formatting
```bash
cd frontend && deno fmt --check && deno lint && deno task build
```

### Step 6.3: Smoke test (requires homelab with Cilium Gateway API)

1. Start dev environment: `make dev-db && make dev-backend && make dev-frontend`
2. Navigate to `/networking/gateway-api`
3. Verify:
   - Status check shows Gateway API availability
   - Overview cards display correct counts
   - Click each card → list view loads with correct columns
   - Click a row → detail page renders with relationship data
   - GatewayClass detail accessible at `/networking/gateway-api/gatewayclasses/:name`
   - Back navigation works
   - Command palette "Gateway API" action works
   - "Not installed" state renders correctly (if no Gateway API CRDs)

### Step 6.4: Run full CI checks
```bash
make lint && make test && make build
```

---

## File Summary

### New files (18):
| File | Phase |
|---|---|
| `backend/internal/gateway/types.go` | 1 |
| `backend/internal/gateway/discovery.go` | 1 |
| `backend/internal/gateway/normalize.go` | 1 |
| `backend/internal/gateway/handler.go` | 2 |
| `frontend/lib/gateway-types.ts` | 3 |
| `frontend/routes/networking/gateway-api.tsx` | 3 |
| `frontend/islands/GatewayAPIDashboard.tsx` | 3 |
| `frontend/routes/networking/gateway-api/gatewayclasses/[name].tsx` | 4 |
| `frontend/routes/networking/gateway-api/[kind]/[ns]/[name].tsx` | 4 |
| `frontend/islands/GatewayClassDetail.tsx` | 4 |
| `frontend/islands/GatewayDetail.tsx` | 4 |
| `frontend/islands/GatewayResourceDetail.tsx` | 5 |
| `frontend/islands/GatewayHTTPRouteDetail.tsx` | 5 |
| `frontend/islands/GatewayGRPCRouteDetail.tsx` | 5 |
| `frontend/islands/GatewayTCPRouteDetail.tsx` | 5 |
| `frontend/islands/GatewayTLSRouteDetail.tsx` | 5 |
| `frontend/islands/GatewayUDPRouteDetail.tsx` | 5 |

### Modified files (4):
| File | Phase | Change |
|---|---|---|
| `backend/internal/server/server.go` | 2 | Add GatewayHandler to Server + Deps structs |
| `backend/internal/server/routes.go` | 2 | Add registerGatewayRoutes + nil-guard call |
| `backend/cmd/kubecenter/main.go` | 2 | Initialize discoverer + handler, add to Deps |
| `frontend/lib/constants.ts` | 3 | Add Gateway API tab to network section |
| `frontend/islands/CommandPalette.tsx` | 3 | Add quick action |
