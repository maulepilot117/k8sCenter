CREATE TABLE IF NOT EXISTS git_commit_cache (
    canonical_url TEXT NOT NULL,
    sha           TEXT NOT NULL,
    data          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (canonical_url, sha)
);

-- Add encrypted GitHub token column to existing settings table (AES-256-GCM)
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS github_token_enc BYTEA;

-- Index for future cache cleanup by age
CREATE INDEX IF NOT EXISTS idx_git_commit_cache_created_at ON git_commit_cache (created_at);
