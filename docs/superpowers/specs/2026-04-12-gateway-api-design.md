# Gateway API Dashboard — Design Spec

**Date:** 2026-04-12
**Status:** Approved

## Overview

Add a "Gateway API" tab to the networking section that displays all Kubernetes Gateway API resources (GatewayClass, Gateway, HTTPRoute, GRPCRoute, TCPRoute, TLSRoute, UDPRoute) with CRD availability detection, a dashboard overview, and custom detail pages showing cross-resource relationships.

## Architecture

### Backend: `internal/gateway/` package

Dedicated package following the certmanager/policy pattern:

- **CRD Discovery** — checks for `gateway.networking.k8s.io` group CRDs at startup and on-demand (5 min TTL). Reports which resource kinds are installed.
- **Handler** — singleflight + 30s TTL cache for list endpoints. RBAC filtering via `CanAccessGroupResource`. User impersonation for all reads.
- **Normalized types** — Go structs for each Gateway API kind with relationship fields (Gateway carries attached route summaries, routes carry resolved parent Gateway and backend Service refs).
- **Relationship resolution** — detail endpoints fetch the primary resource plus related resources in parallel via `sync.WaitGroup` with 2s timeout per lookup.

### API Endpoints

All under `/api/v1/gateway/`:

| Endpoint | Description |
|---|---|
| `GET /gateway/status` | CRD discovery — which kinds are installed, API version |
| `GET /gateway/gatewayclasses` | List GatewayClasses (cluster-scoped) |
| `GET /gateway/gateways` | List Gateways with attached route counts |
| `GET /gateway/gateways/:ns/:name` | Gateway detail + attached routes list |
| `GET /gateway/httproutes` | List HTTPRoutes with parent/backend summaries |
| `GET /gateway/httproutes/:ns/:name` | HTTPRoute detail + resolved parents & backends |
| `GET /gateway/grpcroutes` | List GRPCRoutes |
| `GET /gateway/grpcroutes/:ns/:name` | GRPCRoute detail |
| `GET /gateway/tcproutes` | List TCPRoutes |
| `GET /gateway/tcproutes/:ns/:name` | TCPRoute detail |
| `GET /gateway/tlsroutes` | List TLSRoutes |
| `GET /gateway/tlsroutes/:ns/:name` | TLSRoute detail |
| `GET /gateway/udproutes` | List UDPRoutes |
| `GET /gateway/udproutes/:ns/:name` | UDPRoute detail |

### Frontend

**Route:** `/networking/gateway-api` — single tab in the networking SubNav.

**Dashboard island:** `GatewayAPIDashboard.tsx`
- Calls `GET /gateway/status` for CRD availability check
- Shows "Gateway API not installed" state with guidance if no CRDs found
- When available: overview cards per installed kind showing count + health summary (e.g., "4 Gateways — 3 Programmed, 1 Not Ready")
- Cards are clickable — navigate to a filtered list view within the same island (client-side tab switching)
- List views use kind-specific table columns

**Detail islands** (one per resource kind):

| Island | Key Panels |
|---|---|
| `GatewayClassDetail.tsx` | Description, controller name, supported features, conditions |
| `GatewayDetail.tsx` | Listeners table (port, protocol, hostname, TLS), attached routes list (linked), addresses, conditions |
| `HTTPRouteDetail.tsx` | Parent gateways (linked), hostnames, rules table (matches + filters + backends with weights), resolved backend service links, conditions |
| `GRPCRouteDetail.tsx` | Parent gateways, rules (method/header matches + backends), conditions |
| `TCPRouteDetail.tsx` | Parent gateways, backend refs, conditions |
| `TLSRouteDetail.tsx` | Parent gateways, hostnames, backend refs, conditions |
| `UDPRouteDetail.tsx` | Parent gateways, backend refs, conditions |

**Detail routes:**
- Namespaced kinds: `/networking/gateway-api/:kind/:ns/:name` — parameterized route rendering the appropriate detail island based on `:kind`.
- GatewayClass (cluster-scoped): `/networking/gateway-api/gatewayclasses/:name` — no namespace param.

**Types:** `frontend/lib/gateway-types.ts` — TypeScript interfaces matching backend normalized types.

**Nav integration:**
- New tab in `constants.ts` under network section: `{ label: "Gateway API", href: "/networking/gateway-api" }`
- Command palette quick action: "Gateway API" navigates to the dashboard

## Data Model

### Backend Normalized Types (`internal/gateway/types.go`)

```
GatewayAPIStatus (CRD discovery response)
├── available: bool
├── version: string (e.g., "v1", "v1beta1")
└── installedKinds: []string

Condition (shared across all types)
├── type, status, reason, message, lastTransitionTime

GatewayClassSummary
├── name, controllerName, description
└── conditions: []Condition (Accepted)

GatewaySummary
├── name, namespace, className, age
├── listeners: []ListenerSummary (name, port, protocol, hostname, attachedRouteCount)
├── addresses: []string
├── attachedRoutes: []RouteSummary (kind, name, namespace)
└── conditions: []Condition (Accepted, Programmed)

GatewayDetail (extends GatewaySummary)
├── listeners: []ListenerDetail (adds TLS config, allowedRoutes, conditions per listener)
└── attachedRoutes: []RouteSummary (full list with hostnames)

HTTPRouteSummary
├── name, namespace, age
├── hostnames: []string
├── parentRefs: []ParentRefSummary (gateway name/ns, status)
├── backendCount: int
└── conditions: []Condition (Accepted, ResolvedRefs)

HTTPRouteDetail (extends HTTPRouteSummary)
├── rules: []HTTPRouteRule
│   ├── matches: []HTTPRouteMatch (path, headers, method, queryParams)
│   ├── filters: []HTTPRouteFilter (requestRedirect, requestHeaderModifier, etc.)
│   └── backendRefs: []BackendRefDetail (service name/ns/port, weight, resolved: bool)
└── parentRefs: []ParentRefDetail (gateway name/ns + gateway conditions)

RouteSummary (shared for GRPC/TCP/TLS/UDP list views)
├── kind, name, namespace, age
├── parentRefs: []ParentRefSummary
└── conditions: []Condition

GRPCRouteDetail
├── (extends RouteSummary)
├── rules: []GRPCRouteRule
│   ├── matches: []GRPCRouteMatch (service, method, headers)
│   └── backendRefs: []BackendRefDetail
└── parentRefs: []ParentRefDetail

TCPRouteDetail / TLSRouteDetail / UDPRouteDetail
├── (extends RouteSummary)
├── backendRefs: []BackendRefDetail
└── parentRefs: []ParentRefDetail
(TLSRouteDetail also has hostnames: []string)
```

### TypeScript Interfaces (`frontend/lib/gateway-types.ts`)

Mirror the Go types with camelCase fields. Use existing `K8sMetadata` for common metadata.

## Relationship Resolution

### Gateway → Routes
When fetching Gateway detail, query all installed route kinds across all namespaces. Filter routes whose `spec.parentRefs` reference this Gateway (match by name, namespace, optionally sectionName/port). Return as `attachedRoutes` array.

### Route → Gateway
When fetching any route detail, resolve each `spec.parentRefs` entry to the actual Gateway object. Include the Gateway's conditions so the detail page can show attachment status.

### Route → Backend Services
For HTTPRoute/GRPCRoute, resolve `spec.rules[].backendRefs` to Service objects. For TCP/TLS/UDP routes, resolve `spec.backendRefs` directly. Include `resolved: bool` — false if the Service doesn't exist or RBAC blocks access.

All resolution uses `sync.WaitGroup` for parallel fetching with 2s timeout. Unresolvable references return `resolved: false` rather than erroring the request.

## Caching

| Scope | Strategy |
|---|---|
| List endpoints | singleflight + 30s TTL cache (keyed by user for RBAC) |
| Detail endpoints | No cache — always fresh for current relationship data |
| CRD discovery | Cached at startup, 5 min TTL, refreshable via status endpoint |

## RBAC

- All reads use user impersonation — users only see resources they have access to
- Relationship resolution respects RBAC — unresolvable refs show name only with `resolved: false`
- GatewayClass is cluster-scoped — uses `CanAccessGroupResource` for `gateway.networking.k8s.io/gatewayclasses`

## Files to Create

### Backend
- `backend/internal/gateway/types.go` — normalized Go types
- `backend/internal/gateway/discovery.go` — CRD discovery with caching
- `backend/internal/gateway/handler.go` — HTTP handlers with singleflight/cache
- `backend/internal/gateway/relationships.go` — cross-resource resolution logic

### Frontend
- `frontend/routes/networking/gateway-api.tsx` — dashboard route
- `frontend/routes/networking/gateway-api/[kind]/[ns]/[name].tsx` — detail route
- `frontend/islands/GatewayAPIDashboard.tsx` — overview cards + list views
- `frontend/islands/GatewayClassDetail.tsx` — GatewayClass detail page
- `frontend/islands/GatewayDetail.tsx` — Gateway detail page
- `frontend/islands/GatewayHTTPRouteDetail.tsx` — HTTPRoute detail page
- `frontend/islands/GatewayGRPCRouteDetail.tsx` — GRPCRoute detail page
- `frontend/islands/GatewayTCPRouteDetail.tsx` — TCPRoute detail page
- `frontend/islands/GatewayTLSRouteDetail.tsx` — TLSRoute detail page
- `frontend/islands/GatewayUDPRouteDetail.tsx` — UDPRoute detail page
- `frontend/lib/gateway-types.ts` — TypeScript interfaces

### Files to Modify
- `frontend/lib/constants.ts` — add Gateway API tab to network section
- `backend/internal/server/routes.go` — register gateway routes
- `backend/internal/server/server.go` — initialize gateway handler
