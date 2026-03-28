# Branching Strategy & CI/CD Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transition k8sCenter from push-to-main to full Gitflow with environment-specific CI/CD, private/public GHCR publishing, and branch protection.

**Architecture:** Five GitHub Actions workflows replace the current two. `ci.yml` handles PR validation for all branches. `ci-dev.yml`, `ci-test.yml`, and `ci-release.yml` handle image builds at each promotion stage. `e2e.yml` gates promotions to `testing` and `main`. Branch protection rulesets enforce the workflow via GitHub API.

**Tech Stack:** GitHub Actions, Docker Buildx, GHCR, GitHub CLI (`gh`), GitHub Rulesets API

**Spec:** `docs/superpowers/specs/2026-03-28-branching-strategy-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `.github/workflows/ci.yml` | Modify | PR validation (lint, test, scan) — expand triggers to all protected branches |
| `.github/workflows/ci-dev.yml` | Create | Build + push `dev-<sha>` images on push to `develop/*` |
| `.github/workflows/ci-test.yml` | Create | Build + push `rc-X.Y.Z` images on push to `testing` |
| `.github/workflows/ci-release.yml` | Create | Build + push `vX.Y.Z` + `latest` images on push to `main`, create GitHub Release |
| `.github/workflows/e2e.yml` | Modify | Expand triggers to PRs against `testing` and `main`, add `workflow_dispatch` |
| `.github/CODEOWNERS` | Create | Ownership rules for PR reviews |
| `helm/kubecenter/values-dev.yaml` | Create | Helm values template for dev image deployments |
| `helm/kubecenter/values-test.yaml` | Create | Helm values template for RC image deployments |
| `helm/kubecenter/values-homelab.yaml` | Modify | Update comments to reflect new tagging scheme |
| `CLAUDE.md` | Modify | Update branching/workflow documentation |
| `CONTRIBUTING.md` | Create | Contributor guide with branching workflow |

---

### Task 1: Create Long-Lived Branches

**Files:** None (git operations only)

- [ ] **Step 1: Create `testing` branch from `main`**

```bash
git checkout main
git pull origin main
git checkout -b testing
git push -u origin testing
```

- [ ] **Step 2: Create `develop/ui` branch from `main`**

```bash
git checkout main
git checkout -b develop/ui
git push -u origin develop/ui
```

- [ ] **Step 3: Create `develop/backend` branch from `main`**

```bash
git checkout main
git checkout -b develop/backend
git push -u origin develop/backend
```

- [ ] **Step 4: Return to `main`**

```bash
git checkout main
```

- [ ] **Step 5: Verify all branches exist on remote**

```bash
gh api repos/{owner}/{repo}/branches --jq '.[].name' | grep -E '^(main|testing|develop/)'
```

Expected output:
```
develop/backend
develop/ui
main
testing
```

---

### Task 2: Refactor `ci.yml` — PR Validation for All Protected Branches

This workflow runs lint + unit tests on PRs to any protected branch. It no longer builds images or creates version tags (those responsibilities move to dedicated workflows).

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Replace the entire `ci.yml` with the new version**

```yaml
name: CI

on:
  pull_request:
    branches: [main, testing, develop/ui, develop/backend]

permissions: {}

jobs:
  changes:
    name: Detect Changes
    runs-on: ubuntu-latest
    permissions:
      contents: read
    outputs:
      backend: ${{ steps.filter.outputs.backend }}
      frontend: ${{ steps.filter.outputs.frontend }}
    steps:
      - uses: actions/checkout@v4
      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            backend:
              - 'backend/**'
            frontend:
              - 'frontend/**'

  backend:
    name: Backend
    runs-on: ubuntu-latest
    permissions:
      contents: read
    needs: [changes]
    if: needs.changes.outputs.backend == 'true'
    defaults:
      run:
        working-directory: backend
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.26"
          cache-dependency-path: backend/go.sum

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test ./... -race -cover -count=1

      - name: Build
        run: go build -o /dev/null ./cmd/kubecenter

  frontend:
    name: Frontend
    runs-on: ubuntu-latest
    permissions:
      contents: read
    needs: [changes]
    if: needs.changes.outputs.frontend == 'true'
    defaults:
      run:
        working-directory: frontend
    steps:
      - uses: actions/checkout@v4

      - uses: denoland/setup-deno@v2
        with:
          deno-version: v2.x

      - name: Install dependencies
        run: deno install

      - name: Lint
        run: deno lint

      - name: Format check
        run: deno fmt --check

      - name: Build
        run: deno task build
```

- [ ] **Step 2: Verify the file is valid YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "OK"
```

Expected: `OK`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "refactor: ci.yml — PR validation only, expand triggers to all protected branches

Removes version-tag and image-build jobs (moved to dedicated workflows).
Triggers on PRs to main, testing, develop/ui, develop/backend."
```

---

### Task 3: Create `ci-dev.yml` — Dev Image Builds

Builds and pushes `dev-<sha>` images when code is merged to `develop/ui` or `develop/backend`. Images are private.

**Files:**
- Create: `.github/workflows/ci-dev.yml`

- [ ] **Step 1: Create the workflow file**

```yaml
name: Dev Build

on:
  push:
    branches: [develop/ui, develop/backend]

permissions: {}

jobs:
  changes:
    name: Detect Changes
    runs-on: ubuntu-latest
    permissions:
      contents: read
    outputs:
      backend: ${{ steps.filter.outputs.backend }}
      frontend: ${{ steps.filter.outputs.frontend }}
    steps:
      - uses: actions/checkout@v4
      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            backend:
              - 'backend/**'
            frontend:
              - 'frontend/**'

  build-backend:
    name: Build Backend (dev)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    needs: [changes]
    if: needs.changes.outputs.backend == 'true'
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Generate tag
        id: meta
        run: |
          SHORT_SHA=$(git rev-parse --short HEAD)
          echo "tag=dev-${SHORT_SHA}" >> "$GITHUB_OUTPUT"
          echo "commit=${SHORT_SHA}" >> "$GITHUB_OUTPUT"
          echo "date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$GITHUB_OUTPUT"

      - name: Build & push backend
        uses: docker/build-push-action@v6
        with:
          context: backend
          file: backend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: ghcr.io/${{ github.repository_owner }}/k8scenter-backend:${{ steps.meta.outputs.tag }}
          build-args: |
            VERSION=${{ steps.meta.outputs.tag }}
            COMMIT=${{ steps.meta.outputs.commit }}
            BUILD_DATE=${{ steps.meta.outputs.date }}
          cache-from: type=gha,scope=backend-dev
          cache-to: type=gha,mode=max,scope=backend-dev

      - name: Set package visibility to private
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api orgs/${{ github.repository_owner }}/packages/container/k8scenter-backend \
            --method PATCH --field visibility=private 2>/dev/null || \
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-backend \
            --method PATCH --field visibility=private 2>/dev/null || true

  build-frontend:
    name: Build Frontend (dev)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    needs: [changes]
    if: needs.changes.outputs.frontend == 'true'
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Generate tag
        id: meta
        run: |
          SHORT_SHA=$(git rev-parse --short HEAD)
          echo "tag=dev-${SHORT_SHA}" >> "$GITHUB_OUTPUT"

      - name: Build & push frontend
        uses: docker/build-push-action@v6
        with:
          context: frontend
          file: frontend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: ghcr.io/${{ github.repository_owner }}/k8scenter-frontend:${{ steps.meta.outputs.tag }}
          cache-from: type=gha,scope=frontend-dev
          cache-to: type=gha,mode=max,scope=frontend-dev

      - name: Set package visibility to private
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api orgs/${{ github.repository_owner }}/packages/container/k8scenter-frontend \
            --method PATCH --field visibility=private 2>/dev/null || \
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-frontend \
            --method PATCH --field visibility=private 2>/dev/null || true
```

- [ ] **Step 2: Verify YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci-dev.yml'))" && echo "OK"
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci-dev.yml
git commit -m "feat: add ci-dev.yml — build dev-<sha> images on develop/* push

Builds multi-arch images tagged dev-<sha7> and pushes to GHCR as private packages."
```

---

### Task 4: Create `ci-test.yml` — RC Image Builds

Builds and pushes `rc-X.Y.Z` images when code is merged to `testing`. Determines version by reading latest `v*` tag from `main` and bumping based on commit content.

**Files:**
- Create: `.github/workflows/ci-test.yml`

- [ ] **Step 1: Create the workflow file**

```yaml
name: Test Build

on:
  push:
    branches: [testing]

permissions: {}

jobs:
  determine-version:
    name: Determine RC Version
    runs-on: ubuntu-latest
    permissions:
      contents: read
    outputs:
      rc_version: ${{ steps.version.outputs.rc_version }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Calculate next version
        id: version
        run: |
          # Get latest release tag from main
          LATEST=$(git tag -l 'v*' --sort=-v:refname | head -1)
          if [ -z "$LATEST" ]; then
            LATEST="v0.0.0"
          fi

          MAJOR=$(echo "$LATEST" | sed 's/^v//' | cut -d. -f1)
          MINOR=$(echo "$LATEST" | sed 's/^v//' | cut -d. -f2)
          PATCH=$(echo "$LATEST" | sed 's/^v//' | cut -d. -f3)

          # Check if any commit in this push is a feature
          COMMIT_MSG=$(git log -1 --pretty=%s)
          if echo "$COMMIT_MSG" | grep -qiE '^feat|^Merge.*feat'; then
            MINOR=$((MINOR + 1))
            PATCH=0
          else
            PATCH=$((PATCH + 1))
          fi

          RC_VERSION="${MAJOR}.${MINOR}.${PATCH}"
          echo "rc_version=${RC_VERSION}" >> "$GITHUB_OUTPUT"
          echo "RC version: ${RC_VERSION} (from ${LATEST})"

  build-backend:
    name: Build Backend (RC)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      security-events: write
    needs: [determine-version]
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Set metadata
        id: meta
        run: |
          SHORT_SHA=$(git rev-parse --short HEAD)
          echo "tag=rc-${{ needs.determine-version.outputs.rc_version }}" >> "$GITHUB_OUTPUT"
          echo "version=${{ needs.determine-version.outputs.rc_version }}" >> "$GITHUB_OUTPUT"
          echo "commit=${SHORT_SHA}" >> "$GITHUB_OUTPUT"
          echo "date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$GITHUB_OUTPUT"

      - name: Build amd64 image for scanning
        uses: docker/build-push-action@v6
        with:
          context: backend
          file: backend/Dockerfile
          platforms: linux/amd64
          load: true
          push: false
          tags: k8scenter-backend:scan
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
            COMMIT=${{ steps.meta.outputs.commit }}
            BUILD_DATE=${{ steps.meta.outputs.date }}
          cache-from: type=gha,scope=backend-test

      - name: Scan with Trivy
        uses: aquasecurity/trivy-action@v0.35.0
        with:
          image-ref: k8scenter-backend:scan
          format: sarif
          output: trivy-backend.sarif
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "0"

      - name: Upload Trivy SARIF
        uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: trivy-backend.sarif
          category: trivy-backend-rc

      - name: Build & push backend
        uses: docker/build-push-action@v6
        with:
          context: backend
          file: backend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: ghcr.io/${{ github.repository_owner }}/k8scenter-backend:${{ steps.meta.outputs.tag }}
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
            COMMIT=${{ steps.meta.outputs.commit }}
            BUILD_DATE=${{ steps.meta.outputs.date }}
          cache-from: type=gha,scope=backend-test
          cache-to: type=gha,mode=max,scope=backend-test

      - name: Set package visibility to private
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-backend \
            --method PATCH --field visibility=private 2>/dev/null || true

  build-frontend:
    name: Build Frontend (RC)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      security-events: write
    needs: [determine-version]
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Set metadata
        id: meta
        run: |
          echo "tag=rc-${{ needs.determine-version.outputs.rc_version }}" >> "$GITHUB_OUTPUT"

      - name: Build amd64 image for scanning
        uses: docker/build-push-action@v6
        with:
          context: frontend
          file: frontend/Dockerfile
          platforms: linux/amd64
          load: true
          push: false
          tags: k8scenter-frontend:scan
          cache-from: type=gha,scope=frontend-test

      - name: Scan with Trivy
        uses: aquasecurity/trivy-action@v0.35.0
        with:
          image-ref: k8scenter-frontend:scan
          format: sarif
          output: trivy-frontend.sarif
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "0"

      - name: Upload Trivy SARIF
        uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: trivy-frontend.sarif
          category: trivy-frontend-rc

      - name: Build & push frontend
        uses: docker/build-push-action@v6
        with:
          context: frontend
          file: frontend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: ghcr.io/${{ github.repository_owner }}/k8scenter-frontend:${{ steps.meta.outputs.tag }}
          cache-from: type=gha,scope=frontend-test
          cache-to: type=gha,mode=max,scope=frontend-test

      - name: Set package visibility to private
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-frontend \
            --method PATCH --field visibility=private 2>/dev/null || true
```

- [ ] **Step 2: Verify YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci-test.yml'))" && echo "OK"
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci-test.yml
git commit -m "feat: add ci-test.yml — build rc-X.Y.Z images on testing push

Determines version from latest v* tag, applies Trivy scanning, pushes
multi-arch RC images to GHCR as private packages."
```

---

### Task 5: Create `ci-release.yml` — Production Release

Builds final `vX.Y.Z` + `latest` images when code is merged to `main`. Creates git tag and GitHub Release.

**Files:**
- Create: `.github/workflows/ci-release.yml`

- [ ] **Step 1: Create the workflow file**

```yaml
name: Release

on:
  push:
    branches: [main]

permissions: {}

jobs:
  changes:
    name: Detect Changes
    runs-on: ubuntu-latest
    permissions:
      contents: read
    outputs:
      backend: ${{ steps.filter.outputs.backend }}
      frontend: ${{ steps.filter.outputs.frontend }}
      code: ${{ steps.filter.outputs.code }}
    steps:
      - uses: actions/checkout@v4
      - uses: dorny/paths-filter@v3
        id: filter
        with:
          filters: |
            backend:
              - 'backend/**'
            frontend:
              - 'frontend/**'
            code:
              - 'backend/**'
              - 'frontend/**'

  version-tag:
    name: Create Version Tag
    runs-on: ubuntu-latest
    permissions:
      contents: write
    needs: [changes]
    if: needs.changes.outputs.code == 'true'
    outputs:
      version: ${{ steps.bump.outputs.version }}
      version_number: ${{ steps.bump.outputs.version_number }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Determine version from Chart.yaml
        id: bump
        run: |
          # Read version from Chart.yaml (set during release branch)
          VERSION=$(grep '^appVersion:' helm/kubecenter/Chart.yaml | sed 's/appVersion: *"\?\([^"]*\)"\?/\1/')

          # Fallback: calculate from tags if Chart.yaml wasn't bumped
          LATEST_TAG=$(git tag -l 'v*' --sort=-v:refname | head -1)
          if [ -z "$LATEST_TAG" ]; then
            LATEST_TAG="v0.0.0"
          fi
          LATEST_NUM=$(echo "$LATEST_TAG" | sed 's/^v//')

          # If Chart.yaml version matches an existing tag, auto-bump patch
          if git tag -l "v${VERSION}" | grep -q .; then
            MAJOR=$(echo "$LATEST_NUM" | cut -d. -f1)
            MINOR=$(echo "$LATEST_NUM" | cut -d. -f2)
            PATCH=$(echo "$LATEST_NUM" | cut -d. -f3)
            PATCH=$((PATCH + 1))
            VERSION="${MAJOR}.${MINOR}.${PATCH}"
          fi

          TAG="v${VERSION}"
          echo "version=${TAG}" >> "$GITHUB_OUTPUT"
          echo "version_number=${VERSION}" >> "$GITHUB_OUTPUT"
          echo "Creating tag: ${TAG}"

          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git tag "$TAG"
          git push origin "$TAG"

  build-backend:
    name: Build Backend (release)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      security-events: write
    needs: [changes, version-tag]
    if: needs.changes.outputs.backend == 'true'
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Set metadata
        id: meta
        run: |
          SHORT_SHA=$(git rev-parse --short HEAD)
          echo "tag=${{ needs.version-tag.outputs.version }}" >> "$GITHUB_OUTPUT"
          echo "version=${{ needs.version-tag.outputs.version_number }}" >> "$GITHUB_OUTPUT"
          echo "commit=${SHORT_SHA}" >> "$GITHUB_OUTPUT"
          echo "date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$GITHUB_OUTPUT"

      - name: Build amd64 image for scanning
        uses: docker/build-push-action@v6
        with:
          context: backend
          file: backend/Dockerfile
          platforms: linux/amd64
          load: true
          push: false
          tags: k8scenter-backend:scan
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
            COMMIT=${{ steps.meta.outputs.commit }}
            BUILD_DATE=${{ steps.meta.outputs.date }}
          cache-from: type=gha,scope=backend

      - name: Scan with Trivy
        uses: aquasecurity/trivy-action@v0.35.0
        with:
          image-ref: k8scenter-backend:scan
          format: sarif
          output: trivy-backend.sarif
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "0"

      - name: Upload Trivy SARIF
        uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: trivy-backend.sarif
          category: trivy-backend

      - name: Build & push backend
        uses: docker/build-push-action@v6
        with:
          context: backend
          file: backend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/k8scenter-backend:${{ steps.meta.outputs.tag }}
            ghcr.io/${{ github.repository_owner }}/k8scenter-backend:latest
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
            COMMIT=${{ steps.meta.outputs.commit }}
            BUILD_DATE=${{ steps.meta.outputs.date }}
          cache-from: type=gha,scope=backend
          cache-to: type=gha,mode=max,scope=backend

      - name: Set package visibility to public
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-backend \
            --method PATCH --field visibility=public 2>/dev/null || true

  build-frontend:
    name: Build Frontend (release)
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      security-events: write
    needs: [changes, version-tag]
    if: needs.changes.outputs.frontend == 'true'
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Set metadata
        id: meta
        run: |
          echo "tag=${{ needs.version-tag.outputs.version }}" >> "$GITHUB_OUTPUT"

      - name: Build amd64 image for scanning
        uses: docker/build-push-action@v6
        with:
          context: frontend
          file: frontend/Dockerfile
          platforms: linux/amd64
          load: true
          push: false
          tags: k8scenter-frontend:scan
          cache-from: type=gha,scope=frontend

      - name: Scan with Trivy
        uses: aquasecurity/trivy-action@v0.35.0
        with:
          image-ref: k8scenter-frontend:scan
          format: sarif
          output: trivy-frontend.sarif
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "0"

      - name: Upload Trivy SARIF
        uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: trivy-frontend.sarif
          category: trivy-frontend

      - name: Build & push frontend
        uses: docker/build-push-action@v6
        with:
          context: frontend
          file: frontend/Dockerfile
          platforms: linux/arm64,linux/amd64
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/k8scenter-frontend:${{ steps.meta.outputs.tag }}
            ghcr.io/${{ github.repository_owner }}/k8scenter-frontend:latest
          cache-from: type=gha,scope=frontend
          cache-to: type=gha,mode=max,scope=frontend

      - name: Set package visibility to public
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api users/${{ github.repository_owner }}/packages/container/k8scenter-frontend \
            --method PATCH --field visibility=public 2>/dev/null || true

  github-release:
    name: Create GitHub Release
    runs-on: ubuntu-latest
    permissions:
      contents: write
    needs: [version-tag, build-backend, build-frontend]
    if: |
      always() &&
      needs.version-tag.result == 'success' &&
      !contains(needs.*.result, 'failure')
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Create release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh release create ${{ needs.version-tag.outputs.version }} \
            --title "${{ needs.version-tag.outputs.version }}" \
            --generate-notes \
            --latest
```

- [ ] **Step 2: Verify YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci-release.yml'))" && echo "OK"
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci-release.yml
git commit -m "feat: add ci-release.yml — production builds, git tags, GitHub Releases

Reads version from Chart.yaml, creates git tag, builds multi-arch images
tagged vX.Y.Z + latest, sets GHCR visibility to public, creates GitHub Release."
```

---

### Task 6: Update `e2e.yml` — Gate Testing and Main Promotions

Expand triggers to run E2E on PRs to `testing` and `main`. Add `workflow_dispatch` for manual runs. Remove `push` trigger (E2E shouldn't re-run after merge).

**Files:**
- Modify: `.github/workflows/e2e.yml`

- [ ] **Step 1: Replace the trigger block (lines 1-7)**

Change the `on:` block from:

```yaml
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
```

To:

```yaml
on:
  pull_request:
    branches: [main, testing]
  workflow_dispatch:
    inputs:
      branch:
        description: 'Branch to test'
        required: false
        type: string
```

- [ ] **Step 2: Verify YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/e2e.yml'))" && echo "OK"
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/e2e.yml
git commit -m "feat: e2e.yml — gate testing and main promotions, add workflow_dispatch

E2E tests now run on PRs to testing and main (the two promotion gates).
Removed push trigger. Added manual dispatch for any-branch testing."
```

---

### Task 7: Create CODEOWNERS

**Files:**
- Create: `.github/CODEOWNERS`

- [ ] **Step 1: Create the file**

```
# Default owner for all files
*                   @maulepilot117

# Domain-specific ownership (meaningful when contributors join)
backend/            @maulepilot117
frontend/           @maulepilot117
helm/               @maulepilot117
.github/workflows/  @maulepilot117
e2e/                @maulepilot117
```

- [ ] **Step 2: Commit**

```bash
git add .github/CODEOWNERS
git commit -m "feat: add CODEOWNERS — all paths owned by @maulepilot117"
```

---

### Task 8: Create Helm Values for Dev and Test Environments

**Files:**
- Create: `helm/kubecenter/values-dev.yaml`
- Create: `helm/kubecenter/values-test.yaml`
- Modify: `helm/kubecenter/values-homelab.yaml`

- [ ] **Step 1: Create `values-dev.yaml`**

```yaml
# Development environment values
# Usage: helm upgrade k8scenter helm/kubecenter -f helm/kubecenter/values-dev.yaml
# Replace <SHA> with the actual 7-char commit SHA from the dev build

backend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-backend
    tag: dev-<SHA>        # e.g., dev-a1b2c3d
    pullPolicy: Always
  config:
    dev: true

frontend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-frontend
    tag: dev-<SHA>
    pullPolicy: Always

# Dev images are private — pull secret required
imagePullSecrets:
  - name: ghcr-pull-secret
```

- [ ] **Step 2: Create `values-test.yaml`**

```yaml
# Testing / Release Candidate environment values
# Usage: helm upgrade k8scenter helm/kubecenter -f helm/kubecenter/values-test.yaml
# Replace <VERSION> with the RC version (e.g., 0.23.0)

backend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-backend
    tag: rc-<VERSION>     # e.g., rc-0.23.0
    pullPolicy: Always
  config:
    dev: true

frontend:
  image:
    repository: ghcr.io/maulepilot117/k8scenter-frontend
    tag: rc-<VERSION>
    pullPolicy: Always

# RC images are private — pull secret required
imagePullSecrets:
  - name: ghcr-pull-secret
```

- [ ] **Step 3: Update `values-homelab.yaml` comment**

Change line 26 from:

```yaml
# Packages are public on GHCR — no pull secret needed
```

To:

```yaml
# Release images are public on GHCR — no pull secret needed
# For dev/test images, add: imagePullSecrets: [{name: ghcr-pull-secret}]
```

- [ ] **Step 4: Commit**

```bash
git add helm/kubecenter/values-dev.yaml helm/kubecenter/values-test.yaml helm/kubecenter/values-homelab.yaml
git commit -m "feat: add values-dev.yaml and values-test.yaml for environment-specific deploys

Dev uses dev-<sha> tags (private), test uses rc-X.Y.Z tags (private),
production uses vX.Y.Z or latest tags (public)."
```

---

### Task 9: Set Up Branch Protection Rulesets via GitHub CLI

**Files:** None (GitHub API operations)

- [ ] **Step 1: Create ruleset for `main`**

```bash
gh api repos/{owner}/{repo}/rulesets --method POST --input - <<'EOF'
{
  "name": "Protect main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/main"],
      "exclude": []
    }
  },
  "rules": [
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_last_push_approval": false
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "Backend" },
          { "context": "Frontend" },
          { "context": "e2e" }
        ]
      }
    },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
EOF
```

- [ ] **Step 2: Create ruleset for `testing`**

```bash
gh api repos/{owner}/{repo}/rulesets --method POST --input - <<'EOF'
{
  "name": "Protect testing",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/testing"],
      "exclude": []
    }
  },
  "rules": [
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_last_push_approval": false
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "Backend" },
          { "context": "Frontend" },
          { "context": "e2e" }
        ]
      }
    },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
EOF
```

- [ ] **Step 3: Create ruleset for `develop/*`**

```bash
gh api repos/{owner}/{repo}/rulesets --method POST --input - <<'EOF'
{
  "name": "Protect develop branches",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/develop/ui", "refs/heads/develop/backend"],
      "exclude": []
    }
  },
  "rules": [
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": false,
        "require_last_push_approval": false
      }
    },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
EOF
```

- [ ] **Step 4: Verify rulesets were created**

```bash
gh api repos/{owner}/{repo}/rulesets --jq '.[].name'
```

Expected:
```
Protect main
Protect testing
Protect develop branches
```

---

### Task 10: Create CONTRIBUTING.md

**Files:**
- Create: `CONTRIBUTING.md`

- [ ] **Step 1: Create the file**

```markdown
# Contributing to k8sCenter

## Branch Structure

| Branch | Purpose | Merges from |
|--------|---------|-------------|
| `main` | Production releases | `release/*`, `hotfix/*` |
| `testing` | Release candidates | `develop/ui`, `develop/backend` |
| `develop/ui` | Frontend integration | `develop/ui/feat/*`, `develop/ui/fix/*` |
| `develop/backend` | Backend integration | `develop/backend/feat/*`, `develop/backend/fix/*` |

**Never commit directly to `main`, `testing`, `develop/ui`, or `develop/backend`.** All changes go through pull requests.

## Workflow

### Feature Development

1. Check out the appropriate develop branch and create a feature branch:
   ```bash
   git checkout develop/ui && git pull
   git checkout -b develop/ui/feat/my-feature
   ```
2. Make changes, commit with conventional commit messages (`feat:`, `fix:`, `refactor:`)
3. Push and open a PR to the parent develop branch
4. CI runs lint + unit tests. Merge when green.

### Promoting to Testing

1. Open a PR from `develop/ui` or `develop/backend` to `testing`
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
3. After merge, back-merge `main` into `testing` and `develop/*`

## Naming Conventions

- Feature branches: `develop/{ui,backend}/feat/short-description`
- Bug fixes: `develop/{ui,backend}/fix/short-description`
- Refactors: `develop/{ui,backend}/refactor/short-description`
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
```

- [ ] **Step 2: Commit**

```bash
git add CONTRIBUTING.md
git commit -m "docs: add CONTRIBUTING.md — branching workflow and conventions"
```

---

### Task 11: Update CLAUDE.md

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Replace the `Pre-Merge Requirements` section**

Find the section starting with `## Pre-Merge Requirements` and replace it with:

```markdown
## Branching Strategy

Full Gitflow with environment-specific CI/CD. See `CONTRIBUTING.md` for the complete workflow.

**Branches:** `main` (production) ← `testing` (RC) ← `develop/ui` | `develop/backend` (dev) ← feature branches

**Image tags:** `dev-<sha>` (private) → `rc-X.Y.Z` (private) → `vX.Y.Z` + `latest` (public)

**Rules:**
- NEVER commit directly to `main`, `testing`, `develop/ui`, or `develop/backend`
- All changes go through PRs with required status checks
- Feature branches: `develop/{ui,backend}/{feat,fix,refactor}/description`
- Hotfixes branch from `main`, back-merge after release
- Version bumps happen on `release/vX.Y.Z` branches (Chart.yaml version + appVersion)

**Every PR to `testing` or `main` requires `/review` before merge.** Smoke test against homelab when backend/frontend changes are in scope.

Credentials: `admin` / `admin123`, setup token: `homelab-setup-token`.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md — replace pre-merge section with branching strategy"
```

---

### Task 12: Update Memory Files

**Files:**
- Create: `/Users/Chris.White/.claude/projects/-Users-Chris-White-Documents-code-projects-k8sCenter/memory/feedback_branching_strategy.md`
- Modify: `/Users/Chris.White/.claude/projects/-Users-Chris-White-Documents-code-projects-k8sCenter/memory/feedback_branch_workflow.md`
- Modify: `/Users/Chris.White/.claude/projects/-Users-Chris-White-Documents-code-projects-k8sCenter/memory/MEMORY.md`

- [ ] **Step 1: Create `feedback_branching_strategy.md`**

```markdown
---
name: Gitflow Branching Strategy
description: Full Gitflow with develop/ui, develop/backend, testing, main. Environment-specific GHCR tags. Never edit protected branches directly.
type: feedback
---

All code changes follow Gitflow: feature branches → develop/{ui,backend} → testing → release/vX.Y.Z → main.

**Why:** User wants professional GitHub practices suitable for multi-contributor projects. Established 2026-03-28.

**How to apply:**
- NEVER create feature branches from main. Always branch from `develop/ui` or `develop/backend`.
- Feature branches named: `develop/{ui,backend}/{feat,fix,refactor}/description`
- PRs to `develop/*`: CI only (lint + tests). Self-merge OK.
- PRs to `testing`: CI + E2E required. Needs 1 approval.
- PRs to `main`: CI + E2E required. Needs 1 approval. Only from `release/*` or `hotfix/*`.
- Image tags: `dev-<sha>` (private), `rc-X.Y.Z` (private), `vX.Y.Z` + `latest` (public).
- Hotfixes branch from main, back-merge to testing + develop/* after release.
- Version bumps happen on release branches by updating Chart.yaml version + appVersion.
```

- [ ] **Step 2: Update `feedback_branch_workflow.md` to reference the new strategy**

Replace contents with:

```markdown
---
name: Branch Workflow — Superseded
description: SUPERSEDED by feedback_branching_strategy.md. See that file for current Gitflow rules.
type: feedback
---

This file is superseded by `feedback_branching_strategy.md` which contains the full Gitflow branching strategy established 2026-03-28.
```

- [ ] **Step 3: Update `MEMORY.md` index**

Replace the line:

```
- [feedback_branch_workflow.md](feedback_branch_workflow.md) — NEVER push major changes to main. Always use feature branches + PRs.
```

With:

```
- [feedback_branching_strategy.md](feedback_branching_strategy.md) — Full Gitflow: feature → develop/* → testing → release → main. Environment GHCR tags. Never edit protected branches.
- [feedback_branch_workflow.md](feedback_branch_workflow.md) — SUPERSEDED by feedback_branching_strategy.md
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md  # if not already committed
git commit -m "docs: update memory files with Gitflow branching strategy"
```

---

### Task 13: Final Validation & Push

- [ ] **Step 1: Verify all workflow files are valid YAML**

```bash
for f in .github/workflows/*.yml; do
  python3 -c "import yaml; yaml.safe_load(open('$f'))" && echo "$f: OK" || echo "$f: FAIL"
done
```

Expected: All OK

- [ ] **Step 2: Verify all expected files exist**

```bash
ls -la .github/workflows/ci.yml .github/workflows/ci-dev.yml .github/workflows/ci-test.yml .github/workflows/ci-release.yml .github/workflows/e2e.yml .github/CODEOWNERS CONTRIBUTING.md helm/kubecenter/values-dev.yaml helm/kubecenter/values-test.yaml
```

- [ ] **Step 3: Run lint to make sure nothing is broken**

```bash
make lint-frontend && make lint-backend
```

- [ ] **Step 4: Push the branch and create PR**

Since we can no longer push to `main` directly (branch protection), all these changes should be on a feature branch merged through the new workflow. However, since the branch protection rulesets don't exist yet until Task 9 is executed, the initial setup commits can be pushed to main as a bootstrap. After Task 9, all subsequent changes must follow the new workflow.

```bash
git push origin main
```

- [ ] **Step 5: Verify CI workflows trigger correctly**

```bash
gh run list --limit 5
```
