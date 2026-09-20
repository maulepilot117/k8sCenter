import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";
import { stubLayoutStore } from "./dashboard-layout-stub.ts";

// The catalog palette: what it offers, how it is driven, and what reaches the
// layout when an entry is chosen. Where a chosen widget lands is unit tested
// in frontend/lib/dashboard/placement_test.ts; these prove the dialog routes
// the user's choice through it and that the result is on screen and saveable.
//
// The stored layout below is deliberately short. The shipped default carries
// every widget in the catalog, so a dashboard that starts from it has nothing
// left to add -- which is worth one test, and useless for the other six.

/** The three widgets the tests start from, and the seven they can add. */
const PARTIAL_LAYOUT: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [
    { instanceId: "p-cluster-health", id: "cluster-health", x: 0, y: 0, w: 6, h: 6 },
    { instanceId: "p-cpu-tile", id: "cpu-tile", x: 6, y: 0, w: 3, h: 3 },
    { instanceId: "p-memory-tile", id: "memory-tile", x: 9, y: 0, w: 3, h: 3 },
  ],
};

/** Every widget registered for the overview scope. */
const CATALOG_SIZE = 10;

const palette = (page: Page) => page.getByTestId("widget-palette");
const option = (page: Page, widgetId: string) =>
  page.getByTestId(`widget-option-${widgetId}`);

/**
 * Loads the dashboard over `stored`, enters edit mode and opens the palette.
 *
 * Returns the writes the stubbed store saw, so a test can assert what a save
 * actually sent rather than only what the grid shows.
 */
async function openPalette(
  page: Page,
  stored: DashboardLayoutConfig | null = PARTIAL_LAYOUT,
) {
  const writes = await stubLayoutStore(
    page,
    stored === null ? undefined : { revision: 4, config: stored },
  );
  await page.goto("/");
  await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(
    stored === null ? CATALOG_SIZE : stored.items.length,
  );
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-mode",
    "wide",
  );

  await page.getByTestId("edit-layout").click();
  await page.getByTestId("add-widget").click();
  await expect(palette(page)).toBeVisible();
  return writes;
}

/** The options the palette is currently showing, in reading order. */
function visibleOptions(page: Page) {
  return palette(page).getByRole("option");
}

test.describe("dashboard widget palette", () => {
  test("offers every widget in the scope, grouped by family", async ({
    page,
  }) => {
    await openPalette(page);

    await expect(visibleOptions(page)).toHaveCount(CATALOG_SIZE);
    // The families the shipped catalog actually spans. Grouping is the whole
    // reason a flat list was not enough.
    for (const family of ["Cluster", "Workloads", "Reliability", "Networking"]) {
      await expect(
        palette(page).getByRole("group", { name: family }),
      ).toBeVisible();
    }
  });

  test("a widget already on the dashboard is shown with the reason, and cannot be added", async ({
    page,
  }) => {
    await openPalette(page);
    const placed = option(page, "cluster-health");

    await expect(placed).toHaveAttribute("aria-disabled", "true");
    await expect(placed).toContainText("Already on this dashboard");

    // Forced, because `aria-disabled` is exactly what Playwright's
    // actionability check would otherwise wait out -- and waiting out the
    // attribute proves nothing about the handler behind it. The click has to
    // reach `onClick` for the refusal to be the thing under test.
    await placed.click({ force: true });
    // Still open, nothing added: a refusal the user can see beats a dialog
    // that closes and quietly does nothing.
    await expect(palette(page)).toBeVisible();
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length,
    );
  });

  test("every entry is disabled once the dashboard holds the whole catalog", async ({
    page,
  }) => {
    // The shipped default is exactly this case, and it is the one a first-time
    // user meets. Nothing is addable and the palette says so on every row
    // rather than opening empty.
    await openPalette(page, null);

    await expect(visibleOptions(page)).toHaveCount(CATALOG_SIZE);
    await expect(
      palette(page).getByRole("option", { selected: true }),
    ).toHaveCount(0);
    for (const o of await visibleOptions(page).all()) {
      await expect(o).toHaveAttribute("aria-disabled", "true");
    }
  });

  test("typing filters the catalog", async ({ page }) => {
    await openPalette(page);

    await page.getByTestId("widget-search").fill("nodes");
    await expect(visibleOptions(page)).toHaveCount(1);
    await expect(option(page, "nodes")).toBeVisible();

    await page.getByTestId("widget-search").fill("zzzz");
    await expect(visibleOptions(page)).toHaveCount(0);
    await expect(palette(page)).toContainText("No widget matches");
  });

  test("Escape closes it and puts focus back on the button that opened it", async ({
    page,
  }) => {
    await openPalette(page);

    await page.keyboard.press("Escape");
    await expect(palette(page)).toBeHidden();
    // Focus on the document is the failure this asserts against: a dialog that
    // drops the keyboard leaves it at the top of the page.
    await expect(page.getByTestId("add-widget")).toBeFocused();
    // And the session it was opened from is untouched.
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
  });

  test("a click on the scrim closes it", async ({ page }) => {
    await openPalette(page);

    // The top-left corner is scrim: the dialog is centred and starts below it.
    await page.mouse.click(5, 5);
    await expect(palette(page)).toBeHidden();
  });

  test("adds the chosen widget, in view, and arms Save", async ({ page }) => {
    const writes = await openPalette(page);

    await option(page, "recent-events").click();
    await expect(palette(page)).toBeHidden();

    const added = page.locator('[data-instance-id^="recent-events-"]');
    await expect(added).toHaveCount(1);
    // The point of placing rather than appending: a widget added below the
    // fold reads as a button that did nothing.
    await expect(added).toBeInViewport();
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length + 1,
    );

    // The insertion has to reach the edit session, not just the grid: the grid
    // owns a private working copy and a Save armed by every path except Add
    // would be worse than no Save at all.
    const save = page.getByTestId("save-layout");
    await expect(save).toBeEnabled();
    await save.click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    expect(writes).toHaveLength(1);
    const items = writes[0].config.items;
    expect(items).toHaveLength(PARTIAL_LAYOUT.items.length + 1);
    const written = items.find((i) => i.id === "recent-events");
    expect(written, "the added widget reached the server").toBeDefined();
    expect(written?.instanceId).not.toBe("");
    expect(written?.w).toBeGreaterThan(0);
    expect(written?.x ?? 0).toBeLessThan(12);
    // The save claims the revision the layout was loaded at, which is the
    // stub's, not a number the client invented.
    expect(writes[0].revision).toBe(4);
  });

  test("the keyboard adds a widget without touching the mouse", async ({
    page,
  }) => {
    await openPalette(page);

    // The opening selection skips the three already placed, so Enter does
    // something rather than being ignored on a highlighted row.
    const first = palette(page).getByRole("option", { selected: true });
    await expect(first).toHaveCount(1);
    const selectedName = await first.textContent();

    await page.keyboard.press("Enter");
    await expect(palette(page)).toBeHidden();
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length + 1,
    );
    // The widget that was highlighted is the one that landed, and the keyboard
    // is now on it rather than back at the top of the page.
    const focused = page.locator("[data-instance-id]:focus");
    await expect(focused).toHaveCount(1);
    await expect(focused).toHaveAttribute(
      "aria-label",
      new RegExp(`^${selectedName?.trim()},`),
    );
  });

  test("arrow keys move the selection over the widgets already placed", async ({
    page,
  }) => {
    await openPalette(page);

    const names: string[] = [];
    for (let i = 0; i < 3; i++) {
      const selected = palette(page).getByRole("option", { selected: true });
      names.push(((await selected.textContent()) ?? "").trim());
      await page.keyboard.press("ArrowDown");
    }

    expect(new Set(names).size, "each press moved the selection").toBe(3);
    // None of them is a widget the dashboard already holds.
    expect(names.some((n) => n.includes("Already on this dashboard"))).toBe(
      false,
    );
  });

  test("the palette does not outlive the edit session", async ({ page }) => {
    await openPalette(page);

    await page.keyboard.press("Escape");
    await page.getByTestId("cancel-edit").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    // Re-entering edit mode must not reopen a dialog nobody asked for.
    await page.getByTestId("edit-layout").click();
    await expect(palette(page)).toBeHidden();
  });
});
