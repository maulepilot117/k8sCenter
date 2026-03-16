# Phase 2: Production-Grade Multi-Cluster Management

## Overview

Transform k8sCenter from a single-cluster MVP into a production-grade, multi-cluster Kubernetes management console. This phase adds PostgreSQL persistence, multi-cluster support with direct kubeconfig registration, persistent UI-configurable settings, a visual Cilium network policy editor (NSX-T-style), complete resource coverage, pod logs/exec, and Hubble flow integration.

---

## Build Order

| Step | Name | Depends On | Effort |
|------|------|------------|--------|
| 16 | PostgreSQL Migration | — | Done (#29) |
| 17 | Persistent Settings Store | 16 | Medium |
| 18 | Multi-Cluster Registry | 16, 17 | Large |
| 19 | Complete Resource Coverage | — | Medium |
| 20 | Pod Logs & Exec | 19 | Medium |
| 21 | Cilium Network Policy Editor | 19 | Large |
| 22 | Hubble Flow Integration | 18, 21 | Medium |
| 23 | E2E Tests & Production Hardening | All | Medium |

---

## Step 16: PostgreSQL Migration

**Replace SQLite with PostgreSQL for all persistence.**

### Backend Changes

**New dependency:** `github.com/jackc/pgx/v5` (pure Go, no CGO — replaces `modernc.org/sqlite`)

**New package:** `backend/internal/store/`
- `store.go` — pgxpool wrapper, connection management, migration runner
- `migrations/` — embedded SQL migration files via `//go:embed`
  - `000001_create_audit_logs.up.sql`
  - `000001_create_audit_logs.down.sql`

**Migration tool:** `github.com/golang-migrate/migrate/v4` with `iofs` source driver (embedded in binary)

**Config changes:**
```go
type DatabaseConfig struct {
    URL      string `koanf:"url"`      // postgresql://user:pass@host:5432/db?sslmode=require
    MaxConns int    `koanf:"maxconns"` // default: 10
    MinConns int    `koanf:"minconns"` // default: 2
}
// Env: KUBECENTER_DATABASE_URL, KUBECENTER_DATABASE_MAXCONNS
```

**Audit store rewrite:**
- Replace `database/sql` + SQLite with `pgxpool` + PostgreSQL
- Change `?` placeholders to `$1, $2` positional params
- Replace `TEXT` timestamps with `TIMESTAMPTZ`
- Replace `INTEGER PRIMARY KEY AUTOINCREMENT` with `BIGSERIAL PRIMARY KEY`
- Remove SQLite PRAGMAs (WAL, busy_timeout)

**Helm chart:**
- Add Bitnami PostgreSQL subchart (`condition: postgresql.enabled`)
- Add `externalDatabase.*` values for bring-your-own PostgreSQL
- Construct `KUBECENTER_DATABASE_URL` in deployment template from subchart or external values
- Remove `replicaCount: 1` constraint (PostgreSQL supports concurrent writers)

**Dev setup:**
- Add `docker-compose.yml` with PostgreSQL for local development
- Add `make dev-db` target to start/stop local PostgreSQL

### Acceptance Criteria
- [ ] Audit logs stored in PostgreSQL
- [ ] Migrations run automatically on startup
- [ ] `helm install` with `postgresql.enabled=true` deploys working stack
- [ ] External PostgreSQL works via `externalDatabase.*` values
- [ ] `modernc.org/sqlite` removed from go.mod
- [ ] `replicaCount: 2+` works without data issues

---

## Step 17: Persistent Settings Store

**Make all admin-configurable settings persistent and UI-editable.**

### Database Schema

```sql
-- Single-row global settings (env vars provide defaults, DB overrides)
CREATE TABLE app_settings (
    id                          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    monitoring_prometheus_url   TEXT,
    monitoring_grafana_url      TEXT,
    monitoring_grafana_token    TEXT,
    monitoring_namespace        TEXT,
    alerting_enabled            BOOLEAN,
    alerting_smtp_host          TEXT,
    alerting_smtp_port          INTEGER,
    alerting_smtp_username      TEXT,
    alerting_smtp_password      TEXT,
    alerting_smtp_from          TEXT,
    alerting_rate_limit         INTEGER,
    alerting_recipients         TEXT[],
    updated_at                  TIMESTAMPTZ DEFAULT NOW()
);

-- Auth provider configs (OIDC/LDAP, managed via UI)
CREATE TABLE auth_providers (
    id              TEXT PRIMARY KEY,
    provider_type   TEXT NOT NULL CHECK (provider_type IN ('oidc', 'ldap')),
    display_name    TEXT NOT NULL,
    config_json     JSONB NOT NULL,
    enabled         BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);
```

### Settings Override Hierarchy

1. Hardcoded defaults (lowest priority)
2. YAML config file
3. Environment variables
4. **Database values (highest priority)**

### Backend

- `backend/internal/store/settings.go` — SettingsService with Get/Update + in-memory cache
- Settings changes take effect immediately (no restart)
- Auth provider CRUD: create/update/delete OIDC/LDAP providers via Settings UI
- Hot-reload: provider registry rebuilt on settings change

### Frontend

- Expand Settings > Authentication to full CRUD for OIDC/LDAP providers
- Add Settings > Monitoring (Prometheus/Grafana URLs)
- Add Settings > Alerting (SMTP config, recipients)
- All settings forms save to database via `PUT /api/v1/settings`

### Acceptance Criteria
- [ ] Admin can configure OIDC/LDAP providers via UI without restart
- [ ] SMTP/alerting settings persist across pod restarts
- [ ] Monitoring URLs configurable via UI
- [ ] Env vars still work as defaults for fresh installs
- [ ] Settings changes take effect immediately

---

## Step 18: Multi-Cluster Registry

**Central console for managing multiple Kubernetes clusters.**

### Architecture

**Direct kubeconfig registration** (no agent required). Each cluster gets its own `ClientFactory` + `InformerManager`, managed by a `ClusterRegistry`.

```
ClusterRegistry
  ├── "local" → ClusterConnection { ClientFactory, InformerManager, status: connected }
  ├── "prod"  → ClusterConnection { ClientFactory, InformerManager, status: connected }
  └── "staging" → ClusterConnection { ClientFactory, InformerManager, status: disconnected }
```

### Database Schema

```sql
CREATE TABLE clusters (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT,
    api_server_url  TEXT NOT NULL,
    ca_data         BYTEA,              -- encrypted
    auth_type       TEXT NOT NULL,       -- 'token', 'certificate'
    auth_data       BYTEA NOT NULL,     -- encrypted JSON
    status          TEXT DEFAULT 'unknown',
    status_message  TEXT,
    k8s_version     TEXT,
    node_count      INTEGER DEFAULT 0,
    is_local        BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    last_probed_at  TIMESTAMPTZ
);

CREATE TABLE cluster_monitoring (
    cluster_id      TEXT PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    prometheus_url  TEXT,
    grafana_url     TEXT,
    grafana_token   TEXT,
    discovery_src   TEXT DEFAULT 'none',
    discovered_at   TIMESTAMPTZ
);

CREATE TABLE cluster_access (
    user_id         TEXT NOT NULL,
    cluster_id      TEXT NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    access_level    TEXT NOT NULL DEFAULT 'member',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, cluster_id)
);
```

### Backend

**New package:** `backend/internal/cluster/`
- `registry.go` — ClusterRegistry: CRUD, connect/disconnect, health probes
- `connection.go` — ClusterConnection: ClientFactory + InformerManager per cluster
- Credentials encrypted at rest (envelope encryption, KEK from k8s Secret)
- Background health probe loop (60s interval)
- Lazy informer startup (connect on first access, disconnect after idle)

**API endpoints:**
```
GET    /api/v1/clusters              — List registered clusters
POST   /api/v1/clusters              — Register new cluster (kubeconfig upload or fields)
GET    /api/v1/clusters/{id}         — Cluster details + health
PUT    /api/v1/clusters/{id}         — Update credentials
DELETE /api/v1/clusters/{id}         — Deregister
POST   /api/v1/clusters/{id}/test    — Test connectivity
```

**Routing:** All resource APIs use `X-Cluster-ID` header to select cluster context. The ClusterRegistry returns the correct ClientFactory for the selected cluster.

**WebSocket:** Extend Hub `subKey` to include `clusterID`. Each cluster's informer callbacks tag events with cluster ID.

**Per-cluster monitoring:** Run existing Prometheus/Grafana discovery per cluster. Store results in `cluster_monitoring` table.

### Frontend

- **Cluster selector** in TopBar (replaces static "local" indicator)
- **All Clusters dashboard** at `/` showing health grid
- **Cluster management page** at `/settings/clusters` (admin-only)
  - Add cluster: kubeconfig file upload or manual fields
  - Test connection before saving
  - Edit credentials, remove cluster
- URL structure: all routes become cluster-scoped (e.g., `?cluster=prod`)
- `selectedCluster` signal replaces `CLUSTER_ID` constant

### Acceptance Criteria
- [ ] Register remote clusters via kubeconfig upload
- [ ] Cluster selector in TopBar switches context
- [ ] All Clusters dashboard shows health overview
- [ ] Per-cluster monitoring auto-discovery
- [ ] WebSocket live updates work per-cluster
- [ ] Credentials stored encrypted
- [ ] RBAC: users see only clusters they have access to

---

## Step 19: Complete Resource Coverage

**Add all remaining Kubernetes resource types.**

### Missing Resource Types

Currently k8sCenter has 18 resource types. Add:

| Resource | API Group | Scope | Priority |
|---|---|---|---|
| ReplicaSets | apps/v1 | Namespaced | High |
| Endpoints | v1 | Namespaced | High |
| EndpointSlices | discovery.k8s.io/v1 | Namespaced | High |
| HorizontalPodAutoscalers | autoscaling/v2 | Namespaced | High |
| PersistentVolumes | v1 | Cluster | High |
| StorageClasses | storage.k8s.io/v1 | Cluster | High (detail view) |
| ResourceQuotas | v1 | Namespaced | Medium |
| LimitRanges | v1 | Namespaced | Medium |
| ServiceAccounts | v1 | Namespaced | Medium |
| PodDisruptionBudgets | policy/v1 | Namespaced | Medium |
| ValidatingWebhookConfigurations | admissionregistration.k8s.io/v1 | Cluster | Low |
| MutatingWebhookConfigurations | admissionregistration.k8s.io/v1 | Cluster | Low |
| CustomResourceDefinitions | apiextensions.k8s.io/v1 | Cluster | Low |
| CiliumNetworkPolicy | cilium.io/v2 | Namespaced | High (Step 21) |
| CiliumClusterwideNetworkPolicy | cilium.io/v2 | Cluster | High (Step 21) |

### Implementation

- Add informer registration for each new type
- Add CRUD handlers following existing pattern
- Add frontend column definitions in `resource-columns.ts`
- Add detail view components for key types (HPA, PV, ResourceQuota)
- Add resource icons

### Acceptance Criteria
- [ ] All core Kubernetes resource types browsable
- [ ] Detail views for HPA, PV, StorageClass, ResourceQuota
- [ ] Column definitions and status badges for all types

---

## Step 20: Pod Logs & Exec

**Add real-time pod log streaming and interactive terminal.**

### Pod Logs
- `GET /api/v1/pods/{ns}/{name}/logs?container=X&follow=true&tail=100`
- SSE (Server-Sent Events) for streaming
- Frontend: LogViewer island with auto-scroll, container selector, follow toggle, search

### Pod Exec
- `WS /api/v1/ws/exec/{ns}/{pod}/{container}`
- WebSocket terminal using xterm.js
- Bidirectional stdin/stdout/stderr + resize
- Backend proxies to k8s API exec endpoint

### Frontend
- LogViewer island with ANSI color support
- Terminal island with xterm.js (Monaco-like monospace editor)
- Accessible from pod detail view tabs

### Acceptance Criteria
- [ ] Stream pod logs in real-time with follow mode
- [ ] Search/filter within log output
- [ ] Interactive terminal (exec) with resize support
- [ ] Multi-container support (container selector)

---

## Step 21: Cilium Network Policy Editor

**NSX-T-style visual policy editor for CiliumNetworkPolicy.**

### UI Design: Rule Table (NSX-T Pattern)

Primary editor is a **tabular rule builder** (not a linear wizard):

```
| # | Direction | Peers          | Ports       | L7 Rules        | Action |
|---|-----------|----------------|-------------|-----------------|--------|
| 1 | Ingress   | [frontend]     | TCP/80,443  | HTTP: GET /api  | Allow  |
| 2 | Egress    | [world]        | TCP/443     | —               | Allow  |
| 3 | Egress    | [kube-dns]     | UDP/53      | DNS: *.svc      | Allow  |
| 4 | Ingress   | [any]          | Any         | —               | Deny   |
```

Each cell is **click-to-edit** (popover), matching NSX-T's interaction pattern.

### Wizard Steps (guided creation)

1. **Policy Scope** — Name, namespace, cluster-wide toggle, description
2. **Applied To** — Label selector with live pod count preview
3. **Ingress Rules** — Rule builder (peer + ports + L7 + action)
4. **Egress Rules** — Same builder for outbound
5. **Review** — Rule table summary + flow diagram + YAML preview
6. **Apply** — Dry-run diff, then apply

### Supported Cilium Features

| Feature | UI Widget |
|---|---|
| Endpoint selectors | Label picker with auto-suggest from cluster inventory |
| Entities (world, cluster, host, kube-apiserver) | Multi-select dropdown |
| CIDR ranges | IP/CIDR input with validation |
| FQDN (egress only) | Domain input with wildcard support |
| Services (egress only) | Service picker from cluster |
| Port + protocol | Port number + TCP/UDP/ANY selector |
| Port ranges | Start port + end port |
| HTTP L7 | Method (regex), path (regex), headers |
| Kafka L7 | Role (produce/consume), topic |
| DNS L7 | matchName, matchPattern |
| ICMP filtering | Type + family (IPv4/IPv6) |
| Allow / Deny | Toggle (deny disables L7 options automatically) |

### Reusable Endpoint Groups

NSX-T "Groups" equivalent — saved label selectors reusable across policies:
- "Frontend Tier" = `{app: frontend, tier: web}`
- "Database Tier" = `{app: postgres, tier: data}`
- Stored in the settings database

### Visual Flow Diagram

Auto-generated read-only diagram below the rule table:
- Center: target pods
- Left: ingress sources with arrows
- Right: egress destinations with arrows
- Green = allow, Red = deny, Dotted = L7 filtered

### Backend

- `backend/internal/cilium/` — CiliumNetworkPolicy CRUD via dynamic client
- Policy validation using dry-run apply
- Label auto-suggest from informer cache (pods, deployments, services)

### Acceptance Criteria
- [ ] Create CiliumNetworkPolicy via NSX-T-style rule table
- [ ] Support all Cilium selector types (endpoints, entities, CIDR, FQDN)
- [ ] L7 rules for HTTP, Kafka, DNS
- [ ] Deny policies with automatic L7 constraint enforcement
- [ ] Flow diagram visualization
- [ ] YAML preview with edit before apply
- [ ] Label auto-suggest from cluster inventory

---

## Step 22: Hubble Flow Integration

**Real-time network flow visibility integrated with the policy editor.**

### Backend
- Discover Hubble Relay service in the cluster
- Proxy Hubble gRPC API (`observer.Observer/GetFlows`)
- Convert flow events to JSON for WebSocket delivery

### Frontend Integration

**In the policy editor:**
- "Observed Flows" panel showing real traffic for the selected endpoints
- Policy verdict overlay: which flows would be allowed/denied by the draft policy
- "Suggest Rules" button: auto-generate allow rules from observed traffic

**Standalone flow viewer:**
- `/networking/flows` — real-time flow table with filters
- Filter by namespace, pod, verdict (forwarded/dropped), protocol
- Directed graph view: nodes = services, edges = flows (like Hubble UI)

### Acceptance Criteria
- [ ] Real-time flow data from Hubble Relay
- [ ] Flow data integrated into policy editor
- [ ] Standalone flow viewer with filters
- [ ] Policy verdict overlay (what-if analysis)

---

## Step 23: E2E Tests & Production Hardening

**End-to-end test suite and final hardening.**

### E2E Tests
- Playwright test suite against a kind cluster
- Test flows: login → browse resources → create deployment → apply YAML → view logs
- Run in CI via GitHub Actions

### Hardening
- `make docker-push` for multi-arch images (amd64 + arm64)
- Helm chart CI: `helm lint` + `helm template` + `helm install --dry-run`
- Rate limit configuration per endpoint
- Connection pool monitoring metrics
- Graceful degradation when PostgreSQL is temporarily unavailable

### Acceptance Criteria
- [ ] Playwright E2E test suite passing in CI
- [ ] Multi-arch container images published
- [ ] Helm chart fully validated in CI
- [ ] Graceful PostgreSQL failover handling

---

## Technology Summary

| Component | Phase 1 | Phase 2 |
|---|---|---|
| Database | SQLite (modernc.org/sqlite) | **PostgreSQL (jackc/pgx/v5)** |
| Migrations | Inline SQL constant | **golang-migrate/migrate with embedded SQL** |
| Cluster support | Single (hardcoded "local") | **Multi-cluster (ClusterRegistry)** |
| Settings | Env vars only (lost on restart) | **PostgreSQL-backed, UI-configurable** |
| Network policy | Standard k8s NetworkPolicy CRUD | **Visual CiliumNetworkPolicy editor (NSX-T style)** |
| Pod management | List, get, delete | **+ Logs (SSE) + Exec (xterm.js WebSocket)** |
| Flow visibility | None | **Hubble Relay integration** |
| Resource types | 18 | **30+ (all core k8s + Cilium CRDs)** |
| Replicas | 1 (SQLite constraint) | **N (PostgreSQL enables HA)** |
| E2E tests | None | **Playwright against kind** |

---

## References

### Internal
- Phase 1 plan: `plans/feat-kubecenter-phase1-mvp.md`
- Existing multi-cluster hooks: `X-Cluster-ID` header, `CLUSTER_ID` constant, `ClientFactory.clusterID`
- Current CNI integration: `backend/internal/networking/`
- Current monitoring: `backend/internal/monitoring/`

### External
- [jackc/pgx v5](https://github.com/jackc/pgx) — PostgreSQL driver
- [golang-migrate](https://github.com/golang-migrate/migrate) — SQL migrations
- [Bitnami PostgreSQL Helm chart](https://artifacthub.io/packages/helm/bitnami/postgresql)
- [Cilium Network Policy docs](https://docs.cilium.io/en/stable/security/policy/language/)
- [Hubble API](https://github.com/cilium/hubble)
- [NSX-T Distributed Firewall](https://techdocs.broadcom.com/us/en/vmware-cis/nsx/nsxt-dc/3-2/administration-guide/security/distributed-firewall/)
- [Headlamp multi-cluster architecture](https://headlamp.dev/docs/latest/development/architecture/)
- [Rancher architecture](https://ranchermanager.docs.rancher.com/reference-guides/rancher-manager-architecture)
- [Open Cluster Management](https://open-cluster-management.io/docs/concepts/architecture/)
