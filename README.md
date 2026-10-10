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
| Dashboard summary | Yes | Partial | Node, pod and service counts and capacity, each with a per-section coverage row (opt-in `?coverage=1`). CPU/memory usage comes from the cluster's metrics binding (`PUT /clusters/{id}/metrics`) and reads `metrics_not_configured` without one. Alert counts and the health score are shown as unavailable, never as 0 |
| Resource lists and detail | Yes | Yes | Every kind on the resource pages (Deployments, Pods, Nodes, Services, ...) and the dashboard list widgets are read from the selected cluster as the user. A remote list is read whole, up to 5,000 objects, and paged by k8sCenter; a longer list is served up to that cap and marked truncated. Secrets keep their own masked route |
| Resource counts | Yes | Yes | Per-kind access checks run on the selected cluster and each kind the user may list is counted by a bounded direct read; a kind cut off at the cap is reported as read with the response marked truncated |
| Cluster info | Yes | Yes | Version, platform and node count read from the selected cluster as the user; the node count is omitted rather than guessed when the user may not list nodes |
| Namespace limits | Yes | Yes | ResourceQuotas and LimitRanges read from the selected cluster as the user and cached briefly per user. The remote read is cluster-wide, so an account allowed only in some namespaces is refused there where the local path filters per namespace |
| Vulnerability reports | Yes | Yes | Trivy and Kubescape reports read as the user on the selected cluster, with scanner presence detected there. The local discovery loop still probes scanner namespaces with the service account for the local status only |
| Pod exec | Yes | No | `k8s/resources/pods.go` (501) |
| Live log stream | Yes | No | `server/handle_ws_logs.go` (WebSocket close) |
| Log search | Yes | No | `server/handle_ws_logs_search.go` (WebSocket close) |
| Network flow stream | Yes | No | `server/handle_ws_flows.go` (WebSocket close) |
| External Secrets write actions | Yes | No | `externalsecrets/actions.go` (501) |
| Node drain | Yes | Yes | Runs to completion on the selected cluster after the 202, and is cancelled if that cluster is removed |
| GitOps applications and sync | Yes | Yes | Argo CD and Flux lists, detail, sync, suspend and rollback |
| Velero backups and restores | Yes | Yes | Backups, restores, schedules, locations and their actions. Backup logs are a download link issued by the remote cluster's object storage, so your browser must be able to reach that storage |
| Volume snapshots | Yes | Yes | List, detail, create and delete |
| CSI drivers and StorageClasses | Yes | Yes | Both lists, and each driver's expansion and snapshot capabilities, are read from the selected cluster |
| Flux notifications | Yes | Yes | Providers, Alerts and Receivers. Providers and Alerts are read and written at `v1beta3`, or at `v1beta2` on a remote that serves only that (Flux 2.0). On Flux 2.0, Provider types added in later Flux releases are refused. Older Flux (0.x) shows as not installed |
| Alert rules | Yes | Yes | PrometheusRule objects only. Whether they fire depends on the remote running prometheus-operator; the active and history alert feeds are always the local Alertmanager's, and the Alerts page says so under a remote selection |
| Gateway API views | Yes | Yes | GatewayClasses, Gateways and routes (read-only on every cluster) |
| Service mesh routing | Yes | Yes | Istio and Linkerd routes, with mesh presence detected on the remote |
| Service mesh mTLS posture | Yes | Partial | Derived from the remote's pods and policies. The Prometheus metric cross-check is reported unavailable |
| External Secrets views | Yes | Yes | Lists, detail and path discovery. Drift shows as unknown on remote, never as in sync |
| cert-manager certificates and issuers | Yes | Yes | Certificates, issuers, cluster issuers, the expiring list, detail, renew and re-issue, with cert-manager presence detected on the remote. Expiry notifications come from the local poller only |
| Policy views and compliance | Yes | Yes | Kyverno and Gatekeeper policies, violations and the compliance score, with engine presence detected on the remote. A remote list that cannot be read fails the view rather than undercounting violations |
| Cilium CNI configuration | Yes | No | `networking/handler.go` (501) |
| Service mesh golden signals | Yes | No | `servicemesh/handler.go` (reported unavailable: the signals come from the local Prometheus) |
| External Secrets sync history | Yes | No | `externalsecrets/history_handler.go` and `externalsecrets/detail_evidence.go` (501: recorded for the local cluster only) |
| External Secrets store metrics | Yes | No | `externalsecrets/metrics.go` (reported unavailable: the rate comes from the local Prometheus) |
| Resource topology graph | Yes | Yes | Built from the selected cluster's objects as the user; a kind the user may not list, or one past the read cap, is left out as it is locally. The mesh overlay is local-only and is refused on a remote selection |
| Resource diagnostics and blast radius | Yes | Yes | The target, its pods and the blast-radius graph are read from the selected cluster as the user; a pod list past the read cap is reported as a limitation rather than undercounted |
| Policy compliance history | Yes | No | `policy/handler.go` (501: daily snapshots are recorded for the local cluster only) |
| Tracked change receipts | Yes | Yes | Receipts are stored by this installation and listed or read for any cluster by their owner. Verifying a receipt and resolving GitOps ownership run against the cluster the receipt recorded, not the selected one, and live access to a remote cluster needs admin (a non-admin still reads a stored final verdict, redacted). Cluster IDs are random per registration, so a receipt is never verified against a re-registered cluster |
| Backup assurance | Yes | No | `velero/assurance_handler.go` (501 `remote_assurance_unsupported`: status, exceptions and policies are collected and stored for the local cluster only) |
| Incident evidence capture | Yes | No | `incidents/handler_capture.go` (400 `remote_capture_unsupported`: evidence is collected from the local cluster's diagnostics, objects and events). Reading, sharing and exporting an incident work under any selection: every evidence item is re-authorized against the cluster recorded on it, never the selected one |
| Prometheus queries | Yes | Yes | With a per-cluster metrics binding (`PUT /clusters/{id}/metrics`); without one the route answers 404 `metrics_not_configured`. A remote cluster is never answered from the local Prometheus |
| Dashboard trends | Yes | Yes | With a per-cluster metrics binding (`PUT /clusters/{id}/metrics`); without one the route answers 404 `metrics_not_configured` and the cards render without sparklines |

Remote pages get no live updates: the WebSocket feed carries the local cluster's informer events only. Refresh the page to see changes on a remote cluster.

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

Export the shared environment once, in the shell you will run every step from. Teardown (step 6) needs the same variables as create: it logs in to deregister, and it must target the same remote context.

```sh
export KUBECENTER_URL=https://<k8scenter>
export KUBECENTER_ADMIN_USER=<admin>
read -rs KUBECENTER_ADMIN_PASSWORD && export KUBECENTER_ADMIN_PASSWORD   # bash/zsh; keeps it out of shell history
export KUBECENTER_REMOTE_REGISTRATION_FILE="$HOME/.k8scenter-remote-registration.json"
# EITHER a kind cluster the script creates on a host with a public address:
export KUBECENTER_REMOTE_API_HOST=<public-name-of-kind-host>
# OR an existing public cluster (leave KUBECENTER_REMOTE_API_HOST unset):
# export KUBECENTER_REMOTE_CONTEXT=<context>
```

1. Build and register the fixture with `sh scripts/test-remote-capabilities.sh`. It prints `K8SCENTER_REMOTE_CLUSTER_ID=<id>`. It refuses to run, and changes nothing, if the remote already has an object with one of the fixture's names that does not carry the fixture label, or if a kind cluster named `k8scenter-remote` exists that the script did not create. The fixture's probe token can impersonate any user but only the admin's own Kubernetes groups plus `system:authenticated`, never `system:masters`.
2. Export that id and the spec's own variables, then run the suite:
   ```sh
   export K8SCENTER_REMOTE_CLUSTER_ID=<id>
   export K8SCENTER_REMOTE_REGISTRATION_FILE="$KUBECENTER_REMOTE_REGISTRATION_FILE"
   export K8SCENTER_LOCAL_KUBE_CONTEXT=<local-context>
   cd e2e && npm test
   ```
   All eight remote specs must pass, alongside the existing suite. The eviction spec deletes the registration and re-registers the cluster, then deregisters that replacement itself, so after the run the id from step 1 no longer exists.
3. Check AE2 by hand: preview the remote-only `Widget` on the YAML page, switch the UI to the local cluster, and confirm that Apply still targets the remote cluster. Confirm the object exists there, and that `kubectl --context <local> get widgets.k8scenter.test` reports the kind does not exist locally. If step 2 already removed the registration, re-register first with `sh scripts/test-remote-capabilities.sh` and use the new id it prints.
4. Check AE3 by hand: with no Prometheus on the remote, the dashboard shows node and pod counts, explicit metrics-unavailable cards, and **no** health score or gauge.
5. Check eviction: delete the cluster in Settings → Clusters, re-register it, and confirm the remote-only CRD resolves under the new id while the old id does not. **Note the new id**: it is the registration teardown must remove.
6. Tear down, in the same shell, with the id of the registration that is still live (the new id from step 5, not the one from step 1):
   ```sh
   KUBECENTER_REMOTE_CLUSTER_ID=<current-id> sh scripts/test-remote-capabilities.sh --teardown
   rm -f "$KUBECENTER_REMOTE_REGISTRATION_FILE"   # it holds a token
   ```
   Teardown deregisters that id, then deletes only objects labelled `app.kubernetes.io/managed-by=k8scenter-e2e-remote-fixture`, selected by label. It deletes the kind cluster only if the cluster carries the ownership marker the script writes when it creates it (ConfigMap `kube-system/k8scenter-remote-fixture-owner`); a same-named cluster without the marker is left in place and the script says why. With `KUBECENTER_REMOTE_CONTEXT` set, no cluster is ever deleted.

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
