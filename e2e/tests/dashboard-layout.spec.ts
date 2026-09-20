import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import {
  attachAuthInjection,
  focusedElementName,
  getAuthHeaders,
} from "../helpers.ts";
// The route glob and the two pure response shapers, and nothing else from the
// stub. This file does not stub the store (see the header) -- but the endpoint
// it talks to is the same one, and the URL and the record shape are exactly the
// kind of thing the stub's own header warns about keeping a second copy of.
import { json, LAYOUT_URL, record } from "./dashboard-layout-stub.ts";
import { DEFAULT_OVERVIEW_LAYOUT } from "../../frontend/lib/dashboard/default-layout.ts";
import type {
  DashboardLayoutConfig,
  LayoutItem,
} from "../../frontend/lib/dashboard/types.ts";

/**
 * Dashboard builder acceptance (Release G / P4, D18).
 *
 * The eight end-to-end claims the feature makes as a whole: an arrangement a
 * user makes is still there tomorrow, Reset takes it back, a layout naming a
 * widget this build does not have degrades instead of breaking, the grid is
 * operable and readable without a mouse or a wide screen, and a second tab is
 * told when it loses a race rather than quietly overwriting the winner.
 *
 * THIS FILE USES THE REAL STORE. Every other dashboard editor spec
 * (dashboard-edit, dashboard-palette, dashboard-copy) mocks
 * /api/v1/preferences/layouts through dashboard-layout-stub.ts, and it has to:
 * a layout is stored per (user, cluster, scope), the whole suite shares one
 * login, and a spec that really saved would hand its arrangement to every
 * later spec that asserts where the DEFAULT layout puts things -- all of
 * dashboard-grid.spec.ts and half of dashboard.spec.ts. This file is the
 * deliberate exception the plan asked D18 to decide, because "persists across
 * a reload" and "the other tab is told" are claims about the server, and a
 * mock cannot make either of them true. Two things pay for it:
 *
 *   - It runs in its own Playwright project ("dashboard-layout"), gated on the
 *     main chromium project finishing, exactly as discoverability.spec.ts is
 *     and for the same reason. `fullyParallel: false` orders tests within a
 *     file, not across files, and `workers` is pinned to 1 only in CI -- so
 *     locally this file could otherwise land in a worker beside
 *     dashboard-grid.spec.ts and change what it sees mid-test.
 *   - `afterEach` puts the shipped default back, so nothing survives a spec.
 *     The layouts endpoint has no DELETE (the scope is the address; PUT
 *     creates or replaces), so "clean" means "stores the default", which
 *     renders identically to storing nothing.
 *
 * NOT covered here, and covered elsewhere instead:
 *   - The no-database deployment (spec scenario 7). The E2E harness always
 *     runs PostgreSQL (playwright.config.ts webServer env), so
 *     "database_unavailable" is unreachable from a browser, exactly as
 *     pins.spec.ts:29-33 records for its own 503 case. It is asserted in Go by
 *     TestHandler_NoDatabase_Returns503
 *     (backend/internal/preferences/handler_test.go), whose allEndpoints()
 *     table covers GET and PUT on both layout routes. The client half -- a
 *     503 read leaves the default rendered and the grid read-only -- is
 *     dashboard-grid.spec.ts's "a layout the server cannot supply leaves the
 *     grid read-only".
 *   - Drag and resize geometry: dashboard-grid.spec.ts.
 *   - Which gestures dirty a session, what Cancel puts back, and the stubbed
 *     conflict path: dashboard-edit.spec.ts.
 *   - The widget catalog and what it refuses to add: dashboard-palette.spec.ts.
 *   - Copy from another cluster: dashboard-copy.spec.ts.
 */

/**
 * The endpoint as a request path, which `LAYOUT_URL`'s route glob cannot be.
 *
 * The stub exports the glob because it registers routes with it; this file
 * fetches the endpoint for real, and `page.request` wants a path.
 */
const LAYOUT_PATH = "/api/v1/preferences/layouts/overview";

/** The cluster these layouts belong to. Pinned, never inferred. */
const CLUSTER = "local";

/** The widget the editor specs also move: it starts in column 10 of 12. */
const SUBJECT = "d-active-alerts";

/** Where `SUBJECT` sits in the shipped default, 1-based. */
const SUBJECT_DEFAULT_COLUMN = 10;

const widget = (page: Page, id: string) =>
  page.locator(`[data-instance-id="${id}"]`);

async function layoutHeaders(page: Page): Promise<Record<string, string>> {
  return { ...(await getAuthHeaders(page)), "X-Cluster-ID": CLUSTER };
}

interface StoredLayout {
  revision: number;
  config: DashboardLayoutConfig;
}

/** The stored layout, or `null` when the user has never saved one (204). */
async function readStoredLayout(page: Page): Promise<StoredLayout | null> {
  const res = await page.request.get(LAYOUT_PATH, {
    headers: await layoutHeaders(page),
    failOnStatusCode: false,
  });
  if (res.status() === 204) return null;
  if (!res.ok()) {
    throw new Error(
      `reading the stored layout failed: ${res.status()} ${await res.text()}`,
    );
  }
  const stored = (await res.json()).data;
  return { revision: stored.revision as number, config: stored.config };
}

/** A layout item's identity and placement, comparable across a round trip. */
const placements = (items: LayoutItem[]) =>
  [...items]
    .map((i) => ({
      instanceId: i.instanceId,
      id: i.id,
      x: i.x,
      y: i.y,
      w: i.w,
      h: i.h,
    }))
    .sort((a, b) => a.instanceId.localeCompare(b.instanceId));

/**
 * Writes `config` over whatever revision is stored, claiming `revision`.
 *
 * Seeding, not a test of the write path -- a refusal throws with the server's
 * own words rather than leaving the spec to fail later on a layout that was
 * never stored.
 */
async function putLayout(
  page: Page,
  revision: number,
  config: DashboardLayoutConfig,
): Promise<void> {
  const res = await page.request.put(LAYOUT_PATH, {
    headers: await layoutHeaders(page),
    data: { revision, config },
    failOnStatusCode: false,
  });
  if (!res.ok()) {
    throw new Error(
      `seeding the layout failed: ${res.status()} ${await res.text()}`,
    );
  }
}

/** Reads the stored revision, then writes `config` over it. */
async function seedLayout(
  page: Page,
  config: DashboardLayoutConfig,
): Promise<void> {
  const current = await readStoredLayout(page);
  await putLayout(page, current?.revision ?? 0, config);
}

/**
 * Puts the shipped default back, so no spec hands its arrangement to the next.
 *
 * One read, and a write only when there is something to undo: nothing stored
 * and a stored default are both already clean. Skipping the no-op write is not
 * only cheaper -- it keeps the revision from climbing on every hook in a file
 * whose specs assert on revisions the client claims.
 *
 * Teardown failures throw rather than passing quietly, the same rule
 * helpers.ts states for the pin and saved-view cleanups: a teardown that
 * swallows a failure lets the suite report success while leaving a layout
 * behind, and the next spec fails somewhere unrelated.
 */
async function restoreDefaultLayout(page: Page): Promise<void> {
  const current = await readStoredLayout(page);
  if (current === null) return;
  if (
    JSON.stringify(placements(current.config.items)) ===
    JSON.stringify(placements(DEFAULT_OVERVIEW_LAYOUT.items))
  ) {
    return;
  }
  await putLayout(page, current.revision, DEFAULT_OVERVIEW_LAYOUT);
}

/**
 * A layout a user could have arranged: the two metric tiles in row 1 swapped.
 *
 * A swap rather than moving one widget somewhere emptier, because the server
 * refuses an overlap (`invalid_config`) and the shipped default leaves no
 * three-column gap for a tile to move into. These two are the same size and
 * share a row, so exchanging their columns is both legal and obvious on
 * screen, and it survives the grid's vertical compaction unchanged.
 */
const SWAPPED = { a: "d-cpu-tile", b: "d-memory-tile" } as const;
/** Where `SWAPPED.a` sits in the default and in the swap, 1-based. */
const SWAPPED_A_DEFAULT_COLUMN = 7;
const SWAPPED_A_CUSTOM_COLUMN = 10;

function customLayout(): DashboardLayoutConfig {
  const xOf = (instanceId: string) => {
    const item = DEFAULT_OVERVIEW_LAYOUT.items.find(
      (i) => i.instanceId === instanceId,
    );
    if (!item) throw new Error(`no default item ${instanceId}`);
    return item.x;
  };
  const swap: Record<string, number> = {
    [SWAPPED.a]: xOf(SWAPPED.b),
    [SWAPPED.b]: xOf(SWAPPED.a),
  };
  return {
    ...DEFAULT_OVERVIEW_LAYOUT,
    items: DEFAULT_OVERVIEW_LAYOUT.items.map((i) =>
      i.instanceId in swap ? { ...i, x: swap[i.instanceId] } : i,
    ),
  };
}

/**
 * The column a widget reports in its accessible name, 1-based.
 *
 * Only meaningful in edit mode: outside it a widget carries no name at all,
 * which dashboard-grid.spec.ts asserts.
 */
async function column(page: Page, id: string): Promise<number> {
  const label = await widget(page, id).getAttribute("aria-label");
  const m = /column (\d+) of/.exec(label ?? "");
  if (m === null) throw new Error(`no column in aria-label: ${label}`);
  return Number(m[1]);
}

function expectColumn(page: Page, id: string, col: number) {
  return expect(widget(page, id)).toHaveAttribute(
    "aria-label",
    new RegExp(`column ${col} of`),
  );
}

/** Loads the dashboard with every widget rendered, then enters edit mode. */
async function editableDashboard(page: Page): Promise<void> {
  await page.goto("/");
  await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
  await page.getByTestId("edit-layout").click();
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-editable",
    "true",
  );
}

/** Moves `SUBJECT` one column with the keyboard and returns where it landed. */
async function nudge(
  page: Page,
  key: "ArrowLeft" | "ArrowRight",
): Promise<number> {
  const before = await column(page, SUBJECT);
  await widget(page, SUBJECT).focus();
  await page.keyboard.press(key);
  const after = before + (key === "ArrowLeft" ? -1 : 1);
  await expectColumn(page, SUBJECT, after);
  return after;
}

/** Saves and waits for the session to close, which is what proves the write. */
async function saveLayout(page: Page): Promise<void> {
  await page.getByTestId("save-layout").click();
  // The closed session, not the toast: the toast dismisses itself on a timer
  // and a test that races it is a flake. Same rule dashboard-edit.spec.ts and
  // saved-views.spec.ts both state.
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-editable",
    "false",
  );
}

/**
 * Presses Tab (or Shift+Tab) until `match` accepts the focused element.
 *
 * Bounded and loud: a keyboard claim that silently gave up after N presses
 * would be a test that passes when the thing it names is unreachable.
 */
async function tabUntil(
  page: Page,
  match: (name: string) => boolean,
  opts: { back?: boolean; limit?: number } = {},
): Promise<string> {
  const key = opts.back ? "Shift+Tab" : "Tab";
  const limit = opts.limit ?? 40;
  const seen: string[] = [];
  for (let i = 0; i < limit; i++) {
    await page.keyboard.press(key);
    const name = await focusedElementName(page);
    seen.push(name);
    if (match(name)) return name;
  }
  throw new Error(
    `nothing matched after ${limit} ${key} presses; focus visited ${seen.join(" -> ")}`,
  );
}

test.describe.serial("Dashboard layout acceptance", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
    // Belt and braces with the pin and cluster specs: these run serially, so a
    // cluster left selected by a failed spec elsewhere would silently re-point
    // every layout read and write at a cluster that does not exist.
    await page.evaluate(() =>
      localStorage.setItem(
        "k8scenter.clusterTarget",
        JSON.stringify({ clusterId: "local", generation: "local" }),
      ),
    );
    await restoreDefaultLayout(page);
  });

  test.afterEach(async ({ page }) => {
    await restoreDefaultLayout(page);
  });

  test.afterAll(async ({ browser }) => {
    // afterAll gets no page fixture, and a spec that failed before its
    // afterEach could still have left a layout stored. Open a context of our
    // own so the last word on this user's layout is the default.
    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    const page = await context.newPage();
    await attachAuthInjection(page);
    await page.goto("/");
    await restoreDefaultLayout(page);
    await context.close();
  });

  test("an arrangement is still there after a reload", async ({ page }) => {
    await editableDashboard(page);
    const moved = await nudge(page, "ArrowLeft");
    expect(moved).toBe(SUBJECT_DEFAULT_COLUMN - 1);
    await saveLayout(page);

    // The server really holds it, not just this tab's memory of it.
    const stored = await readStoredLayout(page);
    expect(stored, "the save reached the store").not.toBeNull();
    expect(
      stored?.config.items.find((i) => i.instanceId === SUBJECT)?.x,
      "the stored layout carries the column the user moved the widget to",
    ).toBe(moved - 1);

    await page.reload();
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await page.getByTestId("edit-layout").click();
    expect(await column(page, SUBJECT)).toBe(moved);
  });

  test("Reset puts the shipped default back", async ({ page }) => {
    await seedLayout(page, customLayout());

    await editableDashboard(page);
    expect(
      await column(page, SWAPPED.a),
      "the seeded layout is the one on screen, so the reset has something to undo",
    ).toBe(SWAPPED_A_CUSTOM_COLUMN);

    await page.getByTestId("reset-layout").click();
    await page.getByRole("button", { name: "Reset layout" }).click();

    // Reset writes nothing on its own -- Save is still the only thing that
    // reaches the server -- so the default has to be on screen first and in
    // the store only after.
    await expectColumn(page, SWAPPED.a, SWAPPED_A_DEFAULT_COLUMN);
    await saveLayout(page);

    const stored = await readStoredLayout(page);
    expect(placements(stored?.config.items ?? [])).toEqual(
      placements(DEFAULT_OVERVIEW_LAYOUT.items),
    );

    await page.reload();
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await page.getByTestId("edit-layout").click();
    expect(await column(page, SWAPPED.a)).toBe(SWAPPED_A_DEFAULT_COLUMN);
  });

  test("a layout naming a widget this build lacks renders the rest and says which", async ({
    page,
  }) => {
    // Stubbed, and it has to be: the write path refuses an id its catalog does
    // not know (`unknown_widget_id`, backend/internal/preferences/dashboard.go
    // ValidateDashboardLayout, covered by P3/D12), so this layout cannot be
    // seeded through the API the way every other spec here seeds one. The case
    // the READ path has to survive is a layout stored by a NEWER build and read
    // back by an older one, which no request this suite can make will produce.
    const fromTheFuture: DashboardLayoutConfig = {
      ...DEFAULT_OVERVIEW_LAYOUT,
      items: [
        { instanceId: "d-cluster-health", id: "cluster-health", x: 0, y: 0, w: 6, h: 6 },
        { instanceId: "d-future", id: "widget-from-the-future", x: 6, y: 0, w: 6, h: 6 },
        { instanceId: "d-nodes", id: "nodes", x: 0, y: 6, w: 4, h: 5 },
      ],
    };
    await page.route(LAYOUT_URL, async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await json(route, 200, { data: record(7, fromTheFuture) });
    });

    await page.goto("/");

    // The widgets this build does have are all still there...
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(2);
    await expect(widget(page, "d-cluster-health")).toBeVisible();
    await expect(widget(page, "d-nodes")).toBeVisible();
    // ...the one it does not is gone rather than rendered as a hole...
    await expect(widget(page, "d-future")).toHaveCount(0);
    // ...and the user is told which, by name. Silently dropping it would make
    // a widget disappear with no account of where it went.
    await expect(
      page.getByText(
        'Removed "widget-from-the-future" from this dashboard: this build has no such widget.',
      ),
    ).toBeVisible();

    // Dropping everything a build does not know must not substitute the
    // shipped default over an arrangement the user made: two widgets, not ten.
    await expect(page.getByTestId("grid-item")).toHaveCount(2);
  });

  test("the layout is arrangeable with the keyboard alone", async ({
    page,
  }) => {
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    // Into edit mode without a click.
    await page.getByTestId("edit-layout").focus();
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );

    // Tab forward until a widget itself has focus. The first one in reading
    // order is Cluster Health, which starts hard against the left edge, so
    // right is the direction that has somewhere to go.
    const reached = await tabUntil(page, (name) => name.startsWith("d-"));
    expect(reached, "the first widget in reading order").toBe(
      "d-cluster-health",
    );

    const before = await column(page, "d-cluster-health");
    expect(before).toBe(1);
    await page.keyboard.press("ArrowRight");
    await expectColumn(page, "d-cluster-health", before + 1);

    // Back out to Save, still without a pointer. The toolbar sits ahead of the
    // grid in the DOM, with the four time-range buttons between them.
    const landed = await tabUntil(page, (name) => name === "save-layout", {
      back: true,
    });
    expect(landed).toBe("save-layout");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );

    await page.reload();
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await page.getByTestId("edit-layout").click();
    expect(await column(page, "d-cluster-health")).toBe(before + 1);
  });

  test("a customized dashboard is readable at 400px", async ({ page }) => {
    // A layout the user arranged, not the shipped one: the collapse has to
    // survive placements nobody designed the breakpoint around.
    await seedLayout(page, customLayout());
    await page.setViewportSize({ width: 400, height: 900 });
    await page.goto("/");

    const grid = page.getByTestId("dashboard-grid");
    await expect(grid).toHaveAttribute("data-grid-mode", "narrow");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    // One column: every cell shares a left edge and spans the grid, and each
    // starts below the one before it. Matching left edges alone would also
    // pass for two full-width cells overlapping on one row.
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
      expect(Math.abs(cell.width - cell.gridWidth), `cell ${i} width`)
        .toBeLessThanOrEqual(1);
      if (i > 0) {
        expect(cell.top, `cell ${i} starts below cell ${i - 1}`)
          .toBeGreaterThanOrEqual(cells[i - 1].bottom);
      }
    }

    // Nothing pushes the page sideways. A dashboard you have to scroll
    // horizontally on a phone is the failure this whole breakpoint exists for.
    const overflow = await page.evaluate(() => {
      const d = document.documentElement;
      return { scrollWidth: d.scrollWidth, clientWidth: d.clientWidth };
    });
    expect(overflow.scrollWidth, "the page is no wider than the viewport")
      .toBeLessThanOrEqual(overflow.clientWidth + 1);

    // Arranging is a wide-grid gesture: one column has no columns to move
    // between and no width to size, so the items are not arrangeable here --
    // no drag handles, and no widget is a tab stop. Edit mode itself is still
    // offered, because taking a widget OFF the dashboard is neither of those
    // things (DashboardGrid.tsx's `removable`, which is deliberately not the
    // same flag as `arrangeable`).
    await page.getByTestId("edit-layout").click();
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);
    await expect(page.getByTestId("remove-widget")).toHaveCount(10);
  });

  test("a second tab is told its save is stale rather than quietly winning", async ({
    page,
    context,
  }) => {
    // The real store, so the 409 is the server's own concurrency control
    // rather than a canned one. dashboard-edit.spec.ts covers what the UI does
    // with a stubbed conflict; this covers that a conflict happens at all.
    const other = await context.newPage();
    // A page made from the context does NOT get fixtures/base.ts, so its
    // requests would go out anonymous and it would keep the animations the
    // fixture turns off. helpers.ts says so at length for the first half.
    await other.emulateMedia({ reducedMotion: "reduce" });
    await attachAuthInjection(other);

    try {
      // Both tabs open the editor on the same revision.
      await editableDashboard(page);
      await editableDashboard(other);

      // Tab A wins.
      const mine = await nudge(page, "ArrowLeft");
      await saveLayout(page);

      // Tab B was editing from the revision A just replaced. Two columns, not
      // one: A also moved left, and an arrangement that matched A's would make
      // "B's work survived" and "B took A's" the same assertion. (Right is not
      // available -- the widget's default placement ends at the last column.)
      await nudge(other, "ArrowLeft");
      const theirs = await nudge(other, "ArrowLeft");
      expect(theirs).not.toBe(mine);
      await other.getByTestId("save-layout").click();

      await expect(
        other.getByText("This dashboard changed somewhere else"),
      ).toBeVisible();
      // B's work is still on screen and still B's -- the dialog is a choice,
      // not a message, and the arrangement is the only copy of that work.
      expect(await column(other, SUBJECT)).toBe(theirs);
      await expect(other.getByTestId("dashboard-grid")).toHaveAttribute(
        "data-grid-editable",
        "true",
      );

      // And the store still holds A's, not B's: being told is worthless if the
      // refused write landed anyway.
      const stored = await readStoredLayout(page);
      expect(
        stored?.config.items.find((i) => i.instanceId === SUBJECT)?.x,
        "the winner's placement survived the loser's save",
      ).toBe(mine - 1);

      // Taking the stored layout is the way out, and it brings A's back.
      await other.getByRole("button", { name: "Load the saved layout" }).click();
      await expect(other.getByTestId("edit-layout")).toBeVisible();
      await other.getByTestId("edit-layout").click();
      expect(await column(other, SUBJECT)).toBe(mine);
    } finally {
      await other.close();
    }
  });

  test("a widget whose source fails reports itself and leaves the rest of a customized dashboard up", async ({
    page,
  }) => {
    // P1/D5b proved this on the shipped default (dashboard.spec.ts, "a widget
    // reports its own failure without blanking the page"). The claim D18 adds
    // is that it still holds once the layout is one the user arranged, which
    // is what every dashboard becomes.
    await seedLayout(page, customLayout());
    await page.route("**/api/v1/resources/events*", (route) =>
      route.abort("failed"),
    );
    await page.goto("/");

    await expect(
      widget(page, "d-recent-events").getByTestId("widget-error"),
    ).toBeVisible();
    await expect(page.getByTestId("widget-error")).toHaveCount(1);
    // Every other widget still got there.
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(9);

    // And the failure did not cost the user their arrangement: the customized
    // placement is still what the grid renders, and still what is stored.
    await page.getByTestId("edit-layout").click();
    expect(await column(page, SWAPPED.a)).toBe(SWAPPED_A_CUSTOM_COLUMN);
    const stored = await readStoredLayout(page);
    expect(placements(stored?.config.items ?? [])).toEqual(
      placements(customLayout().items),
    );
  });
});
