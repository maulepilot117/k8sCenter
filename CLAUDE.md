# CLAUDE.md — k8sCenter: Kubernetes Management Platform

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
│       ├── monitoring/       # Prometheus/Grafana discovery, PromQL proxy, dashboard provisioning
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
- Multi-cluster: `GET/POST/DELETE /clusters`
- WebSocket: `/ws/{resources,logs/:ns/:pod/:container,exec/:ns/:pod/:container,alerts,flows}`

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
  - Deferred: Backend summary endpoints for API call deduplication, FOUC fix for non-default themes

---

## Multi-Cluster Architecture

- **ClusterRouter** (`k8s/cluster_router.go`): Routes client requests to correct cluster based on X-Cluster-ID context. Local → ClientFactory, remote → decrypt stored creds, build rest.Config, impersonate.
- **ClusterContext middleware** (`middleware/cluster.go`): Extracts X-Cluster-ID header, admin gate for non-local.
- **Cluster registry**: PostgreSQL-backed, AES-256-GCM encrypted credentials, SSRF-validated URLs.
- **Remote clusters use direct API calls only** — no informers, no WebSocket events. Local cluster uses informers.
- **ClusterProber** (`k8s/cluster_prober.go`): Background goroutine probes remote clusters every 60s (10s timeout). Connection tested before registration. `POST /clusters/:id/test` for on-demand probing.
- **Known limitation:** AccessChecker queries local cluster RBAC, not remote. Kubernetes API enforces real permissions.

---

## Pre-Merge Requirements

**Every PR requires `/review` before merge.** Smoke test against homelab when backend/frontend changes are in scope. See `docs/homelab-deployment.md` and memory file `reference_homelab_deployment.md` for procedures.

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
