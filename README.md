# KubeCenter

A web-based Kubernetes management platform that delivers vCenter-level functionality for Kubernetes clusters. Deploy inside your cluster via Helm and manage everything through a browser.

## Features

- **Resource detail views** with tabbed interface (Overview, YAML, Events, Metrics) for all 18 resource types
- **YAML apply** with Monaco editor, server-side apply, validation, diff, and multi-document support
- **Real-time cluster view** with WebSocket-powered live updates
- **GUI-driven wizards** for deployments, services, storage (CSI), and networking (CNI)
- **Integrated monitoring** via Prometheus and Grafana with auto-discovery, PromQL proxy, and Grafana dashboard embedding
- **RBAC-aware multi-tenancy** with user impersonation (OIDC, LDAP, local accounts)
- **Full YAML escape hatch** with Monaco editor, validation, diff, and server-side apply
- **Pod management** including logs, exec terminal, and resource metrics *(planned)*
- **Alerting** via Alertmanager with email notifications *(planned)*
- **Audit logging** for all write operations and secret access
- **Multi-cluster ready** architecture (single-cluster in Phase 1)

## Architecture

```
Kubernetes Cluster
+--------------------------------------------------+
|  +------------+     +------------------+         |
|  |  Frontend   |---->|     Backend      |         |
|  |  Deno/Fresh |     |    Go 1.26       |         |
|  |  Port 8000  |     |    Port 8080     |         |
|  +------------+     +--------+---------+         |
|                              |                    |
|                  +-----------+-----------+        |
|                  |           |           |        |
|              +---+---+ +----+---+ +-----+---+   |
|              | K8s   | | Prom   | | Grafana  |   |
|              | API   | | etheus | |          |   |
|              +-------+ +--------+ +----------+   |
+--------------------------------------------------+
```

| Layer | Technology |
|---|---|
| Backend API | Go 1.26, chi router, client-go |
| Frontend | Deno 2.x, Fresh 2.x, Preact, Tailwind v4 |
| Monitoring | Prometheus + Grafana (kube-prometheus-stack) |
| Auth | JWT + OIDC / LDAP / local (Argon2id) |
| Deployment | Helm 3.x chart |
| Container | Distroless (Go), Deno slim (frontend) |

## Quick Start

### Prerequisites

- Go 1.26+
- Deno 2.x+
- Docker + Docker Compose
- [kind](https://kind.sigs.k8s.io/) or k3s for local testing
- Helm 3.x
- kubectl

### Local Development

```bash
# Create a local kind cluster (or use existing k3s)
kind create cluster --name kubecenter

# Start the backend (connects via kubeconfig)
make dev-backend

# Start the frontend (in a separate terminal)
make dev-frontend
# Frontend at http://localhost:5173 — proxies /api/* to backend at :8080

# Set up the first admin account
curl -X POST http://localhost:8080/api/v1/setup/init \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme","setupToken":"your-token"}'

# Login via browser at http://localhost:5173/login
# Or via CLI:
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -H "X-Requested-With: XMLHttpRequest" \
  -d '{"username":"admin","password":"changeme"}'
```

### Deploy to Cluster

```bash
helm install kubecenter ./helm/kubecenter
```

## Build

```bash
make build            # Build both backend and frontend
make build-backend    # Build Go binary
make build-frontend   # Build Fresh frontend (outputs to _fresh/)
make test             # Run all tests (backend + frontend)
make test-backend     # Run Go tests with race detection
make test-frontend    # Run Deno tests
make lint             # Lint both backend and frontend
make lint-backend     # Run go vet
make lint-frontend    # Run deno lint + deno fmt --check
make docker-build     # Build container images for both
make helm-lint        # Lint Helm chart
```

## API

All endpoints are prefixed with `/api/v1`. Responses use a standard envelope:

```json
{
  "data": { ... },
  "metadata": { "total": 42, "continue": "token" },
  "error": null
}
```

Key endpoints:

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | No | Liveness probe |
| GET | `/readyz` | No | Readiness probe |
| POST | `/api/v1/setup/init` | No | Create first admin account (one-time) |
| POST | `/api/v1/auth/login` | No | Login, returns JWT access token |
| POST | `/api/v1/auth/refresh` | No | Refresh access token (httpOnly cookie) |
| POST | `/api/v1/auth/logout` | No | Invalidate refresh token |
| GET | `/api/v1/auth/providers` | No | List configured auth providers |
| GET | `/api/v1/auth/me` | Yes | Current user info + RBAC summary |
| GET | `/api/v1/cluster/info` | Yes | Cluster version, node count, KubeCenter version |

Resource CRUD (18 types: deployments, statefulsets, daemonsets, pods, services, ingresses, configmaps, secrets, namespaces, nodes, pvcs, jobs, cronjobs, networkpolicies, roles, clusterroles, rolebindings, clusterrolebindings):

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/resources/:kind` | Yes | List across all namespaces |
| GET | `/api/v1/resources/:kind/:namespace` | Yes | List in namespace |
| GET | `/api/v1/resources/:kind/:namespace/:name` | Yes | Get specific resource |
| POST | `/api/v1/resources/:kind/:namespace` | Yes | Create resource |
| PUT | `/api/v1/resources/:kind/:namespace/:name` | Yes | Update resource |
| DELETE | `/api/v1/resources/:kind/:namespace/:name` | Yes | Delete resource |
| POST | `/api/v1/resources/nodes/:name/cordon` | Yes | Cordon node |
| POST | `/api/v1/resources/nodes/:name/drain` | Yes | Drain node (async, returns task ID) |
| GET | `/api/v1/tasks/:taskID` | Yes | Poll long-running task status |

Monitoring:

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/api/v1/monitoring/status` | Yes | Prometheus + Grafana discovery status |
| POST | `/api/v1/monitoring/rediscover` | Yes | Trigger immediate re-discovery |
| GET | `/api/v1/monitoring/query` | Yes | Proxy PromQL instant query |
| GET | `/api/v1/monitoring/query_range` | Yes | Proxy PromQL range query |
| GET | `/api/v1/monitoring/dashboards` | Yes | List provisioned Grafana dashboards |
| GET | `/api/v1/monitoring/templates` | Yes | List available PromQL templates |
| GET | `/api/v1/monitoring/templates/query` | Yes | Render and execute a named template |
| GET | `/api/v1/monitoring/resource-dashboard` | Yes | Dashboard mapping for a resource kind |
| ALL | `/api/v1/monitoring/grafana/proxy/*` | Yes | Reverse proxy to Grafana (path-allowlisted) |

YAML operations:

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/api/v1/yaml/validate` | Yes | Validate YAML via dry-run apply |
| POST | `/api/v1/yaml/apply` | Yes | Server-side apply (multi-doc) |
| POST | `/api/v1/yaml/diff` | Yes | Dry-run diff against current state |
| GET | `/api/v1/yaml/export/:kind/:ns/:name` | Yes | Export resource as clean YAML |

WebSocket:

| Endpoint | Auth | Description |
|---|---|---|
| `WS /api/v1/ws/resources` | JWT (first message) | Subscribe to real-time resource events |

See [CLAUDE.md](CLAUDE.md) for the complete API reference.

## Security

KubeCenter follows a strict security model:

- All user-initiated Kubernetes API calls use **user impersonation** so that cluster RBAC is enforced server-side
- The service account has **read-only** access for informer caches plus impersonation permissions
- Secrets are **never cached** in-process; they are fetched on-demand via the impersonated client
- JWT access tokens are held in memory only (not localStorage); refresh tokens use httpOnly cookies
- Containers run as **non-root** (UID 65534) with read-only root filesystem and all capabilities dropped
- All write operations and secret accesses are **audit logged**

See [SECURITY.md](SECURITY.md) for the full security policy and vulnerability reporting.

## Project Structure

```
kubecenter/
├── backend/              # Go 1.26 backend
│   ├── cmd/kubecenter/   # Entrypoint
│   ├── internal/
│   │   ├── server/       # HTTP server, routes, handlers
│   │   │   └── middleware/ # Auth, CSRF, rate limiting, CORS
│   │   ├── auth/         # JWT, local accounts, RBAC, sessions
│   │   ├── audit/        # Audit logging interface + slog impl
│   │   ├── websocket/    # WebSocket hub, client, events (gorilla/websocket)
│   │   ├── httputil/      # Shared HTTP response helpers
│   │   ├── k8s/          # Client factory, informers, resource handlers
│   │   │   └── resources/ # CRUD handlers for 18 k8s resource types
│   │   ├── yaml/         # YAML parse, validate, apply, diff, export
│   │   ├── monitoring/   # Prometheus/Grafana discovery, proxy, dashboards
│   │   └── config/       # App configuration
│   └── pkg/              # Public packages (api types, version)
├── frontend/             # Deno 2.x + Fresh 2.x frontend
│   ├── routes/           # Pages, layout, middleware, BFF proxy
│   ├── islands/          # Interactive components (Dashboard, Login, ResourceTable, ResourceDetail, YamlEditor, Monitoring)
│   ├── components/       # Server-rendered UI components
│   └── lib/              # API client, auth state, types, constants
├── helm/kubecenter/      # Helm chart
├── todos/                # Tracked findings and improvements
├── .github/workflows/    # CI pipeline
└── plans/                # Implementation plans
```

## Contributing

1. Fork the repository
2. Create a feature branch from `main`
3. Follow the commit convention: `feat(scope): description`, `fix(scope): description`
4. Ensure `make lint` and `make test` pass
5. **Smoke test against the homelab cluster before merging** (see [CLAUDE.md](CLAUDE.md#pre-merge-requirements))
6. Submit a pull request

## License

[Apache License 2.0](LICENSE)
