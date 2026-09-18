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

  test("a narrow grid collapses to one column in reading order", async ({
    page,
  }) => {
    // Well under the 900px grid-width breakpoint once the sidebar is counted.
    await page.setViewportSize({ width: 700, height: 900 });
    await page.goto("/");

    const grid = page.getByTestId("dashboard-grid");
    await expect(grid).toHaveAttribute("data-grid-mode", "narrow");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    // DOM order is the keyboard and screen-reader order: row, then column.
    const order = await page
      .getByTestId("grid-item")
      .evaluateAll((els) => els.map((el) => el.getAttribute("data-instance-id")));
    expect(order).toEqual([
      "d-cluster-health",
      "d-cpu-tile",
      "d-memory-tile",
      "d-pods-tile",
      "d-network-tile",
      "d-resource-utilization",
      "d-pod-status",
      "d-nodes",
      "d-recent-events",
      "d-active-alerts",
    ]);

    // One column means one cell per row, each spanning the grid: matching
    // left edges alone would also pass if two full-width cells shared a row
    // and overlapped, so assert the stack directly.
    const cells = await page.getByTestId("grid-item").evaluateAll((els) =>
      els.map((el) => {
        const r = el.getBoundingClientRect();
        const parent = (el.parentElement as HTMLElement).getBoundingClientRect();
        return {
          left: Math.round(r.left),
          top: Math.round(r.top),
          bottom: Math.round(r.bottom),
          width: Math.round(r.width),
          gridWidth: Math.round(parent.width),
        };
      }),
    );

    expect(new Set(cells.map((c) => c.left)).size).toBe(1);
    for (const [i, cell] of cells.entries()) {
      // Full grid width, allowing a pixel of rounding.
      expect(Math.abs(cell.width - cell.gridWidth), `cell ${i} width`).toBeLessThanOrEqual(1);
      if (i > 0) {
        const previous = cells[i - 1];
        expect(cell.top, `cell ${i} starts below cell ${i - 1}`).toBeGreaterThanOrEqual(previous.bottom);
      }
    }
  });

  test("a card fills its grid cell and never scrolls sideways", async ({
    page,
  }) => {
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "wide",
    );

    // The cell-fill contract: the card takes the whole cell (so a short widget
    // does not leave a ragged hole) and clips instead of scrolling sideways (a
    // horizontal scrollbar inside a card is how the too-narrow tile showed up).
    const cells = await page.getByTestId("grid-item").evaluateAll((els) =>
      els.map((el) => ({
        id: el.getAttribute("data-instance-id"),
        height: Math.round(el.getBoundingClientRect().height),
        cardHeight: Math.round(
          (el.firstElementChild as HTMLElement).getBoundingClientRect().height,
        ),
        overflowX: el.scrollWidth - el.clientWidth,
      })),
    );

    expect(cells).toHaveLength(10);
    for (const cell of cells) {
      expect(Math.abs(cell.cardHeight - cell.height), `${cell.id} fills its cell`).toBeLessThanOrEqual(1);
      expect(cell.overflowX, `${cell.id} overflows horizontally`).toBeLessThanOrEqual(1);
    }
  });

  test("the grid mode follows its own width across the breakpoint", async ({
    page,
  }) => {
    const grid = page.getByTestId("dashboard-grid");
    // The threshold is the grid's own width, not the viewport's, so the test
    // derives the expected mode from the measurement rather than from a
    // viewport size that depends on the sidebar. A resize reaches the grid
    // through a ResizeObserver, which delivers on its own schedule, so read
    // both halves together until they agree rather than once and hope.
    const observed = async () => {
      let seen = { mode: "", width: 0 };
      await expect
        .poll(
          async () => {
            seen = await grid.evaluate((el) => ({
              mode: el.getAttribute("data-grid-mode") ?? "",
              width: el.clientWidth,
            }));
            return seen.mode === (seen.width < 900 ? "narrow" : "wide");
          },
          { message: `grid mode never matched its width: ${JSON.stringify(seen)}` },
        )
        .toBe(true);
      return seen;
    };

    // 1280 leaves the grid just wide enough for twelve columns; 1240 does not.
    // Both sit within ~40px of the 900px threshold, which is where a mode that
    // fed back into its own width would stick or flicker.
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    const wide = await observed();

    await page.setViewportSize({ width: 1240, height: 900 });
    await observed();

    // Back to the starting width: the same width must give the same mode, or
    // the collapse is one-way.
    await page.setViewportSize({ width: 1280, height: 900 });
    const again = await observed();
    expect(again.width).toBe(wide.width);
    expect(again.mode).toBe(wide.mode);
  });

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
