# Codebase Cleanup Plan (2026-04-15)

Three tiers of cleanup derived from `cleanup-2026-04-15-research.md`. Each PR is
independently shippable with `/review` + CI + smoke test gate.

## Tier 1 — Low risk, high ROI (IN PROGRESS)

### PR-A: Dead code removal
**Delete:**
- `frontend/islands/ClusterSelector.tsx`
- `frontend/islands/PodExec.tsx`
- `frontend/islands/StorageOverview.tsx`
- `frontend/assets/styles.css` lines 227-236 (8 unused CSS aliases)
- `backend/internal/auth/ldap.go:37` — `LDAPProviderConfig` type alias
- `backend/internal/auth/oidc.go:24` — `OIDCProviderConfig` type alias

**Verify:** grep each symbol before delete; `deno lint`, `go vet`, `deno fmt --check`.

### PR-B: Slop cleanup
**Delete section divider banners:**
- `backend/internal/velero/handler.go` (18×)
- `backend/internal/gateway/handler.go` (6×)
- `backend/internal/certmanager/handler.go` (6×)

**Delete narrating doc comment second-lines in:**
- `backend/internal/alerting/webhook.go:40-41`
- `backend/internal/alerting/handler.go:40-41, 168-169, 184-185`

**Delete debug statements:**
- `frontend/islands/NotificationFeed.tsx:104` — `console.warn("markRead failed:", e)`
- `frontend/islands/NotificationBell.tsx:120` — same

### PR-C: Type consolidation micro-fixes
- Move `KeyValueEntry` from `components/ui/KeyValueListEditor.tsx:4` and `islands/ConfigMapWizard.tsx:13` → `lib/wizard-types.ts`.
- Add `lastBackupPhase?: string` to `Schedule` in `lib/velero-types.ts`.
- Export `StatusBadgeProps` from `components/ui/StatusBadge.tsx`.

### PR-D: Defensive programming fixes
**Remove impossible-error catches:**
- `backend/internal/websocket/client.go:164,237,263`
- `backend/internal/websocket/hub.go:321`
- `backend/internal/yaml/parser.go:64`

**Add logging to silent fallbacks:**
- `frontend/lib/api.ts:78-80` (token refresh)
- `frontend/lib/auth.ts:115-118` (fetchCurrentUser)
- `frontend/lib/auth.ts:136-141` (refreshPermissions)

**Fix error context loss:**
- `backend/internal/loki/client.go:205` — include raw body in fallback error.

## Tier 2 — Medium risk, dedicated review

### PR-E: Shared Condition/EventSource helpers (weak types)
- Extract `Condition` + `EventSource` + `MatchLabels` typed structs used across `backend/internal/policy/`, `backend/internal/gitops/`, `backend/internal/notification/`.
- Replace `map[string]interface{}` assertions in: `policy/kyverno.go`, `policy/gatekeeper.go`, `notification/flux_notifications.go`, `gitops/argocd.go`, `gitops/flux.go`.

### PR-F: ResourceDetail.tsx typing pass
- Kill `as any` casts at `ResourceDetail.tsx:613,625,647` and related spots.
- Introduce `extractPodSpec<T>(resource): T | null` and similar helpers in `lib/k8s-types.ts`.
- Type `api.ts:72-76` refresh response as `RefreshResponse`.

### PR-G: Frontend hooks + badge consolidation
- `useApi<T>(url, deps)` hook in `lib/hooks/`.
- `useWizardForm<T>` + `useWizardPreview<T>` hooks.
- Migrate 3-5 wizards as pilot (ConfigMap, Service, StatefulSet), measure LOC reduction before expanding.
- Consolidate badge color maps into `lib/color-maps.ts`; single `ColorBadge` component.

## Tier 3 — COMPLETE (2026-04-16)

### Handler Adapter Refactor (PRs #191-#195 + #192)
- Interface + Registration approach (not generics — per brainstorm review)
- 30 adapters registered, 23 old handler files deleted
- Design spec: `docs/superpowers/specs/2026-04-16-handler-adapter-refactor-design.md`
- Implementation plan: `docs/superpowers/plans/2026-04-16-handler-adapter-refactor.md`

## Workflow per PR
1. Branch `fix/cleanup-prX-<slug>` from `main`.
2. Implement.
3. `go vet ./...`, `deno lint`, `deno fmt --check`, tests.
4. Push; verify CI green.
5. `/compounding-engineering:workflows:review` with parallel agents; address all findings.
6. Smoke test against homelab if backend/frontend behavior changed.
7. Merge, delete local + remote branch.
