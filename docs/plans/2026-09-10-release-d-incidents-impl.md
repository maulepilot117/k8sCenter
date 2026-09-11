---
title: "Release D — Persistent Incident Investigations — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: D
units: U20–U25
migration_sequence: 000020
date: 2026-09-10
q1_policy: resolved — owner + explicit grant + current-authorization re-check at read time, 30-day retention
---

# Release D — Persistent Incident Investigations — Implementation Plan

Covers master-plan requirements **R1, R2, R3, R14, R15, R16, R17**, decisions **KTD3** and **KTD8**, the Incidents row of *Proposed Data and API Boundaries*, delivery row **D**, and acceptance example **AE6**.

This document is a plan only. No production code, migrations, or tests were written into the repository while producing it. The only repository write is this file.

**Unit count after splits: 12** (master plan proposed 6). Every unit is one feature branch, one PR, at most five touched files including tests. Split rationale is stated per unit.

---

## 1. Codebase Findings

Every row below is a file I opened and read during planning. "Constrains" states what the file forces the implementation to do or not do.

### 1.1 Backend — diagnostics (the U20 crux)

| File | What I read | Constrains |
|---|---|---|
| `backend/internal/diagnostics/diagnostics.go` | `Severity`, `Result`, `Link`, `CheckFunc`, `DiagnosticTarget`, `RelatedRBAC`, `ruleEntry`, `registerRule`, `RunDiagnostics`, `runSafeCheck` (5s per-rule timeout + panic recovery), `statusOrder`, `Resolve`, `fetchObject`, `resolveRelatedPods` | `Result` is the *only* result type and has **no** source reference, no observation timestamp, no UID, no cluster ID, and no `inconclusive` status. `Status` is a bare `string` of `"pass"`/`"warn"/"fail"`. U20 must add the normalized contract **alongside** it, not by mutating it. |
| same | `DiagnosticTarget.Events []*corev1.Event` exists but the in-file comment states: *"ResourceLister does not expose ListEvents, so we skip event population."* | `target.Events` is **always nil today**. U22's event adapter cannot read events from the diagnostics resolver; it needs its own fetch path (see §1.3). |
| same | `Resolve(...)` silently returns `target.Pods == nil` when `related.allowsPods()` is false | Rules then report `Status: "pass"` (`"No pods in CrashLoopBackOff"`) for a user who simply cannot list pods. This is a live **R3 truthfulness defect**: a missing observation renders as healthy. U20 must surface it as `inconclusive` on the *new* surface only (see §4.3). |
| `backend/internal/diagnostics/rules.go` | `init()` registering exactly six rules — `CrashLoopBackOff`, `ImagePullBackOff`, `PendingPod` (critical); `ReplicaMismatch`, `ZeroEndpoints`, `PendingPVC` (warning) — and the six `check*` functions | Rule names are the de-facto stable IDs already on the wire. `ruleEntry` has no dependency metadata, so U20 must add `dependsOn` to `ruleEntry` + the six `registerRule` calls to know which checks are pod-derived. This is why U20's file list correctly includes `rules.go`. |
| `backend/internal/diagnostics/handler.go` | `Handler{Lister, TopoBuilder, AccessChecker, NotifService, Logger}`, `diagnosticsResponse{target, results, blastRadius}`, `kindToResource` (6 kinds), `kindNeedsPods`, `kindNeedsReplicaSets`, `HandleDiagnostics`, `HandleNamespaceSummary`, `resolveRelatedRBAC`, `findNodeID`, `podFailureReason` | The wire shape `{data:{target,results,blastRadius}}` is consumed by web **and** mobile. The notification emit loop reads `result.Status == "fail"`, `result.Severity`, `result.RuleName`, `result.Message`. Any change to `Result` field names/JSON tags breaks all of §1.6. `resolveRelatedRBAC` is the precedent for "SAR transport error ⇒ treat as denied, log, degrade gracefully" — U23 reuses that posture but must distinguish it in the response (R3). |
| `backend/internal/diagnostics/blast.go`, `rules_test.go`, `rbac_p3_test.go` | present; `BlastResult`, `AffectedResource`; existing rule tests assert `Result` fields directly | `rules_test.go` and `rbac_p3_test.go` are compile-time consumers of `Result`. U20 must leave them passing unmodified — that is the cheapest real compatibility proof. |

### 1.2 Backend — persistence

| File | What I read | Constrains |
|---|---|---|
| `backend/internal/store/store.go` | `//go:embed migrations/*.sql`, `DB{Pool *pgxpool.Pool}`, `New(ctx, connString, maxConns, minConns, logger)` runs migrations on connect, `Ping`, `Close` | Migrations are embedded and auto-run at startup. Adding `000020_*.sql` files is sufficient; no registration step. |
| `backend/internal/store/eso_history.go` | `ESOSyncHistoryEntry`, `ESOHistoryStore{pool}`, `NewESOHistoryStore(pool)`, `Insert` with `ON CONFLICT (uid, attempt_at) DO NOTHING`, `QueryByUID` (limit clamp 1..500 → 50), `LatestByUID` returning `(nil, nil)` on `pgx.ErrNoRows`, `Cleanup(ctx, retentionDays)` with `cleanupTimeout = 5 * time.Minute` and `DELETE ... WHERE attempt_at < NOW() - $1 * INTERVAL '1 day'` | **This is the exact idiom U21a/U21b/U25a must copy**: plain row struct with primitive fields, constructor taking `*pgxpool.Pool`, `fmt.Errorf("<verb> <table>: %w", err)` wrapping, `(nil, nil)` for not-found, bounded-context cleanup returning `(int64, error)`. Note its explicit RBAC contract comment about `diff_keys_*` leaking Secret **key names** — Release D's stricter Secret rule is the same threat, generalized. |
| `backend/internal/store/eso_bulk_jobs.go` | `BulkRefreshAction` typed enum matching a SQL `CHECK`, `BulkRefreshOutcome` marshalled into JSONB, `ESOBulkRefreshJob` row struct with `uuid.UUID` ID | JSONB payloads are stored as Go structs marshalled at the store boundary; `github.com/google/uuid` is already a dependency. Store row structs are **primitive-only** and import no domain package. |
| `backend/internal/store/migrations/000011_create_eso_sync_history.up.sql` | `BIGSERIAL PK`, `TEXT NOT NULL DEFAULT 'local'` cluster id, `CHECK (outcome IN (...))`, three indexes incl. a partial index, a `COMMENT ON TABLE` | SQL style: `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`, aligned column formatting, inline `CHECK` enums, and a mandatory `COMMENT ON TABLE` describing purpose + retention + scaling note. |
| `.../000013_create_eso_bulk_refresh_jobs.up.sql` + `.down.sql` | `UUID PRIMARY KEY`, `JSONB NOT NULL DEFAULT '[]'`, partial unique index on active rows; down = `DROP TABLE IF EXISTS` | UUID PKs are established. Down migrations for new tables are plain drops. |
| `.../000007_create_notification_center.up.sql` | `UUID PRIMARY KEY DEFAULT gen_random_uuid()`, `length(title) <= 500` CHECK constraints, `TEXT NOT NULL` user ids in `nc_reads(user_id TEXT NOT NULL, ...)`, `REFERENCES ... ON DELETE CASCADE` | **Owner columns are plain `TEXT`, not FKs to `local_users`** — matches the master plan's assumption and `auth.User.ID`. Length CHECKs on user-supplied text are precedent. |
| `backend/internal/store/migrations/NOTES.txt` | Two operator-facing sections (000016, 000017) documenting required operator action, inspection SQL, and binary-rollback constraints | **The master plan's U21 file list omits `NOTES.txt`.** Release D destroys operator data on rollback and has a retroactive-retention footgun; repo convention requires a NOTES.txt section. U21a must include it. |
| `ls backend/internal/store/*_test.go` | **No test files exist in `backend/internal/store/` at all** | There is no in-repo real-PostgreSQL test harness for the store layer. See §1.7. |

### 1.3 Backend — authorization, identity, k8s access

| File | What I read | Constrains |
|---|---|---|
| `backend/internal/auth/provider.go` | `User{ID, Username, Provider, KubernetesUsername, KubernetesGroups []string, Roles []string}`, `IsAdmin`, `UserFromContext`, `ContextWithUser` | Owner/grantee columns store `auth.User.ID` (a **provider-qualified** string, `Provider` is `"local"`/`"oidc"`/`"ldap"`). Kubernetes authorization uses the *different* fields `KubernetesUsername` + `KubernetesGroups`. The two must never be conflated: `owner_id` is an app identity; SARs take the k8s identity. |
| `backend/internal/k8s/resources/access.go` | `accessCacheTTL = 60 * time.Second`; `accessCacheKey{clusterID, username, groups (sorted, comma-joined), resource, namespace, verb}`; `AccessChecker`; `CanAccess(ctx, clusterID, username string, groups []string, verb, resource, namespace string) (bool, error)` (infers API group from resource name); **`CanAccessGroupResource(ctx, clusterID, username string, groups []string, verb, apiGroup, resource, namespace string) (bool, error)`**; `clientForCluster` (local → `ClientFactory.ClientForUser`, remote → `ClusterRouter.ClientForCluster`, error if router nil); `StartCacheSweeper`; test constructors `NewAlwaysAllowAccessChecker`, `NewAlwaysDenyAccessChecker`, `NewDenyResourcesAccessChecker(...)`, `NewPredicateAccessChecker(fn AccessPredicate)` | **This is the exact signature and cost model the read-time re-authorization depends on.** One uncached call = one `SelfSubjectAccessReview` POST to the target cluster's API server. Cached decisions live 60s keyed on the full tuple **including `clusterID`** (F#9). `CanAccessGroupResource` caches under key resource `apiGroup + "/" + resource`. `NewPredicateAccessChecker` is the test seam for "collaborator can read namespace A but not B". |
| `backend/internal/server/middleware/cluster.go` | `ClusterContext`, `WSClusterContext`, `ClusterIDFromContext(ctx) string`, `WithClusterID` | Every SAR call site must pass `middleware.ClusterIDFromContext(r.Context())`. For incidents the *stored* `cluster_id` on the evidence row is authoritative for re-authorization, **not** the request header — see policy P6. |
| `backend/internal/k8s/client.go` | `ClientForUser(username string, groups []string) (kubernetes.Interface, error)`, `DynamicClientForUser`, `BaseClientset`, `RESTMapper`, `DiscoveryClient`, `NewFakeClientFactory` | U22's event adapter fetches events through `ClientForUser(...).CoreV1().Events(ns).List(...)` — the impersonated path, never the service account. `NewFakeClientFactory` is the collector test seam. |
| `backend/internal/k8s/cluster_router.go` | `ClientForCluster`, `DynamicClientForCluster`, `RouterFor`, `LocalFactory`, `EvictCluster`, `RegisterEvictHook`, `StartCacheSweeper` | Remote-cluster capture is possible but out of Release D scope (delivery row D is local-first; C gates remote). Store `cluster_id` anyway so remote capture is additive later. |
| `backend/internal/k8s/informers.go` | `Events() corev1listers.EventLister`, `factory.Core().V1().Events().Informer()` registered | Local-cluster events **are** in the informer cache. But the informer cache is not user-impersonated, so reading it bypasses RBAC. U22 must SAR-gate `list events` before serving from cache, or fetch impersonated. Plan: SAR-gate then use the impersonated client (simpler to reason about, matches Secrets handling). |
| `backend/internal/topology/builder.go` | `ResourceLister` interface — 12 `List*` methods, **no `ListEvents`** | Confirms §1.1: events are not reachable through the diagnostics resolver. |

### 1.4 Backend — redaction, audit, HTTP, resilience

| File | What I read | Constrains |
|---|---|---|
| `backend/internal/k8s/resources/secrets.go` | `const kindSecret = "secrets"`; `const lastAppliedConfigAnnotation = "kubectl.kubernetes.io/last-applied-configuration"`; `func maskedSecret(s *corev1.Secret) *corev1.Secret` — deep-copies, replaces every `Data`/`StringData` value with `"****"`, and **deletes the last-applied-configuration annotation**; `HandleListSecrets` never uses the informer cache | **`maskedSecret` is unexported.** The master plan's instruction that U22's `redaction.go` must "reuse, not reinvent" is not literally satisfiable — see Correction C3. The two non-obvious rules to carry over are (a) `StringData` must be masked as well as `Data`, and (b) the `last-applied-configuration` annotation carries plaintext `stringData` and must be stripped from *any* object, not only Secrets. |
| grep for other maskers | `store.MaskedSettings` (`store/settings.go`), `notifications.maskConfig` (`notifications/handler.go`), `externalsecrets/bulk.go` UID redaction | There is **no shared redaction package** anywhere in `backend/internal/`. Confirmed by full-tree grep. |
| `backend/internal/audit/logger.go` | `Action` string enum (~30 values incl. `ActionCreate/Update/Delete/Reveal/Apply` and domain groups like `ActionESOForceSync`); `Result` = `success`/`failure`/`denied`; `Entry{Timestamp, ClusterID, User, SourceIP, ConnectionIP, Action, ResourceKind, ResourceNamespace, ResourceName, Result, Detail}`; `Logger interface { Log(ctx, Entry) error }`; `SlogLogger` fallback | Release D adds a small block of `ActionIncident*` constants (U23a). `Entry.ResourceKind/Namespace/Name` are reused to record the *incident*, and `Detail` carries a bounded, already-redacted summary. Audit is an interface — the handler takes `audit.Logger`, never a concrete type. |
| `backend/internal/httputil/response.go` | `WriteJSON`, `WriteError(w, status, message, detail)` (strips `detail` on ≥500), `WriteData`, **`WriteErrorWithReason(w, status, message, reason string, extra map[string]any)`**, `RequireUser(w, r) (*auth.User, bool)` | All incident handlers use these. `WriteErrorWithReason` is the mechanism for `409 note_revision_conflict`, `413 evidence_limit_exceeded`, `503 incident_persistence_unavailable`. |
| `backend/pkg/api/*.go` | `Response{Data, Metadata *Metadata, Error *APIError}`, `Metadata{Total, Continue, Page, PageSize}`, `APIError{Code, Message, Detail, Reason, Extra}` | List responses must use `Metadata`. **`Metadata.Total` is the post-filter visible count** under policy P9 — a `withheld` count goes in the data envelope, never in `Total`. |
| `backend/internal/recoverutil/recoverutil.go` | Package doc: wrappers for goroutines outside chi's recovery. `Go(g *errgroup.Group, logger, label string, fn func() error)`; `Safe(logger, label string, fn func())`; `Tick(ctx, logger, label string, fn func(context.Context))`; nil logger → `slog.Default()`; all log under key `task=<label>` with `stack` | U22b's bounded-concurrency collector uses `recoverutil.Go` on an `errgroup`. U25a's retention loop uses `recoverutil.Tick`. |
| `docs/solutions/backend-resilience-conventions.md` | Rule at L19 ("every goroutine outside chi's recovery MUST wrap"); L46–L59 — anything that **must run regardless of panic** lives **outside** the wrapped closure: `defer wg.Done()` is registered on the goroutine *before* calling `Safe`, never inside `fn`, or `wg.Wait()` hangs forever; L96 — same for counted channel sends | Verbatim constraint on U22b and U25a. Stated again in each unit's steps. |
| `.github/workflows/fuzz.yml` | Nightly 04:00 UTC + `workflow_dispatch`; matrix of `{pkg, target}` rows (18 today, incl. `FuzzMaskedSecret` and `FuzzSecretPipeline` in `./internal/k8s/resources/`); a **`-list` drift guard** step (`go test <pkg> -list '^<target>$' | grep -qx '<target>'`) before each `-fuzztime=5m` run; crash-corpus upload | A new fuzz target is **not covered** unless a matrix row is added. That makes `fuzz.yml` a required file in whichever unit introduces a fuzz target — which is what pushes U22 over five files (Correction C2). |
| `backend/internal/config/config.go`, `defaults.go` | `Config{Server, Log, Auth, Monitoring, Loki, Alerting, Audit, Database, Dev, ClusterID, CORS, CiliumAgent}`; `AuditConfig{RetentionDays int \`koanf:"retentiondays"\`}`; koanf defaults map keyed `"audit.retentiondays": DefaultAuditRetentionDays`; env mapping `KUBECENTER_` + `_`→`.` lowercased; `DefaultAuditRetentionDays = 90`, `DefaultAlertingRetentionDays = 30` | A new `Incidents IncidentsConfig` field + defaults entries + `defaults.go` consts are required for "30-day **configurable** retention". Two config files → this is what pushes U25 over five files (Correction C2). |

### 1.5 Backend — registration and DI anchors

| File | What I read | Constrains |
|---|---|---|
| `backend/internal/server/routes.go` | `registerRoutes()`; WS group; `s.Router.Route("/api/v1", ...)` with `chimw.Timeout(s.Config.Server.RequestTimeout)`; the authenticated block's cascade of `if s.XHandler != nil { s.registerXRoutes(ar) }` (lines ~123–226); `registerDiagnosticsRoutes` (L536) using `yamlRL := s.YAMLRateLimiter; if yamlRL == nil { yamlRL = s.RateLimiter }`, `dr.Use(middleware.RateLimit(yamlRL))`, `dr.Use(resources.ValidateURLParams)` | New unit `registerIncidentRoutes(ar chi.Router)` slots into the same cascade. **`resources.ValidateURLParams` only validates the `name` and `namespace` chi params** (read at `resources/handler.go:297`) — it is inert for `{incidentID}` and harmless, so incident routes get their own UUID validation in-handler. |
| `backend/internal/server/server.go` | `Server` struct field list (L~43–90) and the parallel `Deps` struct (L~92–137); the `if deps.XHandler != nil { s.XHandler = deps.XHandler }` assignment cascade (L~230–325); `dbPing func(context.Context) error // nil if no DB` | Two struct fields + one assignment block. Same file, three edits. |
| `backend/cmd/kubecenter/main.go` (965 LOC) | L143 `ctx, stop := signal.NotifyContext(...)`; L192–250 the `if cfg.Database.URL != ""` block establishing `dbPing`, `dbPool`, stores, the audit retention goroutine, `defer db.Close()`; L359 `diagHandler := &diagnostics.Handler{...}`; L710–763 notification service wiring incl. `diagHandler.NotifService = notifService`; L783–870 the ESO history + bulk wiring (`if dbPool != nil { ... }`, hourly retention goroutines with hand-rolled `recover()`); L890–921 the `server.New(server.Deps{...})` literal; L935–965 graceful shutdown | Anchors are unambiguous. Note **L836–866's ESO bulk retention goroutine uses a hand-rolled `defer func(){ recover() }()` rather than `recoverutil.Tick`** — U25a must **not** copy that; `docs/solutions/backend-resilience-conventions.md` and CLAUDE.md require `recoverutil`. |
| DB-unavailable precedent | `externalsecrets/bulk.go:294,457` → `httputil.WriteError(w, http.StatusServiceUnavailable, "bulk refresh unavailable", "")` while the routes themselves stay registered; contrast the `if s.XHandler != nil` route-registration gate which yields a bare chi **404** | **These two patterns conflict, and the master plan picks the wrong one.** See Correction C4: incident routes must always register and return a truthful `503` + `reason`, because R3/U23 demand "no DB reports unavailable", and a 404 is indistinguishable from "no such incident". |

### 1.6 Complete current consumer list of `diagnostics.Result` (U20 must not break any of these)

**Go (compile-time):**
1. `backend/internal/diagnostics/rules.go` — all six `check*` functions construct and return `Result`.
2. `backend/internal/diagnostics/diagnostics.go` — `CheckFunc`, `RunDiagnostics`, `runSafeCheck`, `statusOrder`.
3. `backend/internal/diagnostics/handler.go` — `diagnosticsResponse.Results []Result`; the notification loop reads `.Status`, `.Severity`, `.RuleName`, `.Message`.
4. `backend/internal/diagnostics/rules_test.go`.
5. `backend/internal/diagnostics/rbac_p3_test.go`.

No other Go package imports `internal/diagnostics` except for the `*diagnostics.Handler` type in `backend/internal/server/server.go` and `backend/cmd/kubecenter/main.go` — verified by full-tree grep on `internal/diagnostics`.

**Web (wire-contract):**
6. `frontend/lib/types/diagnostics.ts` — `DiagnosticResult`, `AffectedResource`, `KIND_ROUTE_MAP`, `getResourceSection`.
7. `frontend/islands/DiagnosticChecklist.tsx` — declares its **own** `export interface DiagnosticResult` (duplicate of #6).
8. `frontend/islands/BlastRadiusPanel.tsx` — declares its **own** `export interface AffectedResource` (duplicate of #6).
9. `frontend/islands/DiagnosticWorkspace.tsx` — imports the *island* copies (#7, #8), **not** `lib/types/diagnostics.ts`; declares `DiagnosticResponse{target, results, blastRadius}`.
10. `frontend/routes/observability/investigate.tsx`.

**Mobile (wire-contract):**
11. `mobile/lib/api/diagnostics_repository.dart` — the header comment pins the exact envelope and `kDiagnosticsKinds` mirrors `kindToResource`.
12. `mobile/lib/features/observability/diagnostics/diagnostics_controller.dart`
13. `mobile/lib/features/observability/diagnostics/diagnostics_screen.dart`
14. `mobile/lib/features/observability/diagnostics/diagnostic_checklist.dart`
15. `mobile/lib/features/observability/diagnostics/blast_radius_panel.dart`
16. `mobile/lib/features/observability/diagnostics/namespace_summary_screen.dart`
17. `mobile/lib/features/observability/diagnostics/scrollable_center.dart`
18. `mobile/lib/widgets/resource_detail_scaffold.dart` (Diagnose entry point)
19. `mobile/lib/routing/app_router.dart`
20. `mobile/lib/api/websocket_client.dart`
21. `mobile/lib/observability/pii_scrubber.dart`
22. Tests: `mobile/test/features/observability/diagnostics/diagnostics_controller_test.dart`, `.../blast_radius_panel_test.dart`, `mobile/test/a11y/resource_detail_traversal_test.dart`, `mobile/test/observability/pii_scrubber_test.dart`

**Indirect:** `backend/internal/notifications` receives `RuleName`/`Message`/`Severity` through `NotifService.Emit`, which reaches the notification centre feed, Slack/email channels, and FCM push.

**Consequence:** the only safe U20 is *purely additive*. `Result`'s Go definition, JSON tags, and `RunDiagnostics`'s return type must be byte-identical after U20.

### 1.7 Test infrastructure

| Fact | Evidence | Constrains |
|---|---|---|
| CI backend job runs `go test ./... -race -cover -count=1` with **no PostgreSQL service** | `.github/workflows/ci.yml:69`; no `services:` block | `backend/internal/store/incidents_test.go` **cannot** open a real DB in CI. |
| E2E CI **does** have PostgreSQL 17 | `.github/workflows/e2e.yml:28-30` `services: postgres: image: postgres:17-alpine`; `KUBECENTER_DATABASE_URL` at L115; `e2e/playwright.config.ts` sets the same URL for the backend `webServer` | Real migration round-trips and real persistence behaviour are exercised by the **e2e** suite, not `go test`. Release D's DB-truth tests belong there. |
| `backend/internal/store/` contains **zero** `_test.go` files | directory listing | There is no precedent harness to copy. U21a introduces the first, and must therefore be self-skipping. |
| Frontend tasks | `frontend/deno.json`: `check` = `deno fmt --check . && deno lint . && deno check`; `test` = `deno test -A`; `build` = `vite build` | Agent Directive 4 commands confirmed. |
| E2E | `e2e/playwright.config.ts` — `testDir: "./tests"`, projects `setup` (`testDir: ./fixtures`, `testMatch: /.*\.setup\.ts/`), `chromium` (storageState `playwright/.auth/admin.json`, `testIgnore: /api-routes\.spec\.ts/`), `route-contract`; `e2e/fixtures/auth.setup.ts` creates **only** `admin`; `e2e/fixtures/base.ts` injects the bearer via `addInitScript` | A **second identity requires a new setup file plus a new project** in `playwright.config.ts`. The master plan's single-file `e2e/tests/incidents.spec.ts` cannot deliver AE6's collaborator branch alone. |
| Specs live in both `e2e/*.spec.ts` and `e2e/tests/*.spec.ts` | directory listing | Only `e2e/tests/` is collected by `testDir`. `e2e/tests/incidents.spec.ts` is the correct path. |
| `scripts/check-cluster-routing.sh` exists | `ls scripts/` | Runs as part of the master plan's verification contract for new cluster-aware handlers; U23a/U23b must pass it. |

### 1.8 Frontend

| File | LOC | What I read | Constrains |
|---|---|---|---|
| `frontend/islands/DiagnosticWorkspace.tsx` | **372** | Signals `namespace/kind/name/loading/error/results/directlyAffected/potentiallyAffected/hasData`; `fetchDiagnostics` calls `apiGet('/v1/diagnostics/${ns}/${k}/${n}')`; URL-param bootstrap in `useEffect`; `handleInvestigate` writes `history.replaceState`; a status banner + 3fr/2fr grid of `DiagnosticChecklist` + `BlastRadiusPanel` | >300 LOC ⇒ **Agent Directive 1 ("Step 0") applies to any structural refactor of this file.** U25b's "add a capture entry point" is additive and does *not* trigger it. However the file is written entirely with inline `style={{...}}` objects, which violates CLAUDE.md's *Tailwind utility-only, theme via CSS custom properties* rule. Converting it **is** structural and must be a separate cleanup commit before any other change — do not bundle it into U25b. New incident islands must be Tailwind-only from the start. |
| `frontend/routes/observability/investigate.tsx` | 16 | `define.page(...)` returning `<div class="p-6 space-y-6">` + `<h1 class="text-2xl font-bold text-text-primary">` + `<DiagnosticWorkspace />` | The SSR page shell / island split to copy for `incidents/index.tsx`. Tailwind classes, `text-text-primary` / `text-text-secondary` tokens. |
| `frontend/routes/external-secrets/external-secrets/[namespace]/[name].tsx` | 7 | `define.page(function ...(ctx) { const { namespace, name } = ctx.params; return <Island namespace={namespace} name={name} /> })` | The dynamic-route → island prop-passing idiom for `incidents/[id].tsx`. |
| `frontend/lib/api.ts` | 302 | Client-only module warning; in-memory `accessToken`; `ApiError` with `.reason` and `errorExtra(err, key)`; `api<T>`, `apiGet`, `apiPost`, `apiPut`, `apiDelete`, `apiPostRaw`; **domain namespaces `notifApi` (L257) and `limitsApi` (L297)** | New endpoints go in an `incidentsApi = { ... }` object at the end of `api.ts`, following `notifApi`'s shape exactly. `ApiError.reason` is how the UI distinguishes `incident_persistence_unavailable` from a real 503. |
| `frontend/lib/constants.ts` | 547 | Nav tree; the `observability` group (L391–401) with `{ label: "Service Topology", href: "/observability/topology" }`, `{ label: "Log Explorer", href: "/observability/logs" }`, `{ label: "Investigate", href: "/observability/investigate" }` | One-line nav insertion point for `{ label: "Incidents", href: "/observability/incidents" }`. |
| `frontend/lib/types/diagnostics.ts` | 46 | `KIND_ROUTE_MAP`, `getResourceSection`, `DiagnosticResult`, `AffectedResource` | Reused by the incident timeline for deep-linking evidence back to resource detail pages. |

### 1.9 Corrections to the master plan

These are places where the master plan asserts something the codebase contradicts. Implementers should follow this document, not the parent, on these points.

**C1 — U20's premise is only half-right, and one of its stated test scenarios is a breaking change.**
The parent says *"Add stable check IDs, source references, observation time, pass/fail/inconclusive status, and structured evidence without changing existing diagnosis meaning"* and lists the scenario *"missing permissions/metrics become inconclusive where appropriate."* In the real code:
- `Result` has none of those fields and `DiagnosticTarget.Events` is permanently nil (`ResourceLister` has no `ListEvents`), so "structured evidence" cannot include events in U20 at all.
- Today an RBAC-denied related-pod resolution produces `Status: "pass"` with the message *"No pods in CrashLoopBackOff"*. Turning that into `inconclusive` **on the existing `Result`** would change the JSON that 12 mobile files and 5 web files decode, and would flip the `handler.go` notification-emit condition. That is a mobile-breaking change and violates R4.
  → **Resolution:** U20 is strictly additive. `Result`, its JSON tags, and `RunDiagnostics`'s signature are frozen. The `inconclusive` semantics appear only on the new `CheckResult` surface. A `Denormalize(Normalize(x)) == x` round-trip test is the compatibility proof. Making the legacy surface truthful is listed in the Deferred appendix as a coordinated web+mobile change.

**C2 — Four of the six unit file lists do not fit in five files.**
- **U22** needs `redaction_fuzz_test.go` (CLAUDE.md: redaction is a parse seam) **and** a `.github/workflows/fuzz.yml` matrix row (without it the `-list` drift guard never runs the target, so the fuzzing is decorative) → 6 files.
- **U23** lists exactly 5 files but requires `handler.go` to carry CRUD + capture + notes + grants + export in one PR (~900 LOC) with zero headroom.
- **U24** omits `frontend/lib/api.ts`, without which nothing can call the new endpoints → 6 files.
- **U25** says "30-day **configurable** retention" but omits `backend/internal/config/config.go` and `defaults.go` → 7 files.
- **U21** omits `backend/internal/store/migrations/NOTES.txt`, which repo convention requires for any migration with operator-visible consequences (Release D has two: destructive rollback, and retroactive retention).
  → **Resolution:** 12 units. Splits are named `U21a/U21b`, `U22a/U22b`, `U23a/U23b`, `U24a/U24b/U24c`, `U25a/U25b`.

**C3 — "`redaction.go` must reuse, not reinvent" is not literally achievable.**
Full-tree grep finds no shared redaction package. The only maskers are `resources.maskedSecret` (**unexported**), `store.MaskedSettings`, and `notifications.maskConfig` — three ad-hoc, non-composable helpers. Importing `internal/k8s/resources` from `internal/incidents` would also drag the whole resource-handler surface into the incidents package.
  → **Resolution:** U22a writes `backend/internal/incidents/redaction.go` as a new, self-contained, **fuzzed** package-local implementation, and its doc comment names `resources.maskedSecret` as the behavioural reference it must not diverge from. The two rules that must be carried over verbatim are: mask `StringData` as well as `Data`, and delete `kubectl.kubernetes.io/last-applied-configuration` from **every** object (not only Secrets — it carries plaintext `stringData` on any resource kubectl has applied). A regression test asserts parity with `maskedSecret`'s observable behaviour. Unifying the three maskers behind one package is listed as deferred.

**C4 — The "DB unavailable" pattern the master plan implies produces a 404, not a truthful 503.**
`routes.go` gates every optional handler with `if s.XHandler != nil { s.registerXRoutes(ar) }`. If incidents follow that, a no-DB deployment returns chi's bare 404 for `/api/v1/incidents/{id}` — indistinguishable from "that incident does not exist", which violates R3 and U23's own exit criterion (*"no DB reports unavailable"*). The correct in-repo precedent is `externalsecrets/bulk.go:294`: **routes stay registered; the handler returns `503`.**
  → **Resolution:** `main.go` always constructs `incidents.NewHandler(...)`, passing a possibly-nil store. `routes.go` registers unconditionally on `s.IncidentsHandler != nil` (which is always true). Every handler method begins with a shared `h.requireStore(w)` gate returning `503` + `reason: "incident_persistence_unavailable"`.

**C5 — There is no store-layer test harness, and CI has no PostgreSQL for `go test`.**
`backend/internal/store/` has zero test files and `ci.yml` runs `go test ./...` without a database. The master plan's verification row *"Real PostgreSQL migration round trips"* has no automated home today.
  → **Resolution:** `store/incidents_test.go` is split in two halves in one file: (a) pure-function tests (validation, byte accounting arithmetic, capture-key derivation, SQL-parameter construction) that always run; (b) integration tests behind `t.Skip` unless `KUBECENTER_TEST_DATABASE_URL` is set. The real migration round-trip and real persistence assertions are additionally covered by U24c's e2e suite, which *does* run against PostgreSQL 17. `e2e.yml` gets no new service.

**C6 — `resources.ValidateURLParams` does not validate incident IDs.**
It only checks the `name` and `namespace` chi params (`resources/handler.go:297-311`). Attaching it to the incident router is harmless but provides zero protection for `{incidentID}`.
  → **Resolution:** incident handlers parse `{incidentID}` with `uuid.Parse` and return `400` on failure, before any store call. Do not rely on the middleware.

**C7 — The master plan's `handler_test.go` scenarios assume a permission model the AccessChecker fakes don't express by default.**
`NewAlwaysAllowAccessChecker` / `NewAlwaysDenyAccessChecker` are all-or-nothing; `NewDenyResourcesAccessChecker` only affects `CanAccess` (core resources), and `NewPredicateAccessChecker`'s predicate is consulted **only** by `CanAccessGroupResource` — `CanAccess` short-circuits to allow in predicate mode (`access.go:111`).
  → **Resolution:** all read-time re-authorization in Release D goes through **`CanAccessGroupResource`** (with `apiGroup: ""` for core resources), never `CanAccess`. This is both semantically correct for CRD-scoped evidence and the only way `NewPredicateAccessChecker` can express "allowed in namespace A, denied in namespace B" in tests.

---

## 2. The Q1 Access Policy, Stated Normatively

Q1 is **resolved**. This section is the authoritative specification. Every AE6 branch is answerable from this section alone. Implementers must not soften, extend, or "optimize" any clause; each clause is separately testable and every unit's tests name the clause it covers.

### Definitions

- **Owner** — the `auth.User.ID` recorded in `incidents.owner_id` at creation. Server-supplied; never accepted from the request body.
- **Collaborator** — an `auth.User.ID` with a row in `incident_grants` for this incident.
- **Evidence scope** — the tuple `(cluster_id, api_group, resource, namespace)` stored on the evidence row at capture time. `resource` is the **plural lowercase API resource name** (`pods`, `deployments`, `externalsecrets`), matching what `CanAccessGroupResource` expects.
- **Current authorization** — `AccessChecker.CanAccessGroupResource(ctx, scope.cluster_id, user.KubernetesUsername, user.KubernetesGroups, "get", scope.api_group, scope.resource, scope.namespace)` evaluated **at the moment of the read**, using the scope stored on the row.

### The policy

**P1 — Incident visibility.** An incident record (id, title, summary, status, window, timestamps, counts) is visible **if and only if** the caller is the owner **or** a collaborator. There is no admin override, no "public" incident, and no organization-wide read. A caller who is neither receives **404**, never 403 — a 403 would confirm the incident exists and leak the ID space. *(R14, R16)*

**P2 — A grant is necessary but never sufficient.** A collaborator grant conveys exactly one thing: standing to *ask*. It grants no Kubernetes authority whatsoever. Every evidence item is additionally gated by P4. An owner is subject to P4 identically — ownership is not a permission bypass. *(R16, R2)*

**P3 — What a grant does grant.** A collaborator may: read the incident record; read evidence items that pass P4; read and create notes; and read the export (filtered per P4). A collaborator may **not**: capture new evidence; add, modify, or remove grants; rename or close the incident; or delete the incident. Those are owner-only. `incident_grants.can_annotate` may be set false to make a grant read-only; it can never be raised above the collaborator ceiling.

**P4 — Read-time re-authorization of every evidence item.** For each evidence item returned by any endpoint, the server evaluates *current authorization* for that item's stored scope. The result is one of:
  - **allowed** → the item is returned in full (already-redacted at capture per P11).
  - **denied** (`Status.Allowed == false`) → the item is replaced by a placeholder carrying only `{id, evidenceKind, collectedAt, withheld: true, withheldReason: "forbidden"}`. **No** `namespace`, `name`, `uid`, `resource`, payload, or `completenessDetail` is emitted — those are themselves disclosure.
  - **check unavailable** (SAR transport error) → the item is replaced by the same placeholder shape with `withheldReason: "authorization_check_unavailable"`. Fail closed. The error is logged with `slog.Warn`, matching `diagnostics.Handler.resolveRelatedRBAC`'s posture, but unlike diagnostics it is **surfaced to the caller** as a distinct reason, because R3 forbids conflating unavailable with forbidden.
  *(R2, R3, R16)*

**P5 — Scope de-duplication and bounds.** Before evaluating P4, the server collapses the items to a set of distinct scopes and evaluates each scope once. `AccessChecker` caches each decision for 60s keyed on `(clusterID, username, sorted-groups, apiGroup+"/"+resource, namespace, "get")`, so a warm read costs zero SARs. A cold read costs one SAR per distinct scope. An incident is capped at **20 distinct evidence scopes** (enforced at capture, U22b); a capture that would exceed it is rejected with `409 scope_limit_exceeded`. This bounds a single incident read at ≤20 SAR round-trips worst case.

**P6 — Stored scope is authoritative; the request header is not.** Re-authorization uses the `cluster_id` **stored on the evidence row**, never `middleware.ClusterIDFromContext`. A caller cannot switch `X-Cluster-ID` to a cluster where they happen to have broad rights and thereby unlock evidence captured elsewhere. If the stored `cluster_id` no longer resolves to a registered cluster, `CanAccessGroupResource` returns an error and the item is withheld under `authorization_check_unavailable` (P4).

**P7 — Permission revoked after capture.** If the caller's Kubernetes authorization for an item's scope is revoked at any time after capture, the item is withheld at the next read. There is no grandfathering, no "you could see it once" carve-out, and no cached-decision extension beyond `accessCacheTTL` (60s). Revocation therefore takes effect within 60 seconds. Withheld items are **not** deleted — an authorization change is not a retention event, and a later re-grant restores visibility. *(R2, R16, AE6 revocation branch)*

**P8 — Source object deleted.** Deleting the Kubernetes object an evidence item was captured from changes **nothing** about who may read that item. The scope is namespace-and-kind level, so it survives object deletion, and re-authorization keeps working. Explicitly: **the absence of the object never grants access.** A caller who could not read `pods` in `payments` while the pod existed still cannot read that pod's captured evidence after deletion. *(AE6 deletion branch; this is the exact clause the parent's "Incident Handoff" appendix flags as needing Q1.)*

**P9 — Deleted-then-recreated with the same name.** Evidence is bound to the source object's **UID** (`incident_evidence.source_uid`), per KTD3/R1. A new object with a reused name has a different UID and is a different subject: it inherits no evidence, and evidence captured from the prior object is never re-labelled as belonging to it. Nothing in the schema keys history on `(namespace, name)` alone, and no cascade is defined on name. When `source_uid` is empty (a source that genuinely has no UID, e.g. an aggregated check bundle), the item is flagged `identityWeak: true` in the envelope and the UI must say so rather than implying object identity.

**P10 — Identical filtering across every representation.** The filter in P4 is implemented **once** and applied identically to: the detail read, the evidence list, the incident list, every count, search results, timeline previews, collaborator previews, and both export formats. A count returned to a caller is the count of items **that caller may read**; the number of withheld items is returned separately as `withheldCount`. `api.Metadata.Total` carries the visible count only. It must never be possible to infer the existence, namespace, name, or kind of a withheld item from any count, ordering artefact, pagination boundary, or error message. *(R16, and the parent's "Counts, searches, exports, and collaborator previews must apply the same rules.")*

**P11 — Secret-derived evidence is strictly stricter.** An evidence item whose payload was derived from, or references, a `core/secrets` object is stored with `secret_derived = true`. Then:
  1. **Values are never persisted.** Redaction runs at capture, before the DB write. There is no code path that stores a Secret value, and no reveal endpoint for incident evidence. This is a capture-time invariant, not a read-time filter.
  2. Reading such an item requires, **in addition to P4**, current authorization for `get` on `("", "secrets", namespace)` on the item's stored cluster. Failing that, the item is withheld with `withheldReason: "forbidden"` — even for the owner, even if they hold `get` on the item's own kind.
  3. Key **names** count as sensitive (the `eso_history.go` `QueryByUID` doc comment establishes this precedent: a key named `PROD_DB_PASSWORD` reveals credential structure). Key names are only emitted when clause 2 passes.
  4. Environment variables sourced via `valueFrom.secretKeyRef` / `envFrom.secretRef`, `imagePullSecrets`, and `spec.volumes[].secret` references all mark the containing item `secret_derived = true`.
  *(R2, R15, the master plan's "stricter filtering for Secret-related evidence")*

**P12 — Export.** Export is a **server-rendered projection of exactly what P4/P10/P11 would show the same caller at the same moment**, produced by the same filter function. Additionally:
  - No credentials, tokens, bearer values, kubeconfig fragments, or `Authorization` headers may appear, by construction — none are ever stored (P11.1).
  - Formats are `json` and `markdown` only. **No HTML output.** Untrusted text (object names, event messages, controller messages, note bodies) is emitted as JSON string values in `json`, and inside fenced code blocks with backtick-run escaping in `markdown`. This prevents smuggling stored markup through the export, per the boundary note *"cannot smuggle arbitrary stored HTML or privileged blobs."*
  - `Content-Disposition: attachment` with a generated filename; `Content-Type: application/json` or `text/markdown; charset=utf-8`; `X-Content-Type-Options: nosniff`.
  - Withheld items appear as counted placeholders with their reason, so the export is honest about what it omits rather than silently short.
  - Every export is audit-logged (`ActionIncidentExport`) with the visible/withheld counts in `Detail`.

**P13 — Retention.** Default **30 days**, configurable via `KUBECENTER_INCIDENTS_RETENTIONDAYS` (`Config.Incidents.RetentionDays`), clamped to `[1, 3650]`. The sweep deletes whole incidents by `created_at` using the **currently configured** value — matching `ESOHistoryStore.Cleanup`'s established semantics. Two consequences must be documented in `NOTES.txt` and the ops docs:
  - Lowering the configured value **retroactively deletes** incidents that are now older than the new window, on the next sweep.
  - Raising it does **not** resurrect already-deleted incidents.
  Each incident stores `retention_days_at_capture` for forensic clarity about what the operator's policy was when it was captured. Deletion cascades to evidence, notes, note revisions, and grants via `ON DELETE CASCADE`. Retention is time-based only: it is never triggered by object deletion, permission change, or incident closure.

**P14 — Size bounds, enforced before the write.** **1 MiB per evidence item**, **10 MiB per incident**, **500 items per incident**, **20 distinct scopes per incident** (P5). Enforced at three layers: (1) the collector truncates/rejects before marshalling; (2) the store checks the running total inside the insert transaction under `SELECT ... FOR UPDATE`; (3) SQL `CHECK` constraints as the backstop. An over-limit capture returns `413` with `reason: "evidence_limit_exceeded"` and `extra: {limit, current}`. Truncation is never silent: the item's `redaction.truncated` is true and the UI says so.

**P15 — Append-only evidence.** There is no update path for `incident_evidence`. The store exposes `InsertEvidence` and queries; the only deletion is whole-incident deletion (owner action) and the retention sweep. Re-capturing an identical observation is idempotent via `capture_key` (`ON CONFLICT DO NOTHING`); a genuinely new observation (different `source_observed_at`/`resource_version`) is a new row. Notes are the *only* mutable content, and their edits are revisioned rather than destructive.

**P16 — Audit.** Create, update, delete, capture, note create/update/delete, grant add/remove, and export are all audit-logged via `audit.Logger.Log`. Reads of the incident record are not audited (too noisy), but **every export is**, since export is the exfiltration boundary. Audit `Detail` is bounded and carries no evidence payload.

---

## 3. Design Decisions

### 3.1 DDL — migration `000020_create_incidents`

`backend/internal/store/migrations/000020_create_incidents.up.sql`:

```sql
-- Release D — persistent incident investigations.
-- Q1 policy: personal ownership + explicit collaborator grants; every read
-- re-checks the caller's CURRENT Kubernetes authorization for each evidence
-- item's stored scope (a grant alone never suffices). 30-day configurable
-- retention. Stricter gating for Secret-derived evidence.

CREATE TABLE IF NOT EXISTS incidents (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id                  TEXT        NOT NULL,
    cluster_id                TEXT        NOT NULL DEFAULT 'local',
    title                     TEXT        NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    summary                   TEXT        NOT NULL DEFAULT '' CHECK (length(summary) <= 10000),
    status                    TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    window_start              TIMESTAMPTZ NOT NULL,
    window_end                TIMESTAMPTZ,
    evidence_bytes            BIGINT      NOT NULL DEFAULT 0
                                  CHECK (evidence_bytes >= 0 AND evidence_bytes <= 10485760),
    evidence_count            INTEGER     NOT NULL DEFAULT 0
                                  CHECK (evidence_count >= 0 AND evidence_count <= 500),
    scope_count               INTEGER     NOT NULL DEFAULT 0
                                  CHECK (scope_count >= 0 AND scope_count <= 20),
    retention_days_at_capture INTEGER     NOT NULL CHECK (retention_days_at_capture BETWEEN 1 AND 3650),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at                 TIMESTAMPTZ,
    CHECK (window_end IS NULL OR window_end >= window_start),
    CHECK (status <> 'closed' OR closed_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_incidents_owner_created  ON incidents (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_retention      ON incidents (created_at);
CREATE INDEX IF NOT EXISTS idx_incidents_cluster_status ON incidents (cluster_id, status, created_at DESC);

COMMENT ON TABLE incidents IS
    'Release D incident records. owner_id is auth.User.ID (provider-qualified TEXT, NOT a local_users FK) per the platform precedent set by nc_reads.user_id. Visibility = owner OR incident_grants row, AND per-item current Kubernetes authorization at read time (Q1). Retention: whole-incident DELETE by created_at using the configured window (default 30d); lowering the config retroactively deletes — see NOTES.txt.';

CREATE TABLE IF NOT EXISTS incident_evidence (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id         UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    evidence_kind       TEXT        NOT NULL
                            CHECK (evidence_kind IN ('diagnostic_check', 'object_summary', 'event_list')),
    mode                TEXT        NOT NULL CHECK (mode IN ('snapshot', 'live_link')),
    cluster_id          TEXT        NOT NULL,
    api_group           TEXT        NOT NULL DEFAULT '',
    resource            TEXT        NOT NULL,
    source_kind         TEXT        NOT NULL,
    namespace           TEXT        NOT NULL DEFAULT '',
    name                TEXT        NOT NULL,
    source_uid          TEXT        NOT NULL DEFAULT '',
    resource_version    TEXT        NOT NULL DEFAULT '',
    secret_derived      BOOLEAN     NOT NULL DEFAULT false,
    source_observed_at  TIMESTAMPTZ,
    collected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completeness        TEXT        NOT NULL
                            CHECK (completeness IN ('complete', 'partial', 'failed', 'forbidden', 'timed_out')),
    completeness_detail TEXT        NOT NULL DEFAULT '' CHECK (length(completeness_detail) <= 2000),
    redaction           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    payload             JSONB,
    payload_bytes       INTEGER     NOT NULL DEFAULT 0
                            CHECK (payload_bytes >= 0 AND payload_bytes <= 1048576),
    capture_key         TEXT        NOT NULL,
    CHECK ((mode = 'snapshot'  AND payload IS NOT NULL)
        OR (mode = 'live_link' AND payload IS NULL AND payload_bytes = 0))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_incident_evidence_dedup
    ON incident_evidence (incident_id, capture_key);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_timeline
    ON incident_evidence (incident_id, collected_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_scope
    ON incident_evidence (incident_id, cluster_id, api_group, resource, namespace);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_provenance
    ON incident_evidence (cluster_id, source_uid);
CREATE INDEX IF NOT EXISTS idx_incident_evidence_secret
    ON incident_evidence (incident_id) WHERE secret_derived;

COMMENT ON TABLE incident_evidence IS
    'Append-only evidence. Provenance is keyed on (cluster_id, source_uid) — NEVER on (namespace, name), so a deleted-and-recreated object with a reused name inherits nothing (KTD3/R1). mode=snapshot stores an immutable redacted payload; mode=live_link stores only a reference resolved at read time. Values from core/secrets are never persisted; secret_derived rows additionally require get on secrets in the same namespace at read time.';

CREATE TABLE IF NOT EXISTS incident_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    author_id   TEXT        NOT NULL,
    body        TEXT        NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    revision    INTEGER     NOT NULL DEFAULT 1 CHECK (revision >= 1),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_incident_notes_incident ON incident_notes (incident_id, created_at);

CREATE TABLE IF NOT EXISTS incident_note_revisions (
    note_id    UUID        NOT NULL REFERENCES incident_notes(id) ON DELETE CASCADE,
    revision   INTEGER     NOT NULL,
    author_id  TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, revision)
);

COMMENT ON TABLE incident_note_revisions IS
    'Prior bodies of an edited note. Written in the same transaction as the UPDATE; an optimistic-revision mismatch aborts with zero rows affected and the API returns 409 note_revision_conflict.';

CREATE TABLE IF NOT EXISTS incident_grants (
    incident_id  UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    grantee_id   TEXT        NOT NULL,
    granted_by   TEXT        NOT NULL,
    can_annotate BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, grantee_id)
);

CREATE INDEX IF NOT EXISTS idx_incident_grants_grantee ON incident_grants (grantee_id, incident_id);

COMMENT ON TABLE incident_grants IS
    'Explicit, revocable collaborator grants. A grant conveys standing to ask, never Kubernetes authority: every evidence read still re-checks the caller current authorization for the item stored scope (Q1 P2).';
```

`000020_create_incidents.down.sql` — drop in reverse dependency order, with the operator warning inline (`000016.down.sql` sets the precedent of a runbook comment in the down file):

```sql
-- DESTRUCTIVE. Dropping these tables permanently destroys every captured
-- incident, its evidence, notes and note revisions, and all collaborator
-- grants. There is no export-on-rollback path. Per the master plan's
-- Migration and Rollback section, a binary rollback must NOT require
-- dropping evidence tables: to roll the backend binary back, deploy the
-- older image and LEAVE these tables in place. Only run this file when the
-- operator has consciously decided to destroy incident history. See
-- migrations/NOTES.txt (000020) for the export procedure to run first.
DROP TABLE IF EXISTS incident_grants;
DROP TABLE IF EXISTS incident_note_revisions;
DROP TABLE IF EXISTS incident_notes;
DROP TABLE IF EXISTS incident_evidence;
DROP TABLE IF EXISTS incidents;
```

**Why history is keyed by stable identity and never cascaded by name.** `idx_incident_evidence_provenance` is `(cluster_id, source_uid)`. There is deliberately **no** index or constraint on `(cluster_id, namespace, name)` alone, and no `ON DELETE` behaviour tied to a Kubernetes object at all — Postgres has no knowledge of cluster objects, so "cascade on resource deletion" is not even expressible. This is the point: a `Deployment` named `api` deleted on Monday and a new `Deployment` named `api` created on Tuesday are different subjects with different UIDs. Any query, join, or UI grouping that keys on name would silently attribute Monday's failure evidence to Tuesday's object, which is precisely the R1 failure mode ("a name reused for a different object must not inherit prior-object evidence"). Name and namespace are stored for *display and re-authorization scope* only; `source_uid` is the identity. Where a source genuinely has no UID, `identityWeak` is set and the UI must not imply object identity.

### 3.2 The evidence envelope

Domain types live in `backend/internal/incidents/` (U22b defines them; U21b's store rows are primitive-only, so there is no import cycle — the store layer mirrors `ESOSyncHistoryEntry`'s pattern of a flat row struct and the `incidents` package maps to/from it).

```go
type Completeness string

const (
    CompletenessComplete  Completeness = "complete"
    CompletenessPartial   Completeness = "partial"
    CompletenessFailed    Completeness = "failed"
    CompletenessForbidden Completeness = "forbidden"
    CompletenessTimedOut  Completeness = "timed_out"
)

type EvidenceMode string

const (
    // ModeSnapshot stores an immutable, redacted payload. What it says is
    // what was observed at collectedAt; it never changes if the live object does.
    ModeSnapshot EvidenceMode = "snapshot"
    // ModeLiveLink stores only a reference. It is resolved against the live
    // cluster at read time under the reader's current authorization, and may
    // legitimately 404 (deleted) or 403 (revoked) later.
    ModeLiveLink EvidenceMode = "live_link"
)

type SourceRef struct {
    ClusterID       string `json:"clusterId"`
    APIGroup        string `json:"apiGroup"`        // "" for core
    Resource        string `json:"resource"`        // plural, lowercase — the SAR resource
    Kind            string `json:"kind"`            // display only
    Namespace       string `json:"namespace"`
    Name            string `json:"name"`
    UID             string `json:"uid,omitempty"`
    ResourceVersion string `json:"resourceVersion,omitempty"`
    IdentityWeak    bool   `json:"identityWeak,omitempty"` // true when UID is unavailable
}

type RedactionMeta struct {
    Applied       bool     `json:"applied"`
    Rules         []string `json:"rules,omitempty"`   // stable rule ids, e.g. "secret-values", "last-applied-config"
    FieldsRemoved int      `json:"fieldsRemoved"`
    Truncated     bool     `json:"truncated"`
    SecretDerived bool     `json:"secretDerived"`
}

type Evidence struct {
    ID                 string          `json:"id"`
    IncidentID         string          `json:"incidentId"`
    EvidenceKind       string          `json:"evidenceKind"` // diagnostic_check | object_summary | event_list
    Mode               EvidenceMode    `json:"mode"`
    Source             SourceRef       `json:"source"`
    SourceObservedAt   *time.Time      `json:"sourceObservedAt,omitempty"` // from the source itself
    CollectedAt        time.Time       `json:"collectedAt"`                // server clock at capture
    Completeness       Completeness    `json:"completeness"`
    CompletenessDetail string          `json:"completenessDetail,omitempty"`
    Redaction          RedactionMeta   `json:"redaction"`
    Payload            json.RawMessage `json:"payload,omitempty"` // snapshot only
    PayloadBytes       int             `json:"payloadBytes"`
    CaptureKey         string          `json:"-"`
}

// WithheldEvidence is the ONLY shape emitted for an item the caller may not
// read. It deliberately carries no scope fields — namespace, name, kind, and
// resource are themselves disclosure (Q1 P4).
type WithheldEvidence struct {
    ID             string    `json:"id"`
    EvidenceKind   string    `json:"evidenceKind"`
    CollectedAt    time.Time `json:"collectedAt"`
    Withheld       bool      `json:"withheld"` // always true
    WithheldReason string    `json:"withheldReason"` // "forbidden" | "authorization_check_unavailable"
}
```

**`sourceObservedAt` vs `collectedAt`.** `sourceObservedAt` is when the *cluster* observed the fact (an Event's `lastTimestamp`, a condition's `lastTransitionTime`, a check's `observedAt`). `collectedAt` is when k8sCenter wrote it down. They routinely differ by minutes and the timeline must render both — a 40-minute-old event captured 10 seconds ago is not a 10-second-old event. `sourceObservedAt` is nullable because some sources genuinely have none; the UI then shows "observation time unknown" rather than falling back to `collectedAt`, which would be a fabricated fact.

**Snapshot vs live link.** Snapshots are the default and the only mode in the first release for `diagnostic_check`, `object_summary`, and `event_list`. `live_link` is defined now (schema + envelope) so that the deferred Loki-excerpt and alert-snapshot adapters can use it without a migration, and so the UI's "retained / live" distinction (R15) is built and tested from day one against at least one live-link item — the incident's own target resource reference is stored as a `live_link` `object_summary` alongside the snapshot, giving the UI a real, testable pair.

### 3.3 The normalized check-result contract (U20) and its compatibility adapter

New file `backend/internal/diagnostics/check_result.go`. **`Result`, its JSON tags, `CheckFunc`, and `RunDiagnostics`'s signature are frozen — no edits.**

```go
// CheckStatus is the normalized tri-state. Unlike the legacy Result.Status
// ("pass"/"warn"/"fail"), it can say "we could not tell".
type CheckStatus string

const (
    CheckStatusPass         CheckStatus = "pass"
    CheckStatusFail         CheckStatus = "fail"
    CheckStatusInconclusive CheckStatus = "inconclusive"
)

type InconclusiveReason string

const (
    ReasonPermissionDenied   InconclusiveReason = "permission_denied"
    ReasonSourceUnavailable  InconclusiveReason = "source_unavailable"
    ReasonTimedOut           InconclusiveReason = "timed_out"
    ReasonNoRelatedObjects   InconclusiveReason = "no_related_objects"
)

// CheckID is the stable identity of a check across releases. Format:
// "<producer>/<slug>" — e.g. "diagnostics/crashloopbackoff". Stable ids are
// what let incidents (Release D) and change verification (Release E, U26-U31)
// reference the same check without depending on a display name.
type CheckID string

type CheckEvidenceItem struct {
    Label    string            `json:"label"`
    Kind     string            `json:"kind"`
    Name     string            `json:"name"`
    UID      string            `json:"uid,omitempty"`
    Observed map[string]string `json:"observed,omitempty"` // bounded, redacted key/value facts
}

type CheckResult struct {
    CheckID      CheckID             `json:"checkId"`
    Status       CheckStatus         `json:"status"`
    Severity     Severity            `json:"severity"`
    Summary      string              `json:"summary"`
    Detail       string              `json:"detail,omitempty"`
    Remediation  string              `json:"remediation,omitempty"`
    Source       CheckSourceRef      `json:"source"`
    ObservedAt   time.Time           `json:"observedAt"`
    Evidence     []CheckEvidenceItem `json:"evidence,omitempty"`
    Inconclusive InconclusiveReason  `json:"inconclusive,omitempty"`

    // legacyRuleName and legacyStatus preserve the pre-normalization wire
    // values verbatim so Denormalize is exact. Unexported: they are an
    // internal compatibility detail, never part of the new JSON surface.
    legacyRuleName string
    legacyStatus   string
}

type CheckSourceRef struct {
    ClusterID string `json:"clusterId"`
    APIGroup  string `json:"apiGroup"`
    Resource  string `json:"resource"`
    Kind      string `json:"kind"`
    Namespace string `json:"namespace"`
    Name      string `json:"name"`
    UID       string `json:"uid,omitempty"`
}

// Normalize converts legacy Results into the reusable contract. observedAt is
// supplied by the caller (the handler passes time.Now() at the moment
// RunDiagnostics returned) because Result carries no timestamp.
func Normalize(clusterID string, target *DiagnosticTarget, observedAt time.Time, results []Result) []CheckResult

// Denormalize is the compatibility adapter. For any rs produced by
// RunDiagnostics, Denormalize(Normalize(..., rs)) must equal rs under
// reflect.DeepEqual. This is the contract that keeps every consumer in
// section 1.6 byte-compatible.
func Denormalize(cs []CheckResult) []Result
```

Supporting edits:
- `diagnostics.go` — add `dependsOn []string` to `ruleEntry`; add `Limitations []Limitation` to `DiagnosticTarget`; have `Resolve` append a `Limitation{Kind: "pods"|"replicasets", Reason: "permission_denied"}` when the corresponding `RelatedRBAC` gate denies. `RunDiagnostics` is untouched.
- `rules.go` — add the dependency argument to the six `registerRule` calls (`{"pods"}` for the four pod-derived rules and `ZeroEndpoints`; `nil` for `PendingPVC`; `{"pods","replicasets"}` where the Deployment→ReplicaSet→Pod chain matters). The six `check*` function bodies are unchanged.

`Normalize` then downgrades a `pass` to `inconclusive{permission_denied}` when the rule's `dependsOn` intersects `target.Limitations`. `Denormalize` restores `legacyStatus`, so the legacy wire is unaffected.

**Named consumers this adapter protects** — the full list is §1.6. In one line: five Go files inside `internal/diagnostics` (two of them existing tests), five web files, and twelve mobile files plus four mobile tests, all decoding `{ruleName, status, severity, message, detail?, remediation?, links?}`.

### 3.4 The collector: bounded concurrency, per-source timeouts, honest partials

`backend/internal/incidents/collector.go` (U22b).

```go
type SourceResult struct {
    Items        []Evidence
    Completeness Completeness
    Detail       string
}

type Source interface {
    // ID is a stable adapter identity used in logs, metrics, and partial-record
    // reporting. e.g. "diagnostics", "object", "events".
    ID() string
    Collect(ctx context.Context, req CaptureRequest) (SourceResult, error)
}

type Collector struct {
    sources        []Source
    maxConcurrency int           // Config.Incidents.MaxConcurrency, default 4
    sourceTimeout  time.Duration // Config.Incidents.SourceTimeout,  default 5s
    captureTimeout time.Duration // Config.Incidents.CaptureTimeout, default 20s
    redactor       *Redactor
    logger         *slog.Logger
}

func (c *Collector) Capture(ctx context.Context, req CaptureRequest) (CaptureReport, error)
```

Rules, all testable:

1. **Bounded concurrency.** One `errgroup.Group` from `errgroup.WithContext(captureCtx)` with `g.SetLimit(c.maxConcurrency)`. Each source is launched with **`recoverutil.Go(g, c.logger, "incidents capture "+src.ID(), func() error { ... })`**. A panicking adapter becomes a `failed` source, not a dead process.
2. **Per-source timeout.** Each worker derives `srcCtx, cancel := context.WithTimeout(captureCtx, c.sourceTimeout); defer cancel()`. A source hitting its deadline yields `Completeness: timed_out`.
3. **Whole-capture deadline.** `captureCtx, cancel := context.WithTimeout(ctx, c.captureTimeout)`.
4. **A timing-out source never discards a successful one.** Workers write into a pre-sized `results []SourceResult` slice indexed by position — never a shared map, never an append — so there is no lock and no lost write. `recoverutil.Go` returns an error only on panic; a source failure is recorded **in its slot** and returns `nil`, so `errgroup`'s context is never cancelled by one adapter's failure. This is the single most important behavioural rule: **`g.Wait()`'s error is never allowed to void the whole capture.** The report's overall completeness is `complete` only if every source is `complete`; otherwise `partial`, with a per-source breakdown.
5. **Resilience conventions (verbatim from `docs/solutions/backend-resilience-conventions.md` L46–L59).** Any `defer wg.Done()` or counted channel send is registered on the goroutine **before** entering the wrapped closure, never inside `fn`. The collector's design avoids `sync.WaitGroup` entirely (errgroup + indexed slice) specifically to make this class of bug unreachable; the rule is restated in the file's doc comment for anyone who later adds a raw goroutine.
6. **Cancellation.** Client disconnect cancels `ctx` → `captureCtx` → every `srcCtx`. Nothing is written to the DB: the store write happens strictly **after** `g.Wait()` returns, and is skipped on `ctx.Err() != nil`. A cancelled capture leaves no partial incident state.
7. **Redaction before size accounting before write.** Order is fixed: collect → redact → measure → enforce P14 → marshal → insert. Measuring before redaction would let a rejected-for-size item be the *unredacted* one; redacting after measuring would make byte accounting a lie.
8. **First-release adapters** (what is realistically readable today):
   - `diagnostics` — SAR-gate `get` on the target kind, run `Resolve` + `RunDiagnostics` + `Normalize`. Everything it needs already exists.
   - `object` — impersonated `GET` of the target object via `ClientFactory.ClientForUser`, projected to an allowlisted summary (kind, name, namespace, uid, resourceVersion, labels, ownerReferences, `.status.conditions`, replica counts, container images). **Never** the whole object; never `.data`; never annotations except an allowlist. Emits both a `snapshot` and a paired `live_link`.
   - `events` — SAR-gate `list` on `("", "events", ns)`, then impersonated `CoreV1().Events(ns).List(...)` with `FieldSelector` on `involvedObject.uid` where available, capped at 200 events and 1 MiB after redaction. **Not** the informer cache: the cache is not impersonated, and `DiagnosticTarget.Events` is permanently nil (§1.1).
   **Deferred, explicitly not in this release:** Loki log excerpts, Alertmanager snapshots, change receipts (Release E), GitOps/integration history. Each needs its own permission, redaction, bound, and stale-data contract per the master plan's own rule.

### 3.5 Export filtering

One function, used by the detail read, the list, the counts, and both export formats:

```go
// FilterEvidence applies Q1 P4/P10/P11 to a batch. It returns the readable
// items and a per-reason withheld tally. It is the ONLY place authorization
// filtering of evidence is implemented; every representation calls it.
func (h *Handler) FilterEvidence(ctx context.Context, u *auth.User, items []store.IncidentEvidenceRow) ([]Evidence, []WithheldEvidence, error)
```

Export adds nothing to it: `HandleExport` calls `FilterEvidence`, then serializes. Credentials cannot appear because none are stored (P11.1). HTML cannot be smuggled because there is no HTML output path and untrusted strings are emitted as JSON values or inside escaped fences (P12). A "print view" or HTML export is explicitly deferred — it would reintroduce the smuggling surface and needs its own sanitization design.

### 3.6 Fuzzing (CLAUDE.md requirement)

| Unit | Target | Package | Oracles |
|---|---|---|---|
| **U22a** | `FuzzIncidentRedaction` | `./internal/incidents/` | **A** no panic on arbitrary `unstructured`/bytes/strings; **D** no plaintext leak — for any input containing a marked secret value, the redacted output never contains it, `StringData` is masked, and `kubectl.kubernetes.io/last-applied-configuration` is absent; **T** truncation is flagged, never silent |
| **U22b** | `FuzzEventProjection` | `./internal/incidents/` | **A** no panic projecting adversarial `corev1.Event` (huge messages, invalid UTF-8, control characters, deeply nested `involvedObject`); **B** output is always ≤ the configured item bound; **D** no unredacted field survives |

Both units add their matrix row to `.github/workflows/fuzz.yml` in the same PR — the `-list` drift guard means a target without a row is silently never fuzzed. Seeds are teeth-via-mutation per `docs/solutions/backend-resilience-conventions.md`: start from real `maskedSecret` inputs and mutate. No other Release D unit introduces a parse seam: U20 does not parse untrusted bytes (it re-shapes already-typed rule output), and U21/U23/U24/U25 do not parse untrusted resource text.

---

## 4. Per-Unit Sections

Notation: **Files** lists every touched path including tests; the cap is five. **Verification** is Agent Directive 4 repo-wide, never scoped.

---

### U20 — Normalize reusable diagnostic check results

**Branch:** `feat/u20-normalized-check-results` · **PR:** `feat(diagnostics): add normalized check-result contract with byte-compatible legacy adapter`

**Depends on:** nothing. **Covers:** R17, KTD8.

**Files (4):**
1. `backend/internal/diagnostics/check_result.go` — new
2. `backend/internal/diagnostics/check_result_test.go` — new
3. `backend/internal/diagnostics/diagnostics.go` — edit
4. `backend/internal/diagnostics/rules.go` — edit

*(Master-plan list confirmed correct. No fuzz target here — see §3.6.)*

**Steps:**
1. `check_result.go` — declare `CheckStatus`, `InconclusiveReason`, `CheckID`, `CheckSourceRef`, `CheckEvidenceItem`, `CheckResult` exactly as §3.3. Add `var checkIDForRule = map[string]CheckID{"CrashLoopBackOff": "diagnostics/crashloopbackoff", "ImagePullBackOff": "diagnostics/imagepullbackoff", "PendingPod": "diagnostics/pendingpod", "ReplicaMismatch": "diagnostics/replicamismatch", "ZeroEndpoints": "diagnostics/zeroendpoints", "PendingPVC": "diagnostics/pendingpvc"}` with a doc comment stating these ids are a **frozen wire contract** — renaming a rule must not change its `CheckID`.
2. `check_result.go` — implement `Normalize(clusterID string, target *DiagnosticTarget, observedAt time.Time, results []Result) []CheckResult`. Map `Result.Links` → `[]CheckEvidenceItem`. Carry `legacyRuleName = r.RuleName`, `legacyStatus = r.Status`. Populate `Source` from `target.Kind/Name/Namespace` plus `clusterID`; resolve `Resource` through the same `kindToResource` mapping (move it from `handler.go` to `check_result.go` as an exported-to-package `resourceForKind` and have `handler.go` reference it, or duplicate it with a comment — prefer moving, it stays a 4-file change).
3. `check_result.go` — implement `Denormalize(cs []CheckResult) []Result` restoring `RuleName`, `Status`, `Severity`, `Message`, `Detail`, `Remediation`, `Links` verbatim from the carried legacy fields.
4. `diagnostics.go` — add `type Limitation struct { Kind string; Reason InconclusiveReason }`; add `Limitations []Limitation` to `DiagnosticTarget`; in `Resolve`, append `{Kind:"pods", Reason:ReasonPermissionDenied}` when `!related.allowsPods()` and `{Kind:"replicasets", ...}` when the Deployment path is skipped by `!related.allowsReplicaSets()`. Add `dependsOn []string` to `ruleEntry` and a variadic-free `registerRule(name string, severity Severity, appliesTo []string, dependsOn []string, check CheckFunc)`. **Do not touch** `Result`, `CheckFunc`, `RunDiagnostics`, `runSafeCheck`, or `statusOrder`.
5. `rules.go` — update the six `registerRule` calls with `dependsOn`: `{"pods"}` for `CrashLoopBackOff`, `ImagePullBackOff`, `PendingPod`, `ZeroEndpoints`; `{"pods","replicasets"}` is implied for Deployment targets and is handled by matching either limitation kind, so `{"pods"}` suffices plus `ReplicaMismatch` takes `{"pods","replicasets"}`; `PendingPVC` takes `nil`. No check-function bodies change.
6. `Normalize` downgrades `Status: "pass"` to `CheckStatusInconclusive` + `Inconclusive: ReasonPermissionDenied` when the rule's `dependsOn` intersects `target.Limitations`. `"warn"`/`"fail"` are never downgraded. Timed-out results (message `Rule %q timed out after 5s`) map to `CheckStatusInconclusive` + `ReasonTimedOut` while `legacyStatus` stays `"fail"`.

**Tests — `check_result_test.go`:**

| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestDenormalizeRoundTripsEveryRule` | *"existing diagnostic clients remain compatible"* — table over all six rules × {pass, warn, fail} × with/without links; asserts `reflect.DeepEqual(Denormalize(Normalize(...)), input)` |
| `TestNormalizePreservesLegacyStatusUnderLimitation` | *"Existing rules preserve their outcomes"* — with `Limitations` set, `CheckResult.Status == inconclusive` **and** `Denormalize(...)[i].Status == "pass"` |
| `TestNormalizeMarksPodDeniedChecksInconclusive` | *"missing permissions/metrics become inconclusive where appropriate"* (revoked permission) |
| `TestNormalizeMarksTimedOutCheckInconclusive` | cancellation / timeout |
| `TestNormalizeLeavesPVCCheckConclusiveUnderPodDenial` | `dependsOn: nil` rules are not falsely downgraded |
| `TestCheckIDsAreStableAndUnique` | frozen wire contract; guards a rule rename |
| `TestNormalizeCarriesSourceIdentity` | deleted/recreated resource — `Source.UID` from `target.Object`, empty ⇒ no fabricated identity |
| `TestNormalizeNeverEmbedsSecretValues` | *"Check serialization never embeds raw Secret values"* — feeds a target whose object carries `stringData` and `last-applied-configuration`; asserts neither appears in `json.Marshal(Normalize(...))` |
| `TestNormalizeIsNilSafe` | nil target, nil results, empty limitations |

Also: `rules_test.go` and `rbac_p3_test.go` must pass **unmodified**. If either needs editing, the change is not additive and must be reworked.

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
```

**Exit / done means:**
- `git diff` shows zero changes to `Result`'s definition or JSON tags and zero changes to `RunDiagnostics`'s signature.
- `rules_test.go` and `rbac_p3_test.go` are untouched and green.
- `Denormalize∘Normalize` round-trip test passes for all six rules.
- No frontend or mobile file is touched.
- Checks can be referenced by stable `CheckID` by U22b and by Release E.

---

### U21a — Incident schema, incident and note stores

**Branch:** `feat/u21a-incident-schema` · **PR:** `feat(store): add incidents schema (000020) with incident and revisioned-note persistence`

**Depends on:** Q1 (resolved). **Covers:** R14, R16; KTD8.

**Split note:** the master plan's U21 was one 4-file unit covering four tables, size accounting, append-only evidence, and grants. That is a single ~900-LOC security-critical PR. Split into U21a (schema + incidents + notes) and U21b (evidence + grants). Also adds `NOTES.txt`, which the master plan omits (Correction C2).

**Files (5):**
1. `backend/internal/store/migrations/000020_create_incidents.up.sql` — new
2. `backend/internal/store/migrations/000020_create_incidents.down.sql` — new
3. `backend/internal/store/incidents.go` — new
4. `backend/internal/store/incidents_test.go` — new
5. `backend/internal/store/migrations/NOTES.txt` — edit (append a `000020_create_incidents` section)

**Steps:**
1. Write both migration files exactly as §3.1. **The sequence is `000020`.** Do not renumber; 000018, 000019, 000021, 000022 belong to other tracks.
2. `NOTES.txt` — append a section covering: the destructive-rollback constraint (deploy the older image, leave the tables); the retroactive-retention consequence of lowering `KUBECENTER_INCIDENTS_RETENTIONDAYS` (P13); the inspection query `SELECT id, owner_id, cluster_id, created_at, evidence_count, evidence_bytes FROM incidents ORDER BY created_at;`; and the fact that `owner_id` is `auth.User.ID`, not a `local_users` FK, so deleting a local user does not delete their incidents.
3. `incidents.go` — mirror `eso_history.go` exactly:
```go
type IncidentRow struct {
    ID                     uuid.UUID
    OwnerID                string
    ClusterID              string
    Title                  string
    Summary                string
    Status                 string
    WindowStart            time.Time
    WindowEnd              *time.Time
    EvidenceBytes          int64
    EvidenceCount          int
    ScopeCount             int
    RetentionDaysAtCapture int
    CreatedAt              time.Time
    UpdatedAt              time.Time
    ClosedAt               *time.Time
}

type IncidentNoteRow struct {
    ID         uuid.UUID
    IncidentID uuid.UUID
    AuthorID   string
    Body       string
    Revision   int
    CreatedAt  time.Time
    UpdatedAt  time.Time
}

type IncidentStore struct{ pool *pgxpool.Pool }
func NewIncidentStore(pool *pgxpool.Pool) *IncidentStore

func (s *IncidentStore) Create(ctx context.Context, r IncidentRow) (uuid.UUID, error)
// Get returns (nil, nil) when the id does not exist — callers MUST distinguish
// that from an error so a transient DB fault never renders as "not found".
func (s *IncidentStore) Get(ctx context.Context, id uuid.UUID) (*IncidentRow, error)
// ListVisible returns incidents where owner_id = $1 OR an incident_grants row
// exists for $1. Ordering is (created_at DESC, id DESC) for a stable cursor.
func (s *IncidentStore) ListVisible(ctx context.Context, userID string, limit int, cursor string) ([]IncidentRow, string, error)
func (s *IncidentStore) Update(ctx context.Context, id uuid.UUID, ownerID, title, summary, status string) error
func (s *IncidentStore) Delete(ctx context.Context, id uuid.UUID, ownerID string) error
// Cleanup deletes incidents older than retentionDays. Bounded by a 5-minute
// context (the eso_history.cleanupTimeout precedent). Cascades to evidence,
// notes, revisions and grants.
func (s *IncidentStore) Cleanup(ctx context.Context, retentionDays int) (int64, error)

func (s *IncidentStore) CreateNote(ctx context.Context, incidentID uuid.UUID, authorID, body string) (IncidentNoteRow, error)
func (s *IncidentStore) ListNotes(ctx context.Context, incidentID uuid.UUID) ([]IncidentNoteRow, error)
// UpdateNote is optimistic: it UPDATEs WHERE id=$1 AND revision=$2 and inserts
// the prior body into incident_note_revisions in the SAME transaction.
// Zero rows affected => ErrNoteRevisionConflict.
func (s *IncidentStore) UpdateNote(ctx context.Context, noteID uuid.UUID, authorID, body string, expectedRevision int) (IncidentNoteRow, error)
func (s *IncidentStore) DeleteNote(ctx context.Context, noteID uuid.UUID, authorID string) error

var ErrNoteRevisionConflict = errors.New("note revision conflict")
var ErrNotOwner = errors.New("not the incident owner")
```
4. `Update` and `Delete` carry `ownerID` in the `WHERE` clause so an ownership violation is impossible even if a handler check is later dropped — defence in depth. Zero rows ⇒ `ErrNotOwner`.
5. `Cleanup` uses the `eso_history.go` shape verbatim: reject `retentionDays < 1`, wrap in `context.WithTimeout(ctx, 5*time.Minute)`, `DELETE FROM incidents WHERE created_at < NOW() - $1 * INTERVAL '1 day'`, return `tag.RowsAffected()`.

**Tests — `incidents_test.go`** (per Correction C5, one file, two halves):

*Always-run (pure):*
| Test | Case |
|---|---|
| `TestIncidentRowValidationRejectsOverlongTitle` | size bounds before the write |
| `TestIncidentRowValidationRejectsInvertedWindow` | `window_end < window_start` |
| `TestRetentionDaysClamp` | retention configurability bounds `[1, 3650]` |
| `TestCleanupRejectsNonPositiveRetention` | mirrors `ESOHistoryStore.Cleanup`'s guard |
| `TestListVisibleCursorRoundTrip` | stable pagination cursor encode/decode |

*Integration (`t.Skip` unless `KUBECENTER_TEST_DATABASE_URL` is set), each opening a pool, running migrations, and using a transaction rolled back at the end:*
| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestIncidentOwnerBoundary` | *"Owner/grant boundaries"* — cross-user access: user B's `Update`/`Delete` return `ErrNotOwner`; B's `ListVisible` omits A's incident |
| `TestListVisibleIncludesGrantedIncident` | grant makes an incident listable (evidence gating is U21b/U23a) |
| `TestNoteEditConflict` | *"note edit conflicts"* — concurrent `UpdateNote` with a stale revision ⇒ `ErrNoteRevisionConflict`, prior body preserved in `incident_note_revisions` |
| `TestNoteRevisionHistoryIsAppendOnly` | revision N-1 body still readable after edit |
| `TestDeleteCascades` | deleting an incident removes notes, revisions, grants |
| `TestCleanupPreservesNonExpired` | *"expired evidence cleanup preserves non-expired records"* — retention |
| `TestCleanupIsBounded` | cancelled parent ctx aborts cleanly |
| `TestGetMissingReturnsNilNil` | unavailable-DB vs not-found distinction |
| `TestTransactionFailurePreservesConsistency` | *"transaction failure preserve consistency"* — forced rollback mid-`UpdateNote` leaves revision unchanged |
| `TestMigrationRoundTrip` | up → populate → down → up leaves unrelated tables (`audit_logs`, `clusters`) intact |

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
# with a DB, locally:
KUBECENTER_TEST_DATABASE_URL="postgresql://k8scenter:k8scenter@localhost:5432/k8scenter?sslmode=disable" go test ./internal/store/ -count=1 -v
```

**Exit / done means:** migration applies cleanly against a fresh and a populated DB and rolls back without touching other tables; owner boundary enforced in SQL, not only in Go; note edits are revisioned and conflict-safe; `NOTES.txt` documents both operator footguns; `go test ./...` passes with **and** without a database configured.

---

### U21b — Evidence and grant persistence

**Branch:** `feat/u21b-incident-evidence-store` · **PR:** `feat(store): append-only incident evidence with transactional size accounting and grants`

**Depends on:** U21a. **Covers:** R15, R16; KTD3, KTD8.

**Files (3):**
1. `backend/internal/store/incident_evidence.go` — new
2. `backend/internal/store/incident_grants.go` — new
3. `backend/internal/store/incident_evidence_test.go` — new

**Steps:**
1. `incident_evidence.go` — `IncidentEvidenceRow` mirroring the DDL one-to-one (primitive fields only; `Redaction` and `Payload` as `[]byte`/`json.RawMessage`; no import of `internal/incidents`).
2. Implement:
```go
type IncidentEvidenceStore struct{ pool *pgxpool.Pool }
func NewIncidentEvidenceStore(pool *pgxpool.Pool) *IncidentEvidenceStore

// InsertBatch appends evidence for one capture. It runs in a single
// transaction:
//   1. SELECT evidence_bytes, evidence_count, scope_count
//        FROM incidents WHERE id = $1 FOR UPDATE
//   2. reject with ErrEvidenceLimit if adding this batch would exceed
//      maxIncidentBytes / 500 items / 20 distinct scopes
//   3. INSERT ... ON CONFLICT (incident_id, capture_key) DO NOTHING
//      (idempotent re-capture of an identical observation)
//   4. UPDATE incidents SET evidence_bytes = evidence_bytes + <actually
//      inserted bytes>, evidence_count = ..., scope_count = ..., updated_at = now()
// Only rows actually inserted are counted, so a deduplicated re-capture does
// not inflate the running total.
func (s *IncidentEvidenceStore) InsertBatch(ctx context.Context, incidentID uuid.UUID, rows []IncidentEvidenceRow, maxItemBytes, maxIncidentBytes int) (inserted int, err error)

func (s *IncidentEvidenceStore) ListByIncident(ctx context.Context, incidentID uuid.UUID, limit int, cursor string) ([]IncidentEvidenceRow, string, error)
// DistinctScopes returns the deduplicated (cluster_id, api_group, resource,
// namespace) tuples for one incident. The handler uses it to run at most one
// SAR per scope (Q1 P5).
func (s *IncidentEvidenceStore) DistinctScopes(ctx context.Context, incidentID uuid.UUID) ([]EvidenceScope, error)

var ErrEvidenceLimit = errors.New("incident evidence limit exceeded")
```
3. **No `UpdateEvidence`, no `DeleteEvidence(id)`.** The package exposes no per-row mutation at all (P15). Whole-incident removal happens through `incidents` cascade.
4. `incident_grants.go` — `IncidentGrantRow`; `AddGrant`, `RemoveGrant`, `ListGrants`, `HasGrant(ctx, incidentID, userID) (bool, error)`. `AddGrant` takes `ownerID` and asserts it in the `WHERE` via a subquery against `incidents.owner_id` so a non-owner cannot grant even if a handler check is dropped. Self-grant is a no-op (`ON CONFLICT DO NOTHING`).

**Tests — `incident_evidence_test.go`** (same two-half structure):

*Always-run:* `TestCaptureKeyIsDeterministic`, `TestCaptureKeyChangesWithObservedAt` (a new observation is a new row; an identical one is deduped), `TestItemBytesRejectedAboveOneMiB`, `TestScopeDedupIsOrderIndependent`.

*Integration:*
| Test | Case |
|---|---|
| `TestInsertBatchEnforcesIncidentByteCeiling` | *"Payload limits are enforced before DB write"* — P14 |
| `TestInsertBatchIsIdempotentOnDuplicateCapture` | *"duplicate captures"*; repeated clicks do not duplicate |
| `TestInsertBatchDedupDoesNotInflateByteCount` | the subtle accounting bug: only inserted rows count |
| `TestEvidenceHasNoUpdatePath` | append-only — asserts the store exposes no update/delete-by-id (compile-level + reflection over method set) |
| `TestDistinctScopesBoundedAtTwenty` | P5 scope cap |
| `TestRecreatedNameDoesNotInheritEvidence` | deleted/recreated resource — two rows, same `namespace`/`name`, different `source_uid`; queries by UID never cross |
| `TestGrantAddRemoveByNonOwnerFails` | cross-user access |
| `TestGrantRemovalIsImmediate` | revoked collaborator |
| `TestLiveLinkRowRejectsPayload` | the `mode`/`payload` CHECK constraint holds |
| `TestSnapshotRowRequiresPayload` | same, other direction |
| `TestInsertBatchRollsBackOnConstraintViolation` | transaction failure preserves consistency |
| `TestCancellationLeavesNoPartialBatch` | request cancellation |

**Verification:** identical commands to U21a.

**Exit / done means:** evidence is provably append-only; size ceilings are enforced inside the insert transaction under `FOR UPDATE`; re-capture is idempotent without corrupting byte accounting; provenance is UID-keyed and a name reuse demonstrably inherits nothing; grants are owner-gated in SQL.

---

### U22a — Redaction

**Branch:** `feat/u22a-incident-redaction` · **PR:** `feat(incidents): add fuzzed redaction for captured evidence`

**Depends on:** nothing (can land in parallel with U21). **Covers:** R2, R15; Q1 P11.

**Split note:** U22 as written is six files (four + fuzz test + `fuzz.yml` row). Split into U22a (redaction, fuzzed) and U22b (collector + adapters). See Correction C2 and C3.

**Files (4):**
1. `backend/internal/incidents/redaction.go` — new
2. `backend/internal/incidents/redaction_test.go` — new
3. `backend/internal/incidents/redaction_fuzz_test.go` — new
4. `.github/workflows/fuzz.yml` — edit (one matrix row)

**Steps:**
1. Package doc for `internal/incidents` stating the Q1 policy in one paragraph and pointing at this plan.
2. `redaction.go`:
```go
// Redactor removes sensitive material from captured evidence BEFORE it is
// measured, marshalled, or written. Behavioural reference:
// backend/internal/k8s/resources/secrets.go maskedSecret — which is
// unexported and Secret-specific, so it cannot be imported. Two of its rules
// are non-obvious and MUST be preserved here:
//   1. StringData is masked as well as Data.
//   2. kubectl.kubernetes.io/last-applied-configuration is stripped from
//      EVERY object kind, not just Secrets — it carries the plaintext
//      stringData of the original kubectl apply.
type Redactor struct{ maxBytes int }

const MaskValue = "****"
const LastAppliedConfigAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// RedactObject projects an unstructured object down to an allowlist and masks
// what remains. Returns the projection, metadata, and whether the source was
// Secret-derived (Q1 P11).
func (r *Redactor) RedactObject(obj map[string]any) (map[string]any, RedactionMeta)

// RedactEventMessage bounds and sanitizes a controller-authored string.
func (r *Redactor) RedactText(s string) (string, bool /*truncated*/)
```
3. **Allowlist projection, not denylist.** Never "copy the object then remove bad fields" — an unknown CRD field would survive. Copy only: `apiVersion`, `kind`, `metadata.{name,namespace,uid,resourceVersion,creationTimestamp,labels,ownerReferences}`, `spec.replicas`, `spec.template.spec.containers[].{name,image}`, `status.conditions[]`, `status.{replicas,readyReplicas,availableReplicas,phase}`. Everything else is dropped and counted in `FieldsRemoved`.
4. Mark `SecretDerived` when: `kind == "Secret"`; or any `valueFrom.secretKeyRef` / `envFrom.secretRef` / `imagePullSecrets` / `volumes[].secret` reference is present; or the source resource is `secrets`.
5. Strip `LastAppliedConfigAnnotation` from `metadata.annotations` unconditionally. Drop all other annotations except a small allowlist (`kubecenter.io/*`, `deployment.kubernetes.io/revision`).
6. Sanitize text: strip C0/C1 control characters except `\n`/`\t`, coerce invalid UTF-8 with `strings.ToValidUTF8`, truncate to `maxBytes` at a rune boundary and set `truncated`.
7. `fuzz.yml` — add `- { pkg: ./internal/incidents/, target: FuzzIncidentRedaction }` to the matrix, preserving alignment.

**Tests — `redaction_test.go`:**
| Test | Case |
|---|---|
| `TestRedactSecretMasksDataAndStringData` | parity with `maskedSecret` (secret redaction) |
| `TestRedactStripsLastAppliedConfigOnEveryKind` | the non-obvious rule, on a Deployment and a ConfigMap |
| `TestRedactUsesAllowlistNotDenylist` | an unknown CRD field is dropped, not copied |
| `TestRedactMarksSecretDerivedForSecretKeyRef` | P11.4 |
| `TestRedactMarksSecretDerivedForImagePullSecrets` | P11.4 |
| `TestRedactTruncationIsFlagged` | never silent (P14) |
| `TestRedactHandlesInvalidUTF8AndControlChars` | malicious resource text |
| `TestRedactionMetaCountsRemovedFields` | envelope truthfulness |
| `TestRedactNilAndEmptyObjects` | nil-safety |

**`redaction_fuzz_test.go` — `FuzzIncidentRedaction`:** seeds are real `maskedSecret` inputs plus mutations (deeply nested maps, 10 MiB strings, invalid UTF-8, `nil` interior values, annotation keys with newlines). Oracles: **A** no panic; **D** a canary value planted in `data`, `stringData`, or `last-applied-configuration` never appears in the output; **B** output size ≤ `maxBytes`. Corpus seeds committed under `backend/internal/incidents/testdata/fuzz/FuzzIncidentRedaction/`.

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
cd backend && go test ./internal/incidents/ -list '^FuzzIncidentRedaction$' | grep -qx 'FuzzIncidentRedaction'
cd backend && go test ./internal/incidents/ -run=^$ -fuzz=^FuzzIncidentRedaction$ -fuzztime=60s
```

**Exit / done means:** no code path can persist a Secret value or a `last-applied-configuration` annotation; the fuzz target exists, is registered in `fuzz.yml`, and survives a local 60s run; redaction is allowlist-based.

---

### U22b — Bounded evidence collector and first-release source adapters

**Branch:** `feat/u22b-incident-collector` · **PR:** `feat(incidents): bounded-concurrency evidence collector with diagnostics, object and event adapters`

**Depends on:** U20, U21b, U22a. **Covers:** R2, R3, R15, R17; KTD8.

**Files (4):**
1. `backend/internal/incidents/collector.go` — new (envelope types + `Collector`)
2. `backend/internal/incidents/collector_test.go` — new
3. `backend/internal/incidents/sources.go` — new (the three adapters)
4. `backend/internal/incidents/sources_fuzz_test.go` — new (`FuzzEventProjection`) — **plus** the `fuzz.yml` matrix row, which is added in U22a's PR alongside the redaction row to keep this unit at four files. *(Add both rows in U22a; the drift guard for `FuzzEventProjection` will fail until U22b lands, so U22a and U22b must merge in the same day, or U22a adds only its own row and U22b spends its fifth file on `fuzz.yml`. Prefer the latter — it keeps each PR independently green.)*

**Corrected file list (5), preferring independently-green PRs:**
1. `backend/internal/incidents/collector.go` — new
2. `backend/internal/incidents/collector_test.go` — new
3. `backend/internal/incidents/sources.go` — new
4. `backend/internal/incidents/sources_fuzz_test.go` — new
5. `.github/workflows/fuzz.yml` — edit (add the `FuzzEventProjection` row)

*(`sources_test.go` is folded into `collector_test.go`; the adapters are exercised through the collector, which is also where their partial/timeout/forbidden behaviour matters.)*

**Steps:**
1. `collector.go` — declare `Completeness`, `EvidenceMode`, `SourceRef`, `RedactionMeta`, `Evidence`, `WithheldEvidence`, `CaptureRequest`, `CaptureReport`, `Source`, `SourceResult`, `Collector` exactly as §3.2/§3.4.
2. Implement `Capture` per §3.4 rules 1–7. Key invariants to write as code comments *and* assert in tests: one source's failure never cancels the group; results land in a pre-sized indexed slice, never a shared map; the store write happens only after `g.Wait()` and only if `ctx.Err() == nil`.
3. `CaptureKey` derivation: `sha256(evidenceKind | clusterID | apiGroup | resource | namespace | name | uid | sourceObservedAt.UTC().Format(time.RFC3339Nano))`, hex-encoded.
4. `sources.go` — the three adapters from §3.4:
   - `diagnosticsSource` holding `*diagnostics.Handler`'s collaborators (`topology.ResourceLister`, `*topology.Builder`, `*resources.AccessChecker`); SAR-gates `get` on the target via `CanAccessGroupResource(ctx, clusterID, u.KubernetesUsername, u.KubernetesGroups, "get", apiGroup, resource, namespace)`; produces one `diagnostic_check` snapshot per `CheckResult` from `diagnostics.Normalize`, carrying the stable `CheckID`.
   - `objectSource` using `ClientFactory.ClientForUser(u.KubernetesUsername, u.KubernetesGroups)`; emits a `snapshot` `object_summary` (redacted allowlist projection) **and** a paired `live_link` `object_summary` so R15's retained/live distinction has a real pair to render.
   - `eventSource` SAR-gating `list` on `("", "events", ns)`, then impersonated `CoreV1().Events(ns).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.uid=<uid>", Limit: 200})`, projecting each event through `Redactor.RedactText`, capped at 1 MiB. On SAR denial: `Completeness: forbidden`, zero items, no scope leakage in `CompletenessDetail`.
5. `sources_fuzz_test.go` — `FuzzEventProjection` per §3.6.
6. `fuzz.yml` — add `- { pkg: ./internal/incidents/, target: FuzzEventProjection }`.

**Tests — `collector_test.go`** (uses `k8s.NewFakeClientFactory`, `resources.NewPredicateAccessChecker`, and stub `Source`s):
| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestCaptureOneSourceTimeoutYieldsPartialAndKeepsOthers` | *"One source timeout yields a partial record and preserves successful evidence"* — **the headline scenario** |
| `TestCaptureOneSourcePanicIsRecoveredAndReportedFailed` | `recoverutil.Go`; process survives |
| `TestCaptureRespectsMaxConcurrency` | bounded concurrency (counting semaphore assertion) |
| `TestCaptureDeniedNamespaceProducesForbiddenWithoutLeak` | *"Denied namespaces"* — cross-user / revoked permission; asserts `CompletenessDetail` names no namespace or object |
| `TestCaptureRedactsSensitiveEnvAndAnnotations` | *"sensitive annotations/env fields"*; secret redaction |
| `TestCaptureRejectsMaliciousResourceText` | *"malicious resource text"* |
| `TestCaptureBoundsHugeEventList` | *"huge events... cannot exhaust memory"* |
| `TestCaptureDeletedTargetProducesFailedNotPanic` | *"deleted targets"*; deleted/recreated resource |
| `TestCaptureCancellationWritesNothing` | request cancellation |
| `TestCaptureIsIdempotentForIdenticalObservation` | repeated clicks (capture-key dedup) |
| `TestCaptureNewObservationCreatesNewRow` | append-only truth is not lost to dedup |
| `TestCaptureEmitsPairedSnapshotAndLiveLink` | R15 retained/live distinction |
| `TestCaptureCompletenessAggregation` | complete only if all sources complete |
| `TestCaptureNeverPersistsSecretValues` | Q1 P11.1 — end-to-end through the collector |

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
cd backend && go test ./internal/incidents/ -list '^FuzzEventProjection$' | grep -qx 'FuzzEventProjection'
cd backend && go test ./internal/incidents/ -run=^$ -fuzz=^FuzzEventProjection$ -fuzztime=60s
```

**Exit / done means:** a capture with one hanging source still persists the other sources' evidence and reports `partial`; no adapter can panic the process; every adapter SAR-gates before reading and reports `forbidden` without leaking scope; nothing is written on cancellation; both fuzz targets are in `fuzz.yml` and green.

---

### U23a — Incident CRUD and notes API, plus server wiring

**Branch:** `feat/u23a-incident-api` · **PR:** `feat(api): incident CRUD, notes, and read-time authorization filtering`

**Depends on:** U21a, U21b, U22b. **Covers:** R14, R16; Q1 P1–P10.

**Split note:** the master plan's U23 packs CRUD, capture, notes, grants, and export into one `handler.go` alongside three wiring files, leaving no headroom. Split into U23a (base handler, CRUD, notes, wiring) and U23b (capture, grants, export). See Correction C2.

**Files (5):**
1. `backend/internal/incidents/handler.go` — new
2. `backend/internal/incidents/handler_test.go` — new
3. `backend/internal/server/routes.go` — edit
4. `backend/internal/server/server.go` — edit
5. `backend/cmd/kubecenter/main.go` — edit

**Steps:**
1. `handler.go`:
```go
type Handler struct {
    Store         *store.IncidentStore          // nil when no DB
    EvidenceStore *store.IncidentEvidenceStore  // nil when no DB
    GrantStore    *store.IncidentGrantStore     // nil when no DB
    Collector     *Collector
    AccessChecker *resources.AccessChecker
    AuditLogger   audit.Logger
    Config        IncidentLimits
    Logger        *slog.Logger
}

func NewHandler(...) *Handler

// requireStore is the truthful DB-unavailable gate (Correction C4). Routes are
// ALWAYS registered so a no-DB deployment gets 503 + a machine-readable reason
// rather than an ambiguous 404.
func (h *Handler) requireStore(w http.ResponseWriter) bool {
    if h.Store == nil {
        httputil.WriteErrorWithReason(w, http.StatusServiceUnavailable,
            "incident persistence unavailable",
            "incident_persistence_unavailable",
            map[string]any{"requires": "postgresql"})
        return false
    }
    return true
}

// visibility resolves Q1 P1. Returns (row, role, ok). A caller who is neither
// owner nor collaborator gets 404 — never 403 (Q1 P1).
func (h *Handler) visibility(ctx context.Context, id uuid.UUID, u *auth.User) (*store.IncidentRow, Role, bool, error)

// FilterEvidence implements Q1 P4/P5/P10/P11 and is the ONLY authorization
// filter for evidence. Every representation calls it.
func (h *Handler) FilterEvidence(ctx context.Context, u *auth.User, rows []store.IncidentEvidenceRow) ([]Evidence, []WithheldEvidence, error)
```
2. `FilterEvidence` algorithm: collapse rows to distinct scopes → for each scope call `h.AccessChecker.CanAccessGroupResource(ctx, scope.ClusterID, u.KubernetesUsername, u.KubernetesGroups, "get", scope.APIGroup, scope.Resource, scope.Namespace)` (never `CanAccess` — Correction C7) → for `secret_derived` rows additionally check `("", "secrets", scope.Namespace)` → build the allow set → partition. SAR error ⇒ `withheldReason: "authorization_check_unavailable"`, logged at `Warn`, fail closed.
3. Handlers: `HandleList`, `HandleCreate`, `HandleGet`, `HandleUpdate`, `HandleDelete`, `HandleListNotes`, `HandleCreateNote`, `HandleUpdateNote`, `HandleDeleteNote`. Each begins `user, ok := httputil.RequireUser(w, r); if !ok { return }` then `if !h.requireStore(w) { return }` then `id, err := uuid.Parse(chi.URLParam(r, "incidentID"))` with `400` on failure (Correction C6).
4. `HandleGet` returns `{incident, evidence: [...], withheld: [...], counts: {visible, withheld, total: visible}}`. `HandleList` returns `api.Response{Data: [...], Metadata: &api.Metadata{Total: <visible count>, Continue: cursor}}` — `Total` is the visible count only (P10).
5. `HandleUpdateNote` reads the expected revision from the body (`{"body": "...", "revision": 3}`); `store.ErrNoteRevisionConflict` ⇒ `httputil.WriteErrorWithReason(w, 409, "note was modified", "note_revision_conflict", map[string]any{"currentRevision": n})`.
6. Audit: add to `backend/internal/audit/logger.go`… **no** — that would be a sixth file. Instead declare the incident actions as `audit.Action` values **locally in `handler.go`** (`const actionIncidentCreate audit.Action = "incident_create"` …), matching the pattern that `Action` is an open string type. A follow-up housekeeping PR can promote them into `logger.go`. Note this in the PR description.
7. `routes.go` — in the authenticated cascade, after the diagnostics block:
```go
// Incident routes — registered whenever the handler exists so a no-DB
// deployment reports 503 "incident_persistence_unavailable" rather than a
// bare 404 (R3).
if s.IncidentsHandler != nil {
    s.registerIncidentRoutes(ar)
}
```
and the registration function alongside `registerDiagnosticsRoutes`:
```go
func (s *Server) registerIncidentRoutes(ar chi.Router) {
    h := s.IncidentsHandler
    ar.Route("/incidents", func(ir chi.Router) {
        yamlRL := s.YAMLRateLimiter
        if yamlRL == nil {
            yamlRL = s.RateLimiter
        }
        ir.Use(middleware.RateLimit(yamlRL))
        ir.Get("/", h.HandleList)
        ir.Post("/", h.HandleCreate)
        ir.Get("/{incidentID}", h.HandleGet)
        ir.Put("/{incidentID}", h.HandleUpdate)
        ir.Delete("/{incidentID}", h.HandleDelete)
        ir.Get("/{incidentID}/notes", h.HandleListNotes)
        ir.Post("/{incidentID}/notes", h.HandleCreateNote)
        ir.Put("/{incidentID}/notes/{noteID}", h.HandleUpdateNote)
        ir.Delete("/{incidentID}/notes/{noteID}", h.HandleDeleteNote)
    })
}
```
*(`resources.ValidateURLParams` is deliberately **not** attached — it validates only `name`/`namespace` and would be inert here; UUIDs are validated in-handler.)*
8. `server.go` — add `IncidentsHandler *incidents.Handler` to both `Server` and `Deps`, plus `if deps.IncidentsHandler != nil { s.IncidentsHandler = deps.IncidentsHandler }` in the assignment cascade (~L263, next to the diagnostics block).
9. `main.go` — after the ESO block (~L868), before the `server.New` literal:
```go
// Persistent incident investigations (Release D). The handler is constructed
// unconditionally; its store fields stay nil when no DB is configured, and
// every endpoint then answers 503 incident_persistence_unavailable rather than
// disappearing behind a 404 (R3).
var incidentStore *appstore.IncidentStore
var incidentEvidenceStore *appstore.IncidentEvidenceStore
var incidentGrantStore *appstore.IncidentGrantStore
if dbPool != nil {
    incidentStore = appstore.NewIncidentStore(dbPool)
    incidentEvidenceStore = appstore.NewIncidentEvidenceStore(dbPool)
    incidentGrantStore = appstore.NewIncidentGrantStore(dbPool)
}
incidentCollector := incidents.NewCollector(k8sClient, topoLister, topoBuilder, accessChecker, cfg.Incidents, logger)
incidentsHandler := incidents.NewHandler(
    incidentStore, incidentEvidenceStore, incidentGrantStore,
    incidentCollector, accessChecker, auditLogger, cfg.Incidents, logger,
)
```
and `IncidentsHandler: incidentsHandler,` in the `server.Deps` literal (~L903, beside `DiagnosticsHandler`).

**Tests — `handler_test.go`** (httptest + `chi.NewRouteContext`, `resources.NewPredicateAccessChecker`, in-memory store fakes behind narrow interfaces):
| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestGuessedIncidentIDReturns404NotForbidden` | *"Guessed incident ID"* — Q1 P1; cross-user access |
| `TestNonOwnerNonCollaboratorSeesNothing` | cross-user access |
| `TestCollaboratorSeesIncidentButOnlyAuthorizedEvidence` | Q1 P2 — the core of AE6 |
| `TestRevokedCollaboratorLosesAccessImmediately` | *"revoked collaborator"* |
| `TestSourcePermissionChangeWithholdsItemWithoutMetadataLeak` | *"source permission change"* — Q1 P4/P7; asserts the withheld shape carries no namespace/name/kind |
| `TestSARErrorWithholdsAsCheckUnavailableNotForbidden` | R3 — unavailable ≠ forbidden |
| `TestDeletedSourceObjectStillGovernedByScopeAuthorization` | *"deleted source"* — Q1 P8 (AE6 deletion branch) |
| `TestSecretDerivedEvidenceRequiresSecretsGet` | Q1 P11; secret redaction |
| `TestOwnerWithoutSecretsGetStillWithheld` | Q1 P11.2 — ownership is not a bypass |
| `TestCountsMatchFilteredItems` | Q1 P10 — `Metadata.Total` == visible count |
| `TestListDoesNotLeakWithheldIncidentsViaPagination` | Q1 P10 |
| `TestScopeDeduplicationIssuesOneSARPerScope` | Q1 P5 — counts SAR calls through a recording predicate |
| `TestNoDatabaseReturns503WithReason` | *"no DB reports unavailable"* — Correction C4; asserts 503 + `reason`, **not** 404 |
| `TestInvalidUUIDReturns400` | Correction C6 |
| `TestNoteRevisionConflictReturns409WithReason` | note edit conflicts |
| `TestWriteOperationsAreAudited` | audit idiom |
| `TestUnauthenticatedRequestReturns401` | baseline |

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
bash scripts/check-cluster-routing.sh
```

**Exit / done means:** every read path routes through the single `FilterEvidence`; a no-DB deployment answers 503 with a machine-readable reason on every incident endpoint; cross-user access yields 404; a withheld item leaks no scope metadata; `scripts/check-cluster-routing.sh` passes.

---

### U23b — Capture, collaborator grants, and export endpoints

**Branch:** `feat/u23b-incident-capture-export` · **PR:** `feat(api): incident capture, collaborator grants, and filtered export`

**Depends on:** U23a. **Covers:** R14–R16; Q1 P3, P12, P14.

**Files (5):**
1. `backend/internal/incidents/handler_capture.go` — new
2. `backend/internal/incidents/handler_grants.go` — new
3. `backend/internal/incidents/handler_export.go` — new
4. `backend/internal/incidents/handler_actions_test.go` — new
5. `backend/internal/server/routes.go` — edit (extend `registerIncidentRoutes`)

**Steps:**
1. `handler_capture.go` — `HandleCapture` (`POST /incidents/{incidentID}/capture`): owner-only (P3); body `{clusterId, namespace, kind, name, sources: ["diagnostics","object","events"]}`; validate against `diagnostics`-supported kinds; call `h.Collector.Capture`; redact→measure→`InsertBatch`; map `store.ErrEvidenceLimit` to `413` + `reason: "evidence_limit_exceeded"` + `extra: {limit, current}`; return the `CaptureReport` with per-source completeness. `HandleListEvidence` (`GET /incidents/{incidentID}/evidence`) delegates to `FilterEvidence` with cursor pagination.
2. `handler_grants.go` — `HandleListGrants`, `HandleAddGrant`, `HandleRemoveGrant`. All owner-only. `HandleAddGrant` body `{granteeId, canAnnotate}`; the grantee id is an `auth.User.ID`, validated for length and charset but **not** resolved against `local_users` (OIDC/LDAP identities have no row there). Self-grant is a 204 no-op. Removal is immediate and takes effect on the collaborator's next request (no session invalidation needed — visibility is computed per request).
3. `handler_export.go` — `HandleExport` (`GET /incidents/{incidentID}/export?format=json|markdown`) per §3.5 and Q1 P12. Sets `Content-Disposition: attachment; filename="incident-<id>-<yyyymmdd>.json|md"`, the correct `Content-Type`, and `X-Content-Type-Options: nosniff`. Markdown emits untrusted text inside fences with backtick-run escaping. Audit-logs `incident_export` with visible/withheld counts.
4. `routes.go` — extend `registerIncidentRoutes` with:
```go
ir.Get("/{incidentID}/evidence", h.HandleListEvidence)
ir.Post("/{incidentID}/capture", h.HandleCapture)
ir.Get("/{incidentID}/grants", h.HandleListGrants)
ir.Post("/{incidentID}/grants", h.HandleAddGrant)
ir.Delete("/{incidentID}/grants/{granteeID}", h.HandleRemoveGrant)
ir.Get("/{incidentID}/export", h.HandleExport)
```

**Tests — `handler_actions_test.go`:**
| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestExportMatchesVisibleEvidenceExactly` | *"Export matches visible evidence"* — same fixture, asserts export set == `FilterEvidence` set |
| `TestExportExcludesCredentialsAndSecretValues` | *"excludes credentials"*; secret redaction |
| `TestExportRejectsHTMLFormat` | Q1 P12 — no HTML path |
| `TestExportEscapesUntrustedTextInMarkdown` | no smuggled markup; backtick-run escaping |
| `TestExportCountsWithheldItemsHonestly` | Q1 P10/P12 |
| `TestExportSetsAttachmentAndNosniffHeaders` | download hardening |
| `TestExportByCollaboratorAppliesSameFilter` | Q1 P10 collaborator preview parity |
| `TestCaptureIsOwnerOnly` | Q1 P3; cross-user access |
| `TestCaptureRejectsOverSizeBatchBeforeWrite` | Q1 P14 |
| `TestCaptureScopeLimitReturns409` | Q1 P5 |
| `TestRepeatedCaptureDoesNotDuplicateEvidence` | *"repeated clicks do not duplicate evidence unexpectedly"* |
| `TestCapturePartialSourceStillPersistsSucceeded` | R3 partial truth end-to-end |
| `TestCaptureWithoutDBReturns503` | unavailable DB |
| `TestGrantAddAndRemoveAreOwnerOnly` | Q1 P3 |
| `TestRemovedGrantTakesEffectOnNextRequest` | revoked collaborator |
| `TestGrantDoesNotConveyKubernetesAuthority` | Q1 P2 — grantee with a grant but no namespace access sees only withheld placeholders |
| `TestCaptureCancellationPersistsNothing` | request cancellation |

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
bash scripts/check-cluster-routing.sh
```

**Exit / done means:** export is provably the same filtered set as the screen; capture is owner-only, bounded, idempotent, and honest about partials; grants are revocable and convey no Kubernetes authority; no HTML output path exists.

---

### U24a — Incident types, API client, and list view

**Branch:** `feat/u24a-incident-list` · **PR:** `feat(frontend): incident types, API client, and incident list route`

**Depends on:** U23b. **Covers:** R14.

**Split note:** the master plan's U24 omits `frontend/lib/api.ts` (six files) and bundles the two-identity e2e suite into one spec file with no fixture for a second user. Split into U24a/U24b/U24c. See Correction C2 and §1.7.

**Files (4):**
1. `frontend/lib/incident-types.ts` — new
2. `frontend/lib/api.ts` — edit (append `incidentsApi`)
3. `frontend/islands/IncidentList.tsx` — new
4. `frontend/routes/observability/incidents/index.tsx` — new

**Steps:**
1. `incident-types.ts` — mirror the Go envelope: `Incident`, `Evidence`, `WithheldEvidence`, `EvidenceMode`, `Completeness`, `RedactionMeta`, `SourceRef`, `IncidentNote`, `IncidentGrant`, `CaptureReport`, `IncidentCounts`. Model withheld items as a **discriminated union** (`type EvidenceItem = Evidence | WithheldEvidence` keyed on `withheld`) so the compiler forces the UI to handle the withheld branch — it cannot be forgotten.
2. `api.ts` — append `export const incidentsApi = { list, create, get, update, remove, listEvidence, capture, listNotes, createNote, updateNote, deleteNote, listGrants, addGrant, removeGrant, exportUrl }` following `notifApi`'s shape exactly (`apiGet`/`apiPost`/`apiPut`/`apiDelete`, template-literal paths under `/v1/incidents`). `exportUrl(id, format)` returns a string for an `<a href>` rather than fetching, so the browser handles `Content-Disposition`.
3. `IncidentList.tsx` — Tailwind utilities + theme CSS custom properties only (CLAUDE.md); `.card-lift`/`.glass` from `assets/styles.css` for chrome; no inline `style={{}}` (do **not** copy `DiagnosticWorkspace.tsx`'s pattern). States: loading, empty, error, and a first-class **`persistence unavailable`** state driven by `ApiError.reason === "incident_persistence_unavailable"` — it must read as "this deployment has no database", never as "you have no incidents".
4. `incidents/index.tsx` — SSR shell copying `observability/investigate.tsx` (`define.page`, `<div class="p-6 space-y-6">`, `<h1 class="text-2xl font-bold text-text-primary">Incidents</h1>`, subtitle in `text-text-secondary`) rendering `<IncidentList />`.

**Tests:** covered by `deno task check` (types/lint/fmt) and by U24c's e2e. The discriminated union is the type-level test: a build that ignores the withheld branch fails `deno check`.

**Verification:**
```
cd frontend && deno task check && deno task test && deno task build
```

**Exit / done means:** `deno task check` and `deno task build` pass repo-wide; the withheld branch is compiler-enforced; the no-DB state is visually and semantically distinct from the empty state; no inline styles.

---

### U24b — Incident detail workspace

**Branch:** `feat/u24b-incident-workspace` · **PR:** `feat(frontend): persistent incident workspace with evidence timeline and notes`

**Depends on:** U24a. **Covers:** R14–R16; AE6.

**Files (5):**
1. `frontend/islands/IncidentWorkspace.tsx` — new (shell: header, capture status, grants, export)
2. `frontend/islands/IncidentEvidenceTimeline.tsx` — new
3. `frontend/islands/IncidentNotes.tsx` — new
4. `frontend/routes/observability/incidents/[id].tsx` — new
5. `frontend/lib/incident-types.ts` — edit (view-model helpers only)

**Steps:**
1. `[id].tsx` — copy the `external-secrets/[namespace]/[name].tsx` idiom: `define.page(function IncidentDetailPage(ctx) { const { id } = ctx.params; return <IncidentWorkspace id={id} />; })`.
2. `IncidentEvidenceTimeline.tsx` — ordered by `collectedAt DESC`. Each row shows **both** timestamps with distinct labels ("observed" vs "captured"), and renders "observation time unknown" when `sourceObservedAt` is null — never falls back to `collectedAt` (§3.2). A `snapshot` badge vs a `live_link` badge makes R15's retained/live distinction visible; live links deep-link through `KIND_ROUTE_MAP`/`getResourceSection` from `lib/types/diagnostics.ts` and must render "no longer present" on a 404 rather than an error. Completeness renders as five distinct states (`complete`/`partial`/`failed`/`forbidden`/`timed_out`) — never collapsed into "error". Redaction metadata (`applied`, `truncated`, `fieldsRemoved`) is shown, so the user knows what they are not seeing. Withheld placeholders render as an explicit "withheld — you do not currently have access to this scope" row with the reason, positioned in the timeline so the user knows *something* is there without learning what.
3. `IncidentNotes.tsx` — create/edit/delete; the edit form carries the note's `revision`; on `ApiError.reason === "note_revision_conflict"` it shows a non-destructive conflict banner preserving the user's draft and offering reload — it must never silently discard typing.
4. `IncidentWorkspace.tsx` — header (title, status, window, counts incl. `withheldCount`), capture panel (target picker + source checkboxes + per-source result), grants panel (owner-only), export menu (JSON / Markdown via `exportUrl`), and a bounded evidence list with "load more" for long timelines.
5. Accessibility: semantic `<ol>` for the timeline, `aria-live="polite"` on capture status, keyboard-reachable capture/export/grant controls, visible focus rings, and a `<caption>`/`aria-label` on any table.
6. Tailwind + theme tokens only; `.glass`/`.glass-elevated` for chrome; data surfaces stay solid per the Liquid Glass rule.

**Verification:**
```
cd frontend && deno task check && deno task test && deno task build
```

**Exit / done means:** the timeline shows both timestamps, all five completeness states, redaction metadata, and snapshot-vs-live badges; withheld items are visible as placeholders that disclose nothing; note conflicts never lose a draft; the whole surface is keyboard-operable; build passes.

---

### U24c — AE6 end-to-end with two identities

**Branch:** `feat/u24c-incident-e2e` · **PR:** `test(e2e): AE6 incident capture, deletion, handoff, revocation and export`

**Depends on:** U24b. **Covers:** AE6.

**Split note:** AE6's collaborator branch needs a second authenticated identity. `e2e/fixtures/auth.setup.ts` creates only `admin`, and `playwright.config.ts` has a single `storageState`. This unit adds both. The master plan's single-spec U24 could not have satisfied "UI tests need at least two identities."

**Files (4):**
1. `e2e/fixtures/collaborator.setup.ts` — new
2. `e2e/playwright.config.ts` — edit (add a `collaborator` project + setup dependency)
3. `e2e/tests/incidents.spec.ts` — new
4. `e2e/helpers.ts` — edit (add `createIncident`, `captureEvidence`, `grantCollaborator` API helpers)

**Steps:**
1. `collaborator.setup.ts` — modelled on `auth.setup.ts`: create a second local user via `POST /api/v1/users` as admin (admin-only route, present at `routes.go` L229–235), log in as that user through the UI, extract the access token the same way, save `playwright/.auth/collaborator.json`.
2. `playwright.config.ts` — add the setup file to the `setup` project's match (it already matches `/.*\.setup\.ts/` under `./fixtures`, so it is collected automatically), and add a `collaborator` project with `storageState: "playwright/.auth/collaborator.json"`, `testMatch: /incidents\.spec\.ts/`, `dependencies: ["setup"]`. Keep `fullyParallel: false` and the existing `chromium` `testIgnore` untouched.
3. `incidents.spec.ts` — the AE6 arc:
   - **Capture:** admin creates a pod in an e2e namespace, opens `/observability/incidents`, creates an incident, captures diagnostics + object + events, and sees a populated timeline.
   - **Deletion:** delete the pod via `deleteResource`; reload the incident; **stored evidence is still readable** and the live-link row reads "no longer present". *(AE6 deletion branch, Q1 P8.)*
   - **Handoff:** admin grants the collaborator; the collaborator project opens the same incident and sees the record and notes.
   - **Revocation:** admin removes the grant; the collaborator's next load returns 404.
   - **Cross-user:** before any grant, the collaborator requesting the incident id gets 404, not 403.
   - **Filtered export:** admin downloads the JSON export and asserts it contains no `stringData`, no `last-applied-configuration`, and matches the on-screen visible count.
   - **Partial capture:** capture with an unsupported kind ⇒ a partial/failed source is displayed, not a blanket error.
   - **Note conflict:** two tabs edit the same note; the second sees the conflict banner with its draft intact.
   - **Retention/no-DB:** skipped here (covered by U25a's Go tests) — e2e always has PostgreSQL.
4. **Known limitation to state in the PR:** in `kind`, both local users may map to Kubernetes identities with identical RBAC, so the e2e cannot reliably prove the *revoked Kubernetes permission* branch. That branch is proven in `handler_test.go` via `NewPredicateAccessChecker` (`TestSourcePermissionChangeWithholdsItemWithoutMetadataLeak`, `TestGrantDoesNotConveyKubernetesAuthority`). The e2e proves the *grant* branch. Do not claim otherwise in the PR description.

**Verification:**
```
cd e2e && npm test
cd frontend && deno task check
```

**Exit / done means:** the AE6 arc passes end to end against real PostgreSQL and a real kind cluster; the deletion branch demonstrably preserves stored evidence; grant and revocation both take effect; the export is verified free of secret material.

---

### U25a — Retention loop and configuration

**Branch:** `feat/u25a-incident-retention` · **PR:** `feat(incidents): configurable bounded retention sweep with recoverutil lifecycle`

**Depends on:** U21a, U23a. **Covers:** R14; Q1 P13.

**Split note:** "30-day **configurable** retention" requires `config.go` and `defaults.go`, which the master plan's U25 omits; with them the unit is seven files. Split into U25a (backend retention + config) and U25b (frontend entry points). See Correction C2.

**Files (5):**
1. `backend/internal/incidents/retention.go` — new
2. `backend/internal/incidents/retention_test.go` — new
3. `backend/internal/config/config.go` — edit
4. `backend/internal/config/defaults.go` — edit
5. `backend/cmd/kubecenter/main.go` — edit

**Steps:**
1. `config.go` — add `Incidents IncidentsConfig \`koanf:"incidents"\`` to `Config`, and:
```go
// IncidentsConfig holds configuration for persistent incident investigations
// (Release D). Env vars follow the KUBECENTER_<SECTION><FIELD> convention:
// KUBECENTER_INCIDENTS_RETENTIONDAYS, KUBECENTER_INCIDENTS_MAXITEMBYTES, ...
type IncidentsConfig struct {
    RetentionDays    int           `koanf:"retentiondays"`    // default 30, clamped [1,3650]
    MaxItemBytes     int           `koanf:"maxitembytes"`     // default 1048576
    MaxIncidentBytes int           `koanf:"maxincidentbytes"` // default 10485760
    MaxItems         int           `koanf:"maxitems"`         // default 500
    MaxScopes        int           `koanf:"maxscopes"`        // default 20
    CaptureTimeout   time.Duration `koanf:"capturetimeout"`   // default 20s
    SourceTimeout    time.Duration `koanf:"sourcetimeout"`    // default 5s
    MaxConcurrency   int           `koanf:"maxconcurrency"`   // default 4
}
```
Add the eight entries to the `defaults` map (`"incidents.retentiondays": DefaultIncidentsRetentionDays`, …) and a post-`Unmarshal` clamp so out-of-range operator values are corrected and logged rather than silently accepted.
2. `defaults.go` — add the corresponding consts beside `DefaultAuditRetentionDays`/`DefaultAlertingRetentionDays`.
3. `retention.go`:
```go
type Retainer struct {
    store         *store.IncidentStore
    retentionDays int
    logger        *slog.Logger
}

// RunLoop sweeps once immediately, then hourly, until ctx is cancelled.
// The tick body is wrapped in recoverutil.Tick so a panic in the sweep logs
// and the loop continues to its next iteration rather than crashing the
// process (docs/solutions/backend-resilience-conventions.md).
//
// LIFECYCLE CONTRACT: RunLoop owns no WaitGroup and sends on no counted
// channel. If a future change adds either, the wg.Done()/send MUST be
// registered on the goroutine BEFORE entering the recoverutil closure —
// registering it inside means a recovered panic skips it and Wait() hangs
// forever (conventions doc L46-L59).
func (r *Retainer) RunLoop(ctx context.Context) {
    r.sweep(ctx)
    ticker := time.NewTicker(time.Hour)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            r.logger.Info("incident retention loop stopping")
            return
        case <-ticker.C:
            recoverutil.Tick(ctx, r.logger, "incidents retention sweep", r.sweep)
        }
    }
}

// sweep deletes expired incidents. A failure is logged and the next tick
// retries; it never blocks shutdown and never returns an error upward.
func (r *Retainer) sweep(ctx context.Context)
```
The initial immediate sweep is also wrapped (`recoverutil.Tick(ctx, ...)`) so a startup-time panic cannot take down `main`.
4. `sweep` calls `store.IncidentStore.Cleanup(ctx, r.retentionDays)` (already bounded by a 5-minute context inside the store), logs `deleted` at `Info` when > 0, logs failures at `Error`, and emits the observability signals named in the master plan (`incident_retention_deleted`, `incident_retention_failures`) as structured log fields — **no unbounded labels** (no incident ids, no user ids).
5. `main.go` — inside the `if dbPool != nil` region added by U23a:
```go
if incidentStore != nil {
    retainer := incidents.NewRetainer(incidentStore, cfg.Incidents.RetentionDays, logger)
    go retainer.RunLoop(ctx)
}
```
Note in the PR that this deliberately uses `recoverutil.Tick` rather than copying the hand-rolled `recover()` in the ESO bulk retention goroutine at `main.go` L836–866, which predates the conventions doc.

**Tests — `retention_test.go`:**
| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestSweepDeletesOnlyExpiredIncidents` | retention (fake store, clock-injected) |
| `TestSweepFailureIsObservableAndLoopContinues` | *"Retention failure is observable, resumes next cycle"* |
| `TestSweepPanicIsRecoveredAndLoopContinues` | `recoverutil.Tick` |
| `TestRunLoopStopsPromptlyOnContextCancel` | *"never blocks normal API shutdown"*; startup/restart |
| `TestRunLoopSweepsImmediatelyThenHourly` | first sweep is not delayed an hour |
| `TestRetentionDaysClampedToRange` | configurability bounds |
| `TestRetentionDaysZeroIsRejectedNotTreatedAsInfinite` | a config typo must not disable retention silently |
| `TestSweepLogsNoUnboundedLabels` | observability rule |
| `TestNoStoreMeansNoLoop` | unavailable DB |

**Verification:**
```
cd backend && go vet ./... && go test ./... -race -count=1
```

**Exit / done means:** retention is configurable via `KUBECENTER_INCIDENTS_RETENTIONDAYS` with clamping and a startup log line stating the effective value; the loop uses `recoverutil.Tick`; cancellation returns promptly and never blocks shutdown; a failed sweep is logged and retried next hour; `NOTES.txt`'s retroactive-deletion warning (U21a) matches the implemented behaviour.

---

### U25b — Diagnosis-to-incident entry points and navigation

**Branch:** `feat/u25b-capture-entry-points` · **PR:** `feat(frontend): capture-to-incident entry point and incidents navigation`

**Depends on:** U24b, U25a. **Covers:** R14, R17.

**Files (3):**
1. `frontend/islands/CaptureToIncidentButton.tsx` — new
2. `frontend/islands/DiagnosticWorkspace.tsx` — edit (additive only)
3. `frontend/lib/constants.ts` — edit (one nav entry)

**Steps:**
1. `CaptureToIncidentButton.tsx` — props `{clusterId, namespace, kind, name}`. Opens a small sheet offering "capture into a new incident" (creates, then captures, then navigates to `/observability/incidents/<id>`) or "capture into an existing open incident" (a list from `incidentsApi.list`). Carries the diagnosis's **target and time window** through unchanged — `windowStart` defaults to the earliest `sourceObservedAt` among the diagnosis's checks, falling back to now minus one hour, never to a fabricated value. The button is disabled while a capture is in flight and the request is idempotent server-side (capture-key dedup), so a double click cannot duplicate evidence. Handles `ApiError.reason === "incident_persistence_unavailable"` by rendering the button disabled with an explanatory tooltip instead of failing on click.
2. `DiagnosticWorkspace.tsx` — **additive only**: import the new island and render it in the existing status-banner row beside the "Re-scan" button, passing `namespace.value`, `kind.value`, `name.value`, and the current cluster from `lib/cluster.ts`.
   **Agent Directive 1 note:** this file is 372 LOC (>300). Adding a button is not a structural refactor, so Step 0 does not trigger. However the file is written entirely with inline `style={{...}}`, violating CLAUDE.md's Tailwind-only rule. **Do not convert it in this PR** — that conversion is structural, requires a separate dead-code/cleanup commit first, and would balloon the diff. File it as a follow-up (listed in Risks).
3. `constants.ts` — add `{ label: "Incidents", href: "/observability/incidents" }` to the observability group beside `{ label: "Investigate", href: "/observability/investigate" }` (~L401).

**Tests:** the diagnosis-to-incident scenarios are exercised in `e2e/tests/incidents.spec.ts` (U24c) — extend that spec in this PR only if it stays within U24c's file budget; otherwise assert them via `deno task test` unit coverage of the window-derivation helper plus a manual smoke against homelab per the branching rules. The two master-plan scenarios are:
- *"Diagnosis-to-incident preserves target/time window"* → assert the created incident's `clusterId/namespace/kind/name` and `windowStart` match the diagnosis.
- *"repeated clicks do not duplicate evidence unexpectedly"* → double-click produces one incident and one evidence set (server-side capture-key dedup, already tested in U23b's `TestRepeatedCaptureDoesNotDuplicateEvidence`).

**Verification:**
```
cd frontend && deno task check && deno task test && deno task build
cd e2e && npm test
```

**Exit / done means:** an operator can go from a diagnosis to a persistent incident in one click without retyping the target; the target and time window survive the transition; repeated clicks are harmless; the nav entry exists; `DiagnosticWorkspace.tsx`'s diff is purely additive.

---

## 5. Cross-Unit Sequencing and Conflict Notes

**Merge order (strict where an arrow is shown):**

```
U20 ─────────────┐
U21a → U21b ─────┼→ U22b → U23a → U23b → U24a → U24b → U24c
U22a ────────────┘                    └→ U25a ─────────────→ U25b
```

- **U20, U21a, U22a** have no dependencies on each other and can be worked in parallel.
- **U25a** depends on U21a (for `IncidentStore.Cleanup`) and U23a (for the `main.go` `incidentStore` variable). It can merge before U24a.
- **U25b** must merge last; it depends on U24b's route existing and U25a's config being present.

**Shared-file conflicts — the ones that will actually collide:**

| File | Units touching it | Sequencing rule |
|---|---|---|
| `backend/internal/server/routes.go` | **U23a** (adds `registerIncidentRoutes` + the cascade entry), **U23b** (extends the same function) | Strictly sequential. U23b rebases on U23a. Both edit the *same function body* — never work them in parallel. |
| `backend/internal/server/server.go` | **U23a** only | No conflict within Release D. **But**: Release A (U1–U3) and Release F (U32–U36) also add `Deps` fields. The master plan's own warning applies — land Release D's `server.go` edit in one PR and rebase other tracks onto it. |
| `backend/cmd/kubecenter/main.go` | **U23a** (store + collector + handler construction, `Deps` literal), **U25a** (the `go retainer.RunLoop(ctx)` line inside the block U23a created) | Sequential. U25a's edit is a three-line insertion inside U23a's `if dbPool != nil` region; it cannot be authored before U23a merges. |
| `backend/internal/store/migrations/` | **U21a** only | **Sequence `000020` is reserved for this plan.** 000018, 000019, 000021, 000022 belong to other tracks. Do not renumber under any circumstance — golang-migrate records applied versions and a renumber after any deployment silently skips the migration (the exact failure documented in `NOTES.txt` for 000016/000017). |
| `backend/internal/store/migrations/NOTES.txt` | **U21a** only | Append-only; conflicts with other tracks are trivially resolvable. |
| `.github/workflows/fuzz.yml` | **U22a** (`FuzzIncidentRedaction` row), **U22b** (`FuzzEventProjection` row) | Sequential, one row each, so each PR stays independently green under the `-list` drift guard. Never add a row for a target that does not yet exist. |
| `backend/internal/config/config.go`, `defaults.go` | **U25a** only within Release D | Releases A and F also add config sections. Same rebase discipline. |
| `frontend/lib/api.ts` | **U24a** only | Appends `incidentsApi` after `limitsApi`. Other tracks appending their own namespaces conflict only on the trailing lines. |
| `frontend/lib/constants.ts` | **U25b** only | One nav line in the observability group. |
| `frontend/lib/incident-types.ts` | **U24a** (creates), **U24b** (appends view-model helpers) | Sequential. |
| `frontend/islands/DiagnosticWorkspace.tsx` | **U25b** only | Additive. Any structural/Tailwind conversion is a separate PR with its own Step-0 cleanup commit. |
| `e2e/tests/incidents.spec.ts`, `e2e/playwright.config.ts`, `e2e/helpers.ts` | **U24c** only | `playwright.config.ts` gains one project. Other tracks adding e2e projects should sequence behind it; `fullyParallel: false` and `workers: 1` in CI mean an added project extends wall-clock time — mention the delta in the PR. |
| `backend/internal/diagnostics/*` | **U20** only | Release E (U26–U31) consumes `CheckResult` but must not modify it without re-running U20's round-trip test. |

**Cross-track note:** U20 is a dependency of Release E (`Delivery Order` row E: *"U20 for reusable verification"*). Landing U20 early unblocks E even if the rest of D slips.

---

## 6. Deferred Appendix

Explicitly **not** in Release D. Each item names its gate.

1. **Collaborator handoff UX beyond basic grants.** Release D ships add/remove/list grants and a read-or-annotate flag. Deferred: user search/typeahead for grantee ids (needs a directory-lookup endpoint that itself has a disclosure surface), time-boxed grants, handoff notifications through the notification centre, "request access" flows, and an audit-visible handoff timeline. Gate: the basic grant model proving out in production use.
2. **Loki log-excerpt adapter.** `backend/internal/loki/` exists (`client.go`, `discovery.go`, `handler.go`, `security.go` with the fuzzed `FuzzEnforceNamespaces` namespace enforcement) and is the natural source. Deferred because it needs its own contract per the master plan's rule: source permission (Loki namespace enforcement is *not* the same as Kubernetes RBAC), redaction of log lines (unbounded free text, far harder than object projection), size and time bounds, a mandatory human preview before persistence (raw logs are excluded by default — R15's "selected redacted excerpts require preview"), and stale-data behaviour. The `live_link` mode and the envelope's `redaction.truncated` field are already in place for it.
3. **Alert snapshots.** `backend/internal/alerting/store.go`'s `MemoryStore` (`AlertEvent`, `ActiveAlerts`, `List`, `Resolve`, `Prune`, `RunPruner`) is **in-memory only** — snapshots taken from it are lost on restart, which makes their provenance untrustworthy for an incident record. Gate: a durable alert store, or an explicit `identityWeak`-style caveat in the UI.
4. **Change-receipt evidence.** Release E (U27–U29) creates the receipt records. Once they exist, a `change_receipt` evidence kind links an incident to the applies that touched its subject. Gate: Release E's receipt schema stabilizing.
5. **Recurring-incident comparison.** Requires stable evidence identities and stable `CheckID`s across releases — U20 freezes the six ids, but the set will grow. Gate: the master plan's own condition, *"only after evidence identities and check IDs stabilize."*
6. **Mobile parity.** Native incident reading and handoff follow the web/API release with independent tests for cached evidence after revocation and expired authentication. `mobile/lib/features/observability/incidents/` and `mobile/test/features/observability/incidents/` are the expected owners. **Critical constraint:** a mobile client must not cache evidence past an authorization change — Q1 P7 gives a 60-second revocation window server-side, which any client-side cache would extend. Gate: M5 public-store launch completing.
7. **Making the legacy `diagnostics.Result` surface truthful.** Today an RBAC-denied related-pod resolution renders as `"pass"` — an R3 violation (Correction C1). Fixing it means changing the JSON that 12 mobile files decode, so it is a coordinated web + mobile + backend change with its own migration window. Gate: a mobile release train.
8. **Unifying the three ad-hoc maskers.** `resources.maskedSecret`, `store.MaskedSettings`, `notifications.maskConfig`, and now `incidents.Redactor` are four independent implementations (Correction C3). A shared `internal/redact` package with one fuzz suite would be strictly better. Gate: a dedicated refactor PR with Step-0 cleanup; not something to bolt onto a feature release.
9. **HTML / print export.** Deliberately excluded — it reintroduces the markup-smuggling surface that P12 closes. Gate: a sanitization design with its own fuzz target.
10. **Remote-cluster capture.** The schema and envelope carry `cluster_id` throughout and `AccessChecker`/`ClusterRouter` already route SARs per cluster, so this is additive. Gate: Release C (U7–U12) landing.

---

## 7. Risks and Open Items

| # | Risk | Impact | Mitigation / owner |
|---|---|---|---|
| R-1 | **Read-time SAR cost on a cold cache.** Up to 20 `SelfSubjectAccessReview` POSTs per incident read after 60s of inactivity. | Slow first render; API-server load on large deployments. | P5 caps scopes at 20 and de-duplicates before checking. Instrument sweep/read latency and SAR counts per the master plan's observability section. If it bites, the fix is a per-request memo *within* one request — never a longer cross-request TTL, which would widen the revocation window past 60s. |
| R-2 | **No store-layer test harness and no PostgreSQL in the backend CI job.** | The DB-truth assertions in U21a/U21b are skipped in CI. | Correction C5: pure tests always run; integration tests skip-guard on `KUBECENTER_TEST_DATABASE_URL`; U24c's e2e covers real persistence against PostgreSQL 17. **Open item:** decide whether to add a `services: postgres` block to `ci.yml`'s backend job. That is a CI-wide change affecting every track and is deliberately **out of scope here** — raise it separately. |
| R-3 | **Two-identity e2e cannot prove the revoked-Kubernetes-permission branch** in `kind` (both local users likely map to identical RBAC). | AE6's revocation branch is proven only in Go tests. | Stated explicitly in U24c's steps and PR description. The Go tests using `NewPredicateAccessChecker` are the real proof. Do not overclaim in release notes. |
| R-4 | **Retroactive retention deletion.** Lowering `KUBECENTER_INCIDENTS_RETENTIONDAYS` deletes incidents on the next sweep. | Silent data loss for an operator who was tuning config. | Documented in `NOTES.txt` (U21a) and in the effective-value startup log line (U25a). **Open item:** should a lowering be gated behind an explicit confirmation flag? Recommend: log at `Warn` when the configured value is lower than the max `retention_days_at_capture` present in the table. |
| R-5 | **`main.go` is 965 LOC and three Release-D-adjacent tracks all edit it.** | Merge conflicts. | Sequencing table in §5. Keep each unit's `main.go` diff to a contiguous block. |
| R-6 | **`DiagnosticWorkspace.tsx` violates the Tailwind-only rule** and is 372 LOC. | Technical debt; a future structural edit triggers Agent Directive 1. | U25b stays additive. File the conversion as its own PR with a Step-0 cleanup commit. **Open item:** owner and timing. |
| R-7 | **`audit.Action` constants declared locally in `handler.go`** (U23a) rather than in `audit/logger.go`, to stay within five files. | Slight inconsistency with the ~30 existing constants. | Noted in U23a's PR description; a one-line housekeeping PR promotes them. |
| R-8 | **`live_link` items can 404/403 long after capture.** | UI could read as broken. | The timeline renders "no longer present" / "no longer authorized" as first-class states, not errors (U24b). Tested in U24c's deletion branch. |
| R-9 | **Evidence-scope authorization is coarser than object-level.** A caller with `get pods` in a namespace can read evidence about *any* pod captured there, including one they might not have been able to read under a hypothetical object-level policy. | A theoretical over-disclosure. | This is intentional and matches Kubernetes' own model: RBAC is namespace-and-resource scoped, not object scoped, so an object-level policy would be *stricter than Kubernetes itself* and would be unenforceable at read time (the object may be gone — P8). Stated here so it is a recorded decision, not an oversight. |
| R-10 | **`api_group`/`resource` recorded at capture could drift** if a CRD is re-grouped or an aggregated API changes. | A stale scope could re-authorize against a resource that no longer exists ⇒ SAR returns denied ⇒ permanently withheld. | Fail-closed is the correct direction. The UI's `withheldReason` distinguishes `forbidden` from `authorization_check_unavailable`, so an operator can tell the difference. **Open item:** whether a re-scope/repair tool is worth building; recommend deferring until it is observed. |
| R-11 | **Adding an e2e project extends CI wall-clock** (`fullyParallel: false`, `workers: 1` in CI). | Slower PR feedback for everyone. | U24c must report the measured delta in its PR. If it is large, scope the `collaborator` project to `testMatch: /incidents\.spec\.ts/` (already planned) so it runs the minimum. |

**Open questions this plan does not answer** (deliberately, they are operator/product inputs, not gaps to fill by assumption):
- **OQ-1** Should `ci.yml`'s backend job gain a PostgreSQL service? (Cross-track; see R-2.)
- **OQ-2** Should retention lowering require explicit operator confirmation? (See R-4.)
- **OQ-3** Who owns the `DiagnosticWorkspace.tsx` Tailwind conversion, and when? (See R-6.)

Q1 itself is **resolved and closed** — §2 is the specification. Q2, Q3, Q4, and Q5 do not gate Release D.
