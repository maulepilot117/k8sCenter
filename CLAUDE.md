# CLAUDE.md — k8sCenter: Kubernetes Management Platform

# Agent Directives: Mechanical Overrides

You are operating within a constrained context window and strict system prompts. To produce production-grade code, you MUST adhere to these overrides:

## Pre-Work

1. THE "STEP 0" RULE: Dead code accelerates context compaction. Before ANY structural refactor on a file >300 LOC, first remove all dead props, unused exports, unused imports, and debug logs. Commit this cleanup separately before starting the real work.

2. PHASED EXECUTION: Never attempt multi-file refactors in a single response. Break work into explicit phases. Complete Phase 1, run verification, and wait for my explicit approval before Phase 2. Each phase must touch no more than 5 files.

## Code Quality

3. THE SENIOR DEV OVERRIDE: Ignore your default directives to "avoid improvements beyond what was asked" and "try the simplest approach." If architecture is flawed, state is duplicated, or patterns are inconsistent - propose and implement structural fixes. Ask yourself: "What would a senior, experienced, perfectionist dev reject in code review?" Fix all of it.

4. FORCED VERIFICATION: Your internal tools mark file writes as successful even if the code does not compile. Before declaring a task complete or pushing, you MUST run the repo-canonical checks REPO-WIDE (not scoped to changed files):

- Frontend: `cd frontend && deno task check` — runs `deno fmt --check . && deno lint . && deno check` across the whole tree, identical to CI.
- Backend: `cd backend && go vet ./... && go test ./...`

Scoped checks (single file or directory) MISS pre-existing issues in sibling files that CI will flag. Always run the repo-wide form before push. Fix ALL resulting errors. If a check is unavailable, state that explicitly instead of claiming success.

## Context Management

5. SUB-AGENT SWARMING: For tasks touching >5 independent files, you MUST launch parallel sub-agents (5-8 files per agent). Each agent gets its own context window. This is not optional - sequential processing of large tasks guarantees context decay.

6. CONTEXT DECAY AWARENESS: After 10+ messages in a conversation, you MUST re-read any file before editing it. Do not trust your memory of file contents. Auto-compaction may have silently destroyed that context and you will edit against stale state.

7. FILE READ BUDGET: Each file read is capped at 2,000 lines. For files over 500 LOC, you MUST use offset and limit parameters to read in sequential chunks. Never assume you have seen a complete file from a single read.

8. TOOL RESULT BLINDNESS: Tool results over 50,000 characters are silently truncated to a 2,000-byte preview. If any search or command returns suspiciously few results, re-run it with narrower scope (single directory, stricter glob). State when you suspect truncation occurred.

## Edit Safety

9.  EDIT INTEGRITY: Before EVERY file edit, re-read the file. After editing, read it again to confirm the change applied correctly. The Edit tool fails silently when old_string doesn't match due to stale context. Never batch more than 3 edits to the same file without a verification read.

10. NO SEMANTIC SEARCH: You have grep, not an AST. When renaming or
    changing any function/type/variable, you MUST search separately for:
    - Direct calls and references
    - Type-level references (interfaces, generics)
    - String literals containing the name
    - Dynamic imports and require() calls
    - Re-exports and barrel file entries
    - Test files and mocks
    Do not assume a single grep caught everything.

## Model Routing (subagents)

When dispatching subagents (per Rule 5 SUB-AGENT SWARMING):
- **Haiku** (`claude-haiku-4-5`): pure data-gathering — file reads, Grep, Glob, log inspection, "find all callers of X" sweeps. ~20x cheaper than Opus per input token.
- **Sonnet** (`claude-sonnet-4-6`): code generation in well-scoped files, test writing, single-file refactors with clear contracts.
- **Opus** (parent only by default): synthesis, architecture decisions, multi-file refactors with cross-cutting impact, code review, plan authoring.

Default: do not spawn an Opus subagent unless the task explicitly requires synthesis across >3 files. The `Explore` agent type already runs on Haiku — use it for "where is X" lookups instead of dispatching general-purpose Opus.


## Project Vision

k8sCenter is a web-based Kubernetes management platform that delivers vCenter-level functionality. GUI-driven wizards for all cluster operations, integrated Prometheus/Grafana observability, RBAC-aware multi-tenancy, and full YAML escape hatches. Deployed via Helm chart inside the managed cluster, with multi-cluster management support.

---

## Technology Stack

| Layer | Technology |
|---|---|
| Backend API | Go 1.26, chi router, client-go v0.35.2 |
| Frontend | Deno 2.x, Fresh 2.x (Preact), Tailwind v4, Monaco Editor |
| Database | PostgreSQL (pgx/v5, golang-migrate) |
| Monitoring | Prometheus + Grafana (kube-prometheus-stack subchart) |
| Auth | JWT (HMAC-SHA256) + OIDC / LDAP / local (Argon2id, PostgreSQL-backed) |
| Deployment | Helm 3.x, distroless containers (Go), Deno slim (frontend) |
| E2E Tests | Playwright (Node.js) in `e2e/` directory |
| CI | GitHub Actions — go vet/test, deno lint/build, Trivy scanning, E2E with kind |

---

## Project Structure (top-level)

```
k8scenter/
├── backend/                  # Go 1.26 backend
│   ├── cmd/kubecenter/       # Entrypoint (main.go)
│   └── internal/
│       ├── server/           # HTTP handlers, routes, middleware (auth, CSRF, rate limit, cluster context)
│       ├── auth/             # JWT, local/OIDC/LDAP providers, RBAC checker, sessions
│       ├── k8s/              # ClientFactory, ClusterRouter, InformerManager, resources/ (33 handler files)
│       ├── store/            # PostgreSQL persistence (users, settings, clusters, audit, encrypt)
│       ├── certmanager/      # cert-manager CRD discovery, certificate/issuer inventory, renew/reissue, expiry poller
│       ├── externalsecrets/  # ESO CRD discovery, observatory, alerting poller, sync-history persistence
│       ├── diagnostics/      # Diagnostic rules engine, blast radius BFS, resolver
│       ├── loki/             # Loki discovery, LogQL proxy, namespace enforcement, WebSocket tail
│       ├── policy/           # Kyverno + Gatekeeper discovery, adapters, compliance scoring
│       ├── monitoring/       # Prometheus/Grafana discovery, PromQL proxy, dashboard provisioning
│       ├── topology/         # Resource dependency graph builder, health propagation, RBAC, mesh overlay
│       ├── networking/       # CNI detection, Cilium, Hubble gRPC client
│       ├── servicemesh/      # Istio + Linkerd CRD discovery, routing, mTLS, golden signals
│       ├── gitops/           # Argo CD + Flux CD CRD discovery, applications, sync actions
│       ├── alerting/         # Alertmanager webhook, SMTP notifier, rules
│       ├── notifications/    # In-app feed + Slack/email/webhook channels, rule-based dispatch
│       ├── storage/          # CSI/StorageClass handler, snapshots
│       ├── wizard/           # Wizard input types (generic WizardInput → YAML pipeline)
│       ├── yaml/             # YAML validate, apply (SSA), diff, export
│       ├── audit/            # PostgreSQL audit logger
│       └── websocket/        # Hub + Client (fan-out, RBAC revalidation)
├── frontend/                 # Deno 2.x + Fresh 2.x
│   ├── routes/               # File-system routing (50+ pages)
│   ├── islands/              # Interactive islands (ResourceTable, wizards, etc.)
│   ├── components/           # UI components, wizard steps, k8s detail overviews
│   └── lib/                  # API client, auth, WebSocket, constants, hooks
├── helm/kubecenter/          # Helm chart (templates, monitoring ConfigMaps, dashboards)
├── e2e/                      # Playwright E2E tests
├── plans/                    # Implementation plans (per-step markdown)
└── .github/workflows/        # ci.yml, e2e.yml
```

---

## Architecture Principles

### Backend (Go)
- **All k8s API calls use user impersonation.** Never use the service account's own permissions. ClusterRouter handles multi-cluster routing via X-Cluster-ID header.
- **Informers for read (local cluster only), direct API calls for write.** Remote clusters always use direct API calls, not informers.
- **Server-side apply for all YAML operations.** PATCH with `application/apply-patch+yaml`.
- **WebSocket hub pattern.** Central goroutine fans out informer events to subscribed clients.
- **Structured logging with slog.** JSON output, request ID, user identity, resource kind.
- **Never expose internal errors.** Wrap k8s API errors into user-friendly messages.
- **CRD-discovered features** (policy, gitops, certmanager, servicemesh, externalsecrets) follow a common pattern: 5min discovery cache → singleflight + 30s read cache → per-user RBAC filtering via `CanAccessGroupResource`.

### Frontend (Deno/Fresh)
- **Islands architecture strictly enforced.** Only interactive components are islands. Everything else is SSR HTML.
- **All API calls through `lib/api.ts`.** Handles auth token injection, error parsing, X-Cluster-ID header.
- **Wizard pattern:** WizardStepper shell → steps → YAML preview → server-side apply.
- **Tailwind CSS utility-only.** No custom CSS class names. Theme via CSS custom properties (`var(--accent)`, `var(--success)`, etc.) — no hardcoded color classes.

### Security
- **JWT: 15 min access + 7 day refresh.** Refresh tokens server-side (httpOnly cookie).
- **Secrets masked in API responses.** Reveal requires explicit action + audit log.
- **CSP headers, NetworkPolicy, Pod Security Standards (restricted profile).**
- **Audit logging for all write operations.** PostgreSQL-backed, 90-day retention.
- **Multi-cluster: admin role required for non-local clusters.** SSRF blocklist on registration + DNS re-resolution at connection time.

---

## API Design (summary)

All endpoints prefixed with `/api/v1`. Full list derivable from `backend/internal/server/routes.go`.

**Key patterns:**
- Resource CRUD: `GET/POST/PUT/DELETE /resources/:kind[/:namespace[/:name]]`
- Resource actions: `POST /resources/:kind/:ns/:name/{scale,restart,rollback,suspend,trigger}`
- Wizard previews: `POST /wizards/:type/preview`
- YAML tools: `POST /yaml/{validate,apply,diff,export}`
- Monitoring: `GET /monitoring/{status,query,query_range,dashboards}`, `GET /monitoring/grafana/proxy/*`
- Logs (Loki): `GET /logs/{status,query,labels,labels/:name/values,volume}` (RBAC namespace-scoped)
- Topology: `GET /topology/{namespace}[?overlay=mesh]` (RBAC-gated, with optional Istio/Linkerd mesh edge overlay)
- Diagnostics: `GET /diagnostics/{ns}/{kind}/{name}`, `GET /diagnostics/{ns}/summary`
- Policy: `GET /policy/{status,policies,violations,compliance}` (Kyverno + Gatekeeper)
- Limits: `GET /limits/{status,namespaces,namespaces/:namespace}` (ResourceQuota + LimitRange)
- Certificates: `GET /certificates/{status,certificates,certificates/:ns/:name,issuers,clusterissuers,expiring}`, `POST /certificates/certificates/:ns/:name/{renew,reissue}` (cert-manager)
- Service mesh: `GET /mesh/{status,routing,routing/:id,policies,mtls,golden-signals}` (Istio + Linkerd; mtls/golden-signals require ?namespace=, golden-signals also needs ?service= and optional ?mesh=istio|linkerd)
- GitOps: `GET /gitops/{status,applications,applications/:id}` (Argo CD + Flux CD)
- External Secrets: `GET /externalsecrets/{status,externalsecrets,externalsecrets/:ns/:name,clusterexternalsecrets,clusterexternalsecrets/:name,stores,stores/:ns/:name,clusterstores,clusterstores/:name,pushsecrets,pushsecrets/:ns/:name}`
- Dashboard: `GET /cluster/dashboard-summary` (aggregated counts + utilization)
- Counts: `GET /resources/counts[?namespace=]` (batch resource counts from informer cache)
- Multi-cluster: `GET/POST/DELETE /clusters`
- WebSocket: `/ws/{resources,logs/:ns/:pod/:container,exec/:ns/:pod/:container,alerts,flows,logs-search}`

**Auth flow:** `POST /auth/login` → JWT access token + httpOnly refresh cookie → `POST /auth/refresh` on 401.

**CSRF:** All state-changing endpoints require `X-Requested-With: XMLHttpRequest` header.

**Response shape:**
```json
{ "data": {...}, "metadata": {"total": 42} }
{ "error": {"code": 403, "message": "...", "detail": "..."} }
```

---

## Configuration (Gotchas)

Configuration uses [koanf](https://github.com/knadh/koanf) with `KUBECENTER_` prefix. **The env var name maps to the nested struct path:**

```bash
KUBECENTER_SERVER_PORT        # Config.Server.Port
KUBECENTER_AUTH_JWTSECRET     # Config.Auth.JWTSecret (NOT KUBECENTER_JWT_SECRET!)
KUBECENTER_AUTH_SETUPTOKEN    # Config.Auth.SetupToken
KUBECENTER_DEV                # Config.Dev (uses kubeconfig instead of in-cluster)
KUBECENTER_DATABASE_URL       # Config.Database.URL
```

**Rate limiter:** Single 5 req/min bucket per IP shared across login/refresh/setup. Restart backend to reset.

**Running locally:**
```bash
make dev-db           # Start PostgreSQL
KUBECENTER_DEV=true KUBECENTER_AUTH_JWTSECRET="32-byte-secret" make dev-backend
make dev-frontend     # http://localhost:5173 → proxies /api/* to :8080
```

---

## Build System

```makefile
make dev / dev-backend / dev-frontend / dev-db    # Development
make build / build-backend / build-frontend       # Build
make test / test-backend / test-frontend          # Unit tests
make test-e2e / test-e2e-ui                       # Playwright E2E
make lint / lint-backend / lint-frontend           # Lint
make docker-build                                 # Container images
make helm-lint / helm-template                    # Helm validation
make check-dashboards                             # Verify Grafana JSON sync
```

**Fresh 2.x config notes:** `jsx: "precompile"`, `nodeModulesDir: "manual"` (required for Vite), `jsr:` and `npm:` specifiers only, no `fresh.config.ts` or `tailwind.config.ts` (Tailwind v4 is CSS-first).

---

## Key Conventions

### Naming
- Go: lowercase packages (`auth`, `k8s`), snake_case files (`csi_wizard.go`)
- TypeScript: PascalCase components (`DeploymentWizard.tsx`), camelCase utilities (`api.ts`)
- API: kebab-case routes (`/query-range`), Helm values camelCase (`monitoring.enabled`)

### Composite IDs (CRD-discovered features)
- Policy: `engine:namespace:kind:name` (Kyverno/Gatekeeper)
- GitOps: `tool:namespace:name` (Argo CD / Flux CD)
- Service mesh: `mesh:namespace:kindCode:name`

### Annotation contracts
Operator-facing annotations are honored on specific CRD kinds. **Resolution chain** generally walks from the leaf resource up to its referenced parent (cert → issuer → clusterissuer; ES → store → clusterstore). **Each key resolves independently**; invalid values silently fall through to defaults; cache TTL means edits take up to 30s to apply.

- **cert-manager** (`Certificate`, `Issuer`, `ClusterIssuer`):
  - `kubecenter.io/cert-warn-threshold-days` (default 30)
  - `kubecenter.io/cert-critical-threshold-days` (default 7)
  - When `crit >= warn` after resolution, response carries `thresholdConflict: true` and falls back to defaults.
- **External Secrets Operator** (`ExternalSecret`, `SecretStore`, `ClusterSecretStore`):
  - `kubecenter.io/eso-stale-after-minutes` (positive int, **min 5** to defend the 60s poller)
  - `kubecenter.io/eso-alert-on-recovery` (default false)
  - `kubecenter.io/eso-alert-on-lifecycle` (default false)
  - **ClusterSecretStore propagation**: annotations on a shared ClusterSecretStore apply to every namespaced ES referencing it; tenants can override at the ES level.

---

## Multi-Cluster Architecture

- **ClusterRouter** (`k8s/cluster_router.go`): Routes client requests to correct cluster based on X-Cluster-ID context. Local → ClientFactory, remote → decrypt stored creds, build rest.Config, impersonate.
- **ClusterContext middleware** (`middleware/cluster.go`): Extracts X-Cluster-ID header, admin gate for non-local.
- **Cluster registry**: PostgreSQL-backed, AES-256-GCM encrypted credentials, SSRF-validated URLs.
- **Remote clusters use direct API calls only** — no informers, no WebSocket events. Local cluster uses informers.
- **ClusterProber** (`k8s/cluster_prober.go`): Background goroutine probes remote clusters every 60s (10s timeout). `POST /clusters/:id/test` for on-demand probing.
- **Known limitation:** AccessChecker queries local cluster RBAC, not remote. Kubernetes API enforces real permissions.

---

## Branching Strategy

GitHub Flow. See `CONTRIBUTING.md` for the complete workflow.

**Branch:** `main` (protected, always deployable) ← short-lived feature branches

**Image tags:** `vX.Y.Z` (release) + `sha-<hash>` (every merge) + `latest` (floating)

**Rules:**
- NEVER commit directly to `main` — all changes via PR
- Feature branches: `feat/description`, `fix/description`, `refactor/description`
- CI + E2E must pass before merge
- On merge to main: images built, tagged, pushed to GHCR (public), GitHub Release created

**After every push:** Watch CI (`gh run list --limit 1` / `gh run view`), review any failures, and fix before moving on. Do not assume CI passes — verify it.

**Before any merge:** Run `/ce:review` first. No exceptions. Smoke test against homelab when backend/frontend changes are in scope.

**After successful merge:** Delete the local and remote feature branch. Use `gh api -X DELETE` if SSH times out. Clean up stale tracking refs with `git branch -dr`.

Credentials: `admin` / `admin123`, setup token: `homelab-setup-token`.

---

## Security Checklist

- [ ] All endpoints require auth (except login, healthz, readyz)
- [ ] All k8s operations use impersonation (never service account)
- [ ] Secrets masked in API responses and audit logs
- [ ] CSRF on all state-changing endpoints (X-Requested-With)
- [ ] Rate limiting on auth endpoints (5/min per IP)
- [ ] Container images non-root, read-only rootfs, distroless, drop ALL capabilities
- [ ] NetworkPolicy restricts pod traffic
- [ ] Audit log captures all writes and secret accesses
- [ ] CSP headers prevent XSS
- [ ] Trivy scans images before GHCR push

---

## Compaction Survival

When compacting this conversation, always preserve:
- Active phase + which units have shipped vs are pending (see "Build Progress" / "Phase 14" below)
- Annotation contracts from Key Conventions (cert-manager + ESO threshold keys, ClusterSecretStore propagation rule)
- Agent Directives 1–10 (Pre-Work, Code Quality, Context Management, Edit Safety) and the Model Routing block
- The current task's modified file paths and any in-flight test results / verification command output
- User preferences expressed in this session (e.g., scope confirmations, explicit "skip X" decisions)

Drop freely: historical Build Progress phase descriptions (1–13 are reference, not active state); Roadmap items already checked off; verbose tool-result transcripts that have been summarised.

---

<!--
=========================================================================
VOLATILE SECTIONS BELOW — these change on every PR merge.
Keep below the static sections so the prompt cache prefix stays warm.
=========================================================================
-->

## Build Progress

**Status:** Web phases 1–14 + Mobile M1 + M2 + M3 are complete. Mobile M4–M5 next per `plans/mobile-app.md`.

### Web (complete)

- **Phases 1–6 — MVP through Frontend Redesign:** Resource CRUD, multi-cluster routing + SSRF protection, RBAC gating, 18 wizard types, E2E suite, Trivy scanning, NetworkPolicy, Grafana dashboard provisioning, RBAC visualization, cost analysis. Phase 6 redesign added 7 dark themes, icon nav rail, Cmd+K palette, dashboard summary endpoint (16→3 calls), batch resource counts (7→1), full theme-token migration.
- **Phase 7 — Observability:** Loki (5 endpoints + WebSocket tail, LogQL namespace enforcement). Topology graph (RBAC-gated, 2000-node cap). Diagnostics (6 rules, blast-radius BFS).
- **Phase 8–9 — Policy + GitOps:** Kyverno + Gatekeeper compliance scoring. Argo CD + Flux CD apps with sync/suspend/rollback, ApplicationSet, Flux Notification CRDs (Provider/Alert/Receiver), diff view, git commit display.
- **Phase 10–13 — Security + Cert-Manager + Service Mesh:** Trivy Operator + Kubescape compliance. Cert-Manager observatory (8 endpoints, 60s expiry poller dedupe-by-`(uid, threshold)`) + Certificate/Issuer wizards (SelfSigned + HTTP01) + per-cert configurable thresholds via annotations (single-source-of-truth `ResolveCertThresholds` + `ApplyThresholds`, per-key source attribution, `ThresholdSource.Valid()` enum guard). Istio + Linkerd: 5 mesh CRD groups, mTLS posture with three-source attribution, golden signals via templated PromQL, `?overlay=mesh` topology edges (fail-closed RBAC).
- **Phase 14 — External Secrets Operator:** `internal/externalsecrets/` package with CRD discovery (5min cache + 30s read cache + singleflight), 9 endpoints under `/externalsecrets/*`, per-user RBAC filtering. Four operational lenses: drift detection (tri-state `InSync`/`Drifted`/`Unknown`, Revert button), persistent sync history (`eso_sync_history` table, 90-day sweep), bulk refresh (`eso_bulk_refresh_jobs`, scope-pinned, 409-on-drift), per-store rate/cost-tier panel. Annotation thresholds (`kubecenter.io/eso-stale-after-minutes` ≥5, `eso-alert-on-{recovery,lifecycle}`) resolve through ES → SecretStore → ClusterSecretStore chain (per-key source attribution; ClusterSecretStore propagates with tenant override). Topology gains `?overlay=eso-chain` (cap 2000, `EdgesTruncated` flag, `Overlay: "unavailable"` when ESO not installed). Wizards: 8 first-class providers (Vault, AWS SM/PS, Azure KV, GCP SM, Kubernetes, Doppler, 1Password) + 11-provider YAML template registry (Phase K — Akeyless, Bitwarden, Conjur, Infisical, Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud, Alibaba KMS, generic webhook) via `/external-secrets/stores/new-from-template?template=<provider>`.

### Mobile (M1 + M2 + M3 complete)

- **M1 — read-only oncall companion:** Flutter 3.41/Dart 3.11 under `mobile/` with Riverpod 2.x, go_router, Dio, `web_socket_channel`, `flutter_secure_storage`, `firebase_messaging`, `data_table_2`, `fl_chart`. Theme parity enforced at build via `make check-themes` (`kube_theme_builder.dart` → Material 3 + `KubeColors` extension). Adaptive shell at 768px (phone drawer / tablet two-pane). Dio interceptor stack (`Cluster`/`CSRF`/`Auth`/`ErrorMapping`) + body-mode `/v1/auth/refresh` with `Completer<bool>` 401 dedupe. Cluster pill + bottom-sheet picker; `activeClusterProvider` invalidates per-cluster providers. 12 specialized resource screens + generic catch-all over `resourceListProvider`/`resourceGetProvider`. Overview/YAML/Events tabs (Events filters by `involvedObject.uid`). Pod log tail via `KubeWebSocketClient` (auth-in-band handshake, capped backoff, 5000-line ring). Notification feed + FCM device registration (silent fall-through when Firebase config absent) + deep links (`k8scenter://` + Universal Link). CI workflow + Fastlane lanes for TestFlight + Play Internal (gated on operator secrets). OIDC mobile deferred (cookie-exchange path doesn't work in in-app browsers).
- **M2 — write actions + YAML editor:** `resource_actions.dart` is a 1:1 Dart port of `frontend/lib/action-handlers.ts` (`ActionId` enum, `actionsByKind`, `actionVerbMap`, `getVisibleActions`, `getActionMeta`, `executeAction`). `permissions.dart` ports `canPerform` with strict cluster-scoped default + opt-in `allowAnyNamespaceFallback`. `confirm_sheet.dart` mirrors `ConfirmDialog.tsx` with type-to-confirm gating (whitespace + ZWSP/ZWNJ/ZWJ/BOM normalized). `resource_actions_button.dart` orchestrates ActionSheet → ScaleSheet/ConfirmSheet → `executeAction` → snackbar → invalidation. **Cluster pinning** at every write site (sheet-open / picker-build / YAML controller construction); mismatch aborts. `_executing` gate prevents double-tap. Per-action timeouts (90s delete, 30s default; 30s `sendTimeout` for multi-MB ConfigMap uploads). `_resourceBase` URL helper omits namespace segment for cluster-scoped resources. Rollback picker at `/clusters/:cluster/workloads/deployments/:ns/:name/rollback` filters owned ReplicaSets by `ownerReferences[*].uid`, sorts newest-first by `kRevisionAnnotation = 'deployment.kubernetes.io/revision'`, current revision tap-disabled. YAML editor (`yaml_apply_controller.dart` + `yaml_editor_panel.dart`): `AutoDisposeFamilyNotifier<YamlApplyState, YamlApplyKey>` state machine; `_post` unwraps `{data: ...}` envelope and dispatches `isApply` (validate returns `{documents, valid}`, apply returns `{results, summary}`). **Secret-data destruction defense:** `stripSensitiveDataFields: true` removes `data`/`stringData` from the editor seed for Secrets so SSA never overwrites real credentials with the backend's `"****"` mask. ConfigMap + Secret opt in via `editableYaml` + `applyKey` on `ResourceDetailScaffold`. 184 tests; `flutter analyze` clean.
- **Mobile invariants worth keeping in mind for M3+:** all writes route through `executeAction` or the YAML controller and pin the active cluster; type-to-confirm sheet exists and supports null-aware fields; web/Dart action maps must stay isomorphic (`frontend/lib/action-handlers.ts` is the parallel-edit target); `data`/`stringData` are masked on Secret GETs so any future Secret-edit path must strip them from the seed before SSA; `_resourceBase` and `resource_repository.dart`'s URL builder are the two places cluster-scoped routing lives.
- **M3 PR-3a — wizard infrastructure + ConfigMap/Secret/Service:** generic `WizardController<TForm>` (AutoDisposeFamilyNotifier on `WizardKey { clusterId, draftId }`) drives a six-state machine (`formEditing → previewing → reviewing → applying → applied | failed`) shared by every wizard. Hardened post-review with: `_dispatchId` race-protection (form edits / back / discardAndReset bump it; in-flight preview/apply checks identity at result-arrival and drops late results), `_disposed` flag via `ref.onDispose` so torn-down notifiers no-op state setters, partial-apply gate (`summary.failed > 0` routes to `failed` not `applied`), cluster-pin re-check at result-arrival (catches mid-flight cluster switches that would otherwise leak preview YAML from the new cluster), `state.unrouted` for 422 paths the per-wizard `errorRouter` doesn't recognize (top-of-step banner instead of silent step-0 merge), `state.clusterMismatch` flag drives a "Discard & restart" footer instead of a Retry that re-fails. `WizardPreviewClient` wraps `POST /v1/wizards/:type/preview`, parses 422 `error.detail` (JSON-encoded array) into typed `List<WizardFieldError>`. Apply hits the existing `/v1/yaml/apply` with explicit `Options(sendTimeout: 60s)` for multi-MB payloads and invalidates `resourceListProvider` on success so the next list visit refetches. `WizardScreenScaffold` accepts a `wizardType` and provides a default `onApplied` derived from the registry (SnackBar + go-to-detail-or-list) so wizards don't duplicate routing. Shared `WizardReviewBody` widget owns the Review step's success card + YAML preview + retry. Drawer's "Create" submenu hides while RBAC is loading (defense against `canPerform(rbac=null,…) → true` window). Wizard routes are RBAC-gated at the route builder via `_WizardRouteGuard` so deep-links can't bypass the drawer's gate. `wizardRegistry` enumerates all 28 routable wizard types from the web frontend (R10 isomorphism); un-built types fall through to `_ComingSoonScreen`. ConfigMap/Secret/Service are 2-step (Configure + Review); Secret obscures KV value rows; Service composes selector + labels + repeating ports table. `KeyValueTable.didUpdateWidget` reconciles controllers in place rather than dispose-and-rebuild, preserving focus when typing into the trailing-sentinel row. 226 tests pass; `flutter analyze` clean repo-wide; `make check-themes` clean.
- **M3 PR-3b — workloads wizards (Deployment, Job, CronJob, DaemonSet, StatefulSet):** five wizards added on top of M3 PR-3a's controller scaffold. Shared widgets — `repeating_row_group.dart` (generic ordered-list with Add button + per-row Remove), `probe_form.dart` (liveness/readiness probe with HTTP/TCP picker; **no `exec` and no startup probe** because backend `ProbeInput` rejects them — surface matches reality so apply never 422s on a handler the form accepted), `resources_form.dart` (CPU/memory request+limit with quantity hints), `container_form_parts.dart` (`EnvVarData` + `ContainerPortData` records mirroring backend `ContainerInput`, plus the row widgets). Wire-format-aware error routing: Deployment uses flat field paths (`image`, `ports[N].containerPort`, `resources.requestCpu`, `probes.liveness.path`) routed across its 4 steps (Basics / Networking / Resources / Review) per the backend's `DeploymentInput` flattening; Job / CronJob / DaemonSet / StatefulSet nest under `container.*` and route everything to their single Configure step. Job + CronJob share `buildJobContainerJson` so the embedded job-template's container payload stays structurally identical; CronJob ships the 6 canonical schedule patterns (`@hourly`, `@daily`, `@weekly`, `@monthly`, `0 */6 * * *`, `*/15 * * * *`) as `ActionChip` rows that overwrite the schedule field. DaemonSet drops replicas (one pod per node) and surfaces `nodeSelector` + `maxUnavailable`. StatefulSet adds required `serviceName` + `volumeClaimTemplates` (RepeatingRowGroup of `_VctRow` with per-index error keys like `volumeClaimTemplates[N].size`) + `OrderedReady`/`Parallel` `podManagementPolicy`. Optional integer fields (Job parallelism/completions/backoffLimit, StatefulSet replicas) treat blank text as "omit field" so backend nil pointers stay nil. 268 tests pass; `flutter analyze` clean repo-wide; `make check-themes` clean.
- **M3 PR-3c — networking, scaling, RBAC, storage class, namespace limits (Ingress, NetworkPolicy, HPA, PDB, RoleBinding, StorageClass, NamespaceLimits):** seven wizards on top of PR-3a + PR-3b infrastructure. Two new shared widgets — `kind_picker.dart` (ChoiceChip row over a fixed list of kinds, used by HPA's `targetKind` and RoleBinding's `roleRef.kind`) and `named_resource_picker.dart` (FutureProvider-backed dropdown over `resourceListProvider(ResourceListKey(clusterId, kind, namespace))` so switching namespaces re-fetches; loading/empty/error states inline). Plus a new `wizards/widgets/section_header.dart` (`WizardSectionHeader`) extracted from the byte-identical `_SectionHeader` private class duplicated across 5 PR-3a/PR-3b screens — PR-3c-new screens adopt it, older PR-3a/3b screens left untouched (out of scope). **Cluster-pinning hardening on the picker path:** the original review caught a real divergence — `ResourceListKey` was keyed on the wizard's pinned cluster, but `Dio`'s `ClusterInterceptor` unconditionally rewrote `X-Cluster-ID` from `activeClusterProvider`, so a mid-wizard cluster switch produced wrong-cluster picker reads under the pinned cache slot. Fix: `ClusterInterceptor` now only injects when the header is absent; `ResourceRepository.list` accepts `clusterIdOverride` and `resourceListProvider` threads `key.clusterId` through. Verified by a widget regression test asserting `X-Cluster-ID == pinnedClusterId`. NetworkPolicy is the most complex: nested `RepeatingRowGroup`s for ingress/egress rules → peers (segmented Pod / Namespace selector / IP block) → ports, with policyTypes checkboxes; quarantine pattern (`policyTypes: [Ingress, Egress]` + empty rule arrays) confirmed by test. Ingress mirrors that pattern at one level deeper — rules → paths nested. HPA composes KindPicker + NamedResourcePicker for `scaleTargetRef`, `OptionalIntField` for minReplicas (blank means omit), and a metrics RepeatingRowGroup with auto-switching label ("Target % utilization" vs "Average value") based on `targetType`. PDB enforces minAvailable/maxUnavailable mutual exclusion at the form layer via a `SegmentedButton<PdbPolicy>` — the backend's "both set is rejected" rule is unreachable from the wizard. RoleBinding's subject form hides the namespace input for User/Group and emits `namespace: ""` regardless of operator entry, so the YAML preview matches what the cluster will actually store; ServiceAccount subjects require namespace and surface a per-row inline error if blank. StorageClass is the lone cluster-scoped wizard in this PR — no namespace input — and verifies that `WizardScreenScaffold._defaultOnApplied` falls through to the kind list when `outcome.firstResultNamespace` is null. NamespaceLimits emits two-doc YAML (`---`-separated ResourceQuota + LimitRange); `apply` already handled multi-doc via the `summary.failed > 0` partial-apply gate added in PR-3a, so the controller needed nothing extra. Optional NamespaceLimits fields (PVC limits, GPU quota, configurable warn/critical thresholds) are deferred to a follow-up — operators with that need have the YAML editor. NamespaceLimits also overrides a new `WizardController.extraResourceListKinds` extension point so apply success invalidates both `resourcequotas` and `limitranges` list caches in one go. NetworkPolicy hardens against silent-drop UX: peers with empty CIDR or empty selectors fail `validateLocally` instead of vanishing from the preview body; rule-level errors surface aggregated under the rules group rather than reading a hardcoded `[0]` index; ipBlock except-CIDR errors render under the textarea; the textarea now splits on `[\n,]` instead of `\n`-only so comma-pasted CIDRs from kubectl examples don't decay into a single bogus value. Ingress strips empty TLS rows from the body (and validates partial TLS rows pre-preview) so an accidentally-added empty TLS section never 422s the operator. `WizardController.validateNameAndNamespace` helper centralizes the "Name + Namespace required" copy across all 7 new wizards. 304 tests pass; `flutter analyze` clean repo-wide; `make check-themes` clean.
- **M3 PR-3d — storage / backup family (PVC, Snapshot, ScheduledSnapshot, RestoreSnapshot, VeleroBackup, VeleroRestore, VeleroSchedule):** seven wizards on top of PR-3a/b/c infrastructure. Three new shared widgets — `duration_input.dart` (Velero/Go duration parser with `validateDuration` accepting `30m`, `24h`, `7d`, decimals, and chained units like `1h30m`; empty is OK because every callsite is optional), `multi_namespace_picker.dart` (`FilterChip` row over `resourceListProvider(kind: 'namespaces')`; takes `disabledNamespaces` so the included/excluded pair can mutually disable each other and prevent overlap at the widget level), and `list_picker_screen.dart` (generic `radio_button_*`-styled list picker over any kind/ns; bounded by `maxHeight` so the wizard's Next button stays reachable; copied M2's `RollbackPickerScreen` shape rather than refactoring the original to keep blast radius contained — M5 polish revisits). PVC backend wire is `{name, namespace, storageClassName, size, accessMode, dataSource?}` — single accessMode string, NOT an array (the plan's "multi-checkbox" hint pre-dated reading `pvc.go`; R10 follows backend reality). RestoreSnapshot is a UX wrapper over the `pvc` backend wizard type — it pre-populates `dataSource: {name, kind: 'VolumeSnapshot', apiGroup: 'snapshot.storage.k8s.io'}` and lands a PVC bound to the snapshot. Registered as its own `restore-snapshot` entry in `wizard_registry.dart` (RBAC-checks `create persistentvolumeclaims`). ScheduledSnapshot is the lone 3-step wizard (Source & Schedule / Class & Retention / Review) and lands a 4-doc multi-resource YAML (ServiceAccount + Role + RoleBinding + CronJob); `WizardController`'s existing `summary.failed > 0` partial-apply gate handles per-doc failures. VeleroBackup + VeleroSchedule share the same scope-and-template surface — Schedule's Backup template fields are inlined as a second step rather than nested under `template:` in the form, because backend `VeleroScheduleInput` flattens those fields at the top level (mirrors `velero.go:187`). VeleroRestore uses `named_resource_picker` (kind=`backups`, scoped to the form's Velero namespace) for the `backupName` selector — the plan's "list_picker_screen" line was reconciled in implementation: NamedResourcePicker fits this scope-bounded picker, ListPickerScreen ships only for RestoreSnapshot's snapshot picker. Velero `includedNamespaces`/`excludedNamespaces` overlap is caught client-side via Set intersection in `validateLocally` and via `disabledNamespaces` on the picker so the operator can't reach a 422 by toggling the same namespace into both lists. Velero TTL accepts empty (Velero default) and `0s` (no expiry) per Velero's own duration grammar. **Post-`/ce:review` fixes:** ScheduledSnapshot's `kCronPresets[0]` was emitting `@hourly`, which the backend's strict 5-field `cronRegex` (`backend/internal/wizard/container.go:20`) rejects — replaced with `0 * * * *`; VeleroSchedule keeps `@hourly` because its backend uses `cron.ParseStandard` which accepts `@`-shorthand. VeleroRestore's `mappingAsMap` was silently dropping rows with one of key/value blank (the same UX-trap PR-3c fixed for NetworkPolicy peers); `validateLocally` now rejects half-filled rows with a typed `namespaceMapping` error, and `mappingAsMap` only drops fully-empty sentinel rows. ScheduledSnapshot's registry entry was gating on `kind: 'schedules'` (the Velero CRD plural — wrong verb) — gated on `cronjobs` instead since the wizard's user-perceivable resource is the CronJob (the SA/Role/RoleBinding are housekeeping). The `PvcDataSource.copyDataSource` extension in `restore_snapshot_wizard_controller.dart` was a no-op wrapper — inlined to `PvcDataSource(name: form.sourceSnapshot).toJson()`. **Pre-existing PR-3a inheritance fix:** the wizard preview/apply path was missing the explicit `X-Cluster-ID` header that PR-3c added to the picker path; `WizardPreviewClient.preview` now requires a `clusterId:` parameter and threads it as a request header, and `WizardController._postYamlApply` does the same. With explicit header pinning, the post-emission cluster-pin check is now informational rather than defensive — `_clusterStillPinned` takes a `_PinPhase` parameter so the pre-emission ("aborted to avoid mutating the wrong cluster") and post-emission ("the request landed on the pinned cluster — re-open the wizard from that cluster to confirm") messages reflect what actually happened. 346 tests pass; `flutter analyze` clean repo-wide; `make check-themes` clean.
- **M3 PR-3e (partial — cert-manager triplet) — Certificate, Issuer, ClusterIssuer:** three CRD wizards on top of M3 PR-3a/c/d infrastructure, plus one new shared widget. `issuer_picker.dart` is a combined Issuer + ClusterIssuer dropdown — fetches `/api/v1/certificates/issuers` and `/api/v1/certificates/clusterissuers` in parallel via `issuerListProvider` (FutureProvider.family keyed on `(clusterId, namespace)`), filters namespaced issuers client-side to the wizard's active namespace (cert-manager rejects cross-namespace Issuer refs anyway, so the cross-namespace entries would only mislead the operator), distinguishes scope per-row via icon + trailing kind label, and pins both fetches via `X-Cluster-ID` so a mid-flight active-cluster switch can't redirect the read. Emits a typed `IssuerSelection { name, kind }` so the Certificate wizard can drive both `issuerRef.name` and `issuerRef.kind` from a single tap. **Certificate wizard:** one Configure + Review. Body shape mirrors backend `CertificateInput` (`backend/internal/wizard/certificate.go:38`) — `name`, `namespace`, `secretName`, `issuerRef: {name, kind}`, optional `dnsNames` / `commonName` / `duration` / `renewBefore`, plus `privateKey: {algorithm, size?}`. Algorithm/size coupling lives in the screen: switching algorithm resets size to `kCertDefaultKeySize[alg]` so an Ed25519 selection emits `{algorithm: 'Ed25519'}` without a stray size that the backend would 422 on. `validateLocally` enforces backend invariant "at least one of dnsNames or commonName is required" so the operator gets an inline message before the round-trip. **Issuer / ClusterIssuer wizard:** one shared screen and one shared `_IssuerWizardBase` controller class with two thin concrete subclasses (`IssuerWizardController` → `wizardType: 'issuer'`, `WizardScope.namespaced`; `ClusterIssuerWizardController` → `wizardType: 'cluster-issuer'`, `WizardScope.cluster`). Two providers because `AutoDisposeNotifierProvider.family` can't carry constructor args. Three steps (Type / Configure / Review) — Type ChoiceChip picks SelfSigned vs ACME, Configure renders name + namespace (namespace-scoped only) + per-type body. SelfSigned emits `{selfSigned: {}}` per backend's "exactly one body must match type" rule. ACME form ships server preset radios for Let's Encrypt prod + staging plus an editable URL field for custom servers, an email, a `privateKeySecretRefName`, and an HTTP01-only solver `RepeatingRowGroup` with optional ingress class — DNS01 is intentionally absent because the backend rejects it (see `issuer.go:142`). `validateNameAndNamespace(requireNamespace: scope != cluster)` keeps the cluster-scope form from blocking on a missing namespace. Backend Issuer wizard registers two distinct preview routes (`/wizards/issuer/preview` + `/wizards/cluster-issuer/preview`) because `IssuerInput.Scope` is `json:"-"` and the route is authoritative — mobile mirrors that by sending to whichever path the controller's `wizardType` resolves. `errorRouter` puts `type` / `selfSigned` on step 0 and `name` / `namespace` / `acme.*` on step 1; an unknown server-introduced field path surfaces via `state.unrouted` instead of silently merging into step 0. **Routes wired** at `/clusters/:clusterId/wizards/{certificate,issuer,cluster-issuer}/new`; the registry entries already shipped in PR-3a so `_WizardRouteGuard` and the drawer's RBAC gating just start showing the entries once the routes return real screens. **Scope of this PR:** cert-manager triplet only. ExternalSecret, SecretStore (12 providers + scope variant), Policy (engine auto-detect + template registry) are deliberately deferred to follow-up PRs — operator-facing R8 cert-manager demo (Certificate signed by an existing ClusterIssuer) is reachable end-to-end as of this PR. 365 tests pass (was 346 — 4 issuer_picker + 7 certificate + 8 issuer); `flutter analyze` clean repo-wide; `make check-themes` clean.
- **M3 PR-3f — completes M3 with the deferred PR-3e CRD wizards (ExternalSecret, SecretStore / ClusterSecretStore, Policy):** four wizard types and three new shared widgets land on top of PR-3e infrastructure. `store_picker.dart` is the SecretStore equivalent of `issuer_picker.dart` — combined namespaced + cluster store dropdown, fetches `/api/v1/externalsecrets/stores?namespace=` and `/api/v1/externalsecrets/clusterstores` in parallel via `storeListProvider` (FutureProvider.family keyed on `(clusterId, namespace)`), pins via `X-Cluster-ID`, and surfaces the store's provider id as a trailing hint so operators don't have to remember which store maps to which backend. Selection emits a typed `StoreSelection { name, kind }`. `provider_picker.dart` renders the 8 web-supported providers (vault, aws, awsps, gcpsm, azurekv, kubernetes, doppler, onepassword) as a popular-first list of radio tiles with descriptions; the 3 backend-recognized but web-deferred providers (bitwardensecretsmanager, conjur, infisical) are intentionally omitted because the backend wizard rejects them anyway — operators reach those via the YAML editor. `policy_templates.dart` is the Dart port of `frontend/lib/policy-templates.ts` — 8 templates across 4 categories with typed `PolicyParamField` records driving the wizard's generic param-form rendering (boolean → SwitchListTile, string → TextField, stringList → repeating rows). **ExternalSecret wizard:** one Configure + Review. Body shape mirrors backend `ExternalSecretInput` (`backend/internal/wizard/externalsecret.go:59`) — `name`, `namespace`, `storeRef: {name, kind}`, `targetSecretName`, optional `refreshInterval`, `data: [{secretKey, remoteRef: {key, property?}}]`. `dataFrom` is intentionally not in the form (web doesn't surface it either; reachable via YAML editor). `validateLocally` enforces "at least one populated data row" + per-row "secretKey + remoteRef.key both required" so half-filled rows surface inline before the round-trip. `toPreviewBody` drops fully-empty sentinel rows (matches the NetworkPolicy peer pattern from PR-3c). **SecretStore / ClusterSecretStore wizard:** scope-variant pattern lifted from Issuer — one shared `SecretStoreWizardBase` (public this time so the screen can call `switchProvider`/`updateProviderSpec` through the static type) with two thin concrete subclasses. Four steps (Identity / Provider / Configure / Review). Provider switch resets `providerSpec: {}` so stale fields can't leak. Each per-provider form (`vault_provider_form.dart` … `onepassword_provider_form.dart`) is a stateless `Widget Function(ProviderFormProps props)` that reads the untyped `Map<String, dynamic>` spec and emits via `props.onUpdateSpec`; per-method auth pickers (Vault: token/kubernetes/appRole/jwt/cert; AWS/AWSPS: jwt-IRSA/secretRef-static; GCPSM: workloadIdentity/secretRef; Doppler: secretRef/oidcConfig) clear the auth slate on switch — mirrors web's `setMethod` exactly. Backend per-provider validators emit field paths bare (e.g. `auth.kubernetes.role`, not `providerSpec.auth.kubernetes.role`); the controller's `errorRouter` routes top-level paths (`name`, `namespace`, `refreshInterval`, `scope`) to step 0, `provider`/`providerSpec` to step 1, and everything else to step 2 — so a vault `auth.appRole.roleId` 422 lands inline under the operator's RoleID input. **Policy wizard:** three steps (Template / Configure / Review). Engine auto-detect runs once at wizard open via `policyEngineStatusProvider` (FutureProvider.family keyed on clusterId, hits `GET /v1/policies/status`); when neither Kyverno nor Gatekeeper is detected, the Template step renders an EmptyState in place of the picker — apply path is unreachable in that state because `validateLocally` on step 0 gates "Pick a template". When both engines are installed, the operator picks one on step 1 via a ChoiceChip row that auto-defaults `action` to that engine's default (Audit/Enforce for Kyverno, deny/dryrun/warn for Gatekeeper). Template tile shows severity badge (high/medium/low color-coded). `pickTemplate` auto-fills name (from id), targetKinds (from template), description (from template), params (from template field defaults) and the action default. Per-template params render generically by walking `paramFields`. Required-param gates (registries / labels stringList non-empty) fire inline before the round-trip, mirroring the backend's `validateParams`. **Routes wired** at `/clusters/:clusterId/wizards/{external-secret,secret-store,cluster-secret-store,policy}/new`. Registry entries already shipped in PR-3a, so `_WizardRouteGuard` and the drawer's RBAC gating just start showing the entries once the routes return real screens. **Scope of this PR:** completes M3. All 28 web wizards now have mobile screens (no more `_ComingSoonScreen` branches for registered types). Operator-facing R8 ExternalSecret + Vault demo (Certificate + ClusterIssuer was already R8 of PR-3e) reachable end-to-end. **Test coverage shipped with this PR:** ExternalSecret controller (5 tests in `external_secret_wizard_test.dart`) plus the SecretStore controller / per-provider form / Policy controller / store_picker / provider_picker test files added by the post-`/ce:review` follow-up. **Post-`/ce:review` fixes (anchor 75+ findings):** `pickTemplate(availableEngines:)` intersects template-supported engines with cluster-installed engines, eliminating the silent Kyverno-default on Gatekeeper-only clusters; SecretStore `errorRouter` special-cases bare `namespace` to step 2 when `provider == 'vault'` and the form's k8s namespace is non-empty (Vault Enterprise namespace collision); SecretStore step 2 `validateLocally` gates on empty `providerSpec` so the operator can't reach Review with a blank spec; ExternalSecret `validateLocally` gates duplicate `secretKey` rows inline (backend rejects them anyway); `storeListProvider` per-fetch error catch surfaces partial results when one of the two ESO endpoints is down (with an inline warning banner); StorePicker renders a "no longer visible" warning when the selected store is missing from the loaded lists; PolicyWizard `_StatusError` ships a Retry button; PolicyWizard Configure step renders a banner when `pickableEngines.isEmpty && form.templateId.isNotEmpty` (catches engine-flip mid-wizard). `flutter analyze` clean repo-wide; `make check-themes` clean.

---

## Roadmap

Priority order from 2026-04-09 brainstorm. Check off each item as its PR merges to main.

- [x] **1. Notification Center** — in-app feed + Slack/email/webhook channels, rule-based dispatch, aggregated across alerts/policy/GitOps/diagnostics (PR #162)
- [x] **2. Git commit display** — Git provider API integration for commit messages in GitOps revision history (PR #155)
- [x] **3. Diff view** — compare manifests between GitOps revisions (PR #156)
- [x] **4. Resource Quota & LimitRange Management** — namespace quota wizards, utilization vs. quota visualization, overage warnings (PR #164)
- [x] **5. Backup & Restore (Velero)** — schedule backups, browse snapshots, one-click restore
- [x] **6. Service Mesh Observability (Istio/Linkerd)** — traffic routing, mTLS posture, golden signals, topology overlay (Phase 12)
- [x] **7. Cert-Manager integration** — inventory, expiry warnings, issuer management (Phase 11A)
- [x] **7b. Cert-Manager wizards (Phase 11B)** — Certificate/Issuer/ClusterIssuer creation (PR #180, follow-ups #181–#183)
- [x] **7c. Cert-Manager configurable expiry thresholds** — per-cert/per-issuer annotation overrides (Phase 13)
- [x] **8. External Secrets Operator integration** — observatory + per-provider wizards + chain topology overlay + drift detection + persistent sync history + bulk refresh + per-store rate/cost-tier panel (Phase 14)
- [ ] **9. k8sCenter Mobile App (Flutter, iOS + Android)** — full-parity native app shipped over five milestones. **M1 (read-only oncall companion) complete** as of PR-1g — auth + cluster context + 12 specialized resource screens + generic detail + log tail + notification feed + FCM + custom-scheme deep links + CI/release pipeline + Universal Link Helm template. M2 (writes), M3 (wizards), M4 (advanced observability), M5 (polish + public store launch) remain. OIDC mobile flow tracked as a follow-up — cookie-exchange path doesn't work on mobile in-app browsers; backend body-mode endpoint warrants its own PR. See `plans/mobile-app.md` and `plans/mobile-app-m1-pr-sequence.md`.
- [ ] **10. Saved Views & Custom Dashboards** *(deferred behind the mobile app)* — per-user persistence for filter presets, pinned favorites, arrangeable dashboard widgets.
  - **Why**: today every visit to `/workloads/pods` re-applies the default sort + filter set. Power users running a dozen tabs across namespaces re-create the same scopes by hand.
  - **Likely shape**:
    - **Phase A — Persistence**: PostgreSQL `user_preferences` table; new `internal/preferences/` package with typed CRUD over `SavedView`, `PinnedResource`, `DashboardLayout`.
    - **Phase B — API**: `GET/POST/PUT/DELETE /preferences/{views,pins,dashboards}`, RBAC-personal, audit-logged.
    - **Phase C — Frontend**: ResourceTable "Save view" affordance, sidebar "Pinned" section, dashboard layout config.
  - **Open questions**: cross-cluster vs per-cluster scoping, team-shared views, dashboard widget catalog.

#9 and #10 should both start with `/ce:brainstorm` before `/ce:plan` for product-shape framing.
