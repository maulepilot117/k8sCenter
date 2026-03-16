# Steps 17-18: Persistent Settings Store + Multi-Cluster Registry

## Overview

Add a PostgreSQL-backed settings store so all admin configuration persists across restarts and is editable via the UI. Simultaneously add multi-cluster support — a cluster registry where admins register remote clusters via a wizard, and all resource browsing/management is scoped to the selected cluster.

## Problem Statement

1. **Settings are ephemeral** — OIDC/LDAP providers, SMTP config, monitoring URLs reset on pod restart. Admins must redeploy to change config.
2. **Single-cluster only** — k8sCenter can only manage the cluster it's deployed in. Users need a central console for all their clusters.

## Proposed Solution

### Database Schema (2 new migrations)

```sql
-- Migration 000002: app_settings + auth_providers
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

CREATE TABLE auth_providers (
    id              TEXT PRIMARY KEY,
    provider_type   TEXT NOT NULL CHECK (provider_type IN ('oidc', 'ldap')),
    display_name    TEXT NOT NULL,
    config_json     JSONB NOT NULL,
    enabled         BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

-- Migration 000003: clusters + cluster_monitoring
CREATE TABLE clusters (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT,
    api_server_url  TEXT NOT NULL,
    ca_data         BYTEA,
    auth_type       TEXT NOT NULL DEFAULT 'token',
    auth_data       BYTEA NOT NULL,
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
```

### Settings Override Hierarchy

```
Hardcoded defaults → YAML config → Env vars → Database (highest priority)
```

Env vars still work for initial setup. Once an admin saves via the UI, the DB value overrides.

---

## Implementation Plan

### Phase A: Persistent Settings (Step 17)

#### Files to Create

**`backend/internal/store/migrations/000002_create_settings.up.sql`**
**`backend/internal/store/migrations/000002_create_settings.down.sql`**

**`backend/internal/store/settings.go`** — SettingsService
- `SettingsService` struct with pgxpool + in-memory cache
- `Get(ctx) (*Settings, error)` — returns merged view (DB overrides config)
- `Update(ctx, patch SettingsPatch) error` — writes to DB, refreshes cache
- `Settings` struct covering monitoring, alerting, CORS fields
- Cache refreshes on Update or every 60 seconds

**`backend/internal/store/auth_providers.go`** — Auth provider CRUD
- `ListAuthProviders(ctx) ([]AuthProviderRecord, error)`
- `CreateAuthProvider(ctx, record) error`
- `UpdateAuthProvider(ctx, id, record) error`
- `DeleteAuthProvider(ctx, id) error`
- `GetAuthProvider(ctx, id) (*AuthProviderRecord, error)`

#### Files to Modify

- `backend/internal/server/handle_settings.go` — Expand with full CRUD for settings and auth providers
- `backend/internal/server/routes.go` — Add settings CRUD routes (admin-only)
- `backend/cmd/kubecenter/main.go` — Initialize SettingsService, pass to server
- `frontend/islands/AuthSettings.tsx` — Full CRUD forms (create/edit/delete OIDC/LDAP)
- `frontend/islands/AlertSettings.tsx` — Save to persistent store instead of in-memory

#### API Endpoints

```
GET    /api/v1/settings              — Get all settings (merged view, secrets masked)
PUT    /api/v1/settings              — Update settings (partial patch)
GET    /api/v1/settings/auth         — List auth providers
POST   /api/v1/settings/auth         — Create auth provider
PUT    /api/v1/settings/auth/:id     — Update auth provider
DELETE /api/v1/settings/auth/:id     — Delete auth provider
```

### Phase B: Multi-Cluster Registry (Step 18)

#### Files to Create

**`backend/internal/store/migrations/000003_create_clusters.up.sql`**
**`backend/internal/store/migrations/000003_create_clusters.down.sql`**

**`backend/internal/cluster/registry.go`** — ClusterRegistry
- `ClusterRegistry` manages `map[string]*ClusterConnection`
- `Register(ctx, cluster ClusterRecord) error` — store in DB, connect
- `Deregister(ctx, id string) error` — disconnect, remove from DB
- `Get(id string) (*ClusterConnection, error)` — get connection by ID
- `List(ctx) ([]ClusterRecord, error)` — list all clusters with status
- `StartHealthProbes(ctx)` — 60s probe loop updating status

**`backend/internal/cluster/connection.go`** — ClusterConnection
- Wraps `k8s.ClientFactory` + `k8s.InformerManager` per cluster
- `Connect(ctx) error` — create rest.Config from stored credentials, start informers
- `Disconnect()` — cancel context, stop informers
- Lazy connection (connect on first access)
- Auto-reconnect with backoff on transient failures

**`backend/internal/server/handle_clusters.go`** — Cluster API handlers
- CRUD for cluster registration
- Kubeconfig file upload parsing
- Connection test endpoint

**`frontend/islands/ClusterManager.tsx`** — Cluster management island
- List registered clusters with health status
- "Add Cluster" wizard (3 steps):
  1. **Connection** — API server URL + auth method (token or kubeconfig upload)
  2. **Test** — Verify connectivity, show k8s version + node count
  3. **Confirm** — Name, display name, save

**`frontend/islands/ClusterSelector.tsx`** — TopBar cluster picker
- Dropdown showing all clusters with health indicator (green/red dot)
- "All Clusters" option for the dashboard overview
- Replaces the static "local" cluster indicator

**`frontend/routes/settings/clusters.tsx`** — Cluster management page

#### API Endpoints

```
GET    /api/v1/clusters              — List clusters with status
POST   /api/v1/clusters              — Register cluster (kubeconfig or fields)
GET    /api/v1/clusters/:id          — Cluster details
PUT    /api/v1/clusters/:id          — Update credentials
DELETE /api/v1/clusters/:id          — Deregister
POST   /api/v1/clusters/:id/test     — Test connectivity
```

#### Routing Changes

All resource API calls use `X-Cluster-ID` header (already exists in the codebase) to select which cluster's ClientFactory handles the request. The handler layer reads the header and dispatches to the correct ClusterConnection.

#### Frontend Changes

- `selectedCluster` signal replaces `CLUSTER_ID` constant
- All `api()` calls include `X-Cluster-ID` header from the signal
- Dashboard at `/` shows all-clusters health grid when "All Clusters" selected
- Sidebar nav sections show resources for the selected cluster
- WebSocket subscriptions include cluster ID in the subscribe message

#### Per-Cluster Monitoring Discovery

Each cluster gets its own monitoring discovery run. Results stored in `cluster_monitoring` table. When switching clusters, the monitoring/performance panels use that cluster's Prometheus/Grafana endpoints.

---

## Acceptance Criteria

### Settings (Step 17)
- [ ] Admin can configure OIDC/LDAP providers via UI — changes persist across restarts
- [ ] SMTP/alerting settings persist in PostgreSQL
- [ ] Monitoring URLs configurable via UI
- [ ] Env vars work as defaults for fresh installs
- [ ] Settings changes take effect without restart

### Multi-Cluster (Step 18)
- [ ] Register remote clusters via kubeconfig upload wizard
- [ ] Cluster selector in TopBar switches context for all views
- [ ] All Clusters dashboard shows health grid
- [ ] Resources, monitoring, alerting scoped to selected cluster
- [ ] WebSocket live updates work per-cluster
- [ ] Credentials stored encrypted in PostgreSQL
- [ ] Health probe updates cluster status every 60s
- [ ] Local cluster auto-registered on first boot

## Dependencies

- Step 16 (PostgreSQL) — Done (#29)
- Existing `X-Cluster-ID` header infrastructure
- Existing `ClientFactory.clusterID` field

## References

- Phase 2 plan: `plans/phase-2-production-multi-cluster.md` (Steps 17-18)
- Multi-cluster research: Headlamp pattern (WebSocket multiplexing), Rancher (impersonation)
- Settings research: single-row settings table + auth_providers table
- Existing settings UI: `frontend/islands/AuthSettings.tsx`, `frontend/islands/AlertSettings.tsx`
- Existing cluster hooks: `CLUSTER_ID` constant, `X-Cluster-ID` header, `ClientFactory.clusterID`
