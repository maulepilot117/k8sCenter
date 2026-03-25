# Step 24: E2E Tests with Playwright

## Overview

Add end-to-end browser tests using Playwright against a real kind cluster. Tests cover the full user journey: setup/login, resource browsing, wizard creation flows, YAML tools, WebSocket live updates, settings, and monitoring pages. Tests run in CI via GitHub Actions.

---

## Architecture Decisions

### Runtime: Node-based Playwright in separate `e2e/` directory

Playwright is a Node.js tool. Running it via Deno has compatibility issues (denoland/deno#31595). The E2E tests live in a top-level `e2e/` directory with their own `package.json`, completely independent from the Deno frontend and Go backend.

### Auth: UI-driven login in `auth.setup.ts` with `storageState` reuse

The access token is in-memory only (Preact signals), so API-based login cannot inject it into the browser context. Tests drive the login UI once in a `setup` project, persist cookies + localStorage via `storageState`, and all subsequent test projects reuse that state. This also avoids hitting the 5 req/min rate limiter.

**Important:** `storageState` preserves the httpOnly refresh cookie but NOT the in-memory access token. Each test starts with no access token — the first API call triggers a transparent refresh via the saved cookie. This is the expected flow and matches real-world page reload behavior.

### Cluster: kind cluster with backend running outside (dev mode)

The backend runs with `KUBECENTER_DEV=true` using the kind cluster's kubeconfig. No Helm deployment needed for E2E tests. PostgreSQL runs via docker-compose (local) or GitHub Actions service container (CI) for settings/audit persistence.

### Test isolation: Random-suffixed resource names + afterEach cleanup + labels

Each wizard test creates resources with `e2e-{kind}-{random}` names and an `e2e: "true"` label. Resources are deleted in `afterEach`. A CI cleanup step also runs `kubectl delete` with label selector as a safety net for orphaned resources.

### No Page Object Models (start simple, extract later)

No POM class hierarchy. Tests use Playwright's built-in locators directly. A single `helpers.ts` exports shared utility functions for wizard flows, resource cleanup, and common assertions. POMs can be extracted later if actual duplication emerges.

### Data-driven tests for repeated patterns

Resource browsing (all types) and wizard flows (all 15 types) use parameterized data-driven tests — one spec file per pattern, not one per resource type.

### Monaco/CodeMirror: Skip inline editing, test render + apply-as-is

Monaco and CodeMirror editors cannot be filled with standard Playwright `.fill()`. Tests verify the editor renders and that Apply works with the generated YAML. Inline YAML editing is not tested.

### Monitoring: Test the "not configured" state

A vanilla kind cluster has no Prometheus. Monitoring tests verify the "Prometheus Not Available" message renders. Metrics tab tests verify the tab is clickable and shows the appropriate empty state.

---

## Project Structure

```
e2e/
├── package.json                    # Node.js project with @playwright/test
├── package-lock.json
├── tsconfig.json                   # Strict TypeScript config
├── playwright.config.ts            # Config: webServer, projects, timeouts
├── .gitignore                      # playwright/.auth/, test-results/, playwright-report/
├── kind-config.yaml                # kind cluster config (single node)
├── helpers.ts                      # Shared utility functions (wizard flow, cleanup, assertions)
├── fixtures/
│   ├── auth.setup.ts               # Global auth: setup/init + login + storageState
│   ├── base.ts                     # Extended test fixture (reducedMotion, refresh wait)
│   └── k8s/
│       ├── test-clusterrolebinding.yaml  # admin ClusterRoleBinding
│       └── test-namespace.yaml           # e2e-test namespace
└── tests/
    ├── auth.spec.ts                # Login, logout, token refresh, session persistence
    ├── dashboard.spec.ts           # Cluster overview loads with real data
    ├── navigation.spec.ts          # Sidebar links, error page, dark mode
    ├── resource-browse.spec.ts     # Data-driven: all resource types load in table
    ├── resource-detail.spec.ts     # Detail tabs: overview, YAML, events, metrics
    ├── wizard-flows.spec.ts        # Data-driven: all 15 wizard create flows
    ├── yaml-apply.spec.ts          # Validate + apply
    ├── websocket.spec.ts           # Live ADDED/DELETED events
    ├── settings.spec.ts            # General, users, audit
    └── monitoring.spec.ts          # Status, prometheus, dashboards pages
```

**Total: ~14 files** (vs ~45 in the pre-review plan).

---

## Implementation Phases

### Phase 1: Infrastructure + Auth + Smoke Tests

**Goal:** Playwright project setup, CI workflow, auth fixture, and first passing tests.

#### 1.1 Create `e2e/` project

**`e2e/package.json`:**
```json
{
  "name": "k8scenter-e2e",
  "private": true,
  "scripts": {
    "test": "playwright test",
    "test:smoke": "playwright test --grep @smoke"
  },
  "devDependencies": {
    "@playwright/test": "^1.51.0",
    "@types/node": "^22.0.0"
  }
}
```

**`e2e/tsconfig.json`:**
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true
  },
  "include": ["**/*.ts"]
}
```

**`e2e/playwright.config.ts`:**
```typescript
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI
    ? [['github'], ['html', { open: 'never' }]]
    : [['html', { open: 'on-failure' }]],

  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:5173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  // Backend must start first (frontend BFF proxy depends on it)
  webServer: [
    {
      command: 'go run ./cmd/kubecenter --config ""',
      cwd: '../backend',
      url: 'http://localhost:8080/healthz',
      timeout: 60_000,
      reuseExistingServer: !process.env.CI,
      env: {
        KUBECENTER_DEV: 'true',
        KUBECENTER_AUTH_JWTSECRET: 'e2e-test-secret-minimum-32-bytes-long!!',
        KUBECENTER_AUTH_SETUPTOKEN: 'e2e-setup-token',
      },
    },
    {
      command: 'deno task dev',
      cwd: '../frontend',
      url: 'http://localhost:5173',
      timeout: 120_000,
      reuseExistingServer: !process.env.CI,
    },
  ],

  projects: [
    { name: 'setup', testMatch: /.*\.setup\.ts/ },
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        storageState: 'playwright/.auth/admin.json',
      },
      dependencies: ['setup'],
    },
  ],
});
```

**`e2e/kind-config.yaml`:**
```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
```

#### 1.2 Auth setup fixture

**`e2e/fixtures/auth.setup.ts`:**
```typescript
import { test as setup, expect } from '@playwright/test';
import { fileURLToPath } from 'url';
import path from 'path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const authFile = path.join(__dirname, '../playwright/.auth/admin.json');

setup('create admin and authenticate', async ({ page, request }) => {
  // Idempotent setup — ignore 409 if already initialized
  await request.post('/api/v1/setup/init', {
    data: { username: 'admin', password: 'admin123', setupToken: 'e2e-setup-token' },
    headers: { 'X-Requested-With': 'XMLHttpRequest' },
    failOnStatusCode: false,
  });

  // Log in via UI (required — access token is in-memory signals)
  await page.goto('/login');
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill('admin123');
  await page.getByRole('button', { name: /sign in/i }).click();

  // Wait for dashboard to confirm auth
  await page.waitForURL('/');
  await expect(page.getByText(/cluster overview|dashboard/i)).toBeVisible();

  // Persist cookies (httpOnly refresh token) + localStorage
  await page.context().storageState({ path: authFile });
});
```

#### 1.3 Base fixture

**`e2e/fixtures/base.ts`:**
```typescript
import { test as base, expect } from '@playwright/test';

export const test = base.extend({
  page: async ({ page }, use) => {
    // Disable CSS animations for test stability
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await use(page);
  },
});

export { expect };
```

#### 1.4 Helpers

**`e2e/helpers.ts`:**
```typescript
import { type Page, type APIRequestContext, expect } from '@playwright/test';

/** Generate a unique E2E resource name */
export function e2eName(kind: string): string {
  const rand = Math.random().toString(36).slice(2, 6);
  return `e2e-${kind}-${rand}`;
}

/** Delete a k8s resource via the API */
export async function deleteResource(
  request: APIRequestContext,
  kind: string,
  namespace: string,
  name: string,
) {
  await request.delete(`/api/v1/resources/${kind}/${namespace}/${name}`, {
    headers: { 'X-Requested-With': 'XMLHttpRequest' },
    failOnStatusCode: false,
  });
}

/** Wait for the resource table to finish loading */
export async function waitForTableLoaded(page: Page) {
  // Wait for loading spinner to disappear and at least the header row to exist
  await expect(page.getByRole('table')).toBeVisible();
  await expect(page.locator('.animate-spin')).not.toBeVisible();
}
```

#### 1.5 K8s fixtures

**`e2e/fixtures/k8s/test-clusterrolebinding.yaml`:**
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: e2e-admin-binding
subjects:
  - kind: User
    name: admin
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
```

**`e2e/fixtures/k8s/test-namespace.yaml`:**
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: e2e-test
```

#### 1.6 Makefile targets

Add to `Makefile`:
```makefile
test-e2e:
	cd e2e && npx playwright test

test-e2e-ui:
	cd e2e && npx playwright test --ui
```

#### 1.7 CI workflow

**`.github/workflows/e2e.yml`:**
```yaml
name: E2E Tests

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 30

    services:
      postgres:
        image: postgres:17-alpine
        env:
          POSTGRES_USER: k8scenter
          POSTGRES_PASSWORD: k8scenter
          POSTGRES_DB: k8scenter
        ports:
          - 5432:5432
        options: >-
          --health-cmd "pg_isready -U k8scenter"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: backend/go.mod
          cache: true

      - uses: denoland/setup-deno@v2
        with:
          deno-version: '~2'

      - name: Install frontend dependencies
        working-directory: frontend
        run: deno install

      - uses: actions/setup-node@v4
        with:
          node-version: lts/*

      - name: Create kind cluster
        uses: helm/kind-action@v1
        with:
          cluster_name: e2e
          config: e2e/kind-config.yaml
          wait: 120s

      - name: Apply test RBAC
        run: kubectl apply -f e2e/fixtures/k8s/

      - name: Install Playwright
        working-directory: e2e
        run: |
          npm ci
          npx playwright install chromium --with-deps

      - name: Run E2E tests
        working-directory: e2e
        env:
          CI: "true"
        run: npx playwright test

      - name: Cleanup orphaned E2E resources
        if: ${{ !cancelled() }}
        run: |
          kubectl delete all,configmaps,secrets,pvc,rolebindings,clusterrolebindings \
            -l e2e=true --all-namespaces --ignore-not-found || true

      - name: Upload report
        uses: actions/upload-artifact@v4
        if: ${{ !cancelled() }}
        with:
          name: playwright-report
          path: e2e/playwright-report/
          retention-days: 14

      - name: Upload traces on failure
        uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: test-results
          path: e2e/test-results/
          retention-days: 7
```

#### 1.8 First smoke tests

**`e2e/tests/auth.spec.ts`:**
```typescript
import { test, expect } from '../fixtures/base';

test.describe('Auth @smoke', () => {
  test('logs in with valid credentials', async ({ page }) => {
    // storageState already has us logged in — verify dashboard loads
    await page.goto('/');
    await expect(page.getByText(/cluster overview|dashboard/i)).toBeVisible();
  });

  test('refreshes session on page reload', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText(/cluster overview|dashboard/i)).toBeVisible();
    // Reload clears in-memory token — refresh cookie should restore session
    await page.reload();
    await expect(page.getByText(/cluster overview|dashboard/i)).toBeVisible();
  });
});
```

**`e2e/tests/dashboard.spec.ts`:**
```typescript
import { test, expect } from '../fixtures/base';

test.describe('Dashboard @smoke', () => {
  test('loads with real cluster data', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText(/cluster overview|dashboard/i)).toBeVisible();
    await expect(page.getByText(/node/i)).toBeVisible();
  });
});
```

**Files to create:** `e2e/package.json`, `e2e/tsconfig.json`, `e2e/playwright.config.ts`, `e2e/.gitignore`, `e2e/kind-config.yaml`, `e2e/helpers.ts`, `e2e/fixtures/auth.setup.ts`, `e2e/fixtures/base.ts`, `e2e/fixtures/k8s/test-clusterrolebinding.yaml`, `e2e/fixtures/k8s/test-namespace.yaml`, `e2e/tests/auth.spec.ts`, `e2e/tests/dashboard.spec.ts`, `.github/workflows/e2e.yml`

**Files to modify:** `Makefile` (add `test-e2e` targets), `.gitignore` (add e2e artifacts)

**Success criteria:** `npx playwright test` passes locally against a kind cluster.

---

### Phase 2: All Remaining Tests

**Goal:** Complete test coverage for navigation, resource browsing, wizards, YAML, WebSocket, settings, and monitoring. Tests are independent and can be written in any order.

#### 2.1 Navigation

**`e2e/tests/navigation.spec.ts`:**
- Data-driven sidebar test: iterate all nav section hrefs, assert no 404
- Error page: navigate to `/nonexistent-page`, assert error page renders
- Dark mode: toggle to dark, verify `<html>` has `dark` class, reload, verify persistence

#### 2.2 Resource browsing (data-driven)

**`e2e/tests/resource-browse.spec.ts`:**

One parameterized test over all resource types:
```typescript
const resources = [
  { kind: 'deployments', path: '/workloads/deployments', hasRows: true },
  { kind: 'pods', path: '/workloads/pods', hasRows: true },
  { kind: 'services', path: '/networking/services', hasRows: true },
  { kind: 'configmaps', path: '/config/configmaps', hasRows: true },
  { kind: 'nodes', path: '/cluster/nodes', hasRows: true },
  { kind: 'namespaces', path: '/cluster/namespaces', hasRows: true },
  { kind: 'clusterroles', path: '/rbac/clusterroles', hasRows: true },
  { kind: 'storageclasses', path: '/cluster/storageclasses', hasRows: false },
  { kind: 'hpas', path: '/scaling/hpas', hasRows: false },
  // ... more types
];

for (const r of resources) {
  test(`${r.kind} table loads`, async ({ page }) => {
    await page.goto(r.path);
    await waitForTableLoaded(page);
    if (r.hasRows) {
      await expect(page.getByRole('row')).toHaveCount({ minimum: 2 });
    }
  });
}
```

#### 2.3 Resource detail tabs

**`e2e/tests/resource-detail.spec.ts`:**
- Navigate to a known Deployment (coredns in kube-system), click into detail
- Switch to each tab: Overview, YAML, Events, Metrics
- Overview: content renders
- YAML: `.cm-editor` visible
- Events: tab activates
- Metrics: shows "Prometheus Not Available" or "No Metrics Available"
- Namespace filter: switch namespace, verify list changes
- Search: type text, verify table filters

#### 2.4 Wizard flows (data-driven)

**`e2e/tests/wizard-flows.spec.ts`:**

One parameterized test over all wizard types:
```typescript
const wizards = [
  {
    kind: 'deployment',
    createPath: '/workloads/deployments/new',
    listPath: '/workloads/deployments',
    apiKind: 'deployments',
    fields: [
      { label: 'Name', value: '$NAME' },
      { label: 'Container Image', value: 'nginx:alpine' },
    ],
    steps: 3, // number of Next clicks before review
  },
  {
    kind: 'configmap',
    createPath: '/config/configmaps/new',
    listPath: '/config/configmaps',
    apiKind: 'configmaps',
    fields: [{ label: 'Name', value: '$NAME' }],
    steps: 1,
  },
  // ... all 15 wizard types
];

for (const w of wizards) {
  test.describe(`${w.kind} wizard`, () => {
    let resourceName: string;

    test.beforeAll(() => {
      resourceName = e2eName(w.kind);
    });

    test(`creates ${w.kind} via wizard`, async ({ page }) => {
      await page.goto(w.createPath);
      // Fill fields (replace $NAME placeholder)
      for (const f of w.fields) {
        const value = f.value === '$NAME' ? resourceName : f.value;
        await page.getByLabel(f.label).fill(value);
      }
      // Advance through steps
      for (let i = 0; i < w.steps; i++) {
        await page.getByRole('button', { name: /next/i }).click();
      }
      // Review + Apply
      await page.getByRole('button', { name: /apply/i }).click();
      // Verify success
      await expect(page.getByText(/successfully|created/i)).toBeVisible();
    });

    test.afterAll(async ({ request }) => {
      await deleteResource(request, w.apiKind, 'default', resourceName);
    });
  });
}
```

Wizards with prerequisites (HPA, PDB) create a Deployment in `beforeAll`.

#### 2.5 YAML apply

**`e2e/tests/yaml-apply.spec.ts`:**
- Navigate to `/tools/yaml-apply`
- Verify editor renders
- Click Validate — assert success or error state
- Click Apply — assert result message
- Cleanup created resource

#### 2.6 WebSocket live updates

**`e2e/tests/websocket.spec.ts`:**
- Navigate to Deployments list, wait for table to load
- Create a Deployment via `page.request.post()`
- Assert new row appears within 10 seconds (no page reload)
- Delete via API, assert row disappears

#### 2.7 Settings

**`e2e/tests/settings.spec.ts`:**
- `/settings/general` loads
- `/settings/users` — admin user appears in list
- `/settings/audit` — audit table renders

#### 2.8 Monitoring

**`e2e/tests/monitoring.spec.ts`:**
- `/monitoring` — status page loads
- `/monitoring/prometheus` — PromQL page loads
- `/monitoring/dashboards` — dashboards page loads
- All show "not configured" or appropriate empty state

---

## Acceptance Criteria

### Functional Requirements

- [ ] `npx playwright test` passes locally against a kind cluster
- [ ] CI workflow runs on push to main and PRs (with PostgreSQL service)
- [ ] Auth setup (setup/init + login) works idempotently
- [ ] All sidebar navigation links resolve without 404
- [ ] Dashboard loads with real cluster data (nodes > 0)
- [ ] Resource tables load for all tested resource types
- [ ] Detail pages render with Overview, YAML, Events, Metrics tabs
- [ ] All 15 wizard flows create resources successfully
- [ ] YAML validate and apply work
- [ ] WebSocket live updates reflect created/deleted resources
- [ ] Dark mode toggle works and persists
- [ ] Namespace selector filters resources
- [ ] Search filters table rows
- [ ] Settings pages load (general, users, audit)
- [ ] Monitoring pages load (status, prometheus, dashboards)

### Non-Functional Requirements

- [ ] Total test suite completes in < 10 minutes in CI
- [ ] Test failures produce Playwright traces + screenshots for debugging
- [ ] Tests are not flaky (< 5% flake rate with retries)

### Quality Gates

- [ ] All tests pass on CI before merging
- [ ] Playwright HTML report uploaded as CI artifact
- [ ] No `page.waitForTimeout()` calls (use proper assertions)

---

## Out of Scope (Deferred)

- **OIDC/LDAP auth flows** — Require external IdP, covered by backend unit tests
- **Cilium-specific features** (CiliumPolicyEditor, FlowViewer, CNI status) — Require Cilium in kind
- **Multi-cluster switching** — Requires multiple cluster registrations
- **Visual regression testing** — Add in a future phase once baseline UI is stable
- **Performance/Lighthouse testing** — Add as a separate nightly workflow
- **Pod terminal (exec)** — Complex WebSocket + SPDY interaction
- **Pod log streaming** — Requires long-running pod with stdout
- **Multi-browser testing** (Firefox, WebKit) — Add as nightly workflow, not PR gate
- **Monaco/CodeMirror inline editing** — Technical limitation with Playwright `.fill()`

---

## Dependencies & Risks

| Risk | Mitigation |
|---|---|
| Kind cluster resource creation is slow | Use `expect.poll()` with generous timeouts |
| Rate limiter blocks test login | Single auth setup with `storageState` reuse |
| Backend needs PostgreSQL | Docker-compose locally, GitHub Actions service container in CI |
| Tests share cluster state | Random resource names + `e2e=true` label + afterEach cleanup + CI safety net |
| CI time budget exceeded | Run Chromium only, sequential workers, `@smoke` tag for fast subset |
| No Prometheus in kind cluster | Test the "not configured" state explicitly |
| Monaco editor untestable via fill() | Test render + apply-as-is, skip inline editing |
| Orphaned test resources | Label-based `kubectl delete` cleanup step in CI |
| storageState lacks in-memory token | Expected: refresh cookie triggers transparent token refresh on first API call |

---

## Review Feedback Applied

Changes from plan review by DHH, Kieran, and Simplicity reviewers:

1. **15 wizard spec files → 1 data-driven `wizard-flows.spec.ts`** (DHH, Simplicity)
2. **7 browse spec files → 1 data-driven `resource-browse.spec.ts`** (DHH, Simplicity)
3. **8 POM class files → 1 `helpers.ts` with utility functions** (all three)
4. **5 phases → 2 phases** (DHH, Simplicity)
5. **3 auth spec files → 1 `auth.spec.ts`** (Simplicity)
6. **Removed `global-setup.ts` / `global-teardown.ts`** — CI handles cluster lifecycle (all three)
7. **Added PostgreSQL service container to CI** (Kieran — critical)
8. **Added `deno install` step to CI** (Kieran — critical)
9. **Added `tsconfig.json` content** (Kieran)
10. **Fixed `__dirname` → `import.meta.url` for ESM** (Kieran)
11. **Reduced timeouts: 90s→30s per test, 20s→10s assertions** (DHH)
12. **Added `e2e=true` label + kubectl cleanup step for orphaned resources** (Kieran)
13. **Used `page.emulateMedia({ reducedMotion })` instead of DOM style injection** (Kieran)
14. **Documented storageState/refresh-cookie interaction** (Kieran — critical)
15. **Removed dark-mode.spec.ts — inlined in navigation.spec.ts** (Simplicity)

---

## References

### Internal

- Frontend auth flow: `frontend/lib/auth.ts`, `frontend/lib/api.ts`
- WebSocket client: `frontend/lib/ws.ts`
- BFF proxy: `frontend/routes/api/[...path].ts`, `frontend/routes/ws/[...path].ts`
- Rate limiter: `backend/internal/server/middleware/ratelimit.go` (5 req/min per IP)
- Nav sections: `frontend/lib/constants.ts:NAV_SECTIONS`

### External

- [Playwright docs: Authentication](https://playwright.dev/docs/auth)
- [Playwright docs: WebSocket testing](https://playwright.dev/docs/api/class-websocketroute)
- [Playwright docs: CI configuration](https://playwright.dev/docs/ci)
- [helm/kind-action](https://github.com/helm/kind-action)
- [kind: Quick Start](https://kind.sigs.k8s.io/docs/user/quick-start/)
