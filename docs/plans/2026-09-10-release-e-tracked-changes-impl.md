---
title: "Release E — GitOps-Aware Tracked Changes — Implementation Plan"
parent_plan: docs/plans/2026-09-10-1315-feat-feature-expansion-plan.md
release: E
units: U26–U31
migration_sequence: 000021
date: 2026-09-10
depends_on: Release D U20 (check results), Release C U9 (remote yaml)
---

# Release E — GitOps-Aware Tracked Changes — Implementation Plan

**Scope.** Read-only GitOps ownership evidence before apply (U26), a durable
receipt store (U27), a tracked-apply + verification service (U28), authorized
receipt APIs and DI wiring (U29a/U29b), additive integration into the existing
`/yaml/apply` endpoint (U30a/U30b), and the web surfaces (U31).

**Not in scope.** Git patch/PR creation (Q4 unresolved), automatic rollback,
controller suspension as part of an apply, offline mutation queues, mobile
parity. See the Deferred appendix.

**Unit count after splits: 8** — U26, U27, U28, U29a, U29b, U30a, U30b, U31.
Each unit is one feature branch, one PR, ≤5 files including tests.

**Resolved inputs carried in from the user.** Q1 is settled: owner-scoped
persistence plus explicit grants, reads re-check the caller's *current*
authorization at access time, 30-day configurable retention, stricter filtering
for Secret-related content. That policy is applied to receipts throughout this
plan. Q4 is *not* settled, so U26 delivers read-only ownership evidence only.

---

## Codebase Findings

Every row below was read at the cited lines. Paths are absolute under
`C:\Users\whstu\Documents\code-projects\k8sCenter`.

| File | What it constrains |
|---|---|
| `backend\internal\yaml\applier.go` (203 LOC, read fully) | The apply engine U30a must wrap. `ApplyDocuments` loops documents independently; `applyOne` resolves GVK→GVR with a 3-attempt retry (500ms/1s backoff), GETs the object for action detection, marshals to JSON, PATCHes with `types.ApplyPatchType` + `FieldManager = "kubecenter"` + optional `Force`. Action derives from `isNew` / `resourceVersion` equality. **No transaction, no rollback, no ordering guarantee beyond document order.** |
| `backend\internal\yaml\handler.go` (380 LOC, read fully) | `HandleApply` (:120-182): `RequireUser` → `readYAMLBody` (MaxBytesReader + `CheckSecurity`) → `ParseMultiDoc` → `force := r.URL.Query().Get("force") == "true"` → `ClusterRouter.RouterFor` → **`if !pair.IsLocal` → 501** → `ApplyDocuments` → one audit `Entry` per result → `httputil.WriteData(w, resp)`. |
| `backend\internal\gitops\types.go` (147 LOC, read fully) | `Tool`, `NormalizedApp`, `AppSource`, `ManagedResource{Group,Kind,Namespace,Name,Status,Health}` — **no version, no UID**. |
| `backend\internal\gitops\argocd.go` (579 LOC, key funcs read) | `extractArgoResources` (:167-198) parses `status.resources[]`; called only from `GetArgoAppDetail` (:57). `NormalizeArgoApp` (:66) builds `fmt.Sprintf("argo:%s:%s", ns, name)` (:113). |
| `backend\internal\gitops\flux.go` (406 LOC, key funcs read) | `extractFluxInventory` (:283-315) splits `status.inventory.entries[].id` as `namespace_name_group_kind` via `strings.SplitN(id, "_", 4)`. IDs `flux-ks:` / `flux-hr:`. **HelmRelease has no inventory** (`GetFluxAppDetail` :80-92 fills `History` only). |
| `backend\internal\gitops\handler.go` (1014 LOC, key funcs read) | `fetchApps` (:93) → 30s `cacheTTL` + `singleflight`, populated with `h.K8sClient.BaseDynamicClient()` (service account). `filterAppsByRBAC` (:349-386) memoizes `AccessChecker.CanAccessGroupResource(..., "list", apiGroup, resource, ns)` per (toolPrefix, namespace); cluster-scoped apps are admin-only. `parseCompositeID` (:403), `toolGVR` (:68), `toolPrefixForApp` (:82). |
| `backend\internal\gitops\discovery.go` (204 LOC) | 5-min `recheckInterval`, `recoverutil.Tick`, `Status()` returns a copy. Argo via `argoproj.io/v1alpha1`, Flux via `kustomize.toolkit.fluxcd.io/v1` + `helm.toolkit.fluxcd.io/v2`. |
| `backend\internal\store\migrations\` (listed) | Highest existing sequence is **000017**. Style: UPPERCASE keywords, `CREATE TABLE IF NOT EXISTS`, `TIMESTAMPTZ`, `TEXT NOT NULL CHECK (col IN (...))` instead of PG enums, `JSONB NOT NULL DEFAULT '[]'`, `TEXT[] NOT NULL DEFAULT '{}'`, `idx_<table>_<cols>` index names, partial + unique-partial indexes, mandatory `COMMENT ON TABLE`. A table-creating down migration is `DROP TABLE IF EXISTS x;`. |
| `...\migrations\000013_create_eso_bulk_refresh_jobs.up.sql` + `000014` | The exact DDL template for a caller-generated-UUID job table with JSONB outcome arrays and a partial UNIQUE index closing a TOCTOU. |
| `...\migrations\NOTES.txt` (read fully) | Operator-note convention: 72-dash banner, `NNNNNN_name (context)` heading, "Operator action required" bullets, inspection SQL, rollback constraints. |
| `backend\internal\store\eso_bulk_jobs.go` (285 LOC, head read) | Store idiom to copy: `type X struct{ pool *pgxpool.Pool }` + `NewX(pool)`, caller-generated `uuid.UUID` PK, `errors.As(&pgErr) && pgErr.Code == "23505"` → sentinel error, JSONB via `json.Marshal` → `$n::jsonb` with `||` append, `Cleanup(ctx, retentionDays)` with an inner `context.WithTimeout(ctx, 5*time.Minute)`, `CompleteOrphans(ctx) (int64, error)`. |
| `backend\internal\store\eso_history.go` | `cleanupTimeout = 5 * time.Minute`; `DELETE ... WHERE attempt_at < NOW() - $1 * INTERVAL '1 day'`; `retentionDays < 1` rejected. |
| `backend\internal\store\migrate.go` / `store.go` | `//go:embed migrations/*.sql`; `migrate.NewWithSourceInstance("iofs", ...)`; `m.Up()` runs on every boot from `store.New`; dirty-state check. golang-migrate wraps each migration in a transaction. |
| `backend\internal\auth\provider.go` (:8-16) | `User{ID, Username, Provider, KubernetesUsername, KubernetesGroups, Roles []string}`. **No `Email`, no `Groups`; `Roles` is a slice.** `auth.IsAdmin(u)` scans `Roles`. |
| `backend\internal\auth\{oidc,ldap,local}.go` | `ID` is `oidc:<providerID>:<subject>` (oidc.go:294), `ldap:<providerID>:<DN>` (ldap.go:326), and for local an **unprefixed opaque random id** (local.go:55, generated at :236). |
| `backend\internal\k8s\resources\access.go` (373 LOC) | `AccessChecker.CanAccessGroupResource(ctx, clusterID, username string, groups []string, verb, apiGroup, resource, namespace string) (bool, error)` (:187). SelfSubjectAccessReview through an impersonating clientset; 60s `sync.Map` cache keyed on cluster+user+groups+resource+ns+verb; remote via `clusterRouter` (:258), fail-closed when the router is nil. |
| `backend\internal\audit\logger.go` (138 LOC) | `Logger{ Log(ctx, Entry) error }`; `Entry{Timestamp, ClusterID, User, SourceIP, ConnectionIP, Action, ResourceKind, ResourceNamespace, ResourceName, Result, Detail}`; `ActionApply = "apply"` already exists; `ResultSuccess/Failure/Denied`. `PostgresLogger.Log` is async and lossy (1000-entry buffer, drops on overflow). The existing apply audit keys `User: user.Username`, **not** `user.ID`. |
| `backend\internal\httputil\response.go` (69 LOC, read fully) | `WriteData` → always HTTP 200, `api.Response{Data: data}`, never sets `Metadata`. `WriteError` strips `detail` on ≥500. `WriteErrorWithReason(w, status, message, reason, extra)` is the sanctioned machine-readable-conflict shape. `RequireUser` → 401. |
| `backend\pkg\api\types.go` | `Response{Data, Metadata, Error}`; `Metadata{Total, Continue, Page, PageSize}`; `APIError{Code, Message, Detail, Reason, Extra}`. |
| `backend\internal\server\routes.go` (902 LOC) | Auth+CSRF+ClusterContext applied once at the group level (:108-113); every feature is `if s.XHandler != nil { s.registerXRoutes(ar) }` (:183-226); `registerYAMLRoutes` (:284-301); `registerGatewayRoutes` (:780-801) is the cleanest template; `resources.ValidateURLParams` wraps any `{namespace}/{name}` route. |
| `backend\internal\server\server.go` (391 LOC) | `Server` has 47 fields; `Deps` mirrors it. **`YAMLHandler` is constructed inside `New` (:218-224), not in main.go** — every other feature handler is built in main.go and copied through a nil guard (:230-327). |
| `backend\cmd\kubecenter\main.go` (965 LOC) | DB block :190-254; `dbPool` stays nil without a DB; feature-degrade pattern `if dbPool != nil { ... }` at :580, :783-786, :824-867 (incl. `CompleteOrphans` orphan reaping and an hourly retention goroutine); `server.New(server.Deps{...})` at :878-922. |
| `backend\internal\recoverutil\recoverutil.go` (89 LOC) | `Go(g *errgroup.Group, logger, label, fn func() error)`, `Safe(logger, label, fn func())`, `Tick(ctx, logger, label, fn func(context.Context))`. `wg.Done()` and counted channel sends must stay **outside** the wrapped closure. |
| `docs\solutions\backend-resilience-conventions.md` (197 LOC) | Oracle taxonomy A (no panic) / B (round-trip) / C (guard cannot be bypassed) / D (secret never survives); teeth-via-mutation seeds; in-package `*_fuzz_test.go`; hermetic. |
| `.github\workflows\fuzz.yml` (63 LOC) | 18-row matrix, `- { pkg: ./internal/x/, target: FuzzY }`, `-list` drift guard, `-fuzztime=5m`, SHA-pinned actions. |
| `backend\internal\diagnostics\{diagnostics,rules,handler}.go` | `Result{RuleName, Status string, Severity, Message, Detail, Remediation, Links}` — **`Status` is an untyped string with no `inconclusive` value**; no `Engine` type; rule registry (`rules`, `registerRule`) is unexported; `RunDiagnostics(ctx, *DiagnosticTarget) []Result`. `checkReplicaMismatch` (rules.go:180-224) compares `spec.replicas` vs `status.readyReplicas` only — no `observedGeneration`, no conditions. Data comes from `topology.ResourceLister` over **local informers with the service account**, so diagnostics is local-cluster-only and cannot serve remote verification. |
| `frontend\lib\yaml-apply.ts` (123 LOC, read fully) | Owns `ApplyResult` / `ApplyResponse` and the `useYamlApply` hook. **Two island consumers** (`YamlApplyPage.tsx`, `SecretStoreFromTemplateEditor.tsx`) plus `frontend\lib\secretstore-template-nav_test.ts`, which imports `ApplyResponse`. |
| `frontend\islands\YamlApplyPage.tsx` (361 LOC) | Pure presentation over the hook; no `api()` calls; `deno lint` clean, no unused imports, no dead code. |
| `frontend\lib\api.ts` (302 LOC) | `api<T>(path, options & {signal?})`; injects `Authorization`, `X-Cluster-ID` (always, from the ambient `selectedCluster` signal), `X-Requested-With` on non-GET. `apiPostRaw<T>(path, body, contentType = "text/yaml")` has **no signal parameter**. Machine-readable error data rides `error.reason` / `error.extra` (`ApiError.reason`, `errorExtra(err, key)`). |
| `frontend\routes\api\[...path].ts` | `FORWARD_HEADERS = [authorization, content-type, accept, x-requested-with, x-cluster-id, cookie]`; `PROXY_TIMEOUT_MS = 30_000`. |
| `frontend\routes\tools\yaml-apply.tsx`, `frontend\routes\gitops\applications\[id].tsx` | Route idiom: `export default define.page(function X(ctx) { ... ctx.params.id ... })`, `decodeURIComponent`, island-with-prop. |
| `frontend\lib\action-handlers.ts` (174 LOC) | `ActionId = "scale"\|"restart"\|"delete"\|"suspend"\|"trigger"` only. **Release E does not touch this file**; its header already documents the precedent for keeping poll-shaped domain writes out of the map, so the `resource_actions.dart` isomorphism is unaffected. |
| `frontend\lib\limits-types.ts`, `frontend\lib\score-color_test.ts` | Types-module convention and the `Deno.test` + `jsr:@std/assert@1` test convention (relative `./x.ts` imports). |
| `e2e\playwright.config.ts`, `e2e\tests\yaml-apply.spec.ts` (40 LOC), `e2e\fixtures\base.ts`, `e2e\helpers.ts` | `testDir: "./tests"`; projects `setup` → `chromium` → `route-contract`; specs import `{ test, expect } from "../fixtures/base.ts"`; **zero `data-testid` in the repo** — role/label selectors only; cleanup via `try/finally` + `deleteResource`. |
| `scripts\check-cluster-routing.sh` | `HANDLER_DIRS` enumerates every package with handlers. A new `backend/internal/changes` package must be added or it is silently unscanned. |
| `mobile\lib\api\yaml_apply_controller.dart`, `mobile\lib\wizards\wizard_controller.dart` | The legacy clients. Both parse by key lookup with `?? default` and ignore unknown JSON keys. |

### (a) The exact existing apply response that must stay backward-compatible

`POST /api/v1/yaml/apply` → `httputil.WriteData` → HTTP **200** with:

```json
{
  "data": {
    "results": [
      { "index": 0, "kind": "Deployment", "name": "web", "namespace": "prod",
        "action": "configured" }
    ],
    "summary": { "total": 1, "created": 0, "configured": 1, "unchanged": 0, "failed": 0 }
  }
}
```

Field by field, from `applier.go:21-44`:

| JSON field | Go field + tag | Emission rule |
|---|---|---|
| `data.results[].index` | `Index int` `json:"index"` | always; 0-based document index |
| `data.results[].kind` | `Kind string` `json:"kind"` | always; `obj.GetKind()` |
| `data.results[].name` | `Name string` `json:"name"` | always; `obj.GetName()` |
| `data.results[].namespace` | `Namespace string` `json:"namespace,omitempty"` | omitted for cluster-scoped; defaulted to `"default"` at applier.go:132 for namespaced docs with no `metadata.namespace` |
| `data.results[].action` | `Action string` `json:"action"` | always; exactly one of `created`, `configured`, `unchanged`, `failed` |
| `data.results[].error` | `Error string` `json:"error,omitempty"` | present only when `action == "failed"` |
| `data.summary.total` | `Total int` `json:"total"` | always; `== len(results)` |
| `data.summary.created` | `Created int` | always, including zero |
| `data.summary.configured` | `Configured int` | always, including zero |
| `data.summary.unchanged` | `Unchanged int` | always, including zero |
| `data.summary.failed` | `Failed int` | always, including zero |

Consumers that must keep parsing it unchanged:

- `mobile\lib\api\yaml_apply_controller.dart:107` `ApplyResponse.fromApply` —
  `json['results'] as List? ?? const <dynamic>[]`,
  `json['summary'] as Map<String,dynamic>? ?? const {}`; `ApplySummary.fromJson`
  reads the five ints as `(json['x'] as num?)?.toInt() ?? 0`;
  `ApplyResult.fromJson` reads the six fields as `as String? ?? ''` /
  `(json['index'] as num?)?.toInt() ?? 0`.
- `mobile\lib\wizards\wizard_controller.dart:84` `WizardApplyOutcome` — reads the
  four counts plus the first result's kind/name/namespace;
  `allSucceeded => failed == 0`.
- `frontend\lib\yaml-apply.ts:16-35` `ApplyResult` / `ApplyResponse` — structural
  TS interfaces over `res.json()`; extra runtime keys are ignored, not errors.
- `frontend\components\wizard\WizardReviewStep.tsx:64` and
  `frontend\islands\ResourceDetail.tsx:751` post to the same endpoint.

**Compatibility proof used by U30a.** Every consumer above reads by key with a
default; none enumerates keys, asserts a key count, or uses a strict/sealed
decoder (Dart's `jsonDecode` yields `Map<String, dynamic>`; TS interfaces are
erased at runtime and excess-property checks apply only to object literals, not
to parsed JSON). Adding a sibling key `data.tracking`, and changing the type,
presence rule, and meaning of *no* field in the table above, is therefore
invisible to all four consumers. `data.tracking` is emitted **only** when the
caller opted in with `?trackedOperationId=`; a caller that omits it receives a
byte-identical body to today's.

### (b) The Argo/Flux ownership signals that actually exist today

| Signal | Where in the repo | Direction | Confirms ownership? |
|---|---|---|---|
| Argo `Application.status.resources[]` → `{group, kind, namespace, name, status, health.status}` | `backend\internal\gitops\argocd.go:167-198` (`extractArgoResources`), called only from `GetArgoAppDetail` at `argocd.go:57` | Application → its objects | **Yes.** It is the controller's own reconciliation record. |
| Flux `Kustomization.status.inventory.entries[].id` = `namespace_name_group_kind` | `backend\internal\gitops\flux.go:283-315` (`extractFluxInventory`), called from `GetFluxAppDetail` at `flux.go:88`; count-only use at `flux.go:129-134` | Kustomization → its objects | **Yes**, for Kustomizations only. |
| Flux `HelmRelease` inventory | **does not exist** — `GetFluxAppDetail` fills only `History` for `HelmRelease` (`flux.go:89-91`) | — | No. Permanent blind spot; must be reported, never guessed. |
| `argocd.argoproj.io/tracking-id` annotation on a live object | **absent from the repo.** The only `argocd.argoproj.io/*` literal anywhere is `"argocd.argoproj.io/application-set-refresh"` at `argocd.go:485` | object → Application (self-reported) | **No** — writable by anyone who can write the object. |
| `app.kubernetes.io/instance`, `app.kubernetes.io/managed-by`, `metadata.managedFields` field-manager entries | absent from every gitops code path | object → tool (self-reported) | **No.** |

**Correction to the master plan.** U26's approach line resolves ownership from
"Argo tracking metadata and Flux inventory". Only the Flux half exists. There is
no tracking-metadata reader at all, and the Argo evidence that does exist runs in
the opposite direction — Application → object list — reachable only
per-application, never by object. U26 must therefore (1) build a **reverse index**
over the cached application list, and (2) newly parse the tracking annotation and
instance label as *hints that narrow the search*, never as proof (KTD10).

### Other places the master plan is wrong about this codebase

1. **"Preserve Secret masking" in the YAML apply path (U9, echoed by U30).**
   There is none. `yaml/handler.go` rejects Secret **diff** with 422 (`:204-211`)
   and Secret **export** with 422 (`:261-266`); `HandleApply` has no Secret branch
   at all. `stripSensitiveDataFields` is a Flutter editor-seed flag
   (`mobile\lib\widgets\yaml_editor_panel.dart:31`), not backend behavior.
   Consequence for Release E: **plaintext Secret manifests do flow through
   apply**, so the receipt must never persist request content.
2. **`frontend/lib/change-types.ts` is the wrong anchor for the apply response.**
   `ApplyResult`/`ApplyResponse` already live in `frontend\lib\yaml-apply.ts`
   with two island consumers and a test importer. That file is the seam;
   `change-types.ts` is still created, but for receipt/ownership types only.
3. **`YAMLHandler` is not wired in `main.go`.** It is constructed inside
   `server.New` (`server.go:218-224`), so U30a's injection lands in `server.go`.
4. **Composite IDs are not `tool:namespace:name` with `tool ∈ {argocd,fluxcd}`**
   (as `CLAUDE.md` states). The real prefixes are `argo`, `flux-ks`, `flux-hr`,
   `argo-as` (`gitops/handler.go:68-79`).
5. **There is no cluster-registry "generation"** (KTD3). `clusters` has no such
   column (`000003_create_clusters.up.sql`). Release E uses `clusters.updated_at`
   as a generation surrogate and labels it as such on the wire.
6. **There is no PostgreSQL test harness.** No build tags, no testcontainers, no
   env gates; `internal/store/` has zero `*_test.go` files. `audit\store_test.go:43-48`
   and `externalsecrets\persist_test.go:12-14` both document DB round-trips as
   manual smoke. U27's "migration applies to a populated DB" scenario is a
   documented manual step, not an automated test.
7. **The BFF proxy caps every backend call at 30s and forwards only six
   headers.** Verification cannot run inline inside the apply request, and the
   idempotency key cannot be a custom header.
8. **`scripts/check-cluster-routing.sh` `HANDLER_DIRS` must gain
   `backend/internal/changes`**, or the new handler package is silently exempt
   from the routing guard. This is the sixth file that forces the U29 split.
9. `RunDiagnostics` has **no inconclusive status and no exported rule registry**,
   and its data source is local informers under the service account. Release E
   cannot reuse it as-is; see the U20 interface contract below.


---

## Design Decisions

### D1. Ownership resolution result type (R18, KTD10)

Declared in `backend/internal/gitops/types.go` (U26):

```go
// OwnershipController names the GitOps controller that claims a live object.
type OwnershipController string

const (
	OwnedByNone   OwnershipController = "none"
	OwnedByArgoCD OwnershipController = "argocd"
	OwnedByFluxCD OwnershipController = "fluxcd"
	OwnedByBoth   OwnershipController = "both"
)

// OwnershipConfidence qualifies how far the evidence actually goes.
type OwnershipConfidence string

const (
	// ConfidenceConfirmed: a controller's own reconciliation record names this
	// exact object. Only EvidenceArgoStatusResource or EvidenceFluxInventoryEntry
	// can produce this value.
	ConfidenceConfirmed OwnershipConfidence = "confirmed"
	// ConfidenceConflicting: two controllers both produced confirming evidence.
	ConfidenceConflicting OwnershipConfidence = "conflicting"
	// ConfidenceUnknown: no confirming evidence. Covers "nothing found at all"
	// and "hints present but unconfirmed" — the Reason distinguishes them.
	ConfidenceUnknown OwnershipConfidence = "unknown"
	// ConfidenceForbidden: the caller may not list the controller's applications
	// in the relevant namespace, so absence of evidence proves nothing.
	ConfidenceForbidden OwnershipConfidence = "forbidden"
	// ConfidenceUnavailable: the controller is not installed, or its API errored.
	ConfidenceUnavailable OwnershipConfidence = "unavailable"
)

// OwnershipEvidenceKind classifies one piece of evidence. Only the two
// AUTHORITATIVE kinds may raise confidence to ConfidenceConfirmed (KTD10).
type OwnershipEvidenceKind string

const (
	// Authoritative — the controller's own status.
	EvidenceArgoStatusResource OwnershipEvidenceKind = "argo-status-resource"
	EvidenceFluxInventoryEntry OwnershipEvidenceKind = "flux-inventory-entry"
	// Hints — self-reported by the object, writable by anyone who can write it.
	EvidenceArgoTrackingID OwnershipEvidenceKind = "argo-tracking-annotation"
	EvidenceInstanceLabel  OwnershipEvidenceKind = "instance-label"
	EvidenceManagedByLabel OwnershipEvidenceKind = "managed-by-label"
	EvidenceFieldManager   OwnershipEvidenceKind = "field-manager"
)

// Authoritative reports whether this evidence kind can establish ownership on
// its own. The ONLY place the KTD10 rule is encoded; ResolveOwnership and the
// fuzz oracle both call it.
func (k OwnershipEvidenceKind) Authoritative() bool {
	return k == EvidenceArgoStatusResource || k == EvidenceFluxInventoryEntry
}

// OwnershipEvidence is one observation about one object.
type OwnershipEvidence struct {
	Kind     OwnershipEvidenceKind `json:"kind"`
	Tool     Tool                  `json:"tool"`
	AppID    string                `json:"appId,omitempty"` // composite id, only when confirmed
	RawValue string                `json:"rawValue,omitempty"` // hint payload, length-capped, never trusted
	Note     string                `json:"note,omitempty"`
}

// ObjectRef identifies a live object for ownership and verification. Version and
// Resource are populated by the apply path (from the RESTMapping); the ownership
// path may leave them empty because neither Argo status.resources[] nor Flux
// inventory entries carry a version.
type ObjectRef struct {
	ClusterID string `json:"clusterId"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"` // plural, for SelfSubjectAccessReview
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// OwnershipResult is the answer for exactly one ObjectRef.
type OwnershipResult struct {
	Object     ObjectRef           `json:"object"`
	Controller OwnershipController `json:"controller"`
	Confidence OwnershipConfidence `json:"confidence"`
	// Reason is a stable machine code, e.g. "confirmed-argo-status",
	// "hints-only", "no-evidence", "flux-helmrelease-no-inventory",
	// "argo-list-forbidden", "argo-not-installed", "both-claim".
	Reason string `json:"reason"`
	// Apps carries the confirming applications ONLY. Never populated from hints.
	Apps []OwnedByApp `json:"apps,omitempty"`
	// Evidence lists everything observed, hints included, each labelled.
	Evidence []OwnershipEvidence `json:"evidence,omitempty"`
	// IdentityBasis is always "group-kind-namespace-name" in Release E because
	// neither controller records a UID. UIDConfirmed is therefore always false.
	IdentityBasis string `json:"identityBasis"`
	UIDConfirmed  bool   `json:"uidConfirmed"`
	// WritableGitSource is ALWAYS false in Release E (Q4 unresolved). Present so
	// clients never infer write capability from the presence of Source.
	WritableGitSource bool      `json:"writableGitSource"`
	ObservedAt        time.Time `json:"observedAt"`
}

// OwnedByApp names a confirming application. Source is copied from the
// application the caller is already authorized to list — never from a hint.
type OwnedByApp struct {
	AppID     string    `json:"appId"` // "argo:ns:name" / "flux-ks:ns:name"
	Tool      Tool      `json:"tool"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	Source    AppSource `json:"source"`
	Suspended bool      `json:"suspended"`
}
```

**The KTD10 rule, stated explicitly.** A familiar label
(`app.kubernetes.io/instance`, `app.kubernetes.io/managed-by`), an
`argocd.argoproj.io/tracking-id` annotation, or a `managedFields` entry naming a
controller's field manager **does not establish ownership and never establishes a
writable Git source.** Those values live on the object itself and are writable by
anyone who can write the object; a copied manifest, a Helm chart that hardcodes
the label, or a hand-set annotation all produce them. They are used for exactly
one thing: narrowing which applications to check first. If confirmation fails, the
result is `Confidence: unknown`, `Reason: "hints-only"`, `Apps: nil`, and no
`AppSource` anywhere in the payload. `WritableGitSource` is hardcoded `false` for
all of Release E and is the single field a client may consult before offering any
Git-write affordance.

**Resolution algorithm** (`ResolveOwnership`, U26):

1. `h.Discoverer.Status()` — if Argo is not available, its branch yields
   `unavailable` / `argo-not-installed`; same for Flux. Both unavailable →
   `controller: none`, `confidence: unavailable`.
2. `h.fetchApps(ctx)` — the existing 30s-TTL, singleflight, service-account
   cache. Then `h.filterAppsByRBAC(ctx, user, apps)` **before any matching**, so
   an application the caller cannot list can never contribute evidence or leak a
   `repoURL`. If the RBAC filter removed every application of a tool that *is*
   installed, that tool's branch yields `forbidden` / `argo-list-forbidden`.
3. Build the reverse index once per request over the filtered list, then fetch
   per-app detail only for candidate applications:
   - Argo: `GetArgoAppDetail(ctx, dyn, ns, name)` → `extractArgoResources` →
     match on `(group, kind, namespace, name)`.
   - Flux Kustomization: `GetFluxAppDetail(ctx, dyn, "Kustomization", ns, name)`
     → `extractFluxInventory` → match on the same tuple.
   - Flux HelmRelease: **skipped**; contributes
     `Reason: "flux-helmrelease-no-inventory"` when a hint pointed at one.
   Candidate order: applications named by a hint first, then, bounded by
   `maxDetailFetches = 25` per request, the rest in namespace-affinity order.
   Exhausting the bound without a match yields `unknown` /
   `search-bound-exhausted`, never `none`.
4. Confirmations from both tools → `controller: both`, `confidence: conflicting`,
   `reason: "both-claim"`, all confirming apps listed.
5. One confirmation → `controller: argocd|fluxcd`, `confidence: confirmed`.
6. No confirmation, hints present → `unknown` / `hints-only`.
   No confirmation, no hints → `none` / `no-evidence`.

Detail fetches use the **caller-impersonating** dynamic client from
`ClusterRouter.RouterFor`, not `BaseDynamicClient`, so a caller who can list but
not get an Application gets `forbidden` rather than another tenant's inventory.

### D2. Receipt schema (U27, migration `000021`)

**Identifier: client-supplied operation id, and it is the primary key.** A
server-minted id cannot satisfy the AE7-adjacent requirement, because a client
whose response was dropped has no id with which to ask "did my apply happen?" —
its retry is indistinguishable from a new apply. The id is a UUIDv4 generated by
the client, validated server-side with `uuid.Parse` plus an explicit
`u.Version() == 4` check, and passed as the query parameter
`?trackedOperationId=` (a query parameter, not a header, because
`frontend/routes/api/[...path].ts` forwards only six allow-listed headers and the
apply body is `text/yaml`, leaving no room for a JSON envelope field).

**No request content is stored.** The receipt stores a `content_digest` —
`"sha256:" + hex(sha256(rawBody))` over the exact bytes that passed
`CheckSecurity`, before `ParseMultiDoc` — plus per-object references and
outcomes. It stores **no manifest, no `spec`, no `data`, no `stringData`, and no
rendered YAML**, tracked or untracked, Secret-bearing or not. That is what makes
"a receipt can never leak a Secret value" a structural property rather than a
filtering promise. `contains_secret` records only that the bundle contained at
least one `kind: Secret` document, so the reader can apply Q1's stricter
filtering and the UI can refuse to offer any content-reuse affordance.

```sql
-- 000021_create_change_receipts.up.sql

CREATE TABLE IF NOT EXISTS change_receipts (
    id                  UUID PRIMARY KEY,
    owner_id            TEXT NOT NULL,
    owner_username      TEXT NOT NULL DEFAULT '',
    cluster_id          TEXT NOT NULL DEFAULT 'local',
    cluster_generation  TEXT NOT NULL DEFAULT '',
    content_digest      TEXT NOT NULL,
    document_count      INTEGER NOT NULL,
    force               BOOLEAN NOT NULL DEFAULT false,
    contains_secret     BOOLEAN NOT NULL DEFAULT false,
    repair_of           UUID,
    state               TEXT NOT NULL DEFAULT 'applying'
        CHECK (state IN ('previewed', 'applying', 'applied', 'partial', 'failed', 'unknown')),
    objects             JSONB NOT NULL DEFAULT '[]',
    ownership           JSONB NOT NULL DEFAULT '[]',
    verification_state  TEXT NOT NULL DEFAULT 'pending'
        CHECK (verification_state IN ('pending', 'verifying', 'verified', 'inconclusive', 'verification_failed')),
    verification        JSONB NOT NULL DEFAULT '[]',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mutation_started_at TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    verified_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_change_receipts_owner_created
    ON change_receipts (owner_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_change_receipts_cluster_created
    ON change_receipts (cluster_id, created_at DESC);

-- Recovery scan on boot: every row that never reached a terminal state.
CREATE INDEX IF NOT EXISTS idx_change_receipts_unfinished
    ON change_receipts (created_at)
    WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS change_receipt_grants (
    receipt_id   UUID NOT NULL REFERENCES change_receipts(id) ON DELETE CASCADE,
    grantee_id   TEXT NOT NULL,
    granted_by   TEXT NOT NULL,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (receipt_id, grantee_id)
);

CREATE INDEX IF NOT EXISTS idx_change_receipt_grants_grantee
    ON change_receipt_grants (grantee_id);

COMMENT ON TABLE change_receipts IS
    'Release E tracked-apply receipts. id is the CLIENT-supplied idempotency key (UUIDv4). Stores a sha256 digest of the submitted bundle and per-object references/outcomes only -- NEVER manifest content, so a Secret-bearing apply cannot leak through this table. owner_id is auth.User.ID (provider-qualified for oidc/ldap, opaque for local). cluster_generation is clusters.updated_at at apply time, used to detect re-registration (no dedicated generation column exists). state and verification_state advance independently: applied + pending is a legal, common combination. Rows with completed_at IS NULL after a restart are reconciled to failed (never mutated) or unknown (mutation started) -- never replayed. 30-day retention via DELETE-WHERE sweep.';

COMMENT ON TABLE change_receipt_grants IS
    'Explicit read grants on a change receipt (Q1 policy: ownership plus explicit grants). Release E honours existing rows on read but ships no grant-creation API; grant management lands with the Release D incident-sharing surface.';
```

Down migration:

```sql
-- 000021_create_change_receipts.down.sql
DROP TABLE IF EXISTS change_receipt_grants;
DROP TABLE IF EXISTS change_receipts;
```

`objects` JSONB element (`store.ReceiptObject`):

```json
{
  "index": 0,
  "group": "apps", "version": "v1", "resource": "deployments", "kind": "Deployment",
  "namespace": "prod", "name": "web", "uid": "8b1c…",
  "action": "configured",
  "error": "",
  "recordedAt": "2026-09-10T13:15:02Z"
}
```

`verification` JSONB element is a `CheckResult` (see the U20 contract below).
`ownership` JSONB is the `[]OwnershipResult` captured at preview time, stored so
the receipt still explains "this object was Argo-owned when you changed it" after
the controller has since reconciled it away.

**Grants and read-time re-authorization.** A receipt is readable when the caller
is the owner (`owner_id == user.ID`), holds a row in `change_receipt_grants`, or
is an admin (`auth.IsAdmin`). That gate alone is not sufficient: per Q1, every
read additionally re-checks **current** Kubernetes authorization per object via
`AccessChecker.CanAccessGroupResource(ctx, receipt.ClusterID, user.KubernetesUsername,
user.KubernetesGroups, "get", group, resource, namespace)`. Objects that fail the
check are replaced by `{"index": n, "redacted": true, "reason": "forbidden"}` —
index preserved, every other field dropped. The stored `summary` counts stay
intact (they are the historical record) with an added `redactedObjects` count.
When `contains_secret` is true, Q1's stricter filtering applies: object rows for
`kind: Secret` are redacted unless the caller currently passes a `get` check on
`secrets` in that namespace, and the per-object `error` string is replaced by its
Kubernetes reason class (`conflict`, `forbidden`, `invalid`, `other`) because
admission and validation messages routinely echo submitted field values.

### D3. Durable-intent protocol and exact write ordering (KTD9)

`Service.TrackedApply` (U28) performs, in order:

| Step | Action | Failure behaviour |
|---|---|---|
| 1 | Parse body, `CheckSecurity`, `ParseMultiDoc`, compute digest, `RouterFor`, read `clusters.updated_at`. No mutation yet. | Ordinary 4xx/5xx; nothing persisted, nothing applied. |
| 2 | `INSERT INTO change_receipts (... state='applying', mutation_started_at=NULL, objects='[]')` | **Abort the tracked apply.** HTTP 503, `reason: "receipt_store_unavailable"`. **Zero documents are applied.** The client may retry without `trackedOperationId` to get today's untracked behaviour. Unique violation → the idempotency branch in D4. |
| 3 | `UPDATE change_receipts SET mutation_started_at = NOW() WHERE id=$1 AND mutation_started_at IS NULL` | Abort, HTTP 503, same reason. Still zero documents applied. This second write is the only thing that distinguishes "crashed before touching the cluster" from "crashed mid-apply". |
| 4 | `ApplyDocumentsObserved(...)`. After **each** document, the observer runs `UPDATE change_receipts SET objects = objects \|\| $2::jsonb WHERE id=$1`. | Append failure → observer returns an error → the applier stops. Documents not yet attempted are reported as `action:"failed"`, `error:"not applied: change recording failed"` (see D5). |
| 5 | `UPDATE change_receipts SET state=$2, completed_at=NOW() WHERE id=$1` where `$2` is `applied` / `partial` / `failed`. | Response is still returned with truthful `results`/`summary`; `tracking.state = "unknown"`, `tracking.warnings = ["receipt finalization failed"]`. The row stays `state='applying'`, `completed_at IS NULL` → recovery-visible. |
| 6 | Return. **Verification is not run here** — the BFF proxy caps the request at 30s. | — |

**What a crash at each step leaves behind:**

| Crash point | Row after restart | Cluster | Reconciled to | Reported to the user |
|---|---|---|---|---|
| Before step 2 | no row | untouched | — | Connection error. Retrying with the **same** operation id is safe: no row exists, so it inserts fresh. |
| After 2, before 3 | `state='applying'`, `mutation_started_at IS NULL` | untouched | `failed`, `verification_state='inconclusive'` | "Never started. No object was changed. Re-preview and retry." |
| After 3, during 4 | `state='applying'`, `mutation_started_at` set, `objects` holds a prefix | partially mutated | `unknown` | "Interrupted. These N objects were recorded; anything after them is unknown. Nothing was replayed." |
| After 4, before 5 | `state='applying'`, `objects` complete | fully attempted | `unknown` | Per-object outcomes shown from `objects`; banner explains finalization was interrupted. |
| After 5 | terminal state | consistent | untouched | Normal receipt. |

**Startup reconciliation** (U29b, `main.go`, mirrors
`ESOBulkJobStore.CompleteOrphans` at `main.go:824-832`):

```sql
UPDATE change_receipts
   SET state = CASE WHEN mutation_started_at IS NULL THEN 'failed' ELSE 'unknown' END,
       verification_state = 'inconclusive',
       completed_at = NOW()
 WHERE completed_at IS NULL
```

Single-replica-safe only, exactly like the ESO bulk reaper; the Helm chart pins
one replica. **Reconciliation never re-applies anything** — it only stops a row
from claiming to be in flight forever. There is no auto-replay path anywhere in
Release E, and no code in the `changes` package calls `ApplyDocuments*` outside a
live authenticated request.

### D4. Idempotency: retry versus a genuinely new attempt

The operation id is the primary key, so the database decides. `Insert` maps
PostgreSQL `23505` to `store.ErrReceiptExists` (the exact idiom of
`ErrBulkJobActiveExists`, `eso_bulk_jobs.go:61,91-96`). On that error the service
re-reads the row and branches:

| Existing row versus this request | Result |
|---|---|
| Different `owner_id` | HTTP **409**, `reason: "operation_id_conflict"`, no detail. The other owner's cluster, digest, and object names are never disclosed. |
| Same owner, same `content_digest`, same `cluster_id`, row is terminal | HTTP **200** with the stored receipt rendered into the normal `results`/`summary` shape plus `tracking.replayed: true`. **No document is applied a second time.** |
| Same owner, same digest and cluster, row is still `state='applying'` | HTTP **409**, `reason: "operation_in_flight"`, `extra: {"receiptId": …}`. The client polls `GET /v1/changes/{id}` rather than re-submitting. |
| Same owner, different digest or different `cluster_id` | HTTP **409**, `reason: "operation_id_reused"`, message "generate a new operation id for new content". |

**Why a dropped response cannot produce a duplicate apply.** The mutation window
in step 4 is opened only after the row exists (step 2) and is stamped (step 3).
Any retry carrying the same operation id therefore collides on the primary key
before any `dr.Patch` call is reached, and lands in one of the four rows above —
every one of which either returns the stored outcome or refuses. A retry can only
apply again if the client generates a **new** operation id, which is the same
explicit act as choosing to apply again. This is exactly AE7's "a repair preview
targets only explicitly selected work" applied to the transport layer.

The honest limit, stated on the wire and in the UI: the receipt records *what
k8sCenter observed*, not a distributed-transaction guarantee. `applier.go` applies
documents independently, so a `partial` receipt means some objects changed and
some did not — never that anything was rolled back, and never that a retry is
safe without a fresh preview.

### D5. What happens to documents that were never attempted

When the step-4 observer fails, the applier stops. The remaining documents are
still emitted in `results` with `action: "failed"` and
`error: "not applied: change recording failed; re-preview and retry"`. This is
deliberate and load-bearing for backward compatibility: `wizard_controller.dart`
computes `allSucceeded => failed == 0`, so silently truncating `results` would
make a legacy mobile client report success for a bundle that was half-applied.
Emitting them as failures keeps `summary.failed > 0` and keeps `summary.total ==
len(docs)`, preserving both invariants the legacy clients rely on. The additive
`tracking.notAttempted` count is what lets a *new* client distinguish "the API
server rejected it" from "we stopped before trying it".

### D6. Verification contract (R20, KTD9)

**Verification is separate from API acceptance.** A receipt with
`state: "applied"` and `verification_state: "pending"` is a normal, expected
state and is rendered as two independent badges, never merged into one
"success". A `verification_state` of `verified` never upgrades a `partial`
`state`, and a `verification_failed` never downgrades an `applied` `state`.

**Supported postconditions (initial set).** Evaluated against the object
re-fetched live through the caller-impersonating dynamic client:

| Kind | Postcondition |
|---|---|
| `apps/v1 Deployment` | `status.observedGeneration >= metadata.generation` AND `status.updatedReplicas == spec.replicas` AND `status.availableReplicas >= spec.replicas` AND condition `Available=True` |
| `apps/v1 StatefulSet` | `status.observedGeneration >= metadata.generation` AND `status.updatedReplicas == spec.replicas` AND `status.readyReplicas >= spec.replicas` AND `status.currentRevision == status.updateRevision` |
| `apps/v1 DaemonSet` | `status.observedGeneration >= metadata.generation` AND `status.updatedNumberScheduled == status.desiredNumberScheduled` AND `status.numberReady >= status.desiredNumberScheduled` AND `status.numberUnavailable == 0` |

Every other kind returns `CheckInconclusive` with `Reason:
"kind_not_supported"`. **An unsupported kind is never reported as a pass.** This
is the specific defect being avoided: `checkReplicaMismatch`
(`rules.go:180-224`) currently returns `Status: "pass"` for both "not applicable"
and "RBAC denied me the pods", which renders as a green check for a permission
failure.

Other terminal reasons, all distinct: `not_found` (object deleted since apply) →
`fail`; `uid_changed` (object recreated under a new UID) → `inconclusive`,
`Reason: "target_recreated"` — the receipt's evidence must not transfer to a
different object with the same name (R1); `forbidden` → `inconclusive`,
`Reason: "read_forbidden"`; `window_expired` → `inconclusive`; object was applied
with `action: "failed"` → skipped entirely, never verified.

**Bounded observation, without detached credentials.** Verification is
*stateless-polled*, not a background watcher: `GET /v1/changes/{id}/verification`
performs one read per verifiable object under the caller's own impersonated
identity and persists the result. While `now - completed_at <= 120s` and any
check is still unsatisfied, the endpoint returns `verification_state: "verifying"`
and a `retryAfterSeconds: 5` hint. Past 120s, unsatisfied checks become
`inconclusive` with `Reason: "window_expired"` and the state is frozen. This
design was chosen over a background goroutine specifically to avoid using a
user's impersonation credentials after their request ended, and it removes the
need for a `recoverutil`-wrapped observation loop entirely. The only background
work Release E adds is the hourly retention sweep, which does use
`recoverutil.Tick`.

### D7. Additive response strategy for U30a

`ApplyResult`, `ApplySummary`, and `ApplyResponse` in `applier.go` are **not
modified**. A new sibling field is added to the response envelope:

```go
// ApplyResponse gains exactly one field. Tracking is nil (and therefore absent
// from the JSON, via omitempty on a pointer) for every untracked apply, so the
// untracked wire format is byte-identical to today's.
type ApplyResponse struct {
	Results  []ApplyResult `json:"results"`
	Summary  ApplySummary  `json:"summary"`
	Tracking *ApplyTracking `json:"tracking,omitempty"`
}
```

`ApplyTracking` is declared in `backend/internal/changes/types.go` and referenced
through a small interface-free struct copy in `yaml` to avoid an import cycle;
the wire shape is:

```json
{
  "operationId": "3f2a…",
  "receiptUrl": "/v1/changes/3f2a…",
  "state": "partial",
  "clusterId": "local",
  "clusterGeneration": "2026-08-02T09:11:04Z",
  "contentDigest": "sha256:9c1b…",
  "recordedThrough": 3,
  "notAttempted": 0,
  "replayed": false,
  "containsSecret": false,
  "objects": [
    { "index": 0, "group": "apps", "version": "v1", "resource": "deployments", "uid": "8b1c…" }
  ],
  "verification": { "state": "pending", "url": "/v1/changes/3f2a…/verification" },
  "warnings": []
}
```

Proof that old clients still parse it, restated concretely:

- Dart: `ApplyResponse.fromApply` reads only `json['results']` and
  `json['summary']`; an unread key in a `Map<String, dynamic>` is inert.
  `WizardApplyOutcome` reads only the four counts and `results[0]`.
- TypeScript: `ApplyResponse` is an interface over `await res.json()`. Excess
  properties on a parsed object are not a type error (excess-property checking
  applies to object literals), and no code enumerates `Object.keys(res.data)`.
- No consumer sends `?trackedOperationId=`, so no consumer will ever receive the
  field until it opts in.

A regression test in `tracked_apply_test.go` marshals an untracked
`ApplyResponse` and asserts the serialized JSON has exactly the keys
`{"results","summary"}` — the guard that keeps a future contributor from making
`Tracking` non-pointer or dropping `omitempty`.

### D8. Repair flow (R20)

There is **no** `POST /v1/changes/{id}/repair` and no server-side re-apply. The
server stores no manifest, so it has nothing to replay; that is the point.

A repair is a new tracked apply:

1. The user selects the failed objects in `ChangeReceipt.tsx`.
2. The island fills the YAML editor from **the user's own current content** —
   their still-open buffer, or a fresh `/v1/yaml/export` of the live object.
   Never from the receipt.
3. The user runs a **fresh preview** (`/v1/yaml/diff` or `/v1/yaml/validate`) and
   a fresh ownership resolution against the current cluster state, because
   ownership, the target's UID, and field ownership may all have changed since
   the original apply.
4. The user submits with a **new** operation id and optional
   `?repairOf=<originalId>`, which is recorded in `repair_of` purely as a link.

When `contains_secret` is true, the UI additionally refuses to pre-fill anything
and says so: the original Secret content is not recoverable from k8sCenter, by
design. Authorization is re-evaluated from scratch on the new apply; a user who
has since lost `patch` on the namespace gets a normal 403 from the API server,
recorded as a `failed` object in the new receipt.


---

## Verification commands (Agent Directive 4 — repo-wide, never scoped)

Referenced as **VC-BE**, **VC-FE**, **VC-E2E**, **VC-ROUTE**, **VC-FUZZ** below.

```
VC-BE     cd backend  && go vet ./... && go test ./...
VC-FE     cd frontend && deno task check && deno task test && deno task build
VC-E2E    cd e2e      && npm test
VC-ROUTE  CHECK_CLUSTER_ROUTING_GATE=fail bash scripts/check-cluster-routing.sh
VC-FUZZ   cd backend && go test <pkg> -list '^<Target>$' | grep -qx '<Target>' \
                    && go test <pkg> -run=^$ -fuzz=^<Target>$ -fuzztime=60s
```

Scoped forms (`go test ./internal/changes/`, `deno check islands/X.tsx`) are
insufficient: CI runs `deno task check` and `go test ./...` across the whole
tree and will surface sibling breakage the scoped form hides.

---

## U26. Resolve GitOps ownership evidence

**Branch:** `feat/release-e-u26-gitops-ownership`
**PR title:** `feat(gitops): resolve read-only Argo/Flux ownership evidence for a live object`
**Covers:** R18, KTD10. **Depends on:** nothing.

### Files (5 — master plan listed 3; the fuzz target and its matrix row are mandatory per CLAUDE.md)

1. `backend/internal/gitops/ownership.go` — **new**
2. `backend/internal/gitops/ownership_test.go` — **new**
3. `backend/internal/gitops/ownership_fuzz_test.go` — **new**
4. `backend/internal/gitops/types.go` — edit (append the D1 types only; no existing type is modified)
5. `.github/workflows/fuzz.yml` — edit (one matrix row)

`gitops/handler.go` is deliberately **not** in the list. `ResolveOwnership` is
declared as a method on `*Handler` inside `ownership.go`; Go allows methods on a
type to live in any file of the package, so the existing 1014-LOC handler is not
touched and Agent Directive 1 does not trigger.

### Steps

1. **`types.go`** — append the D1 block verbatim: `OwnershipController`,
   `OwnershipConfidence`, `OwnershipEvidenceKind` + `Authoritative()`,
   `OwnershipEvidence`, `ObjectRef`, `OwnershipResult`, `OwnedByApp`. Add
   `"time"` to the import block (currently there is none — `types.go` has no
   imports today, so a new `import "time"` is added at the top).

2. **`ownership.go`** — public entry point:

   ```go
   // maxDetailFetches bounds per-request Application/Kustomization detail GETs.
   // Exhausting it yields ConfidenceUnknown/"search-bound-exhausted", never
   // ConfidenceNone -- absence of evidence we did not look for is not evidence.
   const maxDetailFetches = 25

   // ResolveOwnership answers, for each ref, which GitOps controller demonstrably
   // manages it. RBAC-filtered against the caller before any matching, so an
   // application the caller cannot list contributes no evidence and leaks no
   // repository coordinates. Never returns a writable Git source (Q4 unresolved).
   func (h *Handler) ResolveOwnership(
       ctx context.Context,
       user *auth.User,
       clusterID string,
       dynClient dynamic.Interface,
       refs []ObjectRef,
   ) ([]OwnershipResult, error)
   ```

   Internal helpers, all pure and directly unit-testable:

   ```go
   // parseArgoTrackingID parses "app:group/Kind:namespace/name" from the
   // argocd.argoproj.io/tracking-id annotation. UNTRUSTED input: the value is
   // written by whoever can write the object. Returns a HINT only.
   func parseArgoTrackingID(v string) (appName, group, kind, namespace, name string, ok bool)

   // parseFluxInventoryID parses "namespace_name_group_kind". Mirrors the split
   // already used by extractFluxInventory (flux.go:301) so the two cannot drift.
   func parseFluxInventoryID(id string) (namespace, name, group, kind string, ok bool)

   // hintsFor extracts every non-authoritative signal from a live object.
   func hintsFor(obj *unstructured.Unstructured) []OwnershipEvidence

   // matchesRef reports whether a ManagedResource names exactly this ref, on
   // (group, kind, namespace, name). Case-sensitive on kind, per the API.
   func matchesRef(mr ManagedResource, ref ObjectRef) bool

   // decide applies the KTD10 rule: only evidence whose Kind.Authoritative() is
   // true may produce ConfidenceConfirmed, and Apps/Source are populated only
   // from authoritative matches.
   func decide(ref ObjectRef, ev []OwnershipEvidence, apps []OwnedByApp, argoState, fluxState toolState) OwnershipResult
   ```

   `decide` is the single chokepoint for the KTD10 rule and the target of the
   fuzz oracle. `hintsFor` caps `RawValue` at 256 bytes and strips control
   characters before it is ever placed in a response.

3. **Evidence gathering order** — as specified in D1 step 2/3. Discovery status
   first (`h.Discoverer.Status()`), then `h.fetchApps(ctx)` +
   `h.filterAppsByRBAC(ctx, user, apps)`, then bounded per-app detail GETs
   through `dynClient` (caller-impersonated, supplied by the caller so U26 does
   not itself call `RouterFor` and does not need a routing-guard exemption).
   Flux `HelmRelease` applications are skipped with an explicit
   `flux-helmrelease-no-inventory` note rather than silently.

4. **`ownership_test.go`** — table-driven, using `dynamicfake.NewSimpleDynamicClient`
   with an explicit `runtime.NewScheme()` and a
   `map[schema.GroupVersionResource]string` list-kind map, exactly as
   `gitops/argocd_test.go:1-13` and `flux_test.go` already do. `AccessChecker` is
   supplied via the existing `resources.NewPredicateAccessChecker(fn)` /
   `NewAlwaysDenyAccessChecker()` test constructors (`access.go:280,310`).

5. **`ownership_fuzz_test.go`** — `FuzzOwnershipEvidence`, in-package.
   - **Oracle A (crash-safety):** `parseArgoTrackingID`, `parseFluxInventoryID`,
     `hintsFor`, and `decide` never panic on any input, including deeply nested
     `unstructured` maps built by the shared `unstructuredFromFuzz` helper
     pattern used by `gitops/normalize_fuzz_test.go`.
   - **Oracle C (guard cannot be bypassed):** for any evidence slice containing
     **no** element whose `Kind.Authoritative()` is true, `decide` must return
     `Confidence != ConfidenceConfirmed`, `len(Apps) == 0`, and
     `WritableGitSource == false`. The oracle re-derives "authoritative" from its
     own literal set `{argo-status-resource, flux-inventory-entry}` rather than
     calling `Authoritative()`, so deleting a case from `Authoritative()` fails
     the fuzz instead of silently agreeing with it.
   - **Teeth check (mandatory before merge):** temporarily change
     `Authoritative()` to `return true`, confirm the corpus fails, revert. Record
     the result in the PR description.
   - Seeds: a well-formed tracking id; a tracking id with an embedded `:` in the
     name; `_`-heavy Flux ids; an empty id; a 4 KiB annotation value; an id whose
     `kind` segment itself contains `_`.

6. **`fuzz.yml`** — append to the matrix (line ~35, after the gateway row):

   ```yaml
          - { pkg: ./internal/gitops/, target: FuzzOwnershipEvidence }
   ```

### Test functions → scenarios

| Test | Master-plan scenario / cross-cutting case |
|---|---|
| `TestResolveOwnership_ConfirmedArgo` | "Confirmed Argo" |
| `TestResolveOwnership_ConfirmedFluxKustomization` | "Confirmed Flux" |
| `TestResolveOwnership_BothControllersClaim` | "both" → `conflicting`, both apps listed |
| `TestResolveOwnership_NoEvidence` | "neither" → `none` / `no-evidence` |
| `TestResolveOwnership_ForbiddenInventory` | "forbidden inventory" → `forbidden`, and **no `repoURL` in the payload** |
| `TestResolveOwnership_StaleTrackingMetadataIsHintOnly` | "stale tracking metadata": annotation names an Application whose `status.resources[]` no longer lists the object → `unknown` / `hints-only`, `Apps` empty |
| `TestResolveOwnership_InstanceLabelAloneNeverConfirms` | KTD10 core rule |
| `TestResolveOwnership_FluxHelmReleaseHasNoInventory` | blind spot reported, not guessed |
| `TestResolveOwnership_SearchBoundExhausted` | `maxDetailFetches` → `unknown`, not `none` |
| `TestResolveOwnership_ReadOnlyUserLearnsNothing` | cross-user access: a caller filtered out by `filterAppsByRBAC` sees no app id, name, namespace, or `repoURL` |
| `TestResolveOwnership_RevokedPermissionMidSession` | revoked permission: `AccessChecker` flips to deny between two calls → second call returns `forbidden`, not a cached `confirmed` |
| `TestResolveOwnership_RecreatedTargetSameName` | deleted/recreated target: `IdentityBasis` is `group-kind-namespace-name` and `UIDConfirmed` is false, so the result is explicitly name-scoped |
| `TestResolveOwnership_ArgoNotInstalled` | unavailable controller ≠ `none` |
| `TestResolveOwnership_ContextCancelled` | cancellation: returns `ctx.Err()`, no partial slice |
| `TestOwnershipEvidenceKind_Authoritative` | pins the KTD10 set |
| `FuzzOwnershipEvidence` | Oracle A + Oracle C |

Not applicable to U26: unavailable DB, crash/restart, retention, secret
redaction, legacy-client compatibility — U26 adds no persistence and no route.

### Verification

VC-BE, VC-ROUTE, plus `VC-FUZZ` with `<pkg>=./internal/gitops/`,
`<Target>=FuzzOwnershipEvidence`.

### Exit criteria — done means

- [ ] All six `OwnershipConfidence` values are reachable and covered by a named test.
- [ ] `Apps` and `AppSource` are provably unreachable from hint-only evidence (unit test + fuzz Oracle C).
- [ ] `WritableGitSource` is `false` on every code path (grep shows one literal assignment).
- [ ] RBAC filtering happens before matching; no test can produce a `repoURL` for an application the caller cannot list.
- [ ] `fuzz.yml` row added and the `-list` guard passes locally.
- [ ] Teeth check performed and recorded in the PR body.
- [ ] VC-BE and VC-ROUTE green.

---

## U27. Persist operation intent and receipts

**Branch:** `feat/release-e-u27-change-receipt-store`
**PR title:** `feat(store): add change_receipts (migration 000021) and the receipt store`
**Covers:** R19, R20, KTD6, KTD9, Q1. **Depends on:** nothing (Q1 resolved).

### Files (5 — master plan listed 4; `NOTES.txt` is required by the repo's own migration convention)

1. `backend/internal/store/migrations/000021_create_change_receipts.up.sql` — **new**
2. `backend/internal/store/migrations/000021_create_change_receipts.down.sql` — **new**
3. `backend/internal/store/change_receipts.go` — **new**
4. `backend/internal/store/change_receipts_test.go` — **new** (first `*_test.go` in `internal/store/`)
5. `backend/internal/store/migrations/NOTES.txt` — edit (append a `000021` section)

**Sequence number is `000021`, fixed.** The highest existing migration is
`000017`; `000018`, `000019`, `000020`, and `000022` belong to other tracks. Do
not renumber even if `000018` is still absent when this lands — golang-migrate
applies by version order and tolerates gaps, and renumbering would collide.

### Steps

1. **`000021_*.up.sql` / `.down.sql`** — exactly the DDL in D2. Note for the
   implementer: `document_count` has no default because a receipt without one is
   meaningless; `state` and `verification_state` use `TEXT ... CHECK (... IN ...)`
   rather than a PG enum, per `000011:8` and `000013:5`, so extending the sets
   later is a `DROP CONSTRAINT` / `ADD CONSTRAINT` migration like `000010`.

2. **`change_receipts.go`** — package `store`, following `eso_bulk_jobs.go`:

   ```go
   // ErrReceiptExists is returned by Insert when the client-supplied operation
   // id is already present. The caller inspects the existing row to decide
   // between replay, in-flight, reuse, and cross-owner conflict.
   var ErrReceiptExists = errors.New("change receipt already exists for this operation id")

   type ReceiptState string
   const (
       ReceiptPreviewed ReceiptState = "previewed"
       ReceiptApplying  ReceiptState = "applying"
       ReceiptApplied   ReceiptState = "applied"
       ReceiptPartial   ReceiptState = "partial"
       ReceiptFailed    ReceiptState = "failed"
       ReceiptUnknown   ReceiptState = "unknown"
   )

   type VerificationState string
   const (
       VerifyPending      VerificationState = "pending"
       VerifyVerifying    VerificationState = "verifying"
       VerifyVerified     VerificationState = "verified"
       VerifyInconclusive VerificationState = "inconclusive"
       VerifyFailed       VerificationState = "verification_failed"
   )

   type ReceiptObject struct {
       Index      int       `json:"index"`
       Group      string    `json:"group,omitempty"`
       Version    string    `json:"version,omitempty"`
       Resource   string    `json:"resource,omitempty"`
       Kind       string    `json:"kind"`
       Namespace  string    `json:"namespace,omitempty"`
       Name       string    `json:"name"`
       UID        string    `json:"uid,omitempty"`
       Action     string    `json:"action"`
       Error      string    `json:"error,omitempty"`
       RecordedAt time.Time `json:"recordedAt"`
   }

   type ChangeReceipt struct {
       ID                uuid.UUID
       OwnerID           string
       OwnerUsername     string
       ClusterID         string
       ClusterGeneration string
       ContentDigest     string
       DocumentCount     int
       Force             bool
       ContainsSecret    bool
       RepairOf          *uuid.UUID
       State             ReceiptState
       Objects           []ReceiptObject
       Ownership         json.RawMessage
       VerificationState VerificationState
       Verification      json.RawMessage
       CreatedAt         time.Time
       MutationStartedAt *time.Time
       CompletedAt       *time.Time
       VerifiedAt        *time.Time
   }

   type ChangeReceiptStore struct{ pool *pgxpool.Pool }

   func NewChangeReceiptStore(pool *pgxpool.Pool) *ChangeReceiptStore

   // Insert records the operation intent. Returns ErrReceiptExists on 23505.
   func (s *ChangeReceiptStore) Insert(ctx context.Context, r ChangeReceipt) error

   // MarkMutationStarted stamps mutation_started_at exactly once. Returns
   // ErrReceiptNotFound when the guarded UPDATE matches no row.
   func (s *ChangeReceiptStore) MarkMutationStarted(ctx context.Context, id uuid.UUID) error

   // AppendObject appends one per-document outcome. Append-only: an outcome is
   // never rewritten, so a crash leaves a truthful prefix.
   func (s *ChangeReceiptStore) AppendObject(ctx context.Context, id uuid.UUID, o ReceiptObject) error

   // Finalize sets the terminal state and completed_at.
   func (s *ChangeReceiptStore) Finalize(ctx context.Context, id uuid.UUID, state ReceiptState) error

   func (s *ChangeReceiptStore) SetVerification(ctx context.Context, id uuid.UUID, state VerificationState, payload json.RawMessage) error
   func (s *ChangeReceiptStore) SetOwnership(ctx context.Context, id uuid.UUID, payload json.RawMessage) error

   func (s *ChangeReceiptStore) Get(ctx context.Context, id uuid.UUID) (*ChangeReceipt, error) // (nil, nil) when absent
   func (s *ChangeReceiptStore) ListForOwner(ctx context.Context, p ReceiptQueryParams) ([]ChangeReceipt, int, error)
   func (s *ChangeReceiptStore) GrantsFor(ctx context.Context, id uuid.UUID) ([]string, error)

   // ReconcileOrphans marks every non-terminal receipt after a restart:
   // failed when the mutation never started, unknown when it had. NEVER replays.
   func (s *ChangeReceiptStore) ReconcileOrphans(ctx context.Context) (int64, error)

   func (s *ChangeReceiptStore) Cleanup(ctx context.Context, retentionDays int) (int64, error)
   ```

   Real SQL for the three ordering-critical writes:

   ```sql
   -- MarkMutationStarted (guarded, idempotent)
   UPDATE change_receipts
      SET mutation_started_at = NOW()
    WHERE id = $1 AND mutation_started_at IS NULL

   -- AppendObject (append-only, mirrors eso_bulk_jobs.go:193-199)
   UPDATE change_receipts
      SET objects = objects || $2::jsonb
    WHERE id = $1

   -- Finalize (guarded so a reconciled row is not re-finalized)
   UPDATE change_receipts
      SET state = $2, completed_at = NOW()
    WHERE id = $1 AND completed_at IS NULL
   ```

   `ReceiptQueryParams` copies `audit.QueryParams` (`audit/query.go:5-38`):
   `Page`/`PageSize` with `DefaultPageSize = 50`, `MaxPageSize = 200`,
   `Normalize()`, `Offset()`. **Offset pagination, not cursors** — no cursor
   pagination exists anywhere in this repo, and inventing one here would be an
   unreviewed second idiom.

   `Cleanup` follows `eso_history.go:162-171`: reject `retentionDays < 1`, wrap in
   `context.WithTimeout(ctx, cleanupTimeout)` with a package-level
   `cleanupTimeout = 5 * time.Minute`, and
   `DELETE FROM change_receipts WHERE created_at < NOW() - $1 * INTERVAL '1 day'`.
   `change_receipt_grants` disappears via `ON DELETE CASCADE`.

3. **`NOTES.txt`** — append a `000021_create_change_receipts` section covering:
   what the table stores and, emphatically, what it does not (no manifest
   content, so no Secret exposure); that `id` is client-supplied and therefore
   attacker-influenceable in shape but constrained to UUIDv4 by the handler;
   the 30-day default and the
   `KUBECENTER_CHANGES_RECEIPTRETENTIONDAYS` override; the single-replica
   constraint on `ReconcileOrphans`; the inspection query

   ```sql
   SELECT id, owner_username, cluster_id, state, verification_state, created_at
     FROM change_receipts
    WHERE completed_at IS NULL
    ORDER BY created_at DESC;
   ```

   and the rollback note: `000021.down.sql` drops both tables and destroys all
   receipt history; there is no forward path that preserves it, and a backend
   image containing the Release E handler will return 503 on `/changes` rather
   than crash, because every access is nil-guarded.

### Test functions → scenarios

Pure-Go only. **`internal/store/` has no PostgreSQL test harness** — no build
tags, no testcontainers, no env gates, and `audit/store_test.go:43-48` documents
the convention explicitly. Round-trip and migration behaviour are manual smoke,
listed under Verification below.

| Test | Scenario |
|---|---|
| `TestReceiptQueryParams_Normalize` | page/pageSize clamping, `Offset()` |
| `TestReceiptState_CheckConstraintSetsMatchGoConstants` | every Go constant appears in the SQL `CHECK` list (parses the embedded migration via `migrationsFS`) — the drift guard between D2's DDL and the Go enums |
| `TestVerificationState_CheckConstraintSetsMatchGoConstants` | same for verification |
| `TestReceiptObject_MarshalOmitsEmptyOptionalFields` | JSONB shape stability |
| `TestReceiptObject_NeverCarriesManifestFields` | secret redaction: reflect over `ReceiptObject` and assert no field name matches `(?i)(data|stringData|spec|manifest|content|body|yaml)`; the structural guarantee that no manifest can be persisted |
| `TestChangeReceipt_NeverCarriesManifestFields` | same over `ChangeReceipt` |
| `TestCleanup_RejectsInvalidRetention` | retention: `retentionDays < 1` errors before any SQL |
| `TestNewChangeReceiptStore_NilPoolIsConstructible` | unavailable DB: construction never panics; callers nil-guard the store, not the pool |
| `TestMigration000021_UpAndDownAreWellFormed` | reads both files from `migrationsFS`, asserts the up creates both tables and every index, the down drops both, and that `000021` is the only new sequence introduced |
| `TestMigration000021_DownDropsGrantsBeforeReceipts` | FK ordering |

Deferred to manual smoke and named in the PR body: duplicate-operation-id
insert returning `ErrReceiptExists`; `AppendObject` producing a truthful prefix
after a killed process; `ReconcileOrphans` splitting `failed` vs `unknown` on
`mutation_started_at`; migration applied to a populated database with unrelated
tables intact; `000021.down.sql` removing only the two new tables.

### Verification

VC-BE, plus manual: `make dev-db`, start the backend once to run `m.Up()`,
confirm `schema_migrations.version = 21` and `dirty = false`, insert two rows by
hand, run the down migration, confirm no other table changed.

### Exit criteria — done means

- [ ] Migration is `000021` and nothing else; `.up`/`.down` pair present.
- [ ] No field anywhere in the store can hold manifest content (test-enforced).
- [ ] Go enum sets and SQL `CHECK` sets are equal (test-enforced).
- [ ] `Insert` maps `23505` to `ErrReceiptExists`.
- [ ] `Finalize` and `MarkMutationStarted` are guarded so a reconciled row is not resurrected.
- [ ] `Cleanup` bounded by `context.WithTimeout` and rejects `< 1` days.
- [ ] `NOTES.txt` section added with inspection SQL and the rollback caveat.
- [ ] VC-BE green.

---

## U28. Coordinate tracked apply and verification

**Branch:** `feat/release-e-u28-changes-service`
**PR title:** `feat(changes): tracked-apply coordination and bounded postcondition verification`
**Covers:** R18–R20, KTD6, KTD9. **Depends on:** U26, U27; U20 for check results (fallback below).

### Files (5)

1. `backend/internal/changes/types.go` — **new** (`ApplyTracking`, `TrackedApplyRequest`, `TrackedApplyOutcome`, `ReceiptView`)
2. `backend/internal/changes/service.go` — **new**
3. `backend/internal/changes/service_test.go` — **new**
4. `backend/internal/changes/verifier.go` — **new** (incl. the U20-shaped `CheckResult` fallback)
5. `backend/internal/changes/verifier_test.go` — **new**

No HTTP handler and no route here — that is U29a/U29b. No `yaml` package edit —
that is U30a.

### Steps

1. **`types.go`** — the D7 wire struct plus:

   ```go
   type TrackedApplyRequest struct {
       OperationID   uuid.UUID
       RepairOf      *uuid.UUID
       User          *auth.User
       ClusterID     string
       ClusterGen    string
       RawBody       []byte // used ONLY to compute the digest; never stored
       Docs          []*unstructured.Unstructured
       Force         bool
       Dynamic       dynamic.Interface
       Mapper        meta.RESTMapper
   }
   ```

   `RawBody` is documented as digest-only input and is not retained past
   `computeDigest`.

2. **`service.go`**:

   ```go
   type Service struct {
       receipts      *store.ChangeReceiptStore
       clusterRouter *k8s.ClusterRouter
       clusters      *store.ClusterStore // optional; nil => empty ClusterGeneration
       access        *resources.AccessChecker
       audit         audit.Logger
       logger        *slog.Logger
   }

   func NewService(receipts *store.ChangeReceiptStore, cr *k8s.ClusterRouter,
       clusters *store.ClusterStore, ac *resources.AccessChecker,
       al audit.Logger, logger *slog.Logger) *Service

   // Available reports whether tracked execution can run at all. False when the
   // receipt store is nil (no PostgreSQL) -- callers return 503 and leave the
   // untracked apply path untouched.
   func (s *Service) Available() bool

   // TrackedApply runs the D3 protocol. apply is injected by the yaml package so
   // this package never imports yaml (which would be an import cycle) and never
   // reimplements the apply engine.
   func (s *Service) TrackedApply(
       ctx context.Context,
       req TrackedApplyRequest,
       apply func(ctx context.Context, obs ApplyObserverFunc) TrackedApplyOutcome,
   ) (*ApplyTracking, error)
   ```

   `ApplyObserverFunc` and `TrackedApplyOutcome` are declared here in terms of
   plain data (index, group/version/resource/kind/ns/name/uid, action, error), so
   the dependency direction is `yaml → changes`, never the reverse.

   `computeDigest(raw []byte) string` returns
   `"sha256:" + hex.EncodeToString(h[:])`.

   **Audit.** `TrackedApply` writes **no** audit entries. `yaml/handler.go`
   already emits one `audit.Entry{Action: audit.ActionApply}` per result
   (`handler.go:162-179`) and continues to do so unchanged for tracked and
   untracked applies alike. Duplicating them here would double every apply row.
   The receipt id is threaded into the existing entries' `Detail` field as
   `"<action> op=<operationId>"` in U30a — an append to an existing free-text
   field, not a schema change, and the only audit-visible difference.

3. **`verifier.go`**:

   ```go
   // verificationWindow bounds observation from receipt completion. Past this,
   // unsatisfied checks freeze as inconclusive/"window_expired".
   const verificationWindow = 120 * time.Second

   // VerifyOnce performs exactly one live read per verifiable object under the
   // caller's own impersonated identity and returns normalized results. It is
   // called from a request goroutine and starts no background work, so no
   // user credential outlives the request that supplied it.
   func (s *Service) VerifyOnce(
       ctx context.Context,
       user *auth.User,
       r *store.ChangeReceipt,
       dyn dynamic.Interface,
   ) (store.VerificationState, []CheckResult, error)

   // CheckRollout evaluates the rollout postcondition for one live object.
   // Returns CheckInconclusive/"kind_not_supported" outside
   // {Deployment, StatefulSet, DaemonSet}. Pure: no I/O, fully table-testable.
   func CheckRollout(obj *unstructured.Unstructured, src SourceRef, now time.Time) CheckResult
   ```

   `CheckRollout` implements D6 exactly, including `observedGeneration` and the
   condition reads that `checkReplicaMismatch` omits today.

4. **U20 fallback.** `verifier.go` declares `CheckStatus`, `CheckResult`, and
   `SourceRef` locally with the field names and JSON tags fixed by the interface
   contract section below, behind a comment naming the swap:

   ```go
   // Local mirror of the Release D U20 contract. When
   // diagnostics.CheckResult lands, delete these declarations and replace them
   // with type aliases:
   //   type CheckResult = diagnostics.CheckResult
   //   type CheckStatus = diagnostics.CheckStatus
   //   type SourceRef   = diagnostics.SourceRef
   // The JSON tags below are load-bearing: they are already persisted in
   // change_receipts.verification, so the alias swap must not change the wire
   // shape.
   ```

5. **Cluster generation.** `ClusterGen` is `clusters.updated_at` in RFC3339 for a
   remote cluster and the literal `"local"` for the local cluster. On read, when
   the current value differs from the stored one, the receipt view sets
   `targetGenerationChanged: true`. Release E adds **no** column for this; a
   real generation is KTD3's job in another track.

### Test functions → scenarios

| Test | Scenario |
|---|---|
| `TestTrackedApply_InsertFailsBeforeMutation_NothingApplied` | DB unavailable before mutation prevents tracked execution — the injected `apply` func asserts it was never called |
| `TestTrackedApply_MarkMutationStartedFails_NothingApplied` | same at step 3 |
| `TestTrackedApply_AppendFailsMidBundle_StopsAndReportsNotAttempted` | failure after one mutation preserves partial outcome, no replay; remaining docs come back `failed` + `notAttempted > 0` |
| `TestTrackedApply_FinalizeFails_ReportsUnknown` | interrupted finalization → `state: unknown`, warning present, row left recovery-visible |
| `TestTrackedApply_NeverReplaysOnAnyPath` | asserts `apply` is invoked at most once across every error branch |
| `TestTrackedApply_DuplicateOperationID_SameDigest_Replays` | idempotency: stored receipt returned, `apply` not called, `replayed: true` |
| `TestTrackedApply_DuplicateOperationID_DifferentDigest_Conflicts` | `operation_id_reused` |
| `TestTrackedApply_DuplicateOperationID_DifferentOwner_Conflicts` | cross-user access: 409 with no detail about the other owner |
| `TestTrackedApply_DuplicateOperationID_InFlight_Conflicts` | `operation_in_flight` |
| `TestTrackedApply_PartialSummaryMatchesReceipt` | **AE7**: three documents, two succeed, one fails → receipt holds all three, `state: partial`, never `applied` |
| `TestTrackedApply_ContextCancelled_LeavesRecoverableIntent` | cancellation |
| `TestTrackedApply_ClusterGenerationChanged_IsFlagged` | deleted/recreated cluster registration |
| `TestTrackedApply_WritesNoAuditEntries` | audit is not duplicated |
| `TestTrackedApply_DigestCoversRawBodyNotParsedDocs` | digest stability |
| `TestVerifyOnce_DeploymentReady_Verified` | Deployment readiness |
| `TestVerifyOnce_DeploymentStaleObservedGeneration_Verifying` | the gap `checkReplicaMismatch` has today |
| `TestVerifyOnce_StatefulSetRevisionMismatch_Verifying` | StatefulSet |
| `TestVerifyOnce_DaemonSetUnavailable_Verifying` | DaemonSet |
| `TestVerifyOnce_ConfigMapIsInconclusiveNotPass` | unsupported kind never silently passes |
| `TestVerifyOnce_TargetDeleted_Fails` | deleted target |
| `TestVerifyOnce_TargetRecreatedNewUID_Inconclusive` | deleted/recreated target: evidence does not transfer (R1) |
| `TestVerifyOnce_Forbidden_Inconclusive` | revoked permission → inconclusive, not pass |
| `TestVerifyOnce_WindowExpired_Inconclusive` | verification timeout |
| `TestVerifyOnce_ControllerOverwroteFields_StillReportsObserved` | controller overwrite stays distinguishable |
| `TestVerifyOnce_FailedObjectsAreNotVerified` | a `failed` apply result is skipped, never counted as verified |
| `TestVerifyOnce_StartsNoBackgroundGoroutine` | pins the no-detached-credential design |
| `TestCheckRollout_TableDriven` | every D6 predicate, both polarities |

### Verification

VC-BE, VC-ROUTE.

### Exit criteria — done means

- [ ] `changes` does not import `yaml` (grep the import block).
- [ ] Every abort path is proven not to have called `apply`.
- [ ] `state` and `verification_state` are provably independent (a test asserts `applied` + `pending` round-trips).
- [ ] No `go func`, no `recoverutil` use, and no background goroutine in this package.
- [ ] Unsupported kinds return `inconclusive`, never `pass`.
- [ ] VC-BE green.


---

## U29a. Authorized receipt handler (split from master-plan U29)

**Branch:** `feat/release-e-u29a-changes-handler`
**PR title:** `feat(changes): authorized receipt and ownership HTTP handlers`
**Covers:** R2, R19, R20, Q1. **Depends on:** U26, U27, U28.

**Why U29 is split.** The master plan lists five files for U29
(`handler.go`, `handler_test.go`, `server.go`, `routes.go`, `main.go`), but a new
handler package also requires `scripts/check-cluster-routing.sh` to list
`backend/internal/changes` in `HANDLER_DIRS`, and the redaction guard needs a
fuzz target plus its `fuzz.yml` row. That is eight files. U29a takes the package
and its guards; U29b takes the three shared wiring files.

### Files (5)

1. `backend/internal/changes/handler.go` — **new** (includes the pure authorization/redaction functions)
2. `backend/internal/changes/handler_test.go` — **new**
3. `backend/internal/changes/redaction_fuzz_test.go` — **new**
4. `scripts/check-cluster-routing.sh` — edit (`HANDLER_DIRS` += `backend/internal/changes`)
5. `.github/workflows/fuzz.yml` — edit (one matrix row)

### Steps

1. **`handler.go`**:

   ```go
   type Handler struct {
       Service       *Service
       GitOps        OwnershipResolver // satisfied by *gitops.Handler
       ClusterRouter *k8s.ClusterRouter
       Access        *resources.AccessChecker
       Logger        *slog.Logger
   }

   // OwnershipResolver is the narrow view of *gitops.Handler this package needs,
   // declared here so changes does not depend on the whole gitops handler.
   type OwnershipResolver interface {
       ResolveOwnership(ctx context.Context, user *auth.User, clusterID string,
           dyn dynamic.Interface, refs []gitops.ObjectRef) ([]gitops.OwnershipResult, error)
   }

   func NewHandler(s *Service, g OwnershipResolver, cr *k8s.ClusterRouter,
       ac *resources.AccessChecker, logger *slog.Logger) *Handler

   func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request)
   func (h *Handler) HandleGet(w http.ResponseWriter, r *http.Request)
   func (h *Handler) HandleVerification(w http.ResponseWriter, r *http.Request)
   func (h *Handler) HandleResolveOwnership(w http.ResponseWriter, r *http.Request)
   ```

   Every handler opens with `httputil.RequireUser`, then the 503 guard in the
   exact repo idiom (`externalsecrets/bulk.go:293-296`):

   ```go
   if h.Service == nil || !h.Service.Available() {
       httputil.WriteError(w, http.StatusServiceUnavailable,
           "change receipts require a database", "")
       return
   }
   ```

   Cluster access goes through `h.ClusterRouter.RouterFor(r.Context(), clusterID,
   user.KubernetesUsername, user.KubernetesGroups)` — never
   `K8sClient.DynamicClientForUser` — so `VC-ROUTE` passes with no exemption
   comment.

2. **The two pure authorization functions** — the whole Q1 policy, in one place
   and directly fuzzable:

   ```go
   // ReadDecision is the envelope-level gate: ownership, explicit grant, admin.
   type ReadDecision int
   const (
       ReadDenied ReadDecision = iota
       ReadAsOwner
       ReadAsGrantee
       ReadAsAdmin
   )

   // authorizeReceiptRead decides envelope visibility only. It never inspects
   // object contents; per-object visibility is redactObjects' job.
   func authorizeReceiptRead(user *auth.User, r *store.ChangeReceipt, grants []string) ReadDecision

   // redactObjects applies Q1's read-time re-authorization. allowed reports the
   // caller's CURRENT k8s authorization for one object; containsSecret triggers
   // the stricter Secret filtering. A denied object keeps ONLY its index.
   func redactObjects(objs []store.ReceiptObject, containsSecret bool,
       allowed func(o store.ReceiptObject) bool) (out []ReceiptObjectView, redacted int)

   // ReceiptObjectView is the wire form. Every field except Index and Redacted
   // is dropped when Redacted is true.
   type ReceiptObjectView struct {
       Index     int    `json:"index"`
       Redacted  bool   `json:"redacted,omitempty"`
       Reason    string `json:"reason,omitempty"` // "forbidden" | "secret-filtered"
       Group     string `json:"group,omitempty"`
       Version   string `json:"version,omitempty"`
       Resource  string `json:"resource,omitempty"`
       Kind      string `json:"kind,omitempty"`
       Namespace string `json:"namespace,omitempty"`
       Name      string `json:"name,omitempty"`
       UID       string `json:"uid,omitempty"`
       Action    string `json:"action,omitempty"`
       // ErrorClass replaces the raw error for Secret-bearing receipts, because
       // admission and validation messages routinely echo submitted values.
       Error      string `json:"error,omitempty"`
       ErrorClass string `json:"errorClass,omitempty"`
   }
   ```

   `allowed` is wired in `HandleGet` to
   `h.Access.CanAccessGroupResource(ctx, r.ClusterID, user.KubernetesUsername,
   user.KubernetesGroups, "get", o.Group, o.Resource, o.Namespace)`, memoized per
   `(group, resource, namespace)` for the request, exactly as
   `gitops/handler.go:349-386` memoizes its own decisions.

3. **Routes served** (registered in U29b):

   | Method + path | Handler | Notes |
   |---|---|---|
   | `GET /api/v1/changes` | `HandleList` | owner-scoped; `?page=&pageSize=`; emits `api.Metadata{Total,Page,PageSize}` via `httputil.WriteJSON` because `WriteData` never sets metadata |
   | `GET /api/v1/changes/{id}` | `HandleGet` | envelope gate + per-object redaction |
   | `GET /api/v1/changes/{id}/verification` | `HandleVerification` | one `VerifyOnce` pass, persists, returns `{state, checks, retryAfterSeconds}` |
   | `POST /api/v1/changes/ownership` | `HandleResolveOwnership` | body `{clusterId, objects:[{group,version,kind,namespace,name}]}`, capped at 50 refs; POST because the input is a list, CSRF inherited |

   `{id}` is parsed with `uuid.Parse` and a `Version() == 4` check; anything else
   is a 400 before any store call. A receipt the caller may not read returns
   **404**, not 403, so receipt ids are not enumerable.

4. **`redaction_fuzz_test.go`** — `FuzzReceiptRedaction`, **Oracle D**
   (secret-leak masking), the same oracle class as `FuzzMaskedSecret`:
   for arbitrary `[]store.ReceiptObject` and an arbitrary deny-biased `allowed`
   predicate, every emitted `ReceiptObjectView` with `Redacted == true` must
   marshal to JSON containing none of the source object's `Name`, `Namespace`,
   `Kind`, `UID`, or `Error` substrings (non-empty ones, length ≥ 3). Plus
   **Oracle C**: when `containsSecret` is true, no emitted view for a
   `Kind == "Secret"` object may carry `Error`, only `ErrorClass`. The oracle
   re-derives the redaction rule from its own literal field list rather than
   calling the production helper. Teeth check: temporarily make `redactObjects`
   pass the object through unchanged and confirm the corpus fails.

5. **`scripts/check-cluster-routing.sh`** — append `backend/internal/changes` to
   `HANDLER_DIRS` (the space-separated list). The header comment already
   explains that the list must be a superset of every package exposing handlers.

6. **`fuzz.yml`** — append:

   ```yaml
          - { pkg: ./internal/changes/, target: FuzzReceiptRedaction }
   ```

### Test functions → scenarios

| Test | Scenario |
|---|---|
| `TestHandleGet_OwnerSeesOwnReceipt` | baseline |
| `TestHandleGet_OtherUserGets404` | cross-user access, non-enumerable ids |
| `TestHandleGet_GranteeCanRead` | Q1 explicit grants honoured |
| `TestHandleGet_AdminCanRead` | admin path |
| `TestHandleGet_RevokedNamespaceAccessRedactsObject` | revoked permission: owner still sees the envelope; the object collapses to `{index, redacted, reason:"forbidden"}` |
| `TestHandleGet_SecretBearingReceiptFiltersErrorText` | secret redaction: `errorClass` only |
| `TestHandleGet_SecretObjectRedactedWithoutSecretGet` | stricter Secret filtering |
| `TestHandleGet_RedactedCountReportedAndSummaryPreserved` | historical counts intact |
| `TestHandleGet_DeletedRecreatedTargetStillShowsStoredUID` | deleted/recreated target: the receipt shows the UID it acted on, and verification (not the receipt) reports the mismatch |
| `TestHandleGet_NoDatabaseReturns503` | unavailable DB |
| `TestHandleGet_InvalidUUIDReturns400` | input validation |
| `TestHandleList_OwnerScopedAndPaginated` | owner scoping + `metadata.total` |
| `TestHandleList_NeverReturnsAnotherOwnersRow` | cross-user access |
| `TestHandleVerification_PersistsAndFreezesAfterWindow` | verification timeout, retention of the frozen state |
| `TestHandleVerification_ForbiddenObjectIsInconclusive` | revoked permission |
| `TestHandleVerification_RequestCancelled` | cancellation |
| `TestHandleResolveOwnership_CapsRefCount` | input bound |
| `TestHandleResolveOwnership_ForbiddenAppLeaksNoRepoURL` | cross-user access |
| `TestHandlers_RequireAuth` | 401 without a user in context |
| `TestHandlers_LegacyApplyEnvelopeUnaffected` | legacy-client compatibility: asserts this package registers no route under `/yaml` and defines no type that alters the apply envelope |
| `FuzzReceiptRedaction` | Oracle C + Oracle D |

### Verification

VC-BE, VC-ROUTE (now scanning the new package), `VC-FUZZ` with
`<pkg>=./internal/changes/`, `<Target>=FuzzReceiptRedaction`.

### Exit criteria — done means

- [ ] All four routes' handlers exist, each with the 503 nil-guard and `RequireUser`.
- [ ] Unauthorized receipt reads are 404, never 403.
- [ ] Redaction keeps only `index` (test + fuzz-enforced).
- [ ] `backend/internal/changes` is in `HANDLER_DIRS` and `VC-ROUTE` passes in `fail` mode.
- [ ] `fuzz.yml` row added; teeth check recorded in the PR body.
- [ ] No direct `ClientForUser` / `DynamicClientForUser` call in the package.
- [ ] VC-BE green.

---

## U29b. Wire the changes service, routes, and startup reconciliation

**Branch:** `feat/release-e-u29b-changes-wiring`
**PR title:** `feat(server): register /changes routes and wire receipt store, retention, and startup reconciliation`
**Covers:** R2, R19, R20; KTD1. **Depends on:** U29a.

### Files (4)

1. `backend/internal/server/server.go` — edit (2 struct fields + 1 nil-guard copy block)
2. `backend/internal/server/routes.go` — edit (1 registration line + 1 `registerChangesRoutes` func)
3. `backend/cmd/kubecenter/main.go` — edit (DI block + `Deps` fields)
4. `backend/internal/config/config.go` — edit (`Changes.ReceiptRetentionDays`)

### Steps

1. **`config.go`** — add, mirroring the existing `Audit.RetentionDays`:

   ```go
   // Changes holds tracked-change (Release E) settings.
   type Changes struct {
       // ReceiptRetentionDays bounds how long change receipts are kept.
       // Env: KUBECENTER_CHANGES_RECEIPTRETENTIONDAYS. Default 30 (Q1).
       ReceiptRetentionDays int `koanf:"receiptretentiondays"`
   }
   ```

   plus the `Changes Changes \`koanf:"changes"\`` field on `Config` and the
   default `30` alongside the other defaults. The koanf env mapping is
   `KUBECENTER_CHANGES_RECEIPTRETENTIONDAYS` → `Config.Changes.ReceiptRetentionDays`,
   per the nested-path rule in `CLAUDE.md`.

2. **`server.go`** — two fields on both `Server` and `Deps`, placed next to
   `GitOpsHandler`:

   ```go
   	ChangesHandler     *changes.Handler
   	ChangesService     *changes.Service
   ```

   and, in the copy-through block (`server.go:230-327`, same shape as the GitOps
   entry at :~253):

   ```go
   	// Tracked changes handler + service (Release E)
   	if deps.ChangesHandler != nil {
   		s.ChangesHandler = deps.ChangesHandler
   	}
   	s.ChangesService = deps.ChangesService
   ```

   `ChangesService` is copied unconditionally because U30a reads it to decide
   whether tracked apply is offered; a nil value is the "unavailable" signal.

3. **`routes.go`** — inside the authenticated group, next to the other
   feature registrations (~line 187, after the GitOps block):

   ```go
   			// Tracked-change routes — only registered if the changes handler is available
   			if s.ChangesHandler != nil {
   				s.registerChangesRoutes(ar)
   			}
   ```

   and, next to `registerGitOpsRoutes`:

   ```go
   func (s *Server) registerChangesRoutes(ar chi.Router) {
   	h := s.ChangesHandler
   	rl := s.YAMLRateLimiter
   	if rl == nil {
   		rl = s.RateLimiter
   	}
   	ar.Route("/changes", func(cr chi.Router) {
   		// Share the YAML bucket (30 req/min): receipt polling follows an apply
   		// and must not exhaust the 5 req/min auth bucket.
   		cr.Use(middleware.RateLimit(rl))

   		cr.Get("/", h.HandleList)
   		cr.Post("/ownership", h.HandleResolveOwnership)
   		cr.Get("/{id}", h.HandleGet)
   		cr.Get("/{id}/verification", h.HandleVerification)
   	})
   }
   ```

   `resources.ValidateURLParams` is deliberately **not** applied: `{id}` is a
   UUID, not a Kubernetes name, and the handler validates it with `uuid.Parse`.
   Auth, CSRF, and `ClusterContext` are inherited from the group at
   `routes.go:108-113` and must not be re-declared.

4. **`main.go`** — after the ESO bulk block (~line 867), before `server.New`:

   ```go
   	// Tracked changes (Release E). Receipts require PostgreSQL; when dbPool is
   	// nil the /changes routes are not registered and tracked applies return
   	// 503, while the untracked /yaml/apply path is completely unaffected.
   	//
   	// ReconcileOrphans is single-replica safe only — it marks every receipt
   	// left non-terminal by a crash. The Helm chart pins one replica. It never
   	// re-applies anything.
   	var changesService *changes.Service
   	var changesHandler *changes.Handler
   	if dbPool != nil {
   		receiptStore := appstore.NewChangeReceiptStore(dbPool)
   		if n, err := receiptStore.ReconcileOrphans(ctx); err != nil {
   			logger.Warn("changes: receipt reconcile failed", "error", err)
   		} else if n > 0 {
   			logger.Info("changes: reconciled interrupted receipts from prior run", "count", n)
   		}
   		changesService = changes.NewService(receiptStore, clusterRouter, clusterStore,
   			accessChecker, auditLogger, logger)
   		changesHandler = changes.NewHandler(changesService, gitopsHandler, clusterRouter,
   			accessChecker, logger)

   		// Hourly retention sweep (default 30 days, Q1).
   		go func() {
   			sweep := func(c context.Context) {
   				n, err := receiptStore.Cleanup(c, cfg.Changes.ReceiptRetentionDays)
   				if err != nil {
   					logger.Warn("changes: receipt retention sweep failed", "error", err)
   					return
   				}
   				if n > 0 {
   					logger.Info("changes: receipts pruned", "count", n)
   				}
   			}
   			ticker := time.NewTicker(time.Hour)
   			defer ticker.Stop()
   			recoverutil.Tick(ctx, logger, "changes receipt retention", sweep)
   			for {
   				select {
   				case <-ctx.Done():
   					return
   				case <-ticker.C:
   					recoverutil.Tick(ctx, logger, "changes receipt retention", sweep)
   				}
   			}
   		}()
   	}
   ```

   and in the `server.Deps` literal (`main.go:878-922`), next to `GitOpsHandler`:

   ```go
   		ChangesHandler:         changesHandler,
   		ChangesService:         changesService,
   ```

   The retention loop uses `recoverutil.Tick` per
   `docs/solutions/backend-resilience-conventions.md`; it owns no `WaitGroup` and
   no counted channel, so there is nothing that must live outside the wrapped
   closure. Note in the PR body that the neighbouring ESO/audit retention loops
   at `main.go:231-247`, `:801-814`, and `:839-866` still use raw `go func` with a
   hand-rolled or absent recover — **do not copy them**, and do not fix them in
   this PR (out of scope, separate cleanup).

### Test functions → scenarios

Wiring is exercised through the existing server-level tests in
`backend/internal/server/`; no new test file is added (keeping this unit at four
files). Add cases to the nearest existing route test:

| Test | Scenario |
|---|---|
| `TestRoutes_ChangesRegisteredWhenHandlerPresent` | route table contains all four paths |
| `TestRoutes_ChangesAbsentWhenHandlerNil` | unavailable DB: no `/changes` route exists at all |
| `TestRoutes_ChangesRequiresAuthAndCSRF` | `POST /changes/ownership` without `X-Requested-With` → 403 |
| `TestRoutes_ChangesRateLimitedWithYAMLBucket` | shares the 30 req/min bucket |
| `TestRoutes_YAMLRoutesUnchanged` | legacy-client compatibility: the `/yaml` route set is byte-identical |

### Verification

VC-BE, VC-ROUTE. Manual: boot with `KUBECENTER_DATABASE_URL` unset and confirm
`GET /api/v1/changes` 404s (route absent) while `POST /api/v1/yaml/apply` still
works; boot with a DB, kill the process mid-apply, restart, and confirm the
reconcile log line and the receipt's `unknown` state.

### Exit criteria — done means

- [ ] `/changes` registered only when the handler is non-nil.
- [ ] Retention loop uses `recoverutil.Tick`.
- [ ] `ReconcileOrphans` runs once at boot and logs its count.
- [ ] `KUBECENTER_CHANGES_RECEIPTRETENTIONDAYS` maps to `Config.Changes.ReceiptRetentionDays`.
- [ ] `/yaml` routes untouched.
- [ ] VC-BE, VC-ROUTE green.

---

## U30a. Integrate tracked apply into the existing endpoint (backend)

**Branch:** `feat/release-e-u30a-tracked-yaml-apply`
**PR title:** `feat(yaml): opt-in tracked apply with an additive response block`
**Covers:** R4, R19, R20, AE7. **Depends on:** U29b; U9 for remote support.

### Files (4)

1. `backend/internal/yaml/applier.go` — edit (additive observer entry point; `ApplyDocuments` signature unchanged)
2. `backend/internal/yaml/handler.go` — edit (opt-in tracked branch in `HandleApply`)
3. `backend/internal/yaml/tracked_apply_test.go` — **new**
4. `backend/internal/server/server.go` — edit (one field on the `yamlpkg.Handler` literal)

`server.go` appears here rather than in U29b because the `Changes` field must
exist on `yamlpkg.Handler` before it can be assigned, and that struct lives in
`handler.go`. See the sequencing note below.

### Steps

1. **`applier.go`** — additive only:

   ```go
   // ApplyObservation carries everything the caller needs to record one
   // document's outcome, including the resolved mapping and applied object that
   // ApplyResult deliberately does not expose on the wire.
   type ApplyObservation struct {
       Result  ApplyResult
       Mapping *meta.RESTMapping          // nil when GVK resolution failed
       Applied *unstructured.Unstructured // nil when the apply failed
   }

   // ApplyObserver is invoked after each document, in document order, before the
   // next is attempted. Returning an error STOPS the loop: the remaining
   // documents are reported as failed with a "not applied" error rather than
   // omitted, so summary.total still equals len(docs) and legacy clients that
   // compute success from summary.failed cannot be misled.
   type ApplyObserver func(ctx context.Context, obs ApplyObservation) error

   // ApplyDocumentsObserved is ApplyDocuments plus a per-document observer.
   func ApplyDocumentsObserved(ctx context.Context, dynClient dynamic.Interface,
       mapper meta.RESTMapper, docs []*unstructured.Unstructured, force bool,
       logger *slog.Logger, obs ApplyObserver) *ApplyResponse

   // ApplyDocuments is unchanged for every existing caller.
   func ApplyDocuments(ctx context.Context, dynClient dynamic.Interface,
       mapper meta.RESTMapper, docs []*unstructured.Unstructured, force bool,
       logger *slog.Logger) *ApplyResponse {
       return ApplyDocumentsObserved(ctx, dynClient, mapper, docs, force, logger, nil)
   }
   ```

   `applyOne` is refactored only to return `ApplyObservation` instead of
   `ApplyResult` — a mechanical change with no behavioural difference. The
   GVK-retry loop, the `default` namespace fallback (applier.go:132), the
   force/conflict message strings, and the created/configured/unchanged decision
   are untouched. The abort path appends, for each remaining document:

   ```go
   ApplyResult{Index: j, Kind: d.GetKind(), Name: d.GetName(), Namespace: d.GetNamespace(),
       Action: "failed",
       Error:  "not applied: change recording failed; re-preview and retry"}
   ```

   and the `Summary.Failed` counter increments for each, so `Summary.Total`
   still equals `len(docs)`.

   Add `Tracking *ApplyTrackingWire \`json:"tracking,omitempty"\`` to
   `ApplyResponse`, where `ApplyTrackingWire` is a plain struct declared in
   `applier.go` (no import of `changes`, avoiding a cycle; `handler.go` maps
   `changes.ApplyTracking` onto it).

2. **`handler.go`** — inside `HandleApply`, after the existing
   `RouterFor`/`IsLocal` block and before `ApplyDocuments`:

   ```go
   	// Opt-in tracked execution. Absent parameter => byte-identical legacy path.
   	trackedID := r.URL.Query().Get("trackedOperationId")
   	if trackedID == "" {
   		resp := ApplyDocuments(r.Context(), dynClient, mapper, docs, force, h.Logger)
   		h.auditApplyResults(r, user, clusterID, resp.Results, "")
   		httputil.WriteData(w, resp)
   		return
   	}
   ```

   The tracked branch parses the id (`uuid.Parse` + `Version() == 4`, else 400
   `"invalid trackedOperationId"`), 503s when `h.Changes == nil ||
   !h.Changes.Available()`, then calls
   `h.Changes.TrackedApply(r.Context(), req, applyFn)` where `applyFn` closes over
   `ApplyDocumentsObserved`. The existing audit loop is factored into
   `auditApplyResults(r, user, clusterID, results, operationID)` — same `Entry`
   fields, same one-entry-per-result cardinality, with `Detail` becoming
   `result.Action` untracked and `result.Action + " op=" + operationID` tracked.

   `Handler` gains one field:

   ```go
   	// Changes is nil when no database is configured; tracked apply then 503s
   	// and the untracked path is unaffected.
   	Changes *changes.Service
   ```

   `?repairOf=<uuid>` is accepted and forwarded; it is recorded as a link only
   and grants no authority.

   **Secret handling is unchanged.** `HandleApply` still has no Secret branch —
   this PR does not add one, and does not remove the 422s on diff and export.
   What it adds is the guarantee that the tracked path stores a digest and object
   references only, never the body.

3. **`server.go`** — one line in the existing literal (`server.go:218-224`):

   ```go
   		s.YAMLHandler = &yamlpkg.Handler{
   			K8sClient:     deps.K8sClient,
   			ClusterRouter: deps.ClusterRouter,
   			AuditLogger:   deps.AuditLogger,
   			Logger:        deps.Logger,
   			ClusterID:     deps.Config.ClusterID,
   			Changes:       deps.ChangesService,
   		}
   ```

### Test functions → scenarios

`tracked_apply_test.go` uses `dynamicfake.NewSimpleDynamicClient` plus a
`meta.NewDefaultRESTMapper` — note that `backend/internal/yaml` has **no existing
apply tests at all** (`yaml_test.go` covers only security, parsing, and export),
so this file introduces the fake-client harness for the package.

| Test | Scenario |
|---|---|
| `TestApplyDocuments_UntrackedResponseHasOnlyResultsAndSummary` | legacy-client compatibility: marshal and assert the top-level key set is exactly `{"results","summary"}` — the guard on `omitempty` and the pointer type |
| `TestApplyDocuments_UntrackedBehaviourByteIdentical` | golden JSON for created/configured/unchanged/failed |
| `TestHandleApply_NoTrackedParam_DoesNotTouchChangesService` | opt-in |
| `TestHandleApply_TrackedAddsOnlyTrackingKey` | additive-response proof |
| `TestHandleApply_TrackedTwoSuccessOneFailure` | **AE7**: `summary` and the receipt agree; `tracking.state == "partial"` |
| `TestHandleApply_DroppedResponseRetrySameOperationID_NoDuplicateApply` | dropped response + client retry: the fake dynamic client counts `Patch` calls and asserts they do not increase |
| `TestHandleApply_ForceConflictStillReportsConflictMessage` | force conflict semantics preserved |
| `TestHandleApply_SecretManifestStoresNoContent` | secret redaction: apply a `kind: Secret`, then assert the recorded receipt contains no substring of the submitted `data` value |
| `TestHandleApply_TargetClusterMismatch_Aborts` | target mismatch: stored `cluster_id` differs from the request's `X-Cluster-ID` → 409, no apply |
| `TestHandleApply_ObserverFailure_RemainingDocsReportedFailed` | `notAttempted` accounting; `summary.total == len(docs)` |
| `TestHandleApply_ChangesUnavailable_Returns503AndAppliesNothing` | unavailable DB |
| `TestHandleApply_InvalidOperationID_Returns400` | input validation |
| `TestHandleApply_AuditCardinalityUnchanged` | one audit entry per result, tracked or not |
| `TestHandleApply_AuditDetailCarriesOperationID` | traceability |
| `TestHandleApply_RemoteClusterStill501UntilU9` | records the U9 dependency as an executable expectation |
| `TestApplyDocumentsObserved_ObserverSeesMappingAndAppliedObject` | the observer contract |
| `TestApplyDocumentsObserved_NilObserverEqualsApplyDocuments` | delegation |

### Verification

VC-BE, VC-ROUTE. Manual smoke against the homelab per `CLAUDE.md`: one untracked
apply from the existing mobile build (proves the legacy envelope), one tracked
apply from `curl` with a UUID, and a repeat of the same `curl` proving
`replayed: true` with no second mutation.

### Exit criteria — done means

- [ ] `ApplyDocuments` keeps its exact signature and behaviour.
- [ ] Untracked responses serialize to exactly `{"results","summary"}` (test-enforced).
- [ ] `summary.total == len(docs)` on every path, including the observer-abort path.
- [ ] A repeated operation id never produces a second `Patch` (test-enforced against the fake client).
- [ ] No manifest bytes reach the store (test-enforced).
- [ ] Audit entry cardinality and field set unchanged.
- [ ] VC-BE, VC-ROUTE green; homelab smoke recorded in the PR body.


---

## U30b. Client contract for tracked apply (frontend types)

**Branch:** `feat/release-e-u30b-change-types`
**PR title:** `feat(frontend): additive tracking types on the shared apply contract`
**Covers:** R4, R19. **Depends on:** U30a.

### Files (3)

1. `frontend/lib/yaml-apply.ts` — edit (additive optional field + optional hook option)
2. `frontend/lib/change-types.ts` — **new** (receipt, ownership, verification types)
3. `frontend/lib/yaml-apply_test.ts` — **new**

**Master-plan correction.** U30 listed only `frontend/lib/change-types.ts` as
new. The apply response types are not there — they are in
`frontend/lib/yaml-apply.ts`, consumed by `islands/YamlApplyPage.tsx`,
`islands/SecretStoreFromTemplateEditor.tsx`, and
`lib/secretstore-template-nav_test.ts`. Both files are needed:
`yaml-apply.ts` for the additive apply field, `change-types.ts` for the receipt
and ownership shapes that U31 renders.

### Steps

1. **`change-types.ts`** — mirrors the Go JSON tags one-for-one, following the
   `frontend/lib/limits-types.ts` convention (flat, `export interface` /
   `export type`, one-line doc comment per declaration naming the Go type):
   `OwnershipController`, `OwnershipConfidence`, `OwnershipEvidenceKind`,
   `OwnershipEvidence`, `ObjectRef`, `OwnedByApp`, `OwnershipResult`,
   `ReceiptState`, `VerificationState`, `CheckStatus`, `CheckResult`,
   `ReceiptObjectView`, `ChangeReceiptView`, `ApplyTracking`,
   `VerificationResponse`.

   Unlike `ApplyResult.action` (a bare `string`), these are **string-literal
   unions**, so a renamed backend state is a compile error rather than a silent
   `undefined` branch:

   ```ts
   /** ReceiptState mirrors store.ReceiptState. */
   export type ReceiptState =
     | "previewed" | "applying" | "applied" | "partial" | "failed" | "unknown";

   /** VerificationState mirrors store.VerificationState. */
   export type VerificationState =
     | "pending" | "verifying" | "verified" | "inconclusive" | "verification_failed";

   /** OwnershipConfidence mirrors gitops.OwnershipConfidence. */
   export type OwnershipConfidence =
     | "confirmed" | "conflicting" | "unknown" | "forbidden" | "unavailable";
   ```

2. **`yaml-apply.ts`** — two additive edits, no removals, no signature changes to
   any existing export:

   ```ts
   import type { ApplyTracking } from "./change-types.ts";

   export interface ApplyResponse {
     results: ApplyResult[];
     summary: { total: number; created: number; configured: number; unchanged: number; failed: number };
     /**
      * Present only when the request opted in with `?trackedOperationId=`.
      * Absent for every legacy caller, so the untracked contract is unchanged.
      */
     tracking?: ApplyTracking;
   }

   export interface UseYamlApplyOptions {
     forceConflicts?: Signal<boolean>;
     onApplySuccess?: (res: ApplyResponse) => void;
     /**
      * When supplied, handleApply mints a fresh UUIDv4 per attempt via
      * crypto.randomUUID() and appends `?trackedOperationId=`. A retry of the
      * SAME attempt reuses the stored id; a new attempt mints a new one.
      */
     tracked?: Signal<boolean>;
   }
   ```

   `handleApply` gains an internal `lastOperationId` signal so that a retry after
   a network error reuses the id (the whole point of D4) while a fresh user-
   initiated apply mints a new one. Query-string assembly moves from the current
   inline ternary to a small `buildApplyQuery(force, operationId)` helper so it
   is unit-testable. `UseYamlApplyReturn` gains `lastOperationId: Signal<string | null>`
   — additive, and the two existing consumers destructure by name, so neither
   breaks.

   The `tracking` block is deliberately **not** consumed by the shared hook's
   result rendering; `ApplyResults` in `YamlApplyPage.tsx` continues to render
   `results`/`summary` only. `SecretStoreFromTemplateEditor.tsx` passes no
   `tracked` signal and is therefore behaviourally untouched.

3. **`yaml-apply_test.ts`** — `Deno.test` + `jsr:@std/assert@1`, relative
   imports, per `frontend/lib/score-color_test.ts`.

### Test functions → scenarios

| Test | Scenario |
|---|---|
| `buildApplyQuery: no force, no tracking → ""` | legacy-client compatibility |
| `buildApplyQuery: force only → "?force=true"` | unchanged legacy behaviour |
| `buildApplyQuery: tracking only → "?trackedOperationId=…"` | opt-in |
| `buildApplyQuery: both → "?force=true&trackedOperationId=…"` | ordering stability |
| `ApplyResponse without tracking parses and renders` | additive proof at the type level |
| `ApplyResponse with unknown extra keys still satisfies the interface` | forward compatibility |
| `retry reuses lastOperationId; new attempt mints a new one` | idempotency at the client |
| `tracked signal absent leaves the request identical to today` | the SecretStore editor is unaffected |

### Verification

VC-FE.

### Exit criteria — done means

- [ ] No existing export in `yaml-apply.ts` changed shape.
- [ ] `SecretStoreFromTemplateEditor.tsx` and `secretstore-template-nav_test.ts` compile untouched.
- [ ] Every backend state enum has a matching TS literal union.
- [ ] VC-FE green.

---

## U31. Ownership preview and change receipts (UI)

**Branch:** `feat/release-e-u31-change-receipt-ui`
**PR title:** `feat(ui): pre-apply ownership warnings and durable change receipts`
**Covers:** R18–R20, AE7. **Depends on:** U30b.

### Files (5)

1. `frontend/islands/YamlApplyPage.tsx` — edit
2. `frontend/islands/ChangeReceipt.tsx` — **new**
3. `frontend/routes/changes/[id].tsx` — **new**
4. `frontend/lib/change-api.ts` — **new** (typed `/v1/changes` client)
5. `e2e/tests/change-receipts.spec.ts` — **new**

**Agent Directive 1 (Step 0).** `YamlApplyPage.tsx` is 361 LOC, over the 300-LOC
threshold, so the rule nominally applies. It has already been checked: all eight
imports are used, `deno lint` reports zero diagnostics, there are no debug logs,
no unused exports, and no dead props. **The Step-0 cleanup commit would be
empty**; state that in the PR body and proceed directly, rather than budgeting a
phase for it. (One pre-existing inconsistency is noted but explicitly *not*
fixed here: the file styles with inline `style={{...}}` objects rather than
Tailwind utilities, contrary to `CLAUDE.md`. Converting it is a separate
refactor with its own review boundary.)

### Steps

1. **`change-api.ts`** — thin typed wrappers over `apiGet`/`apiPost`, no state:

   ```ts
   export function resolveOwnership(clusterId: string, objects: ObjectRef[]): Promise<OwnershipResult[]>
   export function getReceipt(id: string, signal?: AbortSignal): Promise<ChangeReceiptView>
   export function getVerification(id: string, signal?: AbortSignal): Promise<VerificationResponse>
   export function listReceipts(page: number, pageSize: number): Promise<{ items: ChangeReceiptView[]; total: number }>
   ```

   `getReceipt` and `getVerification` take an `AbortSignal` and use
   `apiGet(path, signal)` — the only wrapper in `api.ts` that accepts one. Errors
   are read through `ApiError.reason` / `errorExtra(err, key)`, never by digging
   into `body.error`, per that module's own instruction.

2. **`YamlApplyPage.tsx`** — three additions:
   - **Before apply:** after a successful validate, POST the parsed object refs
     to `/v1/changes/ownership` and render a per-object ownership strip. Copy is
     written from the `confidence` value, never inferred:
     `confirmed` → "Managed by Argo CD application `argo:ns:name`. Applying here
     changes the live object; the controller may revert it on its next sync.";
     `conflicting` → "Two controllers claim this object."; `unknown` +
     `hints-only` → "This object carries GitOps labels, but no controller's
     inventory confirms it. Ownership is unknown."; `forbidden` → "You cannot
     list applications in that namespace, so ownership could not be determined.";
     `unavailable` → "Argo CD is not installed / did not respond."
     `writableGitSource` is `false`, so **no "open a PR" affordance is rendered
     anywhere** — the Git-write path is Q4-gated.
   - **Tracking toggle:** a "Keep a record of this change" checkbox bound to a
     `tracked` signal, passed into `useYamlApply`. Default on when the backend
     advertises the capability (`/v1/changes` reachable), off and disabled with an
     explanatory line when it 404s or 503s.
   - **After apply:** when `result.value?.tracking` is present, render a link to
     `/changes/{operationId}` plus an inline state chip. When
     `tracking.state === "unknown"`, the panel says so explicitly and does **not**
     offer a one-click retry.

3. **`ChangeReceipt.tsx`** — the durable surface, driven by `change-api.ts`:
   - Header: target cluster, `clusterGeneration` (with a "cluster registration
     changed since this apply" note when `targetGenerationChanged`), content
     digest, timestamps, `repairOf` link.
   - **Two independent badges** — execution state and verification state — never
     merged. `applied` + `pending` renders as "Applied · Verification pending",
     and an `inconclusive` verification is rendered in a neutral tone with its
     `reason` spelled out ("This kind has no supported readiness postcondition"),
     never as a green check.
   - Per-object table from `objects[]`. Redacted rows render as
     "Object #3 — you no longer have access to this resource" with no other
     detail. `redactedObjects > 0` shows a count line.
   - Verification polls `getVerification` every 5s while
     `state === "verifying"`, honouring `retryAfterSeconds`, cancelling via
     `AbortSignal` on unmount and stopping at the first terminal state.
   - **Repair:** "Retry failed objects" is enabled only when the receipt is
     `partial` or `failed` *and* `containsSecret` is false. It navigates to
     `/tools/yaml-apply` and asks the user to supply current content; it never
     fetches stored content, because none exists. When `containsSecret` is true
     the button is replaced by "The original content is not stored. Re-create the
     Secret manifest to retry."

4. **`routes/changes/[id].tsx`** — the exact repo idiom
   (`frontend/routes/gitops/applications/[id].tsx`):

   ```tsx
   import { define } from "@/utils.ts";
   import ChangeReceipt from "@/islands/ChangeReceipt.tsx";

   export default define.page(function ChangeReceiptPage(ctx) {
     const id = decodeURIComponent(ctx.params.id);
     return <ChangeReceipt id={id} />;
   });
   ```

5. **`e2e/tests/change-receipts.spec.ts`** — `import { test, expect } from
   "../fixtures/base.ts"`, role/label selectors only (**no `data-testid`** — the
   repo has none), `try/finally` cleanup via `deleteResource` from
   `../helpers.ts`, and unique names via `e2eName("configmap")`.

### Test functions → scenarios

Frontend behaviour that is pure is covered by the U30b unit tests; U31's
scenarios are E2E:

| Spec | Scenario |
|---|---|
| `applies three documents, two succeed one fails, and the receipt survives reload` | **AE7**, R19 |
| `reload of /changes/{id} shows the identical partial result` | durability |
| `repair opens a fresh preview and never prefills from the receipt` | R20, D8 |
| `an Argo-owned object shows a controller warning before apply` | R18 |
| `ambiguous ownership renders a distinct explanation from unknown ownership` | R18 |
| `unknown execution outcome is explained and offers no one-click retry` | R19 |
| `inconclusive verification renders neutrally, not as success` | R20 |
| `applied plus pending verification renders two separate badges` | R20 |
| `a second user cannot open another user's receipt (404)` | cross-user access |
| `a Secret-bearing receipt offers no content reuse and shows no error text` | secret redaction |
| `no Git PR affordance is rendered for a confirmed Argo object` | Q4 gate |
| `receipt page degrades to an explicit unavailable state when /changes 503s` | unavailable DB |

The two-user case reuses the `auth.setup.ts` pattern to mint a second
`storageState`; per the master plan's Verification Contract, "UI tests need at
least two identities".

### Verification

VC-FE, VC-E2E. Manual homelab smoke of the ownership strip against a real Argo
application, per `CLAUDE.md`'s "smoke test against homelab when backend/frontend
changes are in scope".

### Exit criteria — done means

- [ ] Execution and verification are two badges, never one.
- [ ] `inconclusive` and `unknown` each have their own copy and neither is styled as success.
- [ ] No affordance anywhere offers a Git write.
- [ ] Repair always requires fresh user content; nothing is prefilled from the server.
- [ ] Redacted rows show only an index.
- [ ] Step-0 cleanup verified empty and recorded in the PR body.
- [ ] VC-FE, VC-E2E green.

---

## Cross-unit sequencing and conflict notes

**Merge order (each PR rebases on the previous):**

```
U26 ─┐
     ├─> U28 ─> U29a ─> U29b ─> U30a ─> U30b ─> U31
U27 ─┘
```

U26 and U27 are independent and may run in parallel; everything after U28 is a
straight line because each step touches a file the previous one created.

### Shared-file conflicts

| File | Units | Constraint |
|---|---|---|
| `.github/workflows/fuzz.yml` | **U26** (`FuzzOwnershipEvidence`), **U29a** (`FuzzReceiptRedaction`) | Both append one matrix line. U26 lands first; U29a rebases. Trivial conflict, but do not let both PRs sit open with stale bases — the `-list` guard will fail CI on a dropped row. |
| `backend/internal/server/server.go` | **U29b** (2 struct fields + copy block), **U30a** (1 field on the `yamlpkg.Handler` literal) | U29b must land first: it introduces `Deps.ChangesService`, which U30a assigns. Both edits are in different regions of the file (`:43-137` and `:218-224`), so the textual conflict is nil, but the *semantic* dependency is strict. |
| `backend/internal/server/routes.go` | **U29b** only | No other Release E unit touches it. **Cross-release hazard:** Release C's U8 also adds a registration line in the same `if s.XHandler != nil` block (`routes.go:183-226`) and Release A's U2 adds `/preferences`. Whichever lands second rebases; the block is a list of independent `if` statements, so conflicts are mechanical. |
| `backend/cmd/kubecenter/main.go` | **U29b** only | Same cross-release hazard with Release A's U3 and Release D's U23, all of which append to the same `server.Deps` literal at `:878-922`. |
| `backend/internal/yaml/handler.go` + `applier.go` | **U30a**, and **Release C's U9** | **Ordering constraint: U9 lands before U30a.** U9 replaces the local `h.K8sClient.RESTMapper()` with a per-target mapper and deletes the four `if !pair.IsLocal { 501 }` blocks across validate/apply/diff/export. U30a inserts a branch in `HandleApply` *after* that gate and passes `mapper` into `ApplyDocumentsObserved`. If U30a lands first, U9 must re-thread the mapper through the tracked branch as well as the untracked one — doable but it makes U9 a larger, riskier diff in a security-sensitive file. If U9 slips past U30a, U30a ships with `TestHandleApply_RemoteClusterStill501UntilU9` asserting the 501, and U9 flips that test. Either way the dependency is explicit and executable, never implicit. |
| `frontend/lib/yaml-apply.ts` | **U30b**, and (indirectly) **Release C's U11** | U11 surfaces capability state in the apply UI. Both add optional fields; `UseYamlApplyOptions` is the shared struct. Coordinate so only one of them changes `UseYamlApplyReturn`'s field set at a time. |
| `frontend/lib/change-types.ts` | **U30b** creates it; **U31** consumes it | The master plan marks it "new" in both U30 and U31. It is created exactly once, in U30b. U31 only imports from it. |
| `frontend/islands/YamlApplyPage.tsx` | **U31**, and **Release C's U11** | Both edit the same island. Sequence them; do not run both in parallel. U11's capability banner and U31's ownership strip occupy the same region above the editor and will conflict textually. |
| `scripts/check-cluster-routing.sh` | **U29a** only | Release C's U9 does not add a package, so no conflict. |
| `backend/internal/store/migrations/` | **U27** only | `000021` is reserved for Release E. `000018`–`000020` and `000022` belong to other tracks; never renumber. |

### Files Release E does **not** touch

- `frontend/lib/action-handlers.ts` and `mobile/lib/api/resource_actions.dart`.
  YAML apply is not in the action map (`ActionId` is
  `scale|restart|delete|suspend|trigger`), and the file's own header documents the
  precedent for keeping poll-shaped domain writes out of it. The web/Dart
  isomorphism invariant in `CLAUDE.md` is therefore unaffected by this release.
- `frontend/routes/api/[...path].ts`. The idempotency key is a query parameter
  precisely so the `FORWARD_HEADERS` allowlist stays untouched.
- Anything under `mobile/`. Mobile continues to call `/yaml/apply` without
  `trackedOperationId` and receives today's exact response. Native receipt
  parity is a separate milestone.

---

## Interface contract with Release D U20

Release E consumes exactly this from `backend/internal/diagnostics`. Both plans
must agree on it verbatim, including JSON tags — Release E persists these values
in `change_receipts.verification`, so a tag change after the fact is a data
migration.

```go
package diagnostics

// CheckStatus is the normalized outcome of one check. The value Release E
// depends on most is CheckInconclusive: today's Result.Status has no such
// value, so a permission denial or an unsupported kind is indistinguishable
// from a pass (see rules.go:47-52, 200-203).
type CheckStatus string

const (
	CheckPass         CheckStatus = "pass"
	CheckWarn         CheckStatus = "warn"
	CheckFail         CheckStatus = "fail"
	CheckInconclusive CheckStatus = "inconclusive"
)

// SourceRef identifies the object a check observed. Release E populates
// Resource (plural) because it needs it for SelfSubjectAccessReview at read
// time, and UID because a recreated same-name object must not inherit evidence.
type SourceRef struct {
	ClusterID string `json:"clusterId"`
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	UID       string `json:"uid,omitempty"`
}

// CheckResult is the reusable contract shared by incident evidence (Release D)
// and change verification (Release E).
type CheckResult struct {
	CheckID    string            `json:"checkId"`   // stable, e.g. "workload.rollout-complete"
	Status     CheckStatus       `json:"status"`
	Severity   Severity          `json:"severity"`
	Reason     string            `json:"reason"`    // machine code, e.g. "kind_not_supported"
	Message    string            `json:"message"`
	Detail     string            `json:"detail,omitempty"`
	Source     SourceRef         `json:"source"`
	ObservedAt time.Time         `json:"observedAt"`
	Evidence   map[string]string `json:"evidence,omitempty"` // never raw Secret values
}

// CheckRollout evaluates the rollout postcondition for one live object.
// MUST return CheckInconclusive with Reason "kind_not_supported" for any kind
// outside {Deployment, StatefulSet, DaemonSet}; MUST NOT return CheckPass for a
// kind it does not understand.
func CheckRollout(obj *unstructured.Unstructured, src SourceRef, now time.Time) CheckResult
```

**Three properties Release E requires and Release D must not weaken:**

1. **`CheckInconclusive` exists and is distinct from `CheckPass`.** Verification
   correctness depends on it entirely.
2. **`CheckRollout` takes a live `*unstructured.Unstructured` the caller
   fetched**, not a `topology.ResourceLister` or an informer handle. Today's
   `Resolve`/`RunDiagnostics` path reads local informers under the *service
   account* (`topology/informer_lister.go:27-29`), which is both
   local-cluster-only and non-impersonated. Release E fetches through
   `ClusterRouter.RouterFor` under the caller's identity, so keeping the check
   function pure over an object is what makes remote verification possible once
   Release C's U9 lands, and what keeps it honest about RBAC.
3. **`Reason` is a stable machine code**, not prose. Release E's UI branches on
   it and Release E's tests assert it.

**Fallback if U20 slips.** U28 declares `CheckStatus`, `SourceRef`,
`CheckResult`, and `CheckRollout` locally in
`backend/internal/changes/verifier.go` with these exact names, fields, and JSON
tags, behind the comment quoted in U28 step 4. Release E does **not** block on
Release D. When U20 lands, the swap is three type aliases plus deleting the local
`CheckRollout` — no wire change, no migration, because the persisted JSON shape
was fixed by this contract on day one. The only rule is that whichever plan lands
second adopts the tags above rather than inventing its own; if Release D needs a
different shape, it must change this section *before* U27 ships, because after
that the tags are in the database.

Release E does not consume `diagnostics.Result`, `RunDiagnostics`,
`DiagnosticTarget`, or the unexported rule registry, and does not require U20 to
change any of them. The existing diagnostics HTTP envelope
(`{target, results, blastRadius}`) is untouched by this release.


---

## Fuzzing summary (CLAUDE.md "fuzz the parse seams")

| Unit | Target | Package | New untrusted input | Oracles |
|---|---|---|---|---|
| U26 | `FuzzOwnershipEvidence` | `./internal/gitops/` | Flux inventory entry ids (`namespace_name_group_kind`) from CRD status, Argo `status.resources[]` unstructured maps, and the `argocd.argoproj.io/tracking-id` annotation — the last is fully attacker-controlled, since anyone who can write an object can write its annotations | **A** no panic; **C** hint-only evidence can never yield `confirmed`, a non-empty `Apps`, or `WritableGitSource: true` |
| U29a | `FuzzReceiptRedaction` | `./internal/changes/` | Stored receipt object rows rendered to an arbitrary caller | **D** a redacted row leaks no name/namespace/kind/uid/error substring; **C** a Secret-bearing receipt emits `errorClass` and never `error` |

Both rows are added to `.github/workflows/fuzz.yml` with the standard `-list`
drift guard. No other unit introduces a new parser over untrusted input:
`trackedOperationId` and `repairOf` go through `uuid.Parse` plus a version check,
the content digest is produced (never parsed), and pagination is integer
offset/limit clamped by `ReceiptQueryParams.Normalize()` — there is no cursor to
forge because this repo has no cursor pagination and Release E does not invent
one.

Each fuzz PR must record its **teeth check** in the PR body: delete or invert the
production guard, confirm the seeded corpus fails, revert. A corpus that passes
against deliberately broken code is not a test.

---

## Deferred appendix

### Git patch and pull-request workflow — **Q4-gated, not planned here**

Release E deliberately stops at read-only ownership evidence, which is exactly
step 1 of the master plan's "Git Patch and Pull Request Workflow" milestone
("ship U26–U31 first. Identify source application and show what is known and
unknown"). Nothing in this plan creates a branch, a commit, a patch, or a PR, and
nothing treats an existing credential as write-capable. Concretely:

- `OwnershipResult.WritableGitSource` is hardcoded `false` on every path, and
  U31 renders no Git-write affordance for any confidence value.
- `AppSource` (repoURL, path, targetRevision) is returned **only** for a
  confirmed application the caller is already authorized to list, and is
  presented as provenance, not as an editable target.
- The `gitprovider.CommitCache` GitHub token wired at `main.go:580-590` stays
  read-only. Release E adds no code path that could use it to write.

Reopening this requires Q4 to be answered — which provider, which repository,
which rendering format — plus, per the master plan, an explicit
repository→manifest mapping (rendered Helm/Kustomize output is not a reliable
source-file mapping), a pinned base revision, separately configured
bounded-scope write credentials, and branch-race handling. Likely owners remain
`backend/internal/gitprovider/change_proposal.go`,
`backend/internal/changes/proposal.go`, and
`frontend/islands/GitChangeProposal.tsx`. None of those exist and none should be
created by this release.

### Automatic rollback — **explicitly excluded**

`applier.go` applies documents independently with server-side apply. There is no
transaction, no saved prior state, and no reverse operation. A `partial` receipt
means some objects changed and some did not; it does not mean anything was or can
be undone. Release E therefore stores no pre-apply snapshot and offers no
"revert" control. Two independent reasons reinforce this: the receipt stores no
manifest content (so there is nothing to revert *to*), and for a GitOps-owned
object the controller — not k8sCenter — owns convergence, so a k8sCenter revert
would itself be a live edit the controller may undo. Repair is always a new,
freshly previewed, freshly authorized apply (D8).

### Controller suspension — **explicitly excluded**

`SuspendArgoApp` / `ResumeArgoApp` already exist (`gitops/argocd.go:265,309`) and
remain reachable through the GitOps UI as deliberate, separately audited
operator actions. Release E does **not** call them, does not offer
"suspend before applying", and does not restore a sync policy after an apply. An
apply that silently disables a controller's automated sync would be an invisible
change to cluster-wide reconciliation behaviour with no receipt of its own.
Instead, U31 *warns* that an Argo-owned object may be reverted on the next sync
and leaves the decision to the operator.

### Offline mutation queue — **explicitly excluded**

No client-side queue, no retry-later buffer, no background replay. This is not an
omission but a direct consequence of D4: a durable intent must be created
server-side *before* any mutation, so an offline client has nowhere to record
intent and no way to distinguish "never sent" from "sent, response lost". The
correct offline behaviour is to fail visibly and let the user re-preview when
connectivity returns.

### Mobile parity — **separate milestone**

Mobile keeps calling `/yaml/apply` with no `trackedOperationId` and keeps
receiving today's exact envelope. Receipt reading, ownership warnings, and the
tracked toggle are a native milestone gated on `mobile/lib/api/` work, and must
respect the existing cluster-pinning invariants in `CLAUDE.md`.

---

## Risks and open items

| # | Risk | Impact | Mitigation / owner |
|---|---|---|---|
| R-1 | **Ownership resolution cost.** Confirming ownership requires per-application detail GETs; the cached list is service-account-wide and can hold hundreds of applications. | Slow previews, API-server load. | `maxDetailFetches = 25`, hint-first ordering, namespace affinity, reuse of the existing 30s singleflight cache. Exhaustion reports `search-bound-exhausted`, never a false `none`. Revisit with a per-application inventory cache only if measurement shows a problem. |
| R-2 | **Name-keyed ownership.** Neither Argo `status.resources[]` nor Flux inventory records a UID, so a deleted-and-recreated object inherits the ownership answer. | An R1 violation in the ownership surface specifically. | Reported honestly: `identityBasis: "group-kind-namespace-name"`, `uidConfirmed: false`, surfaced in the UI. Verification *does* compare UIDs, so the receipt's evidence path is UID-safe even though the ownership path cannot be. |
| R-3 | **`ReconcileOrphans` is single-replica-safe only.** A second replica would mark another replica's in-flight receipts as `unknown`. | False `unknown` states in a scaled deployment. | Identical to the existing `ESOBulkJobStore.CompleteOrphans` constraint; the Helm chart pins one replica. Documented in `NOTES.txt` and in the `main.go` comment. A leader election or an owner-instance column is the real fix and belongs with whichever release first scales the backend. |
| R-4 | **No PostgreSQL test harness.** The store's most important behaviours — duplicate-id insert, append-prefix-after-crash, reconcile split, migration round trip — cannot be automated today. | Regressions in the durability layer land unnoticed. | U27 covers everything pure (including a migration/Go-enum drift test that reads the embedded SQL) and lists the DB behaviours as named manual smoke steps in the PR body. **Open item:** a shared Postgres test harness is worth its own unit; it would benefit Releases A, D, E, and F equally and is currently nobody's. |
| R-5 | **`PostgresLogger` drops audit entries under load** (1000-entry non-blocking buffer, `postgres_logger.go`). A large tracked apply emits one entry per document. | Audit and receipt can disagree about a very large bundle. | Not introduced by this release, and receipts are written synchronously, so the *receipt* stays complete. Worth noting in the receipt UI that the audit log is best-effort. Fixing the audit buffer is out of scope. |
| R-6 | **U9 ordering.** If Release C's U9 lands after U30a, U9's diff grows to re-thread the per-target mapper through the tracked branch. | Larger, riskier diff in a security-sensitive file. | Stated as a hard ordering constraint above, plus `TestHandleApply_RemoteClusterStill501UntilU9` as an executable marker that U9 flips. |
| R-7 | **U20 tag drift.** Release D could ship `CheckResult` with different JSON tags after U27 has already persisted the current shape. | A data migration on `change_receipts.verification`. | The interface-contract section is the agreement; the deadline is "before U27 ships". Both plans must reference it. |
| R-8 | **`change_receipt_grants` ships with no write API.** | Dead schema if grant UX never lands. | Deliberate: the read path is grant-aware from day one so Q1's policy is implemented rather than retrofitted, and the table costs one `CREATE TABLE`. **Open item:** confirm grant creation lands with Release D's incident-sharing surface, or drop the table in a later migration. |
| R-9 | **A tracked apply that stops mid-bundle reports later documents as `failed`** even though they were never sent to the API server. | Slightly pessimistic reporting. | Chosen deliberately over truncating `results`, because `wizard_controller.dart` computes `allSucceeded => failed == 0` and truncation would make a legacy client claim success for a half-applied bundle. `tracking.notAttempted` gives new clients the precise distinction. |
| R-10 | **Verification is client-polled.** A user who closes the tab before the 120s window elapses leaves the receipt at `verifying`. | Receipts that never reach a verification verdict. | The next reader's first poll evaluates the window and freezes it as `inconclusive`/`window_expired`, so the state is self-correcting on read. A background verifier was rejected explicitly: it would require holding a user's impersonation credentials past their request. |
| R-11 | **The BFF proxy's 30s cap** also bounds the tracked apply request itself. | A very large bundle could exceed it. | Pre-existing for untracked applies too, and unchanged by this release. Tracked mode makes it *safer*, because a timed-out response now has a durable receipt the client can read instead of an unknowable outcome. |
| R-12 | **Content digest is over raw bytes**, so trivially reformatted YAML produces a different digest. | A retry after an editor reformat is treated as new content and 409s with `operation_id_reused`. | Correct and intended: different bytes are a different change and deserve a fresh preview. The error message says exactly that. |

### Open items requiring a decision before the affected unit starts

1. **U27 / R-4** — accept manual smoke for DB behaviours, or fund a Postgres test
   harness first? Recommendation: accept manual smoke for Release E, and raise
   the harness as its own cross-release unit.
2. **U27 / R-8** — keep `change_receipt_grants` in `000021`, or defer it to the
   migration that ships grant UX? Recommendation: keep it; the read path is
   already grant-aware and a second migration is more churn than one unused
   table.
3. **U28 / R-7** — confirm with the Release D plan owner that the `CheckResult`
   JSON tags above are final before U27 merges.
4. **U30a / R-6** — confirm Release C's U9 merge slot relative to U30a.

