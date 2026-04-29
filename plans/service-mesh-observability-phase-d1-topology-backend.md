---
title: "Service mesh observability — Phase D1: topology overlay backend"
type: feat
status: complete
date: 2026-04-29
origin: plans/service-mesh-observability.md
---

# Service mesh observability — Phase D1: topology overlay backend

## Overview

Add opt-in mesh-edge emission to the namespace topology graph behind a new `?overlay=mesh` query parameter. When the caller asks for the overlay and a service mesh (Istio or Linkerd) is installed, the existing `/api/v1/topology/{namespace}` response gains additional service-to-service edges that visualize traffic routing declared by `VirtualService` and Linkerd `ServiceProfile` CRDs. When the caller does not pass the parameter, the response is byte-identical to today.

This is a backend-only PR. Frontend toggle (D2), golden-signals card (D3), and Helm RBAC + docs (D4) are separate units in the same Phase D.

## Problem Frame

Phase 7B's topology graph shows owner, selector, mount, ingress, and HPA edges — but nothing about mesh-managed traffic flow. With Phases A–C now in production, the mesh routing data is already discovered and normalized in `internal/servicemesh` (singleflight + 30s cache). The topology view is the natural place to show it as edges, alongside the existing dependency view.

The parent plan (`plans/service-mesh-observability.md`, lines 547–580) defines this work. Phases A–C are merged (PRs #199, #200, #203, #204); D1 is the next backend unit.

## Requirements Trace

- **R5** *(parent plan)* — Mesh edges visualized on the existing topology graph as an opt-in overlay
- **R7** *(parent plan)* — RBAC-aware: per-CRD `CanAccessGroupResource` check filters which mesh edges a user sees
- **R9** *(parent plan)* — `go vet`, `go test ./...` pass; theme tokens only (no frontend in this unit)
- **Default-response invariance** — Without `?overlay=mesh`, the topology response is byte-identical to today (no new fields, no behavioral change)
- **Fail-soft** — Overlay requested but mesh not installed → base topology returned, response carries `overlay: "unavailable"`; never a 5xx

## Scope Boundaries

- No frontend changes (overlay toggle, edge styling, legend) — that is Unit D2
- No golden-signals integration on Service detail — that is Unit D3
- No Helm/RBAC manifest changes — that is Unit D4
- No `DestinationRule` edges — DR semantics describe subsets of a single service, not edges between services. A DR-as-node treatment would require adding new node kinds and is deferred (see Key Technical Decisions)
- No `Gateway` or `ServiceEntry` edges — out of scope for D1; these define ingress/egress relationships, which the existing `EdgeIngress` already covers in spirit
- No new informer watches — the data already lives in `servicemesh.Handler`'s 30s cache
- No support for cluster-scoped mesh resources in the overlay — D1 only emits edges between same-namespace services

### Deferred to Separate Tasks

- Frontend toggle and themed edge styling: Unit D2 (next PR)
- Golden-signals card on Service detail: Unit D3
- Helm chart RBAC additions for mesh CRD discovery: Unit D4

## Context & Research

### Relevant Code and Patterns

- `backend/internal/topology/builder.go` — `Builder.BuildNamespaceGraph` is the existing entry point; pattern for reading from a `ResourceLister`, RBAC-gating each kind via `canAccess`, and appending edges via per-builder helpers (`buildOwnerEdges`, `buildServiceSelectorEdges`, etc.). Mesh-edge emission follows the same shape.
- `backend/internal/topology/types.go` — `EdgeType` constants live here; node-cap constant (`maxNodes = 2000`) lives in `builder.go`.
- `backend/internal/topology/handler.go` — currently does not parse query params. New overlay handling slots in here.
- `backend/internal/servicemesh/handler.go` — `Handler.fetchData(ctx)` returns the cached `cachedMeshData{routes, policies, errors, fetchedAt}` populated cluster-wide via service account; per-user RBAC happens at filter time. D1 reuses this exact pattern via a new exported accessor.
- `backend/internal/servicemesh/types.go` — `TrafficRoute{Mesh, Kind, Name, Namespace, Hosts, Destinations[].Host}` is the contract D1 consumes. `MeshType` discriminator distinguishes Istio vs Linkerd routes.
- `backend/internal/k8s/resources/access.go:134` — `AccessChecker.CanAccessGroupResource(ctx, user, groups, verb, apiGroup, resource, namespace)` is the right RBAC check for CRD groups (`networking.istio.io/virtualservices`, `linkerd.io/serviceprofiles`).
- `backend/internal/server/server.go:66,79` — `Handlers.TopologyHandler` and `Handlers.ServiceMeshHandler` are already siblings in the dependency graph, so wiring the mesh handler into the topology builder is straightforward.

### Institutional Learnings

- No `docs/solutions/` entries match topology or mesh-overlay patterns. The most directly applicable internal precedent is Phase A's "service account fetches cluster-wide data, then filter per-user via `CanAccessGroupResource`" — D1 uses the same shape.

### External References

- Skipped. The codebase has strong local patterns (Phase 7B for topology, Phases A–C for mesh) and the unit is internal to a stable interface boundary.

## Key Technical Decisions

- **Reuse `servicemesh.Handler` data over adding informer watches**: The parent plan suggested extending `ResourceLister` with `ListVirtualServices`/`ListServiceProfiles` methods backed by informers. Phase A actually implemented mesh CRD discovery with a dynamic-client + singleflight + 30s cache pattern, so adding informer watches now would duplicate that pipeline and re-introduce the "remote clusters can't use informers" caveat for an additional set of CRDs. Instead, define a small `MeshRouteProvider` interface in `topology` and have `servicemesh.Handler` satisfy it via a new exported `Routes(ctx)` method. *Why*: avoids duplicate caches, keeps remote-cluster behavior consistent, and makes the topology package's dependency on mesh data explicit and minimal.

- **Service-to-service edges only; no CRD-as-node**: Mesh edges connect existing `Service` nodes (host → destination). `VirtualService` and `ServiceProfile` are not added as graph nodes. *Why*: matches the user mental model of "traffic flow", keeps node kinds unchanged (zero migration risk to existing consumers like `topology/handler.go`'s callers and the Phase 7B frontend), and allows D2's frontend to render mesh edges with a distinct style without inventing new node visuals.

- **Drop `mesh_dest` (DestinationRule) edges from D1**: A DestinationRule declares subsets of a single service, not a relationship between two services. Without subset-as-node, there is no meaningful inter-service edge to emit. The parent plan named `mesh_dest` as an edge type but did not specify endpoints; revisit as a node-annotation enhancement after D2 ships if signal demands it.

- **New `Overlay` field on `Graph`, omitempty**: Default response (no `?overlay=` param) leaves `Overlay` zero-value, which JSON-omits — preserving byte-identical Phase 7B response shape. Values: `""` (not requested), `"mesh"` (applied), `"unavailable"` (requested but no mesh installed).

- **Per-CRD RBAC, fail-soft**: For each mesh CRD group the user lacks `list` on, that CRD's edges are silently dropped (no error, no partial-data flag). If the user lacks RBAC on every mesh CRD, the response still carries `overlay: "mesh"` with zero mesh edges added. *Why*: matches the existing topology builder's per-resource `canAccess` posture (forbidden resources silently skipped), and avoids leaking the existence of a mesh CRD via 403 vs 200 timing.

- **Separate edge cap, distinct from node cap**: Add `maxMeshEdges = 2000` in `topology/builder.go`. Mesh edge emission stops at the cap and sets the existing `Graph.Truncated = true` flag. *Why*: a single `VirtualService` can fan out to dozens of destinations; in a 1000-service mesh, this is the only realistic blow-up vector. Re-using the existing `Truncated` flag avoids inventing a second truncation signal.

- **Emit edges via `Hosts` + `Destinations[].Host` lookup against `nameIndex`**: The existing topology `nameIndex` maps `"Kind/Name"` → UID. Source service of a mesh edge is the VS/SP host; target services are each `Destination.Host`. Hosts that do not resolve to a same-namespace `Service` node are silently skipped. *Why*: cross-namespace and external hosts (`*.example.com`, `redis.cache.svc.cluster.local`) are common in mesh routing but are not in the namespace's graph; dropping them is correct, not a bug.

## Open Questions

### Resolved During Planning

- **Q: Add VS/SP/DR as graph nodes or use service-to-service edges?** Resolved: service-to-service. See Key Technical Decisions.
- **Q: How does this consume mesh data without duplicating informers?** Resolved: new `Routes(ctx)` accessor on `servicemesh.Handler` + `MeshRouteProvider` interface in `topology`. See Key Technical Decisions.
- **Q: Default response invariance?** Resolved: `Overlay` field is `omitempty`, no other shape change.

### Deferred to Implementation

- **Exact host-string normalization for `RouteDestination.Host` lookup.** Mesh routes carry `host: my-svc`, `host: my-svc.namespace`, `host: my-svc.namespace.svc.cluster.local`. The lookup needs to resolve all three to the same `Service` node when present in the namespace. The implementer will write a small `resolveHost(host, namespace, nameIndex)` helper and table-test it; the exact regex/split shape is not worth pre-specifying. The cluster-domain default (`cluster.local` vs custom) is acknowledged as a known limitation per the existing memory note (Phase B mTLS scope boundaries) — D1 inherits the same constraint.
- **Whether to surface a per-CRD-group `errors` map on the response.** Today's `Graph` shape has no error map. Decision deferred until D2 starts and a concrete UX need emerges; the silent fail-soft posture is sufficient for D1.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```
                  GET /api/v1/topology/{namespace}?overlay=mesh
                                    │
                                    ▼
                        topology.Handler.HandleNamespaceGraph
                          │ parses overlay param
                          ▼
                        topology.Builder.BuildNamespaceGraph(ctx, ns, user, checker, opts)
                          │ existing path: pods/services/.../HPAs → nodes + edges
                          │ new path (when opts.Overlay == "mesh"):
                          │   1. meshProvider.Routes(ctx)            ← servicemesh.Handler.Routes
                          │   2. filter to namespace
                          │   3. for each route's CRD group, CanAccessGroupResource
                          │   4. buildMeshEdges(filteredRoutes, namespace, nameIndex)
                          │   5. append to graph.Edges, respect maxMeshEdges
                          ▼
                        Graph{Nodes, Edges, Truncated, ComputedAt, Overlay}

  Edge endpoints:  source = nameIndex["Service/" + host(VS or SP)]
                   target = nameIndex["Service/" + dest.Host]
  Edge type:       EdgeMeshVS   (Istio VirtualService route → destination)
                   EdgeMeshSP   (Linkerd ServiceProfile route → destination)
```

The shape mirrors how `buildIngressEdges` already walks `Ingress.Spec.Rules[].HTTP.Paths[].Backend.Service.Name` and looks the target up in `nameIndex`; mesh-edge emission is analogous, just with mesh-specific host-resolution rules.

## Implementation Units

- [x] **Unit D1.1: Define `MeshRouteProvider` and expose routes from servicemesh handler**

**Goal:** Add a small interface in `topology` for fetching cached mesh routes, and an exported accessor on `servicemesh.Handler` that satisfies it. Establishes the package boundary without coupling `topology` to `servicemesh` internals.

**Requirements:** R5 (foundation)

**Dependencies:** None — `servicemesh.Handler.fetchData` already exists.

**Files:**
- Modify: `backend/internal/servicemesh/handler.go` (add exported `Routes(ctx context.Context) ([]TrafficRoute, error)`)
- Modify: `backend/internal/topology/builder.go` (declare `MeshRouteProvider` interface + nil-safe `meshProvider` field on `Builder`)
- Test: `backend/internal/servicemesh/handler_test.go` (extend existing test file with one case for `Routes`)

**Approach:**
- `servicemesh.Handler.Routes` is a thin wrapper over `fetchData` that returns the routes slice (cluster-wide, NOT RBAC-filtered — RBAC is the topology builder's responsibility for the overlay scope, mirroring how `fetchData` is unfiltered for `HandleListRoutes` callers).
- `topology.MeshRouteProvider` interface signature: `Routes(ctx context.Context) ([]servicemesh.TrafficRoute, error)`. Yes, this imports `servicemesh` in `topology` — that import already feels right because the mesh data shape is the contract.
- `Builder` gains an optional `meshProvider MeshRouteProvider` field. `NewBuilder` keeps its existing signature; add `NewBuilderWithMesh(lister, meshProvider, logger)` or accept a functional option, whichever is more idiomatic in the codebase. (Codebase preference: looking at `Builder` today there are no options; a second constructor is cleaner than adding a setter.)

**Patterns to follow:**
- `backend/internal/servicemesh/handler.go`'s `cachedMeshData` shape and `fetchData` signature — mirror its return semantics exactly.
- `backend/internal/topology/builder.go` `ResourceLister` interface as the precedent for "small, behavior-focused interfaces in `topology`".

**Test scenarios:**
- *Happy path*: `Routes(ctx)` returns the same slice that `HandleListRoutes` would expose pre-RBAC-filter; assert via direct call after seeding `cache` in test.
- *Edge case*: cache empty → returns empty slice + nil error (no fetch attempted).
- *Edge case*: cache stale → fetch fires, populates, returns. Verify singleflight coalesces concurrent callers (one fetch when 5 callers race).
- *Error path*: dynamic client unavailable → returns empty slice + nil error (matches existing `doFetch` behavior for that case).

**Verification:**
- `go test ./internal/servicemesh/...` passes.
- New `Routes` method is exported and documented; godoc clearly states "cluster-wide, not RBAC-filtered — caller must filter".

---

- [x] **Unit D1.2: Mesh edge emitter (`mesh_edges.go` + tests)**

**Goal:** Pure function that converts a slice of `servicemesh.TrafficRoute` plus the namespace + nameIndex into a slice of `topology.Edge`, capped and host-resolved. No I/O, no RBAC — those are wired in D1.3.

**Requirements:** R5

**Dependencies:** D1.1 (provides the type), but the emitter itself is a pure function so it can be implemented and tested independently.

**Files:**
- Modify: `backend/internal/topology/types.go` (add `EdgeMeshVS`, `EdgeMeshSP` constants; add `Overlay string \`json:"overlay,omitempty"\`` field to `Graph`)
- Create: `backend/internal/topology/mesh_edges.go`
- Create: `backend/internal/topology/mesh_edges_test.go`

**Approach:**
- Function signature (directional): `buildMeshEdges(routes []servicemesh.TrafficRoute, namespace string, nameIndex map[string]string, maxEdges int) (edges []Edge, truncated bool)`.
- Iterates routes; skips routes outside `namespace`; resolves `route.Hosts[0]` to a source service UID via `nameIndex`; for each `Destination`, resolves `dest.Host` to a target service UID; emits an edge typed `EdgeMeshVS` (when `route.Mesh == MeshIstio`) or `EdgeMeshSP` (when `route.Mesh == MeshLinkerd`).
- Host resolver helper: `resolveHost(host, namespace string, nameIndex map[string]string) (uid string, ok bool)` strips `.namespace.svc.cluster.local` and `.namespace` suffixes before lookup. External hosts (no service match) → `ok=false`, edge skipped.
- Edge dedup: same `(source, target, type)` triple emitted by multiple routes is collapsed to one edge — match the dedup pattern in `buildIngressEdges`'s `seen` map.
- Cap: stop at `maxEdges`, return `truncated=true`.

**Patterns to follow:**
- `buildIngressEdges` in `backend/internal/topology/builder.go` for the `seen` dedup map and `nameIndex` lookup pattern.
- Edge type constant style in `backend/internal/topology/types.go`.

**Test scenarios:**
- *Happy path — Istio VS, single destination*: VS in namespace `foo` with `hosts: [a]` and one route to `b` → one `mesh_vs` edge from `Service/a` UID to `Service/b` UID.
- *Happy path — Linkerd SP*: ServiceProfile in namespace `foo` with `hosts: [a]` and one route to `b` → one `mesh_sp` edge.
- *Happy path — multiple destinations*: VS routes to `b`, `c`, `d` → three edges, all `mesh_vs`, sharing source.
- *Happy path — host suffix variants*: route destination `host: b`, `host: b.foo`, `host: b.foo.svc.cluster.local` all resolve to the same `Service/b` UID and dedup to one edge.
- *Edge case — no matching destination service*: VS routes to `external.example.com` → no edges emitted, no error.
- *Edge case — VS in different namespace*: route in namespace `bar` while building `foo` → not emitted.
- *Edge case — empty routes*: nil slice → empty edges, `truncated=false`.
- *Edge case — routes without hosts*: `Hosts` empty → emitter falls back to `route.Name`'s namespace lookup if applicable, otherwise skips. (Implementer to choose; document what's chosen in code.)
- *Cap*: 2500 edge candidates → returns 2000 edges, `truncated=true`.
- *Dedup*: two VS objects routing the same source→target → one edge, not two.
- *Mesh discriminator*: Istio route → `EdgeMeshVS`; Linkerd route → `EdgeMeshSP`; unknown mesh → no edge (defensive).

**Verification:**
- `go test ./internal/topology/...` passes.
- `mesh_edges.go` is pure (no logger, no context, no I/O). Tests run in microseconds.

---

- [x] **Unit D1.3: Builder + handler overlay path**

**Goal:** Wire the overlay flow end-to-end through `Builder.BuildNamespaceGraph` and `Handler.HandleNamespaceGraph`. RBAC checks gate which mesh CRD groups contribute edges; default response is byte-identical to today.

**Requirements:** R5, R7, default-response invariance, fail-soft

**Dependencies:** D1.1 (provider interface), D1.2 (emitter).

**Files:**
- Modify: `backend/internal/topology/builder.go` (extend `BuildNamespaceGraph` to accept a `BuildOptions{Overlay string}`, or add a parallel `BuildNamespaceGraphWithOptions` to avoid breaking other callers; route to mesh-edge emission when `Overlay == "mesh"`)
- Modify: `backend/internal/topology/handler.go` (parse `?overlay=` query param, pass through; populate `Graph.Overlay`)
- Modify: `backend/internal/server/server.go` (pass `ServiceMeshHandler` into `topology.Handler`'s builder construction)
- Test: `backend/internal/topology/builder_test.go` (add overlay-path cases; existing tests must still pass)
- Test: `backend/internal/topology/handler_test.go` (create if absent — looks like there's no handler test today; if so, defer to existing test conventions)

**Approach:**
- Handler reads `r.URL.Query().Get("overlay")`. Empty → call existing path; `"mesh"` → call new path with `Overlay: "mesh"`. Any other value → 400 (`"unsupported overlay"`). The 400 makes typos visible; matching the existing `resolveMeshParam` pattern for invalid input.
- Builder, when `opts.Overlay == "mesh"`:
  1. If `b.meshProvider == nil` → set `graph.Overlay = "unavailable"` and return base graph.
  2. Call `b.meshProvider.Routes(ctx)`. On error, log + degrade to `graph.Overlay = "unavailable"` (do not 5xx).
  3. Filter routes to `namespace`.
  4. For each unique CRD group present in the filtered routes (`networking.istio.io/virtualservices`, `linkerd.io/serviceprofiles`), call `checker.CanAccessGroupResource(ctx, user, ..., "list", apiGroup, resource, namespace)`. Drop routes whose CRD the user can't list.
  5. Call `buildMeshEdges(filteredRoutes, namespace, nameIndex, maxMeshEdges)`. Append edges. If emitter returned `truncated=true`, set `graph.Truncated = true`.
  6. Set `graph.Overlay = "mesh"`.
- Detection of "mesh installed" is implicit via `Routes(ctx)` returning a non-empty slice (or via a separate `Detected()` accessor — implementer's call). If routes are empty and the call succeeded → `Overlay = "mesh"` with zero mesh edges (not "unavailable"). "Unavailable" is reserved for "provider unwired or errored".
- `maxMeshEdges = 2000` defined in `builder.go` near `maxNodes`.

**Patterns to follow:**
- `backend/internal/topology/builder.go`'s `canAccess` helper as the RBAC-check pattern (though this unit needs `CanAccessGroupResource`, not `CanAccess`).
- `backend/internal/servicemesh/handler.go`'s `filterByRBAC[T]` for the per-CRD-group access cache pattern (one SSAR per CRD-group/namespace pair, not per route).

**Test scenarios:**
- *Default path unchanged (regression)*: no `?overlay=` query → response shape and content byte-identical to pre-D1 (no `Overlay` field present, no mesh edges, no behavior change). Assert via response-fixture diff against a known baseline.
- *Happy path — Istio installed, user has VS list permission*: `?overlay=mesh` → response includes `mesh_vs` edges; `Overlay == "mesh"`.
- *Happy path — both meshes installed*: `?overlay=mesh` → response includes both `mesh_vs` and `mesh_sp` edges.
- *Edge case — overlay requested, no mesh installed*: provider returns empty slice → `Overlay == "mesh"`, no mesh edges added, base graph intact.
- *Edge case — overlay requested, provider unwired (nil)*: → `Overlay == "unavailable"`, base graph intact.
- *Edge case — overlay requested, provider errors*: → `Overlay == "unavailable"`, error logged, no 5xx.
- *Edge case — invalid overlay value*: `?overlay=garbage` → 400 with user-safe message.
- *RBAC — user lacks VS access*: user can't list `networking.istio.io/virtualservices` in the namespace → no `mesh_vs` edges, but `mesh_sp` edges still emitted if user has SP access. `Overlay == "mesh"`.
- *RBAC — user lacks every mesh CRD*: no mesh edges, `Overlay == "mesh"`. Equivalent to "no mesh edges to show".
- *RBAC — checker errors*: `CanAccessGroupResource` returns error → that CRD's edges are dropped (fail-closed); other CRDs proceed. Logged.
- *Cap*: provider returns 2500 routes that would each emit one edge → response has exactly 2000 mesh edges, `Truncated == true`.
- *Cross-cluster invariant*: any test that builds a graph for a non-local cluster context (if test infra supports it) must not reach the mesh provider — D1 leaves that invariant intact because the existing builder is local-cluster-only and remote topology is currently routed elsewhere. *(Verify with the current routing layer; if remote topology already 403s, no extra check needed.)*

**Verification:**
- `cd backend && go vet ./... && go test ./...` passes (run repo-wide per CLAUDE.md, not scoped).
- A baseline-fixture diff confirms the no-overlay response shape is unchanged.
- Manual smoke against homelab once Istio is installed: `curl /api/v1/topology/<ns>?overlay=mesh` returns mesh edges; `curl /api/v1/topology/<ns>` returns the original shape.

---

- [x] **Unit D1.4: Wire `ServiceMeshHandler` into topology builder construction in server bootstrap**

**Goal:** In `server.go`, pass `ServiceMeshHandler` (which now satisfies `MeshRouteProvider`) into the topology builder so D1.3's overlay path actually has data in production.

**Requirements:** R5 (final wiring)

**Dependencies:** D1.1, D1.3.

**Files:**
- Modify: `backend/internal/server/server.go` (the dependency wiring site for `TopologyHandler`)

**Approach:**
- Wherever `topology.NewBuilder` is currently invoked, switch to `topology.NewBuilderWithMesh(lister, deps.ServiceMeshHandler, logger)` (or whichever construction shape D1.1 settled on). `ServiceMeshHandler` is already constructed before `TopologyHandler` in the existing init order — confirm during implementation; if not, swap order. No new fields on the `Handlers` struct.

**Patterns to follow:**
- Existing wiring pattern in `backend/internal/server/server.go` for sibling handlers.

**Test scenarios:**
- Test expectation: none — pure wiring change. Behavior is covered by D1.3's tests (which rely on `Builder` being constructed with a non-nil provider) and by the homelab smoke.

**Verification:**
- `cd backend && go vet ./...` clean.
- Server starts; `/api/v1/topology/<ns>?overlay=mesh` returns mesh edges on a cluster with a mesh installed.

## System-Wide Impact

- **Interaction graph:** New dependency edge from `topology.Builder` to `servicemesh.Handler` (via `MeshRouteProvider` interface). No middleware changes. No new routes — the existing `/api/v1/topology/{namespace}` route gains a query parameter. No WebSocket changes.
- **Error propagation:** Mesh-overlay failures are absorbed at the builder layer (set `Overlay = "unavailable"`, log, continue). Base topology never fails because of a mesh failure. CRD RBAC errors are fail-closed for that CRD only.
- **State lifecycle risks:** None new. Mesh data lives in `servicemesh.Handler`'s 30s cache; topology becomes a read-only consumer. No write paths.
- **API surface parity:** `Graph.Overlay` is the single new field, `omitempty`. Frontend Phase 7B's existing topology page and the diagnostics blast-radius view (which also consumes `Graph`) see no behavioral difference unless they pass the new query param.
- **Integration coverage:**
  (a) Mesh installed + RBAC granted → mesh edges in response.
  (b) Mesh installed + partial RBAC (VS yes, SP no) → only VS edges.
  (c) Mesh not installed + overlay requested → `Overlay = "mesh"`, zero mesh edges.
  (d) Provider unwired or errored → `Overlay = "unavailable"`, base graph intact.
  (e) Default request (no `?overlay`) → response byte-identical to pre-D1.
- **Unchanged invariants:**
  - `/api/v1/topology/<ns>` without `?overlay` is byte-identical to today.
  - `topology.Graph` field set is unchanged except for the new `Overlay omitempty` field.
  - `internal/servicemesh` package's existing handler behavior is unchanged. The new exported `Routes(ctx)` method is additive.
  - Remote-cluster topology (if any path reaches it) is untouched. D1 inherits whatever the current local-cluster-only assumption is.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Default response shape drift breaks Phase 7B frontend or diagnostics consumers | `Overlay omitempty`, response-fixture diff test, manual `curl` against pre-/post-PR baselines during smoke. |
| Mesh route fetch latency adds to topology endpoint p95 | Reuses the existing 30s cache; the only synchronous cost in the typical case is a map lookup. Provider error path is fail-soft, never blocks the base graph. |
| Cardinality blow-up on a 1000-service mesh | `maxMeshEdges = 2000`, dedup by `(source, target, type)`, and `Truncated` flag. |
| User can list services but not VirtualService → confusing partial graph | Documented behavior: missing CRD list permission silently drops that CRD's edges. Phase D2 frontend will surface "mesh edges are filtered by your CRD permissions" via help text on the toggle. |
| Mesh data exposed to a non-admin user via topology that they wouldn't see in `/api/v1/mesh/routing` | The same `CanAccessGroupResource` check used by `HandleListRoutes`'s `filterByRBAC` runs here. No new RBAC bypass path. |
| Custom cluster domain (`cluster.svc.example.org`) breaks host resolution | Inherits the known limitation already accepted in Phase B (per memory: `project_servicemesh_scope_boundaries.md`). Document in code comment. Not a regression. |
| Remote cluster passes through this path and dereferences a nil mesh provider | Builder's nil-check sets `Overlay = "unavailable"` and returns base graph. Defensive, not a 5xx. |

## Documentation / Operational Notes

- No README/docs changes in this unit. D4 covers Helm + docs + roadmap-tick.
- After merge, append a one-line bullet to the parent plan's "Phase D" status note (`plans/service-mesh-observability.md`). Do not mark roadmap item #6 complete until D1–D4 are all merged.

## Sources & References

- **Origin document:** [plans/service-mesh-observability.md](service-mesh-observability.md), Unit D1 (lines 547–580)
- **Phase A precedent:** [plans/service-mesh-observability.md](service-mesh-observability.md), Phase A (singleflight + 30s cache, RBAC filter pattern)
- **Topology builder:** `backend/internal/topology/builder.go` (existing edge emission patterns)
- **Mesh data shape:** `backend/internal/servicemesh/types.go` (`TrafficRoute`)
- **RBAC check:** `backend/internal/k8s/resources/access.go:134` (`CanAccessGroupResource`)
- **Recent service mesh PRs:** #199 (Phase A), #200 (Phase B), #203 (Phase B follow-ups), #204 (Phase C frontend)
