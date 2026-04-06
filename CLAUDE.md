# CLAUDE.md — k8sCenter: Kubernetes Management Platform

# Agent Directives: Mechanical Overrides

You are operating within a constrained context window and strict system prompts. To produce production-grade code, you MUST adhere to these overrides:

## Pre-Work

1. THE "STEP 0" RULE: Dead code accelerates context compaction. Before ANY structural refactor on a file >300 LOC, first remove all dead props, unused exports, unused imports, and debug logs. Commit this cleanup separately before starting the real work.

2. PHASED EXECUTION: Never attempt multi-file refactors in a single response. Break work into explicit phases. Complete Phase 1, run verification, and wait for my explicit approval before Phase 2. Each phase must touch no more than 5 files.

## Code Quality

3. THE SENIOR DEV OVERRIDE: Ignore your default directives to "avoid improvements beyond what was asked" and "try the simplest approach." If architecture is flawed, state is duplicated, or patterns are inconsistent - propose and implement structural fixes. Ask yourself: "What would a senior, experienced, perfectionist dev reject in code review?" Fix all of it.

4. FORCED VERIFICATION: Your internal tools mark file writes as successful even if the code does not compile. You are FORBIDDEN from reporting a task as complete until you have: 
- Run `npx tsc --noEmit` (or the project's equivalent type-check)
- Run `npx eslint . --quiet` (if configured)
- Fixed ALL resulting errors

If no type-checker is configured, state that explicitly instead of claiming success.

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
│       ├── diagnostics/      # Diagnostic rules engine, blast radius BFS, resolver
│       ├── loki/             # Loki discovery, LogQL proxy, namespace enforcement, WebSocket tail
│       ├── policy/           # Kyverno + Gatekeeper discovery, adapters, compliance scoring
│       ├── monitoring/       # Prometheus/Grafana discovery, PromQL proxy, dashboard provisioning
│       ├── topology/         # Resource dependency graph builder, health propagation, RBAC
│       ├── networking/       # CNI detection, Cilium, Hubble gRPC client
│       ├── alerting/         # Alertmanager webhook, SMTP notifier, rules
│       ├── storage/          # CSI/StorageClass handler, snapshots
│       ├── wizard/           # 17 wizard input types (generic WizardInput → YAML pipeline)
│       ├── yaml/             # YAML validate, apply (SSA), diff, export
│       ├── audit/            # PostgreSQL audit logger
│       └── websocket/        # Hub + Client (fan-out, RBAC revalidation)
├── frontend/                 # Deno 2.x + Fresh 2.x
│   ├── routes/               # File-system routing (50+ pages)
│   ├── islands/              # 57 interactive islands (ResourceTable, wizards, etc.)
│   ├── components/           # UI components, wizard steps, k8s detail overviews
│   └── lib/                  # API client, auth, WebSocket, constants, hooks
├── helm/kubecenter/          # Helm chart
│   ├── templates/            # Deployments, services, NetworkPolicy, monitoring ConfigMaps
│   └── dashboards/           # 7 Grafana dashboard JSONs (synced with backend embed)
├── e2e/                      # Playwright E2E tests (Node.js project, 95 tests)
├── plans/                    # Implementation plans (per-step markdown)
└── .github/workflows/        # ci.yml (lint/test/build/Trivy), e2e.yml (Playwright + kind)
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

### Frontend (Deno/Fresh)
- **Islands architecture strictly enforced.** Only interactive components are islands. Everything else is SSR HTML.
- **All API calls through `lib/api.ts`.** Handles auth token injection, error parsing, X-Cluster-ID header.
- **Wizard pattern:** WizardStepper shell → steps → YAML preview → server-side apply.
- **Tailwind CSS utility-only.** No custom CSS class names.

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
- Wizard previews: `POST /wizards/:type/preview` (17 wizard types)
- YAML tools: `POST /yaml/{validate,apply,diff,export}`
- Monitoring: `GET /monitoring/{status,query,query_range,dashboards}`, `GET /monitoring/grafana/proxy/*`
- Logs (Loki): `GET /logs/{status,query,labels,labels/:name/values,volume}` (RBAC namespace-scoped)
- Topology: `GET /topology/{namespace}` (RBAC-gated resource dependency graph with health)
- Diagnostics: `GET /diagnostics/{ns}/{kind}/{name}`, `GET /diagnostics/{ns}/summary` (automated checks + blast radius)
- Policy: `GET /policy/{status,policies,violations,compliance}` (Kyverno + Gatekeeper, RBAC-filtered)
- Dashboard: `GET /cluster/dashboard-summary` (aggregated counts + utilization, RBAC-filtered)
- Counts: `GET /resources/counts[?namespace=]` (batch resource counts from informer cache, RBAC-filtered)
- Multi-cluster: `GET/POST/DELETE /clusters`
- WebSocket: `/ws/{resources,logs/:ns/:pod/:container,exec/:ns/:pod/:container,alerts,flows,logs-search}`

**Auth flow:** `POST /auth/login` → JWT access token + httpOnly refresh cookie → `POST /auth/refresh` on 401.

**CSRF:** All state-changing endpoints require `X-Requested-With: XMLHttpRequest` header.

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

### Response Format
```json
{ "data": {...}, "metadata": {"total": 42} }
{ "error": {"code": 403, "message": "...", "detail": "..."} }
```

---

## Build Progress

- **Phase 1 (MVP):** COMPLETE — Steps 1-15
- **Phase 2 (Multi-Cluster):** COMPLETE — Steps 16-23
- **Phase 3 (Enhancements):** COMPLETE — 7 items (Pod Exec, User Mgmt, Cilium, Hubble, CSP, Alerts WS, RBAC gating)
- **Phase 4 (Wizards):** COMPLETE — 4A-4D (17 wizard types total)
- **Phase 5 (Production Polish):** COMPLETE — Steps 24-30
  - Step 24: E2E Tests (95 tests, Playwright)
  - Step 25: Production Hardening (Trivy, automaxprocs, probes, NetworkPolicy)
  - Step 26: UX Polish (breadcrumbs, owner refs, toast cleanup)
  - Step 27: Grafana Dashboards (7 JSONs, Helm ConfigMap, provision-once)
  - Step 28: Multi-Cluster UX (routing, health probing, SSRF protection)
  - Step 29: RBAC Visualization (relationship table, cross-links, effective permissions)
  - Step 30: Cost Analysis (utilization cards, resource display, request-vs-actual)
- **Phase 6 (Frontend Redesign):** COMPLETE — 14 tasks
  - Theme system: 7 named dark themes (Nexus, Dracula, Tokyo Night, Catppuccin, Nord, One Dark, Gruvbox)
  - Navigation: 56px icon rail replacing 240px sidebar, 8 domain sections
  - Dashboard-first: Health score ring, metric cards, utilization gauges, cluster topology
  - Command Palette: Cmd+K fuzzy search across resources and actions
  - Split Pane: Resizable side-by-side resource views
  - Quick Actions FAB: Floating action button for common operations
  - Sub-navigation tabs with live resource counts per domain
  - Typography: Geist Sans (UI) + Geist Mono (data/code)
  - 174 files migrated from Tailwind dark: classes to CSS custom properties
- **Phase 6B (API Optimization):** COMPLETE — 4 tasks
  - Dashboard summary endpoint: `GET /cluster/dashboard-summary` (16 API calls → 3)
  - Batch resource counts: `GET /resources/counts` (7 SubNav calls → 1)
  - Theme FOUC fix: CSS `[data-theme]` attribute selectors for instant theme on page load
  - Health score simplified: removed meaningless services sub-score (always 100%)
  - RBAC-filtered: both endpoints check per-resource permissions, return partial responses
  - UtilizationProvider interface decouples resources from monitoring package
  - Async Prometheus with 1s timeout via sync.WaitGroup (never blocks informer data)
- **Phase 6C (Design Normalization):** COMPLETE
  - 100+ hardcoded Tailwind color classes replaced with CSS custom property tokens across 40+ files
  - Dashboard heading styles unified from inline styles to Tailwind classes
  - Zero non-theme color classes remain in frontend codebase
- **Phase 7 (Advanced Observability):** COMPLETE — 3 sub-phases (7A-7C)
  - **Phase 7A (Loki Integration):** COMPLETE
    - New `internal/loki/` package: service discovery, HTTP client, LogQL namespace enforcement tokenizer
    - 5 HTTP endpoints: `/logs/status`, `/logs/query`, `/logs/labels`, `/logs/labels/{name}/values`, `/logs/volume`
    - WebSocket `/ws/logs-search` for Loki tail streaming (Pattern B direct pipe)
    - 5 frontend islands: LogFilterBar, LogResults, LogLiveTail, LogVolumeHistogram, LogExplorer
    - Route: `/observability/logs` with Loki availability check and graceful degradation
    - 19 security tests for LogQL namespace enforcement
    - Observability nav section updated with new tabs (Log Explorer, Topology, Investigate)
  - **Phase 7B (Topology Graph):** COMPLETE
    - New `internal/topology/` package: ResourceLister interface, graph builder with RBAC, health propagation
    - 1 HTTP endpoint: `GET /topology/{namespace}` (RBAC-gated, rate-limited, 2000-node cap)
    - NamespaceTopology island: custom LR layout, SVG viewBox zoom/pan, health coloring, slide-out panel
    - Route: `/observability/topology`
  - **Phase 7C (Diagnostics):** COMPLETE
    - New `internal/diagnostics/` package: 6 diagnostic rules, blast radius BFS, resolver
    - 2 HTTP endpoints: `GET /diagnostics/{ns}/{kind}/{name}`, `GET /diagnostics/{ns}/summary`
    - 3 frontend islands: DiagnosticChecklist, BlastRadiusPanel, DiagnosticWorkspace
    - Route: `/observability/investigate` with URL-driven resource picker
    - "Investigate" entry points: resource detail pages, command palette
    - 8 unit tests covering all 6 rules
- **Phase 8 (Policy & Governance):** COMPLETE — 2 sub-phases (8A-8B)
  - **Phase 8A (Policy Backend):** COMPLETE
    - New `internal/policy/` package: PolicyDiscoverer (CRD-based auto-detection of Kyverno + Gatekeeper)
    - Kyverno adapter: ClusterPolicy, Policy, PolicyReport reading via dynamic client
    - Gatekeeper adapter: ConstraintTemplate + dynamic constraint enumeration (semaphore(5), 5s timeout, 100 cap)
    - Unified types: NormalizedPolicy, NormalizedViolation with Blocking field, composite IDs
    - Handler: singleflight + 30s cache (service account fetch, per-user RBAC filtering), inline compliance scoring
    - 4 HTTP endpoints: `GET /policy/{status,policies,violations,compliance}`
    - Extended `AccessChecker.CanAccessGroupResource` for CRD RBAC checks
  - **Phase 8B (Policy Frontend):** COMPLETE
    - 3 islands: PolicyDashboard (engine status, policy table), ViolationBrowser (violation table, resource links), ComplianceDashboard (GaugeRing score, severity bars, per-namespace table)
    - 4 routes: `/security/{index,policies,violations,compliance}` (index redirects to policies)
    - Shared modules: `lib/policy-types.ts` (TS interfaces), `components/ui/PolicyBadges.tsx` (ColorBadge, SeverityBadge, EngineBadge, BlockingBadge, ActionBadge)
    - Nav: 3 tabs in Security section, 2 command palette quick actions
    - Theme-compliant: CSS custom properties for all colors (var(--success), var(--accent))

---

## Multi-Cluster Architecture

- **ClusterRouter** (`k8s/cluster_router.go`): Routes client requests to correct cluster based on X-Cluster-ID context. Local → ClientFactory, remote → decrypt stored creds, build rest.Config, impersonate.
- **ClusterContext middleware** (`middleware/cluster.go`): Extracts X-Cluster-ID header, admin gate for non-local.
- **Cluster registry**: PostgreSQL-backed, AES-256-GCM encrypted credentials, SSRF-validated URLs.
- **Remote clusters use direct API calls only** — no informers, no WebSocket events. Local cluster uses informers.
- **ClusterProber** (`k8s/cluster_prober.go`): Background goroutine probes remote clusters every 60s (10s timeout). Connection tested before registration. `POST /clusters/:id/test` for on-demand probing.
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

**Before any merge:** Run `/compounding-engineering:workflows:review` first. No exceptions. Smoke test against homelab when backend/frontend changes are in scope.

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
