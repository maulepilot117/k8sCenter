package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

// CommitCache resolves commit SHAs to metadata, backed by PostgreSQL and singleflight.
type CommitCache struct {
	db  *pgxpool.Pool
	gh  *GitHubClient // nil if no token configured
	log *slog.Logger

	group singleflight.Group
}

// NewCommitCache creates a commit cache. gh may be nil if no GitHub token is configured.
func NewCommitCache(db *pgxpool.Pool, gh *GitHubClient, log *slog.Logger) *CommitCache {
	return &CommitCache{db: db, gh: gh, log: log}
}

// GetCommits resolves a batch of SHAs to commit metadata.
// Returns found commits and a list of SHAs that could not be resolved.
func (c *CommitCache) GetCommits(ctx context.Context, canonicalURL, owner, repo string, shas []string) (map[string]*CommitInfo, []string) {
	if len(shas) == 0 {
		return nil, nil
	}

	// 1. Batch lookup from PostgreSQL
	cached := c.batchGet(ctx, canonicalURL, shas)

	// Collect uncached SHAs
	var missing []string
	for _, sha := range shas {
		if _, ok := cached[sha]; !ok {
			missing = append(missing, sha)
		}
	}

	if len(missing) == 0 || c.gh == nil {
		return cached, missing
	}

	// 2. Fan out fetches for uncached SHAs (bounded concurrency via singleflight)
	var mu sync.Mutex
	var unavailable []string
	var wg sync.WaitGroup

	// Use a semaphore channel to limit concurrent GitHub API calls
	sem := make(chan struct{}, 5)

	for _, sha := range missing {
		wg.Add(1)
		go func(sha string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			key := canonicalURL + ":" + sha
			result, err, _ := c.group.Do(key, func() (any, error) {
				return c.gh.GetCommit(ctx, owner, repo, sha)
			})

			if err != nil {
				c.log.Debug("commit fetch failed", "sha", sha[:min(7, len(sha))], "err", err)
				mu.Lock()
				unavailable = append(unavailable, sha)
				mu.Unlock()
				return
			}

			info := result.(*CommitInfo)
			c.put(ctx, canonicalURL, info)

			mu.Lock()
			cached[sha] = info
			mu.Unlock()
		}(sha)
	}

	wg.Wait()
	return cached, unavailable
}

// batchGet retrieves cached commits from PostgreSQL.
func (c *CommitCache) batchGet(ctx context.Context, canonicalURL string, shas []string) map[string]*CommitInfo {
	result := make(map[string]*CommitInfo, len(shas))

	rows, err := c.db.Query(ctx,
		`SELECT sha, data FROM git_commit_cache WHERE canonical_url = $1 AND sha = ANY($2)`,
		canonicalURL, shas,
	)
	if err != nil {
		c.log.Error("querying commit cache", "err", err)
		return result
	}
	defer rows.Close()

	for rows.Next() {
		var sha string
		var data []byte
		if err := rows.Scan(&sha, &data); err != nil {
			c.log.Error("scanning commit cache row", "err", err)
			continue
		}
		var info CommitInfo
		if err := json.Unmarshal(data, &info); err != nil {
			c.log.Error("unmarshaling cached commit", "err", err)
			continue
		}
		result[sha] = &info
	}

	return result
}

// put writes a commit to the PostgreSQL cache.
func (c *CommitCache) put(ctx context.Context, canonicalURL string, info *CommitInfo) {
	data, err := json.Marshal(info)
	if err != nil {
		c.log.Error("marshaling commit for cache", "err", err)
		return
	}

	_, err = c.db.Exec(ctx,
		`INSERT INTO git_commit_cache (canonical_url, sha, data) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		canonicalURL, info.SHA, data,
	)
	if err != nil {
		c.log.Error("caching commit", "sha", info.SHA[:min(7, len(info.SHA))], "err", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SetGitHubClient replaces the GitHub client (used when settings change at runtime).
func (c *CommitCache) SetGitHubClient(gh *GitHubClient) {
	c.gh = gh
}

// HasGitHub returns true if a GitHub client is configured.
func (c *CommitCache) HasGitHub() bool {
	return c.gh != nil
}

// CommitResponse is the API response shape for the commits endpoint.
type CommitResponse struct {
	Commits     map[string]*commitResponseEntry `json:"commits"`
	Unavailable []string                        `json:"unavailable"`
}

type commitResponseEntry struct {
	SHA        string `json:"sha"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	AuthorName string `json:"authorName"`
	AuthorDate string `json:"authorDate"`
	WebURL     string `json:"webUrl,omitempty"`
}

// ToResponse converts the raw cache results into the API response format.
func ToResponse(commits map[string]*CommitInfo, unavailable []string) *CommitResponse {
	resp := &CommitResponse{
		Commits:     make(map[string]*commitResponseEntry, len(commits)),
		Unavailable: unavailable,
	}
	if resp.Unavailable == nil {
		resp.Unavailable = []string{}
	}

	for sha, c := range commits {
		resp.Commits[sha] = &commitResponseEntry{
			SHA:        c.SHA,
			Title:      c.Title(),
			Message:    c.Message,
			AuthorName: c.AuthorName,
			AuthorDate: fmt.Sprintf("%s", c.AuthorDate.UTC().Format("2006-01-02T15:04:05Z")),
			WebURL:     c.WebURL,
		}
	}

	return resp
}
