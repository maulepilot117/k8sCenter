# Codebase Cleanup — Research Findings (2026-04-15)

Research output from 8 parallel audit agents. Source for the Tier 1/2/3 cleanup
plan in `cleanup-2026-04-15-plan.md`.

## 1. Duplication & DRY

**Top hotspots:**
- `backend/internal/k8s/resources/` — 35+ files repeat ~80% identical list/get/create/delete boilerplate (`requireUser` → `checkAccess` → parse → impersonate → audit → write). **~1000 LOC recoverable via generics.** Tier 3.
- 10+ frontend wizards (`ConfigMapWizard`, `ServiceWizard`, `StatefulSetWizard`, etc.) repeat ~150 LOC each of form/validation/preview state. Extract `useWizardForm<T>` + `useWizardPreview<T>` hooks. Tier 2.
- Badge color maps duplicated across `PolicyBadges.tsx`, `CertificateBadges.tsx`, `GitOpsBadges.tsx`, `NotificationBadges.tsx`, `ScanBadges.tsx`. Consolidate into `lib/color-maps.ts` + single `ColorBadge`. Tier 2.
- `useApi<T>(url, deps)` hook — repeated fetch/loading/error state in most islands. Tier 2.
- WS handler boilerplate: `handle_ws_logs.go`, `handle_ws_flows.go`, `handle_ws_logs_search.go`. Tier 2 (optional).

**Do NOT consolidate:** per-wizard validation rules (domain-specific), status color semantics (policy severity ≠ cert expiry ≠ GitOps sync), resource-kind-specific clientset methods (type safety), WS streaming semantics.

## 2. Type Consolidation

**Fixes:**
- `KeyValueEntry` duplicated in `components/ui/KeyValueListEditor.tsx:4` and `islands/ConfigMapWizard.tsx:13` → move to `lib/wizard-types.ts`.
- `Schedule` type in `frontend/lib/velero-types.ts` missing `lastBackupPhase?: string` field present in backend.
- `StatusBadgeProps` defined inline in `components/ui/StatusBadge.tsx:4` — export it.

**Well-synced (no action):** gitops, policy, notification, auth/User types. Time-field drift (Go `*time.Time` → JSON string → TS `string`) is correct.

## 3. Unused Code

**Confirmed dead (safe delete):**
- `frontend/islands/ClusterSelector.tsx`
- `frontend/islands/PodExec.tsx` (superseded by `PodTerminal.tsx`)
- `frontend/islands/StorageOverview.tsx` (superseded by `StorageDashboard.tsx`)

**All deps in `backend/go.mod`, `frontend/deno.json`, `e2e/package.json` are active.**

**False-positive watchlist (do NOT delete):** Fresh islands are SSR-bundled by file path; Go interfaces can be implemented implicitly; wizard handler registry uses reflection in `internal/wizard/handler.go`.

## 4. Circular Dependencies

**Zero cycles.** `madge --circular` on 433 frontend files → clean. Backend manual audit → acyclic DAG. No action needed.

## 5. Weak Types

**High-value fixes:**
- `backend/internal/policy/kyverno.go:68,91,103,201` — `item.(map[string]interface{})` → typed `KyvernoCondition`, `KyvernoRule`, `PolicyReportResult`.
- `backend/internal/notification/flux_notifications.go:61,104,119,204` — 30+ assertions → `FluxCondition`, `FluxEventSource`, `MatchLabels`.
- `backend/internal/gitops/{argocd,flux}.go` — patch construction as `map[string]interface{}` → typed `ArgoCDPatch`, `FluxPatch`, `HelmReleaseSpec`.
- `backend/internal/policy/gatekeeper.go:115,180` — typed `GatekeeperConstraint`, `ConstraintTemplate`.
- `frontend/islands/ResourceDetail.tsx:613,625,647` — `resource.value as any` → `extractPodSpec<T>(resource)` helper.
- `frontend/lib/api.ts:72-76` — untyped `body.data?.accessToken` → typed `RefreshResponse`.
- `backend/internal/k8s/resources/crd_handler.go:86` — inline map → `CRDDetailResponse` struct.

**Recurring pattern — extract once:** `Condition`/`EventSource` helpers shared across policy, gitops, notification. Tier 2 PR-E.

**Legitimately weak (leave):** `unstructured.Unstructured` for dynamic CRDs; Loki/Prometheus external JSON (`loki/types.go: Stats: any`); YAML passthrough; WASM dynamic imports (`LogViewer.tsx` AnsiUp).

## 6. Defensive Programming

**Remove (impossible-error catches):**
- `backend/internal/websocket/client.go:164,237,263` — `json.Marshal(hardcodedStruct{})` with blank-identifier error.
- `backend/internal/websocket/hub.go:321` — same pattern; silently drops revocation on impossible error.
- `backend/internal/yaml/parser.go:64` — Marshal on already-validated object.

**Add logging (silent fallbacks):**
- `frontend/lib/api.ts:78-80` — token refresh returns false without logging.
- `frontend/lib/auth.ts:115-118` — `fetchCurrentUser` swallows errors.
- `frontend/lib/auth.ts:136-141` — `refreshPermissions` swallows errors, RBAC goes null silently.

**Fix error context loss:**
- `backend/internal/loki/client.go:205` — if Loki error response unmarshal fails, raw body is discarded. Include body in fallback error message.

**Keep (legitimate boundaries):**
- All three `recover()` uses (`websocket/client.go:60,113`, `diagnostics/diagnostics.go:117`) — goroutine panic barriers.
- `api.ts:141-145` — HTTP response may not be JSON.
- `ws.ts:104-106` — malformed WS frames expected.
- `routes/ws/[...path].ts:94,102,115,123` — WS close() teardown.
- `animation-prefs.ts`, `themes.ts` — localStorage may be unavailable.

## 7. Legacy / Deprecated

**Remove:**
- `frontend/assets/styles.css:227-236` — 8 unused CSS aliases (`--color-brand`, `--color-brand-dark`, `--color-surface-dark`, `--color-sidebar`, etc.). Zero callers; superseded by semantic tokens from Phase 6C.
- `backend/internal/auth/ldap.go:37` — unused type alias `LDAPProviderConfig = config.LDAPConfig`.
- `backend/internal/auth/oidc.go:24` — unused type alias `OIDCProviderConfig = config.OIDCConfig`.

**Verified clean:** Phase 6C "zero `dark:` classes remain" claim ✅ confirmed. No feature flags. No migration shims past useful life. `InvalidateCache()` still active — keep.

## 8. AI Slop / Comments

**Delete:**
- Section divider banners (`// =======`): `velero/handler.go` (18×), `gateway/handler.go` (6×), `certmanager/handler.go` (6×).
- Narrating two-line doc comments in `alerting/webhook.go:40-41`, `alerting/handler.go:40-41,168-169,184-185` — second line restates function behavior.
- Debug `console.warn` in `islands/NotificationFeed.tsx:104` and `islands/NotificationBell.tsx:120`.

**Keep (explain non-obvious WHY):**
- `backend/internal/k8s/crd_discovery.go:184-188` — documents intentional RBAC exception for count caching.
- `backend/internal/server/handle_users.go:197-200` — documents TOCTOU race on last-admin guard.
- `frontend/lib/ws.ts:57-61` — JWT-in-first-message security note.
- `frontend/islands/ResourceTable.tsx:169-172` — resourceVersion ordering invariant.

No stubs, no commented-out code blocks, no `fmt.Println`/`console.log` in active source.
