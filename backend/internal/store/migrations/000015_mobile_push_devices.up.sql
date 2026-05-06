-- Mobile push device registry: per-user FCM/APNS device tokens for the
-- ChannelMobilePush dispatch path. One row per (user, device); the same user
-- can register multiple devices (phone + tablet). device_token is unique so
-- re-registration from the same handset upserts onto last_seen_at.

CREATE TABLE IF NOT EXISTS mobile_push_devices (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       TEXT        NOT NULL,
    device_token  TEXT        NOT NULL UNIQUE,
    platform      TEXT        NOT NULL CHECK (platform IN ('ios', 'android')),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mobile_push_devices_user
    ON mobile_push_devices (user_id);
