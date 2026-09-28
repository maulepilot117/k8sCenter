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
- Top-bar cluster switcher; every request is pinned to the cluster it was issued against
- Per-operation capability disclosure (`GET /api/v1/capabilities/{clusterId}`) that tells an operator what a cluster supports before they enter data — see [Remote cluster support](#remote-cluster-support)

**Networking**
- Cilium Network Policy editor with rule table, YAML preview, and dangerous policy warnings
- Hubble network flow visibility with real-time gRPC-to-WebSocket streaming

## Remote cluster support

What works against a registered remote cluster, operation by operation. The table mirrors `capabilityOperations` in `backend/internal/server/handle_capabilities.go`, the source the capabilities endpoint and the YAML page read. Each "No" names the guard that refuses the operation, so a reader can check the claim against the code.

| Operation | Local | Remote | Note |
|---|---|---|---|
| Validate YAML | Yes | Yes | Schema resolves against the target cluster's own discovery, never the local one |
| Diff YAML against live state | Yes | Yes | Same target-scoped discovery; Secrets are refused on both |
| Export YAML | Yes | Yes | Same; Secrets are refused on both |
| Apply YAML | Yes | Yes | Apply is pinned to the cluster the preview ran against. A mismatch is refused with 409 (`cluster_pin_mismatch` / `cluster_generation_mismatch`) and nothing is applied |
| Dashboard summary | Yes | Partial | Node, pod and service counts and capacity, each with a per-section coverage row (opt-in `?coverage=1`). CPU/memory usage, alert counts and the health score are shown as unavailable, never as 0: there is no remote metrics binding yet |
| Resource counts | Yes | No | `k8s/resources/counts.go` (400: counts read the local informer cache). List pages say so instead of loading |
| Pod exec | Yes | No | `k8s/resources/pods.go` (501) |
| Live log stream | Yes | No | `server/handle_ws_logs.go` (WebSocket close) |
| Log search | Yes | No | `server/handle_ws_logs_search.go` (WebSocket close) |
| Network flow stream | Yes | No | `server/handle_ws_flows.go` (WebSocket close) |
| External Secrets write actions | Yes | No | `externalsecrets/actions.go` (501) |

Dashboard trends (the sparklines) are local-only as well; the cards render without them.

"Unsupported" is reported only for the rows above marked "No". A remote cluster that is down, or an account without RBAC for an operation, shows as blocked right now or unknown, never as something k8sCenter cannot do.

**Verification status: verified against fixtures only.** Release C's remote paths are covered by unit and handler tests against fake clusters, plus a two-cluster e2e spec (`e2e/tests/remote-capabilities.spec.ts`) that runs only against a real registered remote. That live run has not been executed yet, so no two-cluster evidence is claimed here. The procedure is below; record its result in this section when it has been run.

<details>
<summary>Live two-cluster verification runbook (manual)</summary>

**Prerequisites**

1. Two Kubernetes clusters reachable from the k8sCenter backend. The remote cluster's API server must resolve to a **public** address: registration and every dial refuse loopback, RFC1918, link-local, CGNAT and unspecified addresses (`ValidateRemoteURLContext` / `StrictDialContext`). A laptop kind cluster on 127.0.0.1 or a homelab address on 10.x is refused. That is correct behaviour, not something to work around.
2. The remote cluster's real CA bundle. Do not set `allowInsecureTLS`; the point is to exercise the production TLS path.
3. `kubectl`, `curl` and `jq`, plus `kind` if the script creates the remote cluster.
4. An admin k8sCenter account. Non-local clusters are admin-only.

**Procedure**

1. Build and register the fixture:
   ```sh
   KUBECENTER_URL=https://<k8scenter> \
   KUBECENTER_ADMIN_USER=<admin> KUBECENTER_ADMIN_PASSWORD=<password> \
   KUBECENTER_REMOTE_API_HOST=<public-name-of-kind-host> \
   KUBECENTER_REMOTE_REGISTRATION_FILE=$HOME/.k8scenter-remote-registration.json \
   sh scripts/test-remote-capabilities.sh
   ```
   To use an existing public cluster instead of kind, set `KUBECENTER_REMOTE_CONTEXT=<context>` and leave `KUBECENTER_REMOTE_API_HOST` unset. The script prints `K8SCENTER_REMOTE_CLUSTER_ID=<id>`.
2. Export that id (plus `K8SCENTER_LOCAL_KUBE_CONTEXT` and `K8SCENTER_REMOTE_REGISTRATION_FILE`) and run `cd e2e && npm test`. All eight remote specs must pass, alongside the existing suite.
3. Check AE2 by hand: preview the remote-only `Widget` on the YAML page, switch the UI to the local cluster, and confirm that Apply still targets the remote cluster. Confirm the object exists there, and that `kubectl --context <local> get widgets.k8scenter.test` reports the kind does not exist locally.
4. Check AE3 by hand: with no Prometheus on the remote, the dashboard shows node and pod counts, explicit metrics-unavailable cards, and **no** health score or gauge.
5. Check eviction: delete the cluster in Settings → Clusters, re-register it, and confirm the remote-only CRD resolves under the new id while the old id does not.
6. Tear down with `KUBECENTER_REMOTE_CLUSTER_ID=<id> sh scripts/test-remote-capabilities.sh --teardown`. It deletes only objects labelled `app.kubernetes.io/managed-by=k8scenter-e2e-remote-fixture`, selected by label, then the kind cluster if the script created it. Delete the saved registration file, which holds a token.

**Recording the result:** add the date, both clusters' Kubernetes versions, the identity used, and the pass/fail of each step to this section, and replace "verified against fixtures only" with what was actually verified.

</details>

## Mobile App

Native iOS and Android oncall companion for k8sCenter — full read-side parity with the web app, safe write actions including all 28 wizards, OIDC sign-in, push notifications, WCAG 2.2 AA accessibility, opt-in crash reporting.

<!-- TODO(post-launch): swap placeholder install links once Apple assigns the App Store numeric ID and Play production-track rollout reaches 100%. App Store URL becomes https://apps.apple.com/app/id<numeric>; Play URL is stable today. -->

[![Download on the App Store](https://developer.apple.com/assets/elements/badges/download-on-the-app-store.svg)](https://apps.apple.com/app/k8scenter)
[![Get it on Google Play](https://play.google.com/intl/en_us/badges/static/images/badges/en_badge_web_generic.png)](https://play.google.com/store/apps/details?id=io.kubecenter.kubecenter)

Source under [`mobile/`](mobile/). Build setup and release runbook in [`mobile/docs/RELEASE.md`](mobile/docs/RELEASE.md). Privacy policy at [`mobile/docs/APP_PRIVACY.md`](mobile/docs/APP_PRIVACY.md) and on the web at [/privacy](frontend/src/pages/privacy.astro).

## Architecture

```
Kubernetes Cluster
+-----------------------------------------------------------+
|  +----------+     +-----------+     +------------+        |
|  | Frontend  |---->|  Backend  |---->| PostgreSQL |        |
|  | Bun/Astro |     |  Go 1.26  |     +------------+        |
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
| Frontend | Bun 1.4.x, Astro 7.x (Preact islands), Tailwind v4 |
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

- Go 1.26+, Bun 1.4.x (see `.bun-version`), Docker, Helm 3.x, kubectl
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
make test           # Run all tests (Go + Bun)
make lint           # Lint both (go vet + bun run check)
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
