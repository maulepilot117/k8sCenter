# k8sCenter

A web-based Kubernetes management platform that delivers vCenter-level functionality. Deploy inside your cluster via Helm and manage everything through a browser.

## Features

**Cluster Management**
- Resource detail views with tabbed interface (Overview, YAML, Events, Metrics) for 37 resource types
- GUI-driven wizards for deployments, services, storage (CSI), networking (CNI), namespaces, and policy creation (17 wizard types)
- YAML apply with Monaco editor, server-side apply, validation, diff, and multi-document support
- Real-time WebSocket-powered live updates across resources, alerts, and network flows
- Resource action buttons (scale, restart, delete, suspend, trigger) with type-to-confirm for destructive actions
- Pod management: logs, exec terminal, resource metrics

**Observability**
- Integrated Prometheus + Grafana with auto-discovery, PromQL proxy, and 7 provisioned dashboards
- Log Explorer with Loki integration — search, filter, live tail (WebSocket), LogQL, volume histogram
- Resource topology graph — interactive SVG dependency DAG with health propagation and zoom/pan
- Diagnostic workspace — automated health checks with blast radius analysis via topology BFS
- Alerting via Alertmanager webhook with SMTP notifications, PrometheusRule CRUD, and real-time alert banner

**Security & Governance**
- RBAC-aware multi-tenancy with user impersonation (OIDC, LDAP, local accounts)
- Policy engine integration — auto-detects Kyverno and/or OPA/Gatekeeper, compliance scoring with trend tracking
- Security scanning — Trivy Operator + Kubescape (vulnerability reports, config audits, compliance frameworks)
- Cert-manager integration — certificate inventory, issuer management, expiry dashboard, one-click renew/re-issue, proactive expiry notifications, per-cert / per-issuer warning + critical threshold overrides via annotation
- Service mesh observability (Istio + Linkerd) — auto-detection, traffic-routing CRD inventory, mTLS posture per workload, golden signals (RPS / error rate / p50/p95/p99 latency) on Service detail, opt-in mesh-edge overlay on the topology graph
- External Secrets Operator integration — observatory (ExternalSecret / SecretStore / ClusterSecretStore / PushSecret inventory), drift detection with Revert action, persistent sync history with diff view, bulk refresh actions with scope-pinned execution, per-store rate + cost-tier panel, annotation-driven staleness/recovery/lifecycle thresholds, per-provider SecretStore wizards (Vault / AWS / Azure / GCP / Kubernetes / Doppler / 1Password) plus YAML templates for 11 additional providers (Akeyless, Bitwarden Secrets Manager, CyberArk Conjur, Infisical, Pulumi ESC, Passbolt, Keeper, Onboardbase, Oracle Cloud Vault, Alibaba KMS, generic webhook), chain topology overlay
- Audit logging with PostgreSQL persistence, filterable viewer, and 90-day retention
- Frontend permission gating via SelfSubjectRulesReview
- CSP headers, NetworkPolicy, Pod Security Standards (restricted profile)

**GitOps**
- Argo CD + Flux CD auto-detection with unified application listing and sync/health status
- Argo CD ApplicationSet support with CRUD actions
- Flux Notification Controller support (Provider, Alert, Receiver CRUD)
- GitOps actions: sync, suspend/resume, rollback with real-time WebSocket status updates

**Multi-Cluster**
- Cluster routing via X-Cluster-ID header with encrypted credential storage
- SSRF-protected registration, background health probing (60s), connection testing
- Admin role required for non-local clusters

**Networking**
- Cilium Network Policy editor with rule table, YAML preview, and dangerous policy warnings
- Hubble network flow visibility with real-time gRPC-to-WebSocket streaming

## Mobile App

Native iOS and Android oncall companion for k8sCenter — full read-side parity with the web app, safe write actions including all 28 wizards, OIDC sign-in, push notifications, WCAG 2.2 AA accessibility, opt-in crash reporting.

<!-- TODO(post-launch): swap placeholder install links once Apple assigns the App Store numeric ID and Play production-track rollout reaches 100%. App Store URL becomes https://apps.apple.com/app/id<numeric>; Play URL is stable today. -->

[![Download on the App Store](https://developer.apple.com/assets/elements/badges/download-on-the-app-store.svg)](https://apps.apple.com/app/k8scenter)
[![Get it on Google Play](https://play.google.com/intl/en_us/badges/static/images/badges/en_badge_web_generic.png)](https://play.google.com/store/apps/details?id=io.kubecenter.kubecenter)

Source under [`mobile/`](mobile/). Build setup and release runbook in [`mobile/docs/RELEASE.md`](mobile/docs/RELEASE.md). Privacy policy at [`mobile/docs/APP_PRIVACY.md`](mobile/docs/APP_PRIVACY.md) and on the web at [/privacy](frontend/routes/privacy.tsx).

## Architecture

```
Kubernetes Cluster
+-----------------------------------------------------------+
|  +----------+     +-----------+     +------------+        |
|  | Frontend  |---->|  Backend  |---->| PostgreSQL |        |
|  | Deno/Fresh|     |  Go 1.26  |     +------------+        |
|  | :8000     |     |  :8080    |                           |
|  +----------+     +-----+-----+                           |
|                         |                                  |
|            +------------+------------+                     |
|            |            |            |                     |
|        +---+---+  +----+----+  +----+----+                |
|        | K8s   |  | Prom +  |  |  Loki   |                |
|        | API   |  | Grafana |  |         |                |
|        +-------+  +---------+  +---------+                |
+-----------------------------------------------------------+
```

| Layer | Technology |
|---|---|
| Backend API | Go 1.26, chi router, client-go v0.35.2 |
| Frontend | Deno 2.x, Fresh 2.x (Preact), Tailwind v4 |
| Database | PostgreSQL (pgx/v5, golang-migrate) |
| Monitoring | Prometheus + Grafana (kube-prometheus-stack) |
| Logs | Loki (LogQL proxy, namespace enforcement) |
| Certificates | cert-manager (CRD discovery, expiry poller, per-cert/per-issuer threshold annotations) |
| Service Mesh | Istio + Linkerd (mTLS posture, golden signals, topology overlay) |
| External Secrets | External Secrets Operator (drift detection, sync history, bulk refresh, per-store rate / cost-tier, chain overlay) |
| Auth | JWT + OIDC / LDAP / local (Argon2id) |
| Deployment | Helm 3.x, distroless containers |

## Quick Start

### Prerequisites

- Go 1.26+, Deno 2.x+, Docker, Helm 3.x, kubectl
- [kind](https://kind.sigs.k8s.io/) or k3s for local development

### Local Development

```bash
# Create a local cluster
kind create cluster --name kubecenter

# Start PostgreSQL, backend, and frontend
make dev-db
make dev-backend    # KUBECENTER_DEV=true
make dev-frontend   # http://localhost:5173 -> proxies /api/* to :8080

# Initialize the first admin account
curl -X POST http://localhost:8080/api/v1/setup/init \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme","setupToken":"your-token"}'
```

### Deploy to Cluster

```bash
# Basic install
helm install kubecenter ./helm/kubecenter

# With ingress and monitoring
helm install kubecenter ./helm/kubecenter \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=k8scenter.example.com \
  --set monitoring.deploy=true
```

## Build

```bash
make build          # Build backend + frontend
make test           # Run all tests (Go + Deno)
make lint           # Lint both (go vet + deno lint/fmt)
make test-e2e       # Playwright E2E (95 tests against kind)
make docker-build   # Container images
make helm-lint      # Validate Helm chart
```

## Documentation

See the [wiki](https://github.com/maulepilot117/k8sCenter/wiki) for detailed documentation:

- **[API Reference](https://github.com/maulepilot117/k8sCenter/wiki/API-Reference)** — full endpoint listing with auth requirements
- **[Architecture](https://github.com/maulepilot117/k8sCenter/wiki/Architecture)** — project structure, design decisions, package layout
- **[Security](SECURITY.md)** — security model, vulnerability reporting

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow. In short:

1. Branch from `main` (`feat/`, `fix/`, `refactor/`)
2. Ensure `make lint` and `make test` pass
3. Submit a PR — CI + E2E must be green before merge

## License

[Apache License 2.0](LICENSE)
