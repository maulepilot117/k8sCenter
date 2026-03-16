CREATE TABLE IF NOT EXISTS clusters (
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

CREATE TABLE IF NOT EXISTS cluster_monitoring (
    cluster_id      TEXT PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    prometheus_url  TEXT,
    grafana_url     TEXT,
    grafana_token   TEXT,
    discovery_src   TEXT DEFAULT 'none',
    discovered_at   TIMESTAMPTZ
);
