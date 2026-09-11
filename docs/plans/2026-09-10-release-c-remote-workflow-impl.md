---
title: "Release C — Remote-Cluster Workflow Parity — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: C
units: U7–U12
migration_sequence: none
date: 2026-09-10
---

# Release C — Remote-Cluster Workflow Parity — Implementation Plan

Scope: requirements R1–R3, R8, R9, R10; KTD2, KTD3, KTD5; acceptance examples AE2 and AE3.
Source revision read for this plan: `main` @ `b9eb8171`.

**Delivery rule (user decision, not re-litigated):** one PR per unit, one feature branch per
unit, at most five files touched per PR including tests. Where the master plan's unit does not
fit that cap — or where splitting materially reduces blast radius — the unit is split and given
a new id below.

**Migration rule (user decision):** Release C adds **no** database migration. Nothing in U7–U12
requires a schema change; where the master plan implies one (KTD3's "cluster registry
generation"), the resolution is derived from existing columns and recorded in Design Decisions.
Any future need for a column is raised as a risk, never as an invented sequence number.

---

## Codebase Findings

Every row below was read directly. Line numbers are from the reviewed revision and are quoted
as edit anchors, not as guarantees they will not drift.

| File (LOC) | What it actually does | What it constrains for Release C |
|---|---|---|
| `backend/internal/k8s/cluster_router.go` (601) | `ClientForCluster`/`DynamicClientForCluster`/`RouterFor` resolve local vs remote; `buildRemoteConfig` (376) does registry `Get` → `ValidateRemoteURL` → `store.Decrypt` of `AuthData` and `CAData` → `rest.Config` with `Impersonate`, `Dial: StrictDialContext` → `applyClusterTLS` fail-closed. `remoteConfig` (340) wraps the build in `singleflight` keyed `clusterID\x00cacheKey(username,groups)` under `context.WithoutCancel` + 30s cap. `EvictCluster` (187) prefix-deletes `remoteCache`/`remoteDynCache` and fans out to `RegisterEvictHook` callbacks. `StartCacheSweeper` (232) is a raw `go func()` ticker at `cacheSwapInterval` (60s). | Foundation for U7. Every remote credential path already exists and must be reused verbatim — U7 adds a *discovery* consumer of `remoteConfig`, it does not add a second config builder. The eviction fan-out and the sweeper ticker are the two hooks the new cache must register with. |
| `backend/internal/k8s/client.go` (293) | `ClientFactory`. `ClientForUser` returns `kubernetes.Interface` (the seam CLAUDE.md names). **`RESTMapper()` (209) builds `restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(f.baseClientset.Discovery()))` — service-account discovery, `sync.Once`, never impersonated, never per-cluster.** `DiscoveryClient()` (218) returns `f.baseClientset.Discovery()` — also SA. `cacheKey` (285) = sha256 of `username\x00sorted(groups)`. | The *local* mapper/discovery are SA-scoped and shared across all identities. That is pre-existing and U7 must not change it (doing so would change RBAC semantics for every local handler). The remote path has no SA client at all — every remote `rest.Config` impersonates — so remote discovery is unavoidably per-identity. This asymmetry is the whole of the identity-isolation design in U7. |
| `backend/internal/k8s/cluster_prober.go` (240) | `ClusterProber.Run` (84) uses `recoverutil.Tick`, probes every 60s. `ProbeOne` (117) re-validates SSRF, decrypts, builds a **non-impersonating** `rest.Config` with a 10s timeout, calls `ServerVersion()`, then `ProbeImpersonateRights` (26), then counts nodes, then `clusterStore.UpdateStatus(..., "connected"\|"disconnected"\|"blocked"\|"error", ...)`. `sanitizeProbeError` (224) collapses errors to safe categories. | U8's "reachable" dimension must **read** `ClusterRecord.Status` + `LastProbedAt`, not issue its own probe. Reusing the prober avoids a per-request remote round trip, a second TLS policy, and a second error-sanitiser. Note `ProbeOne`'s config omits `Dial: StrictDialContext` (the router sets it) — recorded under Risks. |
| `backend/internal/server/middleware/cluster.go` (87) | `ClusterContext` (16) reads `X-Cluster-ID`, treats `""`/`"local"` as local, caps length at 64, and requires `auth.IsAdmin` for anything else. `ClusterIDFromContext` (75) defaults to `"local"`. `WSClusterContext` (56) deliberately skips the admin gate. | U8's "header/path target must agree" check sits **above** this: by the time a handler runs, a non-local header has already been admin-gated. Requiring `path == header` therefore inherits the admin gate for free and needs no new middleware — this is why the capabilities route does **not** need to live inside the admin-only `/clusters` group. |
| `backend/internal/yaml/handler.go` (380) | Four handlers. Each calls `middleware.ClusterIDFromContext` then `ClusterRouter.RouterFor`, then rejects non-local. `resolveGVR` (350) walks `clientFactory.DiscoveryClient().ServerGroupsAndResources()`. `readYAMLBody` (327) enforces `MaxBodySize` + `CheckSecurity`. | **Crux of U9.** See the exact inventory below. |
| `backend/internal/yaml/applier.go` (203) | `ApplyDocuments(ctx, dynClient dynamic.Interface, mapper meta.RESTMapper, docs, force, logger)` (49); `applyOne` (81) retries `mapper.RESTMapping` 3× with 500ms/1s backoff for newly-registered CRDs; SSA `PATCH` with `FieldManager = "kubecenter"`. | **Already fully parameterised on `mapper` and `dynClient`.** It needs **no change** for remote. The master plan lists it as a U9 touch file; that is wrong (see corrections). The 3× retry already satisfies the plan's "refresh target discovery for a CRD newly introduced within a bundle" — provided the injected mapper self-invalidates, which `NewDeferredDiscoveryRESTMapper` does. |
| `backend/internal/yaml/differ.go` (155) | `DiffDocuments(ctx, dynClient, mapper, docs, logger)` (file-local 39) → `diffOne` does `Get` + dry-run SSA `PATCH`, then `CleanForExport` on both sides. | Also fully parameterised. **No change needed.** Master plan lists it as a U9 touch file; wrong. |
| `backend/internal/yaml/export.go` (56) | `CleanForExport` (strips uid/resourceVersion/generation/timestamps/selfLink/managedFields/ownerReferences/status + 2 noisy annotations) and `ExportToYAML`. No cluster coupling of any kind. | **No change needed.** Master plan lists it as a U9 touch file; wrong. |
| `backend/internal/yaml/security.go` (79), `parser.go` (87) | `CheckSecurity`, `MaxBodySize`, `ParseMultiDoc`. Cluster-agnostic. | Unchanged by Release C. `parser_fuzz_test.go` already exists; no new fuzz target is required (no new parse seam is introduced). |
| `backend/internal/k8s/resources/dashboard.go` (832) | `HandleDashboardSummary` (536). Local-only guard at 542–547. Acquisition, aggregation, Prometheus and health are interleaved in one function. | Crux of U10. Exact seam below. |
| `backend/internal/k8s/resources/health.go` (521) | **`computeClusterHealth(in HealthInputs) ClusterHealth` (220) is already a pure function** — no I/O, no clock, no informers. Weighted sub-scores at 434–449 **renormalise over whichever signals resolved `ok`** (443–457); only when `totalWeight == 0` does it return `Status: unknown, Score: nil` (459–465). | Directly AE3-relevant: renormalisation means "nodes succeeded, everything else missing" yields a confident numeric score computed from nodes alone. That is exactly the manufactured score AE3 forbids. The remote path must not call this function. |
| `backend/internal/k8s/resources/access.go` | `CanAccess` caches on `accessCacheKey{clusterID, username, sortedGroups, resource, namespace, verb}` (197). `clientForCluster` (258) routes non-local SARs through `ClusterRouter.ClientForCluster`. | SAR results are already per-identity **and** per-cluster keyed — U8 can reuse `AccessChecker` for the `authorized` dimension with no new cache and no cross-identity risk. |
| `backend/internal/k8s/resources/counts.go` | `HandleResourceCounts` returns **400** for non-local (27–31) because counts read the local informer cache. | U11c must render this as a capability-driven "unavailable on remote", not as an error toast. |
| `backend/internal/server/routes.go` (902) | Authenticated group at 108–113 applies `Auth` → `CSRF` → `ClusterContext`. `/clusters` group at 256–263 is wrapped in `middleware.RequireAdmin`; it has `GET /`, `POST /`, `GET /{clusterID}`, `DELETE /{clusterID}`, `POST /{clusterID}/test`. `registerYAMLRoutes` (284) mounts `/yaml/{validate,apply,diff}` + `/yaml/export/{kind}/{namespace}/{name}` behind `YAMLRateLimiter`. | U8 route placement (see Design Decisions — the `/clusters` group is admin-only, which is wrong for local capability disclosure). |
| `backend/internal/server/server.go` (391) | `Server` carries `K8sClient`, `ClusterRouter`, `ClusterStore`, `ClusterProber`, `ResourceHandler`, `YAMLHandler`. `New(deps)` (140) constructs `YAMLHandler` at 216–222 with `K8sClient`, `ClusterRouter`, `AuditLogger`, `Logger`, `ClusterID`. `AccessChecker` is reachable as `s.ResourceHandler.AccessChecker`. | U8 needs **no** `server.go` edit — every dependency it wants is already a `Server` field. Master plan lists `server.go` in U8; unnecessary. |
| `backend/cmd/kubecenter/main.go` (965) | `NewClusterRouter` + `StartCacheSweeper` at 431–432; `accessChecker.SetClusterRouter` 437; `clusterProber.Run` 453; the single `RegisterEvictHook` call at 777 (cert-manager). | U7's discovery cache lives *inside* `ClusterRouter`, so it needs **no** `main.go` edit and no new hook registration. Release C touches `main.go` zero times. |
| `backend/internal/store/clusters.go` | `ClusterRecord` has `CreatedAt`, `UpdatedAt`, `LastProbedAt` and **no generation column**. `UpdateStatus` (152) bumps `updated_at = NOW()` on **every 60s probe**. `UpdateCredentials` (128) is **dead code — zero callers**. `Delete` (164) refuses `is_local = true`. | KTD3's "cluster registry generation" has no column and cannot be `updated_at` (it churns every minute). Resolution in Design Decisions. |
| `backend/internal/server/handle_clusters.go` | `generateClusterID` (~355) = 128-bit `crypto/rand` hex. Create at 72; Delete at 286 calls `s.ClusterRouter.EvictCluster(id)` at 305. **There is no update/PATCH endpoint** — credentials can only change by delete + re-create, which always yields a new random id. | Cluster-id reuse across registrations is cryptographically implausible, so the generation token is defence-in-depth rather than the primary isolation mechanism. |
| `scripts/check-cluster-routing.sh` (164) | Greps `HANDLER_DIRS` for `.ClientForUser(` / `.DynamicClientForUser(`; exempts `ALLOWED_PREFIXES` (`cluster_router.go`, `client.go`, `informers*`, `cluster_prober.go`) and lines preceded by `// nolint:cluster-routing <reason>`. Gate defaults to `warn`. | It **does not** catch `.RESTMapper()` or `.DiscoveryClient()` — the exact calls U9 removes. Extending it is how the no-fallback invariant becomes structural rather than conventional. `.github/workflows/ci.yml:63` runs it with `CHECK_CLUSTER_ROUTING_GATE: warn`. |
| `docs/solutions/backend-resilience-conventions.md` | Part 1 mandates `recoverutil.Go` / `Tick` / `Safe` for any goroutine outside chi's recovery middleware; `wg.Done()` and counted channel sends stay **outside** the wrapped closure. | Affects U7 and U10 — see "recoverutil impact" per unit. |
| `frontend/lib/api.ts` (302) | `api()` (133) defines `doFetch` which sets `X-Cluster-ID` from `selectedCluster.value` **inside the closure** (142). On 401 it refreshes and calls `doFetch()` **again** (172) — re-reading the signal. `apiPostRaw` (231) accepts no `signal`. | U11a's race, precisely located: a cluster switch that lands during a token refresh silently retargets the retried request. Independent of that, a slow response carries no cluster stamp, so a late reply from cluster A can be applied to a UI now showing cluster B. |
| `frontend/lib/cluster.ts` (20) | `selectedCluster` signal, seeded from `localStorage`, persisted by an `effect`. Nothing else. | No generation, no epoch, no switch function, no subscriber protocol. |
| `frontend/lib/yaml-apply.ts` (123) | `useYamlApply` owns validate/apply. **It never reads `selectedCluster`.** `handleValidate` (76) and `handleApply` (94) issue independent `apiPostRaw` calls with no cancellation and no target binding. Second consumer: `frontend/islands/SecretStoreFromTemplateEditor.tsx:39`. | This — not `YamlApplyPage.tsx` — is where AE2 lives. |
| `frontend/islands/YamlApplyPage.tsx` (361) | Pure presentation over `useYamlApply`; the only state it owns is `forceConflicts`. | Confirms the correction above. |
| `frontend/islands/DashboardV2.tsx` (1084) | `DashboardSummary` is an **island-local interface** (35–54) with `health?: ClusterHealth`. `fetchSummary` (102) / `fetchTrends` (113); mount effect (134) has an **empty dependency array** (189) and a 60s interval; `tabAbort` (100) guards only the range-tab race. Health rendering at 257–263 does `health?.score ?? 0` and `health?.status ?? "unknown"`. | The island never reacts to a cluster change at all, and `?? 0` would render a **0 score** for a null health — the exact "manufactured" reading AE3 forbids, inverted. U11c must fix both. |
| `frontend/islands/TopBarV2.tsx` | Line 98 renders `{selectedCluster.value}` as text. | See the missing-switcher correction. |
| `frontend/lib/resource-counts.ts` | Module-level `effect` (82–91) refetches counts whenever `selectedNamespace` or `selectedCluster` changes, with debounce + `AbortController`. | The only existing cluster-reactive client code; it is the pattern U11a should generalise, and it will start hitting the counts 400 the moment a switcher exists. |
| `e2e/playwright.config.ts` | `testDir: ./tests`; projects `setup` → `chromium` (ignores `api-routes.spec.ts`) → `route-contract`. `webServer` starts the backend with `KUBECENTER_DEV=true` against the ambient kubeconfig. | A new `tests/*.spec.ts` is picked up by the `chromium` project with **no config change**, which is what lets U12 land the spec while the live run stays gated. |
| `e2e/kind-config.yaml` | Four lines: one control-plane node, `kind.x-k8s.io/v1alpha4`. | The two-cluster fixture is a second file of the same shape plus a distinct CRD manifest; nothing structural to invent. |
| `e2e/tests/yaml-apply.spec.ts` (40) | Blocks the Monaco CDN so the `<textarea>` fallback renders, fills it, clicks Validate. | The interaction idiom U12's spec must copy. |

### (a) Complete inventory: local-mapper / local-client usage in the `yaml` package

Grep used: `grep -rn "h.ClusterID\|K8sClient" backend/internal/yaml/*.go | grep -v _test`.

| # | Site | What it does | U9 action |
|---|---|---|---|
| 1 | `handler.go:25` | `K8sClient *k8s.ClientFactory` field on `Handler` | Removed in U9b's Step 0, once the last reader is gone |
| 2 | `handler.go:69` | `mapper := h.K8sClient.RESTMapper()` in `HandleValidate` | Replace with target mapper (U9a) |
| 3 | `handler.go:154` | `mapper := h.K8sClient.RESTMapper()` in `HandleApply` | Replace with target mapper (U9b) |
| 4 | `handler.go:227` | `mapper := h.K8sClient.RESTMapper()` in `HandleDiff` | Replace with target mapper (U9a) |
| 5 | `handler.go:290` | `gvr, err := resolveGVR(h.K8sClient, kind)` in `HandleExport` | Re-signature to take `discovery.DiscoveryInterface` (U9a) |
| 6 | `handler.go:350–361` | `resolveGVR(clientFactory *k8s.ClientFactory, kind string)` → `clientFactory.DiscoveryClient().ServerGroupsAndResources()` | Re-signature (U9a) |

**There are no others.** `applier.go`, `differ.go`, `export.go`, `parser.go`, `security.go` contain
zero references to `ClientFactory`, `RESTMapper`, or `DiscoveryClient`. `h.ClusterID` is
referenced only inside a comment (`handler.go:159`) explaining why it is *not* used.

### (b) Complete inventory: "remote rejected" guards across the backend

Grep used: `grep -rn "StatusNotImplemented\|not supported on remote\|not yet supported on remote\|remote clusters not supported\|!pair.IsLocal\|IsLocalClusterID" backend/internal backend/cmd --include=*.go | grep -v _test.go`.

| Site | Status | Message | In Release C scope? |
|---|---|---|---|
| `yaml/handler.go:62–67` | 501 | "YAML validate is not yet supported on remote clusters" | **U9a removes** |
| `yaml/handler.go:147–152` | 501 | "YAML apply is not yet supported on remote clusters" | **U9b removes** |
| `yaml/handler.go:220–225` | 501 | "YAML diff is not yet supported on remote clusters" | **U9a removes** |
| `yaml/handler.go:282–287` | 501 | "YAML export is not yet supported on remote clusters" | **U9a removes** |
| `k8s/resources/dashboard.go:542–547` | 400 | "dashboard summary is only available for the local cluster" | **U10 replaces with opt-in dispatch** (see mobile-compat decision) |
| `k8s/resources/counts.go:27–31` | 400 | "resource counts are only available for the local cluster" | No — stays. U8 reports `unsupported_platform`; U11c renders it |
| `k8s/resources/pods.go:156–165` | 501 | "pod exec not yet supported on remote clusters" | No — deferred appendix. U8 reports `unsupported_platform` |
| `server/handle_ws_logs.go:103–127` | WS close | "WebSocket log streaming not yet supported on remote clusters" | No — deferred appendix |
| `server/handle_ws_logs_search.go:60–74` | WS close | "WebSocket log search not yet supported on remote clusters" | No — deferred appendix |
| `server/handle_ws_flows.go:61–75` | WS close | "WebSocket flow streaming not yet supported on remote clusters" | No — deferred appendix |
| `externalsecrets/actions.go:66–76` (`rejectNonLocalClusterWrite`) | 501 | "ESO write actions are local-cluster only in v1" | No — Release B territory; U8 reports it |
| `networking/handler.go:125–136` | 501 | "&lt;feature&gt; is not supported for remote clusters" | No |
| `certmanager/handler.go:501, 587, 717, 760, 798` | varies | remote-aware branches (cert-manager already *supports* remote via its own `remoteCache`) | No — but it is the working precedent for a per-cluster subsystem cache + evict hook |
| `scanning/handler_vulnerability_detail.go:96` | 501 | "CVE-level detail requires Trivy Operator" | Not a remote guard — scanner-availability guard. Listed only to close the grep honestly |

### (c) The exact dashboard local/aggregate seam

`HandleDashboardSummary` (`dashboard.go:536–771`) is one function that interleaves four concerns:

| Lines | Concern | Purity |
|---|---|---|
| 542–547 | Local-only rejection | control flow |
| 556 | `canListNodes := h.canList(...)` — one SAR, deliberately resolved once | **impure** (SAR) |
| 558–571 | `h.Informers.Nodes().List(...)` / `.Pods().List(...)` | **impure** (informer cache; local-only by construction) |
| 573–582 | Node total/ready counting | **pure over `[]*corev1.Node`** |
| 584–595 | Pod phase counting | **pure over `[]*corev1.Pod`** |
| 597–602 | `h.Informers.Services().List(...)` + `len()` | impure fetch, trivial aggregation |
| 604–613 | `h.Alerts.ActiveAlertCounts(ctx)` | **impure** (Alertmanager) |
| 615–646 | Allocatable / requests / limits `resource.Quantity` sums | **pure over `[]*corev1.Node` + `[]*corev1.Pod`** |
| 648–667 | `formatCPU` / `formatMem` closures | **pure** |
| 669–734 | Prometheus CPU%/Mem% + control-plane goroutines under a shared 1s budget | **impure** |
| 736–747 | Control-plane result resolution | pure over goroutine output |
| 748–767 | Fallback `Utilization` from k8s allocatable when Prometheus is absent | pure |
| 768–770 | `computeClusterHealth(healthInputs)` | **already pure — lives in `health.go:220`** |

So the seam U10 must create is **not** a health seam (that one exists). It is a *counts and
capacity* seam. Three functions, extracted verbatim from the ranges above, callable by both the
local and the remote acquisition path:

```go
// aggregateCounts is pure: no informers, no SARs, no clock, no network.
// Extracted from dashboard.go:573-602.
func aggregateCounts(nodes []*corev1.Node, pods []*corev1.Pod, serviceCount int) (NodeSummary, PodSummary, ServiceCount)

// capacityTotals holds the six Quantity sums computed at dashboard.go:615-646.
type capacityTotals struct {
    CPUAllocatable, CPURequests, CPULimits resource.Quantity
    MemAllocatable, MemRequests, MemLimits resource.Quantity
}

// aggregateCapacity is pure. Extracted from dashboard.go:615-646.
func aggregateCapacity(nodes []*corev1.Node, pods []*corev1.Pod) capacityTotals

// utilizationFrom renders a *Utilization from capacity plus an optional
// observed percentage. pct == nil reproduces the "N/A / 0%" fallback at
// dashboard.go:748-767. Extracted from dashboard.go:648-667 + 702-723.
func utilizationFrom(t capacityTotals, kind resourceKind, pct *float64) *Utilization
```

---

## Where the master plan is wrong about this codebase

These are corrections, not preferences. Each is grounded in a file read above.

1. **U9's file list names three files that need no change.** `applier.go`, `differ.go` and
   `export.go` are already fully parameterised on `mapper meta.RESTMapper` and
   `dynClient dynamic.Interface` (`applier.go:49`, `differ.go:39`) or have no cluster coupling at
   all (`export.go`). The entire local-mapper coupling is six sites in `handler.go`. U9's real
   touch set is `handler.go` plus tests.
2. **U9's "preserve Secret masking" describes behaviour that does not exist in this package.**
   There is no masking helper in `internal/yaml`. Secrets are **refused outright**: diff at
   `handler.go:204–211` (422) and export at `handler.go:261–266` (422). The only masking helper in
   the tree is the unexported `maskedSecret` at `k8s/resources/secrets.go:19`, which the yaml
   package neither imports nor could import without changing behaviour. The correct invariant to
   preserve remotely is **refusal**, and `HandleApply` is deliberately *not* Secret-refusing (it
   must stay able to create Secrets). U9 must assert refusal-parity, not masking-parity.
3. **The frontend has no cluster switcher.** `grep -rn selectedCluster frontend/ --include=*.ts
   --include=*.tsx` returns six sites: the signal's own definition and persistence
   (`cluster.ts:11,13,18`), one header injection (`api.ts:7,142`), one read-only display
   (`TopBarV2.tsx:9,98`), and one reactive refetch (`resource-counts.ts:15,85`). `ClusterManager.tsx`
   registers/tests/deletes clusters and never writes the signal. Nothing in the product can change
   the selected cluster. U11's premise ("rapid cluster switching cannot display old health") is
   untestable until a switcher exists, so building one is part of Release C, not an assumed given.
4. **KTD3's "cluster registry generation" has no backing column, and the obvious candidate is
   poisoned.** `clusters` has `created_at`, `updated_at`, `last_probed_at` and nothing else;
   `UpdateStatus` (`store/clusters.go:152`) writes `updated_at = NOW()` on every 60-second probe,
   so `updated_at` changes roughly 1,440×/day per cluster and would invalidate every cache entry
   on that cadence. Resolution below uses `created_at`.
5. **U8 does not need `server.go`.** Every dependency the handler wants — `ClusterRouter`,
   `ClusterStore`, `ClusterProber`, and `ResourceHandler.AccessChecker` — is already a field on
   `Server` (`server.go:44–88`). No new `Deps` field, no new constructor branch.
6. **`/clusters/{id}/capabilities` is the wrong mount point.** The `/clusters` group is wrapped in
   `middleware.RequireAdmin` (`routes.go:257`), so mounting there would deny a non-admin operator
   the ability to ask "can I apply YAML *here*, on local?" — which is the primary use of the
   endpoint. Alternative mount and its authorization argument are in Design Decisions.
7. **CLAUDE.md's "Known limitation: AccessChecker queries local cluster RBAC, not remote"
   (line 258) is stale.** `access.go:258–265` routes non-local SARs through
   `ClusterRouter.ClientForCluster`, and `main.go:437` wires it. U12 corrects the doc.
8. **`store.ClusterStore.UpdateCredentials` is dead code.** No caller exists anywhere in the tree.
   Any plan text that assumes an in-place credential-rotation path is describing a capability the
   product does not have; rotation today is delete + re-register, which mints a new random id.
9. **U10 as written would break the mobile client.** `mobile/lib/features/dashboard/dashboard_repository.dart`
   defines `DashboardLocalOnlyError` and detects it by matching **HTTP 400 plus the literal
   substring `"local cluster"`**. Turning that 400 into a 200 silently changes mobile behaviour,
   violating R4. Resolution below is an opt-in query parameter.
10. **`scripts/check-cluster-routing.sh` cannot see the regression U9 is guarding against.** It
    matches only `.ClientForUser(` / `.DynamicClientForUser(` (line 89). A future handler that
    reintroduces `h.K8sClient.RESTMapper()` on a remote path passes the guard cleanly.

---

## Design Decisions

### D1. Per-target discovery cache

**Type and location.** `backend/internal/k8s/discovery_cache.go`, package `k8s`, unexported
struct owned by `ClusterRouter` (a plain field, not a global):

```go
// targetSchemaCache is a bounded, TTL'd, identity-keyed cache of per-cluster
// discovery + RESTMapper pairs. Owned by ClusterRouter; never shared across
// ClusterRouter instances and never exported.
type targetSchemaCache struct {
    mu      sync.Mutex
    entries map[schemaCacheKey]*schemaCacheEntry
    order   []schemaCacheKey // insertion order for the bounded-size eviction
}

type schemaCacheKey struct {
    clusterID  string // normalized via NormalizedClusterID
    generation string // see D2
    identity   string // cacheKey(username, groups) — sha256, already collision-resistant
}

type schemaCacheEntry struct {
    discovery discovery.CachedDiscoveryInterface // memory.NewMemCacheClient(...)
    mapper    meta.RESTMapper                    // restmapper.NewDeferredDiscoveryRESTMapper(discovery)
    expiresAt time.Time
}
```

**Key.** `(normalized cluster id, credential generation, identity hash)`. The identity component
is `cacheKey(username, groups)` — the exact function the client caches already use
(`client.go:285`), so key semantics cannot drift between the client cache and the schema cache.

**Bounded size and TTL.** `maxSchemaCacheEntries = 128`; TTL = the existing `clientCacheTTL`
(5 minutes, `client.go:24`). Insertion beyond 128 evicts the oldest key from `order` (FIFO, not
LRU — FIFO is simpler, and with a 5-minute TTL the difference is immaterial while the invariant
"no entry outlives its TTL" stays trivially provable). Each entry holds one memcache discovery
client and one deferred mapper; the memory cost is dominated by the cached `APIResourceList`
set, typically low hundreds of KiB per entry.

**Eviction hooks it must register.** Two, both inside `cluster_router.go`, both same-package
direct calls (no `RegisterEvictHook` needed — that mechanism exists to avoid *upward* imports
from `k8s` into `certmanager` etc.; the schema cache has no such problem):

1. `EvictCluster` (`cluster_router.go:187`) — add `cr.schemaCache.evictCluster(clusterID)` before
   the existing `evictCBs` fan-out, so cluster deletion drops schema alongside clients.
2. `StartCacheSweeper` (`cluster_router.go:232`) — add `cr.schemaCache.sweepExpired(now)` inside
   the existing `case <-ticker.C:` body, so no new goroutine is created.

**Identity-isolation rule.** Discovery on a remote cluster is *unavoidably* per-identity: every
remote `rest.Config` `ClusterRouter` builds sets `Impersonate` (`cluster_router.go:407–410`), and
there is no service-account remote client anywhere in the product. Kubernetes' default
`system:discovery` ClusterRole is bound to `system:authenticated`, but a hardened cluster can
remove that binding, at which point two impersonated identities legitimately see different API
resource sets. **Therefore discovery results are permission-bearing and the cache key includes
the identity hash. No entry is ever served to an identity other than the one that populated it.**

*Pricing it, as required.* Worst-case entries = (distinct identities active within the 5-minute
TTL) × (distinct remote clusters). The 128-entry cap makes the worst case bounded rather than
proportional: at 128 entries the cache degrades to a cold-start miss per request, which costs one
`ServerGroupsAndResources` round trip against an already-authenticated, already-pooled
connection. That is the correct failure mode — slower, never leakier. A deployment with more
than ~25 concurrently-active admins across ~5 remote clusters should raise
`maxSchemaCacheEntries`; the constant is a single named `const` for exactly that reason.

**The local branch is deliberately different and must not be "fixed".** For `clusterID == local`,
`TargetSchema` returns `cr.localFactory.RESTMapper()` and `cr.localFactory.DiscoveryClient()` —
which are service-account-scoped and shared across all identities (`client.go:209–220`). This is
pre-existing behaviour for every local handler in the product. Changing local discovery to
per-identity would alter RBAC semantics repo-wide and is explicitly out of Release C scope.
The asymmetry is documented on the method and asserted by a test so nobody "harmonises" it later.

**Public surface added to `ClusterRouter`:**

```go
// TargetSchema binds a cluster identity to the discovery + mapper that must be
// used for it. Callers MUST use the returned Mapper/Discovery; reaching back to
// LocalFactory().RESTMapper() on a remote request is the bug this type exists to
// prevent (see scripts/check-cluster-routing.sh).
type TargetSchema struct {
    ClusterID  string // normalized
    Generation string // "local" for the local cluster; see D2 otherwise
    IsLocal    bool
    Discovery  discovery.DiscoveryInterface
    Mapper     meta.RESTMapper
    // Invalidate forces the next RESTMapping to re-discover. Safe to call on the
    // local branch (delegates to the deferred mapper's own Reset).
    Invalidate func()
}

// TargetFor resolves clients AND schema for one cluster in a single call, so a
// handler cannot pair a remote client with a local mapper.
func (cr *ClusterRouter) TargetFor(ctx context.Context, clusterID, username string, groups []string) (*ClientPair, *TargetSchema, error)

// TargetSchemaFor returns only the schema half (capabilities, export).
func (cr *ClusterRouter) TargetSchemaFor(ctx context.Context, clusterID, username string, groups []string) (*TargetSchema, error)
```

`RouterFor` is left untouched — it has many callers and none of them need schema.

### D2. Credential generation without a migration

`generation` is `ClusterRecord.CreatedAt.UTC().Format(time.RFC3339Nano)` for a remote cluster,
and the literal `"local"` for the local cluster. Rationale:

- `created_at` is written once at `Create` and never updated by any code path in the tree.
- `updated_at` is unusable: `UpdateStatus` bumps it every 60s from the prober.
- The real re-registration defence is upstream: `generateClusterID` mints 128 bits of
  `crypto/rand` (`handle_clusters.go:355–361`) and there is no update endpoint, so a
  delete-then-re-register cycle **cannot** reuse an id. `created_at` in the key is
  defence-in-depth against a future in-place rotation path, plus it makes the cache key
  self-describing in logs.
- `EvictCluster` on delete (`handle_clusters.go:305`) remains the primary invalidation.

**Risk, flagged rather than solved (per the no-migration rule):** if a future release adds
in-place credential rotation (reviving the dead `UpdateCredentials`), `created_at` stops
distinguishing generations and a real `credential_generation` column becomes necessary. That
release owns the migration. Release C records the requirement here and does not pre-allocate a
sequence number.

### D3. The capability model

Six independent dimensions, never collapsed into one boolean:

| Dimension | Type | Meaning | Source |
|---|---|---|---|
| `platformSupported` | `bool` | k8sCenter has implemented this operation for this target class (local vs remote). Static per operation × class. | Compile-time table in `handle_capabilities.go` |
| `discoveryPresent` | `*bool` | The target's API surface actually contains the group/resource the operation needs. `null` when the operation needs no specific GVR. | `TargetSchema.Discovery` |
| `reachable` | `*bool` | Last observation says the target answers. `null` when unknown or stale. | `ClusterRecord.Status` + `LastProbedAt` (never a fresh probe) |
| `authorized` | `*bool` | This identity's SAR verdict for the operation's representative verb/resource. `null` when the SAR could not be evaluated. | `AccessChecker.CanAccess` |
| `observedAt` | RFC3339 | When the *weakest-freshness* input in this row was observed. | max-staleness of the contributing inputs |
| `reasonCode` | enum | Why the row is not plain `ok`. | below |

**Reason codes — the complete list.** Any value outside this set is a bug.

| Code | Meaning |
|---|---|
| `ok` | Supported, discovered, reachable, authorized |
| `unsupported_platform` | k8sCenter has not implemented this for this target class (remote exec, remote log/flow/search streams, remote resource counts, remote ESO writes, remote health scoring) |
| `discovery_missing` | Target's discovery does not contain the required group/resource |
| `discovery_unavailable` | Discovery call failed — the answer is unknown, not "absent" |
| `unreachable` | Last probe says `disconnected` / `blocked` / `error` |
| `stale_observation` | Last probe is older than 3× the 60s probe interval; `reachable` is `null` |
| `forbidden` | SAR returned `Allowed: false` for this identity |
| `authz_unknown` | SAR could not be issued or errored |
| `cluster_unknown` | No such cluster id in the registry |
| `credentials_invalid` | Decrypt / TLS-policy / impersonation-probe failure |
| `db_unavailable` | No `ClusterStore` wired (local-only deployment) and a non-local target was asked for |

**Stated plainly, because AE2 and AE3 both depend on it:**
**`unreachable` is not `unsupported`. `forbidden` is not `unsupported`.** When a cluster is down
or an identity lacks RBAC, `platformSupported` stays **`true`** and only `reachable`/`authorized`
go false. A UI that renders "not supported" for a temporarily-down cluster teaches operators that
the product cannot do something it can do, and it hides a recoverable outage behind a permanent-
sounding word. The three states are rendered differently by U11b.

**No permission-bearing response is ever cached across identities.** The capabilities response is
computed per request. The only caches it consults are (i) the identity-keyed schema cache (D1) and
(ii) `AccessChecker`'s SAR cache, whose key already includes `clusterID`, `username` and sorted
`groups` (`access.go:197–205`). The handler sets `Cache-Control: no-store` and holds no map of
its own.

### D4. Target-pinning contract for YAML (AE2)

**Wire representation.** Two query parameters, both optional on read verbs and both **honoured on
`POST /v1/yaml/apply`**:

```
POST /api/v1/yaml/apply?force=true&targetCluster=<clusterId>&targetGeneration=<RFC3339Nano|local>
X-Cluster-ID: <clusterId>
```

Query parameters, not a body field, because the apply body is raw YAML (`Content-Type: text/yaml`,
`readYAMLBody` at `handler.go:327`) and adding an envelope would break every existing client
including mobile.

**Binding.** A preview (`/yaml/validate` or `/yaml/diff`) response gains two fields —
`targetCluster` and `targetGeneration` — echoed from the resolved `TargetSchema`. The client
stores them alongside the previewed YAML. That pair *is* the pin.

**Enforcement.** `HandleApply` compares, in this order:

1. If `targetCluster` is absent → unpinned legacy request; proceed on the header (preserves
   mobile and any existing script).
2. If `targetCluster` is present and, after `NormalizedClusterID`, differs from the header-derived
   cluster → **409** via `httputil.WriteErrorWithReason(w, 409, "…", "cluster_pin_mismatch",
   map[string]any{"pinnedClusterId": …, "requestClusterId": …})`. **No document is applied.**
3. If `targetGeneration` is present and differs from the resolved `TargetSchema.Generation` →
   **409**, reason `cluster_generation_mismatch`. Same no-mutation guarantee.

Both checks run **before** the documents reach the applier, so a mismatch cannot partially apply.

**What happens when the UI cluster changes mid-flow.** The client (U11a/U11b) never mutates a
pinned request in place. `useYamlApply` captures `{clusterId, generation}` at the moment a preview
succeeds; `handleApply` sends that captured pair and sets `X-Cluster-ID` from the *same captured
value*, not from `selectedCluster.value`. If the operator has since switched clusters, the request
is still addressed to the pinned target — and the server's own check confirms it. The apply
therefore **stays pinned**; it never silently retargets. If the operator explicitly discards the
pin (a "re-preview on &lt;new cluster&gt;" action), the preview is cleared first and a new pin is
minted. If the pinned cluster has been deleted between preview and apply, the server answers
`cluster_unknown` and the client shows "the target you previewed no longer exists" — an abort, not
a retarget.

**Why read verbs are not enforced.** `validate`, `diff` and `export` are idempotent reads and
dry-runs. A mis-targeted read is a UX defect, handled client-side by discarding stale results; it
is not a safety event. Enforcing on the mutating verb only keeps the change surface minimal and
keeps the security argument crisp: *the only endpoint that can change a remote cluster refuses to
run against a cluster the operator did not review.*

### D5. Remote dashboard coverage model (AE3)

**Per-section status** — `ok | partial | unavailable | forbidden | stale`, one row per section:

```go
type SectionCoverage struct {
    Section    string `json:"section"`    // nodes|pods|services|cpu|memory|alerts|health
    Status     string `json:"status"`
    ReasonCode string `json:"reasonCode"` // shares D3's enum verbatim
    ObservedAt string `json:"observedAt,omitempty"`
    Detail     string `json:"detail,omitempty"`
}
```

`Coverage []SectionCoverage` with `json:"coverage,omitempty"` is added to the existing
`DashboardSummary`. `omitempty` means the local response is byte-identical to today's, so mobile
and the existing web island are unaffected until they opt in.

**The rule, stated as an invariant:** *no aggregate health score is synthesised from incomplete
inputs.* Concretely, on the remote path `Health` is **always `nil`** in v1 and its coverage row is
`{section:"health", status:"unavailable", reasonCode:"unsupported_platform", detail:"remote health
scoring requires the remote metrics binding (deferred)"}`. `computeClusterHealth` is **not
called** on the remote path.

This is not defensive over-caution; it follows from reading `health.go:434–457`. The composite
score renormalises weights across whichever signals resolved `ok`, so a remote cluster where only
node listing succeeds would emit a *confident* score derived from nodes alone — a manufactured
health reading, precisely what AE3 forbids. The remote path has no informer-backed workloads
signal, no Prometheus binding, and no alert provider; four of four weighted signals are absent or
unverifiable. `nil` is the only truthful answer, and it must be enforced by test rather than by
comment (see U10).

`partial` is reserved for a section whose *own* inputs were incomplete — e.g. node listing
succeeded but a page-limit truncated the result, or pods succeeded in some namespaces and were
forbidden in others. `stale` carries `observedAt` and is used when the value shown came from a
probe or list older than the freshness budget.

### D6. The no-fallback invariant, enforced structurally

**Statement:** a failure against a remote target must never execute locally. Four structural
mechanisms, in order of strength:

1. **Type-level.** `TargetSchema` is the only way a handler obtains a mapper after U9.
   `TargetFor` builds the `ClientPair` and the `TargetSchema` from the *same* resolved cluster id
   in one call, so the "remote client + local mapper" pairing that produced the current 501s is
   not expressible. On any remote error `TargetFor` returns `(nil, nil, err)` — there is no branch
   that substitutes `localFactory`.
2. **Lint-level.** `scripts/check-cluster-routing.sh` gains `.RESTMapper(` and `.DiscoveryClient(`
   to its match set (U9b), with `backend/internal/k8s/client.go`, `cluster_router.go`,
   `informers*` and `cluster_prober.go` already exempt by the existing `ALLOWED_PREFIXES`. A
   handler that reintroduces `h.K8sClient.RESTMapper()` then fails the same CI step that already
   guards `ClientForUser`.
3. **Test-level.** `TestTargetSchema_RemoteFailureReturnsNoLocalMapper` (U7): with a
   `ClusterStore` whose `Get` errors, assert `schema == nil && err != nil`. Companion
   `TestTargetSchema_RemoteMapperIsNotLocalMapper`: on a *successful* remote resolve, assert
   `schema.Mapper != cr.LocalFactory().RESTMapper()` (pointer inequality) and
   `schema.Discovery != cr.LocalFactory().DiscoveryClient()`.
4. **Behavioural test-level.** `TestHandleApply_RemoteUnreachableDoesNotTouchLocal` (U9b): a
   fake local dynamic client is seeded and handed to `localFactory`; the remote resolve is made to
   fail; assert the response is a 5xx **and** the fake local client recorded **zero** actions.
   This is the test that would actually catch a silent fallback, because it asserts on the local
   cluster's side of the wire rather than on the error string.

---

## Unit plan

Nine units after splits (master plan had six). Splits and their justifications are marked.

| Id | Title | Master-plan origin | Files | Depends on |
|---|---|---|---|---|
| U7 | Per-target discovery + schema binding | U7 | 4 | — |
| U8 | Capability disclosure endpoint | U8 | 4 | U7 |
| U9a | Remote YAML read verbs (validate / diff / export) | U9 (split) | 3 | U7 |
| U9b | Remote YAML apply + pin enforcement + routing guard | U9 (split) | 4 | U9a |
| U10 | Remote dashboard summary + coverage | U10 | 3 | U7 |
| U11a | Cluster switcher + switch-safe request targeting | U11 (split) | 5 | U8 |
| U11b | Capability client + YAML pin/gate UI | U11 (split) | 5 | U11a, U9b |
| U11c | Remote dashboard coverage rendering | U11 (split) | 4 | U11a, U10 |
| U12 | Two-cluster fixture, live-run runbook, documentation | U12 | 5 | U11b, U11c |

---

## U7 — Resolve discovery for the actual target cluster

**Branch:** `feat/u7-target-discovery-cache`
**PR title:** `feat(k8s): per-target discovery and RESTMapper via ClusterRouter`
**Covers:** R1, R9; KTD2, KTD3, KTD5.

### Files (4)

1. `backend/internal/k8s/discovery_cache.go` — **new**
2. `backend/internal/k8s/discovery_cache_test.go` — **new**
3. `backend/internal/k8s/cluster_router.go`
4. `backend/internal/k8s/cluster_router_test.go`

Master plan listed the same four. Confirmed correct — no `main.go` edit is needed because the
cache is a `ClusterRouter` field, and no `RegisterEvictHook` is needed because both live in
package `k8s`.

### Agent Directive 1 (Step 0)

`cluster_router.go` is 601 LOC, above the 300-LOC threshold, and U7 is a structural change to it.
Run the Step-0 scan (unused imports, dead props, unused exports, debug logs) **before** the
feature work. From the read: no dead code was observed in this file — every exported symbol has a
caller and there are no debug prints. **Record the scan result in the PR description and skip the
cleanup commit.** The directive requires the scan; it does not require inventing a cleanup.

### Implementation steps

1. **`discovery_cache.go`** — define `schemaCacheKey`, `schemaCacheEntry`, `targetSchemaCache`
   exactly as in D1, plus:
   ```go
   const maxSchemaCacheEntries = 128

   func newTargetSchemaCache() *targetSchemaCache
   func (c *targetSchemaCache) get(k schemaCacheKey, now time.Time) (*schemaCacheEntry, bool)
   func (c *targetSchemaCache) put(k schemaCacheKey, e *schemaCacheEntry)   // evicts FIFO past the cap
   func (c *targetSchemaCache) evictCluster(clusterID string)
   func (c *targetSchemaCache) sweepExpired(now time.Time)
   func (c *targetSchemaCache) len() int                                    // test-only accessor
   ```
   No goroutine is created here. `recoverutil` therefore does not apply to this file.

2. **`cluster_router.go` — struct field.** Add `schemaCache *targetSchemaCache` to
   `ClusterRouter` (anchor: the field block at lines 24–49, immediately after `remoteDynCache`)
   and initialise it in `NewClusterRouter` (anchor: lines 53–60).

3. **`cluster_router.go` — `TargetSchema` type + `TargetSchemaFor`.** Insert after
   `LocalFactory()` (anchor: line 141). Local branch returns
   `{ClusterID: LocalClusterID, Generation: "local", IsLocal: true, Discovery:
   cr.localFactory.DiscoveryClient(), Mapper: cr.localFactory.RESTMapper(), Invalidate: no-op}`
   and **does not touch the cache**. Remote branch:
   a. fail closed when `cr.clusterStore == nil`, mirroring `ClientForCluster`'s F#18 message
      (anchor: lines 75–77);
   b. `rec, err := cr.clusterStore.Get(ctx, clusterID)` → `generation :=
      rec.CreatedAt.UTC().Format(time.RFC3339Nano)`;
   c. cache lookup on `schemaCacheKey{NormalizedClusterID(clusterID), generation,
      cacheKey(username, groups)}`;
   d. on miss, `cfg, err := cr.remoteConfig(ctx, clusterID, username, groups)` — **reuse the
      existing singleflight-protected builder verbatim; do not add a second config path**; then
      `dc, err := discovery.NewDiscoveryClientForConfig(cfg)`,
      `cached := memory.NewMemCacheClient(dc)`,
      `mapper := restmapper.NewDeferredDiscoveryRESTMapper(cached)`;
   e. `put` and return, with `Invalidate: cached.Invalidate`.

4. **`cluster_router.go` — `TargetFor`.** Compose `RouterFor` + `TargetSchemaFor` and return
   `(*ClientPair, *TargetSchema, error)`. Both halves resolve the same `clusterID` argument, which
   is what makes the "remote client, local mapper" pairing unexpressible.

5. **`cluster_router.go` — `EvictCluster`.** Insert `cr.schemaCache.evictCluster(clusterID)`
   immediately after the `remoteDynCache.Range` block and **before** the `evictCBs` snapshot
   (anchor: line 201).

6. **`cluster_router.go` — `StartCacheSweeper`.** Inside the existing `case <-ticker.C:` body
   (anchor: line 241), after the two existing `Range` sweeps, add
   `cr.schemaCache.sweepExpired(now)`.

7. **`cluster_router.go` — recoverutil.** `StartCacheSweeper` is a bare `go func()` (line 233)
   with no panic recovery — a pre-existing gap under
   `docs/solutions/backend-resilience-conventions.md` Part 1. U7 adds new work into that loop
   (step 6), so wrap the loop body: `recoverutil.Tick(ctx, cr.logger, "k8s cluster-router cache
   sweep", func(context.Context) { …existing body… })`. There is no `wg.Done()` and no counted
   channel send in this loop, so the channel-send/cleanup hazard does not apply. This is the only
   recoverutil change in Release C's backend; U8, U9a and U9b add **no** goroutines, and U10's is
   covered in its own section.

### Named tests

`discovery_cache_test.go`:

| Test | Asserts |
|---|---|
| `TestSchemaCache_TTLExpiry` | An entry past `clientCacheTTL` is not returned by `get` |
| `TestSchemaCache_BoundedSize` | Inserting 129 keys leaves `len() == 128` and the first key is gone |
| `TestSchemaCache_EvictClusterDropsOnlyThatCluster` | Entries for other cluster ids survive |
| `TestSchemaCache_GenerationChangeIsACacheMiss` | Same cluster id, different `generation` → separate entry |
| `TestSchemaCache_IdentityIsolation` | Two identity hashes → two entries; `get` with identity B never returns A's entry |
| `TestSchemaCache_SweepExpiredIsIdempotent` | Repeated sweeps do not panic and do not drop live entries |

`cluster_router_test.go` (additions, following the existing `fake.NewSimpleDynamicClient` +
`NewTestClientFactoryWithDynamic` idiom at lines 79–145):

| Test | Master-plan scenario | Asserts |
|---|---|---|
| `TestTargetSchemaFor_LocalUsesLocalFactory` | — | `IsLocal`, `Generation == "local"`, mapper identical to `LocalFactory().RESTMapper()`, cache untouched (`len() == 0`) |
| `TestTargetSchemaFor_RemoteFailsClosedWhenStoreNil` | "forbidden discovery … do not reuse stale credentials" | error mentions the cluster id; schema is nil |
| `TestTargetSchema_RemoteFailureReturnsNoLocalMapper` | "never the local mapper" | D6 mechanism 3 |
| `TestTargetSchema_RemoteMapperIsNotLocalMapper` | "A CRD present only remotely resolves against that cluster" | pointer inequality for both `Mapper` and `Discovery` |
| `TestTargetFor_ClientAndSchemaShareClusterID` | "target and discovery remain paired" | `pair.ClusterID == schema.ClusterID` for local and remote |
| `TestEvictCluster_DropsSchemaCache` | "cluster deletion/re-registration" | schema entry gone after `EvictCluster` |
| `TestTargetSchema_ConcurrentColdCache` | "concurrent cold-cache requests" | 32 goroutines, same key: `-race` clean, at most one entry created |

### Verification (Agent Directive 4, repo-wide)

```
cd backend && go vet ./... && go test ./...
cd backend && go test ./internal/k8s/... -race -count=1
bash scripts/check-cluster-routing.sh
```
Frontend and e2e are unchanged by this unit; state that in the PR rather than claiming a run.

### Exit criteria — "done means"

- [ ] `TargetSchemaFor` / `TargetFor` exist and are documented with the local/remote identity
      asymmetry and the "do not reach back to LocalFactory" warning.
- [ ] The cache is bounded, TTL'd, identity-keyed, generation-keyed, and evicted by both
      `EvictCluster` and the existing sweeper.
- [ ] No new goroutine; the existing sweeper is now `recoverutil.Tick`-wrapped.
- [ ] `remoteConfig` remains the single remote-`rest.Config` builder — grep proves no second call
      to `store.Decrypt`, `ValidateRemoteURL`, or `applyClusterTLS` was added.
- [ ] All seven new router tests plus six cache tests pass under `-race`.
- [ ] `scripts/check-cluster-routing.sh` still reports zero violations.
- [ ] No behaviour change for any existing caller: `RouterFor`'s signature and semantics are
      byte-identical.

---

## U8 — Publish per-operation capabilities

**Branch:** `feat/u8-cluster-capabilities`
**PR title:** `feat(server): per-cluster, per-identity capability disclosure`
**Covers:** R3, R8; KTD2. **Depends on:** U7.

### Files (4)

1. `backend/internal/server/handle_capabilities.go` — **new**
2. `backend/internal/server/handle_capabilities_test.go` — **new**
3. `backend/internal/server/routes.go`
4. `frontend/lib/capability-types.ts` — **new**

Corrected from the master plan: **`server.go` is not needed** (correction 5).

### Route placement

```go
// routes.go — inside the authenticated group (anchor: after line 115,
// `ar.Get("/cluster/info", …)`), NOT inside the admin-only /clusters group.
ar.Get("/capabilities/{clusterID}", s.handleClusterCapabilities)
```

Deviation from the master plan's `/clusters/{id}/capabilities`, with two reasons:

- The `/clusters` group is wrapped in `middleware.RequireAdmin` (`routes.go:257`). Mounting there
  would deny a non-admin operator the ability to ask about the **local** cluster, which is the
  endpoint's primary use.
- Authorization still lands correctly with zero new middleware. Because the handler requires
  `path == header`, a request for a non-local cluster necessarily carries a non-local
  `X-Cluster-ID`, which `middleware.ClusterContext` (line 112, applied to the whole group) has
  already admin-gated at `cluster.go:33–36`. Local → any authenticated user; remote → admin.
  Exactly the intended shape.

### Implementation steps

1. **Types** in `handle_capabilities.go`: `Capability`, `CapabilitiesResponse`, the operation
   table, and the reason-code constants from D3. The operation table is a package-level
   `var capabilityOperations = []capabilityOp{…}` with, per operation: id, human label, whether it
   is supported locally, whether it is supported remotely, the GVR (if any) whose presence must be
   checked, and the SAR verb/group/resource used for the `authorized` dimension.

   Initial rows, each traceable to a guard in inventory (b):

   | Operation id | Local | Remote (after Release C) | Notes |
   |---|---|---|---|
   | `yaml.validate` | yes | **yes** | U9a |
   | `yaml.diff` | yes | **yes** | U9a; Secrets refused on both |
   | `yaml.export` | yes | **yes** | U9a; Secrets refused on both |
   | `yaml.apply` | yes | **yes** | U9b |
   | `dashboard.summary` | yes | **partial** — no health score | U10; reason `unsupported_platform` on the health section |
   | `resources.counts` | yes | no | `counts.go:27` |
   | `pod.exec` | yes | no | `pods.go:156` |
   | `logs.stream` | yes | no | `handle_ws_logs.go:103` |
   | `logs.search` | yes | no | `handle_ws_logs_search.go:60` |
   | `flows.stream` | yes | no | `handle_ws_flows.go:61` |
   | `eso.write` | yes | no | `externalsecrets/actions.go:66` |

   **The table must be kept honest by construction**, not by discipline: each row carries the
   source file and line of the guard it mirrors as a struct comment, so a reviewer can diff the
   claim against the guard.

2. **Handler** `func (s *Server) handleClusterCapabilities(w http.ResponseWriter, r *http.Request)`:
   a. `user, ok := httputil.RequireUser(w, r)`;
   b. `pathID := chi.URLParam(r, "clusterID")`; `hdrID := middleware.ClusterIDFromContext(r.Context())`;
   c. **mismatch check** — `if k8s.NormalizedClusterID(pathID) != k8s.NormalizedClusterID(hdrID)`
      → `httputil.WriteErrorWithReason(w, http.StatusConflict, "cluster target mismatch",
      "cluster_target_mismatch", map[string]any{"pathClusterId": pathID, "headerClusterId": hdrID})`
      and return;
   d. resolve `schema, err := s.ClusterRouter.TargetSchemaFor(ctx, pathID, user.KubernetesUsername,
      user.KubernetesGroups)` — on error map to `cluster_unknown` / `credentials_invalid` /
      `db_unavailable` by inspecting the error, and still return **200 with every row carrying the
      failure reason** rather than a bare 5xx. The point of the endpoint is to explain
      unavailability; failing opaquely defeats it. (Exception: an unauthenticated or mismatched
      request is a 401/409 and returns no rows.)
   e. reachability from `s.ClusterStore.Get` → `Status` + `LastProbedAt`, with the 3×60s staleness
      rule; the local cluster is `reachable: true, observedAt: now`;
   f. discovery presence from `schema.Discovery.ServerGroupsAndResources()` — tolerate the partial-
      result-with-error shape exactly as `resolveGVR` does (`handler.go:355–361`): only treat it as
      `discovery_unavailable` when the list is nil;
   g. authorization from `s.ResourceHandler.AccessChecker.CanAccess(ctx, normalizedID,
      user.KubernetesUsername, user.KubernetesGroups, verb, resource, namespace)`; a returned error
      maps to `authorized: nil, reasonCode: authz_unknown` — never to `false`;
   h. `w.Header().Set("Cache-Control", "no-store")`; `httputil.WriteData(w, resp)`.

3. **`frontend/lib/capability-types.ts`** — types only, no fetching (that is U11b). Mirrors the Go
   structs, including a `ReasonCode` string-literal union covering all eleven codes so a missing
   case is a compile error under `deno check`.

### Named tests (`handle_capabilities_test.go`)

| Test | Master-plan scenario | Asserts |
|---|---|---|
| `TestCapabilities_HeaderPathMismatchRejected` | "Header/path cluster mismatch is rejected" | 409, reason `cluster_target_mismatch`, no body rows |
| `TestCapabilities_LocalRequiresNoAdmin` | — | A non-admin user gets 200 for `local` |
| `TestCapabilities_UnreachableIsNotUnsupported` | "unreachable and forbidden are not reported as unsupported" | For a `disconnected` cluster: `platformSupported == true`, `reachable == false`, `reasonCode == "unreachable"` |
| `TestCapabilities_ForbiddenIsNotUnsupported` | same | SAR denies: `platformSupported == true`, `authorized == false`, `reasonCode == "forbidden"` |
| `TestCapabilities_TwoIdentitiesGetOwnPermissionView` | "Two identities get their own permission view" | Identity A allowed / identity B denied for the same cluster in the same test; both correct; no shared map in the handler |
| `TestCapabilities_RemoteExecUnsupported` | "remote exec remains unsupported" | `pod.exec` on a remote cluster: `platformSupported == false`, `reasonCode == "unsupported_platform"` |
| `TestCapabilities_StaleProbeYieldsNullReachable` | cross-cutting (R3) | `LastProbedAt` 5 minutes old → `reachable == nil`, `reasonCode == "stale_observation"` |
| `TestCapabilities_DiscoveryPartialErrorIsNotMissing` | cross-cutting | Partial `ServerGroupsAndResources` result → rows resolve, no `discovery_missing` false positive |
| `TestCapabilities_NoStoreHeader` | KTD2 | `Cache-Control: no-store` present |
| `TestCapabilities_ReasonCodesAreClosed` | cross-cutting | Table-drives every operation × every failure injection; asserts every emitted `reasonCode` is in the constant set |

### Verification

```
cd backend && go vet ./... && go test ./...
cd frontend && deno task check
bash scripts/check-cluster-routing.sh
```
(`deno task test` and `deno task build` are unaffected — `capability-types.ts` is types-only — but
run `deno task check` because it type-checks the whole tree.)

### Exit criteria — "done means"

- [ ] `GET /api/v1/capabilities/{clusterID}` returns the six dimensions per operation.
- [ ] Header/path disagreement is a 409 with a machine-readable reason; it is impossible to get a
      capability answer for a cluster other than the one the request is addressed to.
- [ ] `unreachable` and `forbidden` never collapse into `unsupported_platform`; asserted by two
      dedicated tests.
- [ ] Two identities in one test get different `authorized` values; no handler-owned cache.
- [ ] The operation table cites the guard file:line it mirrors for every "no".
- [ ] `frontend/lib/capability-types.ts` compiles and its `ReasonCode` union is exhaustive.

---

## U9a — Remote YAML read verbs (validate / diff / export)

**Branch:** `feat/u9a-remote-yaml-reads`
**PR title:** `feat(yaml): target-scoped discovery for validate, diff and export`
**Covers:** R1, R9 (read half). **Depends on:** U7.

### Split decision and justification (required by the brief)

**Decision: U9 is split into U9a and U9b. The five-file cap is NOT the reason.**

The master plan's U9 lists five files, but the real touch set is two (correction 1), so the cap is
satisfied either way. The reason to split is blast radius: `validate`/`diff`/`export` are reads and
dry-runs, `apply` mutates a remote cluster. Landing them together means one review covers both the
new discovery plumbing and the first remote-mutation path in the product's history. Splitting lets
U9a prove the mapper swap end-to-end with zero mutation risk, and lets U9b's review focus entirely
on the mutation contract, the pin, and the lint guard. This mirrors a distinction the repo already
draws: `HandleDiff` refuses Secrets while `HandleApply` must not.

### Agent Directive 1 (Step 0) — required, and non-empty

`handler.go` is 380 LOC (>300) and U9a is a structural change to it. The Step-0 scan finds one
concrete item: **`Handler.ClusterID` (`handler.go:29`) is dead** — its only appearance outside the
struct definition is the comment at line 159 explaining that the per-request context value is used
*instead of* it. Remove the field and update `server.go:221` (`ClusterID: deps.Config.ClusterID`)
in a **separate first commit on the same branch**, per Directive 1.

Do **not** remove `Handler.K8sClient` here — U9a still needs it until U9b's last call site is
gone. Its removal is U9b's Step 0.

### Files (3)

1. `backend/internal/yaml/handler.go`
2. `backend/internal/yaml/remote_test.go` — **new**
3. `backend/internal/server/server.go` — Step-0 commit only (drop the `ClusterID:` line)

### Implementation steps

1. **Step-0 commit.** Delete `ClusterID string` (`handler.go:29`); delete `ClusterID:
   deps.Config.ClusterID,` (`server.go:221`); adjust the comment at `handler.go:158–160` to stop
   referencing a field that no longer exists. `go build ./...` must pass on this commit alone.

2. **`resolveGVR` re-signature** (anchor: `handler.go:350`):
   ```go
   // resolveGVR resolves a plural resource name to a GroupVersionResource using
   // the TARGET cluster's discovery. Passing the local ClientFactory's discovery
   // here is the bug this signature change exists to prevent.
   func resolveGVR(disc discovery.DiscoveryInterface, kind string) (schema.GroupVersionResource, error)
   ```
   The body is unchanged except that `disc := clientFactory.DiscoveryClient()` (line 353) becomes
   the parameter.

3. **`HandleValidate`** (anchor: lines 51–69). Replace the `RouterFor` call with
   `pair, target, err := h.ClusterRouter.TargetFor(...)`; **delete** the `!pair.IsLocal` 501 block
   (62–67); replace `mapper := h.K8sClient.RESTMapper()` (69) with `mapper := target.Mapper`.
   Add `targetCluster` / `targetGeneration` to `validateResponse` (anchor: the struct at 83–86),
   populated from `target.ClusterID` / `target.Generation` — this is the pin the client stores
   (D4).

4. **`HandleDiff`** (anchor: lines 213–227). Identical treatment. The Secret-refusal loop at
   204–211 runs **before** routing and must stay exactly where it is — refusal parity for remote
   is achieved by not moving the check, and asserted by a test.

5. **`HandleExport`** (anchor: lines 273–290). Identical treatment; `resolveGVR(h.K8sClient, kind)`
   (290) becomes `resolveGVR(target.Discovery, kind)`. The Secret-refusal block at 260–266 and the
   `ValidateK8sName` checks at 250–258 stay ahead of routing, unchanged.

6. **CRD-in-bundle refresh.** No code needed: `applyOne`'s 3× `RESTMapping` retry
   (`applier.go:102–121`) already re-drives the injected mapper, and
   `NewDeferredDiscoveryRESTMapper` over `memory.NewMemCacheClient` invalidates on miss — which is
   why U7 wires `Invalidate` into `TargetSchema` rather than hand-rolling a refresh. `diffOne`
   does **not** retry (`differ.go:60–64`, single `RESTMapping`); that asymmetry is pre-existing and
   is documented in the test table rather than silently changed.

### Named tests (`remote_test.go`)

Built with a fake discovery for the target, `dynamicfake.NewSimpleDynamicClient` for the dynamic
client, and `httptest` for the handler, following the existing `cluster_router_test.go` fake idiom.

| Test | Master-plan scenario | Asserts |
|---|---|---|
| `TestHandleValidate_RemoteUsesTargetMapper` | "A CRD present only remotely resolves against that cluster" | A GVK absent from local discovery and present in target discovery validates successfully; the local mapper is never consulted (local fake discovery records zero calls) |
| `TestHandleDiff_RemoteUsesTargetMapper` | same | as above for diff |
| `TestHandleExport_RemoteUsesTargetDiscovery` | "export … behaves correctly remotely" | `resolveGVR` resolves a remote-only plural; local discovery untouched |
| `TestHandleValidate_ResponseCarriesTargetPin` | AE2 (pin minting) | `targetCluster` / `targetGeneration` echo the resolved schema |
| `TestHandleDiff_SecretRefusedOnRemote` | corrected from "masked Secret" | 422 with the existing message for a remote target — refusal parity |
| `TestHandleExport_SecretRefusedOnRemote` | same | 422 |
| `TestHandleValidate_RemoteDiscoveryFailureIsNotLocalFallback` | "A remote connection failure never falls back to local execution" | Target resolve errors → 5xx; local fake discovery and local fake dynamic client both record zero calls |
| `TestHandleExport_HeaderClusterIsTheOnlyTarget` | R1 | Export against cluster A for a name that exists only on local returns 404 from A, never local's object |
| `TestResolveGVR_PartialDiscoveryError` | cross-cutting | Partial `ServerGroupsAndResources` result still resolves a present resource (preserves the 355–361 behaviour) |
| `TestHandleValidate_LocalPathUnchanged` | regression | Local requests produce byte-identical responses to the pre-change shape apart from the two additive pin fields |

### Verification

```
cd backend && go vet ./... && go test ./...
cd backend && go test ./internal/yaml/... -race -count=1
bash scripts/check-cluster-routing.sh
cd frontend && deno task check
```

### Exit criteria — "done means"

- [ ] Step-0 commit lands first and independently builds.
- [ ] Zero `h.K8sClient.RESTMapper()` / `DiscoveryClient()` calls remain in `HandleValidate`,
      `HandleDiff`, `HandleExport`.
- [ ] The three 501 blocks for validate/diff/export are gone; the two Secret 422s are not.
- [ ] Validate and diff responses carry `targetCluster` + `targetGeneration`.
- [ ] `TestHandleValidate_RemoteDiscoveryFailureIsNotLocalFallback` asserts on the **local
      client's** call count, not on an error string.
- [ ] `applier.go`, `differ.go`, `export.go` are **untouched** — the PR diff proves correction 1.

---

## U9b — Remote YAML apply, target pinning, routing guard

**Branch:** `feat/u9b-remote-yaml-apply`
**PR title:** `feat(yaml): remote server-side apply with enforced target pinning`
**Covers:** R1, R9 (write half); AE2 (server half). **Depends on:** U9a.

### Files (4)

1. `backend/internal/yaml/handler.go`
2. `backend/internal/server/server.go` — Step-0 commit only
3. `backend/internal/yaml/remote_apply_test.go` — **new**
4. `scripts/check-cluster-routing.sh`

The lint extension is placed **here, not in U12**, deliberately: it is the structural enforcement
of D6, and putting it in a later PR would leave a window in which the last local-mapper call is
gone but the guard against reintroducing it does not yet exist.

### Agent Directive 1 (Step 0)

After step 1 below, `Handler.K8sClient` has no remaining reader in the package. Remove the field
(`handler.go:25`) and the `K8sClient: deps.K8sClient,` line (`server.go:217`) as a separate commit,
ordered **after** the feature commit so the build is green at every commit.

### Implementation steps

1. **`HandleApply` target resolution** (anchor: lines 139–154). `RouterFor` → `TargetFor`; delete
   the `!pair.IsLocal` 501 (147–152); `h.K8sClient.RESTMapper()` (154) → `target.Mapper`.

2. **Step-0 commit** (see above) removing `Handler.K8sClient` and its `server.go` initialiser.

3. **Pin parsing and enforcement** — a small helper placed next to `readYAMLBody` (anchor: after
   `handler.go:346`):
   ```go
   // targetPin is the client's declaration of which cluster it reviewed.
   // Absent fields mean "unpinned" and preserve the pre-Release-C contract for
   // existing clients (mobile included).
   type targetPin struct{ ClusterID, Generation string }

   func parseTargetPin(r *http.Request) targetPin

   // enforceTargetPin returns false (and has written a 409) when the pin
   // disagrees with the resolved target. MUST be called before any document is
   // applied.
   func enforceTargetPin(w http.ResponseWriter, pin targetPin, target *k8s.TargetSchema) bool
   ```
   Call site: in `HandleApply`, immediately after `TargetFor` succeeds and **before**
   `ApplyDocuments` (anchor: between the new target resolution and line 156). Mismatch responses
   use `httputil.WriteErrorWithReason` with reasons `cluster_pin_mismatch` /
   `cluster_generation_mismatch` and an `extra` map naming both values.

4. **Audit.** The per-document audit loop (162–179) already records `auditClusterID := clusterID`
   from the request context (F#6). Add one audit entry for a **rejected** pin mismatch, with
   `Result: audit.ResultFailure` and `Detail: "cluster_pin_mismatch"` — a refused remote apply is
   exactly the event an operator wants in the audit trail, and today a 409 would leave no record.

5. **`scripts/check-cluster-routing.sh`** — extend the match `case` (anchor: line 89) to:
   ```sh
   *".ClientForUser("*|*".DynamicClientForUser("*|*".RESTMapper()"*|*".DiscoveryClient()"*)
   ```
   and update the header comment (lines 4–14) and the fix hint (lines 153–154) to name
   `ClusterRouter.TargetFor` / `TargetSchemaFor` as the replacement. `ALLOWED_PREFIXES` (line 45)
   already exempts `client.go`, `cluster_router.go`, `informers*` and `cluster_prober.go`, which is
   exactly the set that legitimately calls the two new patterns. **Verify by running the script
   locally and confirming zero violations before pushing** — if any other package calls
   `.RESTMapper()` legitimately, it needs a `// nolint:cluster-routing <reason>` line, and that
   line belongs in this PR.

   The CI gate remains `warn` (`.github/workflows/ci.yml:63`); flipping it to `fail` is a
   repo-wide decision beyond Release C's scope and is listed under Risks.

### Named tests (`remote_apply_test.go`)

| Test | Master-plan scenario | Asserts |
|---|---|---|
| `TestHandleApply_RemoteUsesTargetMapper` | AE2 core | A remote-only CRD's CR applies against the target |
| `TestHandleApply_CRDAndCRInOneBundle` | "CRD+CR bundle" | CRD document applies, then the CR resolves on retry via the injected mapper's invalidation |
| `TestHandleApply_PinMismatchAbortsBeforeAnyMutation` | **AE2** | `targetCluster=A`, header `B` → 409, reason `cluster_pin_mismatch`, and the fake dynamic clients for **both** A and B record zero actions |
| `TestHandleApply_GenerationMismatchAborts` | AE2 / KTD3 | 409, reason `cluster_generation_mismatch`, zero actions |
| `TestHandleApply_UnpinnedRequestStillWorks` | R4 (mobile compat) | No pin params → applies on the header cluster, exactly as before |
| `TestHandleApply_RemoteUnreachableDoesNotTouchLocal` | "never falls back to local execution" | D6 mechanism 4 — local fake records zero actions |
| `TestHandleApply_AdmissionDenialIsPerDocument` | "admission denial" | One doc fails with an `Invalid` status error, siblings still apply; `summary.failed == 1` |
| `TestHandleApply_FieldConflictWithoutForce` | "field conflict" | `IsConflict` → `action: "failed"` with the "Use force to override" message; with `?force=true` → applies |
| `TestHandleApply_PartialApplyReportsAllDocuments` | "partial apply" | 3 docs, 1 failure: all three `results` entries present, `summary.total == 3` |
| `TestHandleApply_SecretIsAppliedNotRefused` | corrected scenario | A Secret document applies (apply is deliberately not Secret-refusing, unlike diff/export) — guards against someone "harmonising" the three handlers |
| `TestHandleApply_PinMismatchIsAudited` | cross-cutting | The audit logger receives one failure entry with `Detail == "cluster_pin_mismatch"` |

### Verification

```
cd backend && go vet ./... && go test ./...
cd backend && go test ./internal/yaml/... -race -count=1
bash scripts/check-cluster-routing.sh                                   # must report 0 with the widened pattern
CHECK_CLUSTER_ROUTING_GATE=fail bash scripts/check-cluster-routing.sh   # local proof it would gate
cd frontend && deno task check
```

### Exit criteria — "done means"

- [ ] All four YAML verbs work against a remote target using that target's discovery.
- [ ] `internal/yaml` contains **zero** references to `k8s.ClientFactory`.
- [ ] A pin mismatch aborts with 409 before any document reaches `ApplyDocuments`, and both
      clusters' fakes prove zero mutations.
- [ ] A rejected pin is audited.
- [ ] The routing guard matches `.RESTMapper()` / `.DiscoveryClient()` and reports zero violations
      with no new `nolint` lines outside the pre-existing `ALLOWED_PREFIXES`.
- [ ] Unpinned legacy requests behave exactly as before (mobile compatibility, R4).

---

## U10 — Remote core dashboard summary

**Branch:** `feat/u10-remote-dashboard-summary`
**PR title:** `feat(dashboard): remote cluster summary with explicit per-section coverage`
**Covers:** R3, R10; AE3. **Depends on:** U7.

### Files (3)

1. `backend/internal/k8s/resources/dashboard.go`
2. `backend/internal/k8s/resources/dashboard_remote.go` — **new**
3. `backend/internal/k8s/resources/dashboard_remote_test.go` — **new**

Matches the master plan. `health.go` is deliberately **not** touched: the pure function it holds
is correct for local and must simply not be called remotely.

### Agent Directive 1 (Step 0) and Directive 7

`dashboard.go` is 832 LOC — above the 300-LOC structural-refactor threshold and above the 500-LOC
chunked-read threshold. Read it in chunks (1–200, 195–300, 500–700, 700–832; those ranges produced
the seam table above). Run the Step-0 scan; the reading above found no dead props, unused exports,
or debug logs in this file — record that in the PR and skip the cleanup commit.

### Mobile-compatibility decision (correction 9)

`mobile/lib/features/dashboard/dashboard_repository.dart` raises `DashboardLocalOnlyError` by
matching **HTTP 400 plus the literal substring `"local cluster"`**. Turning that 400 into a 200
would silently change mobile behaviour and violate R4.

**Decision:** the remote path is **opt-in**. `GET /v1/cluster/dashboard-summary?coverage=1`
returns the coverage-bearing 200; without the parameter, the existing 400 at `dashboard.go:542–547`
is preserved verbatim, message included. Web (U11c) sends `coverage=1`; mobile continues to get
its 400 until its own parity milestone chooses to opt in. This is additive, reversible, and needs
no mobile change in Release C.

### Implementation steps

1. **Extract the pure aggregators** in `dashboard.go`, verbatim from the ranges in the seam table:
   - `aggregateCounts(nodes, pods, serviceCount)` from lines 573–602;
   - `capacityTotals` + `aggregateCapacity(nodes, pods)` from lines 615–646;
   - `utilizationFrom(t capacityTotals, kind resourceKind, pct *float64) *Utilization` from lines
     648–667 plus the fallback shape at 748–767.

   Rewrite `HandleDashboardSummary` to call them. **Behaviour must be bit-identical** for local —
   the existing dashboard tests are the regression gate, and the extraction is a pure code move.

2. **Add coverage to the response type** (anchor: the `DashboardSummary` struct at
   `dashboard.go:23–31`): `Coverage []SectionCoverage` tagged `json:"coverage,omitempty"`.
   `omitempty` keeps the local response byte-identical.

3. **Dispatch** (anchor: replace lines 542–547):
   ```go
   clusterID := middleware.ClusterIDFromContext(r.Context())
   if !k8s.IsLocalClusterID(clusterID) {
       if r.URL.Query().Get("coverage") != "1" {
           // Unchanged 400 — mobile's DashboardLocalOnlyError depends on this
           // exact status + message. See the Release C plan, decision D5.
           writeError(w, http.StatusBadRequest, "dashboard summary is only available for the local cluster", "")
           return
       }
       h.handleRemoteDashboardSummary(w, r, user, clusterID)
       return
   }
   ```

4. **`dashboard_remote.go`** — `SectionCoverage`, the status constants, and:
   ```go
   func (h *Handler) handleRemoteDashboardSummary(w http.ResponseWriter, r *http.Request, user *auth.User, clusterID string)
   ```
   Acquisition uses `h.ClusterRouter.TargetFor` and **direct API lists, never informers**
   (informers are local-only by construction — CLAUDE.md, "Informers for read (local cluster
   only)"). Per section, in a bounded `errgroup` sharing one deadline:
   - `nodes`: SAR `list nodes` → `pair.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500})`
     (the same 500 cap the prober already uses at `cluster_prober.go:201`, for the same reason);
     a `Continue` token in the response → `status: "partial"`.
   - `pods`: SAR `list pods` → `CoreV1().Pods("").List(...)` with a limit; forbidden → `forbidden`.
   - `services`: SAR + `CoreV1().Services("").List(...)`.
   - `cpu`/`memory`: computed by `aggregateCapacity` from the node/pod lists; percentage is
     **always `nil`** remotely (`status: "unavailable"`, reason `unsupported_platform`, detail
     naming the deferred remote-metrics binding), so the response carries requests/limits/total but
     never a fabricated `percentage`.
   - `alerts`: `status: "unavailable"`, reason `unsupported_platform` (the `AlertCounter` is bound
     to the local Alertmanager).
   - `health`: `nil`, `status: "unavailable"`, reason `unsupported_platform`.

   **`computeClusterHealth` is not called anywhere in this file.** See D5 for why, and the test
   below for how that is enforced.

5. **recoverutil.** If step 4 uses `errgroup`, every worker goes through
   `recoverutil.Go(g, h.Logger, "resources remote dashboard <section>", fn)` per
   `docs/solutions/backend-resilience-conventions.md`. There are no `wg.Done()` calls and no
   counted channel sends in the proposed shape, so the cleanup hazard does not apply — but if the
   implementation reaches for a `sync.WaitGroup` + result channel instead (mirroring
   `gatherHealthInputs` at `dashboard.go:265–272` and 674–684), then `wg.Done()` and the send must
   sit **outside** the wrapped closure. Prefer `errgroup`; it makes the hazard structurally absent.

   Note for the reviewer: `dashboard.go:265` and `dashboard.go:676–684` are pre-existing
   unrecovered goroutines. They are **out of scope** for U10 (they are on the local path and U10
   must not change local behaviour); they are recorded under Risks.

### Named tests (`dashboard_remote_test.go`)

| Test | Master-plan scenario | Asserts |
|---|---|---|
| `TestRemoteSummary_CountsWithoutMetrics` | **AE3** | Node/pod/service counts present; `cpu.percentage`/`memory.percentage` absent; coverage rows `cpu`/`memory` are `unavailable` + `unsupported_platform` |
| `TestRemoteSummary_NeverSynthesizesHealth` | **AE3** | Matrix over every combination of section success/failure — including all-succeed — asserts `Health == nil` and the `health` coverage row is `unavailable` in **every** case |
| `TestRemoteSummary_PartialListPermissions` | "Partial list permissions" | Pods forbidden, nodes allowed → `pods` row `forbidden`, `nodes` row `ok`, no error status on the response |
| `TestRemoteSummary_OneSectionTimeoutDoesNotFailOthers` | "one failed section does not fail unrelated sections" | Services times out → `services` row `unavailable`, nodes/pods still `ok` |
| `TestRemoteSummary_EmptyCluster` | "empty cluster" | Zero nodes → counts are zero, `nodes` row is `ok` (not `unavailable`); no health score |
| `TestRemoteSummary_TruncatedListIsPartial` | R3 | A `Continue` token → `nodes` row `partial` |
| `TestRemoteSummary_StaleCarriesObservedAt` | R3 | `stale` rows always carry a non-empty `observedAt` |
| `TestRemoteSummary_NeverUsesInformers` | R10 "local metrics must never be substituted" | Informer stubs seeded with distinct values; assert none of those values appear in the remote response |
| `TestDashboardSummary_LocalUnchangedWithoutCoverageParam` | R4 / mobile | Remote + no `?coverage=1` → the exact 400 and the exact message mobile matches on |
| `TestAggregateCounts_Pure` / `TestAggregateCapacity_Pure` | extraction regression | Table-driven over the same fixtures the existing local dashboard tests use; the extraction changed no arithmetic |

### Verification

```
cd backend && go vet ./... && go test ./...
cd backend && go test ./internal/k8s/resources/... -race -count=1
bash scripts/check-cluster-routing.sh
```

### Exit criteria — "done means"

- [ ] The counts/capacity aggregation is three pure functions with no informer, SAR, clock or
      network dependency, called by both paths.
- [ ] Local responses are unchanged, including the 400 message mobile depends on.
- [ ] The remote response is opt-in behind `?coverage=1`.
- [ ] `Health` is `nil` on every remote response, proven by an all-succeed test case.
- [ ] `computeClusterHealth` appears nowhere in `dashboard_remote.go` (grep in the PR body).
- [ ] Any goroutine introduced uses `recoverutil`; the choice of `errgroup` over `WaitGroup` is
      stated in the PR description.

---

## U11a — Cluster switcher and switch-safe request targeting

**Branch:** `feat/u11a-cluster-switcher`
**PR title:** `feat(frontend): cluster switcher with per-request target pinning`
**Covers:** R3, R8 (client foundation). **Depends on:** U8.

### Split justification

The master plan's U11 bundles a capability client, the YAML gate, dashboard rendering and an e2e
spec into five files. Three problems: (i) it names `YamlApplyPage.tsx` when the state machine is
in `lib/yaml-apply.ts`; (ii) the two files that actually carry the AE2 race — `lib/api.ts` and
`lib/yaml-apply.ts` — are not in the list at all; (iii) **there is no cluster switcher in the
product** (correction 3), so none of it is reachable. Three units.

### Files (5)

1. `frontend/lib/cluster.ts`
2. `frontend/lib/api.ts`
3. `frontend/islands/ClusterSwitcher.tsx` — **new**
4. `frontend/islands/TopBarV2.tsx`
5. `frontend/lib/cluster-targeting_test.ts` — **new** (covers both `cluster.ts` and `api.ts`
   pinning; one test file because the two behaviours are one contract and the cap is five)

### Implementation steps

1. **`cluster.ts`** — add alongside the existing `selectedCluster` signal (anchor: line 13):
   ```ts
   /** Generation of the currently selected cluster, as reported by the API. */
   export const selectedClusterGeneration = signal<string>("local");

   /** Monotonic counter bumped on every switch. Consumers compare a captured
    *  value against the current one to discard responses from a prior target. */
   export const clusterEpoch = signal(0);

   /** The only sanctioned way to change the active cluster. */
   export function switchCluster(id: string, generation: string): void;

   /** Immutable snapshot of the current target, captured at request-issue time. */
   export interface ClusterTarget { clusterId: string; generation: string; epoch: number }
   export function currentTarget(): ClusterTarget;
   ```
   `switchCluster` writes both signals and increments `clusterEpoch` — increment **last**, so a
   subscriber woken by the epoch always reads a consistent id+generation pair.

2. **`api.ts` — freeze the target per call.** This is the AE2 race fix. Anchor: `api()` at line
   133 and `doFetch` at 137.
   ```ts
   export async function api<T>(
     path: string,
     options: RequestInit & { signal?: AbortSignal; clusterId?: string } = {},
   ): Promise<APIResponse<T>>
   ```
   Capture `const targetCluster = options.clusterId ?? selectedCluster.value;` **once, outside
   `doFetch`**, and have `doFetch` set `headers.set("X-Cluster-ID", targetCluster)` (replacing line
   142). This single move fixes the concrete defect: today the 401-refresh retry at line 172 calls
   `doFetch()` a second time and **re-reads the signal**, so a cluster switch landing during a
   token refresh silently retargets the retried request.

3. **`api.ts` — cancellable raw posts.** `apiPostRaw` (anchor: line 231) gains
   `opts?: { signal?: AbortSignal; clusterId?: string }` and forwards both. Existing call sites
   pass nothing and are unaffected. Add the same optional `clusterId` to `apiGet` / `apiPost`.

4. **`ClusterSwitcher.tsx`** — an island that lists `GET /v1/clusters` (the shape
   `ClusterManager.tsx:67` already consumes), renders the current selection, and calls
   `switchCluster(c.id, c.createdAt)` on choose. Non-admin users receive 403 from the admin-only
   `/clusters` route; the island renders "local" as the only option in that case rather than an
   error — an operator without cluster-management rights still has a working product. Keyboard
   accessible (listbox semantics, `aria-activedescendant`), which the e2e spec in U12 relies on.

5. **`TopBarV2.tsx`** — replace the static `{selectedCluster.value}` text (anchor: line 98) with
   `<ClusterSwitcher />`.

### Named tests (`cluster-targeting_test.ts`)

Deno tests with a stubbed `globalThis.fetch`.

| Test | Scenario | Asserts |
|---|---|---|
| `switchCluster bumps epoch after writing id and generation` | ordering invariant | Reading both signals from an epoch subscriber yields the new pair |
| `api pins X-Cluster-ID at call entry` | — | Header equals the value at issue time |
| `api retry after 401 reuses the original cluster` | **the concrete defect** | Stub returns 401 then 200; `switchCluster` is called between them; both requests carry the **original** id |
| `api honours an explicit clusterId over the signal` | pinning | Explicit wins |
| `apiPostRaw forwards signal and clusterId` | — | AbortController aborts the request; header is the pinned value |
| `currentTarget is a snapshot, not a live view` | — | Mutating the signal after capture does not change the captured object |
| `selectedCluster survives a reload via localStorage` | AE1-adjacent | Round-trip |

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
cd backend && go vet ./... && go test ./...   # unchanged; run to prove no drift
```

### Exit criteria — "done means"

- [ ] An operator can change the active cluster from the top bar.
- [ ] `X-Cluster-ID` is decided once per `api()` call and cannot change on the 401 retry path.
- [ ] `apiPostRaw` is cancellable and pinnable.
- [ ] `resource-counts.ts`'s existing cluster-reactive effect (line 82) now actually fires on a
      switch — confirm manually that it aborts the prior fetch rather than racing (no code change
      expected; if it races, that is a U11c follow-up, recorded not silently fixed).
- [ ] No e2e spec in this PR (see U12).

---

## U11b — Capability client and YAML target pin/gate

**Branch:** `feat/u11b-yaml-capability-gate`
**PR title:** `feat(frontend): capability-aware YAML editor with pinned apply targets`
**Covers:** R3, R8, R9; AE2 (client half). **Depends on:** U11a, U9b.

### Files (5)

1. `frontend/lib/capabilities.ts` — **new**
2. `frontend/lib/capabilities_test.ts` — **new**
3. `frontend/lib/yaml-apply.ts`
4. `frontend/lib/yaml-apply_test.ts` — **new**
5. `frontend/islands/YamlApplyPage.tsx`

`frontend/lib/capability-types.ts` arrives in U8 and is imported, not edited.
**`frontend/islands/SecretStoreFromTemplateEditor.tsx` (the second `useYamlApply` consumer, line
39) is deliberately not touched** — the hook change is a new *optional* option plus additive return
fields, so that island keeps compiling and behaving identically. State this in the PR; a reviewer
should check it, because a required option would have silently broken the ESO template editor.

### Implementation steps

1. **`capabilities.ts`** — `fetchCapabilities(target: ClusterTarget, signal?: AbortSignal)` calling
   `apiGet("/v1/capabilities/" + target.clusterId, { clusterId: target.clusterId })`. **The
   explicit `clusterId` is mandatory**, because U8's handler 409s on header/path disagreement and
   the ambient signal may have moved. A module-level `Map` keyed by `clusterId + "\0" + generation`
   caches the response with the server's `expiresAt`; entries are dropped on `clusterEpoch` change
   and on expiry. Expose `capabilityFor(caps, operationId): Capability | undefined` and a small
   `explain(cap): { tone: "ok"|"blocked"|"unsupported"|"unknown"; message: string }` that maps the
   eleven reason codes to operator-facing copy — and, per D3, maps `unreachable`/`forbidden` to
   `"blocked"`, never to `"unsupported"`.

2. **`yaml-apply.ts` — pin the flow.** Anchor: the `useYamlApply` state block at lines 70–74 and
   the two handlers at 76–112.
   - Add `const pin = useSignal<ClusterTarget | null>(null)` and
     `const pinStale = useSignal(false)`.
   - `handleValidate` captures `currentTarget()` at issue time, passes `clusterId` to
     `apiPostRaw`, and on success sets `pin.value` from the response's `targetCluster` /
     `targetGeneration` (U9a's additive fields). A response whose captured epoch no longer matches
     `clusterEpoch.value` is **discarded**, not rendered.
   - `handleApply` refuses to run when `pin.value === null` (no reviewed target) and otherwise
     sends `?targetCluster=&targetGeneration=` plus `clusterId: pin.value.clusterId`. It reads
     **the pin**, never `selectedCluster.value`.
   - A `useSignalEffect` on `clusterEpoch` sets `pinStale.value = true` when the epoch moves past
     the pin's epoch. Stale means "the UI has moved on"; the pin itself is unchanged.
   - Add an `AbortController` per in-flight request so a switch cancels rather than lands late.
   - Return the new `pin`, `pinStale`, and a `clearPin()` alongside the existing fields
     (additive — `SecretStoreFromTemplateEditor` ignores them).

3. **`YamlApplyPage.tsx`** — render three new things and nothing else:
   - a capability banner above the editor, resolved **before data entry** (the master plan's "a
     remote unsupported action is explained before data entry"), using `explain()` copy;
   - the pinned target next to the Apply button ("Applies to **prod-eu** — previewed 2m ago");
   - when `pinStale.value`, a non-blocking notice "You are now viewing **staging**. Apply still
     targets **prod-eu**." with a "Re-preview on staging" action that calls `clearPin()` and
     re-validates. Apply stays enabled and stays pinned — never silently retargeted.

### Named tests

`capabilities_test.ts`:

| Test | Scenario | Asserts |
|---|---|---|
| `sends explicit clusterId so header and path agree` | U8's 409 | Request URL path id equals the `X-Cluster-ID` header |
| `caches by cluster and generation` | "capability expiry forces fresh checking" | Second call for the same pair does not refetch; a different generation does |
| `expiry forces a refetch` | same | Past `expiresAt` → refetch |
| `epoch change drops the cache` | AE2-adjacent | Switch → next read refetches |
| `unreachable renders as blocked, not unsupported` | **D3, AE-relevant** | `explain()` tone is `"blocked"` |
| `forbidden renders as blocked, not unsupported` | same | tone `"blocked"` |
| `unsupported_platform renders as unsupported` | same | tone `"unsupported"` |
| `unknown reason code does not throw` | robustness | Falls back to tone `"unknown"` |

`yaml-apply_test.ts`:

| Test | Scenario | Asserts |
|---|---|---|
| `apply sends the pinned cluster after a switch` | **AE2** | Validate on A, `switchCluster(B)`, apply → request carries `targetCluster=A` and `X-Cluster-ID: A` |
| `apply is disabled with no pin` | AE2 | No preview → apply refuses locally |
| `pinStale is set by an epoch change` | AE2 | Flag set; pin value unchanged |
| `clearPin forces a fresh preview` | AE2 | Apply refuses until a new validate succeeds |
| `late validate response from a prior epoch is discarded` | "Rapid cluster switching cannot … apply an old preview" | Slow A response resolving after a switch to B does not set the pin |
| `a 409 cluster_pin_mismatch surfaces its reason` | U9b contract | `ApiError.reason === "cluster_pin_mismatch"`; the UI message names both clusters |
| `hook remains backward compatible` | regression | Calling `useYamlApply(initial)` with no options returns every pre-existing field with pre-existing behaviour |

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
```

### Exit criteria — "done means"

- [ ] Capability state is fetched with an explicit cluster id and never 409s on its own header.
- [ ] `unreachable` / `forbidden` / `unsupported_platform` render as three visibly different
      states.
- [ ] Apply is pinned to the previewed target; a cluster switch produces a visible notice and
      **never** retargets the apply.
- [ ] A late response from a prior epoch is discarded rather than rendered.
- [ ] `SecretStoreFromTemplateEditor.tsx` is not in the diff and its behaviour is unchanged.
- [ ] No e2e spec in this PR (see U12).

---

## U11c — Remote dashboard coverage rendering

**Branch:** `feat/u11c-dashboard-coverage`
**PR title:** `feat(frontend): per-section coverage on the remote dashboard`
**Covers:** R3, R10; AE3 (client half). **Depends on:** U11a, U10.

### Files (4)

1. `frontend/lib/dashboard-coverage.ts` — **new**
2. `frontend/lib/dashboard-coverage_test.ts` — **new**
3. `frontend/islands/DashboardV2.tsx`
4. `frontend/lib/resource-counts.ts`

### Implementation steps

1. **`dashboard-coverage.ts`** — the `SectionCoverage` type (mirroring U10) plus
   `coverageFor(summary, section)` and `sectionTone(cov)` mapping
   `ok | partial | unavailable | forbidden | stale` to render tones. Also
   `shouldRenderHealth(summary): boolean` — **`false` whenever `summary.health` is null or its
   coverage row is not `ok`**.

2. **`DashboardV2.tsx`:**
   - Extend the island-local `DashboardSummary` interface (anchor: lines 35–54) with
     `coverage?: SectionCoverage[]`.
   - `fetchSummary` (anchor: line 102) appends `?coverage=1` when the target is non-local, and
     passes `clusterId` from a captured `currentTarget()`.
   - **Fix the health-rendering defect at lines 257–263.** Today `health?.score ?? 0` renders a
     **zero score** — a concrete, confident, wrong number — when health is absent. Replace with an
     explicit unknown state driven by `shouldRenderHealth`: no gauge, no number, and the coverage
     row's reason as the explanation. This defect exists today for local; the remote path makes it
     reachable constantly, which is why it is fixed here rather than deferred.
   - Add a cluster-reactive effect: the mount effect's dependency array is empty (anchor: line
     189). Add a `useSignalEffect` on `clusterEpoch` that aborts the in-flight controller, clears
     `summary` / `trends`, and reloads. Without this the dashboard shows the previous cluster's
     numbers under the new cluster's name — the exact leak AE3's sibling scenario describes.
   - Render each unavailable section in place (an em-dash plus the reason), never a zero.

3. **`resource-counts.ts`** — `GET /v1/resources/counts` returns **400** for a remote cluster
   (`counts.go:27–31`). Treat that specific 400 as "unavailable on this target" and publish a
   `countsUnavailableReason` signal rather than letting an error propagate. This is a small change
   to the existing fetch's catch, and it is required the moment U11a's switcher exists.

### Named tests (`dashboard-coverage_test.ts`)

| Test | Scenario | Asserts |
|---|---|---|
| `shouldRenderHealth is false for null health` | **AE3** | — |
| `shouldRenderHealth is false when the health coverage row is not ok` | AE3 | Even if a score is somehow present |
| `null health never renders as zero` | **the fixed defect** | The formatter returns an unknown marker, not `"0"` |
| `partial renders differently from unavailable` | R3 | Distinct tones |
| `forbidden renders differently from unavailable` | R3 | Distinct tones |
| `stale carries and formats observedAt` | R3 | Non-empty relative time |
| `unknown status falls back safely` | robustness | No throw |
| `counts 400 on remote maps to unavailable` | R3 | Reason string, not an error |

### Verification

```
cd frontend && deno task check
cd frontend && deno task test
cd frontend && deno task build
```

### Exit criteria — "done means"

- [ ] Switching clusters clears and reloads the dashboard; prior-cluster numbers never persist
      under the new cluster's name.
- [ ] Absent health renders as unknown, never as `0`.
- [ ] `ok`, `partial`, `unavailable`, `forbidden` and `stale` are five visibly distinct states.
- [ ] Resource counts degrade to "unavailable on remote" rather than an error.
- [ ] No e2e spec in this PR (see U12).

---

## U12 — Two-cluster fixture, live-run runbook, documentation

**Branch:** `feat/u12-remote-two-cluster-evidence`
**PR title:** `test(e2e): two-cluster remote fixture and operation support matrix`
**Covers:** R1–R4, R8–R10; AE2, AE3. **Depends on:** U11b, U11c.

### Files (5)

1. `e2e/remote-kind-config.yaml` — **new**
2. `scripts/test-remote-capabilities.sh` — **new**
3. `e2e/tests/remote-capabilities.spec.ts` — **new**
4. `README.md`
5. `CLAUDE.md`

`e2e/playwright.config.ts` is **not** touched: `testDir: ./tests` already collects
`tests/*.spec.ts` into the `chromium` project, so the new spec is picked up with no config change.
That is what lets the spec land while the live run stays gated.

### Agent-executable deliverables (land in this PR)

1. **`e2e/remote-kind-config.yaml`** — a second single-control-plane kind cluster, same shape as
   `e2e/kind-config.yaml`, with a distinct `name:` and a distinct API-server port so both clusters
   can run side by side.

2. **`scripts/test-remote-capabilities.sh`** — POSIX `sh`, matching the style of
   `scripts/check-cluster-routing.sh` (`set -eu`, `ROOT` resolution, temp files with a `trap`
   cleanup). It:
   - creates the second kind cluster from `remote-kind-config.yaml`;
   - applies a **remote-only CRD** (a trivial `widgets.k8scenter.test` CRD, embedded as a heredoc
     so no extra file is needed) to the remote cluster **and not** to the local one — this is the
     "intentionally different CRDs" the master plan asks for and the object AE2 previews;
   - creates a ServiceAccount on the remote cluster with `impersonate` on users (required —
     `ProbeImpersonateRights` at `cluster_prober.go:26` fails the registration otherwise) plus a
     deliberately **narrower** role than the local identity, giving the "differing identities" axis;
   - registers the cluster through `POST /api/v1/clusters` with the real CA bundle, so
     `allowInsecureTLS` stays **false** and the fixture exercises the production TLS path;
   - prints the resulting cluster id as `K8SCENTER_REMOTE_CLUSTER_ID=<id>`;
   - takes a `--teardown` flag that deletes **only** objects carrying the fixture's own label
     (`app.kubernetes.io/managed-by=k8scenter-e2e-remote-fixture`) and then the kind cluster —
     "fixture cleanup targets only fixture-created resources", enforced by label selector rather
     than by name guessing.

3. **`e2e/tests/remote-capabilities.spec.ts`** — the **single** home for Release C's e2e coverage
   (see the conflict note below). Gated at the top:
   ```ts
   const REMOTE = process.env.K8SCENTER_REMOTE_CLUSTER_ID;
   test.describe("Remote cluster capabilities", () => {
     test.skip(!REMOTE, "requires a registered remote cluster — see scripts/test-remote-capabilities.sh");
     // …
   });
   ```
   With Q2 unresolved the suite **skips cleanly** in CI and in every developer checkout; when an
   operator answers Q2 and exports the variable, the same file runs for real. No config change, no
   second CI job, no dead code.

   | Spec | Covers |
   |---|---|
   | `capability disclosure explains an unsupported remote operation before data entry` | R8, U11b |
   | `previewing a remote-only CRD resolves against the remote cluster` | **AE2**, U7 + U9a |
   | `switching clusters after a preview keeps apply pinned to the reviewed target` | **AE2**, U11b |
   | `a remote-only object is never created on the local cluster` | D6 — asserts via a direct `kubectl --context kind-<local>` read inside an `expect.poll` |
   | `remote dashboard shows counts and an explicit metrics-unavailable state` | **AE3**, U10 + U11c |
   | `remote dashboard shows no health score` | **AE3** |
   | `deleting the cluster invalidates cached discovery` | U7 — delete, re-register, confirm the remote-only CRD resolves under the new id and 404s under the old |
   | `switching clusters clears the previous cluster's dashboard numbers` | R3, U11c |

4. **`README.md`** — replace the three-line Multi-Cluster block (lines 39–42) with the same
   summary plus an **operation-by-operation support table** whose rows are generated from — and
   must agree with — U8's `capabilityOperations` table. Columns: Operation / Local / Remote / Note.
   Every "no" cites the guard file. Explicitly retain the exec and streaming limitations.

5. **`CLAUDE.md`** — three edits:
   - Correct the stale "Known limitation: AccessChecker queries local cluster RBAC, not remote"
     (line 258) — `access.go:258–265` routes non-local SARs remotely and `main.go:437` wires it.
   - Update the Multi-Cluster section to state that YAML validate/diff/apply/export now resolve
     discovery against the target cluster via `ClusterRouter.TargetFor`, and that handlers must
     never call `LocalFactory().RESTMapper()` on a routed path.
   - Add the target-pin contract (D4) to Key Conventions, next to the existing annotation
     contracts, so the next agent editing `internal/yaml` finds it.

### Operator-gated live run (Q2 — NOT part of this PR)

Q2 ("which clusters and operations matter most for the first remote acceptance environment") is
unanswered. Source work above proceeds against fixtures; the live run is a documented manual gate.

> **Runbook — executed by a human**
>
> **Prerequisites**
> 1. Two Kubernetes clusters reachable from the k8sCenter pod. The remote cluster's API server
>    must resolve to a **public** address — `ValidateRemoteURLContext` (`cluster_router.go:507`)
>    fails closed on loopback, RFC1918, link-local, CGNAT and unspecified addresses, and
>    `StrictDialContext` re-checks on **every** dial. A homelab remote on `10.x` will be refused;
>    that is correct behaviour, not a bug to work around.
> 2. A real CA bundle for the remote API server. Do **not** set `allowInsecureTLS` — the fixture
>    exists to prove the production TLS path.
> 3. A remote ServiceAccount token with `impersonate` on `users` (`ProbeImpersonateRights`) plus
>    read access to nodes/pods/services and write access to the fixture namespace.
> 4. An admin k8sCenter account — `middleware.ClusterContext` (`cluster.go:33`) requires the admin
>    role for any non-local `X-Cluster-ID`.
>
> **Procedure**
> 1. `bash scripts/test-remote-capabilities.sh` against the chosen destination. Record the printed
>    cluster id.
> 2. `export K8SCENTER_REMOTE_CLUSTER_ID=<id>` and `cd e2e && npm test`. All eight remote specs
>    must pass, in addition to the existing suite.
> 3. Manually verify AE2 end to end: preview the remote-only `Widget`, switch the UI to the local
>    cluster, click Apply. Confirm the object is created **on the remote cluster** and that
>    `kubectl --context kind-<local> get widgets` reports the CRD does not exist.
> 4. Manually verify AE3: with no Prometheus on the remote, confirm the dashboard shows node/pod
>    counts, an explicit metrics-unavailable state, and **no** health score or gauge.
> 5. Verify eviction: delete the cluster from Settings → Clusters, re-register it, and confirm the
>    remote-only CRD resolves under the new id while the old id 404s.
> 6. `bash scripts/test-remote-capabilities.sh --teardown`. Confirm by label selector that only
>    fixture-labelled objects were removed.
>
> **Recording the result.** Append the destination's Kubernetes versions, the identity used, and
> the pass/fail of each step to the Release C section of `README.md`. Until this runbook has been
> executed once, Release C's documentation must say remote support is **verified against fixtures
> only**. Do not claim two-cluster evidence the product has not produced.

### Verification

```
cd backend && go vet ./... && go test ./...
cd frontend && deno task check && deno task test && deno task build
cd e2e && npm test                          # remote specs SKIP without K8SCENTER_REMOTE_CLUSTER_ID
bash scripts/check-cluster-routing.sh
sh -n scripts/test-remote-capabilities.sh   # syntax-check the new script
```

### Exit criteria — "done means"

- [ ] The fixture script creates a second cluster with a remote-only CRD and a narrower identity,
      registers it over real TLS, and tears down by label selector only.
- [ ] The e2e spec exists, skips cleanly with the env var unset, and needs no Playwright config
      change.
- [ ] README carries an operation-by-operation support table that agrees with U8's table, with
      exec/streaming limitations retained.
- [ ] CLAUDE.md's stale AccessChecker limitation is corrected and the pin contract is recorded.
- [ ] The live-run runbook is written as an executable human procedure with explicit
      prerequisites, and Release C's docs say "fixture-verified" until it has been run.

---

## Cross-unit sequencing and conflict notes

**Sequencing.** `U7 → {U8, U9a, U10}`; `U9a → U9b`; `U8 → U11a`; `{U11a, U9b} → U11b`;
`{U11a, U10} → U11c`; `{U11b, U11c} → U12`.

Three units can run in parallel after U7 (U8, U9a, U10) because their file sets are disjoint.
U11a / U11b / U11c must be sequential — all three touch the frontend `lib/` and U11b depends on
U11a's `currentTarget()`.

**Shared-file collisions.**

| File | Touched by | Resolution |
|---|---|---|
| `backend/internal/server/routes.go` | **U8 only** in Release C. Release A's U2 also edits it. | Single insertion point (`ar.Get("/capabilities/{clusterID}", …)` after line 115). Release A's preference routes land in the same authenticated group — a two-line textual conflict at worst. Sequence U8 before or after Release A's U2, never concurrently, and rebase rather than merge. |
| `backend/internal/server/server.go` | **U9a** (Step 0, remove `ClusterID:`) and **U9b** (Step 0, remove `K8sClient:`). Release A's U2 adds a handler field. | Both Release C edits are deletions inside the `s.YAMLHandler = &yamlpkg.Handler{…}` literal at lines 216–222; Release A's is an addition to the `Server` struct and `Deps`. Textually disjoint. U9a must merge before U9b (both edit the same literal). |
| `backend/cmd/kubecenter/main.go` | **Zero times in Release C.** | The discovery cache lives inside `ClusterRouter`, so no new DI and no new evict-hook registration. This is a deliberate design outcome, not an oversight — record it in the U7 PR so a reviewer does not go looking for missing wiring. |
| `frontend/islands/DashboardV2.tsx` | **U11c** in Release C; **Release A's U6** (dashboard layouts) also edits it. | Hard conflict — U6 restructures widget ordering while U11c changes fetch, health rendering and adds a cluster-reactive effect. **Sequence U11c strictly before Release A's U6**, and make U6's plan aware that `shouldRenderHealth` and the `clusterEpoch` effect are load-bearing. If U6 must go first, U11c rebases onto it; do not attempt a merge of two large edits to a 1,084-line island. |
| `frontend/lib/cluster.ts` | **U11a only.** | Release A's saved views bind a cluster; they should consume `currentTarget()` rather than re-reading `selectedCluster`. Note it in Release A's plan. |
| `frontend/lib/api.ts` | **U11a only** in Release C; Release A's U3 adds preference helpers. | U11a changes the `api()` signature additively (`clusterId?`); U3 appends new exported consts at the bottom. Disjoint. Land U11a first so U3's helpers can pin. |
| `frontend/lib/yaml-apply.ts` | **U11b only.** | Second consumer `SecretStoreFromTemplateEditor.tsx` is not edited; the change must stay backward compatible. Asserted by `hook remains backward compatible`. |
| `e2e/tests/remote-capabilities.spec.ts` | Master plan marks it **new in both U11 and U12**. | **Resolved: it is created once, in U12, and only in U12.** U11a / U11b / U11c ship deno unit tests only, and their exit criteria say so explicitly. Reason: the spec cannot pass without the fixture, and a spec landing in U11 would have to be `test.skip`-ed for three PRs and then rewritten in U12 — churn with no coverage. |
| `scripts/check-cluster-routing.sh` | **U9b only.** | Master plan implies U12; moved to U9b so no window exists in which the last local-mapper call is gone but the guard against reintroducing it is not. |
| `README.md`, `CLAUDE.md` | **U12 only.** | Release A/B also touch both. Docs conflicts are cheap; sequence by merge order. |

---

## Deferred appendix — clearly out of Release C

These are requirements-only, exactly as the master plan's "Remote Metrics, Terminal, and Fleet
Comparison" milestone frames them. Each names the specific discovery it needs before it can be
planned into units. **Nothing here is authorised by this document.**

### DA1. Remote metrics binding

Requirement: a remote dashboard's `cpu` / `memory` percentages and per-resource metrics must come
from the **target's** monitoring, never from whichever Prometheus happens to answer.

Discovery needed before planning: (a) whether `monitoring.Discoverer` can be instantiated
per-cluster at all, or whether it assumes in-cluster service discovery; (b) whether the deployment
model is one Prometheus per cluster (needs a per-cluster endpoint in the registry — a schema
change, therefore a migration) or one federated Prometheus with a `cluster` label (needs a verified
label mapping and a proof that the label is trustworthy); (c) how `SafeDialContext` versus
`StrictDialContext` should apply to a remote monitoring endpoint, given that `checkIPAlwaysBad`
deliberately permits RFC1918 for in-cluster services (`cluster_router.go:587`) while
`checkIPNotPrivate` does not. Tests must include overlapping namespace/pod names across two
clusters. Until this lands, U10's `cpu` / `memory` coverage rows stay
`unavailable / unsupported_platform` and `health` stays `nil`.

### DA2. Terminal and streaming parity

Requirement: remote pod exec, log tail, log search and flow streams.

Discovery needed: the four guards (`pods.go:156`, `handle_ws_logs.go:103`,
`handle_ws_logs_search.go:60`, `handle_ws_flows.go:61`) each sit on a WebSocket upgrade path where
`WSClusterContext` (`middleware/cluster.go:56`) **deliberately skips the admin gate** because
identity is not in context at middleware time. Before enabling any of them, establish: how the
admin gate is applied post-upgrade; whether the impersonating remote `rest.Config` survives an
SPDY/WebSocket upgrade through `StrictDialContext`; what happens to a live stream when the
operator's authorization is revoked mid-stream; and cancellation semantics on cluster switch. Flow
streaming additionally depends on Hubble gRPC, which has no remote transport today.

### DA3. Fleet comparison

Requirement: bounded multi-cluster reads showing each cluster's observed revision, image,
configuration differences, health and unreachable state.

Discovery needed: a per-target concurrency and deadline budget derived from measurement (the
master plan's "measure existing request latency and API call volume before adding remote
aggregation"), and a decision on whether the 128-entry schema cache cap is adequate when one
request touches N clusters at once — a fleet read with 20 clusters × 1 identity is 20 entries per
request and will thrash a 128-entry cache under concurrent operators.

### DA4. Campaign execution

Requirement: per-target previews, concurrency control, pause/abort, partial outcomes and
controller-specific rollback for a change applied across many clusters.

Discovery needed: this is a durable-operation problem, not a routing problem. It depends on
Release E's operation-intent record (KTD9) existing first. Explicitly **not** inheriting
ApplicationSet semantics. Note that D4's pin is per-request and per-cluster; a campaign needs a
pin per target plus a campaign-level identity, which is a different contract.

---

## Risks and open items

| # | Risk | Why it matters | How this plan prevents or contains it |
|---|---|---|---|
| R-1 | **Cross-identity discovery leakage.** Sharing a discovery cache entry between identities would let identity A learn (and act on) identity B's API surface. | Discovery on remote is impersonated; a hardened cluster can restrict it. | D1 keys the cache on `cacheKey(username, groups)` — the same sha256 the client caches use. `TestSchemaCache_IdentityIsolation` asserts it. The 128-entry cap is a *bound*, never a reason to widen the key. |
| R-2 | **Silent local fallback on remote failure.** The worst possible outcome of this release: an operator believes they applied to prod-eu and actually mutated the local cluster. | Direct wrong-cluster-mutation / data-loss risk. | D6's four mechanisms: `TargetSchema` type, widened lint, pointer-inequality tests, and `TestHandleApply_RemoteUnreachableDoesNotTouchLocal` asserting **zero actions on the local fake**. |
| R-3 | **SSRF / DNS-rebinding regression.** A second remote-`rest.Config` builder would inevitably omit `ValidateRemoteURL`, `StrictDialContext` or `applyClusterTLS`. | Would reopen P2-6 and F#5. | U7 reuses `remoteConfig` **verbatim**; the exit criteria include a grep proving no new call to `store.Decrypt` / `ValidateRemoteURL` / `applyClusterTLS` was added anywhere in Release C. |
| R-4 | **Pre-existing:** `ClusterProber.ProbeOne` builds its `rest.Config` (`cluster_prober.go:159–168`) **without** `Dial: StrictDialContext`, unlike `buildRemoteConfig` (line 423). It validates the URL first, so there is a validate→dial window. | A rebinding flip between validation and dial could let a probe reach a private endpoint. | Out of Release C scope (U8 only *reads* probe results). Recorded here so it is not lost; the fix is a one-line `Dial:` addition and belongs in its own security PR. |
| R-5 | **Pre-existing unrecovered goroutines** at `dashboard.go:265–272` and `dashboard.go:676–684` (raw `go func()` inside `gatherHealthInputs` / `HandleDashboardSummary`), and — until U7 — `cluster_router.go:233` and `client.go:225`. | A panic on a non-request stack crashes the process. | U7 fixes `cluster_router.go:233` because it adds work to that loop. The dashboard ones are on the **local** path and U10 must not change local behaviour, so they are recorded, not silently touched. `client.go:225` is untouched by Release C. |
| R-6 | **Master plan's KTD3 generation has no column.** | A wrong choice (`updated_at`) would invalidate every cache entry every 60 seconds, turning the cache into a permanent cold start against remote API servers. | D2 uses `created_at` and explains why. The need for a real column if in-place rotation ever ships is flagged, with no sequence number invented. |
| R-7 | **Mobile regression via the dashboard status change.** `DashboardLocalOnlyError` matches HTTP 400 + `"local cluster"`. | R4 violation; would surface as a broken remote-cluster screen in a shipped app. | U10's `?coverage=1` opt-in preserves the exact 400 and message. `TestDashboardSummary_LocalUnchangedWithoutCoverageParam` asserts it. |
| R-8 | **`check-cluster-routing.sh` runs in `warn` mode** (`ci.yml:63`), so U9b's widened pattern would not actually fail CI. | The structural guard is advisory in practice. | U9b's verification runs the script locally with `CHECK_CLUSTER_ROUTING_GATE=fail` and requires zero violations, so the gate flip is a one-line follow-up rather than a cleanup project. Flipping CI itself is a repo-wide decision outside Release C. |
| R-9 | **Admin-only remote access narrows the audience.** `middleware.ClusterContext` requires admin for any non-local cluster. Every remote feature in Release C is therefore admin-only. | Release C's value is capped by an existing authorization decision. | Not changed here — loosening it is a security decision, not an implementation detail. U11a's switcher degrades gracefully for non-admins (local only). Recorded as an open product question. |
| R-10 | **AE2's live proof depends on Q2.** | Release C could be declared done on fixture evidence alone. | U12 separates agent-executable deliverables from the gated runbook and requires the docs to say "fixture-verified" until the runbook has been executed once. |
| R-11 | **`DashboardV2.tsx` collides with Release A's U6.** | Two large edits to a 1,084-line island. | Sequencing rule in the conflict table: U11c strictly before U6, or U11c rebases. |
| R-12 | **Bundle-introduced CRDs on a remote target.** `applyOne` retries 3× over ~1.5s; `diffOne` does not retry at all. | A CRD+CR bundle could diff-fail while apply-succeeds, confusing the operator. | Pre-existing asymmetry, documented in U9a's test table rather than changed. Changing `diffOne`'s retry behaviour is a behaviour change for local too and belongs in its own PR. |

### Open items requiring a human decision

1. **Q2** — the acceptance environment. Blocks U12's live run only; all source work proceeds.
2. **R-9** — should non-admin operators be able to *read* a remote cluster? Today they cannot.
3. **R-8** — flip `CHECK_CLUSTER_ROUTING_GATE` to `fail` in CI? Recommended immediately after U9b,
   as its own PR.
4. **R-4** — schedule the `ProbeOne` `StrictDialContext` fix as a security PR.
