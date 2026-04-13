# Gateway API Dashboard — Implementation Plan

**Spec:** `docs/superpowers/specs/2026-04-12-gateway-api-design.md`
**Branch:** `feat/gateway-api-dashboard`
**Review:** Revised per DHH, Kieran, and Simplicity feedback

---

## Phase 1: Backend Package Foundation (types, discovery, normalize, tests)

**Files to create:** 4 | **Files to modify:** 0

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
  Available      bool
  Version        string       // "v1", "v1beta1"
  InstalledKinds []string     // which CRDs are present
  LastChecked    time.Time

GatewayAPISummary                 // returned by GET /gateway/summary
  GatewayClasses GatewayKindSummary
  Gateways       GatewayKindSummary
  HTTPRoutes     GatewayKindSummary
  GRPCRoutes     GatewayKindSummary
  TCPRoutes      GatewayKindSummary
  TLSRoutes      GatewayKindSummary
  UDPRoutes      GatewayKindSummary

GatewayKindSummary
  Total    int
  Healthy  int               // Accepted/Programmed count
  Degraded int               // not healthy count

Condition
  Type               string
  Status             string
  Reason             string
  Message            string
  LastTransitionTime *time.Time

ParentRef                         // single type, no Summary/Detail split
  Group             string
  Kind              string
  Name              string
  Namespace         string
  SectionName       string
  Status            string        // "Accepted", "Not Accepted", ""
  GatewayConditions []Condition   `json:"gatewayConditions,omitempty"`  // populated in detail endpoints only

BackendRef
  Group     string
  Kind      string
  Name      string
  Namespace string
  Port      *int
  Weight    *int
  Resolved  bool

RouteSummary  (shared across ALL route kinds in list views + Gateway's attachedRoutes)
  Kind       string
  Name       string
  Namespace  string
  Hostnames  []string
  ParentRefs []ParentRef
  Conditions []Condition
  Age        time.Time

GatewayClassSummary
  Name           string
  ControllerName string
  Description    string
  Conditions     []Condition
  Age            time.Time

Listener                          // single type, no Summary/Detail split
  Name              string
  Port              int
  Protocol          string
  Hostname          string
  AttachedRouteCount int
  TLSMode           string        `json:"tlsMode,omitempty"`        // populated in detail only
  CertificateRef    string        `json:"certificateRef,omitempty"` // populated in detail only
  AllowedRoutes     string        `json:"allowedRoutes,omitempty"`  // populated in detail only
  Conditions        []Condition   `json:"conditions,omitempty"`     // populated in detail only

GatewaySummary
  Name             string
  Namespace        string
  GatewayClassName string
  Listeners        []Listener
  Addresses        []string
  AttachedRouteCount int
  Conditions       []Condition
  Age              time.Time

GatewayDetail
  GatewaySummary                  // embedded
  AttachedRoutes []RouteSummary   // resolved from cache

HTTPRouteSummary
  Name         string
  Namespace    string
  Hostnames    []string
  ParentRefs   []ParentRef
  BackendCount int
  Conditions   []Condition
  Age          time.Time

HTTPRouteMatch
  PathType    string
  PathValue   string
  Headers     []string  // "name=value" pairs
  Method      string
  QueryParams []string

HTTPRouteFilter
  Type    string
  Details string  // human-readable summary

HTTPRouteRule
  Matches     []HTTPRouteMatch
  Filters     []HTTPRouteFilter
  BackendRefs []BackendRef

HTTPRouteDetail
  HTTPRouteSummary                // embedded
  Rules      []HTTPRouteRule
  ParentRefs []ParentRef          // same type, but with GatewayConditions populated

GRPCRouteMatch
  Service string
  Method  string
  Headers []string

GRPCRouteRule
  Matches     []GRPCRouteMatch
  BackendRefs []BackendRef

GRPCRouteDetail
  Name       string
  Namespace  string
  ParentRefs []ParentRef
  Rules      []GRPCRouteRule
  Conditions []Condition
  Age        time.Time

SimpleRouteDetail                 // covers TCP, TLS, and UDP routes
  Kind       string               // "TCPRoute", "TLSRoute", "UDPRoute"
  Name       string
  Namespace  string
  Hostnames  []string             // only populated for TLSRoute
  ParentRefs []ParentRef
  BackendRefs []BackendRef
  Conditions []Condition
  Age        time.Time
```

**namespacedResource interface** (for RBAC filtering, same as certmanager):
```go
type namespacedResource interface {
    getNamespace() string
}
```

Implement `getNamespace()` on: GatewaySummary, HTTPRouteSummary, RouteSummary, SimpleRouteDetail, GRPCRouteDetail.

**Route kind type** (for parameterized handler dispatch):
```go
type routeKind string

const (
    RouteKindGRPC routeKind = "grpcroutes"
    RouteKindTCP  routeKind = "tcproutes"
    RouteKindTLS  routeKind = "tlsroutes"
    RouteKindUDP  routeKind = "udproutes"
)

var routeKindGVR = map[routeKind]schema.GroupVersionResource{
    RouteKindGRPC: GRPCRouteGVR,
    RouteKindTCP:  TCPRouteGVR,
    RouteKindTLS:  TLSRouteGVR,
    RouteKindUDP:  UDPRouteGVR,
}
```

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
5. Record which standard kinds are present (HTTPRoute, GRPCRoute, TLSRoute at v1)
6. Separately probe `gateway.networking.k8s.io/v1alpha2` for experimental kinds (TCPRoute, TLSRoute, UDPRoute)
7. Build `InstalledKinds` string slice from all detected kinds

### Step 1.3: `backend/internal/gateway/normalize.go`

Follow `certmanager/normalize.go` pattern — pure functions converting `*unstructured.Unstructured` to typed structs.

**Functions to implement (7 total, down from 10):**

```
normalizeGatewayClass(u)       → GatewayClassSummary
normalizeGateway(u)            → GatewaySummary
normalizeGatewayDetail(u)      → GatewayDetail
normalizeHTTPRoute(u)          → HTTPRouteSummary
normalizeHTTPRouteDetail(u)    → HTTPRouteDetail
normalizeGRPCRouteDetail(u)    → GRPCRouteDetail
normalizeRoute(u, kind)        → RouteSummary         // generic for any route kind in list views
normalizeSimpleRouteDetail(u, kind) → SimpleRouteDetail  // TCP, TLS, UDP all use this
```

**Shared helpers** (same as certmanager):
- `stringFrom(m map[string]any, key string) string`
- `intFrom(m map[string]any, key string) int`
- `extractConditions(obj map[string]any, path ...string) []Condition`
- `extractParentRefs(obj *unstructured.Unstructured) []ParentRef` — handle defaults (Group defaults to `gateway.networking.k8s.io`, Kind defaults to `Gateway`)
- `extractBackendRefs(slice []any) []BackendRef` — handle defaults (Group defaults to `""`, Kind defaults to `Service`)

**Key extraction paths:**
- Gateway listeners: `spec.listeners[]` → name, port, protocol, hostname, tls.mode, tls.certificateRefs, allowedRoutes
- Gateway status listeners: `status.listeners[]` → attachedRoutes count, conditions
- Gateway addresses: `status.addresses[].value`
- HTTPRoute rules: `spec.rules[]` → matches (path, headers, method, queryParams), filters (type + config), backendRefs
- Route status: `status.parents[]` → parentRef + conditions (NOT top-level `status.conditions`)

### Step 1.4: `backend/internal/gateway/normalize_test.go`

Table-driven tests for each normalize function. Follow `certmanager/normalize_test.go` pattern.

**Test cases per function:**
- `normalizeGatewayClass`: valid GatewayClass, missing optional fields, empty conditions
- `normalizeGateway`: multiple listeners, multiple addresses, zero attached routes
- `normalizeGatewayDetail`: listeners with TLS config, listeners without TLS
- `normalizeHTTPRoute`: multiple rules, empty matches, empty backendRefs, multiple hostnames
- `normalizeHTTPRouteDetail`: filters extraction, weight handling, parentRef defaults
- `normalizeGRPCRouteDetail`: service/method matches, header matches
- `normalizeRoute`: each route kind produces correct RouteSummary with Kind set
- `normalizeSimpleRouteDetail`: TCP (no hostnames), TLS (with hostnames), UDP (no hostnames)
- `extractParentRefs`: default group/kind handling, explicit group/kind, missing namespace
- `extractBackendRefs`: default group/kind, explicit values, missing port

### Verification checkpoint:
```bash
cd backend && go vet ./internal/gateway/... && go test ./internal/gateway/...
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
    routes         []RouteSummary  // ALL non-HTTP routes (GRPC, TCP, TLS, UDP), differentiated by Kind field
    fetchedAt      time.Time
}
```

**Cache methods** (copy from certmanager):
- `getCached(ctx) (*cachedData, error)` — singleflight + 30s TTL + generation counter
- `fetchAll(ctx, gen) (*cachedData, error)` — `errgroup.WithContext`, parallel fetch of all kinds via `BaseDynamicClient()`, skip unavailable experimental kinds gracefully (check `Discoverer.Status().InstalledKinds`), only write cache if `cacheGen` unchanged. Call `InvalidateCache()` when discovery detects a change in installed kinds.
- `InvalidateCache()` — increment `cacheGen`, nil cache

**RBAC helper:**
```go
func (h *Handler) canAccess(ctx, user, verb, resource, namespace) bool
```
Uses `AccessChecker.CanAccessGroupResource` with group `"gateway.networking.k8s.io"`.

**RBAC filtering** — reuse the generic `filterByRBAC[T namespacedResource]()` pattern from certmanager. GatewayClass is cluster-scoped — check with `canAccess(ctx, user, "list", "gatewayclasses", "")`.

**Endpoints (10 total, down from 15):**

| Handler method | Route | Logic |
|---|---|---|
| `HandleStatus` | `GET /status` | Return `Discoverer.Status()` |
| `HandleSummary` | `GET /summary` | getCached → compute counts/health per kind → return `GatewayAPISummary` |
| `HandleListGatewayClasses` | `GET /gatewayclasses` | getCached → filter RBAC → return |
| `HandleGetGatewayClass` | `GET /gatewayclasses/{name}` | Impersonating Get + normalize |
| `HandleListGateways` | `GET /gateways` | getCached → filter RBAC → return |
| `HandleGetGateway` | `GET /gateways/{ns}/{name}` | Impersonating Get + resolve attached routes **from cache** |
| `HandleListHTTPRoutes` | `GET /httproutes` | getCached → filter RBAC → return |
| `HandleGetHTTPRoute` | `GET /httproutes/{ns}/{name}` | Impersonating Get + resolve parents & backends |
| `HandleListRoutes` | `GET /routes?kind=X` | getCached → filter by Kind → filter RBAC → return |
| `HandleGetRoute` | `GET /routes/{kind}/{ns}/{name}` | Impersonating Get + resolve parents & backends |

**`HandleListRoutes`** accepts `?kind=grpcroutes|tcproutes|tlsroutes|udproutes` query param. Filters `cachedData.routes` by the `Kind` field. Returns 400 if kind is missing or invalid.

**`HandleGetRoute`** uses `routeKindGVR` map to resolve the GVR from the `{kind}` URL param. For GRPC routes, normalizes to `GRPCRouteDetail`. For TCP/TLS/UDP, normalizes to `SimpleRouteDetail` via `normalizeSimpleRouteDetail`.

**Relationship resolution:**

Gateway detail → attached routes:
- Filter the **cached** route data (30s TTL) by matching `parentRef.name`/`parentRef.namespace` to this Gateway. No fresh cluster-wide listing needed — the cache already has all routes.

Route detail → parent gateways + backend services:
1. After fetching the route, extract `spec.parentRefs`
2. For each parentRef, fetch the Gateway object (parallel via WaitGroup, 2s timeout)
3. If resolved, populate `GatewayConditions` on the ParentRef; if not, leave it empty
4. For HTTP/GRPC routes, extract `spec.rules[].backendRefs`; for simple routes, extract `spec.backendRefs`
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
        gr.Get("/summary", h.HandleSummary)
        gr.Get("/gatewayclasses", h.HandleListGatewayClasses)
        gr.With(resources.ValidateURLParams).Get("/gatewayclasses/{name}", h.HandleGetGatewayClass)
        gr.Get("/gateways", h.HandleListGateways)
        gr.With(resources.ValidateURLParams).Get("/gateways/{namespace}/{name}", h.HandleGetGateway)
        gr.Get("/httproutes", h.HandleListHTTPRoutes)
        gr.With(resources.ValidateURLParams).Get("/httproutes/{namespace}/{name}", h.HandleGetHTTPRoute)
        gr.Get("/routes", h.HandleListRoutes)
        gr.With(resources.ValidateURLParams).Get("/routes/{kind}/{namespace}/{name}", h.HandleGetRoute)
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

export type GatewayResourceKind =
  | "gatewayclasses" | "gateways" | "httproutes"
  | "grpcroutes" | "tcproutes" | "tlsroutes" | "udproutes";

export interface GatewayAPIStatus {
  available: boolean;
  version: string;
  installedKinds: string[];
  lastChecked: string;
}

export interface GatewayAPISummary {
  gatewayClasses: GatewayKindSummary;
  gateways: GatewayKindSummary;
  httpRoutes: GatewayKindSummary;
  grpcRoutes: GatewayKindSummary;
  tcpRoutes: GatewayKindSummary;
  tlsRoutes: GatewayKindSummary;
  udpRoutes: GatewayKindSummary;
}

export interface GatewayKindSummary {
  total: number;
  healthy: number;
  degraded: number;
}

export interface Condition {
  type: string;
  status: string;
  reason: string;
  message: string;
  lastTransitionTime?: string;
}

export interface ParentRef {
  group: string;
  kind: string;
  name: string;
  namespace: string;
  sectionName: string;
  status: string;
  gatewayConditions?: Condition[];
}

export interface BackendRef {
  group: string;
  kind: string;
  name: string;
  namespace: string;
  port?: number;
  weight?: number;
  resolved: boolean;
}

export interface RouteSummary {
  kind: string;
  name: string;
  namespace: string;
  hostnames: string[];
  parentRefs: ParentRef[];
  conditions: Condition[];
  age: string;
}

export interface GatewayClassSummary {
  name: string;
  controllerName: string;
  description: string;
  conditions: Condition[];
  age: string;
}

export interface Listener {
  name: string;
  port: number;
  protocol: string;
  hostname: string;
  attachedRouteCount: number;
  tlsMode?: string;
  certificateRef?: string;
  allowedRoutes?: string;
  conditions?: Condition[];
}

export interface GatewaySummary {
  name: string;
  namespace: string;
  gatewayClassName: string;
  listeners: Listener[];
  addresses: string[];
  attachedRouteCount: number;
  conditions: Condition[];
  age: string;
}

export interface GatewayDetail extends GatewaySummary {
  attachedRoutes: RouteSummary[];
}

export interface HTTPRouteSummary {
  name: string;
  namespace: string;
  hostnames: string[];
  parentRefs: ParentRef[];
  backendCount: number;
  conditions: Condition[];
  age: string;
}

export interface HTTPRouteMatch {
  pathType: string;
  pathValue: string;
  headers: string[];
  method: string;
  queryParams: string[];
}

export interface HTTPRouteFilter {
  type: string;
  details: string;
}

export interface HTTPRouteRule {
  matches: HTTPRouteMatch[];
  filters: HTTPRouteFilter[];
  backendRefs: BackendRef[];
}

export interface HTTPRouteDetail extends HTTPRouteSummary {
  rules: HTTPRouteRule[];
  parentRefs: ParentRef[];
}

export interface GRPCRouteMatch {
  service: string;
  method: string;
  headers: string[];
}

export interface GRPCRouteRule {
  matches: GRPCRouteMatch[];
  backendRefs: BackendRef[];
}

export interface GRPCRouteDetail {
  name: string;
  namespace: string;
  parentRefs: ParentRef[];
  rules: GRPCRouteRule[];
  conditions: Condition[];
  age: string;
}

export interface SimpleRouteDetail {
  kind: string;
  name: string;
  namespace: string;
  hostnames: string[];
  parentRefs: ParentRef[];
  backendRefs: BackendRef[];
  conditions: Condition[];
  age: string;
}

/** Discriminated union for typed list data in the dashboard */
export type GatewayListData =
  | { kind: "gatewayclasses"; items: GatewayClassSummary[] }
  | { kind: "gateways"; items: GatewaySummary[] }
  | { kind: "httproutes"; items: HTTPRouteSummary[] }
  | { kind: "grpcroutes" | "tcproutes" | "tlsroutes" | "udproutes"; items: RouteSummary[] };
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

**State (properly typed):**
- `status: Signal<GatewayAPIStatus | null>` — CRD availability
- `summary: Signal<GatewayAPISummary | null>` — counts per kind from `GET /gateway/summary`
- `loading: Signal<boolean>`
- `error: Signal<string>`
- `activeKind: Signal<GatewayResourceKind | null>` — which list is active (null = overview)
- `listData: Signal<GatewayListData | null>` — typed discriminated union
- `search: Signal<string>` — filter by name/namespace

**URL state sync:** Read/write `?kind=` query param via `URLSearchParams`. When `activeKind` changes, update `window.history.replaceState`. On mount, read `?kind=` to restore state. This ensures browser back navigation works.

**IS_BROWSER guard:** `if (!IS_BROWSER) return <div />` at the top of the component, same as all other islands.

**Flow:**
1. On mount, fetch `GET /api/v1/gateway/status` and `GET /api/v1/gateway/summary` in parallel
2. If `!status.available`, render "Gateway API not installed" state with guidance
3. If available, render overview cards for each installed kind using summary data
4. Each card shows: kind name, total count, healthy/degraded breakdown
5. Clicking a card sets `activeKind`, updates URL `?kind=`, and fetches the list endpoint
6. List view renders a kind-specific table (columns vary by kind)
7. "Back to overview" button clears `activeKind` and URL param

**Overview cards** (only show for installed kinds, data from `GatewayAPISummary`):
- GatewayClasses — total + accepted/pending
- Gateways — total + programmed/not programmed
- HTTPRoutes — total + accepted/not accepted
- GRPCRoutes — total
- TCPRoutes — total
- TLSRoutes — total
- UDPRoutes — total

**List table columns per kind:**

| Kind | Columns |
|---|---|
| GatewayClass | Name, Controller, Description, Accepted, Age |
| Gateway | Name, Namespace, Class, Listeners, Addresses, Programmed, Age |
| HTTPRoute | Name, Namespace, Hostnames, Parents, Backends, Accepted, Age |
| GRPCRoute/TCP/TLS/UDP | Name, Namespace, Parents, Accepted, Age |

Each row is clickable — navigates to detail page using per-kind URLs.

**API calls by kind:**
- `gatewayclasses` → `GET /api/v1/gateway/gatewayclasses`
- `gateways` → `GET /api/v1/gateway/gateways`
- `httproutes` → `GET /api/v1/gateway/httproutes`
- `grpcroutes|tcproutes|tlsroutes|udproutes` → `GET /api/v1/gateway/routes?kind=X`

**Response handling:** Use defensive unwrapping `Array.isArray(res.data) ? res.data : []` per CertificatesList pattern.

**Styling:** Use CSS custom property tokens exclusively. Follow CertificatesList.tsx patterns:
- Card: `rounded-lg border border-border-primary bg-bg-elevated p-5`
- Table: `rounded-lg border border-border-primary`, `bg-surface` header, `divide-y divide-border-subtle`
- Status badges: `text-success` for healthy, `text-warning` for degraded, `text-danger` for failed

### Step 3.4: Modify `frontend/lib/constants.ts`

Add Gateway API tab to the network section's `tabs` array (after EndpointSlices):
```ts
{ label: "Gateway API", href: "/networking/gateway-api" },
```

No `kind`/`count` — the dashboard fetches its own counts from the dedicated summary endpoint.

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

## Phase 4: Frontend Detail Routes + Detail Islands (Gateway + GatewayClass + Shared Components)

**Files to create:** 6

### Step 4.1: Shared UI components

**`frontend/components/gateway/ConditionsTable.tsx`** — SSR component (not an island)
Renders a conditions table with columns: Type, Status, Reason, Message, Last Transition.
Used by every detail island. Accepts `conditions: Condition[]` prop.

**`frontend/components/gateway/ParentGatewaysTable.tsx`** — SSR component
Renders parent gateway references with columns: Name, Namespace, Status.
Each row links to Gateway detail page. Accepts `parentRefs: ParentRef[]` prop.

**`frontend/components/gateway/BackendRefsTable.tsx`** — SSR component
Renders backend references with columns: Kind, Name, Namespace, Port, Weight, Resolved.
Service refs link to service detail page. Accepts `backendRefs: BackendRef[]` prop.

**`frontend/components/ui/GatewayBadges.tsx`** — SSR component
Following PolicyBadges/GitOpsBadges pattern:
- `ConditionBadge` — True=success, False=danger, Unknown=warning
- `ProtocolBadge` — HTTP/HTTPS/TLS/TCP/UDP with coloring

### Step 4.2: Detail route files (per-kind, matching project convention)

Create 6 route files. Each is a ~15-line wrapper following `certificates/[namespace]/[name].tsx` pattern:

**`frontend/routes/networking/gateway-api/gatewayclasses/[name].tsx`**
Cluster-scoped (no namespace). Renders `GatewayClassDetail` island with `name` prop.

**`frontend/routes/networking/gateway-api/gateways/[ns]/[name].tsx`**
Renders `GatewayDetail` island with `namespace` and `name` props.

All route files set `currentPath="/networking/gateway-api"` for SubNav highlighting.

### Step 4.3: `frontend/islands/GatewayClassDetail.tsx`

**Props:** `{ name: string }`
**API:** `GET /api/v1/gateway/gatewayclasses/${name}`
**IS_BROWSER guard** + loading/error state pattern per CertificateDetail.

**Panels:**
- Header: name + controller name
- Description text
- `<ConditionsTable conditions={data.conditions} />`

### Step 4.4: `frontend/islands/GatewayDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/gateways/${namespace}/${name}`

**Panels:**
- Header: name + namespace + gateway class (linked to GatewayClass detail)
- Addresses list
- Listeners table: Name, Port, Protocol, Hostname, TLS Mode, Certificate Ref, Attached Routes, Status
- Attached Routes: `<ParentGatewaysTable>` equivalent but for routes (Kind, Name, Namespace — each links to route detail)
- `<ConditionsTable conditions={data.conditions} />`

### Verification checkpoint:
```bash
cd frontend && deno fmt --check && deno lint
```

---

## Phase 5: Frontend Detail Islands (Routes)

**Files to create:** 7

### Step 5.1: Route detail route files

**`frontend/routes/networking/gateway-api/httproutes/[ns]/[name].tsx`**
Renders `GatewayHTTPRouteDetail` island.

**`frontend/routes/networking/gateway-api/grpcroutes/[ns]/[name].tsx`**
Renders `GatewayGRPCRouteDetail` island.

**`frontend/routes/networking/gateway-api/tcproutes/[ns]/[name].tsx`**
Renders `GatewaySimpleRouteDetail` island with `kind="TCPRoute"`.

**`frontend/routes/networking/gateway-api/tlsroutes/[ns]/[name].tsx`**
Renders `GatewaySimpleRouteDetail` island with `kind="TLSRoute"`.

**`frontend/routes/networking/gateway-api/udproutes/[ns]/[name].tsx`**
Renders `GatewaySimpleRouteDetail` island with `kind="UDPRoute"`.

### Step 5.2: `frontend/islands/GatewayHTTPRouteDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/httproutes/${namespace}/${name}`

**Panels:**
- Header: name + namespace
- Hostnames list
- `<ParentGatewaysTable parentRefs={data.parentRefs} />`
- Rules section (one card per rule):
  - Matches: path type/value, method, headers, query params
  - Filters: type + details
  - `<BackendRefsTable backendRefs={rule.backendRefs} />`
- `<ConditionsTable conditions={data.conditions} />`

### Step 5.3: `frontend/islands/GatewayGRPCRouteDetail.tsx`

**Props:** `{ namespace: string; name: string }`
**API:** `GET /api/v1/gateway/routes/grpcroutes/${namespace}/${name}`

**Panels:**
- Header: name + namespace
- `<ParentGatewaysTable parentRefs={data.parentRefs} />`
- Rules section:
  - Matches: Service, Method, Headers
  - `<BackendRefsTable backendRefs={rule.backendRefs} />`
- `<ConditionsTable conditions={data.conditions} />`

### Step 5.4: `frontend/islands/GatewaySimpleRouteDetail.tsx`

**Props:** `{ kind: string; namespace: string; name: string }`
**API:** `GET /api/v1/gateway/routes/${kind.toLowerCase()}s/${namespace}/${name}`

Single island handling TCP, TLS, and UDP routes. Layout:
- Header: name + namespace + kind badge
- Hostnames panel — **only rendered if `data.hostnames?.length > 0`** (TLS only)
- `<ParentGatewaysTable parentRefs={data.parentRefs} />`
- `<BackendRefsTable backendRefs={data.backendRefs} />`
- `<ConditionsTable conditions={data.conditions} />`

### Verification checkpoint:
```bash
cd frontend && deno fmt --check && deno lint
```

---

## Phase 6: Integration Testing + Full Verification

### Step 6.1: Backend type checking
```bash
cd backend && go vet ./... && go build ./... && go test ./internal/gateway/...
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
   - Overview cards display correct counts from summary endpoint
   - Click each card → list view loads with correct columns
   - Browser back returns to overview (URL state sync)
   - Click a row → detail page renders with relationship data
   - GatewayClass detail accessible at `/networking/gateway-api/gatewayclasses/:name`
   - Shared components render correctly (ConditionsTable, ParentGatewaysTable, BackendRefsTable)
   - Command palette "Gateway API" action works
   - "Not installed" state renders correctly (if no Gateway API CRDs)

### Step 6.4: Run full CI checks
```bash
make lint && make test && make build
```

---

## File Summary

### New files (20):
| File | Phase |
|---|---|
| `backend/internal/gateway/types.go` | 1 |
| `backend/internal/gateway/discovery.go` | 1 |
| `backend/internal/gateway/normalize.go` | 1 |
| `backend/internal/gateway/normalize_test.go` | 1 |
| `backend/internal/gateway/handler.go` | 2 |
| `frontend/lib/gateway-types.ts` | 3 |
| `frontend/routes/networking/gateway-api.tsx` | 3 |
| `frontend/islands/GatewayAPIDashboard.tsx` | 3 |
| `frontend/components/gateway/ConditionsTable.tsx` | 4 |
| `frontend/components/gateway/ParentGatewaysTable.tsx` | 4 |
| `frontend/components/gateway/BackendRefsTable.tsx` | 4 |
| `frontend/components/ui/GatewayBadges.tsx` | 4 |
| `frontend/routes/networking/gateway-api/gatewayclasses/[name].tsx` | 4 |
| `frontend/routes/networking/gateway-api/gateways/[ns]/[name].tsx` | 4 |
| `frontend/islands/GatewayClassDetail.tsx` | 4 |
| `frontend/islands/GatewayDetail.tsx` | 4 |
| `frontend/routes/networking/gateway-api/httproutes/[ns]/[name].tsx` | 5 |
| `frontend/routes/networking/gateway-api/grpcroutes/[ns]/[name].tsx` | 5 |
| `frontend/routes/networking/gateway-api/tcproutes/[ns]/[name].tsx` | 5 |
| `frontend/routes/networking/gateway-api/tlsroutes/[ns]/[name].tsx` | 5 |
| `frontend/routes/networking/gateway-api/udproutes/[ns]/[name].tsx` | 5 |
| `frontend/islands/GatewayHTTPRouteDetail.tsx` | 5 |
| `frontend/islands/GatewayGRPCRouteDetail.tsx` | 5 |
| `frontend/islands/GatewaySimpleRouteDetail.tsx` | 5 |

### Modified files (5):
| File | Phase | Change |
|---|---|---|
| `backend/internal/server/server.go` | 2 | Add GatewayHandler to Server + Deps structs |
| `backend/internal/server/routes.go` | 2 | Add registerGatewayRoutes + nil-guard call |
| `backend/cmd/kubecenter/main.go` | 2 | Initialize discoverer + handler, add to Deps |
| `frontend/lib/constants.ts` | 3 | Add Gateway API tab to network section |
| `frontend/islands/CommandPalette.tsx` | 3 | Add quick action |

### Key changes from v1 plan (review feedback):
- **Types consolidated:** Single `ParentRef` (no Summary/Detail split), single `Listener` (no split), single `SimpleRouteDetail` replaces TCP/TLS/UDP detail types
- **Endpoints reduced:** 15 → 10. Parameterized `GET /routes?kind=X` for GRPC/TCP/TLS/UDP lists, `GET /routes/{kind}/{ns}/{name}` for details
- **Summary endpoint added:** `GET /gateway/summary` provides counts+health for overview cards in a single call
- **Islands reduced:** No dispatcher island. TCP/TLS/UDP share `GatewaySimpleRouteDetail`. Total: 5 islands instead of 8
- **Per-kind route files:** Follow project convention, no `[kind]` parameter ambiguity, each island independently loadable
- **Shared components extracted:** ConditionsTable, ParentGatewaysTable, BackendRefsTable, GatewayBadges
- **Tests added:** `normalize_test.go` with table-driven tests for all normalize functions
- **Typed state:** `GatewayResourceKind` union type, `GatewayListData` discriminated union, no `any`
- **URL state sync:** `?kind=` query param for browser back navigation
- **Gateway detail uses cache:** Attached routes resolved from 30s cache, not fresh cluster-wide listing
