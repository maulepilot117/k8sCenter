import type { Page } from "@playwright/test";
import { test, expect } from "../fixtures/base.ts";

// Drag behaviour on the dashboard's snapping grid. The geometry itself is unit
// tested in frontend/lib/dashboard/grid_test.ts; these tests prove the island
// routes the pointer through that engine instead of positioning widgets
// itself, and that the pointer session starts and ends where it should.

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

/** Drags `id` by its handle to a point, in steps, so pointermove fires. */
async function dragTo(page: Page, id: string, to: { x: number; y: number }) {
  await handle(page, id).hover();
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
}

test.describe("Dashboard grid drag", () => {
  test("a widget follows the pointer into a new cell", async ({ page }) => {
    await editableDashboard(page);

    const before = await cells(page);
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
  });

  test("a drag never leaves two widgets on the same cell", async ({ page }) => {
    await editableDashboard(page);

    const before = await cells(page);
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

    const before = await cells(page);
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");

    await handle(page, "d-active-alerts").hover();
    await page.mouse.down();
    await page.mouse.move(health.x + 40, health.y + 40, { steps: 12 });
    // Mid-drag, so the layout has already moved under the pointer.
    await expect(
      page.locator('[data-instance-id="d-active-alerts"]'),
    ).toHaveAttribute("data-dragging", "true");
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

  test("outside edit mode there is nothing to drag", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);

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

    const before = await cells(page);
    const alerts = at(before, "d-active-alerts");
    const health = at(before, "d-cluster-health");
    await dragTo(page, "d-active-alerts", {
      x: health.x + 40,
      y: alerts.y + 8,
    });
    const moved = await cells(page);

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
