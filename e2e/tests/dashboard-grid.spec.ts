import type { Page } from "@playwright/test";
import { test, expect } from "../fixtures/base.ts";
// The grid's own constants, imported rather than copied: a change to the row
// height or the gap has to break these tests loudly, not make them assert the
// wrong geometry in silence. Importing across the project boundary is the
// established pattern here -- security-headers, websocket-channels and
// websocket-rejection all do it -- and types.ts's only import is a type-only
// one, which the transpiler erases.
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_GRID_GAP,
  DASHBOARD_ROW_HEIGHT,
} from "../../frontend/lib/dashboard/types.ts";

// Drag and resize behaviour on the dashboard's snapping grid. The geometry
// itself is unit tested in frontend/lib/dashboard/grid_test.ts; these tests
// prove the island routes the pointer through that engine instead of
// positioning widgets itself, and that the pointer session starts and ends
// where it should.

interface Cell {
  id: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

const cells = (page: Page): Promise<Cell[]> =>
  page.getByTestId("grid-item").evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return {
        id: el.getAttribute("data-instance-id") ?? "",
        x: Math.round(r.x),
        y: Math.round(r.y),
        width: Math.round(r.width),
        height: Math.round(r.height),
      };
    }),
  );

/** Every pair of cells that shares any area, named so a failure says which. */
function overlapping(items: Cell[]): string[] {
  const out: string[] = [];
  for (let a = 0; a < items.length; a++) {
    for (let b = a + 1; b < items.length; b++) {
      const p = items[a];
      const q = items[b];
      const apart =
        p.x + p.width <= q.x ||
        q.x + q.width <= p.x ||
        p.y + p.height <= q.y ||
        q.y + q.height <= p.y;
      if (!apart) out.push(`${p.id}/${q.id}`);
    }
  }
  return out;
}

const at = (items: Cell[], id: string): Cell => {
  const found = items.find((c) => c.id === id);
  if (!found) throw new Error(`no grid item ${id}`);
  return found;
};

const handle = (page: Page, id: string) =>
  page.locator(`[data-instance-id="${id}"] [data-testid="drag-handle"]`);

const corner = (page: Page, id: string) =>
  page.locator(`[data-instance-id="${id}"] [data-testid="resize-handle"]`);

/** Loads the dashboard with every widget rendered, then enters edit mode. */
async function editableDashboard(page: Page) {
  await page.goto("/");
  await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-mode",
    "wide",
  );

  await page.getByTestId("edit-layout").click();
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-editable",
    "true",
  );
}

/**
 * Puts the pointer on `id`'s handle and reports where every cell sits.
 *
 * Reaching a widget near the bottom scrolls the page, which moves every other
 * cell. Measuring after the hover is what keeps the drag targets below
 * pointing at the cells their tests name.
 */
async function grab(page: Page, id: string): Promise<Cell[]> {
  await handle(page, id).hover();
  await recordNextPointerId(page);
  return await cells(page);
}

/** Records the id of whichever pointer starts the next session, so a test that
 * has to name it does not have to guess. */
async function recordNextPointerId(page: Page) {
  await page.evaluate(() => {
    globalThis.addEventListener(
      "pointerdown",
      (ev) => {
        (globalThis as unknown as { __pointerId: number }).__pointerId = (
          ev as PointerEvent
        ).pointerId;
      },
      { capture: true, once: true },
    );
  });
}

/** The pointer id `recordNextPointerId` saw. */
const startedPointerId = (page: Page): Promise<number> =>
  page.evaluate(
    () => (globalThis as unknown as { __pointerId: number }).__pointerId,
  );

/** The instance ids in DOM order, which is the layout's reading order. */
const order = (items: Cell[]): string[] => items.map((c) => c.id);

/** Drags `id` by its handle to a point, in steps, so pointermove fires. */
async function dragTo(page: Page, id: string, to: { x: number; y: number }) {
  await handle(page, id).hover();
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
}

/** Puts the pointer on `id`'s corner grip and reports where every cell sits. */
async function grabCorner(page: Page, id: string): Promise<Cell[]> {
  await corner(page, id).hover();
  await recordNextPointerId(page);
  return await cells(page);
}

/** Drags `id`'s corner grip to a point, in steps, so pointermove fires. */
async function resizeTo(page: Page, id: string, to: { x: number; y: number }) {
  await corner(page, id).hover();
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
}

/**
 * A point a fifth of the way into the cell `right` columns and `down` rows from
 * `item`'s top-left -- the cell its bottom-right corner must land on to make it
 * `right + 1` wide and `down + 1` tall.
 *
 * Inside that cell, deliberately, rather than on its far edge. On the far edge
 * the snapped size and the raw pointer distance are the same number to the
 * pixel, so a widget that followed the pointer continuously -- the one thing a
 * snapping grid must not do -- would satisfy every size assertion below.
 */
function cornerCell(g: Geometry, item: Cell, right: number, down: number) {
  return {
    x: item.x + right * (g.cellWidth + g.gap) + 0.2 * g.cellWidth,
    y: item.y + down * (g.rowHeight + g.gap) + 0.2 * g.rowHeight,
  };
}

/**
 * The live grid's cell geometry in CSS pixels, so a test can express a resize
 * as "two columns wider" rather than as a pixel count that only holds at one
 * viewport size. Mirrors `metricsFrom` in frontend/lib/dashboard/grid.ts.
 */
async function geometry(page: Page) {
  const width = await page
    .getByTestId("dashboard-grid")
    .evaluate((el) => el.getBoundingClientRect().width);
  return {
    gap: DASHBOARD_GRID_GAP,
    rowHeight: DASHBOARD_ROW_HEIGHT,
    cellWidth:
      (width - DASHBOARD_GRID_GAP * (DASHBOARD_COLUMNS - 1)) /
      DASHBOARD_COLUMNS,
  };
}

type Geometry = Awaited<ReturnType<typeof geometry>>;

/** The on-screen size of a w-by-h block of cells, the gaps between included. */
function blockSize(g: Geometry, w: number, h: number) {
  return {
    width: g.cellWidth * w + g.gap * (w - 1),
    height: g.rowHeight * h + g.gap * (h - 1),
  };
}

/**
 * Asserts a measured size is the one `blockSize` predicts.
 *
 * Within half a cell, not to the pixel: a column is a fraction of whatever the
 * container is wide, and a multi-column span accumulates that rounding. Half a
 * cell still tells six columns from five or seven -- they are a whole column
 * stride apart, some 2.5x the tolerance -- which is what these tests are about.
 *
 * Not because the page's scroll bar might appear mid-test: the scrolling box is
 * a fixed-height `overflow-y: auto` `main`, and the grid is taller than it from
 * the first paint, so every column edge holds still for the whole run. That is
 * what lets the tests around this one compare positions with exact equality.
 */
function expectBlock(
  got: Cell,
  g: Geometry,
  w: number,
  h: number,
  what: string,
) {
  const want = blockSize(g, w, h);
  expect(
    Math.abs(got.width - want.width),
    `${what}: width should be ${w} columns`,
  ).toBeLessThanOrEqual(g.cellWidth / 2);
  expect(
    Math.abs(got.height - want.height),
    `${what}: height should be ${h} rows`,
  ).toBeLessThanOrEqual(g.rowHeight / 2);
}

test.describe("Dashboard grid drag", () => {
  test("a widget follows the pointer into a new cell", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    // Active Alerts sits in the right column; drag it onto the left one.
    await dragTo(page, "d-active-alerts", {
      x: health.x + 40,
      y: alerts.y + 8,
    });

    const after = await cells(page);
    expect(at(after, "d-active-alerts").x).toBeLessThan(alerts.x);
    // Whole cells only: the widget snaps to a column, never to the pointer.
    expect(at(after, "d-active-alerts").width).toBe(alerts.width);
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
    // The drop ended the session.
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
  });

  test("a drag never leaves two widgets on the same cell", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-nodes");
    const nodes = at(before, "d-nodes");
    // Straight onto the middle of the biggest widget, which the engine has to
    // displace downward rather than share a cell with.
    const health = at(before, "d-cluster-health");

    await dragTo(page, "d-nodes", {
      x: health.x + Math.round(health.width / 2),
      y: health.y + Math.round(health.height / 2),
    });

    const after = await cells(page);
    expect(overlapping(after)).toEqual([]);
    expect(at(after, "d-nodes").y).not.toBe(nodes.y);
    // Displacement is downward only: nothing moved sideways to make room.
    expect(at(after, "d-cluster-health").x).toBe(health.x);
  });

  test("Escape during a drag puts the layout back", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    await expect(
      page.locator('[data-instance-id="d-active-alerts"]'),
    ).toHaveAttribute("data-dragging", "true");
    // The widget has actually moved: without this the test could certify a
    // restore that had nothing to restore.
    expect(at(await cells(page), "d-active-alerts").x).not.toBe(alerts.x);

    await page.keyboard.press("Escape");
    await page.mouse.up();

    await expect(
      page.locator(
        '[data-instance-id="d-active-alerts"][data-dragging="true"]',
      ),
    ).toHaveCount(0);
    const after = await cells(page);
    expect(at(after, "d-active-alerts").x).toBe(alerts.x);
    expect(at(after, "d-active-alerts").y).toBe(alerts.y);
    expect(after).toEqual(before);
  });

  test("an interrupted pointer puts the layout back", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    expect(at(await cells(page), "d-active-alerts").x).not.toBe(alerts.x);

    // What the OS taking the pointer looks like to the page: a system
    // gesture, or a touch the browser decided was a scroll. The cancel has to
    // name the pointer that started the drag, which the page recorded rather
    // than this test assuming it.
    const pointerId = await startedPointerId(page);
    await handle(page, "d-active-alerts").evaluate((el, id) => {
      el.dispatchEvent(
        new PointerEvent("pointercancel", { pointerId: id, bubbles: true }),
      );
    }, pointerId);

    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
    expect(await cells(page)).toEqual(before);
    await page.mouse.up();
  });

  // "a drag survives the handle disappearing under it" was removed in D15.
  // It flipped edit mode off mid-drag by pressing Space on the still-focused
  // "Edit layout" toggle, and that toggle no longer exists while editing --
  // the toolbar replaces it with Cancel and Save, neither of which can be
  // pressed with a pointer captured by a drag. The grid teardown it covered
  // (the session must end when its handle unmounts) is the same effect the
  // next test drives through the one trigger a user can still reach.

  test("collapsing to one column mid-drag puts the layout back", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    expect(at(await cells(page), "d-active-alerts").x).not.toBe(alerts.x);

    // The window narrows under the drag. One column has no columns to drag
    // between, so the session ends -- and the user never dropped it, so the
    // layout goes back.
    await page.setViewportSize({ width: 700, height: 900 });
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "narrow",
    );
    await page.mouse.up();
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);

    // Back to a wide grid to read the layout in the terms the drag used.
    // Reading order, not pixels: the viewport changed, and a committed drag
    // would have put Active Alerts somewhere earlier in that order.
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "wide",
    );
    expect(order(await cells(page))).toEqual(order(before));
  });

  test("a second pointer cannot start its own drag", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });

    // A second finger lands on another widget's handle. Each session keeps
    // its own pre-drag layout, so a second one could restore over the first
    // one's work; only one may run.
    await handle(page, "d-nodes").evaluate((el) => {
      el.dispatchEvent(
        new PointerEvent("pointerdown", {
          pointerId: 99,
          isPrimary: false,
          button: 0,
          buttons: 1,
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    await expect(page.locator('[data-dragging="true"]')).toHaveCount(1);
    await expect(
      page.locator(
        '[data-instance-id="d-active-alerts"][data-dragging="true"]',
      ),
    ).toHaveCount(1);
    await page.mouse.up();
  });

  test("a non-primary button is not a drag", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down({ button: "middle" });
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });

    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
    await page.mouse.up({ button: "middle" });
    expect(order(await cells(page))).toEqual(order(before));
  });

  test("one column offers no handles at all", async ({ page }) => {
    // Well under the 900px grid-width breakpoint once the sidebar is counted.
    await page.setViewportSize({ width: 700, height: 900 });
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    const grid = page.getByTestId("dashboard-grid");
    await expect(grid).toHaveAttribute("data-grid-mode", "narrow");
    await page.getByTestId("edit-layout").click();
    await expect(grid).toHaveAttribute("data-grid-editable", "true");

    // Edit mode is on, but a single column has no columns to drag between and
    // no width to size: every widget is already full width.
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);
    await expect(page.getByTestId("resize-handle")).toHaveCount(0);
  });

  test("outside edit mode there is nothing to drag", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);
    await expect(page.getByTestId("resize-handle")).toHaveCount(0);

    // A press-and-drag across a title row must leave the layout alone: the
    // dashboard is a monitoring surface first.
    const before = await cells(page);
    const alerts = at(before, "d-active-alerts");
    await page.mouse.move(alerts.x + 40, alerts.y + 12);
    await page.mouse.down();
    await page.mouse.move(alerts.x - 300, alerts.y + 100, { steps: 12 });
    await page.mouse.up();

    expect(await cells(page)).toEqual(before);
  });

  // What leaving edit mode does to the layout the drag produced moved to
  // dashboard-edit.spec.ts in D15. Until then the only exit was a "Done"
  // button that kept the arrangement in memory; now the exits are Cancel,
  // which puts it back, and Save, which stores it -- and both belong with the
  // edit session that decides between them rather than with the geometry.
});

test.describe("Dashboard grid resize", () => {
  test("a widget resizes from its bottom-right corner in whole cells", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");
    const memory = at(before, "d-memory-tile");

    // The CPU tile is three by three, so the cell four columns right and four
    // rows down of its top-left is the corner of a five by five.
    await resizeTo(page, "d-cpu-tile", cornerCell(g, cpu, 4, 4));

    const grown = await cells(page);
    expectBlock(at(grown, "d-cpu-tile"), g, 5, 5, "grown CPU tile");
    // A resize sizes, it does not move: x is the one coordinate the engine
    // promises to leave alone.
    expect(at(grown, "d-cpu-tile").x).toBe(cpu.x);
    // Growing displaces downward only, so the Memory tile beside it gives way
    // without leaving its column.
    expect(at(grown, "d-memory-tile").y).toBeGreaterThan(memory.y);
    expect(at(grown, "d-memory-tile").x).toBe(memory.x);
    expect(overlapping(grown)).toEqual([]);
    expect(grown).toHaveLength(10);
    // The release ended the session.
    await expect(page.locator('[data-resizing="true"]')).toHaveCount(0);
  });

  test("shrinking a widget lets the neighbours it displaced rise again", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");
    const memory = at(before, "d-memory-tile");

    await resizeTo(page, "d-cpu-tile", cornerCell(g, cpu, 4, 4));
    // The grow really did push something down, or the restore below would be
    // certifying nothing.
    expect(at(await cells(page), "d-memory-tile").y).toBeGreaterThan(memory.y);

    // Back to three by three. Gravity is what lifts the Memory tile home: it
    // no longer shares a column with anything above it.
    await resizeTo(page, "d-cpu-tile", cornerCell(g, cpu, 2, 2));

    const shrunk = await cells(page);
    expectBlock(at(shrunk, "d-cpu-tile"), g, 3, 3, "shrunk CPU tile");
    expect(at(shrunk, "d-memory-tile").y).toBe(memory.y);
    // The whole layout comes back, not just the tile that moved: every widget
    // the cascade displaced settles where it started.
    expect(shrunk).toEqual(before);
  });

  test("a resize stops at each widget's own declared minimum", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-active-alerts");
    const g = await geometry(page);
    const alerts = at(before, "d-active-alerts");

    // Dragging a corner to the top-left of the viewport asks for a negative
    // size in both directions -- as far past any minimum as a pointer can get.
    // The clamp is what stops a user producing a widget too small to render
    // its own content.
    await resizeTo(page, "d-active-alerts", { x: 4, y: 4 });

    const clamped = await cells(page);
    // Active Alerts declares minW 2 / minH 3.
    expectBlock(at(clamped, "d-active-alerts"), g, 2, 3, "Active Alerts");
    // However far left the pointer went, the widget did not follow it, and
    // nothing lifted it: resizeItem may change y, and here it must not.
    expect(at(clamped, "d-active-alerts").x).toBe(alerts.x);
    expect(at(clamped, "d-active-alerts").y).toBe(alerts.y);

    // The same gesture on a widget with different minima has to stop
    // somewhere else. One widget alone cannot tell a registry lookup from a
    // pair of constants that happen to match it.
    await resizeTo(page, "d-resource-utilization", { x: 4, y: 4 });

    const after = await cells(page);
    // Resource Utilization declares minW 4 / minH 4.
    expectBlock(at(after, "d-resource-utilization"), g, 4, 4, "Utilization");
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
  });

  test("Escape during a resize puts the layout back", async ({ page }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");

    await corner(page, "d-cpu-tile").hover();
    await page.mouse.down();
    await page.mouse.move(
      cpu.x + cpu.width + 2 * (g.cellWidth + g.gap),
      cpu.y + cpu.height + 2 * (g.rowHeight + g.gap),
      { steps: 12 },
    );
    await expect(
      page.locator('[data-instance-id="d-cpu-tile"]'),
    ).toHaveAttribute("data-resizing", "true");
    // The widget has actually grown: without this the test could certify a
    // restore that had nothing to restore.
    expect(at(await cells(page), "d-cpu-tile").width).toBeGreaterThan(
      cpu.width,
    );

    await page.keyboard.press("Escape");
    await page.mouse.up();

    await expect(page.locator('[data-resizing="true"]')).toHaveCount(0);
    expect(await cells(page)).toEqual(before);
  });

  test("a drag cannot start while a resize is in flight", async ({ page }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");

    await corner(page, "d-cpu-tile").hover();
    await page.mouse.down();
    await page.mouse.move(
      cornerCell(g, cpu, 4, 4).x,
      cornerCell(g, cpu, 4, 4).y,
      { steps: 12 },
    );
    await expect(page.locator('[data-resizing="true"]')).toHaveCount(1);

    // A second finger lands on another widget's drag handle. One session at a
    // time whatever its kind: each holds its own pre-session layout, so a
    // second one could restore over the first one's work.
    await handle(page, "d-nodes").evaluate((el) => {
      el.dispatchEvent(
        new PointerEvent("pointerdown", {
          pointerId: 99,
          isPrimary: false,
          button: 0,
          buttons: 1,
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
    await expect(
      page.locator('[data-instance-id="d-cpu-tile"][data-resizing="true"]'),
    ).toHaveCount(1);
    await page.mouse.up();
  });

  test("a widget gravity lifts mid-resize and keeps its corner on the pointer", async ({
    page,
  }) => {
    await editableDashboard(page);

    // Nothing in the default layout can be lifted by narrowing it: every
    // widget's supports start at or left of its own column, so shortening it
    // from the right never frees it. One resize first sets that up. Cluster
    // Health is 6 rows and declares minH 4; shrinking it to 4 leaves Resource
    // Utilization held at row 6 by the Pods tile alone, through column 6.
    const start = await grabCorner(page, "d-cluster-health");
    const g = await geometry(page);
    await resizeTo(
      page,
      "d-cluster-health",
      cornerCell(g, at(start, "d-cluster-health"), 5, 3),
    );

    const staged = await grabCorner(page, "d-resource-utilization");
    const util = at(staged, "d-resource-utilization");
    // Still where it was: shortening Cluster Health did not move it.
    expect(util.y).toBe(at(start, "d-resource-utilization").y);

    // Now narrow it off column 6. That drops the Pods tile as its support, so
    // gravity lifts it two rows -- and the height being applied was measured
    // from the row it occupied *before* the lift. Without re-aiming inside the
    // same move, this lands 6x4 with its bottom edge two rows above the
    // pointer, and only some later pointermove would have corrected it.
    await resizeTo(page, "d-resource-utilization", cornerCell(g, util, 5, 3));

    const after = await cells(page);
    const lifted = at(after, "d-resource-utilization");
    expect(lifted.y).toBeLessThan(util.y);
    expectBlock(lifted, g, 6, 6, "lifted Resource Utilization");
    expect(lifted.x).toBe(util.x);
    // The plainest statement of the fix: the pointer was on the widget's own
    // bottom row, so the bottom edge has not moved a pixel.
    expect(lifted.y + lifted.height).toBe(util.y + util.height);
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
  });

  test("a widget grows to the grid's last column and no further", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-nodes");
    const g = await geometry(page);
    const nodes = at(before, "d-nodes");

    // Nodes starts at column 0, four wide. Dragging its grip to the right edge
    // of the window -- past the grid's own -- asks for the whole row.
    await resizeTo(page, "d-nodes", {
      x: page.viewportSize()!.width - 2,
      y: nodes.y + nodes.height - 8,
    });

    const after = await cells(page);
    expectBlock(at(after, "d-nodes"), g, 12, 5, "full-width Nodes");
    expect(at(after, "d-nodes").x).toBe(nodes.x);
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
  });

  test("an interrupted pointer puts the layout back mid-resize", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");

    await corner(page, "d-cpu-tile").hover();
    await page.mouse.down();
    const to = cornerCell(g, cpu, 4, 4);
    await page.mouse.move(to.x, to.y, { steps: 12 });
    expect(at(await cells(page), "d-cpu-tile").width).toBeGreaterThan(
      cpu.width,
    );

    const pointerId = await startedPointerId(page);
    await corner(page, "d-cpu-tile").evaluate((el, id) => {
      el.dispatchEvent(
        new PointerEvent("pointercancel", { pointerId: id, bubbles: true }),
      );
    }, pointerId);

    await expect(page.locator('[data-resizing="true"]')).toHaveCount(0);
    expect(await cells(page)).toEqual(before);
    await page.mouse.up();
  });

  test("a resize survives the grip disappearing under it", async ({ page }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-cpu-tile");
    const g = await geometry(page);
    const cpu = at(before, "d-cpu-tile");

    await corner(page, "d-cpu-tile").hover();
    await page.mouse.down();
    const to = cornerCell(g, cpu, 4, 4);
    await page.mouse.move(to.x, to.y, { steps: 12 });
    expect(at(await cells(page), "d-cpu-tile").width).toBeGreaterThan(
      cpu.width,
    );

    // The window narrows under the resize. One column has no width to size,
    // so every handle unmounts -- the grip under the pointer included -- and
    // the session has to end rather than leave the widget stuck mid-resize.
    //
    // D15 rewrote this trigger: it used to press Space on the still-focused
    // "Edit layout" toggle, which the edit toolbar replaced with Cancel and
    // Save. Collapsing the grid is now the only way a user can pull a handle
    // out from under their own pointer.
    await page.setViewportSize({ width: 700, height: 900 });
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "narrow",
    );
    await page.mouse.up();
    await expect(page.locator('[data-resizing="true"]')).toHaveCount(0);

    // Back to a wide grid to read the sizes in the terms the resize used.
    await page.setViewportSize({ width: 1280, height: 900 });
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "wide",
    );
    // Never released, so the size goes back.
    const settled = await cells(page);
    expect(settled).toEqual(before);
    // And the session is really over: a later Escape cannot revert the page.
    await page.keyboard.press("Escape");
    expect(await cells(page)).toEqual(settled);
  });

  test("a resize cannot start while a drag is in flight", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(1);

    // The same guard as the test above, from the other side: a corner grip
    // pressed mid-drag must not open a second session either.
    await corner(page, "d-nodes").evaluate((el) => {
      el.dispatchEvent(
        new PointerEvent("pointerdown", {
          pointerId: 99,
          isPrimary: false,
          button: 0,
          buttons: 1,
          bubbles: true,
          cancelable: true,
        }),
      );
    });

    await expect(page.locator('[data-resizing="true"]')).toHaveCount(0);
    await expect(
      page.locator(
        '[data-instance-id="d-active-alerts"][data-dragging="true"]',
      ),
    ).toHaveCount(1);
    await page.mouse.up();
  });
});

/**
 * Keyboard operation. The mobile app was held to WCAG 2.2 AA in M5 PR-5h, so a
 * dashboard that can only be arranged with a mouse would be a regression
 * against a bar this project already set. These tests are also where the D9
 * review's open question is pinned: edit mode is one tab stop per widget, the
 * two handles are pointer affordances, and the widget's own content is inert
 * while the layout is being arranged.
 */
test.describe("Dashboard grid keyboard", () => {
  /** The instance id of whatever currently has focus, or the element's tag. */
  const focused = (page: Page): Promise<string> =>
    page.evaluate(() => {
      const el = document.activeElement;
      return (
        el?.getAttribute("data-instance-id") ??
        el?.getAttribute("data-testid") ??
        el?.tagName ??
        "none"
      );
    });

  const widget = (page: Page, id: string) =>
    page.locator(`[data-instance-id="${id}"]`);

  test("a widget can be moved with the keyboard alone", async ({ page }) => {
    await editableDashboard(page);

    const before = await cells(page);
    const alerts = at(before, "d-active-alerts");

    await widget(page, "d-active-alerts").focus();
    await page.keyboard.press("ArrowLeft");

    const after = await cells(page);
    expect(at(after, "d-active-alerts").x).toBeLessThan(alerts.x);
    // One cell, not a free-floating pixel offset, and a move is not a resize.
    expect(at(after, "d-active-alerts").width).toBe(alerts.width);
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
  });

  test("a down arrow passes the widget below instead of doing nothing", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await cells(page);
    const cpu = at(before, "d-cpu-tile");
    const pods = at(before, "d-pods-tile");

    // The CPU tile sits directly on the Pods tile, which is the case the
    // engine deliberately absorbs for a pointer drag: a one-row request puts
    // Pods underneath and gravity lifts CPU straight back on top. A key press
    // has no second event to correct that, so this is the difference between
    // `stepItem` and a bare `moveItem` being wired up.
    await widget(page, "d-cpu-tile").focus();
    await page.keyboard.press("ArrowDown");

    const after = await cells(page);
    expect(at(after, "d-cpu-tile").y).toBeGreaterThan(cpu.y);
    expect(at(after, "d-pods-tile").y).toBeLessThan(pods.y);
    // Down is down: neither widget went looking for another column.
    expect(at(after, "d-cpu-tile").x).toBe(cpu.x);
    expect(overlapping(after)).toEqual([]);
  });

  test("shift and an arrow resizes in whole cells", async ({ page }) => {
    await editableDashboard(page);

    const g = await geometry(page);
    const before = await cells(page);
    const cpu = at(before, "d-cpu-tile");

    await widget(page, "d-cpu-tile").focus();
    await page.keyboard.press("Shift+ArrowRight");
    await page.keyboard.press("Shift+ArrowDown");

    // The CPU tile starts three by three.
    const grown = await cells(page);
    expectBlock(at(grown, "d-cpu-tile"), g, 4, 4, "keyboard-grown CPU tile");
    // A resize sizes; x is the coordinate the engine promises to leave alone.
    expect(at(grown, "d-cpu-tile").x).toBe(cpu.x);
    expect(overlapping(grown)).toEqual([]);
  });

  test("a keyboard resize stops at the widget's declared minimum", async ({
    page,
  }) => {
    await editableDashboard(page);

    const g = await geometry(page);
    await widget(page, "d-active-alerts").focus();
    // Active Alerts declares minW 2 / minH 3 and starts 3 by 5, so this asks
    // for far less than it is allowed to be.
    for (let press = 0; press < 6; press++) {
      await page.keyboard.press("Shift+ArrowLeft");
      await page.keyboard.press("Shift+ArrowUp");
    }

    const after = await cells(page);
    expectBlock(at(after, "d-active-alerts"), g, 2, 3, "clamped Active Alerts");
    expect(overlapping(after)).toEqual([]);
    expect(after).toHaveLength(10);
  });

  test("arrow keys never scroll the page out from under the widget", async ({
    page,
  }) => {
    await editableDashboard(page);

    // The dashboard scrolls inside a fixed-height <main>, so that is what an
    // unhandled arrow key would move -- taking the widget being arranged with
    // it.
    const scroll = () =>
      page.evaluate(() => ({
        main: document.querySelector("main")?.scrollTop ?? 0,
        page: globalThis.scrollY,
      }));

    await widget(page, "d-cluster-health").focus();
    const before = await scroll();
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");

    expect(await scroll()).toEqual(before);
  });

  test("edit mode is the widget and its remove button, and nothing else", async ({
    page,
  }) => {
    await editableDashboard(page);

    // The handles are pointer affordances, not buttons: three tab stops per
    // widget where only one of them answered the keyboard was the trade D10
    // reversed.
    await expect(page.getByTestId("drag-handle")).toHaveCount(10);
    expect(
      await page
        .getByTestId("drag-handle")
        .evaluateAll((els) => [...new Set(els.map((el) => el.tagName))]),
    ).toEqual(["DIV"]);

    // Reading order: Cluster Health (0,0), then the CPU tile (6,0). Between
    // them sits exactly one more stop -- the remove control D17 added, which
    // unlike the two handles has no keyboard equivalent on the item and so
    // could not be a pointer-only affordance. Nothing else is focusable: not a
    // handle, not the links inside the health card.
    await widget(page, "d-cluster-health").focus();
    expect(await focused(page)).toBe("d-cluster-health");
    await page.keyboard.press("Tab");
    expect(await focused(page)).toBe("remove-widget");
    await page.keyboard.press("Tab");
    expect(await focused(page)).toBe("d-cpu-tile");
  });

  test("a widget announces which cell it landed in", async ({ page }) => {
    await editableDashboard(page);

    // Active Alerts starts in column 10 of 12 (x = 9), so one step left is 9.
    await widget(page, "d-active-alerts").focus();
    await expect(widget(page, "d-active-alerts")).toHaveAttribute(
      "aria-label",
      /^Active Alerts, column 10 of 12, row \d+, 3 wide, 5 tall$/,
    );

    await page.keyboard.press("ArrowLeft");

    await expect(widget(page, "d-active-alerts")).toHaveAttribute(
      "aria-label",
      /^Active Alerts, column 9 of 12,/,
    );
    // The name alone is not enough: a label that changes under an element that
    // already has focus is not reliably re-read.
    await expect(page.getByTestId("grid-announcement")).toHaveText(
      /^Active Alerts, column 9 of 12,/,
    );
  });

  test("Escape leaves edit mode and hands focus back to the toggle", async ({
    page,
  }) => {
    await editableDashboard(page);

    await widget(page, "d-active-alerts").focus();
    await page.keyboard.press("Escape");

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    // The widget stops being a tab stop the moment edit mode ends, so focus
    // has to land somewhere deliberate rather than on the document.
    expect(await focused(page)).toBe("edit-layout");
  });

  test("outside edit mode a widget is not a tab stop at all", async ({
    page,
  }) => {
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    await expect(
      page.locator('[data-testid="grid-item"][tabindex]'),
    ).toHaveCount(0);
    await expect(
      page.locator('[data-testid="grid-item"][aria-label]'),
    ).toHaveCount(0);
  });

  test("editing suspends the widget's own links and gives them back", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await cells(page);
    const cpu = at(before, "d-cpu-tile");
    // The metric tiles are a link *around* the whole card, so without the
    // content being inert a click anywhere below the title row would navigate
    // away from the layout being arranged.
    const body = { x: cpu.x + 20, y: cpu.y + Math.round(cpu.height / 2) };

    await page.mouse.click(body.x, body.y);
    await expect(page).toHaveURL(/\/$/);

    // Cancel is the way out now that "Done" is gone, and with nothing moved
    // it leaves immediately rather than asking.
    await page.getByTestId("cancel-edit").click();
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    // And the tile is a link again the moment editing ends.
    await page.mouse.click(body.x, body.y);
    await expect(page).toHaveURL(/\/cluster\/nodes$/);
  });

  test("Escape cancels a drag without stealing another widget's focus", async ({
    page,
  }) => {
    await editableDashboard(page);

    // Focus one widget, then drag a different one. The session suppresses the
    // press that would move focus, so the CPU tile keeps it throughout.
    await widget(page, "d-cpu-tile").focus();
    // Measured through `grab`, after the hover, for the reason that helper
    // gives: reaching a widget near the bottom scrolls the page, which moves
    // every cell this test then compares.
    const before = await grab(page, "d-active-alerts");
    const health = at(before, "d-cluster-health");
    expect(await focused(page)).toBe("d-cpu-tile");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(1);

    await page.keyboard.press("Escape");
    await page.mouse.up();

    // The session's own Escape cancels the drag, so the layout goes back --
    // the grid consumed the key without stopping it reaching that listener.
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
    expect(await cells(page)).toEqual(before);
    // And the widget that was never part of the drag still has focus. The
    // global shortcut handler blurs whatever is focused on Escape, so without
    // the grid marking the key handled this lands on the document body.
    expect(await focused(page)).toBe("d-cpu-tile");
    // A drag cancel is not a way out of edit mode.
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
  });

  test("an arrow key during a drag cannot scroll the grid away", async ({
    page,
  }) => {
    await editableDashboard(page);

    const scroll = () =>
      page.evaluate(() => ({
        main: document.querySelector("main")?.scrollTop ?? 0,
        page: globalThis.scrollY,
      }));

    await widget(page, "d-cpu-tile").focus();
    const before = await cells(page);
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    const scrolled = await scroll();

    // The session owns the keyboard, so the arrow moves nothing -- but it must
    // still be swallowed. The session re-measures the grid from its bounding
    // rect on every pointermove, so a scroll here would drop the widget in a
    // different cell with the pointer standing still.
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");

    expect(await scroll()).toEqual(scrolled);
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(1);
    await page.mouse.up();
  });

  test("a modified arrow key is left to the browser", async ({ page }) => {
    await editableDashboard(page);

    // Focus first, then measure: focusing a widget near the bottom scrolls it
    // into view, which moves every cell this test compares.
    await widget(page, "d-active-alerts").focus();
    const before = await cells(page);

    // Ctrl, Alt and Meta with an arrow belong to the browser and the window
    // manager. Swallowing them would take back-navigation away from anyone
    // who happened to be tabbed onto a widget.
    await page.keyboard.press("Control+ArrowLeft");
    await page.keyboard.press("Alt+ArrowRight");

    expect(await cells(page)).toEqual(before);
  });
});

test.describe("layout load gating", () => {
  // The Edit affordance is withheld until the stored layout has landed. That
  // gate exists because adopting a stored layout re-mounts the grid, which
  // discards the private working copy a drag lives in -- so editing during the
  // load window is the one case where offering the button destroys work rather
  // than merely wasting it. The protection is otherwise untested at any level,
  // and a regression would silently reopen exactly that bug.
  test("Edit is withheld until the stored layout lands", async ({ page }) => {
    let release: (() => void) | null = null;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });

    await page.route(
      "**/api/v1/preferences/layouts/overview",
      async (route) => {
        await held;
        await route.fulfill({ status: 204, body: "" });
      },
    );

    await page.goto("/");

    const edit = page.getByTestId("edit-layout");
    await expect(edit).toBeDisabled();
    await expect(edit).toHaveAttribute("title", "Loading your saved layout...");

    release?.();

    await expect(edit).toBeEnabled();
    await expect(edit).not.toHaveAttribute(
      "title",
      "Loading your saved layout...",
    );
  });

  test("a layout the server cannot supply leaves the grid read-only", async ({
    page,
  }) => {
    await page.route("**/api/v1/preferences/layouts/overview", (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: 503,
            message: "no database",
            reason: "database_unavailable",
          },
        }),
      }),
    );

    await page.goto("/");

    // The default dashboard still renders -- a failed load must not look like
    // an empty one -- but arranging it would be work the user loses on reload.
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await expect(page.getByTestId("edit-layout")).toBeDisabled();
  });
});
