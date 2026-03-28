# Contributing to k8sCenter

## Branch Structure

| Branch | Purpose | Merges from |
|--------|---------|-------------|
| `main` | Production releases | `release/*`, `hotfix/*` |
| `testing` | Release candidates | `dev/ui`, `dev/backend` |
| `dev/ui` | Frontend integration | `feat/ui/*`, `fix/ui/*`, `refactor/ui/*` |
| `dev/backend` | Backend integration | `feat/backend/*`, `fix/backend/*`, `refactor/backend/*` |

**Never commit directly to `main`, `testing`, `dev/ui`, or `dev/backend`.** All changes go through pull requests.

## Workflow

### Feature Development

1. Check out the appropriate dev branch and create a feature branch:
   ```bash
   git checkout dev/ui && git pull
   git checkout -b feat/ui/my-feature
   ```
2. Make changes, commit with conventional commit messages (`feat:`, `fix:`, `refactor:`)
3. Push and open a PR to the parent dev branch
4. CI runs lint + unit tests. Merge when green.

### Promoting to Testing

1. Open a PR from `dev/ui` or `dev/backend` to `testing`
2. CI + E2E tests must pass
3. RC images are built and tagged `rc-X.Y.Z`
4. Deploy RC to staging environment for validation

### Releasing

1. Create a release branch: `git checkout testing && git checkout -b release/vX.Y.Z`
2. Bump `helm/kubecenter/Chart.yaml` version and appVersion
3. Open a PR from `release/vX.Y.Z` to `main`
4. On merge: images tagged `vX.Y.Z` + `latest`, GitHub Release created

### Hotfixes

1. Branch from `main`: `git checkout main && git checkout -b hotfix/description`
2. Fix, push, PR to `main`
3. After merge, back-merge `main` into `testing` and `dev/*`

## Naming Conventions

- Feature branches: `feat/{ui,backend}/short-description`
- Bug fixes: `fix/{ui,backend}/short-description`
- Refactors: `refactor/{ui,backend}/short-description`
- Hotfixes: `hotfix/short-description`
- Releases: `release/vX.Y.Z`

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat: add new feature` — triggers MINOR version bump
- `fix: resolve bug` — triggers PATCH version bump
- `refactor: improve structure` — no version bump
- `chore: update dependencies` — no version bump
- `docs: update README` — no version bump

## Image Tags

| Environment | Tag Format | GHCR Visibility |
|-------------|-----------|-----------------|
| Development | `dev-<sha7>` | Private |
| Testing | `rc-X.Y.Z` | Private |
| Production | `vX.Y.Z`, `latest` | Public |
