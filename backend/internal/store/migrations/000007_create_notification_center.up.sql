CREATE TABLE IF NOT EXISTS nc_notifications (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source        TEXT NOT NULL CHECK (source IN ('alert','policy','gitops','diagnostic','scan','cluster','audit')),
    severity      TEXT NOT NULL CHECK (severity IN ('critical','warning','info')),
    title         TEXT NOT NULL CHECK (length(title) <= 500),
    message       TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 10000),
    resource_kind TEXT NOT NULL DEFAULT '',
    resource_ns   TEXT NOT NULL DEFAULT '',
    resource_name TEXT NOT NULL DEFAULT '',
    cluster_id    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_nc_notif_created_at ON nc_notifications (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_nc_notif_source ON nc_notifications (source);
CREATE INDEX IF NOT EXISTS idx_nc_notif_dedup ON nc_notifications (source, resource_kind, resource_ns, resource_name, title, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_nc_notif_ns_created ON nc_notifications (resource_ns, created_at DESC);

CREATE TABLE IF NOT EXISTS nc_reads (
    user_id         TEXT NOT NULL,
    notification_id UUID NOT NULL REFERENCES nc_notifications(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, notification_id)
);

CREATE INDEX IF NOT EXISTS idx_nc_reads_notification_id ON nc_reads (notification_id);

CREATE TABLE IF NOT EXISTS nc_channels (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL UNIQUE,
    type          TEXT NOT NULL CHECK (type IN ('slack','email','webhook')),
    config        BYTEA NOT NULL,
    created_by    TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ,
    updated_by    TEXT,
    last_sent_at  TIMESTAMPTZ,
    last_error    TEXT,
    last_error_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS nc_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL,
    source_filter   TEXT[] NOT NULL DEFAULT '{}',
    severity_filter TEXT[] NOT NULL DEFAULT '{}',
    channel_id      UUID NOT NULL REFERENCES nc_channels(id) ON DELETE CASCADE,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ,
    updated_by      TEXT
);
