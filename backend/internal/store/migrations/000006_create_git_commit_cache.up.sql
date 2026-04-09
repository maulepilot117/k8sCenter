CREATE TABLE IF NOT EXISTS git_commit_cache (
    canonical_url TEXT NOT NULL,
    sha           TEXT NOT NULL,
    data          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (canonical_url, sha)
);

-- Add GitHub token column to existing settings table
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS github_token TEXT;
