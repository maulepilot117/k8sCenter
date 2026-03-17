CREATE TABLE local_users (
    id              TEXT PRIMARY KEY,
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    salt            TEXT NOT NULL,
    k8s_username    TEXT NOT NULL,
    k8s_groups      TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    roles           TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
