# Branching Strategy & CI/CD Pipeline Design

**Date:** 2026-03-28
**Status:** Approved
**Scope:** Git branching model, CI/CD workflows, GHCR image publishing, branch protection

---

## Overview

Transition k8sCenter from an informal push-to-main workflow to a full Gitflow branching strategy with environment-specific CI/CD pipelines, private/public GHCR image publishing, and branch protection rules. Designed for a multi-contributor project.

---

## 1. Branch Structure

```
main                              # Production — tagged releases only, never direct edits
├── hotfix/*                      # Emergency fixes branched from main
├── testing                       # Pre-production staging — RC builds
├── develop/ui                    # UI integration branch — dev builds
│   └── develop/ui/feat/*         # Feature branches (e.g., develop/ui/feat/theme-picker)
│   └── develop/ui/fix/*          # Bug fixes (e.g., develop/ui/fix/breadcrumb-overflow)
│   └── develop/ui/refactor/*     # Refactoring
├── develop/backend               # Backend integration branch — dev builds
│   └── develop/backend/feat/*    # Feature branches (e.g., develop/backend/feat/audit-export)
│   └── develop/backend/fix/*     # Bug fixes
│   └── develop/backend/refactor/*
└── release/vX.Y.Z               # Short-lived version bump branch (testing → main)
```

### Rules

| Branch | Direct commits | Receives merges from | Merge method |
|--------|---------------|----------------------|--------------|
| `main` | Never | `release/*`, `hotfix/*` via PR | Squash |
| `testing` | Never | `develop/ui`, `develop/backend` via PR | Merge commit |
| `develop/ui` | Never | Feature branches via PR | Squash |
| `develop/backend` | Never | Feature branches via PR | Squash |
| Feature branches | Yes | — | — |
| `hotfix/*` | Yes | — | — |
| `release/*` | Yes (version bumps only) | — | — |

### Lifecycle

- **Permanent branches:** `main`, `testing`, `develop/ui`, `develop/backend`
- **Ephemeral branches:** Feature branches, `hotfix/*`, `release/*` — deleted after merge

---

## 2. CI/CD Pipeline

### Workflow Files

| File | Triggers | Purpose |
|------|----------|---------|
| `ci.yml` | PR to any protected branch | Lint, unit test, security scan |
| `ci-dev.yml` | Push to `develop/ui` or `develop/backend` | Build + push dev images to GHCR |
| `ci-test.yml` | Push to `testing` | Build + push RC images to GHCR |
| `ci-release.yml` | Push to `main` | Build + push release images, create GitHub Release |
| `e2e.yml` | PR to `testing` or `main`, `workflow_dispatch` | Playwright E2E test suite |

### Image Tags per Environment

| Environment | GHCR Tag | Example | Visibility |
|-------------|----------|---------|------------|
| Development | `dev-<sha7>` | `dev-a1b2c3d` | Private |
| Testing | `rc-X.Y.Z` | `rc-0.23.0` | Private |
| Production | `vX.Y.Z` + `latest` | `v0.23.0` | Public |

### Image Names

```
ghcr.io/maulepilot117/k8scenter-backend:{tag}
ghcr.io/maulepilot117/k8scenter-frontend:{tag}
```

### Version Assignment

1. **Dev** — No version number. Tag is `dev-<sha7>`. Ephemeral.
2. **Testing** — Version assigned here. Workflow reads latest `v*` tag from `main`. If the PR title or any included commit starts with `feat`, bump MINOR and reset PATCH to 0. Otherwise bump PATCH. Tags image as `rc-X.Y.Z`.
3. **Release** — `release/vX.Y.Z` branch bumps `Chart.yaml` version + appVersion. PR merges to `main`.
4. **Main** — Creates git tag `vX.Y.Z`, builds final images, creates GitHub Release with auto-generated changelog.
5. **Hotfix** — Always bumps PATCH from current latest tag (e.g., `v0.23.0` → `v0.23.1`).

### E2E Test Gates

- Run on PRs to `testing` and `main` (the two promotion gates)
- Do NOT run on PRs to `develop/*` (too slow for feature iteration)
- Can be manually triggered on any branch via `workflow_dispatch`

---

## 3. Branch Protection Rules

### `main`

- Require PR: yes
- Required approvals: 1
- Required status checks: `backend`, `frontend`, `e2e`
- Require branches up to date: yes
- Allow force push: never
- Allow deletion: never

### `testing`

- Require PR: yes
- Required approvals: 1
- Required status checks: `backend`, `frontend`, `e2e`
- Require branches up to date: yes
- Allow force push: never
- Allow deletion: never

### `develop/ui` and `develop/backend`

- Require PR: yes
- Required approvals: 0 (self-merge OK — gate is at `testing`)
- Required status checks: `backend` or `frontend` (respective to domain)
- Require branches up to date: no (avoid constant rebasing)
- Allow force push: never
- Allow deletion: never

### Ephemeral Branches

`hotfix/*`, `release/*`, feature branches — no rulesets. Governed by protection on their target branch.

---

## 4. Promotion Workflows

### Normal Feature Development

```
1. git checkout develop/ui && git pull
2. git checkout -b develop/ui/feat/new-theme-picker
3. Work, commit, push
4. PR → develop/ui (CI: lint + unit tests) → squash merge
5. develop/ui auto-builds → GHCR dev-<sha>
6. (Optional) Deploy to homelab with values-dev.yaml
```

### Promoting to Testing

```
1. PR: develop/ui → testing (or develop/backend → testing)
2. CI + E2E run full suite
3. Can combine multiple features in one promotion PR
4. Merge commit to testing
5. testing auto-builds → GHCR rc-X.Y.Z
6. Deploy to homelab with values-test.yaml, validate
```

### Releasing to Production

```
1. git checkout testing && git checkout -b release/v0.23.0
2. Bump Chart.yaml version + appVersion → 0.23.0
3. Commit: "chore: release v0.23.0"
4. PR: release/v0.23.0 → main (CI + E2E final gate)
5. Squash merge
6. main auto-builds → GHCR v0.23.0 + latest (public)
7. Git tag v0.23.0, GitHub Release created
8. Deploy to homelab with values-homelab.yaml
9. Delete release branch
```

### Hotfix Flow

```
1. git checkout main && git checkout -b hotfix/fix-critical-bug
2. Fix, commit, push
3. PR: hotfix/* → main (CI + E2E, fast-tracked review)
4. Squash merge → GHCR v0.23.1 + latest, git tag, GitHub Release
5. Back-merge main into testing, develop/ui, develop/backend
6. Delete hotfix branch
```

### Cross-Domain Changes

When a feature touches both frontend and backend:

1. Backend work on `develop/backend/feat/new-endpoint` → merge to `develop/backend`
2. UI work on `develop/ui/feat/new-endpoint-ui` → merge to `develop/ui`
3. Promote both `develop/backend` and `develop/ui` to `testing` together
4. E2E tests on `testing` validate the integration

---

## 5. Helm Values per Environment

```yaml
# values-dev.yaml
backend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-backend
    tag: dev-a1b2c3d    # replace with actual SHA
    pullPolicy: Always
frontend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-frontend
    tag: dev-a1b2c3d
    pullPolicy: Always

# values-test.yaml
backend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-backend
    tag: rc-0.23.0
    pullPolicy: Always
frontend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-frontend
    tag: rc-0.23.0
    pullPolicy: Always

# values-homelab.yaml (production)
backend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-backend
    tag: v0.23.0         # or "latest"
    pullPolicy: IfNotPresent
frontend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-frontend
    tag: v0.23.0
    pullPolicy: IfNotPresent
```

---

## 6. CODEOWNERS

```
# .github/CODEOWNERS
*                   @maulepilot117
backend/            @maulepilot117
frontend/           @maulepilot117
helm/               @maulepilot117
.github/workflows/  @maulepilot117
```

---

## 7. What Changes from Today

| Before | After |
|--------|-------|
| Feature branches PR to main | Feature → develop/* → testing → release → main |
| `latest` + `sha-*` tags only | `dev-<sha>`, `rc-X.Y.Z`, `vX.Y.Z` + `latest` |
| Auto version bump on main push | Version assigned at testing, finalized at release |
| Images always public | Dev/RC private, release public |
| No branch protection | Protected main, testing, develop/* |
| Helm chart version manual (0.4.0) and out of sync with git tags (v0.22.1) | Synced on every release |
| No GitHub Releases | Auto-created with changelog on each release |
| 2 workflow files (ci.yml, e2e.yml) | 5 workflow files with clear separation of concerns |
