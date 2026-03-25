# Step 24 Phase 2: Complete E2E Test Coverage (Post-Review)

## Overview

Implement remaining Playwright test specs. Phase 1 infrastructure is done (5 smoke tests passing in CI). This plan adds 7 spec files with ~40-50 tests using data-driven patterns.

## Review Feedback Applied

1. API helpers use BFF proxy path (relative to baseURL) — `page.request` carries cookies through frontend origin
2. Monaco — block CDN route, use textarea fallback for YAML tests
3. Wizard stepping — signal-based (wait for Apply/review visible), not count-based
4. WebSocket — wait for table state after namespace switch before creating resource
5. Cut dark mode persistence test (unit test concern)
6. Trim wizards from 15 to 6 representative types
7. Trim monitoring to 1 test (status page only)
8. YAML apply: validate only, defer Apply until Monaco approach proven
9. Fix `hasRows` — namespace-scoped resources depend on namespace selection
10. Add explicit `WizardConfig` type

---

## Files to Create (7 spec files)

### 1. `e2e/tests/navigation.spec.ts`

Iterate all nav hrefs, assert no error page. One loop, ~30s total.

```typescript
const NAV_ITEMS = [
  "/", "/cluster/nodes", "/cluster/namespaces", "/cluster/events",
  "/cluster/pvs", "/cluster/storageclasses",
  "/workloads/deployments", "/workloads/statefulsets", "/workloads/daemonsets",
  "/workloads/pods", "/workloads/jobs", "/workloads/cronjobs", "/workloads/replicasets",
  "/networking/services", "/networking/ingresses", "/networking/networkpolicies",
  "/networking/cilium-policies", "/networking/flows", "/networking/cni",
  "/networking/endpoints", "/networking/endpointslices",
  "/storage/overview", "/storage/pvcs", "/storage/snapshots",
  "/config/configmaps", "/config/secrets", "/config/serviceaccounts",
  "/config/resourcequotas", "/config/limitranges",
  "/scaling/hpas", "/scaling/pdbs",
  "/rbac/roles", "/rbac/clusterroles", "/rbac/rolebindings", "/rbac/clusterrolebindings",
  "/monitoring", "/monitoring/dashboards", "/monitoring/prometheus",
  "/alerting", "/alerting/rules", "/alerting/settings",
  "/tools/yaml-apply", "/tools/storageclass-wizard",
  "/admin/validatingwebhooks", "/admin/mutatingwebhooks",
  "/settings/general", "/settings/clusters", "/settings/users",
  "/settings/auth", "/settings/audit",
];

for (const href of NAV_ITEMS) {
  test(`${href} loads without error`, async ({ page }) => {
    await page.goto(href);
    // Assert no error page rendered
    await expect(page.getByText(/page not found|404|error/i)).not.toBeVisible();
  });
}
```

Plus error page test: navigate to `/nonexistent-page`, assert error renders.

### 2. `e2e/tests/resource-browse.spec.ts`

Data-driven over all resource types. Resources in kube-system (pods, deployments, daemonsets, configmaps, etc.) are only visible when namespace is set to kube-system or "all". Cluster-scoped resources (nodes, namespaces, clusterroles) are always visible.

```typescript
const RESOURCES = [
  // Cluster-scoped — always have rows
  { kind: "nodes", path: "/cluster/nodes", hasRows: true },
  { kind: "namespaces", path: "/cluster/namespaces", hasRows: true },
  { kind: "clusterroles", path: "/rbac/clusterroles", hasRows: true },
  { kind: "clusterrolebindings", path: "/rbac/clusterrolebindings", hasRows: true },
  { kind: "storageclasses", path: "/cluster/storageclasses", hasRows: true },
  // Namespace-scoped — have rows in kube-system (table shows "all namespaces" by default)
  { kind: "pods", path: "/workloads/pods", hasRows: true },
  { kind: "deployments", path: "/workloads/deployments", hasRows: true },
  { kind: "daemonsets", path: "/workloads/daemonsets", hasRows: true },
  { kind: "replicasets", path: "/workloads/replicasets", hasRows: true },
  { kind: "services", path: "/networking/services", hasRows: true },
  { kind: "endpoints", path: "/networking/endpoints", hasRows: true },
  { kind: "configmaps", path: "/config/configmaps", hasRows: true },
  { kind: "secrets", path: "/config/secrets", hasRows: true },
  { kind: "serviceaccounts", path: "/config/serviceaccounts", hasRows: true },
  // Empty in vanilla kind
  { kind: "statefulsets", path: "/workloads/statefulsets", hasRows: false },
  { kind: "jobs", path: "/workloads/jobs", hasRows: false },
  { kind: "cronjobs", path: "/workloads/cronjobs", hasRows: false },
  { kind: "ingresses", path: "/networking/ingresses", hasRows: false },
  { kind: "networkpolicies", path: "/networking/networkpolicies", hasRows: false },
  { kind: "pvcs", path: "/storage/pvcs", hasRows: false },
  { kind: "pvs", path: "/cluster/pvs", hasRows: false },
  { kind: "hpas", path: "/scaling/hpas", hasRows: false },
  { kind: "pdbs", path: "/scaling/pdbs", hasRows: false },
  { kind: "resourcequotas", path: "/config/resourcequotas", hasRows: false },
  { kind: "limitranges", path: "/config/limitranges", hasRows: false },
];
```

Plus namespace filter test and search test on pods.

### 3. `e2e/tests/resource-detail.spec.ts`

Test on coredns deployment in kube-system (always exists in kind):
- Navigate to deployments, select kube-system, click coredns row
- Overview tab: content renders
- YAML tab: `.cm-editor` visible
- Events tab: tab activates
- Pods tab: pod list visible (deployments have this tab)

Skip Metrics tab — always shows "not available" in kind, adds no signal.

### 4. `e2e/tests/wizard-flows.spec.ts`

6 representative wizard types covering all pattern variants:

```typescript
interface WizardConfig {
  kind: string;
  createPath: string;
  apiKind: string;
  namespace: string | null; // null for cluster-scoped
  fields: { label: string; value: string; generated?: boolean }[];
  submitButton?: string; // defaults to "Apply"
}

const WIZARDS: WizardConfig[] = [
  // Multi-step (3 steps before review)
  {
    kind: "deployment",
    createPath: "/workloads/deployments/new",
    apiKind: "deployments",
    namespace: "default",
    fields: [
      { label: "Name", value: "", generated: true },
      { label: "Container Image", value: "nginx:alpine" },
    ],
  },
  // Single-step
  {
    kind: "configmap",
    createPath: "/config/configmaps/new",
    apiKind: "configmaps",
    namespace: "default",
    fields: [{ label: "Name", value: "", generated: true }],
  },
  // Multi-step with container form
  {
    kind: "job",
    createPath: "/workloads/jobs/new",
    apiKind: "jobs",
    namespace: "default",
    fields: [
      { label: "Job Name", value: "", generated: true },
      { label: "Container Image", value: "busybox:1.37" },
    ],
  },
  // Cluster-scoped, different submit button
  {
    kind: "namespace",
    createPath: "/cluster/namespaces/new",
    apiKind: "namespaces",
    namespace: null,
    fields: [{ label: "Name", value: "", generated: true }],
    submitButton: "Create",
  },
  // Multi-step with rule builder
  {
    kind: "networkpolicy",
    createPath: "/networking/networkpolicies/new",
    apiKind: "networkpolicies",
    namespace: "default",
    fields: [{ label: "Name", value: "", generated: true }],
  },
  // Multi-step with role reference (uses existing cluster-admin)
  {
    kind: "clusterrolebinding",
    createPath: "/rbac/clusterrolebindings/new",
    apiKind: "clusterrolebindings",
    namespace: null,
    fields: [{ label: "Name", value: "", generated: true }],
  },
];
```

**Wizard test pattern (signal-based stepping):**
1. Generate name via `e2eName(kind)`
2. `page.goto(createPath)`
3. Fill fields
4. Click "Next" repeatedly, waiting for either the next step heading to change OR the Apply/Create button to become visible (signal-based, not count-based)
5. Assert review step (Apply/Create button visible)
6. Click Apply/Create
7. Assert success text
8. Cleanup via API

### 5. `e2e/tests/yaml-apply.spec.ts`

**Monaco strategy: block CDN → use textarea fallback**

```typescript
test("validates YAML", async ({ page }) => {
  // Block Monaco CDN so textarea fallback renders
  await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
  await page.goto("/tools/yaml-apply");
  await expect(page.getByText("YAML Apply")).toBeVisible();

  // Fill the fallback textarea
  const yaml = `apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: e2e-validate-test\n  namespace: default\ndata:\n  key: value`;
  await page.locator("textarea").fill(yaml);

  // Validate should be enabled now (content != placeholder)
  await page.getByRole("button", { name: "Validate" }).click();
  // Assert validation completes (no error, or shows valid result)
});
```

Apply test deferred until textarea approach is proven stable.

### 6. `e2e/tests/websocket.spec.ts`

Create a ConfigMap via BFF proxy (using `page.request` which goes through frontend origin and carries cookies):

```typescript
test("live update shows new resource", async ({ page, request }) => {
  // Navigate to configmaps in default namespace
  await page.goto("/config/configmaps");
  // Switch to default namespace and wait for table to stabilize
  await waitForTableLoaded(page);

  const name = e2eName("ws");
  // Create via BFF proxy — request goes through frontend origin, carries cookies
  await request.post(`/api/v1/resources/configmaps/default`, {
    headers: { "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json" },
    data: { apiVersion: "v1", kind: "ConfigMap", metadata: { name, namespace: "default", labels: { e2e: "true" } }, data: { test: "value" } },
  });

  // Assert row appears via WebSocket within 10s (no page reload)
  await expect(page.getByText(name)).toBeVisible({ timeout: 10_000 });

  // Cleanup
  await request.delete(`/api/v1/resources/configmaps/default/${name}`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });
});
```

**Note:** `page.request` in Playwright uses the page's origin (localhost:5173), so requests go through the Fresh BFF proxy which forwards to the backend with proper auth headers. This avoids the Bearer token issue.

### 7. `e2e/tests/settings.spec.ts`

- `/settings/general` — page loads
- `/settings/users` — admin appears, "you" badge visible, Delete disabled for self
- `/settings/audit` — audit table renders

### Monitoring (inline in settings or navigation)

Single test: `/monitoring` status page loads. No separate spec file — the navigation spec already covers all monitoring routes.

---

## Helpers to Add

```typescript
/** Create a ConfigMap via BFF proxy */
export async function createConfigMapViaAPI(
  request: APIRequestContext,
  namespace: string,
  name: string,
) {
  const res = await request.post(`/api/v1/resources/configmaps/${namespace}`, {
    headers: { "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json" },
    data: {
      apiVersion: "v1",
      kind: "ConfigMap",
      metadata: { name, namespace, labels: { e2e: "true" } },
      data: { test: "value" },
    },
  });
  expect(res.ok()).toBeTruthy();
  return res;
}
```

---

## Implementation Order

1. `navigation.spec.ts` — quick win, catches route bugs
2. `resource-browse.spec.ts` — validates all list pages
3. `resource-detail.spec.ts` — tests tab switching
4. `settings.spec.ts` — simple page-load + user table assertions
5. `wizard-flows.spec.ts` — most complex, signal-based stepping
6. `yaml-apply.spec.ts` — Monaco textarea fallback
7. `websocket.spec.ts` — API-driven live update

## Acceptance Criteria

- [ ] All 7 spec files pass locally against kind cluster
- [ ] CI E2E workflow passes
- [ ] Navigation covers all sidebar hrefs
- [ ] Resource browse covers all resource types
- [ ] 6 wizard types create resources successfully
- [ ] WebSocket live update verified
- [ ] Total CI time < 10 minutes
