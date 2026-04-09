# Step 32: Git Commit Display

## Overview

Enrich Argo CD revision history with actual git commit messages, authors, and web URLs by calling the GitHub API. The revision history table in the app detail page currently shows SHAs with an empty Message column ("-"). This feature populates that column with real commit data fetched asynchronously.

## Problem Statement

When users view an Argo CD application's deployment history, they see raw commit SHAs and timestamps but no commit messages or authors. To understand what each deployment contained, they must manually look up commits in GitHub. Argo CD's own UI shows commit messages because its repo-server maintains local git clones — k8sCenter has no equivalent. This feature closes that gap using lightweight GitHub API calls instead of full clones.

## Scope

**In scope (v1 — GitHub only):**
- Argo CD Application `status.history` enrichment (full git SHA history)
- Argo CD `currentRevision` enrichment (single SHA)
- GitHub.com + GitHub Enterprise
- Token stored in existing `app_settings` table
- PostgreSQL commit cache (immutable data, cache-forever)
- Async frontend enrichment (never blocks page load)

**Out of scope (v1):**
- GitLab, Bitbucket adapters (add when requested — no Provider interface yet)
- Flux Kustomization enrichment (no CRD-native revision history, `flux.go:87`)
- Flux HelmRelease enrichment (chart versions, not git SHAs)
- Author avatars
- Admin CRUD UI for multiple providers (one token in settings is enough)
- In-memory LRU cache (PostgreSQL PK lookup is sub-ms; singleflight prevents thundering herd)

**Future (v2, on demand):**
- Provider interface + GitLab/Bitbucket adapters
- Dedicated `git_providers` table for multi-host token management
- Flux `GitRepository` sourceRef resolution for Kustomization `currentRevision`

## Technical Approach

### Architecture

```
Frontend (GitOpsAppDetail)           Backend
┌──────────────────────┐    ┌─────────────────────────────┐
│ 1. Load detail page  │───>│ GET /gitops/applications/:id │
│    (existing flow)   │<───│ Returns history[] with SHAs  │
│                      │    └─────────────────────────────┘
│ 2. Async enrichment  │───>│ GET /gitops/commits          │
│    (new, after load) │    │   ?repoURL=...&shas=a,b,c   │
│                      │    ├─────────────────────────────┤
│ 3. Merge into table  │<───│ PostgreSQL cache + singleflight
│    Message column    │    │   → GitHub API (on miss)     │
└──────────────────────┘    └─────────────────────────────┘
```

**Key design choices:**

1. **Separate endpoint** (`GET /gitops/commits`) — avoids latency on primary page load, follows async Prometheus pattern from Phase 6B
2. **PostgreSQL-only cache** with `singleflight` — commits are immutable, PK lookup is sub-ms, no LRU complexity needed
3. **Concrete `GitHubClient` struct** — no Provider interface until we need a second provider
4. **Token in `app_settings`** — existing settings table + encryption + UI, no new CRUD infrastructure
5. **Commits handler in `gitops/`** — it's a gitops concern, not a standalone domain

### File Plan

**New files (6):**

```
backend/internal/gitprovider/
  github.go        — GitHubClient struct (~80 LOC)
  cache.go         — CommitCache: PostgreSQL + singleflight (~60 LOC)
  parse.go         — ParseRepoURL: host/owner/repo extraction (~50 LOC)
  parse_test.go    — URL parsing tests (~60 LOC)
  github_test.go   — GitHub adapter tests with httptest.Server (~80 LOC)

backend/internal/store/migrations/
  000006_create_git_commit_cache.up.sql
  000006_create_git_commit_cache.down.sql
```

**Modified files (6):**

```
backend/internal/gitops/handler.go       — add HandleGetCommits method
backend/internal/store/settings.go       — add GitHubToken field to AppSettings
backend/internal/server/routes.go        — register GET /gitops/commits
backend/internal/server/server.go        — wire CommitCache into GitOps handler
backend/go.mod                           — add google/go-github/v83
frontend/islands/GitOpsAppDetail.tsx     — async fetch + table enrichment
frontend/lib/gitops-types.ts             — add CommitInfo + CommitsResponse interfaces
```

### Implementation Phases

---

#### Phase 1: Backend (GitHub adapter + cache + endpoint + settings)

##### 1a. Types and URL Parsing (`gitprovider/parse.go`)

```go
// RepoRef is a parsed git repository URL.
type RepoRef struct {
    Host  string // "github.com", "github.enterprise.com"
    Owner string // "org" or "group/subgroup"
    Repo  string // "repo-name"
}

// CommitInfo is normalized commit metadata from a git provider.
type CommitInfo struct {
    SHA        string    `json:"sha"`
    Message    string    `json:"message"`
    AuthorName string    `json:"authorName"`
    AuthorDate time.Time `json:"authorDate"`
    WebURL     string    `json:"webUrl,omitempty"`
}
```

`ParseRepoURL` handles all common formats:
- `https://github.com/org/repo.git` → github.com, org, repo
- `git@github.com:org/repo.git` → github.com, org, repo
- `ssh://git@github.com/org/repo.git` → github.com, org, repo

Uses `net/url` + regex for SSH format. `strings.LastIndex("/")` for owner/repo split. Strips `.git` suffix. Returns a **canonical form** for cache keys: `https://{host}/{owner}/{repo}` (lowercase host, no `.git`, HTTPS scheme).

Design notes:
- `CommitInfo.AuthorDate` is `time.Time` (not string) — JSON serialization handles ISO 8601
- No `Title` field — derive first line from `Message` via `strings.SplitN(message, "\n", 2)[0]` in the response serializer
- No `Provider` interface — concrete struct until we need a second adapter

**Test matrix:** 10+ URL formats including dots in repo name, trailing slashes, port numbers, SSH variants.

##### 1b. GitHub Adapter (`gitprovider/github.go`)

```go
type GitHubClient struct {
    client *github.Client
}

func NewGitHubClient(token, enterpriseURL string) (*GitHubClient, error)

func (g *GitHubClient) GetCommit(ctx context.Context, owner, repo, sha string) (*CommitInfo, error)
```

- Uses `google/go-github/v83` → `Repositories.GetCommit(ctx, owner, repo, sha, nil)`
- Always use `Get*()` helper methods to avoid nil pointer panics on pointer fields
- Handle `*github.RateLimitError` and `*github.AbuseRateLimitError` — log warning with reset time via `slog`
- Support Enterprise via `client.WithEnterpriseURLs(enterpriseURL, enterpriseURL)`
- 10-second context timeout on outbound calls

Tests use `httptest.Server` returning canned responses, covering: success, 404 (unknown SHA), 401 (bad token), 429 (rate limit).

##### 1c. Commit Cache (`gitprovider/cache.go`)

```go
type CommitCache struct {
    db    *pgxpool.Pool
    gh    *GitHubClient   // nil if no token configured
    group singleflight.Group
    log   *slog.Logger
}

func (c *CommitCache) GetCommits(ctx context.Context, canonicalURL, owner, repo string, shas []string) (map[string]*CommitInfo, []string)
```

- PostgreSQL-only cache. No in-memory LRU.
- Batch lookup: `SELECT data FROM git_commit_cache WHERE canonical_url = $1 AND sha = ANY($2)`
- `singleflight.Group` keyed per `canonicalURL:sha` to coalesce concurrent fetches for uncached SHAs
- Fan out uncached SHA fetches with `errgroup` (max 5 concurrent GitHub API calls)
- Write-back: `INSERT ... ON CONFLICT DO NOTHING` (immutable data)
- **Negative results NOT cached** — 404/401 today might succeed after token config change
- Returns `(commits map, unavailable []string)` — never errors the whole batch

##### 1d. Migration (`000006`)

```sql
-- 000006_create_git_commit_cache.up.sql
CREATE TABLE git_commit_cache (
    canonical_url TEXT NOT NULL,
    sha           TEXT NOT NULL,
    data          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (canonical_url, sha)
);
```

```sql
-- 000006_create_git_commit_cache.down.sql
DROP TABLE IF EXISTS git_commit_cache;
```

Design notes:
- `canonical_url` is the normalized repo URL from `ParseRepoURL` (not raw user input)
- `data` is JSONB containing the full `CommitInfo` — no schema evolution needed when adding fields later
- No secondary index on `sha` alone — the batch query always includes `canonical_url`
- No TTL — commits are immutable

##### 1e. Settings Extension (`store/settings.go`)

Add `GitHubToken` to existing `AppSettings` struct:

```go
type AppSettings struct {
    // ... existing fields ...
    GitHubToken string `json:"gitHubToken,omitempty" koanf:"githubtoken"`
}
```

- Encrypted at rest using existing `store.Encrypt`/`store.Decrypt` (same as Grafana token)
- Masked in API responses via existing `MaskedSettings()` pattern — return `"****"` if non-empty
- Configurable via existing settings endpoints (`GET/PUT /api/v1/settings`) and settings UI
- Also settable via env var: `KUBECENTER_GITHUBTOKEN` (for CI/quick-start)

No new table. No new CRUD endpoints. No new frontend island.

##### 1f. HTTP Handler (in `gitops/handler.go`)

Add `HandleGetCommits` to the existing `gitops.Handler`:

```go
// GET /api/v1/gitops/commits?repoURL=<url>&shas=<sha1,sha2,...>
func (h *Handler) HandleGetCommits(w http.ResponseWriter, r *http.Request) {
    // 1. Parse & validate query params
    // 2. Parse repoURL → canonical form + owner/repo
    // 3. Validate repoURL host is github.com or configured GHE host
    // 4. Cap shas at 50 per request
    // 5. Validate repoURL matches at least one app visible to the user (RBAC)
    // 6. Batch lookup via CommitCache
    // 7. Return map of sha → CommitInfo (title derived from message) + unavailable list
}
```

**RBAC:** Validate that `repoURL` matches at least one application the requesting user can see (cross-reference against the cached app list). Prevents information leakage via arbitrary repo URLs.

**Rate limiting:** Uses the existing GitOps route group rate limiter. No separate limiter needed — the endpoint is cached and lightweight.

**Response shape:**

```json
{
  "data": {
    "commits": {
      "abc1234def5678...": {
        "sha": "abc1234def5678...",
        "title": "fix: resolve memory leak in pod watcher",
        "message": "fix: resolve memory leak in pod watcher\n\nThe informer...",
        "authorName": "Jane Smith",
        "authorDate": "2026-04-07T15:00:00Z",
        "webUrl": "https://github.com/org/repo/commit/abc1234def5678..."
      }
    },
    "unavailable": ["def5678..."]
  }
}
```

`title` is derived server-side: `strings.SplitN(message, "\n", 2)[0]`.

`unavailable` lists SHAs that could not be fetched (no token configured, rate limited, not found on GitHub). Frontend uses this to leave those rows as "-".

Route registration in `routes.go`:
```go
gr.Get("/commits", gitopsHandler.HandleGetCommits)
```

Registered inside the existing `/api/v1/gitops` route group (inherits auth + rate limiter).

**Success criteria:**
- [ ] `ParseRepoURL` handles 10+ URL formats with tests
- [ ] GitHub adapter fetches commit by SHA, handles rate limits
- [ ] Migration creates cache table, rollback drops it
- [ ] Settings field stores/masks GitHub token
- [ ] Handler returns cached commits without API calls
- [ ] Handler fetches uncached commits from GitHub
- [ ] RBAC validates repoURL against user-visible apps
- [ ] SHA list capped at 50
- [ ] `go vet` and `go test ./internal/gitprovider/... ./internal/gitops/...` pass

---

#### Phase 2: Frontend Enrichment (async commit fetch + table update)

**Files to modify:**

- `frontend/islands/GitOpsAppDetail.tsx` — async commit fetch + merge into revision table
- `frontend/lib/gitops-types.ts` — add `CommitInfo` and `CommitsResponse` interfaces

##### 2a. TypeScript Types (in `gitops-types.ts`)

```ts
export interface CommitInfo {
  sha: string;
  title: string;
  message: string;
  authorName: string;
  authorDate: string;
  webUrl?: string;
}

export interface CommitsResponse {
  commits: Record<string, CommitInfo>;
  unavailable: string[];
}
```

Added inline to `gitops-types.ts` — no separate file for 15 lines.

##### 2b. Enrichment Flow in `GitOpsAppDetail.tsx`

1. After `detail.value` loads, extract `source.repoURL` and all `history[].revision` SHAs
2. Skip if `repoURL` does not contain `://` (filters out Flux's `"GitRepository/name"` format)
3. Create `AbortController` — cancel on component unmount or when `detail.value` changes
4. Call `GET /api/v1/gitops/commits?repoURL=${encodeURIComponent(repoURL)}&shas=...`
5. Store result in a Preact signal: `const commits = useSignal<Record<string, CommitInfo>>({});`
6. In the table, replace `h.message ?? "-"` with enriched display

Note: `repoURL` must be `encodeURIComponent()`-encoded in the query string (contains `://`, `/`).

##### 2c. Message Column Display

```
┌──────────────────────────────────────────────┐
│ fix: resolve memory leak in pod watcher  ↗   │  ← commit title (truncated, links to webUrl)
│ Jane Smith                                   │  ← author name (text-xs, text-muted)
└──────────────────────────────────────────────┘
```

- Two-line cell: title (truncated, `text-sm`) + author (`text-xs`, muted color via CSS var)
- Title links to `webUrl` (opens in new tab) if available
- Revision SHA column also links to `webUrl` if available (clickable monospace hash)
- While loading: show "-" (same as today, no spinner, no layout shift)
- On failure / unavailable / no token configured: show "-"

##### 2d. Cleanup

- `AbortController` in `useEffect` cleanup to prevent stale responses updating wrong detail view
- No separate loading state — "-" placeholder is sufficient since enrichment is progressive enhancement

**Success criteria:**
- [ ] Argo CD app detail page shows commit messages + authors in history table
- [ ] Commit titles are clickable links to GitHub
- [ ] Revision SHAs are clickable links to GitHub
- [ ] Loading state shows "-", no layout shift on enrichment
- [ ] Navigation away cancels in-flight fetch (AbortController)
- [ ] Pages without configured token degrade gracefully (no errors, shows "-")
- [ ] `deno lint` and `deno fmt --check` pass

---

## Dependencies

| Library | Version | Purpose |
|---------|---------|---------|
| `google/go-github/v83` | latest | GitHub API client |

Single new dependency. No GitLab/Bitbucket libraries until needed.

## Security Considerations

1. **RBAC on commits endpoint:** Validate `repoURL` matches at least one application visible to the requesting user. Prevents using the endpoint to probe arbitrary private repos.
2. **Token encryption:** AES-256-GCM via existing `store.Encrypt`/`store.Decrypt`. Token masked as `"****"` in settings API responses.
3. **Token scope:** Document minimum required: GitHub fine-grained PAT with `contents:read` on target repos.
4. **No SSRF concern for v1:** Outbound calls go only to `api.github.com` (or admin-configured GHE URL stored in settings). No user-supplied URL is used as an outbound target.
5. **Audit logging:** Token save/update logged via existing settings audit trail. Read-only commit fetches not audited.

## Risk Analysis

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| GitHub rate limit hit (5,000/hr) | Low | Low | PostgreSQL cache + singleflight; most SHAs cached after first view |
| Token misconfigured (wrong scope) | Medium | Low | Graceful degradation — shows "-"; admin can test via settings |
| GHE instance unreachable | Low | Low | 10s timeout, partial results, "-" for failed SHAs |
| Cache table unbounded growth | Low | Low | Immutable data, no duplication; `VACUUM` handles dead tuples |

## References

### Internal
- `backend/internal/gitops/types.go` — `RevisionEntry`, `AppSource`, `AppDetail`
- `backend/internal/gitops/argocd.go:197-220` — `extractArgoHistory` (revision extraction)
- `backend/internal/gitops/handler.go:28-105` — singleflight + cache pattern to follow
- `backend/internal/store/settings.go` — `AppSettings` struct, `MaskedSettings()` pattern
- `backend/internal/store/encrypt.go` — AES-256-GCM encryption
- `frontend/islands/GitOpsAppDetail.tsx:424-503` — revision history table
- `frontend/lib/gitops-types.ts:59-64` — `RevisionEntry` TypeScript interface

### External
- [GitHub REST API - Get a Commit](https://docs.github.com/en/rest/commits/commits#get-a-commit)
- [google/go-github v83](https://github.com/google/go-github)
- [Argo CD - RevisionMetadata](https://github.com/argoproj/argo-cd/blob/master/util/git/client.go)
