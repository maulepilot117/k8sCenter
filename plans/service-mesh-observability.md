---
title: feat — Service Mesh Observability (Istio + Linkerd)
type: feat
status: active
date: 2026-04-16
roadmap: "#6"
---

# feat: Service Mesh Observability (Istio + Linkerd)

## Overview

Deliver vCenter-level visibility into Istio and Linkerd service meshes: auto-detect the installed mesh(es), surface traffic routing config, expose mTLS posture per workload, visualize mesh edges on the existing topology graph, and pull golden-signal metrics (RPS, p95 latency, error rate) from Prometheus. Read-only in v1 — write/wizard support deferred to a follow-up phase once the inventory surface is stable.

Dual-mesh v1 scope is deliberate: the dual-tool precedents in this repo (`internal/policy/` for Kyverno+Gatekeeper, `internal/gitops/` for Argo+Flux) have shown that adding the second tool later is painful. The cost of designing for two meshes up front is modest because the per-mesh adapters stay isolated; the pain is amortized across four shippable PRs.

Closes roadmap item **#6**.

## Problem Frame

Kubernetes operators running a service mesh today have no unified view inside k8sCenter:
- Mesh install state, sidecar-injection coverage per namespace, and control-plane health live in disparate CLIs (`istioctl`, `linkerd check`).
- Traffic-shifting config (VirtualService, DestinationRule, ServiceProfile) requires `kubectl get … -o yaml` to inspect.
- mTLS posture is opaque: it is unclear which workloads use mTLS, which have it disabled via a PeerAuthentication exception, and which are unmeshed entirely.
- The existing topology graph (Phase 7B) shows owner/selector/mount edges but no mesh-aware traffic edges.
- Mesh-specific Prometheus metrics (`istio_*`, `linkerd_*`) are reachable through the existing Prometheus proxy but not surfaced as first-class golden signals.

The gap matches the pattern that motivated the cert-manager observatory (Phase 11A) and the policy dashboard (Phase 8A): the CRDs and metrics already exist, but operators lack a GUI that aggregates and RBAC-filters them.

## Requirements Trace

- **R1. Detect mesh installation** — Report Istio and/or Linkerd presence, control-plane namespace, and version. Graceful empty state when neither is installed.
- **R2. List mesh-managed resources** — Enumerate VirtualService, DestinationRule, Gateway, PeerAuthentication, AuthorizationPolicy (Istio) and ServiceProfile, Server, HTTPRoute, AuthorizationPolicy, MeshTLSAuthentication (Linkerd) with RBAC filtering.
- **R3. Detail views** — Per-CRD detail with normalized status, raw YAML fallback, and cross-links to owned/referenced resources.
- **R4. mTLS posture dashboard** — Per-workload mTLS status (Istio PeerAuthentication mode + connection_security_policy metric; Linkerd default-on + edge security observation).
- **R5. Mesh-aware topology** — Mesh traffic edges overlaid on existing `/observability/topology` view with a toggle. Edges emitted from VirtualService destinations and Linkerd edges output.
- **R6. Golden signals** — Per-service RPS, p50/p95/p99 latency, and error rate surfaced on the service detail view when mesh metrics are available.
- **R7. RBAC-aware** — Every mesh list and detail endpoint filters through `AccessChecker.CanAccessGroupResource` using the CRD's real API group (`networking.istio.io`, `policy.linkerd.io`, etc.), matching Phase 11A precedent.
- **R8. Multi-cluster correct** — Informers for local cluster reads; dynamic-client calls for remote clusters, matching the project's standing rule. No informer watches on remote clusters.
- **R9. Non-functional** — `go vet`, `go test ./...`, `deno lint`, `deno fmt --check`, `deno task build` all pass. Zero hardcoded Tailwind color classes (Phase 6C compliance). Theme tokens only.

## Scope Boundaries

- **Read-only in v1.** No mesh config wizards, no apply/delete from the UI. Users who need to edit VirtualServices or AuthorizationPolicies use the existing YAML editor (`/yaml`).
- **No Istio Ambient Mode-specific features.** Ambient mode (ztunnel, waypoint proxies) is detected but treated as "Istio installed" — dedicated ambient views deferred.
- **No Kiali replacement.** Kiali has deep Istio-only features (distributed tracing overlay, live traffic animation) that are out of scope.
- **No service mesh installer.** Detecting an installed mesh is in scope; installing one is not.
- **No cross-mesh traffic.** Multi-mesh federation (Istio multi-primary, Linkerd multi-cluster) is detected-as-version but not visualized.
- **No Open Service Mesh (OSM) or Consul Connect.** Istio and Linkerd only in v1.

### Deferred to Separate Tasks

- **Mesh configuration wizards** — VirtualService / DestinationRule / PeerAuthentication / ServiceProfile creation via the wizard pipeline. Separate plan after v1 lands and the inventory surface is battle-tested on the homelab.
- **Distributed tracing integration** — Tempo/Jaeger client + span viewer. Tracked separately as it is orthogonal to mesh-specific CRDs and metrics.
- **Mesh-aware SLO scoring** — Once golden signals land, SLO burn-rate alerting on top of them is a natural follow-up.

## Context & Research

### Relevant Code and Patterns

Precedent packages to mirror (dual-tool CRD pattern):

- **GitOps** (`backend/internal/gitops/`) — Closest analog. Dual-tool adapters (`argocd.go`, `flux.go`), normalized types with `Tool` + `Kind` discriminators, singleflight + 30s cache, per-user RBAC filter, user impersonation on writes. See handler at `backend/internal/gitops/handler.go`, discoverer at `backend/internal/gitops/discovery.go`.
- **Policy** (`backend/internal/policy/`) — Similar dual-tool shape. Use for the compliance-score angle if a "mesh coverage score" (% workloads in mesh) is added. See `backend/internal/policy/handler.go`.
- **Cert-Manager** (`backend/internal/certmanager/`) — Most recent single-tool CRD observatory. Poller pattern (`poller.go`) is the reference if we add a mesh-health poller in v2.

Shared infrastructure to consume:

- `AccessChecker.CanAccessGroupResource(ctx, username, groups, verb, apiGroup, resource, namespace)` at `backend/internal/auth/access.go` — used by all three precedents.
- `PrometheusClient.Query` / `QueryRange` at `backend/internal/monitoring/` — direct Go-side PromQL for golden signals (not the HTTP proxy).
- `topology.ResourceLister` at `backend/internal/topology/builder.go` — graph builder decoupled from data source. Extend with new lister methods for mesh data rather than forking the builder.
- `InformerManager` — extend with per-mesh CRD listers (local cluster only). Remote clusters use `dynamic.Interface` directly.

Frontend patterns:

- `frontend/islands/PolicyDashboard.tsx` — detected-engine banner + empty-state installation link is the exact shell for the mesh status page.
- `frontend/islands/NamespaceTopology.tsx` — edge renderer supports extensible `type` values (`owner`, `selector`, `mount`, `ingress`). Adding `mesh_vs` / `mesh_edge` is additive, not a rewrite.
- `frontend/components/SubNav.tsx` — tab count pattern via `/resources/counts?namespace=` already batches counts; extend `RESOURCE_API_KINDS` with mesh CRDs.
- Nav section placement: `frontend/lib/constants.ts` has a Networking section (for Cilium/Hubble). Mesh observability primary views fit there; topology overlay sits on the existing Observability topology page.

### External References

- Istio CRD reference: https://istio.io/latest/docs/reference/config/networking/ and https://istio.io/latest/docs/reference/config/security/
- Istio standard metrics: https://istio.io/latest/docs/reference/config/metrics/ — key counter `istio_requests_total`, histogram `istio_request_duration_milliseconds`, label `connection_security_policy=mutual_tls` is the signal for active mTLS.
- Linkerd CRDs: https://linkerd.io/2-edge/reference/ — ServiceProfile (`linkerd.io/v1beta1`), policy resources (`policy.linkerd.io/v1alpha1`/`v1beta1`/`v1beta3`).
- Linkerd proxy metrics: https://linkerd.io/2-edge/reference/proxy-metrics/ — `request_total`, `response_latency_ms` with `direction=inbound|outbound`.
- Linkerd mTLS: https://linkerd.io/2-edge/features/automatic-mtls/ — default-on, no STRICT/PERMISSIVE modes. Observable signal is the `secure_channel` tag on edges.

### Institutional Learnings

- **`docs/solutions/` applicability:** Phase 11A's lesson on CRD discovery caching (5min stale, lazy re-probe) should carry over. Phase 8A's lesson on "service account fetches cluster-wide data, then filter per-user via `CanAccessGroupResource`" is the canonical RBAC shape.
- **Informer vs. dynamic-client split:** Remote clusters (Phase 2 multi-cluster) always use dynamic clients — do not add informer watches that cover remote clusters. This rule is enforced in every existing resource handler; mesh CRDs must respect it.
- **Rate-limit and cap reads:** Policy handler uses a 5-wide semaphore + 5s timeout + 100-item cap when enumerating Gatekeeper constraints. Mesh CRDs in large clusters (1000+ VirtualServices) need the same protection.

## Key Technical Decisions

- **Dual-tool adapter pattern mirrors `gitops/`, not `policy/`.** GitOps keeps per-tool adapter files (`argocd.go`, `flux.go`) with a slim shared handler; Policy over-unified types. Mesh metric shapes (Istio's destination-centric labels vs. Linkerd's direction+authority) are too different to share a single metrics type — per-mesh adapters stay separate.
- **Normalized shared types only where semantics align.** `MeshStatus`, `MeshMembership` (per-namespace injection status), and the outer `MeshedWorkload` wrapper are shared. `IstioTrafficRoute` and `LinkerdTrafficRoute` remain mesh-specific under a common `TrafficRoute` interface with a `Mesh` discriminator field. Frontend TypeScript mirrors this with a tagged union.
- **mTLS posture computed server-side, not inferred client-side.** The handler combines PeerAuthentication mode (Istio) or default-on (Linkerd) with the `connection_security_policy` Prometheus label to produce a three-state result: `active` / `inactive` / `mixed`. Clients do not see the raw data.
- **Golden signals on-demand, not cached.** PromQL queries execute at request time against the existing Prometheus client (templated queries with label substitution — validated via the existing `QueryTemplate.Render` path). No separate cache; Prometheus already caches.
- **Topology overlay is additive.** Extend `ResourceLister` with two new methods (`ListVirtualServices`, `ListMeshEdges`) and add mesh-edge emission to the graph builder behind a flag. The existing per-namespace topology response format gains optional fields; no breaking change.
- **Local cluster informers for CRD lists; remote uses dynamic-client.** Identical to the policy/gitops/cert-manager pattern. Informer startup is gated on mesh CRD presence (detected by discoverer) to avoid listing non-existent resources.
- **Rate-limit + 2000-node cap on topology overlay endpoint.** Large meshes (1000+ services) would generate thousands of edges; cap at 2000 and return a truncation warning field, matching the Phase 7B topology endpoint.

## Open Questions

### Resolved During Planning

- **Q: Shared types or per-mesh types?** — Resolved: hybrid. Shared outer types (`MeshStatus`, `MeshMembership`, `MeshedWorkload`). Per-mesh types for routing config and policies. Golden signals normalized to `{ rps, p50_ms, p95_ms, p99_ms, error_rate }` since both meshes export compatible-enough quantiles.
- **Q: Where do mesh views live in navigation?** — Resolved: primary views under `/networking/mesh/*` (Networking section, adjacent to Cilium/Hubble); topology overlay is a toggle on the existing `/observability/topology` page. Rationale: mesh is data-plane networking; putting it next to CNI policy is the least-surprising placement.
- **Q: Read-only in v1?** — Confirmed. Apply goes through the existing `/yaml/apply` endpoint (SSA). No mesh wizards in v1.
- **Q: Should mTLS status be a standalone page or a column?** — Resolved: both. Per-workload mTLS column on the workload list, standalone `/networking/mesh/mtls` page with namespace-level aggregation + red/yellow/green posture cards.

### Deferred to Implementation

- **Exact label sets for golden-signal PromQL templates.** Validated during Phase C implementation against real Istio/Linkerd Prometheus scrapes — label cardinality varies by mesh version (v1.22 vs v1.24 Istio have different default labels).
- **Istio Ambient Mode detection details.** The `ambient.istio.io/redirection` namespace label and absence of sidecar annotations together signal ambient. Exact detection logic resolved when an ambient cluster is available for testing.
- **Rate-limit threshold for topology overlay at scale.** Starting value 10 req/min; tune after homelab validation.

## Output Structure

```
backend/internal/servicemesh/
├── discovery.go          # MeshDiscoverer — Istio + Linkerd CRD detection, control-plane ns, version
├── types.go              # MeshStatus, MeshMembership, MeshedWorkload, TrafficRoute interface
├── normalize.go          # normalizeIstioVS, normalizeLinkerdSP, mTLS posture computation
├── handler.go            # HTTP handlers, singleflight + 30s cache, RBAC filter
├── istio.go              # Istio adapter — CRD listers, mTLS from PeerAuthentication
├── linkerd.go            # Linkerd adapter — CRD listers, default-on mTLS + edge observation
├── metrics.go            # Prometheus golden-signal queries (templates per mesh)
├── discovery_test.go     # CRD presence table tests
├── normalize_test.go     # Status + mTLS posture unit tests
├── istio_test.go         # Adapter tests with fake dynamic client
├── linkerd_test.go       # Adapter tests with fake dynamic client
└── metrics_test.go       # PromQL template rendering + label substitution

frontend/islands/
├── MeshDashboard.tsx             # Status banner + mesh coverage summary
├── MeshRoutingList.tsx           # Unified list of VirtualServices / ServiceProfiles / etc.
├── MeshRouteDetail.tsx           # Per-route detail with raw YAML fallback
├── MTLSPosture.tsx               # Per-namespace mTLS posture cards + drill-down table
└── MeshTopologyOverlay.tsx       # Toggle + renderer for mesh edges on topology page

frontend/routes/networking/mesh/
├── index.tsx                     # Redirect to /networking/mesh/dashboard
├── dashboard.tsx                 # MeshDashboard island
├── routing.tsx                   # MeshRoutingList island
├── routing/[id].tsx              # MeshRouteDetail island (composite id: "mesh:ns:kind:name")
└── mtls.tsx                      # MTLSPosture island

frontend/lib/
├── mesh-types.ts                 # TS mirror of backend types (tagged unions)
└── mesh-api.ts                   # Typed API client wrappers for /mesh/* endpoints
```

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

**Backend request flow:**

```
GET /api/v1/mesh/status
    ↓
Handler.HandleStatus
    ↓
MeshDiscoverer.Status()  ← 5min cached
    ↓
returns { istio: {installed, namespace, version}, linkerd: {installed, namespace, version} }

GET /api/v1/mesh/routing?namespace=foo
    ↓
Handler.HandleListRoutes
    ↓
singleflight.Do("routing:foo", 30s cache)
    ↓
parallel: istio.ListRoutes(ns)  +  linkerd.ListRoutes(ns)
    ↓
filterByRBAC(user, routes)  ← CanAccessGroupResource per CRD group
    ↓
returns { routes: [NormalizedRoute{Mesh, Kind, Name, Namespace, RawRef, ...}] }

GET /api/v1/mesh/mtls?namespace=foo
    ↓
Handler.HandleMTLSPosture
    ↓
istio:   read PeerAuthentication (namespace + mesh-level) → compute effective mode per workload
         optionally query `sum by (destination_workload)(rate(istio_requests_total{connection_security_policy="mutual_tls"}[5m]))`
linkerd: default-on → observed via presence of linkerd-proxy sidecar; optionally tap metrics
    ↓
returns { workloads: [{name, mesh, mtls: "active"|"inactive"|"mixed", source: "policy"|"metric"|"default"}] }

GET /api/v1/mesh/metrics?service=bar&namespace=foo
    ↓
Handler.HandleGoldenSignals
    ↓
detect which mesh covers this service (via MeshMembership)
    ↓
mesh-specific QueryTemplate.Render({ns, svc}) → PromQL queries in parallel
    ↓
returns { rps, p50_ms, p95_ms, p99_ms, error_rate, mesh: "istio"|"linkerd" }
```

**Topology overlay (Phase D):**

```
GET /api/v1/topology/{ns}?overlay=mesh
    ↓
existing builder builds node/edge graph from ResourceLister
    ↓
if overlay=mesh AND mesh installed:
    ListVirtualServices(ns) → emit edges from vs.spec.hosts → destination.host
    (Linkerd) walk ServiceProfile → infer routes OR query `linkerd viz edges` PromQL equivalent
    ↓
additive: graph.edges += mesh_edges  (edge.type = "mesh_vs" | "mesh_destination")
    ↓
2000-edge cap enforced; truncation flag in response
```

## Implementation Units

Phases deliver independently-shippable PRs. Each phase is reviewed and merged before the next starts (per CLAUDE.md "PHASED EXECUTION" rule).

### Phase A — Backend foundation + routing inventory (PR 1)

- [x] **Unit A1: Package skeleton + MeshDiscoverer**

**Goal:** New `internal/servicemesh/` package with CRD-based discovery for Istio and Linkerd, returning a stable `MeshStatus` response.

**Requirements:** R1, R7

**Dependencies:** None.

**Files:**
- Create: `backend/internal/servicemesh/discovery.go`
- Create: `backend/internal/servicemesh/types.go`
- Create: `backend/internal/servicemesh/discovery_test.go`

**Approach:**
- `MeshDiscoverer` struct mirrors `gitops.GitOpsDiscoverer` — same 5min `recheckInterval`, same lazy re-probe on `Status()`.
- Istio detection: probe `networking.istio.io/v1` for `virtualservices`; cross-check by listing deployments labelled `app=istiod` in any namespace.
- Linkerd detection: probe `policy.linkerd.io/v1beta3` for `servers`; cross-check by listing deployments labelled `linkerd.io/control-plane-component=identity`.
- Version detection from the control-plane deployment's image tag (same heuristic as cert-manager `discovery.go`).
- `types.go` defines: `MeshType` enum (Istio / Linkerd), `MeshInfo { Type, Installed, Namespace, Version }`, `MeshStatus { Istio *MeshInfo, Linkerd *MeshInfo }`.

**Patterns to follow:**
- `backend/internal/gitops/discovery.go` for CRD probe loop
- `backend/internal/certmanager/discovery.go` for version extraction from deployment image

**Test scenarios:**
- Happy path: both meshes installed → Status returns both present with versions.
- Happy path: only Istio installed → Linkerd field is `nil` (or `Installed: false`).
- Edge case: Istio CRDs present but control-plane deployment missing → `Installed: true, Version: "unknown"` (CRDs imply install, even if istiod crashed).
- Edge case: discovery server returns `ErrGroupDiscoveryFailed` → discoverer returns cached `Status` + logs warning, does not fail the request.
- Edge case: stale cache (past `recheckInterval`) → lazy re-probe fires on next `Status()` call.

**Verification:**
- `go test ./internal/servicemesh/... -run TestDiscoverer` passes.
- Smoke: hit `GET /api/v1/mesh/status` on homelab with Istio installed and confirm the response shape.

---

- [x] **Unit A2: Normalized types + Istio adapter**

**Goal:** Istio adapter lists VirtualService, DestinationRule, Gateway, PeerAuthentication, AuthorizationPolicy and normalizes them into the shared `TrafficRoute` / `NormalizedPolicy` shapes.

**Requirements:** R2, R3, R7

**Dependencies:** Unit A1.

**Files:**
- Create: `backend/internal/servicemesh/istio.go`
- Create: `backend/internal/servicemesh/normalize.go`
- Create: `backend/internal/servicemesh/istio_test.go`
- Modify: `backend/internal/servicemesh/types.go` (add `TrafficRoute`, `MeshedPolicy`, `NormalizedMeshMembership`)

**Approach:**
- `istio.go`: Uses `dynamic.Interface` for all reads (works against informer cache on local cluster via the typed wrapper, and direct API on remote).
- Composite ID scheme: `"istio:{namespace}:{kind}:{name}"` where kind is `vs`/`dr`/`gw`/`pa`/`ap`. Matches gitops' `"argo:ns:name"` shape.
- `normalize.go` exports `normalizeIstioVirtualService(u *unstructured.Unstructured) TrafficRoute`, etc. Keeps raw `map[string]any` as a fallback field for the detail view.
- Rate-limit list calls: 5s timeout, 2000-item cap per CRD type.

**Patterns to follow:**
- `backend/internal/gitops/argocd.go` for dynamic-client adapter shape
- `backend/internal/policy/kyverno.go` for semaphore+timeout+cap guards

**Test scenarios:**
- Happy path: unstructured VirtualService with two destinations → normalized with correct host list and destinations.
- Happy path: DestinationRule with subset weights → normalized with subsets array preserved.
- Edge case: AuthorizationPolicy with no rules (deny-all effect) → normalized with empty `rules` and a computed `effect: "deny_all"`.
- Error path: dynamic client returns `403 Forbidden` for a specific CRD → adapter returns partial result (other CRDs listed) with an error annotation.
- Integration: list with namespace="" hits cluster-scope; with namespace=specified hits one namespace only.

**Verification:**
- `go test ./internal/servicemesh/... -run TestIstio` passes with fake dynamic client fixtures.

---

- [x] **Unit A3: Linkerd adapter**

**Goal:** Linkerd adapter for ServiceProfile, AuthorizationPolicy, Server, HTTPRoute, MeshTLSAuthentication.

**Requirements:** R2, R3, R7

**Dependencies:** Unit A1, Unit A2 (shares normalize.go infrastructure).

**Files:**
- Create: `backend/internal/servicemesh/linkerd.go`
- Create: `backend/internal/servicemesh/linkerd_test.go`
- Modify: `backend/internal/servicemesh/normalize.go` (add Linkerd normalizers)

**Approach:**
- Same composite ID shape: `"linkerd:{namespace}:{kind}:{name}"`.
- Normalizers per CRD: `normalizeServiceProfile`, `normalizeLinkerdAuthzPolicy`, `normalizeLinkerdServer`, `normalizeLinkerdHTTPRoute`.
- HTTPRoute uses the Linkerd-flavored Gateway API CRD (`policy.linkerd.io/v1beta1 HTTPRoute`), not the upstream `gateway.networking.k8s.io/v1 HTTPRoute` — these are distinct. Handler disambiguates via API group.

**Patterns to follow:** Unit A2's normalizer layout.

**Test scenarios:**
- Happy path: ServiceProfile with three routes → normalized with route matchers preserved.
- Happy path: Linkerd Server selecting pods by label → normalized with the label selector emitted as a string.
- Edge case: HTTPRoute from Linkerd group vs upstream group → only Linkerd group is picked up.
- Edge case: empty ServiceProfile (no routes) → normalized with empty routes array, not nil.

**Verification:** `go test ./internal/servicemesh/... -run TestLinkerd` passes.

---

- [x] **Unit A4: Handler + routes + RBAC filtering**

**Goal:** HTTP handlers for `GET /mesh/{status,routing,policies,routing/{id}}` with singleflight + 30s cache and per-user RBAC filtering.

**Requirements:** R1, R2, R3, R7, R8

**Dependencies:** A1–A3.

**Files:**
- Create: `backend/internal/servicemesh/handler.go`
- Modify: `backend/internal/server/routes.go` (mount `/mesh/*` routes)
- Create: `backend/internal/servicemesh/handler_test.go`

**Approach:**
- `Handler` struct carries `Discoverer`, `AccessChecker`, `DynamicClient`, `Logger`, `Cache` fields — mirrors gitops `Handler` exactly.
- `filterByRBAC[T]` generic helper factored into `types.go`, signature `filterByRBAC[T Resource](ctx, checker, user, items) []T`. Reused from Phase 11A (already a proven shape).
- Route layout:
  - `GET /api/v1/mesh/status` → HandleStatus
  - `GET /api/v1/mesh/routing?namespace=` → HandleListRoutes
  - `GET /api/v1/mesh/routing/{id}` → HandleGetRoute (composite id URL-encoded)
  - `GET /api/v1/mesh/policies?namespace=` → HandleListPolicies
- All routes require auth; writes reserved for v2.

**Patterns to follow:**
- `backend/internal/gitops/handler.go` for composite-ID parsing and per-handler cache
- `backend/internal/policy/handler.go` for `filterByRBAC` pattern

**Test scenarios:**
- Happy path: authenticated user with cluster-wide access → gets all routes.
- RBAC: user with access only to namespace "foo" → sees only foo's routes, no 403 on mixed data.
- Cache: two concurrent requests coalesce via singleflight; verify with a counter in the adapter mock.
- Error path: composite ID malformed → 400 with structured error message.
- Error path: no mesh installed → routing list returns `{routes: []}` with `status.installed = false`, not 500.

**Verification:**
- Unit tests pass.
- E2E smoke: `curl /api/v1/mesh/status` on homelab with Istio installed returns expected shape.

**Notes carried in from A3 (2026-04-19):**
- `ListIstio` and `ListLinkerd` are structurally near-identical — both fan out a goroutine per CRD, share `listCRD` for timeout/cap, and return a `{Routes, Policies, Errors map[string]string}` bundle. After A4 is wired up and the handler call-sites are visible, decide whether a generic helper (e.g., `listMeshCRDs[T](...)`) pays its way. Defer for now; the plan's "per-mesh adapter isolation" principle is the tiebreaker while the handler side is still unwritten.

### Phase B — mTLS posture + golden signals (PR 2)

- [ ] **Unit B1: mTLS posture computation**

**Goal:** Compute per-workload mTLS status for both meshes with a unified three-state result (`active` / `inactive` / `mixed`).

**Requirements:** R4

**Dependencies:** Phase A.

**Files:**
- Create: `backend/internal/servicemesh/mtls.go`
- Modify: `backend/internal/servicemesh/handler.go` (add `HandleMTLSPosture`)
- Create: `backend/internal/servicemesh/mtls_test.go`
- Modify: `backend/internal/server/routes.go` (add `GET /mesh/mtls`)

**Approach:**
- Istio: list PeerAuthentications at mesh level (root-of-mesh namespace), namespace level, workload level. Apply the precedence rule (workload > namespace > mesh) per workload. Output `IstioMTLSState { Mode: STRICT|PERMISSIVE|DISABLE, Source: mesh|namespace|workload }`.
- Linkerd: mTLS is default-on for meshed workloads. Output is `active` if the pod carries `linkerd.io/proxy-version` annotation, else `inactive` (unmeshed). No PERMISSIVE equivalent.
- Optional metric cross-check (Istio only): query `sum by (destination_workload, destination_workload_namespace)(rate(istio_requests_total{connection_security_policy="mutual_tls"}[5m]))` vs. total — if ratio < 1, posture is `mixed` regardless of policy.

**Patterns to follow:** Policy evaluation precedence mirrors Kyverno's cluster-vs-namespace pattern in `backend/internal/policy/kyverno.go`.

**Test scenarios:**
- Happy path: Istio mesh-level STRICT, no namespace or workload overrides → all workloads `active`.
- Happy path: namespace-level PERMISSIVE override → workloads in that namespace `inactive-or-mixed`.
- Edge case: workload-level DISABLE overrides namespace STRICT → that workload `inactive`.
- Edge case: Linkerd meshed pod in unmeshed namespace → `active` (annotation wins).
- Integration: metric says 90% mTLS but policy says STRICT → posture is `mixed`, `source: "metric"`.
- Error path: Prometheus unavailable → fall back to policy-only evaluation; `source: "policy"`.

**Verification:** unit tests cover all precedence permutations; homelab smoke confirms real cluster output.

---

- [ ] **Unit B2: Golden-signals Prometheus queries**

**Goal:** Per-service RPS, latency percentiles, and error rate pulled from Prometheus via templated queries.

**Requirements:** R6

**Dependencies:** Phase A; requires existing `monitoring.PrometheusClient`.

**Files:**
- Create: `backend/internal/servicemesh/metrics.go`
- Modify: `backend/internal/servicemesh/handler.go` (add `HandleGoldenSignals`)
- Create: `backend/internal/servicemesh/metrics_test.go`
- Modify: `backend/internal/server/routes.go` (add `GET /mesh/metrics`)

**Approach:**
- Use the existing `monitoring.QueryTemplate.Render` path for safe label substitution (already validated at the existing template layer — do not bypass it).
- Istio templates (per service):
  - RPS: `sum(rate(istio_requests_total{destination_service_name="{{.svc}}",destination_service_namespace="{{.ns}}"}[2m]))`
  - Error rate: `sum(rate(istio_requests_total{destination_service_name="{{.svc}}",destination_service_namespace="{{.ns}}",response_code=~"5.."}[2m])) / sum(rate(istio_requests_total{destination_service_name="{{.svc}}",destination_service_namespace="{{.ns}}"}[2m]))`
  - p95 latency: `histogram_quantile(0.95, sum by (le)(rate(istio_request_duration_milliseconds_bucket{destination_service_name="{{.svc}}",destination_service_namespace="{{.ns}}"}[2m])))`
- Linkerd templates use `request_total` and `response_latency_ms` with `direction="inbound"` and `dst_service="{{.svc}}"`. Different label names; parallel template set.
- Handler auto-selects template set based on `MeshMembership` for the target workload (determined by sidecar annotation).
- All queries have `PromQLTimeout = 2s`. Never block the UI.

**Patterns to follow:** `backend/internal/monitoring/utilization.go` for the QueryTemplate render path.

**Test scenarios:**
- Happy path (Istio): template renders with correct labels; mocked Prometheus returns single-vector result; golden signals populated.
- Happy path (Linkerd): different template set, same output shape.
- Edge case: Prometheus returns empty vector (no traffic in 2m window) → golden signals all zero, not error.
- Error path: Prometheus timeout → handler returns 200 with `metrics: null, error: "metrics_unavailable"` (non-blocking degraded mode).
- Error path: label-substitution rejects a namespace with `"` in it → 400, template never sent to Prometheus (validated via existing template validator).

**Verification:** unit tests; homelab smoke against real Istio + real Prometheus.

### Phase C — Frontend (PR 3)

- [ ] **Unit C1: Type mirrors + API client**

**Goal:** TypeScript types + typed API client wrappers for all Phase A+B endpoints.

**Requirements:** R1–R6 (consumption)

**Dependencies:** Phases A and B merged.

**Files:**
- Create: `frontend/lib/mesh-types.ts`
- Create: `frontend/lib/mesh-api.ts`

**Approach:** Tagged union pattern — `type TrafficRoute = IstioRoute | LinkerdRoute` with `mesh` discriminator. API functions use existing `apiGet<T>` helper from `frontend/lib/api.ts`.

**Patterns to follow:** `frontend/lib/gitops-types.ts`, `frontend/lib/gitops-api.ts` (if exists) — or `frontend/lib/policy-types.ts`.

**Test scenarios:**
- Test expectation: none — type definitions and thin wrappers; no behavior beyond network plumbing. Covered indirectly by C2–C4 tests.

**Verification:** `deno check` clean; `deno lint` clean.

---

- [ ] **Unit C2: Mesh dashboard island**

**Goal:** Status banner (detected mesh + version + control-plane namespace) with namespace-level injection coverage summary.

**Requirements:** R1

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MeshDashboard.tsx`
- Create: `frontend/routes/networking/mesh/index.tsx`
- Create: `frontend/routes/networking/mesh/dashboard.tsx`
- Modify: `frontend/lib/constants.ts` (add Mesh SubNav tabs under Networking section)

**Approach:** Mirror `PolicyDashboard.tsx`. Empty state prompts install if no mesh detected. Coverage card: "24 of 31 namespaces have sidecar injection enabled" with drill-down.

**Patterns to follow:** `frontend/islands/PolicyDashboard.tsx` for the banner + empty state.

**Test scenarios:**
- Happy path: mesh installed → banner shows mesh + version.
- Empty state: no mesh → empty state with "Install Istio" / "Install Linkerd" external links.
- Both meshes installed → both banners render.
- Theme: renders in all 7 themes without color regressions (visual Playwright smoke).

**Verification:** component renders in all themes; Playwright smoke: `/networking/mesh/dashboard` shows banner.

---

- [ ] **Unit C3: Routing list + detail islands**

**Goal:** Unified list of traffic routing resources (VirtualService, DestinationRule, ServiceProfile, etc.) with detail view.

**Requirements:** R2, R3

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MeshRoutingList.tsx`
- Create: `frontend/islands/MeshRouteDetail.tsx`
- Create: `frontend/routes/networking/mesh/routing.tsx`
- Create: `frontend/routes/networking/mesh/routing/[id].tsx`

**Approach:** Filterable table with mesh + kind badges (e.g., `Istio / VirtualService`, `Linkerd / ServiceProfile`). Detail view shows normalized fields above a collapsed raw-YAML block (Monaco, read-only). URL-encode composite IDs.

**Patterns to follow:** `frontend/islands/GitOpsApplications.tsx` for the filterable list; `frontend/islands/GitOpsAppDetail.tsx` for the detail.

**Test scenarios:**
- Happy path: list shows routes from both meshes with correct badges.
- Filter: filter by mesh → only that mesh's routes shown.
- Filter: filter by namespace → only that namespace's routes shown.
- Empty state: no routes in the selected namespace → shows empty state, not a broken table.
- Detail: composite ID with `%3A` URL-encoding decodes correctly and loads the right resource.
- Detail: 404 composite ID → empty state with back link, not a crash.

**Verification:** Playwright smoke clicks through list → detail → back; visual no-regression in dark themes.

---

- [ ] **Unit C4: mTLS posture page**

**Goal:** Per-namespace mTLS posture cards + drill-down table.

**Requirements:** R4

**Dependencies:** C1.

**Files:**
- Create: `frontend/islands/MTLSPosture.tsx`
- Create: `frontend/routes/networking/mesh/mtls.tsx`

**Approach:** Cards (per namespace): "X of Y workloads have mTLS active" with a colored posture indicator. Drill-down expands to a per-workload table with `mtls: active|inactive|mixed` column + source (`policy`/`metric`/`default`).

**Patterns to follow:** `frontend/islands/ComplianceDashboard.tsx` (GaugeRing + severity bars layout).

**Test scenarios:**
- Happy path: all workloads active → green cards across the board.
- Edge case: mixed namespace (some active, some inactive) → yellow card with drill-down expandable.
- Edge case: unmeshed namespace → separate "Not in mesh" section, not a red card (distinguishes "opted-out" from "broken").
- Theme: accent colors use `var(--success)` / `var(--warning)` / `var(--error)`, no hardcoded Tailwind colors.

**Verification:** Playwright smoke + manual theme check.

### Phase D — Topology overlay + golden signals display (PR 4)

- [ ] **Unit D1: Topology ResourceLister extension + mesh edges**

**Goal:** Add mesh-edge emission to the topology builder behind an `?overlay=mesh` query param.

**Requirements:** R5

**Dependencies:** Phase A (provides normalized route data).

**Files:**
- Modify: `backend/internal/topology/builder.go` (extend `ResourceLister` interface with `ListVirtualServices`, `ListServiceProfiles`)
- Modify: `backend/internal/topology/informer_lister.go` (implement new methods against informer cache)
- Modify: `backend/internal/topology/handler.go` (accept `overlay=mesh`, call new emitters)
- Create: `backend/internal/topology/mesh_edges.go` (emitter: converts normalized routes to topo edges)
- Create: `backend/internal/topology/mesh_edges_test.go`

**Approach:**
- Edge types added: `mesh_vs` (Istio VirtualService route target), `mesh_dest` (DestinationRule subset), `mesh_sp` (Linkerd ServiceProfile route).
- Overlay is opt-in via query param — default topology remains unchanged.
- 2000-edge cap reused from existing topology handler.
- Informer watch added only if mesh installed; otherwise the lister returns empty (no-op).

**Patterns to follow:** existing `topology/builder.go` edge emission; `backend/internal/k8s/informer_manager.go` for adding watch types.

**Test scenarios:**
- Happy path: two services with a VirtualService routing → mesh_vs edges emitted from VS to each destination.
- Edge case: VS with no matching destination services → no edges emitted (not an error).
- Edge case: overlay=mesh requested but mesh not installed → base topology returned, overlay silently no-op, response includes `overlay: "unavailable"`.
- Cap: 2500 edges → truncated at 2000 with `truncated: true` flag.
- Integration: overlay + RBAC — user without access to VS CRD sees base topology only (no partial mesh edges).

**Verification:** unit tests; homelab smoke shows mesh edges on `/observability/topology?overlay=mesh`.

---

- [x] **Unit D2: Frontend topology overlay toggle**

**Goal:** "Show mesh traffic" toggle on `/observability/topology` renders mesh edges with a distinct style.

**Requirements:** R5

**Dependencies:** D1 backend merged, Phase C.

**Files:**
- Modify: `frontend/islands/NamespaceTopology.tsx` (add overlay toggle + edge style for new types)

**Approach:** Toggle sends `?overlay=mesh` to the topology endpoint. Mesh edges render in the theme's accent color, distinct from owner/selector edges. Legend updated.

**Patterns to follow:** existing edge styles in `NamespaceTopology.tsx` (`EDGE_STYLES` map).

**Test scenarios:**
- Happy path: toggle on → mesh edges appear; toggle off → only base edges.
- Edge case: overlay unavailable (no mesh) → toggle is disabled with tooltip "no mesh detected".
- Theme: mesh edge color is themed (no hardcoded hex).

**Verification:** Playwright smoke on homelab with mesh installed.

---

- [ ] **Unit D3: Golden signals on service detail**

**Goal:** Surface RPS, p50/p95/p99 latency, and error rate on the existing Service detail page when the service is meshed.

**Requirements:** R6

**Dependencies:** Phase B.

**Files:**
- Modify: `frontend/components/k8s/ServiceOverview.tsx` (or equivalent service detail panel — locate via current repo structure)
- Create: `frontend/components/mesh/GoldenSignals.tsx` (small embedded card)

**Approach:** Lazy-load golden signals when the Service detail page mounts. Card renders only when mesh membership detected; no empty state for unmeshed services (silently absent).

**Patterns to follow:** existing monitoring cards on `/monitoring` dashboard.

**Test scenarios:**
- Happy path: meshed service → RPS/latency/error card renders with live values.
- Edge case: unmeshed service → card does not render at all.
- Edge case: Prometheus unavailable → card renders with `Metrics unavailable` sub-message, not an error toast.
- Refresh: values re-query every 30s while the page is open (matches other monitoring cards).

**Verification:** Playwright + homelab smoke against a meshed service with live traffic.

---

- [ ] **Unit D4: Helm chart + docs**

**Goal:** Helm chart updates + smoke documentation.

**Requirements:** R9

**Dependencies:** Phases A–D merged.

**Files:**
- Modify: `helm/kubecenter/templates/*` (add RBAC rules for mesh CRD read — impersonation still carries user creds, but service account needs some baseline for discovery)
- Modify: `README.md` / `CLAUDE.md` — mark item #6 complete, append Phase 12 entry to Build Progress

**Approach:** Service account needs `list`/`get` on the mesh CRD groups for discovery (discoverer runs as SA, impersonates only on user-scoped list calls). Keep the grant minimal — explicitly listed resources, not `*`.

**Patterns to follow:** `helm/kubecenter/templates/clusterrole.yaml` — add mesh CRD groups to existing baseline.

**Test scenarios:**
- Test expectation: none — chart change; covered by `make helm-lint`, `make helm-template`, and a homelab reinstall smoke.

**Verification:** `make helm-lint`, `make helm-template` pass; homelab smoke install → mesh features work without permission errors in logs.

## System-Wide Impact

- **Interaction graph:** New handlers added to `routes.go`; existing AccessChecker used; existing PrometheusClient used; topology builder gains optional path. No middleware changes.
- **Error propagation:** Failures in discovery or metrics degrade gracefully to an empty state with an inline message. No mesh-related error should break the base topology page, the Networking section, or the dashboard.
- **State lifecycle risks:** CRD discovery cache (5min) + handler cache (30s). Stale cache windows are bounded and match existing precedents. No write state to manage.
- **API surface parity:** `/mesh/*` endpoints follow the `/gitops/*` response-shape convention precisely — the frontend client can reuse `apiGet<T>` patterns without adaptation.
- **Integration coverage:** (a) Istio + Linkerd co-installed on the same cluster, (b) Istio ambient mode, (c) remote cluster reads (dynamic client only, no informer), (d) RBAC denial on a specific CRD group — each needs an integration test scenario.
- **Unchanged invariants:** `/observability/topology` default response (no `overlay` param) is byte-identical to before. Cert-manager, policy, and gitops endpoints are untouched. No change to the authentication or session flow.

## Risks & Dependencies

| Risk | Mitigation |
|---|---|
| **Mesh CRD API-group drift** (Istio promoted `v1beta1` → `v1` across versions) | Discovery probes `v1` preferred, falls back to `v1beta1` on `ErrGroupDiscoveryNotFound`. Unit test fixtures cover both shapes. |
| **Istio Ambient Mode differs from sidecar model** | v1 detects ambient but does not differentiate features. `MeshInfo` gains an optional `Mode: "sidecar"\|"ambient"` field; frontend displays the mode as a badge but does not branch behavior. |
| **Prometheus golden-signal queries are expensive on large clusters** | All mesh PromQL has a 2s timeout; failures degrade silently. Hard cap: 10 PromQL queries per request (parallel). |
| **Informer watch for mesh CRDs adds memory pressure on large clusters** | Informer watches gated on discovery result — not started unless the mesh is installed. Lazy-start avoids the cost for mesh-less clusters. |
| **Remote-cluster correctness** (no informer, dynamic client only) | E2E test with a registered remote cluster. Any accidental informer usage on remote clusters is a regression flagged by the existing remote-cluster test suite. |
| **Dual-mesh co-installation causes double-counting** (a service owned by both meshes) | Normalize mesh membership to prefer the sidecar annotation's stated mesh; log a warning if both meshes claim a pod. |
| **CLAUDE.md Rule 5 (Sub-agent swarming for >5 files)** | Phase C (5 frontend islands) stays within one PR's scope because the files are small and follow a proven precedent; no sub-agent needed. Phase A (5 backend files) is the threshold — if scope creeps during implementation, split A2 off into its own commit. |

## Documentation / Operational Notes

- **CLAUDE.md roadmap update:** mark item #6 complete on merge of Phase D; append a Phase 12 entry under Build Progress.
- **README architecture diagram:** add the servicemesh package to the architecture diagram alongside certmanager, policy, and gitops.
- **Homelab smoke:** per CLAUDE.md pre-merge rule, each phase must smoke-test on homelab (Istio + Linkerd should be installed there before Phase A merges).
- **Security review:** Phase B introduces new PromQL queries — security review should confirm label substitution uses the existing validated template path and not string concatenation. Note explicitly in the PR description for each phase.
- **Grafana dashboard:** optional follow-up — add a dedicated mesh Grafana dashboard. Out of v1 scope; tracked as sibling.

## Sources & References

- **Origin document:** `plans/service-mesh-observability.md` (this plan — no prior brainstorm document)
- **Closest precedent:** `backend/internal/gitops/` (dual-tool adapter pattern) — `handler.go`, `discovery.go`, `types.go`, `argocd.go`, `flux.go`
- **Supporting precedents:**
  - `backend/internal/policy/` — RBAC filter generic, compliance scoring
  - `backend/internal/certmanager/` — CRD discovery, version extraction, singleflight cache
  - `backend/internal/topology/` — builder + ResourceLister interface extension
- **Frontend references:**
  - `frontend/islands/PolicyDashboard.tsx` — status banner + empty state
  - `frontend/islands/GitOpsApplications.tsx` / `GitOpsAppDetail.tsx` — list + detail with composite IDs
  - `frontend/islands/NamespaceTopology.tsx` — edge renderer, overlay extension point
  - `frontend/islands/ComplianceDashboard.tsx` — posture-card layout for mTLS page
- **External docs:**
  - Istio CRDs: https://istio.io/latest/docs/reference/config/networking/ and https://istio.io/latest/docs/reference/config/security/
  - Istio metrics: https://istio.io/latest/docs/reference/config/metrics/
  - Linkerd CRDs: https://linkerd.io/2-edge/reference/
  - Linkerd proxy metrics: https://linkerd.io/2-edge/reference/proxy-metrics/
  - Linkerd automatic mTLS: https://linkerd.io/2-edge/features/automatic-mtls/
- **Roadmap:** item #6 in `CLAUDE.md` Future Features list
