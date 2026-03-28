# Contributing to k8sCenter

## Branching Model: GitHub Flow

We use [GitHub Flow](https://docs.github.com/en/get-started/using-git/github-flow) — the industry standard for continuously deployed projects.

```
main (protected, always deployable)
└── feat/description, fix/description (short-lived feature branches)
```

**Never commit directly to `main`.** All changes go through pull requests with CI checks.

## Workflow

### Feature Development

1. Create a feature branch from `main`:
   ```bash
   git checkout main && git pull
   git checkout -b feat/my-feature
   ```
2. Make changes, commit with conventional commit messages (`feat:`, `fix:`, `refactor:`)
3. Push and open a PR to `main`
4. CI runs lint + unit tests. E2E tests run automatically.
5. Get review, merge when green.

### Releasing

On merge to `main`:
- Images are automatically built and pushed to GHCR (`vX.Y.Z`, `sha-<hash>`, `latest`)
- Git tag created, GitHub Release published
- Deploy to homelab: `kubectl set image deployment/... frontend=ghcr.io/.../k8scenter-frontend:sha-<hash>`

### Hotfixes

Same as feature development — branch from `main`, PR to `main`. The only difference is urgency and scope.

## Naming Conventions

- Features: `feat/short-description`
- Bug fixes: `fix/short-description`
- Refactors: `refactor/short-description`
- Docs: `docs/short-description`
- Chores: `chore/short-description`

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat: add new feature` — triggers MINOR version bump
- `fix: resolve bug` — triggers PATCH version bump
- `refactor: improve structure` — no version bump
- `chore: update dependencies` — no version bump
- `docs: update README` — no version bump

## Image Tags

| Tag | Purpose | Example |
|-----|---------|---------|
| `vX.Y.Z` | Release version (immutable) | `v0.23.0` |
| `sha-<hash>` | Commit-traceable (immutable) | `sha-abc1234` |
| `latest` | Most recent release (floating) | — |

## Branch Protection

`main` requires:
- Pull request with at least 1 approval
- CI status checks passing (backend lint/test, frontend lint/build)
- E2E tests passing
- No force push, no deletion
