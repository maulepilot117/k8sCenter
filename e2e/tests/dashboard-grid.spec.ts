import type { Page } from "@playwright/test";
import { test, expect } from "../fixtures/base.ts";

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
  // Record the id of whichever pointer starts the next drag, so a test that
  // has to name it does not have to guess.
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
  return await cells(page);
}

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
 * The live grid's cell geometry in CSS pixels, so a test can express a resize
 * as "two columns wider" rather than as a pixel count that only holds at one
 * viewport size. Mirrors `metricsFrom` in frontend/lib/dashboard/grid.ts; the
 * constants are DASHBOARD_COLUMNS, _GRID_GAP and _ROW_HEIGHT from
 * frontend/lib/dashboard/types.ts, which this project cannot import.
 */
async function geometry(page: Page) {
  const width = await page
    .getByTestId("dashboard-grid")
    .evaluate((el) => el.getBoundingClientRect().width);
  const columns = 12;
  const gap = 16;
  const rowHeight = 40;
  return {
    gap,
    rowHeight,
    cellWidth: (width - gap * (columns - 1)) / columns,
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
 * Within half a cell, not to the pixel: bounding boxes round to whole pixels,
 * a column is a fraction of the container, and a widget growing can add the
 * page's scroll bar, which moves every column edge a little. Half a cell still
 * tells six columns from five or seven, which is what these tests are about.
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
      page.locator('[data-instance-id="d-active-alerts"][data-dragging="true"]'),
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
    const pointerId = await page.evaluate(
      () => (globalThis as unknown as { __pointerId: number }).__pointerId,
    );
    await handle(page, "d-active-alerts").evaluate((el, id) => {
      el.dispatchEvent(
        new PointerEvent("pointercancel", { pointerId: id, bubbles: true }),
      );
    }, pointerId);

    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);
    expect(await cells(page)).toEqual(before);
    await page.mouse.up();
  });

  test("a drag survives the handle disappearing under it", async ({ page }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    expect(at(await cells(page), "d-active-alerts").x).not.toBe(alerts.x);

    // "Edit layout" still has focus, so Space toggles edit mode off and every
    // handle unmounts mid-drag -- including the one the pointer is captured
    // by. The session must still end rather than leave the grid stuck.
    await page.keyboard.press("Space");
    await page.mouse.up();

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(page.locator('[data-dragging="true"]')).toHaveCount(0);

    // The user never dropped it, so the layout goes back. Without this the
    // test would also pass if the drag committed where it happened to be.
    const settled = await cells(page);
    expect(settled).toEqual(before);

    // And the session is really over: a later Escape cannot revert the page.
    await page.keyboard.press("Escape");
    expect(await cells(page)).toEqual(settled);
  });

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
      page.locator('[data-instance-id="d-active-alerts"][data-dragging="true"]'),
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

  test("one column offers no handles to drag", async ({ page }) => {
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

  test("leaving edit mode keeps the layout the drag produced", async ({
    page,
  }) => {
    await editableDashboard(page);

    const before = await grab(page, "d-active-alerts");
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");
    await dragTo(page, "d-active-alerts", {
      x: health.x + 40,
      y: alerts.y + 8,
    });
    const moved = await cells(page);
    // The drag produced a different layout, so "kept" below means something.
    expect(at(moved, "d-active-alerts").x).not.toBe(alerts.x);

    // "Done" ends editing; it is not a cancel. Persistence is P3, so this
    // layout lives until reload.
    await page.getByTestId("edit-layout").click();
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);
    expect(await cells(page)).toEqual(moved);
  });
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

    // The CPU tile is three columns by three rows. Two cells right and two
    // rows down makes it five by five.
    await resizeTo(page, "d-cpu-tile", {
      x: cpu.x + cpu.width + 2 * (g.cellWidth + g.gap),
      y: cpu.y + cpu.height + 2 * (g.rowHeight + g.gap),
    });

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

    // Back to three by three: the corner goes to the cell it started on. What
    // the grow pushed down, gravity lifts again.
    await resizeTo(page, "d-cpu-tile", {
      x: cpu.x + cpu.width - 8,
      y: cpu.y + cpu.height - 8,
    });

    const shrunk = await cells(page);
    expectBlock(at(shrunk, "d-cpu-tile"), g, 3, 3, "shrunk CPU tile");
    expect(at(shrunk, "d-memory-tile").y).toBe(memory.y);
    expect(shrunk).toEqual(before);
  });

  test("a resize stops at the widget's declared minimum", async ({ page }) => {
    await editableDashboard(page);

    const before = await grabCorner(page, "d-active-alerts");
    const g = await geometry(page);
    const alerts = at(before, "d-active-alerts");

    // Active Alerts is three by five and declares minW 2 / minH 3. Dragging
    // its corner to the top-left of the viewport asks for a negative size in
    // both directions -- as far past the minimum as a pointer can get. The
    // clamp is what stops a user producing a widget too small to render its
    // own content, and it comes from the registry, not from the island.
    await resizeTo(page, "d-active-alerts", { x: 4, y: 4 });

    const after = await cells(page);
    expectBlock(at(after, "d-active-alerts"), g, 2, 3, "clamped Active Alerts");
    // However far left the pointer went, the widget did not follow it.
    expect(at(after, "d-active-alerts").x).toBe(alerts.x);
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
      cpu.x + cpu.width + 2 * (g.cellWidth + g.gap),
      cpu.y + cpu.height,
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
    await expect(page.locator('[data-resizing="true"]')).toHaveCount(1);
    await page.mouse.up();
  });
});
