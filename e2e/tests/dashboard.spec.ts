import { test, expect } from "../fixtures/base.ts";

test.describe("Dashboard @smoke", () => {
  test("loads with real cluster data", async ({ page }) => {
    await page.goto("/");

    // Dashboard should show cluster overview heading
    await expect(
      page.getByRole("heading", { name: /cluster overview/i }),
    ).toBeVisible();

    // Stat cards should display — check for the Cluster Health card
    await expect(page.getByText("Cluster Health")).toBeVisible();
  });
});

test.describe("Dashboard widget registry", () => {
  test("every default widget renders through the registry", async ({
    page,
  }) => {
    await page.goto("/");

    await expect(page.getByTestId("widget-host")).toHaveCount(10);

    // The ids are the contract a stored layout references. A rename here is a
    // breaking change to every saved layout, so pin them.
    for (const id of [
      "cluster-health",
      "cpu-tile",
      "memory-tile",
      "pods-tile",
      "network-tile",
      "resource-utilization",
      "pod-status",
      "nodes",
      "recent-events",
      "active-alerts",
    ]) {
      await expect(page.locator(`[data-widget-id="${id}"]`)).toHaveCount(1);
    }
  });

  test("a widget reports its own failure without blanking the page", async ({
    page,
  }) => {
    await page.route("**/api/v1/resources/events*", (route) =>
      route.abort("failed"),
    );
    await page.goto("/");

    // The failing widget says so...
    await expect(
      page
        .locator('[data-widget-id="recent-events"]')
        .getByTestId("widget-error"),
    ).toBeVisible();
    // ...and a widget on another source still renders. The pre-registry page
    // had one all-or-nothing loading gate, so this is the behaviour that is new.
    await expect(
      page.locator('[data-widget-id="cluster-health"]').getByText("Nodes ready"),
    ).toBeVisible();
    await expect(page.getByTestId("widget-error")).toHaveCount(1);
  });

  test("the network tile labels its data with the range it was fetched for", async ({
    page,
  }) => {
    await page.goto("/");
    const tile = page.locator('[data-widget-id="network-tile"]');
    await expect(tile.getByText("p95 · 1h")).toBeVisible();

    const trends = page.waitForResponse((r) =>
      r.url().includes("/api/v1/cluster/dashboard-trends?range=6h"),
    );
    await page.getByRole("button", { name: "6h", exact: true }).click();
    await trends;

    await expect(tile.getByText("p95 · 6h")).toBeVisible();
  });
});
