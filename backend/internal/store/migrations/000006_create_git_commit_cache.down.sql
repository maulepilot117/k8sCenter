DROP INDEX IF EXISTS idx_git_commit_cache_created_at;
ALTER TABLE app_settings DROP COLUMN IF EXISTS github_token_enc;
DROP TABLE IF EXISTS git_commit_cache;
