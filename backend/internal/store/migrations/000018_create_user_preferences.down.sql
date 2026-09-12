-- Fully reversible: the table is additive and nothing else references it.
-- Rolling back destroys every user's saved views and pins; there is no
-- backfill and no dependency in any other table.
DROP INDEX IF EXISTS idx_user_preferences_owner_kind;
DROP INDEX IF EXISTS idx_user_preferences_dedup;
DROP TABLE IF EXISTS user_preferences;
