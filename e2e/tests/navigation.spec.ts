import { test, expect } from "../fixtures/base.ts";

const NAV_ITEMS = [
  "/",
  "/cluster/nodes",
  "/cluster/namespaces",
  "/cluster/events",
  "/cluster/pvs",
  "/cluster/storageclasses",
  "/workloads/deployments",
  "/workloads/statefulsets",
  "/workloads/daemonsets",
  "/workloads/pods",
  "/workloads/jobs",
  "/workloads/cronjobs",
  "/workloads/replicasets",
  "/networking/services",
  "/networking/ingresses",
  "/networking/networkpolicies",
  "/networking/cilium-policies",
  "/networking/flows",
  "/networking/cni",
  "/networking/endpoints",
  "/networking/endpointslices",
  "/storage/overview",
  "/storage/pvcs",
  "/storage/snapshots",
  "/config/configmaps",
  "/config/secrets",
  "/config/serviceaccounts",
  "/config/resourcequotas",
  "/config/limitranges",
  "/scaling/hpas",
  "/scaling/pdbs",
  "/rbac/roles",
  "/rbac/clusterroles",
  "/rbac/rolebindings",
  "/rbac/clusterrolebindings",
  "/monitoring",
  "/monitoring/dashboards",
  "/monitoring/prometheus",
  "/alerting",
  "/alerting/rules",
  "/alerting/settings",
  "/tools/yaml-apply",
  "/tools/storageclass-wizard",
  "/admin/validatingwebhooks",
  "/admin/mutatingwebhooks",
  "/settings/general",
  "/settings/clusters",
  "/settings/users",
  "/settings/auth",
  "/settings/audit",
];

test.describe("Navigation @smoke", () => {
  for (const href of NAV_ITEMS) {
    test(`${href} loads without error`, async ({ page }) => {
      const response = await page.goto(href);
      // Page should not return a server error
      expect(response?.status()).toBeLessThan(500);
      // Should not render the error page
      await expect(page.getByText("Page not found")).not.toBeVisible();
    });
  }

  test("unknown route shows error page", async ({ page }) => {
    await page.goto("/nonexistent-route-12345");
    await expect(page.getByText(/not found|404/i)).toBeVisible();
  });
});
