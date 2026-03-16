# feat: k8sCenter Phase 1 MVP — Full Implementation Plan

## Overview

k8sCenter is a web-based Kubernetes management platform delivering vCenter-level functionality for Kubernetes clusters. This plan covers the complete Phase 1 MVP: a working single-cluster management UI with GUI wizards, real-time updates, monitoring integration, and RBAC-aware multi-tenancy.

This plan incorporates research findings on Go 1.26 patterns, Fresh 2.x breaking changes, and 62 specification gaps identified during analysis.

---

## Progress Tracker

| Step | Name | Status | PR | Notes |
|------|------|--------|-----|-------|
| 1 | Backend Skeleton | Done | #1 | HTTP server, chi router, k8s client factory, informer manager (20 resource types), Helm chart skeleton, CI pipeline, health/readiness probes |
| 2 | Auth System | Done | #2 | JWT (HMAC-SHA256, 15min access + 7day refresh), local auth (Argon2id), RBAC (SelfSubjectRulesReview), audit interface, middleware (auth, CSRF, rate limiting, CORS). 14 review findings fixed, 9 deferred (todos 015-023) |
| 3 | Resource Listing | Done | #3 | Informer-backed CRUD for 15 resource types, RBAC access checks, pagination, validation, secret masking, node cordon/drain with task tracking. Fix PRs: #11 (secret annotation leak), #13 (rate limit bucket) |
| 4 | Frontend Skeleton | Done | #4 | Fresh 2.x app with Vite + Tailwind v4, login page, dashboard, sidebar navigation, BFF proxy with SSRF protection, WS proxy, security headers (CSP, X-Frame-Options) |
| 5 | Resource Browser | Done | #5 | ResourceTable island with WebSocket live updates, search, sort, pagination. Column definitions for all 15 resource types. Status color mapping |
| 6 | Resource Detail | Done | #6 | Tabbed detail views (Overview, YAML, Events, Metrics placeholder) for all 18 resource types. ResourceDetail island with live WS updates. Fix PRs: #12 (Monaco CSP) |
| 7 | YAML Apply | Done | #7 | Monaco editor island, server-side apply (SSA), validate (dry-run), diff, export. Multi-document YAML support. Clean YAML export (strips managed fields) |
| 8 | Resource Creation Wizards | Done | #14 | Deployment wizard (container config, env vars, volumes, probes, strategy) and Service wizard (ClusterIP/NodePort/LoadBalancer). WizardStepper shell. YAML preview before apply |
| 9 | Monitoring Integration | Done | #15 | Prometheus/Grafana auto-discovery, PromQL proxy (instant + range), Grafana reverse proxy with dashboard embedding, 12 pre-built PromQL templates, resource→dashboard mapping |
| 10 | CSI/CNI Wizards | Done | #16 | CSI driver discovery + StorageClass wizard with driver-specific presets. CNI detection (Cilium/Calico/Flannel) with config management. VolumeSnapshot support |
| 11 | Alerting | Done | #17 | Alertmanager webhook receiver, in-memory alert store with pruner, SMTP email notifier with rate limiting, PrometheusRule CRD CRUD (SSA), real-time alert banner via WebSocket, alert settings UI |
| 12 | OIDC/LDAP Auth | Done | #18 | OIDC (PKCE + state + nonce, configurable claims mapping, email domain filtering). LDAP (bind+search, injection prevention, group mapping). Provider registry, multi-provider login/refresh, auth settings UI with test connection. 15 review findings: 13 fixed, 2 deferred (todos 192, 194) |
| 13 | Helm Chart — Production | Done | #23 | Frontend deployment/service, ingress, auto-generated secrets (JWT survives upgrades), ConfigMap, NetworkPolicy (frontend→backend only, conditional LDAP), values.schema.json. Reviewed: removed PDB (no-op) and PVC (premature), tightened NetworkPolicy egress, moved SMTP creds to Secret |
| 14 | Audit Logging (~~SQLite~~ PostgreSQL) | Done | #24, #29 | SQLiteLogger swaps SlogLogger via same interface. Pure Go SQLite (modernc.org/sqlite, no CGO). Paginated query API with filters, daily retention cleanup, WAL mode. Dual-write (SQLite + slog). Frontend audit viewer with filters/pagination. Reviewed: RetentionDays validation, Queryable interface, WAL autocheckpoint |
| 15 | Polish | Done | #28 | Dark mode toggle (localStorage, no flash), loading skeletons, toast notifications, keyboard shortcuts (? help, / search), branding fix (KubeCenter→k8sCenter). Also resolved all 60 deferred code review findings (6 P1, 21 P2, 39 P3): JWT jti, RBAC cache bound, sentinel errors, admin-only alert routes, pagination cursor fix, config struct dedup, shared UI components (Logo, Alert, Spinner, RemoveButton, KeyValueListEditor), wizard constants, port validation, click-outside handler, WS reconnect limit |

---

## Problem Statement

Kubernetes operators need a GUI-based management tool that bridges the gap between `kubectl` and enterprise platforms like vCenter. Existing tools (Rancher, Lens, Headlamp) either require external infrastructure, lack wizard-driven workflows, or don't provide integrated monitoring. k8sCenter deploys inside the managed cluster via Helm and provides a complete management experience.

---

## Proposed Solution

Build the MVP in 15 ordered steps, grouped into 4 implementation phases. Each phase produces a working, testable increment.

---

## CLAUDE.md Corrections Required

Research revealed several corrections needed in CLAUDE.md before implementation begins:

| CLAUDE.md Entry | Correction |
|---|---|
| `fresh.config.ts` in project structure | Remove — does not exist in Fresh 2 |
| `tailwind.config.ts` in project structure | Remove — Tailwind v4 uses CSS-first config via `@theme` in CSS |
| Missing `vite.config.ts` | Add to frontend root |
| Missing `client.ts` | Add to frontend root (client-side entry point) |
| `"jsx": "react-jsx"` in deno.json | Change to `"jsx": "precompile"` for Fresh 2 SSR performance |
| `https://esm.sh/` and `https://deno.land/x/` imports | Use `npm:` and `jsr:` specifiers instead |
| `$fresh/` import prefix | Fresh 2 uses `fresh` from JSR `@fresh/core` |
| No `_error.tsx` mentioned | Add — replaces separate `_404` and `_500` pages |
| Handler patterns `(req, ctx)` | Update to single-parameter `(ctx)` pattern in Fresh 2 |
| `GET /api/v1/nodes/:name/drain` | Change to `POST` — GET is semantically wrong for a destructive operation |
| `POST /api/v1/auth/oidc/callback` | Change to `GET` — OAuth2 redirect sends GET with query params |
| `POST /api/v1/yaml/export/...` | Change to `GET` — export is a read operation |

---

## Specification Gaps — Decisions Required Before Implementation

### Critical Decisions (Block Day-1 Implementation)

**D1. First Admin Account Bootstrap**
- **Decision:** `POST /api/v1/setup/init` endpoint active only when zero users exist. Accepts `{username, password}`, creates admin account, deactivates permanently. Helm values optionally seed credentials via env vars for automated installs.
- **Rationale:** Most secure option. No passwords in values.yaml. Works for both manual and CI/CD installs.

**D2. Token Storage Model**
- **Decision:** Access token in memory (JS variable). Refresh token as `httpOnly; Secure; SameSite=Strict` cookie. CSRF protection via `X-Requested-With` header check on `/api/v1/auth/refresh`.
- **Rationale:** Follows OWASP recommendations. Memory-only access tokens are immune to XSS theft. httpOnly cookies are immune to JS access.

**D3. WebSocket Authentication**
- **Decision:** Client sends `{"type": "auth", "token": "<jwt>"}` as the first message after WS connection. Backend validates and rejects all subscriptions until authenticated. Connection closes after 5s if no auth message received.
- **Rationale:** Avoids query-string tokens (logged by proxies). Simpler than ticket-based approach.

**D4. Multi-Document YAML Apply Semantics**
- **Decision:** Best-effort, document-by-document. Each document's result (success/failure with error) is reported individually. Frontend shows a per-document status table after apply.
- **Rationale:** Kubernetes SSA is not transactional across resources. Pretending otherwise creates false expectations.

**D5. Persistent Storage Backend**
- **Decision:** SQLite with a PVC for audit logs and alert history. Helm chart includes an optional PVC template. For environments that forbid PVCs, fall back to in-memory with structured log output (export to external log aggregator).
- **Rationale:** ConfigMaps have 1MB limit, unsuitable for audit logs. SQLite is operationally simple and requires no external database.

### Missing API Endpoints (Add to Spec)

| Endpoint | Purpose |
|---|---|
| `POST /api/v1/setup/init` | First-run admin creation |
| `GET /api/v1/auth/oidc/:providerID/login` | OIDC initiation redirect |
| `GET /api/v1/auth/oidc/:providerID/callback` | OIDC callback (was POST) |
| `POST /api/v1/yaml/preview` | Wizard YAML generation from structured payload |
| `GET /api/v1/secrets/:ns/:name/values/:key` | Secret reveal (audit-logged) |
| `POST /api/v1/alerts/webhook` | Alertmanager webhook receiver (HMAC-authenticated) |
| `POST /api/v1/nodes/:name/drain` | Node drain (was GET) |
| `PATCH /api/v1/nodes/:name/labels` | Node label management |
| `PATCH /api/v1/nodes/:name/taints` | Node taint management |

---

## Technical Approach

### Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                     │
│                                                          │
│  ┌──────────────┐     ┌──────────────────┐              │
│  │   Frontend    │────▶│     Backend      │              │
│  │  Deno/Fresh   │     │    Go 1.26       │              │
│  │  Port 8000    │     │    Port 8080     │              │
│  └──────────────┘     └────────┬─────────┘              │
│                                │                         │
│                    ┌───────────┼───────────┐             │
│                    │           │           │              │
│               ┌────▼───┐ ┌────▼───┐ ┌────▼────┐        │
│               │  K8s   │ │ Prom   │ │ Grafana │        │
│               │  API   │ │ etheus │ │         │        │
│               └────────┘ └────────┘ └─────────┘        │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

**Go 1.26 features we leverage:**
- **Green Tea GC** (default) — 10-40% less GC overhead, critical for informer-heavy workloads
- **`slog.NewMultiHandler()`** — log to stdout + audit store simultaneously
- **`errors.AsType[T]()`** — generic error handling for k8s API error mapping
- **`io.ReadAll` 2x faster** — benefits YAML processing pipeline
- **`ReverseProxy.Rewrite`** — replaces deprecated `.Director` for Grafana proxy (Step 9)
- **`os/signal.NotifyContext` with cause** — shutdown logging shows which signal triggered it
- **Goroutine leak profile** (experimental) — debug WebSocket connection leaks during development
- **`new(expr)` syntax** — cleaner optional field initialization in API types
- **`reflect` iterators** (`.Fields()`) — generic k8s resource handling

**Key architecture decisions:**
- Backend impersonates users for all k8s API calls (RBAC enforcement)
- Informers provide cached reads with per-request RBAC filtering; direct API calls for writes
- WebSocket hub fans out informer events to subscribed clients (RBAC checked at subscription time)
- Fresh islands architecture minimizes client-side JS
- Grafana embedded via backend auth proxy (iframe)
- Single port (8080) for all endpoints including health checks
- Helm skeleton from Step 1 for in-cluster testing from day one
- CI pipeline from Step 1 (lint, test, build on every push)

### Git Strategy

One branch per step, each merging into `main` via PR.

```
main                              # Always deployable, protected
├── feat/step-1-backend-skeleton
├── feat/step-2-auth
├── feat/step-3-resource-listing
├── feat/step-4-frontend-skeleton
├── feat/step-5-resource-browser
├── feat/step-6-resource-detail
├── feat/step-7-yaml-apply
├── feat/step-8-wizards
├── feat/step-9-monitoring
├── feat/step-10-csi-cni
├── feat/step-11-alerting
├── feat/step-12-oidc-ldap
├── feat/step-13-helm-hardening
├── feat/step-14-audit-persistence
└── feat/step-15-polish
```

**Commit convention:** `feat(scope): description` — scope matches component (`backend`, `frontend`, `helm`, `auth`, `k8s`, `monitoring`, etc.)

**Workflow:** Steps merge sequentially since each depends on its predecessor. Each PR represents a working increment.

### Implementation Phases

---

## Phase A: Foundation (Steps 1-4)

> Goal: A running backend + frontend that can authenticate users and display cluster state.

### Step 1: Backend Skeleton

**Files to create:**

```
backend/
├── go.mod
├── go.sum
├── cmd/kubecenter/main.go
├── internal/
│   ├── server/
│   │   ├── server.go
│   │   ├── routes.go
│   │   └── middleware/
│   │       └── cors.go
│   ├── k8s/
│   │   ├── client.go
│   │   └── informers.go
│   └── config/
│       ├── config.go
│       └── defaults.go
├── pkg/
│   └── version/
│       └── version.go
├── Dockerfile
└── Makefile

helm/kubecenter/          # Skeleton Helm chart (production hardening in Step 13)
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── deployment-backend.yaml
│   ├── service-backend.yaml
│   ├── serviceaccount.yaml
│   ├── clusterrole.yaml
│   └── clusterrolebinding.yaml

.github/workflows/
└── ci.yml                # Lint, test, build on every push
```

**Implementation details:**

`backend/cmd/kubecenter/main.go`:
- Load config via koanf (env vars + optional YAML file)
- Initialize k8s client (in-cluster with kubeconfig fallback for dev)
- Start SharedInformerFactory, wait for cache sync
- Start chi HTTP server with middleware chain
- Graceful shutdown via `signal.NotifyContext` (SIGTERM/SIGINT)
- Readiness probe fails during shutdown (atomic.Bool flag)

`backend/internal/server/server.go`:
- Chi middleware chain order: RequestID → RealIP → slog-chi logger → Recoverer → CleanPath → Timeout(60s) → CORS
- CORS configuration: dev mode allows `localhost:8000` (Fresh dev server). Production mode allows only the configured ingress host (from config/Helm values). Credentials allowed (for httpOnly cookie).
- All endpoints on single port `:8080` including `/healthz` and `/readyz` (unprotected by auth middleware)
- `/healthz` — trivial liveness check (server can respond = alive)
- `/readyz` — readiness check (informer caches synced, returns 503 during shutdown)
- API routes under `/api/v1`

`backend/internal/k8s/client.go`:
- `NewClientFactory(config)` — creates base `rest.Config` from in-cluster or kubeconfig
- `ClientForUser(username, groups)` — returns impersonating clientset
- Impersonating client cache: `sync.Map` keyed by `hash(username+groups)` with 5-minute TTL to avoid repeated TLS handshakes
- `ClusterID` parameter on all operations (defaults to `"local"`)

`backend/internal/k8s/informers.go`:
- `NewInformerManager(clientset)` — starts SharedInformerFactory
- Informers for: Pods, Deployments, StatefulSets, DaemonSets, Services, Ingresses, ConfigMaps, Secrets, Namespaces, Nodes, PVCs, Jobs, CronJobs, NetworkPolicies, Events
- `WaitForSync(ctx)` — blocks until all caches are synced or context cancels

`backend/internal/config/config.go`:
- Config struct with koanf tags
- `KUBECENTER_` env var prefix
- Fields: `Server.Port`, `Server.TLSCert`, `Server.TLSKey`, `Dev` (bool), `Log.Level`, `Log.Format`
- Validation at startup — fail fast with clear errors

`backend/internal/config/defaults.go`:
- Sensible defaults: port 8080, log level info, JSON format

**Helm skeleton chart:**
- Minimal chart with just Deployment, Service, ServiceAccount, ClusterRole, ClusterRoleBinding
- Enables in-cluster testing from day one (impersonation, service account permissions)
- Production hardening (NetworkPolicy, PDB, security contexts, monitoring templates) deferred to Step 13

**CI pipeline (`.github/workflows/ci.yml`):**
- Trigger on push and PR
- Jobs: `go vet`, `golangci-lint`, `go test ./... -race -cover`, `go build`
- Frontend jobs added in Step 4: `deno lint`, `deno fmt --check`, `deno test`
- Helm jobs added in Step 13: `helm lint`, `helm template`

**Acceptance criteria:**
- [x] `go build ./cmd/kubecenter` compiles
- [x] Server starts, connects to k8s (kind cluster in dev)
- [x] `/healthz` returns 200 immediately
- [x] `/readyz` returns 200 after informer sync, 503 before
- [x] `GET /api/v1/cluster/info` returns cluster version and node count
- [x] Graceful shutdown completes within 30s
- [x] Structured JSON logs via slog with request IDs
- [x] `make build-backend`, `make test-backend`, `make lint` all pass
- [x] Helm skeleton deploys to kind cluster: `helm install kubecenter ./helm/kubecenter`
- [x] CI pipeline runs on push and passes

---

### Step 2: Auth System

**Files to create:**

```
backend/internal/
├── auth/
│   ├── provider.go
│   ├── local.go
│   ├── jwt.go
│   ├── rbac.go
│   └── session.go
├── server/middleware/
│   ├── auth.go
│   └── ratelimit.go
```

**Implementation details:**

`backend/internal/auth/provider.go`:
```go
type AuthProvider interface {
    Authenticate(ctx context.Context, credentials Credentials) (*User, error)
    Type() string
}

type User struct {
    ID                   string
    Username             string
    KubernetesUsername   string
    KubernetesGroups    []string
    Roles               []string
}
```

`backend/internal/auth/local.go`:
- Argon2id password hashing (golang.org/x/crypto)
- User store backed by k8s Secret in the k8sCenter namespace (known limitation: 1MB Secret size limit caps user count; migration path to SQLite documented for scale)
- `POST /api/v1/setup/init` — creates first admin when no users exist
  - Hardened: rate limited to 1 request per 10 seconds
  - Returns 410 (Gone) when any user exists (does not leak endpoint existence via 403)
  - All attempts logged at WARN level
  - Optional: Helm values can provide a `setupToken` that must be included in the request body for automated installs
- `POST /api/v1/auth/login` — validates credentials, returns JWT

`backend/internal/auth/jwt.go`:
- 15-minute access tokens (signed with HMAC-SHA256)
- 7-day refresh tokens stored server-side (k8s Secret)
- `POST /api/v1/auth/refresh` — validates httpOnly cookie, issues new access token AND new refresh token (rotation — old refresh token invalidated on use, limits damage window if stolen)
- CSRF check: `X-Requested-With: XMLHttpRequest` header required on ALL state-changing endpoints (POST/PUT/PATCH/DELETE), enforced globally in auth middleware

`backend/internal/auth/rbac.go`:
- `GET /api/v1/auth/me` — returns user info + RBAC summary
- RBAC summary via `SelfSubjectAccessReview` for key resource types
- Response format:
```json
{
  "user": { "username": "chris", "groups": ["developers"] },
  "rbac": {
    "clusterScoped": { "nodes": ["get", "list"] },
    "namespaces": {
      "default": { "pods": ["get", "list", "create", "delete"], "deployments": ["get", "list"] }
    }
  }
}
```

`backend/internal/server/middleware/auth.go`:
- Extract Bearer token from Authorization header
- Validate JWT signature and expiry
- Inject authenticated user into request context
- Skip auth for: `/healthz`, `/readyz`, `/api/v1/auth/login`, `/api/v1/auth/refresh`, `/api/v1/setup/init`, `/api/v1/auth/providers`

`backend/internal/server/middleware/ratelimit.go`:
- Token bucket per IP for auth endpoints
- 5 attempts/min for login
- Return 429 with `Retry-After` header when exceeded

**Scaffold audit middleware (log to stdout until step 14):**

`backend/internal/audit/logger.go`:
- Define `AuditLogger` interface with `Log(AuditEntry) error` method upfront — Step 14 is a drop-in implementation swap, not a refactor
- `AuditEntry` struct: `Timestamp`, `ClusterID`, `User`, `SourceIP`, `Action`, `ResourceKind`, `ResourceNamespace`, `ResourceName`, `Result`, `Detail`
- Initial implementation: `SlogAuditLogger` that writes structured JSON to slog
- Log all write operations (POST/PUT/PATCH/DELETE)
- Persistence layer swapped to SQLite in step 14

**Acceptance criteria:**
- [x] `POST /api/v1/setup/init` creates first admin (only when no users exist)
- [x] `POST /api/v1/auth/login` returns JWT access token
- [x] Refresh token set as httpOnly cookie
- [x] `POST /api/v1/auth/refresh` issues new access token (requires CSRF header)
- [x] Auth middleware rejects requests without valid JWT
- [x] Rate limiter blocks after 5 failed login attempts per minute per IP
- [x] `GET /api/v1/auth/me` returns user info with RBAC summary
- [x] Audit middleware logs all write operations to stdout
- [x] All endpoints except auth and health require authentication

**Implementation notes (deviations from plan):**
- Auth middleware uses chi route groups instead of skip-path list (cleaner, see todo 007)
- RBAC uses `SelfSubjectRulesReview` (1 call/namespace) instead of per-resource `SelfSubjectAccessReview` (see todo 004)
- Argon2id concurrency limited to 3 simultaneous hashes (~64MB each) to prevent OOM
- User store is in-memory (not k8s Secret) — persistence comes in Step 14
- Refresh tokens use single-use rotation with automatic cleanup goroutine
- `Provider` field added to User, JWT claims, and sessions for OIDC readiness
- Handler files split by domain: `handle_auth.go`, `handle_setup.go`, `handle_cluster.go`, `handle_health.go`, `response.go`
- 14 code review findings fixed across 3 commits; 9 follow-up findings deferred as todos 015-023

---

### Step 3: Resource Listing

**Files to create:**

```
backend/internal/
├── k8s/
│   ├── impersonation.go
│   ├── rbac.go
│   └── resources/
│       ├── handler.go
│       ├── lister.go
│       ├── deployments.go
│       ├── statefulsets.go
│       ├── daemonsets.go
│       ├── pods.go
│       ├── services.go
│       ├── ingresses.go
│       ├── namespaces.go
│       ├── nodes.go
│       ├── configmaps.go
│       ├── secrets.go
│       ├── pvcs.go
│       ├── jobs.go
│       ├── networkpolicies.go
│       └── rbac_viewer.go
├── server/routes.go (updated)
```

**Implementation details:**

`backend/internal/k8s/impersonation.go`:
- Extract user from request context
- Create impersonating client for write operations (cached via `sync.Map` with 5-min TTL)
- Use informer cache for read operations WITH RBAC filtering (see `rbac.go`)

`backend/internal/k8s/rbac.go`:
- **Critical: Informer reads must be RBAC-filtered.** The informer cache uses the service account's broad read permissions, but users should only see resources they have access to.
- On first request per user per resource-kind per namespace: perform a `SelfSubjectAccessReview` to check GET permission. Cache the result for 60 seconds per user.
- Namespace selector: filter to only namespaces the user can `list` (checked via `SelfSubjectAccessReview`, cached per user for 60s)
- This prevents a namespace-scoped user from seeing Secrets in namespaces they don't have access to.

`backend/internal/k8s/resources/handler.go`:
- HTTP handler wiring, response formatting, error mapping
- Consistent URL pattern: `/api/v1/resources/:kind/:namespace/:name` for all resources
- Specialized sub-resource endpoints: `/api/v1/resources/deployments/:namespace/:name/scale` (consistent `:namespace` naming, never `:ns`)

`backend/internal/k8s/resources/lister.go`:
- Generic list/get logic using informer cache with RBAC filtering
- Pagination via `continue` token for k8s resources
- Label selector and field selector support
- Response format: `{ "data": [...], "metadata": { "total", "continue" } }`

Resource-specific handlers add type-safe operations:
- `deployments.go`: List, Get, Create, Update, Delete, Scale, Rollback, Restart
- `statefulsets.go`: List, Get, Create, Update, Delete, Scale
- `daemonsets.go`: List, Get, Create, Update, Delete
- `pods.go`: List, Get, Delete (logs and exec in later steps)
- `services.go`: List, Get, Create, Update, Delete
- `ingresses.go`: List, Get, Create, Update, Delete
- `jobs.go`: List, Get, Create, Delete (Jobs + CronJobs)
- `networkpolicies.go`: List, Get, Create, Update, Delete
- `secrets.go`: List (masked values), Get (masked), Reveal (audit-logged)
- `nodes.go`: List, Get, Cordon, Uncordon, Drain (POST, long-running — see below)
- `rbac_viewer.go`: List Roles, ClusterRoles, Bindings (read-only)

**Long-running operations (node drain):**
- `POST /api/v1/resources/nodes/:name/drain` returns `202 Accepted` with a task ID
- Client polls `GET /api/v1/tasks/:id` for status (progress, completion, error)
- Default timeout: 5 minutes. Request body accepts `{ ignoreDaemonSets, deleteEmptyDirData, timeout }`
- Per-endpoint timeout override: drain uses 5min, not the default 60s middleware timeout

**Secrets masking:**
- All GET/LIST responses replace secret `data` values with `"****"`
- `GET /api/v1/secrets/:ns/:name/values/:key` returns plaintext + creates audit entry

**Acceptance criteria:**
- [ ] `GET /api/v1/resources/pods/default` returns pods in default namespace from informer cache
- [ ] `GET /api/v1/resources/deployments` returns deployments across all namespaces
- [ ] Pagination works with `?limit=20&continue=<token>`
- [ ] Label selector filtering: `?labelSelector=app=nginx`
- [ ] Secret values are masked (`****`) in list and get responses
- [ ] Secret reveal endpoint returns plaintext and logs audit entry
- [ ] All write operations use impersonating client
- [ ] RBAC denied returns 403 with user-friendly message
- [ ] k8s API errors mapped to appropriate HTTP status codes

---

### Step 4: Frontend Skeleton

**Files to create:**

```
frontend/
├── deno.json
├── deno.lock
├── main.ts
├── client.ts
├── vite.config.ts
├── static/
│   ├── favicon.ico
│   ├── logo.svg
│   └── styles/
│       └── global.css
├── routes/
│   ├── _app.tsx
│   ├── _layout.tsx
│   ├── _error.tsx
│   ├── _middleware.ts
│   ├── index.tsx
│   ├── login.tsx
│   └── api/
│       └── [...path].ts
├── islands/
│   ├── Sidebar.tsx
│   └── TopBar.tsx
├── components/
│   ├── ui/
│   │   ├── Button.tsx
│   │   ├── Input.tsx
│   │   ├── Card.tsx
│   │   ├── Badge.tsx
│   │   └── Toast.tsx
│   ├── layout/
│   │   ├── PageHeader.tsx
│   │   └── EmptyState.tsx
│   └── k8s/
│       ├── StatusBadge.tsx
│       └── ResourceIcon.tsx
├── lib/
│   ├── api.ts
│   ├── ws.ts
│   ├── auth.ts
│   ├── k8s-types.ts
│   ├── formatters.ts
│   └── constants.ts
└── Dockerfile
```

**Implementation details:**

`frontend/deno.json`:
```json
{
  "compilerOptions": {
    "jsx": "precompile",
    "jsxImportSource": "preact",
    "strict": true
  },
  "nodeModulesDir": "auto",
  "imports": {
    "fresh": "jsr:@fresh/core@^2.0.0-alpha",
    "@fresh/plugin-vite": "jsr:@fresh/plugin-vite@^2.0.0-alpha",
    "preact": "npm:preact@^10.26.6",
    "@preact/signals": "npm:@preact/signals@^2.x",
    "@/": "./"
  },
  "tasks": {
    "dev": "vite",
    "build": "vite build",
    "preview": "deno serve -A _fresh/server.js",
    "lint": "deno lint",
    "fmt": "deno fmt",
    "test": "deno test -A"
  }
}
```

`frontend/static/styles/global.css`:
```css
@import "tailwindcss";

@theme {
  --color-success: #22c55e;
  --color-warning: #f59e0b;
  --color-danger: #ef4444;
  --color-info: #3b82f6;
  --color-surface: #ffffff;
  --color-surface-dark: #1e293b;
}
```

`frontend/lib/api.ts`:
- Typed fetch wrapper with Bearer token injection
- Automatic 401 detection → silent refresh via `/api/v1/auth/refresh`
- Queued refresh to handle concurrent 401s (single refresh, replay all)
- Error parsing into typed `APIError`
- `X-Cluster-ID: local` header on all requests

`frontend/lib/auth.ts`:
- Access token stored in module-level variable (memory only)
- Login: POST to backend, store access token, redirect to dashboard
- Logout: POST to backend, clear token, redirect to login
- `useAuth()` hook: returns `{ user, isAuthenticated, login, logout }`

`frontend/lib/ws.ts`:
- WebSocket client with auth handshake (first message = JWT)
- Subscribe/unsubscribe to resource types + namespaces
- Exponential backoff reconnection (1s → 2s → 4s → ... → 30s max)
- User-visible "Reconnecting..." indicator
- On reconnect: re-subscribe and trigger full data reload

`frontend/routes/api/[...path].ts`:
- Catch-all BFF proxy to Go backend
- Forwards all headers including Authorization
- Streams response body for SSE/large responses

`frontend/islands/Sidebar.tsx`:
- vCenter-style resource tree navigation
- Sections: Cluster, Workloads, Networking, Storage, Config, Monitoring, YAML
- Collapsible groups, active state highlighting
- Links to resource list pages

`frontend/islands/TopBar.tsx`:
- Namespace selector dropdown (populated from `/api/v1/cluster/namespaces`)
- Cluster indicator (showing "local" with disabled multi-cluster selector)
- User menu (username, logout)
- `X-Cluster-ID: local` header included

**Acceptance criteria:**
- [ ] `deno task dev` starts the Fresh dev server
- [ ] Login page renders at `/login`
- [ ] Login flow works: credentials → JWT → redirect to dashboard
- [ ] Dashboard shows cluster overview (node count, pod count, namespace count)
- [ ] Sidebar navigation links work
- [ ] Namespace selector populates from API
- [ ] API proxy (`routes/api/[...path].ts`) forwards to Go backend
- [ ] Token refresh works silently on 401
- [ ] Logout clears session and redirects to login
- [ ] Dark mode follows OS preference
- [ ] EmptyState component renders when no data

---

## Phase B: Core Features (Steps 5-8)

> Goal: Full resource browsing, detail views, YAML editing, and creation wizards.

### Step 5: Resource Browser

**Files to create:**
```
backend/internal/
├── websocket/
│   ├── hub.go
│   ├── client.go
│   └── events.go

frontend/
├── islands/
│   ├── ResourceTable.tsx
│   └── EventStream.tsx
├── components/
│   └── ui/
│       ├── DataTable.tsx
│       └── Pagination.tsx
├── routes/
│   ├── workloads/
│   │   ├── index.tsx
│   │   ├── deployments/index.tsx
│   │   └── pods/index.tsx
│   ├── networking/
│   │   └── services/index.tsx
│   └── cluster/
│       ├── index.tsx
│       ├── nodes/index.tsx
│       └── events.tsx
```

**Implementation details:**

`backend/internal/websocket/hub.go`:
- Central hub goroutine — receives events from informer event handlers, fans out to subscribers
- Per-client subscriptions (kind + namespace)
- **RBAC checked at subscription time** (not per-event): when a client subscribes to `pods` in namespace `production`, perform a `SelfSubjectAccessReview` once. Cache the result for the session. Re-check on reconnect. Trade-off: stale RBAC (user loses permission but receives events for up to 60s until re-check) vs. API server load.
- Auth: first message must be `{"type": "auth", "token": "<jwt>"}`, connection closed after 5s if not received

`backend/internal/websocket/client.go`:
- Per-connection client with auth state, subscriptions, write pump
- Handles subscribe/unsubscribe messages

`backend/internal/websocket/events.go`:
- Event types: resource ADDED/MODIFIED/DELETED, log stream, alert notification

`frontend/islands/ResourceTable.tsx`:
- Generic sortable/filterable table for any k8s resource
- Props: `kind`, `namespace`, `columns[]`, `actions[]`
- WebSocket subscription for real-time updates (add/modify/delete animations)
- Sort state preserved in URL query string (`?sort=name&order=asc`)
- Filter/search bar with label selector support
- Action buttons visibility based on RBAC summary from `/auth/me`
- Loading skeleton during initial data fetch
- EmptyState when no resources match

`frontend/islands/EventStream.tsx`:
- Live cluster event feed
- WebSocket subscription to events
- Color-coded by event type (Normal=info, Warning=warning)
- Auto-scroll with pause on user scroll-up

**Acceptance criteria:**
- [ ] Deployment list shows all deployments with status, replicas, age
- [ ] Pod list shows pods with phase, restarts, node, age
- [ ] Service list shows services with type, cluster IP, ports
- [ ] Node list shows nodes with status, roles, CPU/memory capacity
- [ ] Tables sort by clicking column headers
- [ ] Tables filter by search text and label selector
- [ ] WebSocket delivers real-time updates (create a pod, see it appear)
- [ ] Namespace selector filters all tables
- [ ] Pagination works for large result sets
- [ ] URL preserves sort/filter state across navigation

---

### Step 6: Resource Detail

**Files to create:**
```
frontend/
├── islands/
│   ├── ResourceDetail.tsx
│   ├── LogViewer.tsx
│   └── TerminalEmbed.tsx
├── components/
│   └── ui/
│       ├── Tabs.tsx
│       ├── Modal.tsx
│       └── Tooltip.tsx
├── routes/
│   ├── workloads/
│   │   ├── deployments/[ns]/[name].tsx
│   │   └── pods/[ns]/[name].tsx
│   ├── networking/
│   │   └── services/[ns]/[name].tsx
│   └── cluster/
│       └── nodes/[name].tsx
```

**Backend additions:**
```
backend/internal/
└── k8s/resources/pods.go (add logs + exec WebSocket endpoints)
```

**Implementation details:**

Resource detail pages have tabbed views:
- **Overview**: Key metadata, status, conditions, labels, annotations
- **YAML**: Read-only YAML view (Monaco editor, read-only mode)
- **Events**: Filtered events for this resource
- **Metrics**: Placeholder (filled in step 9)

Pod-specific tabs:
- **Logs**: LogViewer island with container selector, follow toggle, search, download
- **Terminal**: TerminalEmbed island with xterm.js, shell selector (bash/sh)

Note: The WebSocket hub was built in Step 5. This step adds pod-specific WebSocket endpoints (log streaming, exec) that connect through the same hub.

`frontend/islands/LogViewer.tsx`:
- WebSocket connection to `/api/v1/ws/logs/:ns/:pod/:container`
- Ring buffer: 10,000 lines max in DOM
- Auto-scroll with "Paused" indicator on scroll-up
- Search/filter within rendered logs
- "Previous container" toggle for crashed pods
- Download button (exports visible logs as text file)

`frontend/islands/TerminalEmbed.tsx`:
- xterm.js terminal emulator
- WebSocket connection to `/api/v1/ws/exec/:ns/:pod/:container`
- Terminal resize events sent to backend
- Container selector for multi-container pods
- Audit: session start/end logged (not individual commands)

**Acceptance criteria:**
- [ ] Deployment detail shows replicas, strategy, conditions, pod template
- [ ] Pod detail shows containers, volumes, conditions, events
- [ ] YAML tab renders formatted YAML with syntax highlighting
- [ ] Events tab shows events filtered to the specific resource
- [ ] Pod log streaming works in real-time
- [ ] Pod exec terminal works (type command, see output)
- [ ] Container selector works for multi-container pods
- [ ] WebSocket reconnects on disconnection with backoff
- [ ] Delete button shows confirmation modal, deletes resource
- [ ] Node detail shows pods running on it, labels, taints

---

### Step 7: YAML Apply

**Files to create:**
```
backend/internal/yaml/
├── parser.go
├── validator.go
├── applier.go
└── differ.go

frontend/
├── islands/
│   ├── YamlEditor.tsx
│   └── YamlDiffViewer.tsx
├── routes/yaml/
│   ├── apply.tsx
│   └── editor.tsx
```

**Implementation details:**

`backend/internal/yaml/parser.go`:
- Multi-document YAML splitting (`---` separator)
- Parse each document into `unstructured.Unstructured`
- Max file size: 1MB
- Accepted formats: YAML, JSON

`backend/internal/yaml/validator.go`:
- Validate against cluster's OpenAPI schema (from API server discovery)
- Return per-field validation errors with line numbers

`backend/internal/yaml/applier.go`:
- Server-side apply via `PATCH` with `application/apply-patch+yaml`
- Field manager: `kubecenter`
- Per-document results: `{ documents: [{ kind, namespace, name, status, error? }] }`
- Uses impersonating client

`backend/internal/yaml/differ.go`:
- Dry-run apply (`dryRun: All`) to get the server's view
- Diff current vs. proposed using structured diff
- Return unified diff format for display

`frontend/islands/YamlEditor.tsx`:
- Monaco editor with YAML syntax highlighting
- k8s resource schema completion (loaded from backend's OpenAPI spec)
- Inline validation errors (red squiggles on invalid fields)
- Upload button for `.yaml`/`.yml`/`.json` files (max 1MB)

`frontend/islands/YamlDiffViewer.tsx`:
- Side-by-side diff view (current vs. proposed)
- Color-coded additions/removals/changes
- Collapsible unchanged sections

**Acceptance criteria:**
- [ ] Monaco editor loads with YAML syntax highlighting
- [ ] File upload parses and displays YAML
- [ ] Validate button shows per-field errors inline
- [ ] Diff button shows side-by-side comparison
- [ ] Apply button applies YAML via server-side apply
- [ ] Multi-document YAML applies each document and shows per-document results
- [ ] RBAC denied on apply shows clear error message
- [ ] Export button on resource detail exports clean YAML (no managedFields, resourceVersion, etc.)

---

### Step 8: Resource Creation Wizards

**Files to create:**
```
frontend/islands/
├── WizardStepper.tsx
├── DeploymentWizard.tsx
└── ServiceWizard.tsx

frontend/routes/
├── workloads/deployments/create.tsx
└── networking/services/create.tsx
```

**Backend addition:**
- `POST /api/v1/yaml/preview` — accepts structured wizard payload, returns YAML

**Implementation details:**

`frontend/islands/WizardStepper.tsx`:
- Reusable multi-step wizard shell
- Steps defined as `{ title, component, validate }[]`
- Step navigation (back/next), validation on next
- Progress indicator
- "Unsaved changes" warning on navigation away
- **Form-to-YAML only (one-way sync):** Form state generates YAML via `POST /api/v1/yaml/preview`. The YAML preview on the final step is editable in Monaco, but switching back to form mode after editing YAML is NOT supported. Users who edit YAML directly are power users who stay in YAML mode.
- If user toggles to YAML mode mid-wizard, show the generated YAML. If they make edits, disable "Back to Form" with a message: "YAML has been manually edited. Continue in YAML mode or discard edits to return to the form."

`frontend/islands/DeploymentWizard.tsx` steps (4 steps, not 8):
1. **Basics + Container**: Name, namespace, image, tag, pull policy, replicas, update strategy
2. **Configuration**: CPU/memory requests/limits (with sane defaults pre-filled), environment variables (key/value, from ConfigMap ref, from Secret ref)
3. **Advanced** (optional — collapsible section, not a mandatory step): Volumes (PVC, ConfigMap, Secret, emptyDir mounts), health checks (liveness/readiness probes), labels, annotations. Users who skip this step get a working deployment with sensible defaults.
4. **Review & Apply**: Full YAML preview in Monaco editor (editable), Apply button. Name conflict check: if a deployment with that name already exists, show non-blocking warning ("A deployment named X already exists in namespace Y. Applying will update it.")

`frontend/islands/ServiceWizard.tsx` steps:
1. **Basics**: Name, namespace, labels
2. **Type**: ClusterIP / NodePort / LoadBalancer
3. **Ports**: Port mappings (port, targetPort, protocol, nodePort for NodePort)
4. **Selector**: Label selector to match pods
5. **Review & Apply**: YAML preview

**Acceptance criteria:**
- [ ] Deployment wizard walks through 4 steps (Basics, Config, Advanced, Review)
- [ ] Each step validates before allowing Next
- [ ] YAML preview shows correct deployment YAML
- [ ] User can edit YAML in review step
- [ ] Apply creates the deployment successfully
- [ ] Form-to-YAML generates correct YAML at each step
- [ ] YAML mode disables "Back to Form" if user has made manual edits
- [ ] Advanced step is optional (can skip to Review)
- [ ] Service wizard creates services of all types
- [ ] Name conflict warning shown if resource already exists
- [ ] Navigation away shows "unsaved changes" prompt

---

## Phase C: Observability & Advanced Features (Steps 9-12)

> Goal: Monitoring integration, storage/networking wizards, alerting, SSO.

### Step 9: Monitoring Integration

**Files to create:**
```
backend/internal/monitoring/
├── discovery.go
├── prometheus.go
├── grafana.go
├── metrics.go
└── dashboards/
    ├── cluster_overview.json
    ├── node_detail.json
    ├── pod_detail.json
    └── deployment_detail.json

frontend/
├── islands/PerformancePanel.tsx
├── routes/monitoring/
│   ├── index.tsx
│   ├── dashboards.tsx
│   └── prometheus.tsx
```

**Implementation details:**

`backend/internal/monitoring/discovery.go`:
- Probe for Prometheus: check for ServiceMonitor CRD, scan well-known service names (`prometheus-kube-prometheus-prometheus`, `prometheus-server`)
- Probe for Grafana: check well-known service names, Grafana CRD
- Cache discovery results, re-check every 5 minutes
- Expose `GET /api/v1/monitoring/status`

`backend/internal/monitoring/prometheus.go`:
- PromQL proxy: `GET /api/v1/monitoring/query`, `GET /api/v1/monitoring/query_range`
- Pass through query parameters to Prometheus API
- Return Prometheus response format directly

`backend/internal/monitoring/grafana.go`:
- Reverse proxy to Grafana at `/api/v1/monitoring/grafana/proxy/*`
- Inject Grafana service account auth headers
- Provision k8sCenter dashboards via Grafana API on startup
- Template variable injection via URL query params (`?var-namespace=X&var-pod=Y`)

`backend/internal/monitoring/metrics.go`:
- Named PromQL query templates for each resource type:
  - Pod: CPU usage, memory usage, network I/O
  - Deployment: replica health, rollout status
  - Node: CPU/memory/disk utilization, pod count
  - PVC: storage usage percentage
- Template variables: `$namespace`, `$pod`, `$node`, `$deployment`

`frontend/islands/PerformancePanel.tsx`:
- Grafana iframe embed pointed at backend proxy
- Dashboard selector
- Time range picker
- Fallback: if monitoring unavailable, show "Monitoring not configured" with setup instructions
- Dynamic CSP: backend sets `frame-src` header based on proxy URL

**Acceptance criteria:**
- [ ] Backend auto-discovers Prometheus and Grafana on startup
- [ ] `GET /api/v1/monitoring/status` reports discovery results
- [ ] PromQL queries return data from Prometheus
- [ ] Grafana dashboards render in iframe via proxy
- [ ] Resource detail pages show metrics in Performance tab
- [ ] Monitoring unavailable: graceful degradation (no errors, helpful message)
- [ ] Grafana dashboards provisioned automatically

---

### Step 10: CSI/CNI Wizards

**Files to create:**
```
backend/internal/k8s/
├── storage/
│   ├── csi.go
│   ├── csi_wizard.go
│   └── snapshot.go
├── networking/
│   ├── cni.go
│   ├── cilium.go
│   └── cni_wizard.go

frontend/islands/
├── CsiWizard.tsx
└── CniWizard.tsx

frontend/routes/
├── storage/
│   ├── csi/index.tsx
│   ├── csi/configure.tsx
│   ├── classes/index.tsx
│   ├── classes/create.tsx
│   ├── pvcs/index.tsx
│   └── pvcs/create.tsx
├── networking/
│   ├── cni/index.tsx
│   └── cni/configure.tsx
```

**CSI Wizard steps:**
1. **Driver Selection**: List discovered CSI drivers with capabilities
2. **Common Parameters**: Access modes, reclaim policy, volume binding mode, allow expansion
3. **Driver-Specific Parameters**: Generic key-value editor with driver-specific presets (AWS EBS, NFS, Longhorn, Rook Ceph)
4. **Review & Apply**: YAML preview of StorageClass

**CNI Wizard:**
- Auto-detect CNI plugin (Cilium, Calico, Flannel)
- Cilium-specific: Hubble toggle, ClusterMesh config, encryption settings
- Calico: BGP peering, IP pool management
- Warning before apply: "CNI configuration changes may cause network disruption"
- Confirmation dialog with impact description

**Acceptance criteria:**
- [ ] CSI drivers listed with capabilities
- [ ] StorageClass creation wizard works end-to-end
- [ ] PVC creation wizard with StorageClass selection
- [ ] CNI plugin auto-detected
- [ ] CNI configuration wizard with disruption warning
- [ ] Cilium-specific features (Hubble status) when Cilium detected

---

### Step 11: Alerting

**Files to create:**
```
backend/internal/alerting/
├── manager.go
├── smtp.go
├── store.go
└── templates/
    ├── alert.html
    └── digest.html

frontend/routes/monitoring/alerts/
├── index.tsx
├── rules.tsx
└── settings.tsx

frontend/islands/AlertBanner.tsx
```

**Implementation details:**

`backend/internal/alerting/manager.go`:
- `POST /api/v1/alerts/webhook` — receives Alertmanager webhooks
- HMAC-SHA256 verification using shared secret (configured in Helm values)
- Deduplication by alert fingerprint
- Broadcasts to WebSocket alert subscribers

`backend/internal/alerting/smtp.go`:
- Go `net/smtp` with STARTTLS
- HTML email templates via `html/template`
- Queue with retry (3 attempts, exponential backoff)

`backend/internal/alerting/store.go`:
- SQLite persistence for alert history
- Retention: 30 days default (configurable)
- Pagination for history queries

**Acceptance criteria:**
- [ ] Alertmanager webhook receives and processes alerts
- [ ] HMAC signature verification rejects unauthenticated webhooks
- [ ] Alert history persisted in SQLite
- [ ] SMTP configuration via settings page
- [ ] Test email sends successfully
- [ ] AlertBanner shows active alerts in real-time via WebSocket
- [ ] Alert rules CRUD via UI

---

### Step 12: OIDC/LDAP Auth

**Files to create:**
```
backend/internal/auth/
├── oidc.go
└── ldap.go

frontend/routes/settings/auth.tsx (update)
frontend/routes/login.tsx (update with SSO buttons)
```

**Implementation details:**

`backend/internal/auth/oidc.go`:
- `GET /api/v1/auth/oidc/:providerID/login` — redirect to OIDC provider
- `GET /api/v1/auth/oidc/:providerID/callback` — exchange code for tokens
- PKCE (Proof Key for Code Exchange) for security
- State parameter stored server-side (k8s Secret, TTL 10 min)
- Configurable claim mapping: OIDC groups claim → k8s groups

`backend/internal/auth/ldap.go`:
- Bind + search authentication
- Group membership via `memberOf` attribute
- Configurable: base DN, user filter, group filter, TLS
- LDAP group → k8s group mapping

**Acceptance criteria:**
- [ ] OIDC login flow redirects to provider and returns with JWT
- [ ] PKCE is used in the authorization request
- [ ] OIDC groups mapped to Kubernetes groups for impersonation
- [ ] LDAP bind + search authentication works
- [ ] Multiple auth providers can coexist
- [ ] Login page shows configured SSO options
- [ ] Auth settings page allows configuring OIDC/LDAP providers

---

## Phase D: Production Readiness (Steps 13-15)

> Goal: Helm chart, audit trail, polish.

### Step 13: Helm Chart — Production Hardening

The skeleton Helm chart (Deployment, Service, ServiceAccount, ClusterRole, ClusterRoleBinding) was created in Step 1. This step adds production hardening and the full template set.

**Files to add/update:**
```
helm/kubecenter/
├── values.yaml (expanded with all configuration options)
├── values.schema.json
├── templates/
│   ├── namespace.yaml
│   ├── deployment-frontend.yaml    (new)
│   ├── service-frontend.yaml       (new)
│   ├── ingress.yaml                (new)
│   ├── configmap-app.yaml          (new)
│   ├── secret-app.yaml             (new)
│   ├── networkpolicy.yaml          (new)
│   ├── poddisruptionbudget.yaml    (new)
│   ├── pvc-data.yaml               (new)
│   ├── monitoring/
│   │   ├── prometheus-values.yaml
│   │   ├── grafana-datasource.yaml
│   │   ├── grafana-dashboards-cm.yaml
│   │   └── alertmanager-config.yaml
│   └── tests/
│       └── test-connection.yaml
└── charts/.gitkeep
```

**Key Helm values:**
```yaml
replicaCount: 1  # Must be 1 when persistence.enabled=true (SQLite single-writer constraint)
auth:
  initialAdmin:
    username: admin
    # password auto-generated if not set, printed in pod logs
    setupToken: ""  # Optional: required in POST /api/v1/setup/init body for automated installs
  oidc:
    enabled: false
    providers: []
  ldap:
    enabled: false
cors:
  allowedOrigins: []  # Auto-set to ingress host. In dev mode, defaults to localhost:8000
monitoring:
  deploy: false  # Set true to deploy kube-prometheus-stack
  prometheus:
    url: ""  # Auto-discovered if empty
  grafana:
    url: ""  # Auto-discovered if empty
alerting:
  smtp:
    enabled: false
  webhook:
    secret: ""  # Auto-generated if empty
persistence:
  enabled: true
  storageClass: ""
  size: 1Gi
clusters:
  - id: local
    name: Local Cluster
```

**Security hardening:**
- Pod Security Standards: restricted
- Non-root (UID 65534), read-only rootfs
- Drop all capabilities
- NetworkPolicy restricting ingress/egress
- ClusterRole with explicit resource lists (no wildcards)
- JWT secret auto-generated at install

**Acceptance criteria:**
- [ ] `helm lint` passes
- [ ] `helm template` renders valid manifests
- [ ] `helm install` deploys working k8sCenter in kind cluster
- [ ] All security checklist items enforced
- [ ] PDB configured for zero-downtime upgrades
- [ ] NetworkPolicy restricts traffic appropriately
- [ ] `helm test` validates connectivity

---

### Step 14: Audit Logging

**Files to create:**
```
backend/internal/audit/
├── logger.go
└── store.go

frontend/routes/settings/audit.tsx (or integrate into existing)
```

**Implementation details:**

Swap the `AuditLogger` implementation (interface defined in Step 2) from `SlogAuditLogger` to `SQLiteAuditLogger`. No middleware changes needed — just a different implementation of the same interface.

`backend/internal/audit/store.go`:
- SQLite table: `audit_logs(id, timestamp, cluster_id, user, source_ip, action, resource_kind, resource_namespace, resource_name, result, detail)`
- **Schema migration strategy**: Use `golang-migrate/migrate` with embedded SQL migration files. `schema_version` table tracks applied migrations. Migrations run automatically on startup before accepting traffic. This ensures future versions can add columns without manual intervention.
- **Single-replica constraint**: SQLite on a PVC requires `ReadWriteOnce` access mode. Backend must run as a single replica (enforced via Helm values `replicaCount: 1` and documented). NFS-backed PVCs are explicitly not supported for the SQLite data directory.
- 90-day retention with daily cleanup job
- Paginated query API with filters (user, kind, namespace, action, time range) — uses offset-based pagination (not cursor-based like k8s resources)

`backend/internal/audit/logger.go` (updated from Step 2 scaffold):
- `SQLiteAuditLogger` implements the `AuditLogger` interface defined in Step 2
- Captures: who (user), what (action + resource), when (timestamp), where (source IP), result (success/failure)
- Special handling: secret reveals logged with key name (not value)
- Pod exec: session start/end + duration logged

**Acceptance criteria:**
- [ ] All write operations persisted in audit log
- [ ] Secret reveals logged with key name
- [ ] Pod exec sessions logged (start/end/duration)
- [ ] `GET /api/v1/audit/logs` returns paginated, filterable results
- [ ] Retention cleanup runs daily
- [ ] Audit log UI allows filtering by all dimensions

---

### Step 15: Polish

**Focus areas:**
- [ ] Consistent loading skeletons on all data-fetching pages
- [ ] EmptyState components on all list pages
- [ ] Error boundaries with user-friendly error pages
- [ ] Dark mode toggle (persists in localStorage, defaults to OS preference)
- [ ] Keyboard shortcuts: `?` help, `/` search, `k` up, `j` down
- [ ] Toast notifications for async operations (create, delete, apply)
- [ ] Responsive design for tablet (mobile deprioritized except login)
- [ ] CSP headers with dynamic Grafana `frame-src`
- [ ] Rate limiting on all auth endpoints verified
- [ ] Input validation on all API inputs (k8s name regex, max lengths)
- [ ] Container images: distroless for Go, Deno slim for frontend
- [ ] End-to-end test suite with Playwright against kind cluster

---

## Alternative Approaches Considered

| Approach | Why Rejected |
|---|---|
| **Next.js instead of Fresh** | Requires Node.js runtime, larger bundle sizes, not Deno-native |
| **Viper for Go config** | Forcibly lowercases keys, larger dependency tree than koanf |
| **PostgreSQL for persistence** | Requires external database deployment; SQLite sufficient for single-instance Phase 1 |
| **gRPC between frontend and backend** | Adds complexity; REST+WebSocket is simpler and sufficient |
| **Server-sent events instead of WebSocket** | SSE is unidirectional; exec terminal and bidirectional subscriptions require WebSocket |

---

## Dependencies & Prerequisites

### External
- Go 1.26+ installed
- Deno 2.x+ installed
- Docker for container builds
- kind for local Kubernetes cluster
- Helm 3.x for chart development
- kubectl for cluster interaction

### Internal Build Order Dependencies
```
Step 1 (Backend + Helm skeleton + CI) ← Step 2 (Auth + Audit interface) ← Step 3 (Resources + RBAC filtering)
Step 4 (Frontend) ← Step 5 (Browser + WebSocket hub) ← Step 6 (Detail + Logs + Exec)
Step 3 + Step 5 → Step 7 (YAML) → Step 8 (Wizards)
Step 3 → Step 9 (Monitoring)
Step 3 → Step 10 (CSI/CNI)
Step 9 → Step 11 (Alerting)
Step 2 → Step 12 (OIDC/LDAP)
Step 1 skeleton → Step 13 (Helm production hardening)
Step 2 interface → Step 14 (Audit SQLite implementation)
Steps 1-14 → Step 15 (Polish)
```

**Critical ordering notes:**
- Helm skeleton in Step 1 enables in-cluster testing from day one
- `AuditLogger` interface defined in Step 2 (stdout impl), swapped to SQLite in Step 14 — no retroactive instrumentation
- WebSocket hub built in Step 5 (needed for resource tables), extended for logs/exec in Step 6
- CI pipeline in Step 1, extended with frontend jobs in Step 4, Helm jobs in Step 13

---

## Risk Analysis & Mitigation

| Risk | Impact | Mitigation |
|---|---|---|
| Fresh 2.x is still in alpha | High — entire frontend depends on it | Pin version. **Fallback plan**: if Fresh 2 stalls or breaks, eject to plain Preact + Vite with `vite-plugin-pages` for file-based routing. Islands and components are pure Preact (portable). Fresh-specific code is isolated to `routes/`, `main.ts`, and `_app.tsx`. **Decision point**: if Fresh 2 is not at RC by Step 4 start, evaluate alternatives before building frontend. |
| Informer memory on large clusters (10k+ pods) | High — OOM kills | Use filtered informers, set resource limits, add memory metrics |
| CSP + dynamic Grafana URL | Medium — iframe blocked by CSP | Backend sets CSP header dynamically per-request |
| SQLite on network-attached PVC | Medium — corruption risk | Require `ReadWriteOnce` storage class with `fsGroup` support. Document that NFS-backed PVCs are not supported. Enforce single replica via Helm values. WAL mode enabled. |
| SQLite concurrent writes under load | Low — single writer | WAL mode, connection pooling, acceptable for Phase 1 scale |
| Go 1.26 Green Tea GC edge cases | Low — enabled by default, widely tested | Disable with `GOEXPERIMENT=nogreenteagc` if issues arise |
| Backend unreachable from frontend | Medium — degraded UX | Frontend shows "Backend unreachable" banner on connection errors. Retry with exponential backoff. Disable write operations but display last-known cached data if available via service worker or signals. |

---

## Success Metrics

- [ ] Fresh Helm install → login → dashboard in under 5 minutes
- [ ] Resource list pages load in under 500ms (informer cache)
- [ ] WebSocket updates appear within 1s of k8s API change
- [ ] Wizard→Apply→Verify cycle completes without YAML knowledge
- [ ] All 15 security checklist items pass
- [ ] Backend test coverage > 70%
- [ ] Frontend test coverage > 60% (utility functions + key islands)
- [ ] E2E test suite covers: login, resource CRUD, wizard, YAML apply, monitoring view

---

## Future Considerations (Phase 2)

- Multi-cluster management via `clusterID` parameter (hooks already in Phase 1)
- Custom Resource Definition (CRD) management UI
- GitOps integration (ArgoCD, Flux)
- Cluster provisioning (create new clusters)
- Cost management and resource optimization recommendations
- Plugin system for extending the UI
