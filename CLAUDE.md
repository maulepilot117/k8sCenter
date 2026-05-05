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

All foundational phases (1–13) are COMPLETE. High-level inventory:

- **Phase 1–5 (MVP → Production Polish):** Resource CRUD, multi-cluster, RBAC gating, 18 wizard types, E2E tests (95), Trivy scanning, NetworkPolicy, breadcrumbs, Grafana dashboard provisioning, multi-cluster routing + SSRF protection, RBAC visualization, cost analysis.
- **Phase 6 (Frontend Redesign + 6B/6C optimization):** 7 dark themes, 56px icon nav rail, dashboard-first layout, Cmd+K command palette, split pane, FAB, Geist Sans/Mono. Dashboard summary endpoint (16 calls → 3), batch resource counts (7 calls → 1), 100+ hardcoded color classes replaced with theme tokens.
- **Phase 7 (Advanced Observability):** Loki integration (5 endpoints + WebSocket tail, LogQL namespace enforcement), Topology graph (RBAC-gated, 2000-node cap, custom LR layout), Diagnostics (6 rules, blast radius BFS, 3 islands).
- **Phase 8 (Policy & Governance):** Kyverno + Gatekeeper discovery, 4 endpoints, dashboards (engine status, violations, compliance scoring with GaugeRing).
- **Phase 9 (GitOps):** Argo CD + Flux CD discovery, applications list/detail, sync/suspend/rollback actions, ApplicationSet support, Flux Notification Controller (Provider/Alert/Receiver CRUD), real-time WS updates, diff view, git commit display.
- **Phase 10 (Security Scanning):** Trivy Operator + Kubescape (vuln reports, config audits, compliance frameworks).
- **Phase 11A (Cert-Manager Observatory):** 8 endpoints, expiry poller (60s tick, dedupe by `(uid, threshold)`), 3 islands, renew/reissue actions.
- **Phase 11B (Cert-Manager Wizards):** Certificate/Issuer/ClusterIssuer wizards (v1: SelfSigned + HTTP01 ACME; CA/Vault/DNS01 deferred to YAML editor).
- **Phase 12 (Service Mesh — Istio/Linkerd):** Inventory (5 mesh CRD groups), mTLS posture with three-source attribution (`policy`/`metric`/`default`), golden signals (RPS, error rate, p50/p95/p99 via templated PromQL), topology overlay (`?overlay=mesh` adds `mesh_vs`/`mesh_sp` edges, fail-closed RBAC, themed edges), inline `MeshGoldenSignals` on Service detail.
- **Phase 13 (Cert-Manager Configurable Thresholds):** Per-cert/issuer warn/critical days via annotations; `ResolveCertThresholds` + `ApplyThresholds` is single source of truth (handler + poller); response includes per-key source attribution; `ThresholdSource.Valid()` + `sanitizeSource` guards against out-of-enum strings.

### Phase 14 (External Secrets Operator) — IN PROGRESS

Status (2026-05-05): Phases A → B → D → C → E → F → G → H shipped (through PR #217). Phases I → J pending.

Authoritative plan + per-phase technical detail: `plans/external-secrets-operator-integration.md`. Read that file before starting any Phase 14 work — annotation thresholds, cross-tenant suppression, and persistence schema are all spelled out there.

Pending units: I (Chain topology overlay), J (additional polish/docs flip).

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
- [ ] **8. External Secrets Operator integration** — observatory + actions (Phase 14, in progress; see above).
- [ ] **9. Saved Views & Custom Dashboards** — per-user persistence for filter presets, pinned favorites, arrangeable dashboard widgets.
  - **Why**: today every visit to `/workloads/pods` re-applies the default sort + filter set. Power users running a dozen tabs across namespaces re-create the same scopes by hand.
  - **Likely shape**:
    - **Phase A — Persistence**: PostgreSQL `user_preferences` table; new `internal/preferences/` package with typed CRUD over `SavedView`, `PinnedResource`, `DashboardLayout`.
    - **Phase B — API**: `GET/POST/PUT/DELETE /preferences/{views,pins,dashboards}`, RBAC-personal, audit-logged.
    - **Phase C — Frontend**: ResourceTable "Save view" affordance, sidebar "Pinned" section, dashboard layout config.
  - **Open questions**: cross-cluster vs per-cluster scoping, team-shared views, dashboard widget catalog.

Both #8 (already in motion) and #9 should start with `/ce:brainstorm` before `/ce:plan` for product-shape framing.
