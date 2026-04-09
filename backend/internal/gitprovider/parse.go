package gitprovider

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// CommitInfo is normalized commit metadata from a git provider.
type CommitInfo struct {
	SHA        string    `json:"sha"`
	Message    string    `json:"message"`
	AuthorName string    `json:"authorName"`
	AuthorDate time.Time `json:"authorDate"`
	WebURL     string    `json:"webUrl,omitempty"`
}

// Title returns the first line of the commit message.
func (c *CommitInfo) Title() string {
	if i := strings.IndexByte(c.Message, '\n'); i >= 0 {
		return c.Message[:i]
	}
	return c.Message
}

// RepoRef is a parsed git repository URL.
type RepoRef struct {
	Host  string // "github.com", "github.enterprise.com"
	Owner string // "org" or "group/subgroup"
	Repo  string // "repo-name"
}

// CanonicalURL returns the normalized cache key form: https://{host}/{owner}/{repo}
func (r *RepoRef) CanonicalURL() string {
	return "https://" + r.Host + "/" + r.Owner + "/" + r.Repo
}

// sshPattern matches git@host:owner/repo.git
var sshPattern = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

// ParseRepoURL parses a git repository URL into host, owner, and repo components.
// Supported formats:
//   - https://github.com/org/repo.git
//   - https://github.com/org/repo
//   - git@github.com:org/repo.git
//   - ssh://git@github.com/org/repo.git
//   - ssh://git@github.com:22/org/repo.git
func ParseRepoURL(rawURL string) (*RepoRef, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("empty repository URL")
	}

	// Handle SSH shorthand: git@host:owner/repo.git
	if m := sshPattern.FindStringSubmatch(rawURL); m != nil {
		host := strings.ToLower(m[1])
		path := m[2]
		owner, repo, err := splitOwnerRepo(path)
		if err != nil {
			return nil, fmt.Errorf("parsing SSH URL %q: %w", rawURL, err)
		}
		return &RepoRef{Host: host, Owner: owner, Repo: repo}, nil
	}

	// Handle ssh:// scheme: ssh://git@github.com/org/repo.git
	// Handle https:// scheme: https://github.com/org/repo.git
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing URL %q: %w", rawURL, err)
	}

	if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" {
		return nil, fmt.Errorf("unsupported URL scheme %q in %q", u.Scheme, rawURL)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, fmt.Errorf("missing host in URL %q", rawURL)
	}

	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")

	if path == "" {
		return nil, fmt.Errorf("missing path in URL %q", rawURL)
	}

	owner, repo, err := splitOwnerRepo(path)
	if err != nil {
		return nil, fmt.Errorf("parsing URL %q: %w", rawURL, err)
	}

	return &RepoRef{Host: host, Owner: owner, Repo: repo}, nil
}

// splitOwnerRepo splits "owner/repo" or "group/subgroup/repo" into owner and repo.
// The last path segment is always the repo; everything before it is the owner.
func splitOwnerRepo(path string) (owner, repo string, err error) {
	idx := strings.LastIndex(path, "/")
	if idx < 0 || idx == 0 {
		return "", "", fmt.Errorf("cannot extract owner/repo from path %q", path)
	}
	return path[:idx], path[idx+1:], nil
}
