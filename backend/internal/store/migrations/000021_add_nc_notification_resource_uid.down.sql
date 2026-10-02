-- Drops the notification UID column. Lossy but safe: the column only feeds
-- dedup and the optional resourceUid feed field, both of which degrade to the
-- pre-000021 behaviour. Cannot fail on existing data.
ALTER TABLE nc_notifications DROP COLUMN IF EXISTS resource_uid;
