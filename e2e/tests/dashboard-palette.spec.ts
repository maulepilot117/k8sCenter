import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";
import { stubLayoutStore } from "./dashboard-layout-stub.ts";

// The catalog palette: what it offers, how it is driven, and what reaches the
// layout when an entry is chosen. Where a chosen widget lands is unit tested
// in frontend/lib/dashboard/placement_test.ts; these prove the dialog routes
// the user's choice through it and that the result is on screen and saveable.
//
// The stored layout below is deliberately short, so most of the catalog is
// addable and the six tests that need something to add have it.
//
// The shipped default is a CURATED subset of the catalog rather than all of
// it -- a starting dashboard that grew with every release would hand a new
// user a wall of cards -- so the two counts below are different numbers and
// are not interchangeable.

/** The three widgets the tests start from; the rest of the catalog is
 * addable on top of them. */
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

/** Every widget registered for the overview scope, parameterized included. */
const CATALOG_SIZE = 38;

/** What the shipped default layout places: the curated starting subset. */
const DEFAULT_LAYOUT_SIZE = 10;

/**
 * The widgets that take parameters. Their rows stay addable however full the
 * dashboard is of unparameterized copies, because choosing one opens a dialog
 * rather than placing a card.
 */
const PARAMETERIZED_IDS = [
  "diagnostics-summary",
  "vulnerability-severity",
  "mesh-golden-signals",
  "hubble-flows",
];

/**
 * Everything the shipped default does NOT place, and which is therefore still
 * addable on a dashboard that has never been edited.
 *
 * This is not derived from the registry on purpose: the point of the test
 * below is that the default is a deliberate subset, and a list computed from
 * the same source as the thing under test would agree with any subset at all,
 * including an accidental one.
 */
const NOT_ON_DEFAULT_IDS = [
  ...PARAMETERIZED_IDS,
  "workload-health",
  "pending-pods",
  "pod-restarts",
  "hpa-status",
  "pdb-risk",
  "top-consumers",
  "quota-pressure",
  "node-conditions",
  "storage-capacity",
  "policy-compliance",
  "policy-violations",
  "certs-expiring",
  "eso-health",
  "velero-backups",
  "snapshot-health",
  "gitops-app-health",
  "gitops-recent-syncs",
  "mtls-coverage",
  "gateway-routes",
  "cluster-status",
  "notifications-feed",
  "audit-activity",
  "saved-views",
  "pinned-resources",
];

// The literal above is the guard, but a unit that adds a widget and forgets
// this file would otherwise fail on an opaque count mismatch. This says what
// actually went wrong.
test("the catalog, the default and this file's own list agree", () => {
  expect(NOT_ON_DEFAULT_IDS.length + DEFAULT_LAYOUT_SIZE).toBe(CATALOG_SIZE);
});

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
    stored === null ? DEFAULT_LAYOUT_SIZE : stored.items.length,
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

/**
 * Same as `openPalette`, but starting from a grid narrow enough to render in
 * one column.
 *
 * Not a parameter on `openPalette` itself: that helper asserts wide mode on
 * the way in, which every other spec in this file relies on, and the
 * threshold is measured on the grid element's own width (DashboardGrid.tsx's
 * `NARROW_GRID_WIDTH`), not the viewport's -- so the viewport has to be set
 * before `goto`, which the wide-mode helper has no reason to do.
 */
async function openPaletteNarrow(page: Page) {
  await page.setViewportSize({ width: 700, height: 900 });
  const writes = await stubLayoutStore(page, {
    revision: 4,
    config: PARTIAL_LAYOUT,
  });
  await page.goto("/");
  await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(
    PARTIAL_LAYOUT.items.length,
  );
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-mode",
    "narrow",
  );

  await page.getByTestId("edit-layout").click();
  await page.getByTestId("add-widget").click();
  await expect(palette(page)).toBeVisible();
  return writes;
}

test.describe("dashboard widget palette", () => {
  test("offers every widget in the scope, grouped by family", async ({
    page,
  }) => {
    await openPalette(page);

    await expect(visibleOptions(page)).toHaveCount(CATALOG_SIZE);
    // The families the shipped catalog actually spans. Grouping is the whole
    // reason a flat list was not enough.
    for (const family of [
      "Cluster",
      "Workloads",
      "Reliability",
      "Security",
      "Delivery",
      "Data protection",
      "Networking",
      "Platform",
    ]) {
      await expect(
        palette(page).getByRole("group", { name: family }),
      ).toBeVisible();
    }
  });

  // The 200-row "no room on this dashboard" disabled state is not covered
  // here: reaching the cap through the UI takes a long resize sequence that
  // would make a spec slow and brittle, and `placement_test.ts` already unit
  // tests it directly.

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

    // The forced click used to land focus on the disabled row itself, which
    // killed the arrow keys and Enter (both are bound on the search input)
    // and left the Tab trap inert. `onMouseDown` preventDefault on the option
    // is the fix; this proves the keyboard still works after the click that
    // used to break it, not just that the click was refused.
    await expect(page.getByTestId("widget-search")).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(
      palette(page).getByRole("option", { selected: true }),
    ).toHaveCount(1);
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("close-palette")).toBeFocused();
  });

  test("exactly the widgets the shipped default omits remain addable", async ({
    page,
  }) => {
    // The shipped default is the dashboard a first-time user meets. The
    // widgets it DOES place say "already on this dashboard" rather than the
    // palette opening empty; the ones it deliberately leaves off stay
    // addable, which is what makes the default a starting point rather than
    // the whole product.
    await openPalette(page, null);

    await expect(visibleOptions(page)).toHaveCount(CATALOG_SIZE);

    // diagnostics-summary alone: the other parameterized widgets are gated on
    // a discovered feature, so whether they are addable depends on what the
    // cluster under test runs rather than on the behaviour being asserted.
    for (const id of ["diagnostics-summary"]) {
      const row = option(page, id);
      await expect(row).toHaveAttribute("aria-disabled", "false");
      // Said before the row is chosen. Choosing it opens a dialog rather than
      // placing a widget, and an Add that produced a dialog for no stated
      // reason reads as a bug.
      await expect(row.getByTestId("widget-option-needs-values")).toContainText(
        "namespace",
      );
    }

    const rows = await visibleOptions(page).all();
    expect(rows).toHaveLength(CATALOG_SIZE);
    // Two independent reasons disable a row now, so the assertion is about
    // WHICH reason rather than whether there is one. A widget the default
    // places must say it is already here. A widget the default omits must
    // never say that -- but it may still be disabled because this cluster
    // does not run its feature, which is a fact about the environment the
    // suite points at and not about the default being a deliberate subset.
    for (const o of rows) {
      const id = await o.getAttribute("data-testid");
      const omitted = NOT_ON_DEFAULT_IDS.some(
        (w) => id === `widget-option-${w}`,
      );
      const reason = (await o.getAttribute("title")) ?? "";
      if (omitted) {
        expect(reason).not.toContain("Already");
      } else {
        await expect(o).toHaveAttribute("aria-disabled", "true");
        expect(reason).toContain("Already");
      }
    }

    // The selection opens on the first row that can actually be added, which
    // is one of the omitted ones rather than nothing at all.
    await expect(
      palette(page).getByRole("option", { selected: true }),
    ).toHaveCount(1);
  });

  test("typing filters the catalog", async ({ page }) => {
    await openPalette(page);

    // "nodes" now matches node-conditions too, so the term has to be one only
    // one widget carries -- the point is that typing narrows the catalog, not
    // that any particular word is unique in it.
    await page.getByTestId("widget-search").fill("quota");
    await expect(visibleOptions(page)).toHaveCount(1);
    await expect(option(page, "quota-pressure")).toBeVisible();

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

  test("Tab cannot leave the dialog -- it wraps between the search input and Close", async ({
    page,
  }) => {
    await openPalette(page);

    // The only two tab stops inside the dialog: every option row carries
    // tabIndex={-1} on purpose (the arrows move the selection instead), so
    // the ring is exactly these two, both ways. In DOM order Close comes
    // BEFORE the search input (header markup, then the input below it), so
    // `stops` resolves to [close, input] -- `first` is Close, `last` is the
    // input. The previous version of this test started at the input and
    // pressed Shift+Tab (input -> close), then from close pressed Tab
    // (close -> input): both of those are the natural, adjacent-in-DOM-order
    // direction, which a browser does correctly with zero trap code. Neither
    // `handleDialogKeyDown` wrap branch was ever reached, so deleting the
    // whole wrap block left this test green. The pairing below is reversed so
    // each press actually asks the trap to do something the DOM would not.
    await expect(page.getByTestId("widget-search")).toBeFocused();

    // The input is the LAST stop. Tab from the last stop is the forward-wrap
    // branch (`!e.shiftKey && active === last`) -- without it, Tab from the
    // last focusable element would leave the dialog for whatever the page
    // puts next in the DOM, not loop back to the first stop.
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("close-palette")).toBeFocused();

    // Close is the FIRST stop. Shift+Tab from the first stop is the
    // backward-wrap branch (`e.shiftKey && active === first`) -- without it,
    // Shift+Tab from the first focusable element would leave the dialog
    // backward instead of looping to the last stop.
    await page.keyboard.press("Shift+Tab");
    await expect(page.getByTestId("widget-search")).toBeFocused();
  });

  test("focus placed on an option row by script, not by a pointer, is pulled back into the trap rather than escaping", async ({
    page,
  }) => {
    await openPalette(page);

    // Option rows carry tabIndex={-1} and are excluded from `stops`
    // (FOCUSABLE_SELECTOR), and the only way a pointer used to land focus on
    // one -- a mousedown -- is now blocked by that row's own
    // `onMouseDown` preventDefault (see the "already on this dashboard"
    // spec above). So the untracked-focus backstop in `handleDialogKeyDown`
    // (the branch that fires when `document.activeElement` is not among
    // `stops`) has no reachable trigger left through the UI. It stays real
    // and load-bearing -- anything that ever moves focus inside the dialog
    // without going through the trap's own stops hits it -- so it is
    // exercised here by moving focus the one way still available: directly,
    // bypassing the pointer guard entirely.
    await page.evaluate(() => {
      const row = document.querySelector<HTMLElement>(
        '[data-testid^="widget-option-"]',
      );
      row?.focus();
    });

    await page.keyboard.press("Tab");
    // Pulled to the first stop, not left on the option and not escaped.
    await expect(page.getByTestId("close-palette")).toBeFocused();
    // Specifically still inside the modal -- not merely "not on the option"
    // -- since the untracked branch existing at all means the naive failure
    // mode is Tab walking straight out to the page behind the scrim.
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("Ctrl/Cmd+K does not stack the global command palette on top of the catalog", async ({
    page,
  }) => {
    await openPalette(page);

    await page.keyboard.press("Control+k");

    // CommandPalette.tsx exposes no test id -- its only stable marker is
    // aria-label="Command palette" on its own role="dialog" root -- so its
    // absence is asserted by counting role="dialog" nodes rather than
    // querying it directly. One dialog on screen means this one's
    // stopPropagation, not the global listener, won the key.
    await expect(page.locator('[role="dialog"]')).toHaveCount(1);
    await expect(palette(page)).toBeVisible();
    await expect(page.getByTestId("widget-search")).toBeFocused();
  });

  test("adding a widget in one-column mode leaves the keyboard on Add widget, not the page", async ({
    page,
  }) => {
    await openPaletteNarrow(page);

    await option(page, "recent-events").click();
    await expect(palette(page)).toBeHidden();

    // The focus assertions below prove nothing on their own: closing the
    // palette without adding anything lands the keyboard on Add widget too,
    // so a narrow-only regression that drops the insertion (the add handler
    // never reaches the grid, or reaches it but the row never mounts because
    // of the narrow layout math) would still pass this test. Proving the
    // widget actually arrived is what the wide-mode "adds the chosen widget"
    // spec does for the pointer path; this is its one-column twin.
    const added = page.locator('[data-instance-id^="recent-events-"]');
    await expect(added).toHaveCount(1);
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length + 1,
    );
    // The insertion reached the edit session, not just the grid.
    await expect(page.getByTestId("save-layout")).toBeEnabled();

    // Below the grid's narrow breakpoint a placed widget carries no tabindex
    // at all (DashboardGrid withholds it -- see its `arrangeable` prop), so
    // the focus() call the wide-mode insertion test relies on silently
    // no-ops there. The fallback in DashboardV2's insertion effect is what
    // catches that and moves focus to the button instead of letting it drop
    // to the top of the page.
    await expect(page.getByTestId("add-widget")).toBeFocused();
    await expect(page.locator("body")).not.toBeFocused();
  });

  test("Cancel discards a widget added from the palette, on screen and in the session", async ({
    page,
  }) => {
    // Insertion is the only handler that writes the edit session AND
    // re-mounts the grid in the same pass (DashboardV2's `addWidget`), so
    // Cancel has to undo both halves. "Cancel restores" is otherwise only
    // exercised for drags (dashboard-edit.spec.ts), which go through the
    // session by a different path (`handleChange`, not `applyChange` +
    // `mountGrid` together) -- so this is not redundant with those specs.
    await openPalette(page);

    await option(page, "recent-events").click();
    await expect(palette(page)).toBeHidden();
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length + 1,
    );

    await page.getByTestId("cancel-edit").click();
    // Cancel with unsaved changes confirms first -- same path as
    // dashboard-edit.spec.ts's "Cancel after a change asks first" spec, which
    // exercises it for a drag rather than an addition.
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Discard unsaved changes?");
    await dialog.getByRole("button", { name: "Discard changes" }).click();

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(
      page.locator('[data-instance-id^="recent-events-"]'),
    ).toHaveCount(0);
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length,
    );

    // And the session, not just the screen: re-entering edit mode must not
    // resurrect the widget from a working copy the discard failed to clear.
    await page.getByTestId("edit-layout").click();
    await expect(
      page.locator('[data-instance-id^="recent-events-"]'),
    ).toHaveCount(0);
    await expect(page.getByTestId("grid-item")).toHaveCount(
      PARTIAL_LAYOUT.items.length,
    );
  });
});
