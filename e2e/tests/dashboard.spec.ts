import type { Page } from "@playwright/test";
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
  const WIDGET_IDS = [
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
  ];
  const TRENDS = "**/api/v1/cluster/dashboard-trends*";

  const widget = (page: Page, id: string) =>
    page.locator(`[data-widget-id="${id}"]`);

  test("every default widget renders through the registry", async ({
    page,
  }) => {
    await page.goto("/");

    await expect(page.getByTestId("widget-host")).toHaveCount(10);

    // The ids are the contract a stored layout references. A rename here is a
    // breaking change to every saved layout, so pin them.
    for (const id of WIDGET_IDS) {
      await expect(widget(page, id)).toHaveCount(1);
    }

    // The hosts exist before any data does, so counting them alone passes on
    // a page of skeletons. Every widget must actually get past loading.
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await expect(page.getByTestId("widget-stale")).toHaveCount(0);
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
      widget(page, "recent-events").getByTestId("widget-error"),
    ).toBeVisible();
    // ...and a widget on another source still renders. The pre-registry page
    // had one all-or-nothing loading gate, so this is the behaviour that is new.
    await expect(
      widget(page, "cluster-health").getByText("Nodes ready"),
    ).toBeVisible();
    await expect(page.getByTestId("widget-error")).toHaveCount(1);
  });

  test("an optional source that never loaded claims no stale data", async ({
    page,
  }) => {
    // Trends is optional for the CPU tile and the utilization chart, required
    // for the network tile. The backend rejects trends outright for a remote
    // cluster, so this is every remote-cluster page load.
    await page.route(TRENDS, (route) => route.abort("failed"));
    await page.goto("/");

    await expect(
      widget(page, "network-tile").getByTestId("widget-error"),
    ).toBeVisible();
    for (const id of ["cpu-tile", "resource-utilization"]) {
      await expect(widget(page, id)).toHaveAttribute(
        "data-widget-state",
        "ready",
      );
    }
    // Nothing was ever loaded, so "showing last known data" would be false.
    await expect(page.getByTestId("widget-stale")).toHaveCount(0);
  });

  test("a failed refresh keeps the last data and says so", async ({ page }) => {
    await page.clock.install();
    await page.goto("/");
    const cpu = widget(page, "cpu-tile");
    await expect(cpu).toHaveAttribute("data-widget-state", "ready");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    // The next refresh tick fails the trends request after it has loaded once.
    await page.route(TRENDS, (route) => route.abort("failed"));
    const failed = page.waitForEvent("requestfailed", (r) =>
      r.url().includes("/api/v1/cluster/dashboard-trends"),
    );
    await page.clock.runFor(61_000);
    await failed;

    await expect(cpu.getByTestId("widget-stale")).toBeVisible();
    await expect(cpu).toHaveAttribute("data-widget-state", "ready");
    await expect(
      widget(page, "network-tile").getByTestId("widget-stale"),
    ).toBeVisible();
  });

  test("the network tile labels its data with the range it was fetched for", async ({
    page,
  }) => {
    await page.goto("/");
    const tile = widget(page, "network-tile");
    await expect(tile.getByText("p95 · 1h")).toBeVisible();

    const trends = page.waitForResponse((r) =>
      r.url().includes("/api/v1/cluster/dashboard-trends?range=6h"),
    );
    await page.getByRole("button", { name: "6h", exact: true }).click();
    await trends;

    await expect(tile.getByText("p95 · 6h")).toBeVisible();
  });

  test("a later tab wins over a slower earlier one", async ({ page }) => {
    await page.goto("/");
    const tile = widget(page, "network-tile");
    await expect(tile.getByText("p95 · 1h")).toBeVisible();

    // Hold the 6h response until 24h has landed, so it arrives out of order.
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let handled!: () => void;
    const sixHourHandled = new Promise<void>((resolve) => {
      handled = resolve;
    });
    await page.route("**/api/v1/cluster/dashboard-trends?range=6h*", async (route) => {
      await gate;
      // The page has usually cancelled this request by now; either way, the
      // response must not reach the tile.
      await route.continue().catch(() => {});
      handled();
    });

    const sixHourRequested = page.waitForRequest((r) =>
      r.url().includes("dashboard-trends?range=6h"),
    );
    await page.getByRole("button", { name: "6h", exact: true }).click();
    await sixHourRequested;

    const dayResponse = page.waitForResponse((r) =>
      r.url().includes("dashboard-trends?range=24h"),
    );
    await page.getByRole("button", { name: "24h", exact: true }).click();
    await dayResponse;
    await expect(tile.getByText("p95 · 24h")).toBeVisible();

    release();
    await sixHourHandled;
    await expect(tile.getByText("p95 · 24h")).toBeVisible();
    await expect(tile.getByText("p95 · 6h")).toHaveCount(0);
  });
});
