---
title: "External Secrets Operator integration (#8)"
type: feat
status: active
date: 2026-04-29
origin: docs/brainstorms/2026-04-29-external-secrets-operator-requirements.md
---

# External Secrets Operator integration (#8)

## Overview

Feature-complete ESO integration under `/security/external-secrets/*`: observatory + per-provider wizards + chain topology graph + Notification Center alerting + four operational lenses (drift detection, sync diff history, bulk refresh, per-store rate / quota awareness). Phased delivery so each phase ships against a working baseline; no phase introduces a half-broken UX.

The integration mirrors and extends four existing pipelines: Phase 11A (cert-manager observatory: CRD discovery + RBAC + 30s cache), Phase 11B (cert-manager wizards: WizardInput interface + Monaco preview), Phase 13 (annotation-resolution chain: cert > issuer > clusterissuer > default), and Phase 12 (topology overlay: `?overlay=mesh` extension pattern).

## Requirements Trace

R-IDs reference [docs/brainstorms/2026-04-29-external-secrets-operator-requirements.md](../docs/brainstorms/2026-04-29-external-secrets-operator-requirements.md).

| Requirement | Owning phase | Owning unit(s) |
|---|---|---|
| R1, R2, R3, R4, R5 (observatory) | A | 1, 2, 3 |
| R6, R8, R21 (history) | C | 9, 10 |
| R20 (drift, tri-state) | C | 11 |
| R9, R10, R11 (chain topology) | I | 21, 22 |
| R12 (universal ExternalSecret wizard) | G | 17 |
| R13 (per-provider Store wizards × 12) | H | 18, 19, 20 |
| R14 (niche-provider YAML templates) | H | 20 |
| R15, R16 (failure + recovery alerts) | D | 13 |
| R17, R18, R19 (annotation chain) | D | 12 |
| R22, R23, R24 (bulk refresh) | E | 14, 15 |
| R25, R26 (per-store rate / cost) | F | 16 |
| R27, R28 (dashboard) | B | 7, 8 |
| R29 (Helm RBAC) | A | 6 |
| R30 (docs flip) | J | 23 |
| R31 (golang-migrate) | D, C, E | 12 (000010 source enum), 9 (000011 history), 15 (000012 bulk-refresh jobs) |

## Resolved Before Planning

- **Wizard path-discovery scope: Kubernetes provider only in v1.** Cross-provider path-discovery (Vault `LIST` on KV, AWS `ListSecrets`, GCP / Azure / 1Password listing) requires k8sCenter to authenticate against the source store, which contradicts the "k8sCenter never holds source-store credentials" scope boundary and creates an SSRF surface on the user-supplied `prefix=` parameter. v1 ships path-discovery only for the Kubernetes provider (genuinely k8s-RBAC-mediated — listing Secrets in the source namespace). All other 11 providers in R13 ship with a free-text path field with an inline hint ("this provider doesn't expose path discovery — enter the path manually"). Cross-provider path-discovery is deferred to a v2 with an explicit per-provider auth-model design.
- **Poller diff-key computation requires Helm `get/list secrets` grant.** R21 requires the persistent history to carry a key-level diff. The poller has no requesting-user context, so it must read synced Secret keys via the platform service account. Phase J's Helm chart adds `get/list` on `core/secrets` cluster-wide. This is a meaningful expansion of the platform SA's privilege; documented explicitly. Operators in stricter environments can remove the grant — the diff feature degrades to "outcome only, no key-set" but the timeline still functions.
- **ClusterSecretStore visibility uses permissive-read (matches requirements R3).** Cluster-scoped resources are RBAC-filtered via `CanAccessGroupResource` exactly like namespaced resources — a tenant operator with explicit `list clustersecretstores` grant can see them. No additional `auth.IsAdmin` gate. Operators wanting stricter tenancy layer it via Kubernetes RBAC.
- **PushSecret v1 scope.** Read-only observatory only. Listed in R1 with read-only tag; no wizard, no actions. Matches scope-boundary line in the requirements doc. Note: PushSecret spec is intentionally surfaced (selector + remoteRef paths visible to anyone with `list pushsecrets`); cluster admin controls who has that grant.
- **`thresholdConflict` semantics.** Reserved for warn-vs-crit ordering conflicts only (Phase 13 parity). ESO's stale/recovery annotations don't have an ordering relationship; invalid values silently fall through with no flag (R19).
- **`eso-stale-after-minutes` minimum floor: 5 minutes.** Annotation values below 5 are rejected (logged + fall through to next layer) to prevent self-DoS via aggressive stale thresholds against the 60s poller cadence.

## Scope Boundaries

- **No source-store credential handling.** ESO holds the source-store creds; we never authenticate against Vault / AWS / GCP / Azure on the user's behalf. Linkout to source UI is the only cross-system surface.
- **No ESO controller lifecycle management.** Install/upgrade/restart of ESO itself is out of scope; would be a separate "operator lifecycle" initiative.
- **No cross-cluster fleet view.** X-Cluster-ID per-cluster routing applies (the integration works correctly per cluster); a "all clusters at once" dashboard is a separate feature.
- **No PushSecret write/edit wizard in v1.** Read-only observatory is sufficient; PushSecret write surface deferred until usage signals demand.
- **No source-system audit log proxying.** Linkout only.
- **No live billing API integration.** Cost-tier estimates use static rate cards refreshed periodically; not connected to AWS/GCP/Azure billing APIs.
- **No `v1alpha1` ESO support.** v1 is GA in ESO v0.14+ (mid-2025); v1beta1 served-but-not-stored handled transparently by dynamic client. v1alpha1-only clusters are out of scope.

## Phases

| # | Phase | Units | Depends on |
|---|---|---|---|
| A | Backend observatory + Helm RBAC | 1–4, 6 | — |
| B | Frontend observatory | 7–8 | A |
| D | Alerting + annotation thresholds | 12–13 | A |
| C | Persistence: history table + drift detection | 9–11 | A, D |
| E | Bulk refresh actions | 14–15 | A |
| F | Per-store rate + cost-tier panel | 16 | A, B |
| G | Universal ExternalSecret wizard | 17 | A |
| H | Per-provider SecretStore wizards | 18–20 | G |
| I | Chain visualization (topology overlay) | 21–22 | A, B |
| J | Final docs + roadmap flip | 23 | A–I |

Phase ordering is **A → B → D → C → E → F → G → H → I → J**. Alerting (D) ships before persistent history (C) because notification dispatch reads from in-memory poller state, not the history table — operators get paging value sooner. Phase C then adds the timeline UI and drift detection on top.

Phase A delivers the foundation; Phase B is the first user-facing slice. The integration's value lands cumulatively across Phases A–F. Each phase ships its own PR.

**Unit 5 deleted.** Static metric-name constants live in `cost_tier.go` (Phase F) — no separate config endpoint needed.

---

## Phase A — Backend observatory + Helm RBAC

Mirrors Phase 11A 1:1. Creates the `internal/externalsecrets/` package, five normalized CRD types, the singleflight + 30s cache handler, RBAC filtering via `CanAccessGroupResource`, and the explicit ClusterRole grant. Status / list / detail endpoints land in this phase.

### Unit 1: Package skeleton — discovery + types + normalize

**Goal:** Stand up `internal/externalsecrets/` with CRD discovery (mirrors `certmanager/discovery.go`), normalized type definitions for the 5 CRDs, and pure normalize functions.

**Requirements:** R1, R2 (CRD discovery + auto-detect)

**Dependencies:** None.

**Files:**
- Create: `backend/internal/externalsecrets/discovery.go` — `Discoverer.Probe()` against `external-secrets.io/v1`, `IsAvailable()` gate, 5-min status cache.
- Create: `backend/internal/externalsecrets/types.go` — GVR constants, `Status` enum (`Synced` / `SyncFailed` / `Refreshing` / `Stale` / `Drifted` / `Unknown`), normalized types (`ExternalSecret`, `ClusterExternalSecret`, `SecretStore`, `ClusterSecretStore`, `PushSecret`), annotation key constants, `DriftStatus` tri-state enum (`InSync` / `Drifted` / `Unknown`), `ThresholdSource` enum + `Valid()` belt-and-suspenders guard.
- Create: `backend/internal/externalsecrets/normalize.go` — pure `normalize<Kind>(*unstructured.Unstructured)` functions. `DeriveStatus(es ExternalSecret, drift DriftStatus) Status` extracted from normalize so it can re-run after drift resolution.
- Create: `backend/internal/externalsecrets/normalize_test.go` — table-driven coverage for each kind: happy path, missing fields, unknown condition reasons, stale-vs-fresh detection, drift state propagation.

**Approach:**
- GVRs: `external-secrets.io/v1` for `externalsecrets`, `clusterexternalsecrets`, `secretstores`, `clustersecretstores`, `pushsecrets`. Group constant `GroupName = "external-secrets.io"` exported for RBAC + Helm.
- `ExternalSecret` carries `Namespace`, `Name`, `UID`, `RefreshInterval` (parsed `time.Duration`), `LastSyncTime`, `SyncedResourceVersion` (string, may be empty), `StoreRef` (`Name` + `Kind`: `SecretStore` | `ClusterSecretStore`), `TargetSecretName`, condition fields (`ReadyReason`, `ReadyMessage`), and `Status` (Status), `DriftStatus` (DriftStatus). Annotation-resolved fields (`StaleAfterMinutes int`, `AlertOnRecovery *bool`, `AlertOnLifecycle *bool`) populated by Phase D resolver — initialized to zero-values here. Per-key source attribution applies only to `StaleAfterMinutesSource` and `AlertOnRecoverySource` (integers / debugging-relevant booleans); `AlertOnLifecycle` is a simple boolean opt-in with no source attribution since the UI only needs "lifecycle alerts on/off" not "from which layer."
- `SecretStore` / `ClusterSecretStore` carry the same shape minus consumer-specific fields, plus `Provider` (string type — `"vault"`, `"aws"`, `"gcp"`, etc.) and `ProviderSpec` (`map[string]any` — never typed-imported, per L7.1).
- `Status` derivation:
  - `SyncFailed` when `Ready=False` (any reason).
  - `Refreshing` when `Ready=Unknown` (controller mid-reconcile).
  - `Stale` when last sync older than `StaleAfterMinutes` (resolved threshold; default 2× `RefreshInterval`, fallback 2h per R17).
  - `Drifted` when `DriftStatus == Drifted` AND base status is `Synced` (drift never overrides failure).
  - `Synced` otherwise.
  - `Unknown` only when status conditions are entirely absent (very young resource).
- `DriftStatus`: `Unknown` when `SyncedResourceVersion` is empty (provider doesn't populate it); `InSync` when `SyncedResourceVersion == liveResourceVersion`; `Drifted` otherwise. The `liveResourceVersion` lookup is a separate read (Phase A handler resolves it for detail; list view reports `Unknown` to avoid N+1 lookups — explicit decision documented in Unit 3).

**Patterns to follow:**
- `backend/internal/certmanager/discovery.go` — 5-minute cache, lazy probe.
- `backend/internal/certmanager/types.go` — annotation key consts, `ThresholdSource` + `Valid()` (PR #207, L3.5).
- `backend/internal/certmanager/normalize.go` — pure unstructured → typed.

**Test scenarios:**
- *Happy*: `ExternalSecret` with `Ready=True`, fresh `lastRefreshTime` → `Status == Synced`.
- *Failure*: `Ready=False`, reason `"AuthFailed"` → `Status == SyncFailed`.
- *Refreshing*: `Ready=Unknown` → `Status == Refreshing`.
- *Stale*: `Ready=True` but `lastRefreshTime` older than 2× refresh interval (with no annotation) → `Status == Stale`.
- *Drift unknown*: `SyncedResourceVersion == ""` → `DriftStatus == Unknown`, base status preserved.
- *Drift in-sync*: `SyncedResourceVersion == "12345"`, live RV `"12345"` → `DriftStatus == InSync`.
- *Drift detected*: `SyncedResourceVersion == "12345"`, live RV `"12346"`, base `Ready=True` → `Status == Drifted`.
- *Drift never overrides failure*: drift detected but `Ready=False` → `Status == SyncFailed` (failure wins).
- *Discovery missing CRDs*: `Probe()` against cluster without ESO CRDs → `IsAvailable() == false`, no error.

**Verification:** `go test ./internal/externalsecrets/...` passes.

---

### Unit 2: Handler — singleflight + 30s cache + RBAC + list/status endpoints

**Goal:** The canonical handler shape — concurrent CRD lists via `errgroup`, singleflight collapsing concurrent fetches, 30s cached response, per-namespace RBAC filtering. List + status endpoints expose the cached snapshot.

**Requirements:** R3 (RBAC), R4 (singleflight + cache), R5 (normalized status surfaces)

**Dependencies:** Unit 1.

**Files:**
- Create: `backend/internal/externalsecrets/handler.go` — `Handler` struct + `HandleStatus`, `HandleListExternalSecrets`, `HandleListClusterExternalSecrets`, `HandleListStores`, `HandleListClusterStores`, `HandleListPushSecrets`. Internal `fetchAll(ctx)` with five `g.Go` blocks. `getCached()` → `singleflight.Group.Do("all", fetchAll)`.
- Create: `backend/internal/externalsecrets/handler_test.go` — table-driven RBAC and cache hit/miss tests.

**Approach:**
- Cache TTL = 30s, matching Phase 11A. `cacheTTL = 30 * time.Second`. Generation counter for `InvalidateCache` (write actions in Phase E call this).
- Service-account fetch (cluster-wide), per-user RBAC filter at read time. Mirrors Phase 11A. Per-CRD-group RBAC: `CanAccessGroupResource(ctx, user, groups, "list", "external-secrets.io", "<resource>", namespace)`.
- Cluster-scoped resources (`ClusterExternalSecret`, `ClusterSecretStore`) filtered via `CanAccessGroupResource` with empty namespace — permissive-read (matches requirements R3). A tenant operator with explicit `list clustersecretstores` grant sees them; users without the grant get an empty list silently. No `auth.IsAdmin` gate. Namespaced items use `filterByRBAC[T]` generic helper copied verbatim from `internal/certmanager/handler.go`.
- 5 concurrent CRD lists: `dynClient.Resource(GVR).Namespace("").List(ctx, metav1.ListOptions{})` — five `g.Go` blocks under `errgroup.WithContext` with 10s timeout. Empty CRD (not installed) returns empty slice, never errors.
- `HandleStatus` endpoint: returns `{detected: bool, namespace: string, version: string, lastChecked: time}` from the discoverer cache; cheap, no fetch trigger.
- Empty-state pattern (L1.1): when `!Discoverer.IsAvailable()`, all list handlers return `[]` with HTTP 200, never 5xx.
- RBAC fail-closed (L2.1): `auth.IsAdmin` gate on cluster-wide queries when namespace == "".

**Patterns to follow:**
- `backend/internal/certmanager/handler.go:147–162` — `filterByRBAC[T namespacedResource]` generic.
- `backend/internal/certmanager/handler.go:107–118` — `canAccess()` thin wrapper around `CanAccessGroupResource`.
- `backend/internal/certmanager/handler.go:183–262` — `fetchAll` with `errgroup`.

**Test scenarios:**
- *Happy list*: user with `list externalsecrets` in `apps` ns → response includes `apps`-ns ExternalSecrets only.
- *Permissive read with grant*: user with explicit `list clustersecretstore` grant → sees ClusterSecretStore list.
- *Permissive read denied*: user without `list clustersecretstore` grant → empty ClusterSecretStore list, HTTP 200, not 403.
- *ESO not installed integration test*: fake k8s client with zero ESO CRDs registered → all 11 endpoints return 200 with `[]` / `{detected: false}`; verifies the full handler path through `errgroup`'s 5 g.Go blocks, not just the discoverer flag.
- *Cache hit*: two concurrent `HandleListExternalSecrets` requests → `fetchAll` invoked exactly once (singleflight).
- *Cache TTL*: third request after 31s → `fetchAll` re-invoked.
- *CRDs missing*: `IsAvailable() == false` → list endpoints return `[]`, status endpoint returns `{detected: false}`.
- *Per-CRD failure isolated*: ExternalSecret list errors but SecretStore list succeeds → response includes SecretStores; ExternalSecrets returns empty with logged error (g.Go doesn't fail-fast in our wrapper).

**Verification:** `go test ./internal/externalsecrets/...` passes; race detector clean (`go test -race`).

---

### Unit 3: Detail endpoints + drift resolution

**Goal:** Per-resource detail endpoints. Detail endpoint resolves `liveResourceVersion` for the synced k8s Secret to populate `DriftStatus`. Use impersonating client for detail (per L1.3 — Phase B mistake).

**Requirements:** R3, R5, R20

**Dependencies:** Units 1, 2.

**Files:**
- Modify: `backend/internal/externalsecrets/handler.go` — add `HandleGetExternalSecret`, `HandleGetClusterExternalSecret`, `HandleGetStore`, `HandleGetClusterStore`, `HandleGetPushSecret`.
- Modify: `backend/internal/externalsecrets/handler_test.go` — detail-endpoint cases.

**Approach:**
- Use `K8sClient.DynamicClientForUser(user.KubernetesUsername, user.KubernetesGroups)` — RBAC enforced by k8s API itself, not our checker. Phase 11A pattern.
- Detail endpoint additionally fetches the synced `Secret` (impersonated `coreV1Client.Secrets(ns).Get`) to read `liveResourceVersion`. Resolves `DriftStatus` per Unit 1 logic. List view does not — too expensive (N+1) — list reports `DriftStatus = Unknown` for live data and the detail page is the source of truth.
- Detail response includes the cached threshold-resolved fields (Phase D fills these). Phase A leaves them at zero so the response shape is final from day one.
- Drift status surfaced in response with explanatory hint ("provider does not expose syncedResourceVersion") for `Unknown` per the requirements doc.

**Test scenarios:**
- *Happy detail*: ExternalSecret detail with synced Secret RV match → `DriftStatus = InSync`.
- *Drift detected*: `syncedResourceVersion=A`, live `B` → `DriftStatus = Drifted`, `Status = Drifted`.
- *Drift unknown*: `syncedResourceVersion=""` (provider doesn't populate) → `DriftStatus = Unknown`.
- *Synced Secret deleted*: ExternalSecret status says Ready but referenced Secret missing → `DriftStatus = Unknown`, log line; detail returns 200 (not 404 — the ExternalSecret exists).
- *Impersonation forbidden*: user with `get externalsecret` but not `get secret` perm → drift returns `Unknown` (impersonated Get returns 403; we capture and degrade silently).

**Verification:** `go test ./internal/externalsecrets/...`.

---

### Unit 4: Routes wiring + ClusterContext middleware

**Goal:** Register the handler on the chi router under `/api/v1/externalsecrets/*`. Multi-cluster routing applies via existing X-Cluster-ID middleware.

**Requirements:** Wires R1-R5 to HTTP.

**Dependencies:** Units 2, 3.

**Files:**
- Modify: `backend/internal/server/routes.go` — register handler routes under `/externalsecrets`.
- Modify: `backend/internal/server/server.go` (or wherever handlers are wired) — instantiate `externalsecrets.Handler` with `K8sClient`, `Discoverer`, `AccessChecker`, `AuditLogger`, `NotifService`, `Logger`.

**Approach:**
- Routes:
  - `GET /externalsecrets/status`
  - `GET /externalsecrets/externalsecrets`
  - `GET /externalsecrets/externalsecrets/{ns}/{name}`
  - `GET /externalsecrets/clusterexternalsecrets`
  - `GET /externalsecrets/clusterexternalsecrets/{name}`
  - `GET /externalsecrets/stores`
  - `GET /externalsecrets/stores/{ns}/{name}`
  - `GET /externalsecrets/clusterstores`
  - `GET /externalsecrets/clusterstores/{name}`
  - `GET /externalsecrets/pushsecrets`
  - `GET /externalsecrets/pushsecrets/{ns}/{name}`
- Bulk-action and force-sync routes deferred to Phase E.

**Verification:** `make test-backend`; integration smoke against a cluster with ESO installed.

---

### Unit 5: ~~PrometheusRule discovery hook~~ — DELETED

Removed during plan review. The metric-name constants live in `cost_tier.go` (Unit 16); the frontend imports them as TS string constants directly, exactly as the Phase 12 mesh golden-signals panel does. No separate metadata endpoint needed.

---

### Unit 6: Helm ClusterRole grant + chart wiring

**Goal:** Explicit `list` / `watch` grants for `external-secrets.io` CRDs. No write verbs — bulk actions use impersonating client.

**Requirements:** R29

**Dependencies:** None (independent of code units).

**Files:**
- Modify: `helm/kubecenter/templates/clusterrole.yaml` — append a block after the existing Istio/Linkerd grants (lines 123–146 in current chart per L9 of repo research).

**Approach:**
- Phase A ships ONLY the ESO CRD list/watch grant:
  ```yaml
  - apiGroups: ["external-secrets.io"]
    resources:
      - externalsecrets
      - clusterexternalsecrets
      - secretstores
      - clustersecretstores
      - pushsecrets
    verbs: ["list", "watch"]
  ```
- The `core/secrets` `get/list` grant is **deferred to Phase C's Unit 10 PR** — that's the unit whose poller actually consumes it. Shipping the grant ahead of its consumer in Phase A would expand the platform SA's cluster-wide privilege without any code that uses it, and would contradict the existing architectural-boundary comment at `clusterrole.yaml:20` ("secrets are fetched on-demand via impersonated client, not cached in informer"). When Unit 10 lands, it adds:
  ```yaml
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
  ```
  with the same documented trade-off: a k8sCenter compromise yields cluster-wide synced-Secret read access. Operators in stricter environments can remove the block; the diff feature degrades to "outcome only, no key-set" but the timeline still functions, drift detection still works, and notifications still fire.
- Phase A's drift resolution path (`resolveDriftStatus` in `internal/externalsecrets/handler.go`) uses the impersonated client, so it works without the SA grant.
- The existing Helm wildcard catch-all (`apiGroups: ["*"]; verbs: ["list"]`) at `clusterrole.yaml` already authorizes `list` on every CRD; the explicit ESO block is defense-in-depth documentation, not net-new authorization. The `core/secrets` `get` grant IS net-new (the wildcard is `list`-only) — that's why it ships in the unit that consumes it.
- Get verb on ESO CRDs is implicit through the impersonating client for detail endpoints.

**Verification:**
- `make helm-lint` clean.
- `make helm-template` produces clusterrole containing the new block.

---

## Phase B — Frontend observatory

Routes, list views, detail views, dashboard. Theme tokens only (no hardcoded Tailwind colors). Mirrors Phase 11A frontend layout.

### Unit 7: Routes + list islands + nav

**Goal:** Six list / chain pages (ExternalSecrets, ClusterExternalSecrets, SecretStores, ClusterSecretStores, PushSecrets, Chain) + dashboard at index. New nav-rail domain section ("External Secrets") with its own SubNav, parallel to GitOps and Backup. TS types mirror backend.

**Requirements:** R2 (routes render empty state when CRDs missing), R5 (status surfaced), R9 (standalone Chain page), R28 (RBAC scoped — frontend reflects backend-filtered counts)

**Dependencies:** Phase A (endpoints exist).

**Files:**
- Create: `frontend/lib/eso-types.ts` — TS interfaces mirroring backend types: `Status`, `DriftStatus`, `ExternalSecret`, `ClusterExternalSecret`, `SecretStore`, `ClusterSecretStore`, `PushSecret`, `ESOStatus`. Add a Go-side `eso_types_hash_test.go` (in Phase A) that hashes the exported field set of each Go struct and pins the hash; failure forces a TS update — prevents silent Go-TS drift.
- Create: `frontend/lib/eso-api.ts` — typed client (`listExternalSecrets`, `getExternalSecret`, `listStores`, etc., `getStatus`).
- Create: `frontend/routes/external-secrets/index.tsx` — landing page (redirect to dashboard).
- Create: `frontend/routes/external-secrets/external-secrets.tsx`, `cluster-external-secrets.tsx`, `stores.tsx`, `cluster-stores.tsx`, `push-secrets.tsx`, `chain.tsx`.
- Create: `frontend/islands/ESOExternalSecretsList.tsx`, `ESOClusterExternalSecretsList.tsx`, `ESOStoresList.tsx`, `ESOClusterStoresList.tsx`, `ESOPushSecretsList.tsx`.
- Create: `frontend/islands/ESOChainPage.tsx` — standalone chain page (R9): namespace selector + topology overlay pre-toggled to `eso-chain`. Wraps `NamespaceTopology` with the chain-overlay default.
- Create: `frontend/components/eso/ESOStatusBadge.tsx` — themed status badge using CSS custom property tokens: `Synced` → `var(--success)`, `SyncFailed` → `var(--error)`, `Refreshing` → `var(--accent)`, `Stale` → `var(--warning)`, `Drifted` → `var(--accent-secondary)`, `Unknown` → `var(--muted)`. **Theme palette extension:** if `--warning` and `--accent-secondary` aren't already present in all 7 themes, this PR adds them — touches all theme files. Verify in `frontend/static/themes/*.css` before starting.
- Create: `frontend/components/eso/ESOSubNav.tsx` — tab strip with live counts pulled from the dashboard summary endpoint.
- Modify: `frontend/lib/constants.ts` — add new top-level domain section "External Secrets" to the nav rail (parallel to GitOps and Backup). **Do not extend Security SubNav** — it already has 11 entries; +5 ESO tabs would create a 16-tab strip with no grouping. ESO gets its own icon + SubNav.
- Modify: `frontend/components/Sidebar.tsx` (or equivalent nav-rail component) — wire the new "External Secrets" domain section.
- Modify: `frontend/islands/CommandPalette.tsx` — add quick actions for "External Secrets list", "Stores list", "Chain", "New ExternalSecret" (creation entries deferred until Phase G ships).

**Per-list empty-state copy (installed-but-empty case):**

| List | Copy | CTA |
|---|---|---|
| ExternalSecrets | "No ExternalSecrets in this namespace. Create one to start syncing secrets from a SecretStore." | "New ExternalSecret" (Phase G) |
| ClusterExternalSecrets | "No ClusterExternalSecrets visible. These sync the same Secret to multiple namespaces." | (no CTA — admin-managed) |
| SecretStores | "No SecretStores in this namespace. ExternalSecrets require a SecretStore to function." | "New SecretStore" (Phase H) |
| ClusterSecretStores | "No ClusterSecretStores visible. Your permissions may restrict visibility, or no ClusterSecretStores exist." | (no CTA — admin-managed) |
| PushSecrets | "No PushSecrets in this namespace. PushSecrets push Kubernetes Secrets back out to a source store (uncommon)." | (no CTA — read-only in v1) |

**Approach:**
- Theme tokens only: `var(--success)`, `var(--accent)`, `var(--muted)`, etc. No hardcoded `text-green-500`. (CLAUDE.md Phase 6C lesson.) Canonical reference for theme-token-compliant wizard sub-components: `frontend/components/wizard/IssuerForm.tsx` — match its variable usage.
- List filter inputs: namespace filter on `ESOExternalSecretsList`. **Debounce 300ms + AbortController + sequence guard from day one** (L7.8) — match `frontend/islands/CertificatesList.tsx` precedent for debounce-ms.
- Empty states: when `getStatus().detected === false`, render an "ESO not detected" tile with installation guidance + link to docs (R2 contract). Per-list installed-but-empty copy is in the table above.
- Permission-gated islands: hide bulk-refresh button when user lacks the `update externalsecret` cluster-wide perm (frontend uses `SelfSubjectRulesReview` cache).
- ESOStatusBadge handles all six values. Drift on the **list view** is represented by the status badge alone (`Drifted` value); no separate drift pill on list rows — the badge IS the drift signal. The detail page uses a separate `ESODriftIndicator` (Unit 11) that adds tri-state hint text.

**Patterns to follow:**
- `frontend/islands/CertificatesList.tsx`, `frontend/islands/IssuersList.tsx` — list shape + filter wiring.
- `frontend/components/ui/PolicyBadges.tsx` — multi-variant badge component pattern.
- `frontend/components/SubNav.tsx` — tab strip with live counts (precedent in Phase 11A).

**Test scenarios:**
- (No frontend component-test culture in this repo; follow Phase 11A precedent. Verification is `deno task check` + manual smoke.)

**Verification:**
- `cd frontend && deno task check` clean (REPO-WIDE per CLAUDE.md rule 4).
- Smoke against homelab: list pages render with cluster data, namespace filter works without race-condition jitter, empty state renders when ESO uninstalled.

---

### Unit 8: Detail islands + Dashboard

**Goal:** Per-CRD detail page (one component per kind, switching layout via prop). Dashboard at `/external-secrets/dashboard` (or `/external-secrets/`) anchored on a sync-health hero ring; aggregates store inventory, recent failures with affected-workload counts, top reasons.

**Requirements:** R5, R20 (drift surfaced on detail), R27, R28

**Dependencies:** Unit 7.

**Files:**
- Create: `frontend/routes/external-secrets/external-secrets/[ns]/[name].tsx` (and parallel routes for the other kinds).
- Create: `frontend/islands/ESOExternalSecretDetail.tsx` — top section: status badge, drift indicator, source store reference (linked), refresh interval, last-sync time. Tabs: Overview / YAML / Events / History (Phase C-populated; tab is present but empty until Phase C ships) / Chain (Phase I). Tab is rendered with "History will be populated once persistence ships" placeholder until Phase C; this prevents the detail island from needing a v2 reshape post-Phase-C.
- Create: `frontend/islands/ESOStoreDetail.tsx`, `ESOClusterStoreDetail.tsx`, `ESOPushSecretDetail.tsx`.
- Create: `frontend/routes/external-secrets/dashboard.tsx` (or alias index).
- Create: `frontend/islands/ESODashboard.tsx` — **hero**: sync-health ring `X of Y ExternalSecrets synced` (compliance-dashboard GaugeRing precedent). **Secondary cards row**: SyncFailed count, Stale count, Drifted count, Unknown count. **Tertiary**: store-by-provider donut, per-provider cost-tier estimate cards (Phase F-populated). **Failure table**: "Broken ExternalSecrets right now" — for each failed/stale ES, show the count of consuming workloads (Pods/Deployments/StatefulSets that mount the synced Secret) with drill-link to the Chain tab. This combined view serves the Success Criterion ("which workloads are affected in 30s"). Top-reasons grouping below.
- Create: `frontend/components/eso/ESODriftIndicator.tsx` — tri-state: Drifted (`var(--accent-secondary)` + "next sync will overwrite" hint + "Revert drift" button that triggers Unit 14's force-sync), InSync (no badge or muted check), Unknown (muted + "provider does not expose drift state" hint). Matches R20.

**Approach:**
- Detail page: use `WizardStepper` *no* — detail is a non-wizard read view. Mirror `CertificateDetail.tsx` shape: top metadata table, condition reasons, related-resources panel.
- Drift indicator inline next to the status badge on detail page; on list page it's a small pill.
- Dashboard: SVG donut for provider distribution; reuse the chart primitives from Phase 8B compliance dashboard if compatible, otherwise inline.
- Recent-failures table: 24h window, last 50 entries, links to detail page.
- All cards use theme tokens. Cost-tier card stub here (returns "—" placeholder); Phase F populates it.

**Patterns to follow:**
- `frontend/islands/CertificateDetail.tsx` — metadata table, source attribution display, exhaustive `switch` on enum values.
- `frontend/islands/ComplianceDashboard.tsx` — gauge ring + severity bars + per-namespace table.
- `frontend/islands/PolicyDashboard.tsx` — engine-status cards.

**Verification:**
- `deno task check` clean.
- Smoke: dashboard renders with health histogram, drift indicator surfaces correctly for synthetic Drifted / InSync / Unknown ESes.

---

## Phase C — Persistence: history table + drift detection

PostgreSQL migration for `eso_sync_history`. Poller persistence hook. Drift-aware history entries. Phase D's source-enum migration is moved earlier in the schedule (Phase D landing before C means Phase D ships its own migration first — see Unit 12).

### Unit 9: Migration 000011 — `eso_sync_history` table

**Goal:** Schema for the persistent sync timeline. UID-keyed (R8), three text[] columns for diff key-sets (R31), flat table with DELETE-WHERE retention.

**Requirements:** R6, R8, R21, R31

**Dependencies:** Phase D's migration 000010 (source enum extension) lands first per the new phase order.

**Files:**
- Create: `backend/internal/store/migrations/000011_create_eso_sync_history.up.sql`.
- Create: `backend/internal/store/migrations/000011_create_eso_sync_history.down.sql`.

**Approach:**
- Schema (flat table, matches R31's column shape):
  ```sql
  CREATE TABLE eso_sync_history (
      id BIGSERIAL PRIMARY KEY,
      cluster_id UUID NOT NULL,
      uid TEXT NOT NULL,                      -- ExternalSecret UID (R8)
      namespace TEXT NOT NULL,
      name TEXT NOT NULL,
      attempt_at TIMESTAMPTZ NOT NULL,
      outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure', 'partial')),
      reason TEXT,                            -- short ESO condition reason
      message TEXT,                           -- longer ESO condition message
      diff_keys_added TEXT[] NOT NULL DEFAULT '{}',     -- R31
      diff_keys_removed TEXT[] NOT NULL DEFAULT '{}',   -- R31
      diff_keys_changed TEXT[] NOT NULL DEFAULT '{}',   -- R31
      synced_resource_version TEXT            -- snapshot at attempt time (drift baseline)
  );

  CREATE INDEX idx_eso_sync_history_uid_attempt ON eso_sync_history (uid, attempt_at DESC);
  CREATE INDEX idx_eso_sync_history_cluster_failures ON eso_sync_history (cluster_id, attempt_at DESC) WHERE outcome != 'success';
  CREATE UNIQUE INDEX idx_eso_sync_history_dedup ON eso_sync_history (uid, attempt_at);

  COMMENT ON TABLE eso_sync_history IS
      'Per-ExternalSecret sync attempt history. UID-keyed (R8); 90-day retention via DELETE-WHERE. ' ||
      'Partition candidate: if steady-state row count exceeds 10M, migrate to monthly RANGE partitioning on attempt_at.';
  ```
- Volume justification: realistic insert rate is governed by ExternalSecret refresh interval (`ON CONFLICT DO NOTHING` keys on `(uid, attempt_at)` where `attempt_at = lastRefreshTime`). At 1000 ESes with 1h refresh × 90 days = ~2.16M steady-state rows. Compliance-snapshots and audit-log precedents both use flat tables with this volume class. Partitioning is deferred to a future operational migration if steady-state exceeds 10M rows.
- Three `text[]` columns (R31 specification) for diff key-sets — simpler queries than JSONB path expressions, type-checked, exact-match index-friendly.
- Unique index `(uid, attempt_at)` enables `INSERT ... ON CONFLICT DO NOTHING` for restart-safe idempotent inserts (L5.3).
- Down migration: `DROP TABLE eso_sync_history;`

**Patterns to follow:**
- `backend/internal/store/migrations/000005_create_compliance_snapshots.up.sql` — `cluster_id` typing, idempotent insert pattern.
- `backend/internal/store/migrations/000001_create_audit_logs.up.sql` — naming convention, flat-table + retention model.

**Test scenarios:**
- Migration applies clean from `000010` (Phase D's source-enum extension).
- Down migration removes the table cleanly.
- `INSERT ON CONFLICT DO NOTHING` against `(uid, attempt_at)`: second insert returns 0 rows affected.
- `text[]` round-trip: insert with `diff_keys_added = ARRAY['key1', 'key2']`; SELECT returns same array.

**Verification:**
- `make test-backend` includes migration round-trip.
- Manual: `migrate -path ./internal/store/migrations -database "$DATABASE_URL" up` clean against fresh PG.

---

### Unit 10: Sync-history persistence in poller

**Goal:** Each observatory cycle compares the current `(syncedResourceVersion, attempt_at)` against the latest persisted entry. New attempt → INSERT; same → skip. Diff keys computed against the prior synced data.

**Requirements:** R6, R8, R21

**Dependencies:** Unit 9, Phase A (poller stub).

**Files:**
- Create: `backend/internal/externalsecrets/poller.go` — 60s ticker, local cluster only (matches Phase 11A constraint). Calls `Handler.CachedExternalSecrets(ctx)` for the resolved-status snapshot, computes diff vs prior synced data, INSERTs new history rows, emits notifications (Phase D wiring).
- Create: `backend/internal/externalsecrets/poller_test.go` — table-driven attempt-comparison + dedupe tests.
- Create: `backend/internal/store/eso_history.go` — `Store.AppendESOHistory(ctx, entry)`, `Store.QueryESOHistory(ctx, uid, limit)`, `Store.RunESOHistoryRetention(ctx)`, `Store.EnsureESOHistoryPartitions(ctx)`.
- Modify: `backend/internal/server/server.go` — start the poller alongside the cert-manager poller; wire the retention goroutine.
- Modify: `helm/kubecenter/templates/clusterrole.yaml` — add the `core/secrets` `get/list` grant block here (deferred from Phase A's Unit 6 because the poller is its only consumer):
  ```yaml
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
  ```
  This is the meaningful privilege expansion called out in Unit 6 — Phase C is where it lands.

**Approach:**
- Diff computation: read the synced k8s Secret's `Data` keys via the platform service account (which gains cluster-wide `get/list secrets` in this unit's Helm grant addition). The poller has no requesting-user context and cannot impersonate. The grant is documented as a meaningful privilege expansion; operators in stricter environments can remove it and the diff feature degrades to "outcome only, no key-set" while the timeline still functions. Compare against the prior history entry's `diff_keys_*` baseline (we store the resolved key-set per attempt; the "baseline" is the prior attempt's resolved key-set). Emit `{added, removed, changed}` arrays of key names. **Values are never stored or logged.**
- `attempt_at` resolution: use `lastRefreshTime` from ESO status when available, else `now()`. Always at second granularity.
- INSERT pattern: `INSERT INTO eso_sync_history (...) VALUES (...) ON CONFLICT (uid, attempt_at) DO NOTHING` — idempotent under poller restart (L5.3).
- `cluster_id` provenance: read from the platform's local-cluster registry at poller startup; cached for the poller's lifetime. The poller is local-cluster-only (Phase 11A constraint); cluster_id never changes mid-process.
- Retention goroutine: 1h tick, `DELETE FROM eso_sync_history WHERE attempt_at < now() - INTERVAL '90 days'`. Notifications-service retention precedent.
- Drift snapshotting: each entry stores `synced_resource_version` as recorded during this attempt. Drift detection at read time compares the live Secret RV against the *latest* history entry's `synced_resource_version` (already in cache; one fewer impersonated Get).
- Dedupe-key shape (L4.1, L4.2): in-memory dedupe map for notifications keyed by `(cluster_id, uid, kind enum)` — `kind` is one of `synced` / `stale` / `failed` / `recovered`. Bucket-as-enum, not ms-diff. Failure and recovery have **separate dedupe keys** (L4.1 — explicitly required by R16). Cluster_id in the key prevents collision across multi-cluster routing.
- **Restart recovery for the prev-bucket map**: on startup, query `nc_notifications` for the most-recent `external_secrets`-source notification per `(resource_kind, resource_ns, resource_name)` to seed the prev-bucket map. Without this, a process restart between failure and recovery causes the recovery alert to be silently suppressed (cert-manager precedent has the same bug). Add `Store.RecentBySourceAndKind(ctx, source, withinHours int)` to the notifications store.

**Patterns to follow:**
- `backend/internal/certmanager/poller.go` — 60s ticker, dedupe-by-uid map, local-cluster-only constraint.
- L5.5 — retention via `DELETE WHERE` after each insert, not separate cron.

**Test scenarios:**
- *First sync*: ExternalSecret with no prior history, fresh `lastRefreshTime` → INSERT row with `outcome=success`, all three `diff_keys_*` empty arrays.
- *Repeat sync, no change*: same `lastRefreshTime` as last poll → INSERT skipped via `ON CONFLICT`.
- *New successful sync*: new `lastRefreshTime`, Secret data changed → INSERT row with `diff_keys_added`, `diff_keys_removed`, `diff_keys_changed` populated.
- *Failure transition*: `Ready=False` → INSERT row with `outcome=failure`, reason from condition, no diff (failure wins).
- *Partial outcome* (R6 definition): synthetic ESO condition message indicating per-key denial → INSERT with `outcome=partial`. (Requires inspecting `status.binding.name`'s key-set against the source path key-set; only emit `partial` when both sets are non-empty and differ.)
- *Restart safety*: poller restarts mid-window → next tick re-reads the same `(uid, attempt_at)`; ON CONFLICT swallows.
- *Restart recovery emission*: ES failed at T-5min, process restart at T-2min, ES recovers at T-1min → `Store.RecentBySourceAndKind` seeds prev-bucket=Failure → recovery event fires (without seeding, recovery would be silently suppressed).
- *Retention*: row at `now() - 91d` is purged on next retention tick.
- *Helm grant removed degradation*: platform SA without `get secrets` perm → diff_keys_* fields empty in all inserted rows; outcome/reason still populated; timeline still functional.
- *ES delete + recreate*: ES with name `apps/db-creds` deleted, recreated at same name → fresh UID → no history collision (UID-keyed storage).

**Verification:**
- `go test ./internal/externalsecrets/... -count=10` (L7.5 — flush map-iteration-order flakes).
- Manual: tail PG `eso_sync_history`, observe rows after a homelab annotated sync.

---

### Unit 11: Drift detection wiring on detail + dashboard tile

**Goal:** Detail-page drift indicator reflects tri-state `DriftStatus` and offers a "Revert drift" action. Dashboard surfaces drift count alongside other secondary metrics.

**Requirements:** R20, R27

**Dependencies:** Phase B (detail island), Unit 3 (drift resolved on detail), Unit 14 (force-sync — for the Revert button).

**Files:**
- Modify: `frontend/islands/ESOExternalSecretDetail.tsx` — render `ESODriftIndicator` in the metadata header. The "Revert drift" button is wired here (calls Unit 14's force-sync endpoint with confirmation).
- Modify: `frontend/islands/ESODashboard.tsx` — Drift count surfaces in the secondary cards row alongside SyncFailed / Stale / Unknown counts (per Unit 8).
- Modify: `backend/internal/externalsecrets/handler.go` — list response for ExternalSecrets includes a coarse drift hint (`lastObservedDriftStatus: DriftStatus` derived from cached snapshots only, not impersonated Get N+1).

**Approach:**
- List view drift hint is best-effort: when poller has run at least once, the cached ExternalSecret carries `LastObservedDriftStatus` (from the most recent persisted history entry). When no history yet, hint is `Unknown`. Detail page does the live-RV check; this is the source of truth.
- Drift staleness SLA (document for operators): a fresh `kubectl edit secret` takes up to ~90s to surface in the dashboard count (60s poller cycle + 30s handler cache). The detail page is always fresh (live RV check on every request). The dashboard claim is best-effort, the detail page is source of truth — make this explicit in the operator docs (Phase J).
- Drift requires `get secret` on the synced Secret. A user with `get externalsecret` but not `get secret` on the target sees `DriftStatus: Unknown` permanently. Document in Phase J.
- "Revert drift" button: triggers the same force-sync patch from Unit 14. Confirmation dialog: "This will overwrite the local Secret edit with the source store's value. Continue?"

**Verification:**
- `deno task check` clean.
- Smoke: induce drift via `kubectl edit secret`, see detail page flip from `InSync` → `Drifted` after the next poll cycle (~60s).

---

## Phase D — Alerting + annotation thresholds

Notification Center wiring (source enum, dedupe, recovery), per-resource annotation chain (`stale-after-minutes`, `alert-on-recovery`, `alert-on-lifecycle`). Lands BEFORE Phase C — operators get paging value before the persistent timeline ships.

### Unit 12: Notification source enum + migration 000010 + annotation resolver + dispatch metadata flag

**Goal:** Add `external_secrets` to the Go source enum, extend the migration's CHECK constraint (drifted from the Go enum per L4.3 — fix here while we're touching the file), wire annotation resolver, add `SuppressResourceFields` dispatch flag.

**Requirements:** R17, R18, R19, R31

**Dependencies:** Unit 1 (annotation key consts), Phase 13 precedent.

**Files:**
- Modify: `backend/internal/notifications/types.go` — add `SourceExternalSecrets Source = "external_secrets"`. Add `SuppressResourceFields bool` field to the `Notification` struct.
- Modify: `backend/internal/notifications/service.go` — `sendSlack` and `sendWebhook` honor `SuppressResourceFields`: when true, omit `ResourceNS`/`ResourceName` from the Slack block body and the webhook JSON payload. ESO events set this true by default — defeats the tenant-leakage path that the RBAC-generic title alone doesn't close.
- Create: `backend/internal/store/migrations/000010_extend_nc_source_enum.up.sql` — single-transaction `ALTER TABLE nc_notifications DROP CONSTRAINT nc_notifications_source_check; ALTER TABLE nc_notifications ADD CONSTRAINT nc_notifications_source_check CHECK (source IN ('alert','policy','gitops','diagnostic','scan','cluster','audit','velero','certmanager','limits','external_secrets'));`. golang-migrate wraps each migration in a transaction by default; the constraint is logically replaced atomically.
- Create: `backend/internal/store/migrations/000010_extend_nc_source_enum.down.sql` — restores the prior CHECK.
- Create: `backend/internal/externalsecrets/thresholds.go` — `ResolveESOThresholds(es ExternalSecret, storesByNSName map[string]SecretStore, clusterStoresByName map[string]ClusterSecretStore, logger)` returning resolved values + per-key sources. `ApplyThresholds(ess []ExternalSecret, stores []SecretStore, clusterStores []ClusterSecretStore, logger)` mutator builds the indexes once (O(1) lookup per ES).
- Create: `backend/internal/externalsecrets/thresholds_test.go` — table-driven chain tests.
- Modify: `backend/internal/externalsecrets/handler.go` — `fetchAll` runs `ApplyThresholds` after normalize. (Phase A's Unit 2 establishes `fetchAll`; Phase D's Unit 12 extends it. This is an explicit cross-phase modification, not a Phase A change retroactively patched.)
- Modify: `frontend/islands/NotificationRules.tsx` — group `NOTIF_SOURCES` by category (Infrastructure / Policy / Secrets / Operations) so the 11-entry source selector stays usable.

**Approach:**
- Resolver chain per key (resource > referenced store > referenced clusterstore > package default), each key independent. Mirrors Phase 13 exactly:
  ```
  resolveKey(esVal, esNamespace, ref, storesMap, clusterStoresMap, defaultValue, extract func(Store) *int) (int, ESOThresholdSource)
  ```
- `ESOThresholdSource` enum: `default` / `externalsecret` / `secretstore` / `clustersecretstore`. With `Valid()` belt-and-suspenders guard (L3.5).
- Two annotation keys (R17): `kubecenter.io/eso-stale-after-minutes`, `kubecenter.io/eso-alert-on-recovery`. One opt-in key (R18): `kubecenter.io/eso-alert-on-lifecycle`.
- Stale-after default per R17: `2 × refreshInterval`, fallback `2h` when refreshInterval is unset. **Minimum floor: 5 minutes** — values below 5 are rejected (logged + fall through to next layer) to prevent self-DoS via aggressive thresholds against the 60s poller cadence.
- Invalid-value handling (R19): log + silent fall-through; no `thresholdConflict` flag (no warn-vs-crit ordering exists for these annotations).
- Source attribution stored per key on `ExternalSecret`: `StaleAfterMinutesSource`, `AlertOnRecoverySource`. **`AlertOnLifecycle` does not get per-key source attribution** — the UI only needs "lifecycle alerts on/off" so source provenance has no actionable use.
- Index map shape: `storesByNSName` keyed by `<namespace>/<name>` (string), `clusterStoresByName` keyed by `<name>`. O(1) lookup per resolution. Matches `internal/certmanager/thresholds.go` index pattern.

**Patterns to follow:**
- `backend/internal/certmanager/thresholds.go` — `resolveThresholdKey` generic chain walker, `Valid()` guard, `sanitize<Source>` belt-and-suspenders.
- L3.1 — single source of truth, called from BOTH handler `fetchAll` AND poller fallback path.

**Test scenarios:**
- *Happy ES annotation only*: ES sets `eso-stale-after-minutes: "5"`, store has none → resolved (5, `externalsecret`).
- *Happy store annotation only*: ES no annotation, store sets `60` → (60, `secretstore`).
- *ClusterStore fallback*: ES references ClusterSecretStore with annotation → (..., `clustersecretstore`).
- *Default*: no annotations anywhere → (`2 × refreshInterval` or `120` if refresh unset, `default`).
- *Mixed*: ES sets stale-after, store sets alert-on-recovery → per-key sources differ.
- *Invalid value*: ES sets `eso-stale-after-minutes: "potato"` → falls through to store; no `thresholdConflict`.
- *Negative value*: `"-5"` → rejected, falls through.
- *Zero value*: `"0"` → rejected (matches Phase 13), falls through.
- *Below floor*: `"3"` → rejected (below 5-min floor), falls through with logged warning.
- *Missing referenced store*: ES references `nonexistent` store → falls through to clusterstore then default.
- *Source enum migration*: applying 000010 against a DB with extant non-listed sources (e.g., `certmanager` rows already present) does NOT fail — the new constraint must include all currently-used values.
- *SuppressResourceFields dispatch*: ESO `sync_failed` event with `SuppressResourceFields=true` → Slack body omits `*Resource:*` line; webhook JSON omits `resourceNamespace`/`resourceName`. Comparison: `certmanager` event still includes them (legacy behavior preserved).

**Verification:**
- `go test ./internal/externalsecrets/...`.
- Migration round-trip: `migrate up` then `down` clean against PG with rows in `nc_notifications` containing `certmanager` source.

---

### Unit 13: Notification dispatch — failure + recovery + lifecycle

**Goal:** Poller emits Notification Center events for status transitions. Failure / stale / recovery use distinct dedupe keys (L4.1, R16). Lifecycle is opt-in (R18). RBAC-generic message text (L4.4).

**Requirements:** R15, R16, R18

**Dependencies:** Units 10, 12.

**Files:**
- Modify: `backend/internal/externalsecrets/poller.go` — emit logic. Dedupe map keyed by `(uid, ESOKind)` where `ESOKind` is one of `sync_failed` / `stale` / `unhealthy` / `recovered_synced` / `recovered_unhealthy` / `created` / `deleted` / `first_synced`.
- Modify: `backend/internal/externalsecrets/poller_test.go` — failure-then-recovery sequence test (AE2 in requirements doc).

**Approach:**
- Event kinds (R15, R16, R18):
  - `externalsecret.sync_failed` (failure)
  - `externalsecret.stale` (stale)
  - `secretstore.unhealthy` (failure)
  - `clustersecretstore.unhealthy` (failure)
  - `externalsecret.recovered` (recovery — separate dedupe)
  - `secretstore.recovered`, `clustersecretstore.recovered`
  - `externalsecret.created`, `externalsecret.deleted`, `externalsecret.first_synced` (lifecycle, opt-in via annotation)
- Dedupe-key separation per L4.1: failure key `(uid, "sync_failed")`, recovery key `(uid, "recovered")` — recovery is NOT suppressed by a recently-cleared failure.
- Recovery emission: poller tracks "previous bucket". When `prev=Failure`, current=`Synced`, AND `AlertOnRecovery == true` (resolved per Unit 12) → emit recovery, then update prev. AE2 covers this.
- RBAC-generic title (L4.4): "ExternalSecret sync failed" not "ExternalSecret apps/db-creds sync failed". Namespace + name go in metadata (`resource_namespace`, `resource_name`), not the title body.
- Lifecycle events default OFF: emit only when resolved `AlertOnLifecycle == true`.
- DB-level dedupe via `Store.DedupExists(...)` 15-minute window already wires through `NotificationService` — no change needed.

**Test scenarios:**
- *AE1*: ES transitions `Synced` → `SyncFailed` → emit `sync_failed` once. Subsequent polls with same `Ready=False` → no re-fire (in-memory `(uid, "sync_failed")` dedupe).
- *AE2*: same ES `SyncFailed` → `Ready=True` → emit `recovered` once. Different dedupe key from `sync_failed` (the prior failure dedupe entry does NOT suppress recovery).
- *Reason change while still failing*: `Ready=False, reason=AuthFailed` → `Ready=False, reason=PathNotFound` → no re-fire (dedupe is by `(uid, kind)`, NOT reason — AE1 footnote).
- *Stale + alert-on-recovery=false*: ES annotated `eso-alert-on-recovery: "false"` → no recovery event.
- *Lifecycle off by default*: ES `created` event → no fire unless `eso-alert-on-lifecycle: "true"` resolved.
- *Bucket-enum dedupe correctness* (L4.2): bucket transition `Synced` → `Stale` → `Synced` emits one `stale` and one `recovered` — clean clear of dedupe entry.

**Verification:**
- `go test ./internal/externalsecrets/... -count=10`.
- Smoke: induce sync failure on a homelab ExternalSecret, observe Notification Center fires once; force recovery, observe recovery fires once.

---

## Phase E — Bulk refresh actions

Force-sync annotation patch via impersonating client. RBAC-scoped fan-out with per-resource outcome reporting and audit-log honesty (L8.1, L8.2).

### Unit 14: Force-sync action + impersonation + audit

**Goal:** Single-resource force-sync POST. Sets the ESO `force-sync` annotation via strategic-merge patch through the impersonating client. Audit-logs the action.

**Requirements:** R22, R24

**Dependencies:** Phase A.

**Files:**
- Modify: `backend/internal/externalsecrets/handler.go` — add `HandleForceSyncExternalSecret` for `POST /externalsecrets/externalsecrets/{ns}/{name}/force-sync`.
- Modify: `backend/internal/audit/logger.go` — add `ActionESOForceSync`, `ActionESOBulkRefresh`, `ActionESOBulkRefreshNamespace` constants.

**Approach:**
- Force-sync annotation per ESO contract: `force-sync: <RFC3339 timestamp>`. Strategic-merge patch (L8.5 — preserves operator-set annotations):
  ```go
  patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"force-sync":"%s"}}}`, time.Now().UTC().Format(time.RFC3339)))
  client.Resource(esGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
  ```
- In-flight detection (L8.4): pre-check `lastRefreshTime` within last 30s (rather than `Ready=Unknown`, which ESO doesn't reliably set during reconcile — `Ready=False/True` flips happen at end of reconcile). When in-flight, return 409 with reason `"already_refreshing"`. Bulk fan-out treats 409 as `skipped`, not `failed`. Document the heuristic limit: a sync that legitimately takes longer than 30s won't show as in-flight.
- Audit identity sourcing: `requestedBy` reads from `auth.UserFromContext(r.Context())` — the validated session identity. Never from a request-body field.
- Audit `Entry.Detail` carries `{requestedBy: user, target: {ns, name, uid}, result: "success" | "skipped:already_refreshing" | "failed:<reason>"}` JSON-encoded.
- CSRF: register handler under `ar.Group` at `routes.go:75` to inherit `middleware.CSRF`; never register POST routes outside the authenticated group.
- Cache invalidation post-patch: `Handler.InvalidateCache()` so the next list reflects the new annotation.

**Test scenarios:**
- *Happy*: user with `update externalsecret` perm → patch succeeds, audit row written with `Result=success`.
- *RBAC denied*: user without perm → impersonated patch returns 403, handler returns 403, audit row with `Result=denied`.
- *Already refreshing*: `Ready=Unknown` at request time → 409, audit row with `Result=skipped:already_refreshing`.
- *Patch preserves operator annotations* (L8.5): pre-existing `eso-stale-after-minutes` annotation survives the force-sync patch (verified in test by reading post-patch object).

**Verification:** `go test ./internal/externalsecrets/...`.

---

### Unit 15: Bulk refresh — async job model with scope-pinned execution

**Goal:** Background-job bulk refresh: client `POST` returns 202 with `jobId`; client polls `GET /externalsecrets/bulk-refresh-jobs/{jobId}` for progress + outcome. Scope is pinned at job-creation time (target UIDs), not re-resolved at execution. Two-step confirmation dialog renders the resolved scope before job creation. Audit log records both request scope and post-fan-out outcome.

**Requirements:** R22, R23, R24

**Dependencies:** Unit 14, Phase D's source-enum migration (for the bulk-refresh job's notification on completion if we wire one — optional).

**Files:**
- Create: `backend/internal/store/migrations/000012_create_eso_bulk_refresh_jobs.up.sql` (and `.down.sql`) — schema for the lightweight job table:
  ```sql
  CREATE TABLE eso_bulk_refresh_jobs (
      id UUID PRIMARY KEY,
      cluster_id UUID NOT NULL,
      requested_by TEXT NOT NULL,
      action TEXT NOT NULL CHECK (action IN ('refresh_store','refresh_cluster_store','refresh_namespace')),
      scope_target TEXT NOT NULL,       -- "<ns>/<name>" or namespace
      target_uids TEXT[] NOT NULL,      -- pinned at creation
      created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      completed_at TIMESTAMPTZ,
      succeeded TEXT[] NOT NULL DEFAULT '{}',
      failed JSONB NOT NULL DEFAULT '[]',     -- [{uid, reason}, ...]
      skipped JSONB NOT NULL DEFAULT '[]'     -- [{uid, reason}, ...]
  );
  CREATE INDEX idx_eso_bulk_refresh_jobs_cluster_created ON eso_bulk_refresh_jobs (cluster_id, created_at DESC);
  ```
- Modify: `backend/internal/externalsecrets/handler.go` — add `HandleBulkRefreshStore`, `HandleBulkRefreshClusterStore`, `HandleBulkRefreshNamespace` (each returns 202 + `{jobId}`); `HandleGetBulkRefreshJob` for polling; `HandleResolveBulkScope` for the pre-flight scope GET.
  - Endpoints (explicit per route — no `...` placeholders):
    - `GET  /externalsecrets/stores/{ns}/{name}/refresh-scope`
    - `GET  /externalsecrets/clusterstores/{name}/refresh-scope`
    - `GET  /externalsecrets/refresh-namespace/{ns}/refresh-scope`
    - `POST /externalsecrets/stores/{ns}/{name}/refresh-all`
    - `POST /externalsecrets/clusterstores/{name}/refresh-all`
    - `POST /externalsecrets/refresh-namespace/{ns}`
    - `GET  /externalsecrets/bulk-refresh-jobs/{jobId}`
- Create: `backend/internal/externalsecrets/bulk_worker.go` — background goroutine processes jobs from a buffered channel; on shutdown, marks in-flight jobs as `completed_at = now()` with current state (no orphaned IN-PROGRESS rows).
- Create: `frontend/islands/ESOBulkRefreshDialog.tsx` — the two-step confirmation dialog. Renders: (1) primary count line "47 ExternalSecrets across 6 namespaces"; (2) expandable per-namespace target list (truncated at 10 namespaces with "...M more" — full list available behind expand toggle); (3) RBAC-restriction notice when filtered scope < full scope ("Showing only resources you can refresh; 12 additional ExternalSecrets are out of your visibility"); (4) zero-scope empty state ("No ExternalSecrets to refresh — the scope is empty"); (5) loading state while the scope-resolve GET is in flight.
- Modify: `frontend/islands/ESOStoreDetail.tsx`, `ESOClusterStoreDetail.tsx` — "Refresh all dependent ExternalSecrets" button opens the dialog.
- Modify: `frontend/islands/ESOExternalSecretsList.tsx` — namespace bulk-refresh button.
- Create: `frontend/islands/ESOBulkRefreshProgress.tsx` — progress polling UI (poll `/bulk-refresh-jobs/{jobId}` every 2s; show running counts; final outcome rendered in-place when `completed_at` is non-null). Page navigation away does NOT cancel the job — operator can return to see results.

**Approach:**
- POST flow: (a) Re-resolve scope via the same logic as `HandleResolveBulkScope` (RBAC-filtered list → target UIDs); (b) request body MAY carry a `targetUIDs []string` array from the prior GET — when present, the resolved scope MUST equal the requested UIDs; mismatch → 409 Conflict with `{reason: "scope_changed", added: [...], removed: [...]}` so the dialog can re-confirm. (c) Insert job row with the pinned `target_uids`; (d) return 202 + jobId; (e) background worker patches each ES one at a time, updating `succeeded`/`failed`/`skipped` arrays incrementally; (f) writes `completed_at` and the audit log row when done.
- Inter-call delay: 200ms between patches. Rationale: avoids overwhelming the **ESO controller's reconcile queue**, which is bounded by ESO's own `--concurrent` flag (typically 5-10). The k8s API server has plenty of headroom (platform `QPS=50`, `Burst=100` per `internal/k8s/client.go:75-77`); the bottleneck is ESO's controller, not k8s.
- Concurrency limit: at most one bulk-refresh job in flight per `(cluster_id, scope_target)` — enforced by a `SELECT ... FOR UPDATE` against the jobs table. Concurrent requests for the same scope return 409 with the existing job's id.
- Maximum target count: 5000 per job. Above that, return 413 with "scope too large; use per-namespace refresh."
- Audit log honesty (L8.1): single row per job, written at completion time. `Entry.Detail` JSON carries `{jobId, action, scope, succeeded_count, failed: [...], skipped: [...]}`. Audit log read API renders this JSON inline; verify the existing audit-log viewer (`frontend/islands/AuditLogViewer.tsx`) handles a 100KB+ JSON payload without breaking the table layout — add a smoke test in Phase E.
- Multi-cluster: the job table is cluster-scoped via `cluster_id`. Background worker uses the local cluster's impersonating client only; remote cluster bulk refresh is out of scope for v1 (separate work — remote cluster's job dispatch model differs because there's no informer cache).

**Test scenarios:**
- *AE5 happy*: SecretStore with 47 dependent ESes across 6 namespaces; tenant operator can list ESes in 2 of those → GET scope returns 2-namespace target list; POST creates job with those UIDs; worker patches all of them; audit-log shows scoped result.
- *Mixed outcomes*: 5 targets — 3 succeed, 1 already-refreshing (skipped), 1 RBAC-denied (failed) → final job state `{succeeded: 3, failed: 1, skipped: 1}` with per-resource reasons.
- *Empty scope*: store with no dependents → GET returns empty scope; dialog shows zero-scope empty state; POST is disallowed at the UI layer.
- *Scope-changed at execution*: client GETs scope at T=0; new ES created at T+30s; POST submits old `targetUIDs` → 409 with `{added: [new-uid]}` so dialog re-confirms.
- *Concurrent same scope*: two POSTs for the same store within 1s → second returns 409 with the existing job's id.
- *Optimistic-lock conflict*: target ES updated between scope-pin and patch → recorded as `failed: optimistic_lock`.
- *Job survives navigation*: client POSTs, navigates away, returns 5 minutes later → polls jobId, sees completed result.
- *Job survives backend restart*: in-flight job at restart → on startup, `completed_at = now()` with current state (no orphaned in-progress).
- *Audit log render*: 1000-target job → audit row's Detail JSON renders without breaking the audit viewer's table.

**Verification:**
- `go test ./internal/externalsecrets/... -count=10`.
- Smoke: trigger bulk refresh on homelab store with synthetic dependent ESes; verify outcome matches reality, audit log honest.

---

## Phase F — Per-store rate + cost-tier panel

Wires the metrics-config endpoint (Unit 5) to the dashboard. Cost-tier estimate uses static rate cards. Degrades cleanly when Prometheus unavailable.

### Unit 16: Per-store rate panel + cost-tier card

**Goal:** SecretStore detail page surfaces request-rate from ESO Prometheus metrics. Dashboard surfaces aggregate per-provider request-rate + cost-tier estimate cards for paid-tier providers.

**Requirements:** R25, R26, R27

**Dependencies:** Unit 5 (metrics-config), Phase B (dashboard surface).

**Files:**
- Create: `backend/internal/externalsecrets/cost_tier.go` — Go map literal of rate-card data + metric name constants + `EstimateCost(provider, requestCount, window)` function. NO `rate_cards.json` file; the data lives in a typed Go map for compiler-checked keys, no JSON unmarshal path. Each provider entry includes `LastUpdated time.Time` so the API response carries the rate-card snapshot date.
- Modify: `backend/internal/externalsecrets/handler.go` — add `HandleGetStoreMetrics` returning rate and cost estimate for a single store (`GET /externalsecrets/stores/{ns}/{name}/metrics`). Internally calls Prometheus through `monitoring.QueryService` with the metric names from `cost_tier.go`. Per-store endpoint (not a direct frontend `monitoring/query` call) is justified because we enforce store-level RBAC: a user who can't see a store via `CanAccessGroupResource` doesn't get its metrics, even if they could compose the PromQL themselves.
- Create: `frontend/islands/ESOStoreMetricsPanel.tsx` — panel: "Requests in last 24h: X (rate Y/min)" + cost-tier estimate when paid-tier + caveat caption "Rates as of YYYY-MM-DD; not connected to live billing."
- Modify: `frontend/islands/ESOStoreDetail.tsx`, `ESOClusterStoreDetail.tsx` — embed the metrics panel.
- Modify: `frontend/islands/ESODashboard.tsx` — add per-provider cost cards row with the same caveat caption.

**Approach:**
- Rate-card data (Go map literal in `cost_tier.go`):
  ```go
  type providerRateCard struct {
      Operations  map[string]float64  // "get" → $/M requests, etc.
      Currency    string              // "USD"
      LastUpdated time.Time           // when cards were last hand-revised
  }
  var rateCards = map[string]providerRateCard{
      "aws-secrets-manager": {Operations: map[string]float64{"get": 0.05, "list": 0.05}, Currency: "USD", LastUpdated: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)},
      "aws-parameter-store-advanced": {Operations: map[string]float64{"get": 0.05}, Currency: "USD", LastUpdated: ...},
      "gcp-secret-manager": {...},
      "azure-key-vault": {...},
  }
  ```
- Metric name constants (also in `cost_tier.go`, replacing the deleted Unit 5):
  ```go
  const (
      MetricSyncCallsTotal = "externalsecret_sync_calls_total"
      MetricSyncCallsError = "externalsecret_sync_calls_error"
  )
  ```
- Coverage: AWS Secrets Manager, AWS Parameter Store (advanced tier), GCP Secret Manager, Azure Key Vault. Self-hosted (Vault, Kubernetes provider) skip cost estimate per R26.
- Prometheus query patterns:
  - Per-store request count: `sum(rate(externalsecret_sync_calls_total{store="<name>", namespace="<ns>"}[5m]))`
  - Per-store error rate: `sum(rate(externalsecret_sync_calls_error{store="<name>", namespace="<ns>"}[5m]))`
  - 24h window: `sum_over_time(...[24h])`
- Singleflight wrap (L1.4): the metric fetch and the per-store list fetch share a singleflight key so concurrent dashboard polls don't hammer Prometheus independently.
- Degradation: when Prometheus query fails or `monitoring.IsAvailable() == false`, response carries `{rate: nil, error: "rate metrics offline"}`. Frontend shows "rate metrics offline" placeholder. Never fabricates zero (per requirements doc R25).
- API response includes `lastUpdated` from the rate card so the frontend renders the staleness caption inline rather than via tooltip-only.

**Test scenarios:**
- *Happy paid-tier*: Vault store with metric data → response includes `{rate, costEstimate: nil}` (Vault is self-hosted — no cost). AWS store with same data → `{rate, costEstimate: $X.XX}`.
- *Prometheus offline*: Prom unreachable → `{rate: nil, error: "rate metrics offline"}` HTTP 200.
- *No metrics for store*: ESO never synced → `{rate: 0, costEstimate: 0}`.
- *Rate-card lookup*: provider `aws-secrets-manager` returns hit; provider `pulumi-esc` returns nil (not in cards); response `costEstimate: nil`, no error.

**Verification:**
- `go test ./internal/externalsecrets/...`.
- Smoke: dashboard cost cards render with real Prometheus data on homelab; degradation observable when Prometheus stopped.

---

## Phase G — Universal ExternalSecret wizard

Provider-agnostic ExternalSecret creation. Picks any visible Store/ClusterStore. Path-discovery type-ahead opt-in per provider (per Resolve Before Planning).

### Unit 17: ExternalSecret wizard backend + frontend

**Goal:** Generic `ExternalSecretInput` with `Validate()` and `ToYAML()`. Wizard island uses the existing `WizardStepper` shell. Path-discovery via the **Kubernetes provider only** in v1 (no Vault / AWS / GCP / Azure path-discovery — those would require k8sCenter to authenticate against the source store and break the "k8sCenter never holds source-store credentials" scope boundary).

**Requirements:** R12

**Dependencies:** Phase A (visible store list).

**Files:**
- Create: `backend/internal/wizard/externalsecret.go` — `ExternalSecretInput` struct + `Validate() []FieldError` + `ToYAML() (string, error)`.
- Create: `backend/internal/wizard/externalsecret_test.go` — table-driven validation tests.
- Create: `backend/internal/wizard/regex_parity_test.go` — pins Go DNS-label regex against TS regex constant (L7.3).
- Modify: `backend/internal/server/routes.go` — register `POST /wizards/externalsecret/preview`.
- Create: `backend/internal/externalsecrets/path_discovery.go` — `Handler.HandleListPaths` for `GET /externalsecrets/stores/{ns}/{name}/paths?prefix=<>`. **Kubernetes provider only**: lists Secrets in the source namespace via the impersonating client (k8s RBAC enforces visibility — no SSRF surface). All other providers return `{supported: false}` and the frontend renders a free-text path field.
- Create: `frontend/islands/ExternalSecretWizard.tsx` — single island, two steps (Configure / Review). `detailBasePath = "/external-secrets/external-secrets"` so the post-apply success screen links to the detail page correctly.
- Create: `frontend/components/wizard/ExternalSecretForm.tsx` — form fields with the path-discovery-aware picker (typeahead for kubernetes provider, free-text everywhere else).
- Create: `frontend/routes/external-secrets/external-secrets/new.tsx`.

**Approach:**
- `ExternalSecretInput` fields: `Namespace`, `Name`, `StoreRef {Name, Kind}`, `RefreshInterval`, `TargetSecretName`, `Data []DataItem` where `DataItem = {SecretKey, RemoteRef {Key, Property?, Version?}}`. Optional `DataFrom` for full-secret syncs.
- Validation: namespace + name DNS labels, target Secret name (RFC 1123), refresh-interval Go duration parse (`time.ParseDuration`), `Data` must have at least one item (or `DataFrom` set). Cross-language regex parity test pins Go regex to TS.
- `ToYAML()` builds via `map[string]any` (L7.1) — no typed ESO Go SDK import. `apiVersion: external-secrets.io/v1`.
- Path-discovery proxy: Kubernetes provider only. Endpoint `GET /externalsecrets/stores/{ns}/{name}/paths?prefix=<>` calls the impersonating client to list Secrets in the configured source namespace. Other providers: response carries `{supported: false}` and the frontend renders free-text. The "inline hint" for free-text providers is a single-line helper text below the input: *"This provider doesn't expose path discovery. Enter the remote key path manually (e.g. `secret/data/myapp/db`)."* Same component, same wording, applied uniformly.
- Path-discovery picker states (Kubernetes provider): loading (spinner inline), error ("Couldn't load paths — enter manually" + degrade to free-text), empty result ("No paths found in this namespace"), timeout (5s timeout → degrade to free-text with notice).
- Touched-flag pattern (L7.5) for path field on store change.
- ARIA from day one (L7.7): `htmlFor`/`id` pairing, `aria-invalid`, `aria-describedby`.
- No `useCallback` cargo-cult (L7.6).
- Debounce 300ms + AbortController on the path-discovery typeahead.

**Patterns to follow:**
- `backend/internal/wizard/certificate.go` — table-driven `Validate()` shape.
- `frontend/islands/CertificateWizard.tsx` — `WizardStepper` shell integration.
- `frontend/components/wizard/WizardReviewStep.tsx` — Monaco preview + apply.

**Test scenarios:**
- *Happy*: fully-formed input → preview returns valid YAML, apply succeeds.
- *Validation*: empty `Data` and empty `DataFrom` → 422 with `FieldError` "must specify at least one of data or dataFrom".
- *Bad refresh-interval*: `"1z"` → 422.
- *Cross-language regex parity*: `regex_parity_test.go` confirms Go vs TS DNS label regex match.
- *Path-discovery Kubernetes provider*: store with provider=kubernetes + prefix `apps/` → response lists Secrets in `apps` namespace.
- *Path-discovery any other provider*: any non-kubernetes store → `{supported: false}`, frontend renders free-text path input with the standard helper text.

**Verification:**
- `go test ./internal/wizard/... ./internal/externalsecrets/...`.
- `deno task check`.
- Smoke: create an ExternalSecret via wizard against a homelab Vault store.

---

## Phase H — Per-provider SecretStore wizards (12 providers)

Per-provider validation + YAML rendering. `StoreScope` discriminator (Store vs ClusterStore — one Go type, two routes — L7.4). Niche providers ship YAML templates only.

### Unit 18: SecretStore wizard scaffold + StoreScope discriminator

**Goal:** Generic `SecretStoreInput` with `Provider` field + `ProviderSpec map[string]any` driven by per-provider validators. Single Go type; `Scope: StoreScopeNamespaced | StoreScopeCluster` discriminator (mirrors `IssuerScope`).

**Requirements:** R13

**Dependencies:** Phase G (wizard pattern established).

**Files:**
- Create: `backend/internal/wizard/secretstore.go` — `SecretStoreInput` + `Scope` enum + per-provider dispatcher.
- Create: `backend/internal/wizard/secretstore_test.go` — table-driven tests across providers.
- Modify: `backend/internal/server/routes.go` — register `POST /wizards/secret-store/preview` (Scope=Namespaced default), `POST /wizards/cluster-secret-store/preview` (Scope=Cluster default).
- Create: `frontend/islands/SecretStoreWizard.tsx` — single island, prop-driven `scope` switches route + namespace handling. `detailBasePath = "/external-secrets/stores"` for Namespaced; `detailBasePath = "/external-secrets/cluster-stores"` for Cluster — passed via `scope` prop.
- Create: `frontend/routes/external-secrets/stores/new.tsx`, `cluster-stores/new.tsx`.
- Create: `frontend/components/wizard/secretstore/` — per-provider form sub-components (one file per provider). All sub-components reference `frontend/components/wizard/IssuerForm.tsx` as the canonical theme-token-compliant template.

**Approach:**
- Per-provider validator dispatcher: `provider == "vault"` → `validateVaultSpec(spec)`; `"aws"` → `validateAWSSpec(spec)`; etc. One file per provider in `backend/internal/wizard/secretstore_providers/` (or inline in `secretstore.go` until it becomes unwieldy).
- `ToYAML()` renders `kind: SecretStore` or `ClusterSecretStore` based on `Scope`. `apiVersion: external-secrets.io/v1`.
- `provider` selector in the wizard drives the form's secondary panel (Vault auth method picker, AWS region picker, etc.). Touched-flag (L7.5) on every dependent field — switching provider clears the spec slate cleanly.
- L7.2 culling pass: each of the 12 providers is reviewed against "does the wizard add value over a YAML template?" before building. Providers where the value is mostly mandatory-field reminders ship as YAML templates instead. Final list determined in Unit 19; this unit ships the scaffold.

**Patterns to follow:**
- `backend/internal/wizard/issuer.go` — `IssuerScope` discriminator pattern.
- L7.4 — single Go type, single frontend island, two routes.

**Verification:**
- `go test ./internal/wizard/... -count=10`.
- `deno task check`.

---

### Unit 19: Per-provider validators + form components

**Goal:** Implementation for the 12 provider wizards. Per-provider auth-method panels and validation. After culling per L7.2, the final wizard set is the providers where wizard adds genuine value over YAML.

**Sizing note:** With ~3 auth methods per provider average, the implementation surface is closer to **~36 form variants** across 12 providers. This unit must be split into per-provider sub-PRs (one PR per provider) to stay within the CLAUDE.md "5 files per phase" rule and to surface culling decisions per provider in the PR description rather than at end-of-phase smoke. Each sub-PR also documents whether that provider's wizard ships or whether it falls through to a YAML template (Unit 20). **R13's contract is delivered when each of the 12 listed providers has at least one of {wizard, YAML template} ship; the wizard-vs-template split is documented per-provider in the sub-PR.**

**Requirements:** R13

**Dependencies:** Unit 18.

**Files (per provider — anticipated):**
- `backend/internal/wizard/secretstore_providers/vault.go` (multiple auth methods)
- `backend/internal/wizard/secretstore_providers/aws.go`
- `backend/internal/wizard/secretstore_providers/azure.go`
- `backend/internal/wizard/secretstore_providers/gcp.go`
- ... (one per provider that survives culling)
- `backend/internal/wizard/secretstore_providers/<provider>_test.go` — table-driven per provider.
- `frontend/components/wizard/secretstore/<Provider>Form.tsx` per provider.

**Approach (provider-by-provider summary):**

| Provider | Auth methods | Path discovery | Notes |
|---|---|---|---|
| Vault | token, kubernetes, approle, jwt, cert | yes (`LIST` on KV) | Auth-method picker drives required fields. |
| AWS Secrets Manager | IAM (workload identity), static keys | yes (`ListSecrets`) | Region picker. |
| AWS Parameter Store | IAM | yes (`DescribeParameters`) | Region + parameter-tier picker. |
| Azure Key Vault | managed identity, service principal, workload identity | yes (`KeyClient.GetPropertiesOfKeysAsync`) | Vault URL + tenant picker. |
| GCP Secret Manager | workload identity, service account key | yes (`projects.secrets.list`) | Project ID picker. |
| Akeyless | jwt, kubernetes, plain | no | Free-text path. |
| Doppler | service token | no | Project + config picker via Doppler API (deferred). |
| 1Password Connect | connect token | yes (`ListItems`) | Vault picker. |
| Bitwarden Secrets Manager | access token | no | Project picker (deferred). |
| CyberArk Conjur | apiKey, jwt | no | Free-text path. |
| Kubernetes provider | service account in source ns | yes (list Secrets) | Cross-namespace picker. |
| Infisical | universal-auth (machine identity) | no | Project + environment picker. |

- After L7.2 culling pass: providers whose wizard is mostly a "fill the same 4 fields the YAML asks for" likely drop to YAML-template-only. Bitwarden Secrets Manager, Akeyless, CyberArk Conjur, and Infisical are candidates for culling unless smoke testing surfaces wizard-specific value. Final culling decisions made per provider during implementation; document in PR description which providers landed wizards vs templates.
- Cross-language regex parity test for any provider-specific field (path-component validators) (L7.3).

**Test scenarios:**
- *Per provider happy*: minimal valid input → YAML preview matches expected golden.
- *Per provider auth-method switch*: switching from `token` to `kubernetes` clears irrelevant fields (touched flag).
- *Per provider missing required*: empty required field → 422 with `FieldError`.
- *Cross-provider unique fields preserved*: switching from Vault to AWS doesn't leak Vault address into the AWS form.

**Verification:**
- `go test ./internal/wizard/...`.
- `deno task check`.
- Smoke per provider: create a SecretStore via each wizard against a homelab provider where feasible.

---

### Unit 20: Niche-provider YAML templates

**Goal:** Niche providers (Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, custom webhook) get pre-filled YAML templates in the editor — no per-provider wizard.

**Requirements:** R14

**Dependencies:** Unit 18 (wizard pattern; the Stores list page links to "Create from template").

**Files:**
- Create: `frontend/lib/eso-yaml-templates.ts` — TS map `{providerName: templateString}`.
- Modify: `frontend/islands/ESOStoresList.tsx` — "Create from template" dropdown next to "New" button.
- Modify: `frontend/routes/security/external-secrets/stores/new-from-template.tsx` — accepts `?template=<provider>` query param, renders Monaco editor pre-filled with the template + an "Apply" button.

**Approach:**
- Templates are minimal valid SecretStore YAMLs with TODO placeholders for required fields. e.g.:
  ```yaml
  apiVersion: external-secrets.io/v1
  kind: SecretStore
  metadata:
    name: TODO-store-name
    namespace: TODO-namespace
  spec:
    provider:
      pulumi:
        organization: TODO-org
        project: TODO-project
        environment: TODO-environment
        accessToken:
          secretRef:
            name: TODO-token-secret
            namespace: TODO-namespace
            key: token
  ```
- Apply uses the same `/yaml/apply` endpoint as the wizards.

**Verification:**
- `deno task check`.
- Smoke: create a Pulumi ESC SecretStore from template; YAML applies; observatory picks it up.

---

## Phase I — Chain visualization (topology overlay)

Extends Phase 7B topology builder with `?overlay=eso-chain` (L6.1, L6.2). Reuses `EdgeType` enum + `Overlay` enum extension pattern (L6.1).

### Unit 21: Backend — `ESOChainProvider` interface + `applyESOChainOverlay`

**Goal:** Implement the chain overlay analogous to Phase D mesh overlay. New `EdgeType` constants, new `Overlay` enum value, new `applyESOChainOverlay` function in the topology builder. Node-level RBAC. 2000-edge cap with `EdgesTruncated` flag (L6.2).

**Requirements:** R9, R10, R11

**Dependencies:** Phase A (ExternalSecret data source).

**Files:**
- Modify: `backend/internal/topology/types.go` — add `EdgeESOAuth` (auth Secret → SecretStore), `EdgeESOSync` (Store → ExternalSecret), `EdgeESOConsumer` (ExternalSecret → synced Secret → Pod). Add `OverlayESOChain Overlay = "eso-chain"`.
- Modify: `backend/internal/topology/builder.go` — `ESOChainProvider` interface (parallel to `MeshRouteProvider`); `applyOverlay` switch gains `case "eso-chain":`; `applyESOChainOverlay` runtime; `maxESOChainEdges = 2000`.
- Create: `backend/internal/topology/eso_chain_edges.go` — pure `buildESOChainEdges(externalsecrets, stores, clusterStores, secrets, pods, namespace, nameIndex, maxEdges)` with dedup by `(source, target, type)` (L6.5).
- Create: `backend/internal/topology/eso_chain_edges_test.go`.
- Create: `backend/internal/externalsecrets/topology_provider.go` — `Provider` impl of `ESOChainProvider`. Uses cached snapshot from the observatory handler; per-CRD-group RBAC fail-closed (L1.1).
- Modify: `backend/internal/topology/handler.go` — accept `overlay=eso-chain` value; route through `applyOverlay`.

**Approach:**
- **Default-response invariance** (L6.1): `Graph.Overlay` field already `omitempty`. New overlay value extends the existing pattern; no callers without `overlay=` see any change. Verified by a "default response is byte-identical" test.
- Edge construction:
  - For each ExternalSecret in namespace: emit edge `Store → ExternalSecret` (`EdgeESOSync`).
  - For each ExternalSecret with a referenced auth Secret in the Store's spec: emit edge `Secret(auth) → Store` (`EdgeESOAuth`).
  - For each ExternalSecret with a synced Secret: emit edge `ExternalSecret → Secret(synced)` (`EdgeESOConsumer`).
  - Workload edges (Secret(synced) → Pod / Deployment) ALREADY come from the existing builder's mount detection.
- Edge dedup by `(source, target, type)` — a single ClusterSecretStore referenced by 50 ExternalSecrets does not produce 50 duplicate `EdgeESOAuth` edges (L6.5).
- Host-form normalization for cross-namespace store references (L6.3): ClusterSecretStore can be referenced from any namespace; the chain builder resolves both `clusterstore-name` and `clusterstore-name.<ns>` forms via the same `.svc.` splitter pattern Phase D inherited.
- 2000-edge cap separate from node cap (L6.2): when `len(edges) >= maxESOChainEdges`, set `graph.EdgesTruncated = true`, stop emitting. Nodes themselves obey the existing `maxNodes = 2000` cap; that flag stays as `Truncated`.
- Per-CRD-group RBAC (L1.1): inside `applyESOChainOverlay`, before emitting any ESO-related edge, the caller's `CanAccessGroupResource(ctx, user, groups, "list", "external-secrets.io", "externalsecrets", namespace)` must return true. Fail-closed: deny → emit no edges, no error, no log indicating "you don't have access" (avoid existence-leak via timing).

**Patterns to follow:**
- `backend/internal/topology/mesh_edges.go` — pure edge-builder shape.
- `backend/internal/topology/builder.go:355–417` — `applyOverlay` switch + degradation pattern (`OverlayUnavailable` when provider unwired).

**Test scenarios:**
- *Happy*: 5 ExternalSecrets in `apps`, all referencing `vault-store` in `apps`; vault-store's auth Secret is `apps/vault-token`; one synced Secret per ES, one Pod mounting each → graph has the expected 6 edges.
- *Cross-namespace ClusterSecretStore*: ES in `apps` references `clustersecretstore-x`; auth Secret in `vault-system`; chain spans both namespaces (when topology endpoint is called with `namespace=apps`, ClusterSecretStore node appears even though it's cluster-scoped).
- *Default response unchanged*: GET `/topology/apps` (no overlay query) → response byte-identical to pre-Phase-I.
- *Overlay unavailable*: ESO not installed → response carries `Overlay: "unavailable"` (matches mesh precedent).
- *Edge cap*: 3000 ExternalSecrets → response includes `edgesTruncated: true`, not silent clip.
- *RBAC fail-closed*: user without `list externalsecrets` → ESO chain edges silently absent, response otherwise unchanged.
- *Edge dedup*: 50 ExternalSecrets sharing one ClusterSecretStore → exactly one `EdgeESOAuth` edge.
- *Host normalization*: store referenced as `vault-store`, `vault-store.apps`, `vault-store.apps.svc.cluster.local` all resolve to the same node.

**Verification:**
- `go test ./internal/topology/... ./internal/externalsecrets/... -count=10`.

---

### Unit 22: Frontend — chain overlay toggle on topology page + chain tab on detail

**Goal:** Topology page gains a third overlay toggle (after mesh): "ESO chain". Detail pages for SecretStore / ClusterSecretStore / ExternalSecret gain a "Chain" tab embedding the overlay-rendered subgraph. Standalone chain page (Unit 7's `/external-secrets/chain`) wraps the same overlay.

**Requirements:** R9, R10, R11

**Dependencies:** Unit 21.

**Files:**
- Modify: `frontend/islands/NamespaceTopology.tsx` — add overlay toggle for `eso-chain`. **Overlays are mutually exclusive** — selecting eso-chain deselects mesh, and vice versa. Toggle-group UI (radio-style), not independent checkboxes. Themed edge colors via CSS custom properties: `var(--accent)` for `EdgeESOAuth`, `var(--accent-secondary)` for `EdgeESOSync`, `var(--muted)` for `EdgeESOConsumer`. Disabled state when backend reports `overlay: "unavailable"`. **Prerequisite refactor:** if `NamespaceTopology` does not already accept a `focusedNode` prop for client-side BFS subgraph filtering, extract that capability before adding the chain tabs (the renderer is currently built for full-namespace view; embedded subgraph rendering is new).
- Modify: `frontend/islands/ESOStoreDetail.tsx`, `ESOClusterStoreDetail.tsx`, `ESOExternalSecretDetail.tsx` — add "Chain" tab embedding the topology view filtered to the chain neighborhood.

**Approach:**
- Reuses existing `NamespaceTopology` rendering primitives. The chain tab on detail pages fetches `/topology/{ns}?overlay=eso-chain` and filters client-side via BFS to nodes connected to the focal resource.
- Embedded chain tab spec: minimum panel height 400px; zoom/pan controls preserved; "View in Topology" escape-hatch link in the tab toolbar (opens the standalone topology page with the focal resource's namespace + overlay pre-toggled); empty-chain state ("No chain edges — this resource has no synced consumers yet"); responsive collapse on viewports <768px (controls move to a top toolbar instead of overlaying the SVG).
- `Overlay: "unavailable"` semantic: when the user requests `?overlay=eso-chain` against a cluster where ESO is not installed, the response is `Overlay: "unavailable"` regardless of whether mesh is installed. The frontend toggle disabled state with hover text "ESO not installed in this cluster" rather than a generic "overlay unavailable."
- Theme tokens only — no hardcoded edge colors.

**Verification:**
- `deno task check`.
- Smoke: topology page chain toggle renders the edges correctly; detail-page chain tab shows the focal-resource neighborhood.

---

## Phase J — Final docs + roadmap flip

### Unit 23: CLAUDE.md, README.md, roadmap, plan flip

**Goal:** Document the integration in CLAUDE.md (new Phase 14 entry), README.md (Security & Governance bullet update), roadmap item #8 → `[x]`, this plan → `status: complete`.

**Requirements:** R30

**Dependencies:** All prior phases shipped.

**Files:**
- Modify: `CLAUDE.md` — append Phase 14 (External Secrets Operator) to Build Progress; mirror Phase 11A/12 entry shape with the four lenses (history, drift, bulk-refresh, cost-tier) called out. Roadmap item #8 → `[x]`.
- Modify: `README.md` — Security & Governance feature bullet for ESO observatory; Architecture table gains "External Secrets" row.
- Modify: `plans/external-secrets-operator-integration.md` (this file) — `status: complete`.

**Approach:**
- CLAUDE.md entry covers: package paths, endpoint catalog, persistent history table + retention, drift tri-state, annotation chain, bulk refresh, cost-tier static rate cards, chain overlay, wizard set, Helm grant.
- Mark each completed unit as `[x]` in this plan as the work lands; flip top-level status only when all phases ship.

**Verification:**
- `make helm-lint` clean.
- Roadmap item #8 reads `[x]` post-merge.

---

## System-Wide Impact

- **Interaction graph:** New package `internal/externalsecrets/` with handler + poller + thresholds + cost-tier + bulk-worker; new wizard inputs in `internal/wizard/`; new topology overlay provider. `ApplyThresholds` is the single seam consumed by the handler `fetchAll` (L3.1). Notification dispatch gains a `SuppressResourceFields` flag honored by Slack and webhook channels.
- **Multi-cluster behavior:** X-Cluster-ID routing applies via existing `ClusterRouter`. The poller is local-cluster-only (Phase 11A constraint); remote-cluster ESO is observability-only via the on-demand impersonated handler. The `eso_sync_history` and `eso_bulk_refresh_jobs` tables are cluster-scoped via `cluster_id` (UUID). Notification dedupe key extends to `(cluster_id, uid, kind)` to prevent collision across multi-cluster routing.
- **Error propagation:** All new endpoints follow the project's error pattern (`{error: {code, message, detail}}`). 5xx avoided when ESO uninstalled (R2). Bulk-action partial failures reported per-resource (L8.1) — no aggregate-success-when-some-failed lies. Force-sync 409 is a first-class outcome, not a generic error (L8.4); bulk-refresh scope-changed is also 409 with `{added, removed}` diff.
- **State lifecycle risks:**
  - Sync history table grows unboundedly without retention; mitigated by hourly `DELETE WHERE attempt_at < now() - INTERVAL '90 days'`. Partitioning deferred until steady-state exceeds 10M rows (compliance-snapshots precedent — L5.5).
  - Bulk-refresh jobs table grows unboundedly without retention; add a 30-day retention sweep alongside the history table.
  - Notification dedupe map grows unboundedly per UID; mitigated by entry deletion on bucket transition to `synced` (Phase 13 precedent — L4.2). Restart recovery seeds prev-bucket from DB to prevent recovery-event suppression after restart.
  - Drift detection on detail page does an extra impersonated `Get`; bounded by the existing 30s cache horizon (impersonated calls are not cached cluster-wide; per-request).
- **API surface parity:** All new endpoints additive. `Graph.Overlay` field stays `omitempty` so default response is byte-identical to today (L6.1). ExternalSecret/Store list and detail endpoints introduce new types but don't modify existing types. `nc_notifications.source` enum extension is additive at the DB layer.
- **Integration coverage scenarios:**
  - (a) ESO uninstalled → all endpoints return empty / detected=false; no 5xx. Routes render empty state.
  - (b) ESO installed, no ExternalSecrets yet → list endpoints return `[]`; dashboard shows "No External Secrets" empty state.
  - (c) ExternalSecret with ClusterSecretStore reference, tenant operator without ClusterStore visibility → detail page shows "Source: <restricted>" placeholder; chain graph omits the ClusterStore node (L1.1, L2.2).
  - (d) Bulk refresh against a store with mixed-permission targets → response carries `{succeeded, failed, skipped}` per-target outcomes; audit log captures honest aggregate.
  - (e) Provider that doesn't populate `syncedResourceVersion` → drift status reports `Unknown`, never silent false-negative (R20 tri-state).
  - (f) ESO API version drift (cluster on `v1beta1`-only) → dynamic client transparently serves v1beta1 since it's still served (L6 in repo research).
  - (g) Notification rule routing for new event kinds → existing source_filter array column accepts `external_secrets`; rule UI auto-picks it up (no rule-engine change).
- **Unchanged invariants:**
  - Phase 11A package layout, naming, and behavior — ESO is a sibling, not a refactor.
  - `Graph.Truncated` (node cap) and `Graph.EdgesTruncated` (overlay edge cap) keep their current semantics.
  - Notification Service `DedupExists` 15-minute window stays at 15 min — ESO uses a separate in-memory `(uid, kind)` dedupe layered on top.
  - Audit log retention policy unchanged (90 days); ESO bulk-action records use the existing `Detail` JSON field.
  - User impersonation discipline: every write call uses the impersonating client (L8.3).

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Per-provider Go SDK pull-in via wizard typed structs would explode go.mod (L7.1) | Use `map[string]any` for `ProviderSpec` everywhere. Validators are hand-rolled, not type-checked. |
| Notification source enum CHECK constraint already drifted (silently includes velero, certmanager, limits without migration) (L4.3) | Migration 000011 fixes the drift while adding `external_secrets`. Dry-run against a migrated DB before merge. |
| Sync history table volume (~100M rows/year worst case) exceeds flat-table comfort | Monthly partitioning from day one (L5.2). Retention drops empty partitions, not row-by-row DELETEs. |
| Drift detection on list view would cause N+1 impersonated Gets | List view reports `LastObservedDriftStatus` from cached history snapshot only; live drift check is detail-page-only. Documented in Unit 3. |
| Bulk-refresh fan-out 200ms inter-call delay on a 1000-target store = 3+ minute fan-out | Frontend shows progress; the action is async-progress-polled rather than synchronous. Audit row written at completion, not per-target. Document in Unit 15 if smoke testing reveals UX issues. |
| Annotation cache TTL of 30s means edits don't apply immediately | Documented in operator docs (Phase J). Matches Phase 13 precedent. |
| Path-discovery proxy could be abused as an SSRF surface | Cut to Kubernetes provider only in v1 (k8s-RBAC-mediated, no source-store auth needed). Cross-provider path-discovery deferred to v2 with explicit auth-model design. |
| 12-provider wizard scope is sprawling; some wizards may add little value (L7.2) | Unit 19 split into per-provider sub-PRs. Each PR documents whether its provider ships as wizard or template (Unit 20). R13's contract is delivered when all 12 providers have at least one creation path; not 12 wizards specifically. |
| Cluster-scoped ClusterSecretStore references that span namespaces could leak existence via 403 vs 200 timing | Per-CRD-group RBAC fails closed silently (L1.1) — denial returns no data, never 403. |
| Force-sync detection of in-flight ESO reconcile | Use `lastRefreshTime within 30s` heuristic (rather than `Ready=Unknown`, which ESO doesn't reliably set mid-reconcile). 409 with `already_refreshing`. Heuristic limit: a sync legitimately taking >30s won't show as in-flight — accepted trade-off. |
| Bulk-refresh scope race between GET and POST | Client passes `targetUIDs` from GET; backend re-resolves at POST and rejects with 409 + diff if scope changed. |
| Bulk-refresh job lifetime spans backend restarts | Persisted `eso_bulk_refresh_jobs` table. On restart, in-flight jobs marked completed with current state. Operator can resume polling after navigation away. |
| Phase 7B 2000-node cap could clip a heavily-shared ClusterSecretStore chain silently | `EdgesTruncated` flag separate from `Truncated` (L6.2). Frontend renders "showing N of M" notice (Unit 22). |
| Notification recovery emission lost on poller restart | Seed prev-bucket map from `nc_notifications` at startup via `Store.RecentBySourceAndKind`. Without this, recovery alerts post-restart are silently suppressed (cert-manager precedent has the same bug; ESO fixes it). |
| Notification dispatch leaks tenant resource names to Slack/webhook | `SuppressResourceFields` flag on `Notification`; ESO events set true; dispatcher omits `ResourceNS`/`ResourceName` from external payload bodies. |
| Annotation `eso-stale-after-minutes` enables self-DoS | 5-minute floor enforced in resolver; values below are rejected and fall through. |
| Helm `core/secrets get` grant expands platform SA privilege | Documented explicitly in Unit 6; operators in stricter environments can remove the grant; diff-key feature degrades but timeline still functions. |
| Map-iteration-order flakes in tests | All new tests run with `-count=10` per L7.5 / V2. |
| Helm chart changes not exercised locally before push | `make helm-lint` + `make helm-template` mandatory pre-push checks (V1). |
| Theme palette extension (`--warning`, `--accent-secondary` may be new) touches all 7 themes | Unit 7 verifies presence in `frontend/static/themes/*.css` before starting; add to all themes if absent. |

## Documentation / Operational Notes

- Operator-facing: a paragraph in CLAUDE.md (Phase J) covers the annotation surface (`kubecenter.io/eso-stale-after-minutes` with 5-minute floor, `kubecenter.io/eso-alert-on-recovery`, `kubecenter.io/eso-alert-on-lifecycle`), the resolution chain (resource > store > clusterstore > default), the 30s cache TTL for annotation edits, and the bulk-refresh RBAC-scoping behavior.
- Cost-tier rate cards refresh cadence: quarterly hand-update of the Go map literal in `backend/internal/externalsecrets/cost_tier.go`. Each entry's `LastUpdated` field is rendered to the operator inline ("Rates as of YYYY-MM-DD"); drift between rate cards and provider pricing is a known acceptable trade-off (R26).
- Sync history retention: 90 days, hardcoded in retention goroutine. Future operator-config option deferred.
- Drift detection coverage: per-provider `syncedResourceVersion` population is enumerated during Phase C smoke testing; populate a "providers that expose drift" table in operator docs as a Phase J follow-up artifact. Drift detection on the **list view** has up to ~90s staleness (60s poller + 30s handler cache); the **detail page** is always fresh (live RV check on every request). Drift detection requires `get secret` on the synced Secret target — users without that perm see `DriftStatus: Unknown` permanently.
- Path-discovery is Kubernetes-provider-only in v1. All other providers in the wizard render free-text path inputs. Cross-provider path-discovery is deferred to a v2 with an explicit per-provider auth-model design.
- PushSecret read-only: spec is intentionally surfaced (selector + remoteRef paths visible to anyone with `list pushsecrets`). PushSecret write surface remains deferred to v2 contingent on usage signals; the read-only-only v1 surface may suppress demand signal because operators have no easy creation path. Re-evaluate at the 6-month mark using observed ExternalSecret-vs-PushSecret ratios.
- The `eso_sync_history` schema is intentionally ESO-specific. If similar polled-state timelines emerge (GitOps revision history, drift events for other observatory packages), consider extracting a shared schema; defer that decision until at least one second use case lands.
- Notification rule UI: the `frontend/islands/NotificationRules.tsx` source selector groups entries by category in Phase D (Infrastructure / Policy / Secrets / Operations) — keeps the 11-entry checkbox grid usable.

## Sources & References

- Origin requirements: `docs/brainstorms/2026-04-29-external-secrets-operator-requirements.md`.
- Phase 11A precedent: `backend/internal/certmanager/` (entire package); CLAUDE.md Phase 11A entry.
- Phase 11B precedent: `backend/internal/wizard/{certificate,issuer,cert_helpers}.go`; `frontend/islands/{Certificate,Issuer}Wizard.tsx`.
- Phase 13 precedent: `backend/internal/certmanager/thresholds.go`; CLAUDE.md Phase 13 entry; `plans/cert-manager-configurable-expiry-thresholds.md`.
- Phase 12 precedent: `backend/internal/topology/{builder,mesh_edges,types}.go`; `plans/service-mesh-observability-phase-d1-topology-backend.md`.
- Notification Center: `backend/internal/notifications/{types,service,store}.go`; migration `000007_create_notification_center.up.sql`.
- Audit logger: `backend/internal/audit/{logger,postgres_logger,store}.go`; migration `000001_create_audit_logs.up.sql`.
- Migration framework: `backend/internal/store/migrate.go`; `migrations/000005_create_compliance_snapshots.up.sql` for cluster_id+UID-keyed flat-table precedent; `migrations/000001_create_audit_logs.up.sql` for retention pattern.
- Helm RBAC: `helm/kubecenter/templates/clusterrole.yaml` (Istio/Linkerd block at lines 123–146 in current chart).
- Roadmap: `CLAUDE.md` "Future Features" item #8.

## Implementation Units (checklist)

Order is **A → B → D → C → E → F → G → H → I → J**.

### Phase A — Backend observatory + Helm RBAC
- [ ] Unit 1 — Package skeleton (discovery + types + normalize, Go-TS hash test)
- [ ] Unit 2 — Handler (singleflight + cache + permissive-read RBAC + list endpoints)
- [ ] Unit 3 — Detail endpoints + drift resolution (impersonated `get secret`)
- [ ] Unit 4 — Routes wiring (under authenticated `ar.Group` for CSRF)
- [ ] ~~Unit 5~~ — DELETED (metric name constants live in `cost_tier.go`)
- [ ] Unit 6 — Helm ClusterRole grant (ESO CRD list/watch only; core/secrets get/list deferred to Unit 10's PR — see Unit 6 body)

### Phase B — Frontend observatory
- [ ] Unit 7 — Routes + list islands + new ESO nav-rail domain section + standalone Chain page + per-list empty states
- [ ] Unit 8 — Detail islands + Dashboard (sync-health hero ring, broken-ESes-with-consumer-counts table)

### Phase D — Alerting + annotation thresholds (lands BEFORE Phase C)
- [ ] Unit 12 — Migration 000010 (source enum extension via ALTER) + annotation resolver + `SuppressResourceFields` dispatch flag + notification source grouping
- [ ] Unit 13 — Notification dispatch (failure + recovery + lifecycle, separate dedupe keys)

### Phase C — Persistence + drift
- [ ] Unit 9 — Migration 000011 (`eso_sync_history` flat table, three text[] diff columns, partition-candidate comment)
- [ ] Unit 10 — Sync-history persistence in poller (with restart-recovery prev-bucket seeding)
- [ ] Unit 11 — Drift detection wiring on detail + Revert button + dashboard counts

### Phase E — Bulk refresh actions
- [ ] Unit 14 — Force-sync action + impersonation + audit (`lastRefreshTime` heuristic for in-flight detection)
- [ ] Unit 15 — Migration 000012 (`eso_bulk_refresh_jobs`) + async job model + scope-pinned execution + dialog + progress polling

### Phase F — Per-store rate + cost-tier panel
- [ ] Unit 16 — Per-store rate panel + cost-tier card (Go map literal rate cards with `LastUpdated`)

### Phase G — Universal ExternalSecret wizard
- [ ] Unit 17 — ExternalSecret wizard (Kubernetes-provider-only path-discovery) + regex parity test

### Phase H — Per-provider SecretStore wizards (split into per-provider sub-PRs)
- [ ] Unit 18 — SecretStore wizard scaffold + StoreScope discriminator
- [ ] Unit 19 — Per-provider validators + form components (one sub-PR per provider; ~36 form variants total)
- [ ] Unit 20 — Niche-provider YAML templates

### Phase I — Chain visualization
- [ ] Unit 21 — Backend ESOChainProvider + applyESOChainOverlay
- [ ] Unit 22 — Frontend chain overlay toggle (mutually exclusive with mesh) + chain tab on detail (with `focusedNode` BFS prerequisite refactor)

### Phase J — Final docs flip
- [ ] Unit 23 — CLAUDE.md (Phase 14 ESO entry), README.md, roadmap (#8 → [x]), plan status → complete
