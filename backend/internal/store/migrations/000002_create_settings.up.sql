CREATE TABLE IF NOT EXISTS app_settings (
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

-- Seed the single row so UPDATE always works
INSERT INTO app_settings (id) VALUES (1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS auth_providers (
    id              TEXT PRIMARY KEY,
    provider_type   TEXT NOT NULL CHECK (provider_type IN ('oidc', 'ldap')),
    display_name    TEXT NOT NULL,
    config_json     JSONB NOT NULL DEFAULT '{}',
    enabled         BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);
