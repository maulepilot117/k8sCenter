-- Narrowing the CHECK will FAIL while any dashboard_layout row exists, which
-- is correct: silently deleting a user's layouts to satisfy a rollback is
-- worse than a loud failure. Remove them first if you mean it:
--
--   DELETE FROM user_preferences WHERE kind = 'dashboard_layout';
--
-- Find them before deciding:
--
--   SELECT owner_id, cluster_id, dedup_key, updated_at
--     FROM user_preferences WHERE kind = 'dashboard_layout';

ALTER TABLE user_preferences
    DROP CONSTRAINT IF EXISTS user_preferences_kind_check;

ALTER TABLE user_preferences
    ADD CONSTRAINT user_preferences_kind_check
    CHECK (kind IN ('saved_view', 'pin'));
