# CLAUDE.md — KubeCenter: Kubernetes Management Platform

## Project Vision

KubeCenter is a web-based Kubernetes management platform that delivers vCenter-level functionality for Kubernetes clusters. It provides GUI-driven wizards for all cluster operations (deployments, CSI, CNI, networking, storage), integrated Prometheus/Grafana observability, RBAC-aware multi-tenancy, and full YAML escape hatches for power users. It is deployed via Helm chart inside the managed cluster, with architecture designed from day one to support multi-cluster management in a future phase.

---

## Technology Stack

| Layer | Technology | Version |
|---|---|---|
| Backend API | Go | 1.26.x |
| Kubernetes Client | client-go | v0.35.2 (k8s.io/api, apimachinery, client-go) |
| HTTP Router | chi (go-chi/chi/v5) | v5.2.5 |
| JWT | golang-jwt/jwt/v5 | v5.3.1 |
| Password Hashing | golang.org/x/crypto (Argon2id) | v0.49.0 |
| Configuration | koanf/v2 | v2.3.3 (YAML file + env vars) |
| WebSocket | gorilla/websocket | v1.5.x (planned) |
| Frontend Runtime | Deno | 2.x (planned — Step 4+) |
| Frontend Framework | Fresh | 2.x via JSR @fresh/core (planned — Step 4+) |
| Language | TypeScript | Strict mode, ESM only (planned) |
| CSS | Tailwind CSS | v4.x (planned) |
| YAML Editor | Monaco Editor | Latest (planned) |
| Monitoring | Prometheus + Grafana | kube-prometheus-stack compatible (planned — Step 9) |
| Alerting | Prometheus Alertmanager + SMTP | Via Go SMTP client (planned — Step 11) |
| Auth | Local (Argon2id) implemented; OIDC / LDAP planned (Step 12) | golang-jwt/jwt/v5, golang.org/x/crypto |
| Deployment | Helm | v3.x chart (skeleton deployed) |
| Container | Distroless / Alpine-based multi-stage | Scratch for Go, Deno slim for frontend |

---

## Project Structure (Actual — as of Step 2 completion)

Files marked with `[planned]` do not exist yet and will be created in later steps.

```
kubecenter/
├── CLAUDE.md                          # This file — project context for Claude Code
├── README.md                          # User-facing documentation
├── LICENSE                            # Apache 2.0
├── SECURITY.md                        # Security policy
├── Makefile                           # Build, test, lint, Docker targets
├── .gitignore
│
├── backend/                           # Go 1.26 backend
│   ├── go.mod                         # Module: github.com/kubecenter/kubecenter, go 1.26.1
│   ├── go.sum
│   ├── cmd/
│   │   └── kubecenter/
│   │       └── main.go                # Entrypoint — HTTP server, k8s client, informers, auth init
│   ├── internal/
│   │   ├── server/
│   │   │   ├── server.go              # Server struct + Deps, chi router, global middleware chain
│   │   │   ├── routes.go              # Route registration (per-group auth/CSRF, not global skip list)
│   │   │   ├── response.go            # writeJSON, setRefreshCookie, newAuditEntry, issueTokenPair
│   │   │   ├── handle_auth.go         # Login, refresh, logout, providers, /auth/me handlers
│   │   │   ├── handle_auth_test.go    # 19 httptest integration tests (68% server coverage)
│   │   │   ├── handle_setup.go        # POST /setup/init — first admin creation (one-time)
│   │   │   ├── handle_health.go       # GET /healthz, GET /readyz
│   │   │   ├── handle_cluster.go      # GET /cluster/info (version, node count, KubeCenter version)
│   │   │   └── middleware/
│   │   │       ├── auth.go            # JWT validation middleware + CSRF (X-Requested-With header)
│   │   │       ├── auth_test.go       # Middleware unit tests
│   │   │       ├── ratelimit.go       # Rate limiting (5 req/min per IP, global bucket across endpoints)
│   │   │       ├── ratelimit_test.go  # Rate limiter tests
│   │   │       └── cors.go            # CORS configuration
│   │   │
│   │   ├── auth/
│   │   │   ├── provider.go            # AuthProvider interface + StoredUser/User types
│   │   │   ├── provider_test.go
│   │   │   ├── local.go               # Local account provider (Argon2id, semaphore-limited concurrency)
│   │   │   ├── local_test.go
│   │   │   ├── jwt.go                 # JWT TokenManager — HMAC-SHA256, 15min access, 7day refresh
│   │   │   ├── jwt_test.go
│   │   │   ├── rbac.go                # RBACChecker — SelfSubjectRulesReview (1 call/ns, cached 60s)
│   │   │   ├── session.go             # SessionStore — in-memory refresh tokens, rotation on use
│   │   │   └── session_test.go
│   │   │   # [planned] oidc.go        # OIDC provider (Step 12)
│   │   │   # [planned] ldap.go        # LDAP provider (Step 12)
│   │   │
│   │   ├── k8s/
│   │   │   ├── client.go              # ClientFactory — in-cluster/kubeconfig, impersonation cache (sync.Map, 5-min TTL)
│   │   │   └── informers.go           # InformerManager — pods, deployments, services, namespaces, nodes
│   │   │   # [planned] resources/     # Resource handlers (Step 3)
│   │   │   # [planned] storage/       # CSI/StorageClass (Step 10)
│   │   │   # [planned] networking/    # CNI detection (Step 10)
│   │   │
│   │   │   # [planned] monitoring/    # Prometheus/Grafana integration (Step 9)
│   │   │   # [planned] alerting/      # Alertmanager webhook, SMTP (Step 11)
│   │   │   # [planned] yaml/          # YAML parse, validate, apply, diff (Step 7)
│   │   │   # [planned] websocket/     # WS hub, client, events (Step 5)
│   │   │
│   │   ├── audit/
│   │   │   ├── logger.go              # Audit Logger interface + SlogLogger implementation
│   │   │   └── logger_test.go
│   │   │   # [planned] store.go       # SQLite persistence (Step 14)
│   │   │
│   │   └── config/
│   │       ├── config.go              # Config struct — koanf (YAML + env), validation
│   │       ├── defaults.go            # Default values
│   │       └── config_test.go
│   │
│   ├── pkg/
│   │   ├── api/
│   │   │   └── types.go               # Shared API request/response types (APIResponse, APIError)
│   │   └── version/
│   │       ├── version.go             # Build version info (ldflags)
│   │       └── version_test.go
│   │
│   └── Dockerfile                     # Multi-stage: Go build → distroless/static
│
│   # [planned] frontend/              # Deno 2.x + Fresh 2.x frontend (Step 4+)
│
├── helm/
│   └── kubecenter/                    # Helm chart (skeleton — Step 1)
│       ├── Chart.yaml
│       ├── values.yaml
│       ├── templates/
│       │   ├── _helpers.tpl
│       │   ├── deployment-backend.yaml
│       │   ├── service-backend.yaml
│       │   ├── serviceaccount.yaml
│       │   ├── clusterrole.yaml
│       │   └── clusterrolebinding.yaml
│       # [planned] ingress, networkpolicy, frontend templates (Step 13)
│
├── plans/
│   └── feat-kubecenter-phase1-mvp.md  # Full 15-step implementation plan with progress tracker
│
├── todos/                             # Tracked findings and improvements (file-based todo system)
│   ├── 001-014: complete              # First review — all fixed
│   ├── 015-020, 022-023: pending      # Re-review findings — deferred
│   └── 021: complete                  # Handler integration tests
│
├── .github/
│   └── workflows/
│       └── ci.yml                     # go vet + go test -race + go build
│
# [planned] docs/                      # Architecture, API reference, deployment docs
# [planned] scripts/                   # Dev setup, cert generation, demo data
```

---

## Architecture Principles

### 1. Backend (Go) Design Rules

- **All Kubernetes API calls go through user impersonation.** Never use the service account's own permissions for user-initiated actions. The backend impersonates the authenticated user's k8s identity so that Kubernetes RBAC is enforced server-side. The service account needs `impersonate` permissions only.
- **Informers for read, direct API calls for write.** Use `SharedInformerFactory` with label/field selectors to maintain an in-memory cache of cluster state. All list/get operations read from the informer cache. All create/update/delete operations go through the API server directly, with impersonation.
- **Server-side apply for all YAML operations.** Use `PATCH` with `application/apply-patch+yaml` content type. Never use `kubectl apply` under the hood.
- **WebSocket hub pattern for real-time updates.** A central hub goroutine receives events from informers and fans them out to connected WebSocket clients. Clients subscribe to specific resource types and namespaces. Authenticate WebSocket connections with the same JWT used for REST.
- **Structured logging with slog.** Use Go 1.26's `log/slog` package with JSON output. Include request ID, user identity, resource kind, namespace, and name in all log entries.
- **Error handling: never expose internal errors to users.** Wrap k8s API errors into user-friendly messages. Return appropriate HTTP status codes. Log full error details server-side.
- **Configuration via environment variables with YAML file fallback.** Use a single config struct loaded at startup. Env vars override YAML file values. All secrets come from env vars or k8s Secrets, never config files.

### 2. Frontend (Deno/Fresh) Design Rules

- **Islands architecture strictly enforced.** Only components that require client-side interactivity (forms, editors, WebSocket consumers, charts) are islands. Everything else is server-rendered HTML. This minimizes JavaScript sent to the client.
- **API client is a typed wrapper.** All backend calls go through `lib/api.ts` which handles auth token injection, error parsing, and response typing. Never use raw `fetch` in components.
- **Wizard components follow a consistent pattern.** Every wizard uses `WizardStepper.tsx` as its shell. Steps are defined as an array of `{ title, component, validate }` objects. The wizard handles navigation, validation, and final submission. On the final step, the wizard shows a YAML preview of what will be applied, with an option to edit the YAML before applying.
- **Dual-mode for every configuration.** Every resource creation/edit page offers both a wizard/form mode and a raw YAML mode. A toggle switches between them. Changes in one mode are reflected in the other in real-time (form→YAML serialization and YAML→form parsing).
- **Grafana embeds use `<iframe>` with auth proxy.** The backend proxies Grafana with proper auth headers. The frontend embeds Grafana panels via iframe pointed at the backend proxy endpoint. This avoids exposing Grafana directly and handles auth seamlessly.
- **Tailwind CSS utility-only.** No custom CSS files except for the global Tailwind directives and CSS custom properties for theming (dark mode support). Use Tailwind's `@apply` sparingly and only in the global stylesheet for base element styles.
- **Consistent color semantics.** Use CSS custom properties for status colors: `--color-success` (green, healthy/running), `--color-warning` (amber, pending/degraded), `--color-danger` (red, failed/error), `--color-info` (blue, informational). Map k8s resource states to these consistently everywhere.

### 3. Security Rules

- **TLS everywhere.** The backend serves HTTPS. In-cluster, use cert-manager to provision TLS certificates. The Helm chart includes cert-manager Certificate resources.
- **JWT tokens are short-lived (15 min) with refresh tokens (7 day).** Refresh tokens are stored server-side (not in localStorage). Access tokens are sent as `Authorization: Bearer` headers. Refresh via a dedicated `/api/auth/refresh` endpoint.
- **Secrets are never returned in full.** The secrets API endpoint returns metadata and masked values (`****`). A separate `reveal` endpoint returns the actual value, requires explicit user action, and is audit-logged.
- **Content Security Policy headers.** Strict CSP that allows only same-origin scripts, the Monaco CDN, and Grafana iframe sources.
- **Network Policies deployed by default.** The Helm chart includes NetworkPolicy resources that restrict ingress/egress to only what KubeCenter needs.
- **Pod Security Standards: restricted.** KubeCenter pods run as non-root, read-only root filesystem, no privilege escalation, drop all capabilities.
- **Audit logging for all write operations.** Every create, update, delete, and secret reveal is logged with: timestamp, user identity, source IP, resource type, resource name, namespace, action, and result.

### 4. Monitoring Integration Rules

- **Auto-discovery on startup.** The backend probes the cluster for existing Prometheus (by ServiceMonitor CRDs and well-known service names) and Grafana instances. Results are cached and re-checked periodically.
- **If bringing your own Prometheus/Grafana:** the backend configures itself as a Prometheus client pointing at the discovered endpoint. For Grafana, it provisions dashboards via the Grafana HTTP API using a service account token.
- **If deploying fresh:** the Helm chart includes `kube-prometheus-stack` as a conditional subchart dependency (`monitoring.enabled: true` in values.yaml). Prometheus, Grafana, kube-state-metrics, and node-exporter are deployed with pre-configured scrape targets and dashboards.
- **Pre-built PromQL queries for every resource type.** The `internal/monitoring/metrics.go` file contains named query templates for: pod CPU/memory, deployment replica health, PVC usage, service latency (if available), node resource utilization, Cilium network flow metrics.
- **Grafana dashboards are provisioned as ConfigMaps.** JSON dashboard definitions are baked into the Helm chart and loaded via Grafana's sidecar provisioner. They are parameterized with template variables for namespace, pod, node, etc.

---

## API Design

### Implemented Endpoints (as of Step 2)

```
# Public (no auth)
GET    /healthz                        # Liveness probe (always 200)
GET    /readyz                         # Readiness probe (checks informer sync)
POST   /api/v1/setup/init              # Create first admin account (one-time, rate limited)
POST   /api/v1/auth/login              # Local login — returns JWT access token + refresh cookie (rate limited)
POST   /api/v1/auth/refresh            # Refresh access token using httpOnly cookie (rate limited)
POST   /api/v1/auth/logout             # Invalidate refresh token
GET    /api/v1/auth/providers          # List configured auth providers (currently: ["local"])

# Authenticated (requires Bearer token + X-Requested-With header for CSRF)
GET    /api/v1/auth/me                 # Current user info + k8s RBAC summary (SelfSubjectRulesReview)
GET    /api/v1/cluster/info            # Cluster version, node count, KubeCenter version
```

### Full Planned REST Endpoints (Go Backend)

All endpoints are prefixed with `/api/v1`.

```
# Authentication
POST   /api/v1/auth/login            # Local login (username + password)
POST   /api/v1/auth/oidc/callback    # OIDC callback [planned]
POST   /api/v1/auth/refresh           # Refresh access token
POST   /api/v1/auth/logout            # Invalidate session
GET    /api/v1/auth/providers         # List configured auth providers
GET    /api/v1/auth/me                # Current user info + k8s RBAC summary

# Generic Kubernetes Resources (pattern repeats for each resource type)
GET    /api/v1/resources/:kind                    # List across all namespaces
GET    /api/v1/resources/:kind/:namespace          # List in namespace
GET    /api/v1/resources/:kind/:namespace/:name    # Get specific resource
POST   /api/v1/resources/:kind/:namespace          # Create resource (JSON or YAML body)
PUT    /api/v1/resources/:kind/:namespace/:name    # Update resource
DELETE /api/v1/resources/:kind/:namespace/:name    # Delete resource
PATCH  /api/v1/resources/:kind/:namespace/:name    # Patch resource (strategic merge)

# Specialized Resource Endpoints
POST   /api/v1/deployments/:ns/:name/scale        # Scale deployment
POST   /api/v1/deployments/:ns/:name/rollback      # Rollback to revision
POST   /api/v1/deployments/:ns/:name/restart        # Rolling restart
GET    /api/v1/pods/:ns/:name/logs                  # Stream pod logs (SSE)
POST   /api/v1/pods/:ns/:name/exec                  # WebSocket pod exec
GET    /api/v1/nodes/:name/drain                    # Drain node (long-running)
POST   /api/v1/nodes/:name/cordon                   # Cordon/uncordon

# YAML Operations
POST   /api/v1/yaml/validate          # Validate YAML against cluster's OpenAPI schema
POST   /api/v1/yaml/apply             # Server-side apply (supports multi-doc)
POST   /api/v1/yaml/diff              # Dry-run apply and return diff against current state
POST   /api/v1/yaml/export/:kind/:ns/:name   # Export resource as clean YAML

# Storage (CSI)
GET    /api/v1/storage/drivers         # List CSI drivers and their capabilities
GET    /api/v1/storage/classes         # List StorageClasses with CSI driver info
POST   /api/v1/storage/classes         # Create StorageClass via wizard payload
GET    /api/v1/storage/snapshots       # List VolumeSnapshots

# Networking (CNI)
GET    /api/v1/networking/cni          # Detected CNI plugin and version
GET    /api/v1/networking/cni/config   # Current CNI configuration (Cilium CiliumConfig, etc.)
PUT    /api/v1/networking/cni/config   # Update CNI configuration via wizard payload
GET    /api/v1/networking/cilium/status  # Cilium agent status, Hubble status

# Monitoring
GET    /api/v1/monitoring/status       # Prometheus + Grafana connection status
GET    /api/v1/monitoring/query        # Proxy PromQL instant query
GET    /api/v1/monitoring/query_range  # Proxy PromQL range query
GET    /api/v1/monitoring/dashboards   # List available Grafana dashboards
GET    /api/v1/monitoring/grafana/proxy/*  # Reverse proxy to Grafana for iframe embedding

# Alerting
GET    /api/v1/alerts                  # Current active alerts
GET    /api/v1/alerts/history          # Alert history
GET    /api/v1/alerts/rules            # Configured alert rules
POST   /api/v1/alerts/rules            # Create/update alert rule
DELETE /api/v1/alerts/rules/:id        # Delete alert rule
PUT    /api/v1/alerts/settings         # SMTP configuration, notification routing
POST   /api/v1/alerts/test             # Send test email

# Cluster
GET    /api/v1/cluster/info            # Cluster version, node count, resource summary
GET    /api/v1/cluster/events          # Cluster events (paginated)
GET    /api/v1/cluster/namespaces      # Namespace list (for selector dropdowns)
GET    /api/v1/cluster/api-resources   # Available API resources (for dynamic resource discovery)

# Audit
GET    /api/v1/audit/logs              # Audit log entries (paginated, filterable)

# Settings
GET    /api/v1/settings                # Current application settings
PUT    /api/v1/settings                # Update application settings
GET    /api/v1/settings/auth           # Auth provider configuration
PUT    /api/v1/settings/auth           # Update auth provider configuration
```

### WebSocket Endpoints

```
WS /api/v1/ws/resources    # Subscribe to resource events (watch)
                            # Client sends: { "subscribe": { "kind": "pods", "namespace": "default" } }
                            # Server sends: { "type": "ADDED|MODIFIED|DELETED", "object": {...} }

WS /api/v1/ws/logs/:ns/:pod/:container   # Real-time log stream

WS /api/v1/ws/exec/:ns/:pod/:container   # Pod exec terminal (stdin/stdout/stderr/resize)

WS /api/v1/ws/alerts       # Real-time alert notifications
```

---

## Key Implementation Details

### Kubernetes Client Initialization (backend/internal/k8s/client.go)

```go
// Use in-cluster config since we deploy via Helm inside the cluster.
// The service account is configured with impersonation permissions.
// For every user-initiated request, create an impersonating client:
//
//   config, _ := rest.InClusterConfig()
//   config.Impersonate = rest.ImpersonationConfig{
//       UserName: authenticatedUser.KubernetesUsername,
//       Groups:   authenticatedUser.KubernetesGroups,
//   }
//   clientset, _ := kubernetes.NewForConfig(config)
//
// The informer factory uses the SERVICE ACCOUNT's own permissions (broad read access)
// but all write operations use the impersonating client.
```

### Wizard → YAML Pipeline

**Decision from plan review:** Form-to-YAML only (no bidirectional YAML→form sync — too complex, deferred).

Every wizard follows this data flow:
1. User fills in wizard steps (frontend form state)
2. Frontend serializes form state into a structured JSON payload
3. Backend receives JSON, constructs a Kubernetes object programmatically using client-go typed structs
4. Backend serializes the object to YAML
5. Backend returns the YAML to the frontend for preview
6. User reviews YAML in Monaco editor (can edit)
7. User clicks "Apply" — backend validates and applies via server-side apply

### Monitoring Bootstrap Sequence (on Helm install)

```
1. Helm install starts
2. If values.monitoring.deploy = true:
   a. kube-prometheus-stack subchart deploys Prometheus, Grafana, kube-state-metrics, node-exporter
   b. ConfigMaps with KubeCenter Grafana dashboards are deployed
   c. Alertmanager is configured with webhook receiver pointing to KubeCenter backend
3. If values.monitoring.deploy = false (bring your own):
   a. KubeCenter backend starts and runs discovery:
      - Checks for Prometheus via ServiceMonitor CRD existence + well-known service names
      - Checks for Grafana via well-known service names + Grafana CRD
   b. If found, backend configures itself to use discovered endpoints
   c. Backend provisions dashboards into existing Grafana via API
   d. Backend configures webhook receiver in existing Alertmanager via API
4. Backend exposes /api/v1/monitoring/status for frontend to check readiness
```

---

## Build System

### Makefile Targets (actual)

```makefile
make dev              # Alias for dev-backend
make dev-backend      # cd backend && go run ./cmd/kubecenter --config ""
make build            # Alias for build-backend
make build-backend    # go build with ldflags (version, commit, date) → bin/kubecenter
make test             # Alias for test-backend
make test-backend     # go test ./... -race -cover -count=1
make lint             # go vet ./...
make docker-build     # Docker build for backend
make helm-lint        # helm lint helm/kubecenter
make helm-template    # helm template (dry-run)
make clean            # rm -rf backend/bin
```

Targets not yet added (planned for later steps):
- `make dev-frontend` (Step 4)
- `make build-frontend` (Step 4)
- `make test-frontend` (Step 4)
- `make test-e2e` (Step 15)
- `make docker-push` (Step 13)

### Go Module (backend/go.mod)

```
module github.com/kubecenter/kubecenter

go 1.26.1

require (
    github.com/go-chi/chi/v5     v5.2.5
    github.com/go-chi/cors        v1.2.2
    github.com/golang-jwt/jwt/v5  v5.3.1
    github.com/knadh/koanf/v2     v2.3.3   // Config: YAML file + env vars
    golang.org/x/crypto           v0.49.0  // Argon2id password hashing
    k8s.io/api                    v0.35.2
    k8s.io/apimachinery           v0.35.2
    k8s.io/client-go              v0.35.2
)
```

Dependencies not yet added (will be added in later steps):
- `gorilla/websocket` (Step 5: WebSocket hub)
- `coreos/go-oidc/v3` (Step 12: OIDC auth)
- `go-ldap/ldap/v3` (Step 12: LDAP auth)
- `prometheus/client_golang` (Step 9: monitoring)
- `grafana-api-golang-client` (Step 9: Grafana integration)
- `mattn/go-sqlite3` or `modernc.org/sqlite` (Step 14: audit persistence)

### Deno Config (frontend/deno.json) — PLANNED, not yet created

**NOTE:** The plan identified corrections from the original spec. The actual deno.json when created should use:
- `"jsx": "precompile"` (NOT `"react-jsx"`) for Fresh 2 SSR performance
- `jsr:` and `npm:` specifiers (NOT `https://esm.sh/` or `https://deno.land/x/`)
- Fresh 2.x from JSR `@fresh/core` (NOT `$fresh/`)
- Requires `vite.config.ts` and `client.ts` at frontend root (Fresh 2 uses Vite)
- No `fresh.config.ts` or `tailwind.config.ts` (Tailwind v4 is CSS-first via `@theme`)

---

## Configuration

### Environment Variables (koanf)

Configuration uses [koanf](https://github.com/knadh/koanf) with `KUBECENTER_` prefix. The underscore-separated env var name maps to the nested config struct path. **This is a common gotcha** — the env var name uses the struct field path, not a flat name.

```bash
# Config struct path        → Env var name
# Config.Server.Port        → KUBECENTER_SERVER_PORT
# Config.Auth.JWTSecret     → KUBECENTER_AUTH_JWTSECRET
# Config.Auth.SetupToken    → KUBECENTER_AUTH_SETUPTOKEN
# Config.Log.Level           → KUBECENTER_LOG_LEVEL
# Config.Log.Format          → KUBECENTER_LOG_FORMAT
# Config.Dev                 → KUBECENTER_DEV
# Config.ClusterID           → KUBECENTER_CLUSTERID
# Config.CORS.AllowedOrigins → KUBECENTER_CORS_ALLOWEDORIGINS
```

**IMPORTANT:** `KUBECENTER_JWT_SECRET` does NOT work. The correct name is `KUBECENTER_AUTH_JWTSECRET` (maps to `Config.Auth.JWTSecret`). Same for setup token: `KUBECENTER_AUTH_SETUPTOKEN` not `KUBECENTER_SETUP_TOKEN`.

### Running Locally

```bash
# Start backend against a kind cluster
KUBECENTER_DEV=true \
KUBECENTER_AUTH_JWTSECRET="your-secret-minimum-32-bytes-long" \
KUBECENTER_AUTH_SETUPTOKEN="your-setup-token" \
  go run ./cmd/kubecenter

# Or use make (uses default config, no JWT secret = random key per restart)
make dev-backend
```

When `KUBECENTER_DEV=true`, the k8s client uses kubeconfig (~/.kube/config) instead of in-cluster config.

If no JWT secret is configured, a random key is generated (tokens won't survive restarts).

### Rate Limiter Behavior

The rate limiter uses a **single 5 req/min bucket per IP** shared across ALL rate-limited endpoints (login, refresh, setup). In local development from localhost, all requests share one bucket. Restart the backend to reset.

## Development Setup

### Prerequisites
- Go 1.26+
- kind (Kubernetes in Docker) for local testing
- Helm 3.x
- kubectl

### Local Development Flow
```bash
# 1. Create local kind cluster
kind create cluster --name kubecenter

# 2. Start backend in dev mode (connects to kind cluster via kubeconfig)
KUBECENTER_DEV=true \
KUBECENTER_AUTH_JWTSECRET="test-secret-for-dev-minimum-32-bytes" \
KUBECENTER_AUTH_SETUPTOKEN="dev-setup-token" \
  cd backend && go run ./cmd/kubecenter

# 3. Backend API at http://localhost:8080
#    Health: curl http://localhost:8080/healthz
#    Setup:  curl -X POST http://localhost:8080/api/v1/setup/init \
#              -H "Content-Type: application/json" \
#              -d '{"username":"admin","password":"changeme123"}'
#    Login:  curl -X POST http://localhost:8080/api/v1/auth/login \
#              -H "Content-Type: application/json" \
#              -H "X-Requested-With: XMLHttpRequest" \
#              -d '{"username":"admin","password":"changeme123"}'
```

---

## Key Conventions and Patterns

### Naming
- Go packages: lowercase, single word (`auth`, `k8s`, `monitoring`)
- Go files: snake_case (`csi_wizard.go`)
- TypeScript files: PascalCase for components (`DeploymentWizard.tsx`), camelCase for utilities (`api.ts`)
- API routes: kebab-case (`/api/v1/query-range`)
- CSS: Tailwind utility classes only. No custom class names.
- Helm values: camelCase (`monitoring.enabled`, `auth.oidc.issuerUrl`)

### Error Handling (Go)
```go
// Always wrap errors with context
if err != nil {
    return fmt.Errorf("failed to list deployments in namespace %s: %w", namespace, err)
}

// API handlers return structured errors
type APIError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Detail  string `json:"detail,omitempty"`
}
```

### API Response Format
```json
{
  "data": { ... },
  "metadata": {
    "total": 42,
    "page": 1,
    "pageSize": 20
  }
}
```

### Error Response Format
```json
{
  "error": {
    "code": 403,
    "message": "You do not have permission to delete pods in namespace production",
    "detail": "RBAC: user 'chris' lacks 'delete' permission on 'pods' in namespace 'production'"
  }
}
```

---

## Phase 1 Build Order (MVP)

Build in this order to have a working system at each step:

1. **Backend skeleton** — HTTP server, health check, config loading, in-cluster k8s client
2. **Auth system** — Local accounts with JWT, login/logout endpoints, auth middleware
3. **Resource listing** — Informer-backed list/get for pods, deployments, services, namespaces
4. **Frontend skeleton** — Fresh app with layout, sidebar, login page, dashboard home
5. **Resource browser** — Tables for pods, deployments, services with real-time WebSocket updates
6. **Resource detail** — Detail views with tabs (Overview, YAML, Events, Metrics placeholder)
7. **YAML apply** — Monaco editor, validation, diff, server-side apply
8. **Resource creation wizards** — Deployment wizard, Service wizard
9. **Monitoring integration** — Prometheus discovery/deploy, Grafana embed, performance tabs
10. **CSI/CNI wizards** — Storage and networking configuration
11. **Alerting** — Alertmanager webhook receiver, SMTP email notifications
12. **OIDC/LDAP auth** — SSO integration
13. **Helm chart** — Full production Helm chart with all configuration options
14. **Audit logging** — Comprehensive audit trail
15. **Polish** — Error handling, loading states, empty states, dark mode, keyboard shortcuts

---

## Multi-Cluster Preparation (Phase 2 Hooks)

Even in Phase 1, structure the code to support multi-cluster later:

- **Backend:** All k8s client operations accept a `clusterID` parameter (defaults to `"local"` in Phase 1). The client factory returns a client for the given cluster ID. In Phase 1, there is only one entry in the cluster registry.
- **Frontend:** The top bar includes a cluster selector component (disabled/hidden in Phase 1 with only one cluster). All API calls include a `X-Cluster-ID` header.
- **Database:** If any persistent state is added (audit logs, user preferences, alert history), include a `cluster_id` column from day one.
- **Helm:** The values.yaml includes a `clusters` array (with one entry in Phase 1) anticipating remote cluster kubeconfig registration.

---

## Testing Strategy

- **Backend unit tests:** Test each resource handler, auth provider, and monitoring client in isolation. Mock the k8s clientset using `k8s.io/client-go/kubernetes/fake`.
- **Backend integration tests:** Use `envtest` (from controller-runtime) to spin up a real API server for testing against actual k8s behavior.
- **Frontend tests:** Deno's built-in test runner for utility functions. Component tests with Preact Testing Library.
- **E2E tests:** Use a `kind` cluster with Playwright or Cypress driving the browser. Test the full wizard→apply→verify cycle.
- **Helm tests:** `helm lint`, `helm template` validation, and `helm test` hooks.

---

## Security Checklist (Enforce During Development)

- [ ] All API endpoints require authentication (except `/api/v1/auth/login`, `/api/v1/auth/oidc/callback`, `/health`, `/ready`)
- [ ] All user-initiated k8s operations use impersonation (never the service account's own permissions)
- [ ] Secret values are masked in all API responses and audit logs
- [ ] CSRF protection on all state-changing endpoints
- [ ] Rate limiting on auth endpoints (5 attempts/min per IP)
- [ ] Input validation on all API inputs (max lengths, allowed characters, k8s name regex)
- [ ] Container images run as non-root (UID 65534)
- [ ] No shell in production container images (distroless)
- [ ] Helm chart deploys NetworkPolicy restricting pod traffic
- [ ] TLS between all components (backend↔frontend, backend↔Prometheus, backend↔Grafana)
- [ ] JWT secrets are generated at install time and stored in k8s Secrets
- [ ] RBAC: ClusterRole has minimum required permissions with explicit resource lists (no wildcards)
- [ ] Audit log captures all write operations and secret accesses
- [ ] CSP headers prevent XSS via injected scripts
- [ ] WebSocket connections authenticated with same JWT as REST
