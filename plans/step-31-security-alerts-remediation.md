# Security Alerts Remediation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate all 81 open GitHub code scanning alerts by removing unnecessary binaries from the production Docker image and hardening CI workflow permissions.

**Architecture:** The 80 Trivy alerts are Go stdlib CVEs found in esbuild's pre-compiled binary (`bin/esbuild`), which gets copied into the production frontend Docker image via `COPY --from=builder /app/node_modules ./node_modules` and `COPY --from=builder /deno-dir /deno-dir`. esbuild is a build-time-only tool — it should never appear in the production image. The 1 CodeQL alert is a missing `permissions: {}` declaration on `e2e.yml`. The fix is surgical: exclude esbuild binaries from the production COPY steps and add the permissions block.

**Tech Stack:** Docker multi-stage builds, Deno, GitHub Actions

---

## Alert Breakdown

| Source | Alerts | Severity | Root Cause |
|--------|--------|----------|------------|
| Trivy — `app/node_modules/.deno/@esbuild+linux-x64@0.25.7/` | 21 | 6 error, 14 warning, 1 note | Old Go stdlib in esbuild binary |
| Trivy — `app/node_modules/.deno/@esbuild+linux-x64@0.25.12/` | 19 | 5 error, 13 warning, 1 note | Old Go stdlib in esbuild binary |
| Trivy — `deno-dir/npm/registry.npmjs.org/@esbuild/linux-x64/0.25.7/` | 21 | 6 error, 14 warning, 1 note | Old Go stdlib in esbuild binary |
| Trivy — `deno-dir/npm/registry.npmjs.org/@esbuild/linux-x64/0.25.12/` | 19 | 5 error, 13 warning, 1 note | Old Go stdlib in esbuild binary |
| CodeQL — `.github/workflows/e2e.yml` | 1 | warning | Missing permissions declaration |

---

### Task 1: Exclude esbuild binaries from production Docker image

**Files:**
- Modify: `frontend/Dockerfile:25-32` (production COPY steps)

The production image copies the entire `node_modules` and `deno-dir` from the builder stage. esbuild is only needed at build time (`deno task build` runs Vite which uses esbuild). The production runtime (`deno serve _fresh/server.js`) never invokes esbuild.

**Strategy:** Add a `.dockerignore`-style cleanup step in the builder stage that removes esbuild native binaries before the production COPY, or use a more targeted COPY that excludes them. The cleanest approach is a `RUN find` in the builder stage to delete esbuild binaries after the build completes.

- [ ] **Step 1: Verify esbuild is build-only**

Run locally to confirm esbuild binaries are not needed at runtime:

```bash
# In the frontend directory, after building:
deno task build
# Check if the built output references esbuild:
grep -r "esbuild" _fresh/ || echo "No esbuild references in build output"
```

Expected: No esbuild references in the built `_fresh/` output — confirms it's build-only.

- [ ] **Step 2: Add cleanup step to Dockerfile builder stage**

In `frontend/Dockerfile`, add a cleanup step after `RUN deno task build` and `RUN deno cache` that removes esbuild native binaries from both `node_modules` and the Deno cache:

```dockerfile
# Stage 1: Install dependencies and build (always on amd64 — Deno WASM loader
# crashes under QEMU ARM64 emulation, but build output is arch-independent JS)
FROM --platform=linux/amd64 denoland/deno:2.7.8 AS builder

WORKDIR /app

# Copy dependency files first for layer caching
COPY deno.json deno.lock* ./
RUN deno install

# Copy source and build
COPY . .
RUN deno task build

# Cache all runtime dependencies (ensures no network fetches at startup)
RUN deno cache _fresh/server.js || true

# Remove esbuild native binaries — build-only tool, not needed at runtime.
# These contain an embedded Go binary that triggers Go stdlib CVEs in Trivy.
RUN find /app/node_modules -path '*/esbuild/bin/esbuild' -delete 2>/dev/null; \
    find /app/node_modules -path '*/@esbuild/*/bin/esbuild' -delete 2>/dev/null; \
    find /deno-dir -path '*/esbuild/bin/esbuild' -delete 2>/dev/null; \
    find /deno-dir -path '*/@esbuild/*/bin/esbuild' -delete 2>/dev/null; \
    true

# Stage 2: Production runtime (uses target platform — arm64 or amd64)
# Use distroless variant to minimize OS-level CVEs (no shell, no package manager)
FROM denoland/deno:distroless-2.7.8

WORKDIR /app

# Copy built output, config, and cached dependencies
COPY --from=builder /app/_fresh ./_fresh
COPY --from=builder /app/deno.json ./
COPY --from=builder /app/deno.lock* ./
COPY --from=builder /app/static ./static
COPY --from=builder /app/node_modules ./node_modules

# Copy Deno cache from builder (includes ESM modules like Monaco)
COPY --from=builder /deno-dir /deno-dir

EXPOSE 8000

CMD ["serve", "--allow-net", "--allow-read=.", "--allow-env=BACKEND_URL,LOG,PORT,HOSTNAME", "--port", "8000", "_fresh/server.js"]
```

- [ ] **Step 3: Build and verify the image locally**

```bash
cd frontend
docker build -t k8scenter-frontend:test .
# Verify no esbuild binaries in the production image:
docker run --rm --entrypoint="" k8scenter-frontend:test find / -name "esbuild" -type f 2>/dev/null || echo "Clean"
# Verify the server starts:
docker run --rm -p 8000:8000 -e BACKEND_URL=http://localhost:8080 k8scenter-frontend:test &
sleep 3
curl -s http://localhost:8000 | head -5
docker stop $(docker ps -q --filter ancestor=k8scenter-frontend:test)
```

Expected: No esbuild binaries found. Server starts and serves HTML.

Note: The distroless image has no shell, so `find` won't work directly. Instead, verify by scanning with Trivy:

```bash
docker run --rm aquasec/trivy image --severity CRITICAL,HIGH --ignore-unfixed k8scenter-frontend:test 2>&1 | grep -i esbuild || echo "No esbuild CVEs"
```

- [ ] **Step 4: Commit**

```bash
git add frontend/Dockerfile
git commit -m "fix: remove esbuild binaries from production image to eliminate 80 Trivy CVEs

esbuild ships a pre-compiled Go binary that triggers Go stdlib vulnerability
alerts. It's only needed at build time (Vite bundling), never at runtime.
Delete the binaries in the builder stage before copying to production."
```

---

### Task 2: Add permissions to e2e.yml workflow

**Files:**
- Modify: `.github/workflows/e2e.yml:1-8`

The `ci.yml` workflow already has the correct pattern: top-level `permissions: {}` with per-job overrides. `e2e.yml` is missing this, triggering CodeQL alert #4.

- [ ] **Step 1: Add permissions block to e2e.yml**

Add `permissions: {}` after the `on:` block, matching the pattern in `ci.yml`. The e2e job only needs `contents: read` (to checkout code) plus artifact upload permissions:

```yaml
name: E2E Tests

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

# Deny by default — each job declares minimum permissions
permissions: {}

jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    permissions:
      contents: read
```

- [ ] **Step 2: Verify workflow syntax**

```bash
cd .github/workflows
# Check YAML is valid:
python3 -c "import yaml; yaml.safe_load(open('e2e.yml'))" && echo "Valid YAML"
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/e2e.yml
git commit -m "fix: add permissions block to e2e.yml (CodeQL alert #4)

Matches ci.yml pattern: deny-by-default at top level, minimum permissions
per job. Resolves the 'Workflow does not contain permissions' warning."
```

---

### Task 3: Verify alerts resolve on CI

- [ ] **Step 1: Push branch and verify CI passes**

```bash
git push origin HEAD
```

Wait for CI to complete. The Trivy scan on the frontend image should no longer find esbuild binaries. CodeQL should no longer flag e2e.yml.

- [ ] **Step 2: Confirm alerts auto-close**

After CI completes, check that code scanning alerts are resolved:

```bash
gh api repos/{owner}/{repo}/code-scanning/alerts -q '[.[] | select(.state == "open")] | length'
```

Expected: `0` open alerts.

If any esbuild alerts persist (because GitHub caches SARIF results from the old default branch), they will auto-close once the fix merges to `main` and the next CI run uploads clean SARIF.

- [ ] **Step 3: Dismiss any stale alerts if needed**

If alerts from old SARIF persist after merge, bulk-dismiss them:

```bash
# Only if alerts remain after merge to main + clean CI run:
for id in $(gh api repos/{owner}/{repo}/code-scanning/alerts -q '.[] | select(.state == "open" and .most_recent_instance.location.path | test("esbuild")) | .number'); do
  gh api -X PATCH repos/{owner}/{repo}/code-scanning/alerts/$id -f state=dismissed -f dismissed_reason=false_positive
done
```
