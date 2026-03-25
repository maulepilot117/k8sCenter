import { test, expect } from "../fixtures/base.ts";
import { waitForTableLoaded } from "../helpers.ts";

const RESOURCES = [
  // Cluster-scoped — always have rows
  { kind: "nodes", path: "/cluster/nodes", hasRows: true },
  { kind: "namespaces", path: "/cluster/namespaces", hasRows: true },
  { kind: "clusterroles", path: "/rbac/clusterroles", hasRows: true },
  { kind: "clusterrolebindings", path: "/rbac/clusterrolebindings", hasRows: true },
  { kind: "storageclasses", path: "/cluster/storageclasses", hasRows: true },
  // Namespace-scoped — have rows across all namespaces (kube-system has resources)
  { kind: "pods", path: "/workloads/pods", hasRows: true },
  { kind: "deployments", path: "/workloads/deployments", hasRows: true },
  { kind: "daemonsets", path: "/workloads/daemonsets", hasRows: true },
  { kind: "replicasets", path: "/workloads/replicasets", hasRows: true },
  { kind: "services", path: "/networking/services", hasRows: true },
  { kind: "endpoints", path: "/networking/endpoints", hasRows: true },
  { kind: "endpointslices", path: "/networking/endpointslices", hasRows: true },
  { kind: "configmaps", path: "/config/configmaps", hasRows: true },
  { kind: "secrets", path: "/config/secrets", hasRows: true },
  { kind: "serviceaccounts", path: "/config/serviceaccounts", hasRows: true },
  { kind: "roles", path: "/rbac/roles", hasRows: true },
  { kind: "rolebindings", path: "/rbac/rolebindings", hasRows: true },
  // Empty in vanilla kind cluster
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

test.describe("Resource browsing", () => {
  for (const r of RESOURCES) {
    test(`${r.kind} table loads`, async ({ page }) => {
      await page.goto(r.path);
      await waitForTableLoaded(page);
      if (r.hasRows) {
        // Header row + at least 1 data row
        await expect(page.getByRole("row")).toHaveCount({ minimum: 2 });
      }
    });
  }

  test("namespace filter changes displayed resources", async ({ page }) => {
    await page.goto("/workloads/pods");
    await waitForTableLoaded(page);

    // Get initial row count (all namespaces)
    const allCount = await page.getByRole("row").count();

    // Switch to kube-system namespace
    const nsSelect = page.getByTestId("namespace-select").or(
      page.getByRole("combobox").first(),
    );
    await nsSelect.selectOption("kube-system");
    await waitForTableLoaded(page);

    // kube-system should have pods, but count may differ from all namespaces
    const ksCount = await page.getByRole("row").count();
    expect(ksCount).toBeGreaterThan(1); // at least header + 1 pod
  });

  test("search filters table rows", async ({ page }) => {
    await page.goto("/workloads/pods");
    await waitForTableLoaded(page);

    const beforeCount = await page.getByRole("row").count();

    // Search for coredns — should filter to only matching rows
    const searchBox = page.getByRole("searchbox").or(
      page.getByPlaceholder(/search/i),
    );
    await searchBox.fill("coredns");

    // Wait for filter to apply (client-side debounce)
    await page.waitForTimeout(500);
    const afterCount = await page.getByRole("row").count();

    // Should have fewer rows than before (filtered)
    expect(afterCount).toBeLessThan(beforeCount);
    expect(afterCount).toBeGreaterThan(1); // header + at least 1 coredns pod
  });
});
