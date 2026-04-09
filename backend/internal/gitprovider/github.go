package gitprovider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/go-github/v83/github"
)

// GitHubClient fetches commit metadata from the GitHub API.
type GitHubClient struct {
	client *github.Client
	log    *slog.Logger
}

// NewGitHubClient creates a GitHub API client.
// If enterpriseURL is non-empty, it configures the client for GitHub Enterprise.
func NewGitHubClient(token, enterpriseURL string, log *slog.Logger) (*GitHubClient, error) {
	client := github.NewClient(nil).WithAuthToken(token)

	if enterpriseURL != "" {
		var err error
		client, err = client.WithEnterpriseURLs(enterpriseURL, enterpriseURL)
		if err != nil {
			return nil, fmt.Errorf("configuring github enterprise URL: %w", err)
		}
	}

	return &GitHubClient{client: client, log: log}, nil
}

// GetCommit fetches commit metadata by SHA from the GitHub API.
func (g *GitHubClient) GetCommit(ctx context.Context, owner, repo, sha string) (*CommitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rc, _, err := g.client.Repositories.GetCommit(ctx, owner, repo, sha, nil)
	if err != nil {
		var rateLimitErr *github.RateLimitError
		if errors.As(err, &rateLimitErr) {
			g.log.Warn("github rate limit hit",
				"remaining", rateLimitErr.Rate.Remaining,
				"reset", rateLimitErr.Rate.Reset.Time,
			)
			return nil, fmt.Errorf("github rate limit exceeded, resets at %s", rateLimitErr.Rate.Reset.Time.Format(time.RFC3339))
		}

		var abuseErr *github.AbuseRateLimitError
		if errors.As(err, &abuseErr) {
			g.log.Warn("github secondary rate limit hit", "retryAfter", abuseErr.RetryAfter)
			return nil, fmt.Errorf("github secondary rate limit hit")
		}

		return nil, fmt.Errorf("fetching commit %s/%s@%.7s: %w", owner, repo, sha, err)
	}

	info := &CommitInfo{
		SHA:    rc.GetSHA(),
		WebURL: rc.GetHTMLURL(),
	}

	if rc.Commit != nil {
		info.Message = rc.Commit.GetMessage()
		if rc.Commit.Author != nil {
			info.AuthorName = rc.Commit.Author.GetName()
			info.AuthorDate = rc.Commit.Author.GetDate().Time
		}
	}

	return info, nil
}
