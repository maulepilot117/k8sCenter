import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";
import {
  LAYOUT_LIST_URL,
  listedRecord,
  stubLayoutStore,
} from "./dashboard-layout-stub.ts";

// "Copy from another cluster": which layouts are offered, what taking one puts
// on screen, and what it writes.
//
// Which records are offerable is unit tested in
// frontend/lib/dashboard/layout-store_test.ts (`copyableLayouts`); these prove
// the editor reads that answer, that the control is absent when the answer is
// empty, and that a copy is an ordinary edit -- undone by Cancel, written only
// by Save.

/** What this cluster starts with: one widget, so a copy is unmistakable. */
const MINE: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [
    { instanceId: "mine-health", id: "cluster-health", x: 0, y: 0, w: 6, h: 6 },
  ],
};

/** What the other cluster has: three widgets, none of them this one's. */
const THEIRS: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [
    { instanceId: "theirs-nodes", id: "nodes", x: 0, y: 0, w: 4, h: 5 },
    { instanceId: "theirs-cpu", id: "cpu-tile", x: 4, y: 0, w: 3, h: 3 },
    { instanceId: "theirs-pods", id: "pods-tile", x: 7, y: 0, w: 3, h: 3 },
  ],
};

const dialog = (page: Page) => page.getByTestId("layout-copy-dialog");
const rows = (page: Page) => page.getByTestId("copy-layout-option");

/**
 * Loads this cluster's dashboard with `elsewhere` available to copy from, and
 * enters edit mode.
 *
 * Returns the writes the stubbed store saw, so a test can assert what a save
 * actually sent rather than only what the grid shows.
 */
async function editableDashboard(page: Page, elsewhere: unknown[]) {
  const writes = await stubLayoutStore(
    page,
    { revision: 4, config: MINE },
    elsewhere,
  );
  await page.goto("/");
  await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(
    MINE.items.length,
  );
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-mode",
    "wide",
  );

  await page.getByTestId("edit-layout").click();
  await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
    "data-grid-editable",
    "true",
  );
  return writes;
}

test.describe("Copying a dashboard layout from another cluster", () => {
  test("the control is absent when there is nothing to copy", async ({
    page,
  }) => {
    await editableDashboard(page, []);

    // Absent rather than disabled: a button that is visible in every
    // screenshot and opens an empty list is a worse promise than one that has
    // not shipped.
    await expect(page.getByTestId("copy-layout")).toHaveCount(0);
  });

  test("this cluster's own layout is not something to copy from", async ({
    page,
  }) => {
    // The listing spans clusters, so it carries this one's record too. It is
    // what is already on screen, which makes it the one row that could never
    // do anything.
    await editableDashboard(page, [listedRecord("local", MINE)]);

    await expect(page.getByTestId("copy-layout")).toHaveCount(0);
  });

  test("another cluster's layout is offered, and named", async ({ page }) => {
    await editableDashboard(page, [listedRecord("prod-east", THEIRS)]);

    await page.getByTestId("copy-layout").click();
    await expect(dialog(page)).toBeVisible();
    await expect(rows(page)).toHaveCount(1);

    const row = rows(page).first();
    await expect(row).toHaveAttribute("data-cluster-id", "prod-east");
    // The cluster and the size are what tell two rows apart.
    await expect(row).toContainText("prod-east");
    await expect(row).toContainText("3 widgets");
  });

  test("rows are named by the cluster's label, not its id", async ({ page }) => {
    // The shape a real deployment has and the fixtures above do not: a remote
    // cluster's id is 16 random bytes in hex, and the operator's name for it
    // is the only part a user recognises. D17 shipped rendering the id, which
    // every spec here missed because every fixture used a readable one.
    const opaque = "9f3c1d7a4b28e5f06c91ab3de742c1e1";
    await editableDashboard(page, [
      listedRecord(opaque, THEIRS, { clusterLabel: "Production (EU)" }),
    ]);

    await page.getByTestId("copy-layout").click();
    const row = rows(page).first();

    await expect(row).toContainText("Production (EU)");
    await expect(row).not.toContainText(opaque);
    // The id stays on the element: it is what the test suite and any future
    // action address the row by, and it is not what the user reads.
    await expect(row).toHaveAttribute("data-cluster-id", opaque);
  });

  test("a cluster the registry cannot name falls back to its id", async ({
    page,
  }) => {
    // Deregistered between the save and now, or a build with no registry at
    // all. The id is then the only honest label left, and a row with no name
    // would be worse than one named awkwardly.
    const opaque = "0a1b2c3d4e5f60718293a4b5c6d7e8f9";
    await editableDashboard(page, [listedRecord(opaque, THEIRS)]);

    await page.getByTestId("copy-layout").click();
    await expect(rows(page).first()).toContainText(opaque);
  });

  test("two clusters are told apart by their labels", async ({ page }) => {
    // The case the single-row fixtures could never exercise: choosing. A
    // dialog that offers two rows the user cannot tell apart is the failure
    // this affordance exists to avoid.
    await editableDashboard(page, [
      listedRecord("11111111111111111111111111111111", THEIRS, {
        clusterLabel: "Staging",
      }),
      listedRecord("22222222222222222222222222222222", THEIRS, {
        clusterLabel: "Production (EU)",
      }),
    ]);

    await page.getByTestId("copy-layout").click();
    await expect(rows(page)).toHaveCount(2);
    await expect(rows(page).nth(0)).toContainText("Staging");
    await expect(rows(page).nth(1)).toContainText("Production (EU)");
  });

  test("taking a layout replaces the one being edited", async ({ page }) => {
    await editableDashboard(page, [listedRecord("prod-east", THEIRS)]);

    await page.getByTestId("copy-layout").click();
    await rows(page).first().click();

    await expect(dialog(page)).toHaveCount(0);
    // A replacement, not a merge: the rows offer whole dashboards, and folding
    // one into what is on screen would produce an arrangement neither cluster
    // has.
    await expect(page.getByTestId("grid-item")).toHaveCount(
      THEIRS.items.length,
    );
    await expect(page.locator('[data-instance-id="mine-health"]')).toHaveCount(
      0,
    );
    await expect(page.getByTestId("save-layout")).toBeEnabled();
    // The keyboard comes back to the control that opened the dialog.
    await expect(page.getByTestId("copy-layout")).toBeFocused();
  });

  test("a copy writes nothing until Save, and Cancel undoes it", async ({
    page,
  }) => {
    const writes = await editableDashboard(page, [
      listedRecord("prod-east", THEIRS),
    ]);

    await page.getByTestId("copy-layout").click();
    await rows(page).first().click();
    expect(writes).toHaveLength(0);

    await page.getByTestId("cancel-edit").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Discard changes" })
      .click();

    // Back to this cluster's own layout: a copy is an ordinary edit, and the
    // one thing that makes it permanent is Save.
    await expect(page.getByTestId("grid-item")).toHaveCount(MINE.items.length);
    expect(writes).toHaveLength(0);
  });

  test("saving a copy writes it under this cluster", async ({ page }) => {
    const writes = await editableDashboard(page, [
      listedRecord("prod-east", THEIRS),
    ]);

    await page.getByTestId("copy-layout").click();
    await rows(page).first().click();
    await page.getByTestId("save-layout").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    // An ordinary save under the current cluster -- there is no copy endpoint,
    // and the request assertions in the stub are what prove it is the same
    // write any other edit makes.
    expect(writes).toHaveLength(1);
    expect(writes[0].config.items.map((i) => i.id).sort()).toEqual(
      THEIRS.items.map((i) => i.id).sort(),
    );
  });

  test("a layout carrying a widget this build lacks is offered short, and says so", async ({
    page,
  }) => {
    const withGhost: DashboardLayoutConfig = {
      ...THEIRS,
      items: [
        ...THEIRS.items,
        // A layout stored by a newer build is a legitimate thing for an older
        // one to receive; it is the read direction that has to be forgiving.
        { instanceId: "theirs-ghost", id: "from-the-future", x: 0, y: 5, w: 4, h: 4 },
      ],
    };
    await editableDashboard(page, [listedRecord("prod-east", withGhost)]);

    await page.getByTestId("copy-layout").click();
    const row = rows(page).first();
    // Counted and explained before the copy, not after: a warning shown once
    // the layout is on screen reads as an error the user caused.
    await expect(row).toContainText("3 widgets");
    await expect(row).toContainText("from-the-future");

    await row.click();
    await expect(page.getByTestId("grid-item")).toHaveCount(
      THEIRS.items.length,
    );
  });

  test("the dialog closes to a real control when its list empties", async ({
    page,
  }) => {
    // The one transition that takes the dialog off screen without anybody
    // closing it. `startEditing` clears the open flag on every Edit press but
    // not the module-level record list, and fires the read for the new
    // session without awaiting it -- so a second session can open the dialog
    // on the first session's answer and then have the new one land narrower.
    // A layout deleted from another tab or device between the two is enough.
    //
    // The dialog going is right; leaving the user with no dialog and no focus
    // is not. And the button focus would normally return to is gated on the
    // same emptied list, so it has unmounted in the same render -- which is
    // why the fallback has to be a control that outlives the whole session.
    await editableDashboard(page, [listedRecord("prod-east", THEIRS)]);

    let releaseSecond: () => void = () => {};
    const secondAsked = new Promise<void>((resolve) => {
      releaseSecond = resolve;
    });
    // Added after the stub's own route, so this one wins -- and after the
    // first session's read has already been served, so every request it sees
    // is the second session's. Held open until the dialog is up.
    await page.route(LAYOUT_LIST_URL, async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await secondAsked;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: [], metadata: { total: 0 } }),
      });
    });

    // Out of the first session and into a second, whose read is held open.
    await page.getByTestId("cancel-edit").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();
    await page.getByTestId("edit-layout").click();

    // Still offered, on the first session's answer.
    await page.getByTestId("copy-layout").click();
    await expect(dialog(page)).toBeVisible();

    // Now the new answer lands, and there is nothing left to offer.
    releaseSecond();
    await expect(dialog(page)).toHaveCount(0);
    await expect(page.getByTestId("copy-layout")).toHaveCount(0);
    await expect(page.getByTestId("add-widget")).toBeFocused();
  });

  test("the dialog behaves like a dialog", async ({ page }) => {
    await editableDashboard(page, [listedRecord("prod-east", THEIRS)]);

    await page.getByTestId("copy-layout").click();
    await expect(dialog(page)).toHaveAttribute("aria-modal", "true");
    // Focus goes to the first row on open: every row does something, and Close
    // is the one control the user did not press a button to reach.
    await expect(rows(page).first()).toBeFocused();

    // Escape is a way out, and it must not also leave edit mode -- the grid's
    // own Escape does that, and this dialog stops the key before it gets
    // there.
    await page.keyboard.press("Escape");
    await expect(dialog(page)).toHaveCount(0);
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
    await expect(page.getByTestId("copy-layout")).toBeFocused();

    // Tab stays inside. The ring is the rows plus Close, so enough tabs to
    // pass Close must come back round rather than reach the page behind.
    await page.getByTestId("copy-layout").click();
    await page.keyboard.press("Tab");
    await page.keyboard.press("Tab");
    await expect(dialog(page).locator(":focus")).toHaveCount(1);
  });

  test("a layout on a dashboard this build does not serve is not offered", async ({
    page,
  }) => {
    // Every layout the listing returns comes back, including ones addressed to
    // a scope this build has no dashboard for. Offering one would put widgets
    // from another screen onto this one.
    const otherScope = { ...THEIRS, scope: "workloads" };
    await editableDashboard(page, [listedRecord("prod-east", otherScope)]);

    await expect(page.getByTestId("copy-layout")).toHaveCount(0);
  });
});
