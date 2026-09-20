import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";
// The grid's own geometry, imported rather than copied, exactly as
// dashboard-grid.spec.ts does: a change to the column count or the gap has to
// break these drags loudly instead of making them aim at the wrong cell.
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_GRID_GAP,
} from "../../frontend/lib/dashboard/types.ts";
// The stubbed layout endpoint, shared with dashboard-palette.spec.ts. Its
// header explains why these specs mock a store the E2E stack really runs.
import {
  json,
  LAYOUT_URL,
  preferenceError,
  record,
  saveRequestBody,
  stubLayoutList,
  stubLayoutStore,
} from "./dashboard-layout-stub.ts";

// The dashboard editor: entering edit mode, whether Save is offered, what
// Cancel puts back, and what a conflicting save does. The state machine behind
// all four is unit tested in frontend/lib/dashboard/edit-session_test.ts;
// these prove the island routes the user's gestures through it, and that the
// save path talks to the server the way the endpoint expects.

/** The widget the geometry specs also drive: it starts in column 10 of 12. */
const SUBJECT = "d-active-alerts";

const widget = (page: Page, id: string) =>
  page.locator(`[data-instance-id="${id}"]`);

/**
 * The column a widget reports in its accessible name, 1-based.
 *
 * Only meaningful in edit mode: outside it a widget carries no name at all,
 * which is itself asserted over in dashboard-grid.spec.ts.
 */
async function column(page: Page, id: string): Promise<number> {
  const label = await widget(page, id).getAttribute("aria-label");
  const m = /column (\d+) of/.exec(label ?? "");
  if (m === null) throw new Error(`no column in aria-label: ${label}`);
  return Number(m[1]);
}

/** Waits for a widget to report the column it was asked to move to. */
function expectColumn(page: Page, id: string, col: number) {
  return expect(widget(page, id)).toHaveAttribute(
    "aria-label",
    new RegExp(`column ${col} of`),
  );
}

/**
 * What Save's title says once a write has been refused. Mirrors
 * SAVE_BLOCKED_REASON in DashboardV2.tsx, copied rather than imported: that
 * module is a Preact island and importing it would pull the whole rendering
 * stack into the Playwright runtime for one string.
 */
const SAVE_BLOCKED_REASON =
  "This dashboard has to be reloaded before it can be saved again.";

/**
 * A store that refuses the first write as a conflict, and whose reads return
 * the layout that won once it has.
 *
 * `theirs` places the subject widget in column 1, which no test moves it to,
 * so "took theirs" and "kept mine" cannot be confused for one another.
 */
async function conflictingStore(page: Page) {
  const theirs: DashboardLayoutConfig = {
    schemaVersion: 1,
    scope: "overview",
    columns: 12,
    items: [
      { instanceId: SUBJECT, id: "active-alerts", x: 0, y: 0, w: 3, h: 5 },
    ],
  };
  let conflicted = false;

  // Entering edit mode reads it, and this store does not go through
  // `stubLayoutStore`, which would otherwise have registered it.
  await stubLayoutList(page);

  await page.route(LAYOUT_URL, async (route) => {
    if (route.request().method() === "GET") {
      // Nothing stored when the page first loads; once the write has been
      // refused, the read returns what the other tab saved.
      if (!conflicted) {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      await json(route, 200, { data: record(7, theirs) });
      return;
    }
    // Validated before the canned refusal: a conflict response handed back for
    // a malformed request would let a broken client pass these tests.
    saveRequestBody(route);
    conflicted = true;
    await preferenceError(route, 409, "revision_conflict");
  });
}

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
 * Moves the subject widget one column with the keyboard.
 *
 * The keyboard rather than a drag: this file is about what the editor does
 * with a change, and the pointer path has a spec of its own. A key press is
 * also exact, which is what lets a test move a widget back to where it started
 * and assert that Save disarms.
 */
async function nudge(page: Page, key: "ArrowLeft" | "ArrowRight") {
  const before = await column(page, SUBJECT);
  await widget(page, SUBJECT).focus();
  await page.keyboard.press(key);
  const after = before + (key === "ArrowLeft" ? -1 : 1);
  await expectColumn(page, SUBJECT, after);
  return after;
}

/** The drag handle of a placed widget: its card's title row. */
const handle = (page: Page, id: string) =>
  page.locator(`[data-instance-id="${id}"] [data-testid="drag-handle"]`);

/**
 * Where the subject widget sits in the shipped default, 1-based.
 *
 * Stated rather than read off the page, because it is what a Reset test
 * asserts the layout came back to -- reading it from the grid after the reset
 * would compare the grid to itself. It is the same number the comment on
 * `SUBJECT` above already names, and `DEFAULT_OVERVIEW_LAYOUT` is where it
 * comes from; a change there fails this loudly rather than silently.
 */
const DEFAULT_SUBJECT_COLUMN = 10;

/**
 * A stored layout nowhere near the shipped default: one widget, in the first
 * column. Both Reset tests that need a baseline other than the default use it.
 */
const STORED_ONE_WIDGET: DashboardLayoutConfig = {
  schemaVersion: 1,
  scope: "overview",
  columns: 12,
  items: [{ instanceId: SUBJECT, id: "active-alerts", x: 0, y: 0, w: 3, h: 5 }],
};

/**
 * Removes a placed widget through its header control.
 *
 * The pointer rather than the keyboard, because the control is a pointer
 * affordance first; that it is also a tab stop is asserted where focus is the
 * subject.
 */
async function removeWidget(page: Page, id: string) {
  await widget(page, id).getByTestId("remove-widget").click();
}

/**
 * Drags the subject widget sideways by whole columns and asserts it landed.
 *
 * The pointer rather than the keyboard, because the keyboard is the only path
 * the rest of this file exercises and it is not the path most users take:
 * `onChange` is wired separately from the drag (DashboardGrid.tsx), the resize
 * and the keyboard, so a regression in the pointer wiring would otherwise ship
 * with every spec here green.
 */
async function dragColumns(page: Page, id: string, deltaColumns: number) {
  const before = await column(page, id);
  // Hover first, and measure only afterwards. The subject widget sits in the
  // bottom row, so reaching it scrolls the page -- and a box captured before
  // that scroll points at where the widget used to be, which makes the drag
  // land on nothing and the test pass without moving anything.
  await handle(page, id).hover();
  const grid = await page.getByTestId("dashboard-grid").boundingBox();
  if (grid === null) throw new Error("grid has no box");
  // `repeat(DASHBOARD_COLUMNS, minmax(0, 1fr))` with a fixed gap, so one
  // column step is a track plus a gap.
  const track =
    (grid.width - DASHBOARD_GRID_GAP * (DASHBOARD_COLUMNS - 1)) /
    DASHBOARD_COLUMNS;
  const step = track + DASHBOARD_GRID_GAP;

  const grip = await handle(page, id).boundingBox();
  if (grip === null) throw new Error("drag handle has no box");
  const from = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };

  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(from.x + step * deltaColumns, from.y, { steps: 12 });
  await page.mouse.up();

  const after = before + deltaColumns;
  await expectColumn(page, id, after);
  return after;
}

test.describe("Dashboard edit mode", () => {
  test("Save is withheld until the layout actually changes", async ({
    page,
  }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    const save = page.getByTestId("save-layout");
    // Nothing has moved, so there is nothing to write. An enabled Save that
    // writes nothing trains people to ignore the one that does.
    await expect(save).toBeDisabled();

    await nudge(page, "ArrowLeft");
    await expect(save).toBeEnabled();

    // Moved away and back again: the layout is the one that was loaded, so the
    // affordance goes away with it.
    await nudge(page, "ArrowRight");
    await expect(save).toBeDisabled();
  });

  test("a pointer drag arms Save, and dragging back disarms it", async ({
    page,
  }) => {
    // The drag twin of the keyboard test above. `onChange` is fired from three
    // separate call sites in DashboardGrid -- drag, resize and keyboard -- and
    // only the keyboard one was covered, so a drag that moved a widget on
    // screen without telling the session would have shipped green.
    await stubLayoutStore(page);
    await editableDashboard(page);

    const save = page.getByTestId("save-layout");
    await expect(save).toBeDisabled();

    const start = await column(page, SUBJECT);
    await dragColumns(page, SUBJECT, -1);
    await expect(save).toBeEnabled();

    // Back where it started: the layout is the one that was loaded, so the
    // affordance goes away with it.
    await dragColumns(page, SUBJECT, 1);
    await expectColumn(page, SUBJECT, start);
    await expect(save).toBeDisabled();
  });

  test("a drag cancelled mid-gesture leaves Save disarmed", async ({ page }) => {
    // Escape during a drag restores the pre-drag layout through the same
    // `setItems(before)` the grid uses for every interrupted session. That
    // restore has to reach the edit session too, or Save stays armed over a
    // phantom position the user never dropped and a later click writes it.
    await stubLayoutStore(page);
    await editableDashboard(page);

    const save = page.getByTestId("save-layout");
    const start = await column(page, SUBJECT);

    // Hover before measuring, for the reason `dragColumns` gives.
    await handle(page, SUBJECT).hover();
    const grip = await handle(page, SUBJECT).boundingBox();
    if (grip === null) throw new Error("drag handle has no box");
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(grip.x - 200, grip.y + 60, { steps: 12 });

    // The gesture has to have moved something, or the restore below proves
    // nothing and this test would pass on a drag that never happened.
    await expect(widget(page, SUBJECT)).not.toHaveAttribute(
      "aria-label",
      new RegExp(`column ${start} of`),
    );
    await expect(save).toBeEnabled();

    await page.keyboard.press("Escape");
    await page.mouse.up();

    await expectColumn(page, SUBJECT, start);
    await expect(save).toBeDisabled();
  });

  test("the grid is frozen while a save is in flight", async ({ page }) => {
    // The window this guards: `commit()` snapshots the payload before the
    // await, so an edit made during the round trip reaches the session but
    // never the server -- and the success path then clears the session and
    // reports "saved" over an arrangement that was never written.
    let releasePut: (() => void) | null = null;
    const held = new Promise<void>((resolve) => {
      releasePut = resolve;
    });
    const writes: { revision: number; config: DashboardLayoutConfig }[] = [];

    await page.route(LAYOUT_URL, async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      const body = saveRequestBody(route);
      writes.push(body);
      await held;
      await json(route, 200, { data: record(body.revision + 1, body.config) });
    });

    await editableDashboard(page);
    const moved = await nudge(page, "ArrowLeft");
    await page.getByTestId("save-layout").click();

    // The arrangement stops being arrangeable for the duration of the write,
    // the same way Cancel and Save are already held.
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await expect(page.getByTestId("drag-handle")).toHaveCount(0);

    // A keystroke aimed at the widget now does nothing, so nothing can diverge
    // from the payload already in flight.
    await page.keyboard.press("ArrowLeft");
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );

    releasePut?.();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    // What was written is what the user was looking at when they pressed Save.
    expect(writes).toHaveLength(1);
    expect(
      writes[0].config.items.find((i) => i.instanceId === SUBJECT)?.x,
    ).toBe(moved - 1);

    // And the layout on screen still matches it once editing reopens.
    await page.getByTestId("edit-layout").click();
    await expectColumn(page, SUBJECT, moved);
  });

  test("Edit is withheld while the conflict reload is in flight", async ({
    page,
  }) => {
    // Taking the stored layout after a conflict starts a second load, and
    // `layoutLoaded` never returns to false once set -- so the first-load gate
    // does not cover this one. Without its own gate the user can reopen
    // editing during the GET and have that work re-mounted away when the
    // response lands.
    const theirs: DashboardLayoutConfig = {
      schemaVersion: 1,
      scope: "overview",
      columns: 12,
      items: [
        { instanceId: SUBJECT, id: "active-alerts", x: 0, y: 0, w: 3, h: 5 },
      ],
    };
    let conflicted = false;
    let releaseGet: (() => void) | null = null;
    const heldGet = new Promise<void>((resolve) => {
      releaseGet = resolve;
    });

    await page.route(LAYOUT_URL, async (route) => {
      if (route.request().method() === "GET") {
        if (!conflicted) {
          await route.fulfill({ status: 204, body: "" });
          return;
        }
        await heldGet;
        await json(route, 200, { data: record(7, theirs) });
        return;
      }
      saveRequestBody(route);
      conflicted = true;
      await preferenceError(route, 409, "revision_conflict");
    });

    await editableDashboard(page);
    await nudge(page, "ArrowLeft");
    await page.getByTestId("save-layout").click();

    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Load the saved layout" })
      .click();

    // The session is closed, and Edit must not be offered again until the
    // replacement layout is actually on the grid.
    const edit = page.getByTestId("edit-layout");
    await expect(edit).toBeDisabled();
    await expect(edit).toHaveAttribute("title", "Loading your saved layout...");

    releaseGet?.();
    await expect(edit).toBeEnabled();

    // And what landed is the other tab's layout, not this session's edits.
    await edit.click();
    await expectColumn(page, SUBJECT, 1);
  });

  test("Cancel with nothing moved leaves without asking", async ({ page }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    await page.getByTestId("cancel-edit").click();

    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    // The widget that had focus stops being a tab stop the moment editing
    // ends, so focus has to land somewhere deliberate.
    await expect(page.getByTestId("edit-layout")).toBeFocused();
  });

  test("Cancel after a change asks first, and restores the layout as loaded", async ({
    page,
  }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    const start = await column(page, SUBJECT);
    await nudge(page, "ArrowLeft");

    await page.getByTestId("cancel-edit").click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Discard unsaved changes?");

    await dialog.getByRole("button", { name: "Discard changes" }).click();

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    // Back where it was loaded -- the grid is re-mounted on the session's
    // baseline rather than left holding the working copy. Re-entering edit
    // mode is what makes the placement readable again.
    await page.getByTestId("edit-layout").click();
    await expectColumn(page, SUBJECT, start);
  });

  test("keeping the changes leaves the editor exactly as it was", async ({
    page,
  }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    await nudge(page, "ArrowLeft");
    const moved = await column(page, SUBJECT);

    await page.getByTestId("cancel-edit").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Cancel" })
      .click();

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
    expect(await column(page, SUBJECT)).toBe(moved);
    await expect(page.getByTestId("save-layout")).toBeEnabled();
  });

  test("a saved layout is what the next load renders", async ({ page }) => {
    const writes = await stubLayoutStore(page);
    await editableDashboard(page);

    await nudge(page, "ArrowLeft");
    const moved = await column(page, SUBJECT);
    await page.getByTestId("save-layout").click();

    // The closed session is what proves the save, not the toast -- the same
    // rule saved-views.spec.ts states, and for the same reason: the toast
    // dismisses itself on a timer and a test that races it is a flake.
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );

    // Revision 0 is the claim "I believe nothing is stored", which is what an
    // unsaved dashboard's first write has to send.
    expect(writes).toHaveLength(1);
    expect(writes[0].revision).toBe(0);
    expect(writes[0].config.scope).toBe("overview");
    expect(
      writes[0].config.items.find((i) => i.instanceId === SUBJECT)?.x,
    ).toBe(moved - 1);

    await page.reload();
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await page.getByTestId("edit-layout").click();
    expect(await column(page, SUBJECT)).toBe(moved);
  });

  test("a successful save says so", async ({ page }) => {
    // The one assertion on the toast itself. Every other test here proves the
    // save through state that outlives it, so if this is the only red one the
    // failure is the notification and not the write.
    await stubLayoutStore(page);
    await editableDashboard(page);
    await nudge(page, "ArrowLeft");

    await page.getByTestId("save-layout").click();

    // Tighter than the default: the toast dismisses itself after 5s, and a
    // longer wait would report "never appeared" for one that came and went.
    await expect(page.getByText("Dashboard layout saved")).toBeVisible({
      timeout: 4000,
    });
  });

  test("a second save claims the revision the first one produced", async ({
    page,
  }) => {
    const writes = await stubLayoutStore(page);
    await editableDashboard(page);

    await nudge(page, "ArrowLeft");
    await page.getByTestId("save-layout").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    // Straight back in, with no reload between: the store has to carry the
    // revision the save returned, or the second write claims a stale one and
    // the server refuses a conflict the user did not cause.
    await page.getByTestId("edit-layout").click();
    await nudge(page, "ArrowLeft");
    await page.getByTestId("save-layout").click();

    await expect
      .poll(() => writes.length)
      .toBe(2);
    expect(writes[1].revision).toBe(1);
  });

  test("a conflicting save keeps the work and says it cannot be written", async ({
    page,
  }) => {
    await conflictingStore(page);
    await editableDashboard(page);
    const mine = await nudge(page, "ArrowLeft");

    await page.getByTestId("save-layout").click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("This dashboard changed somewhere else");

    // Option one: keep editing. The arrangement on screen is the only copy of
    // this work, so dismissing must not touch it.
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
    await expectColumn(page, SUBJECT, mine);

    // And Save says what the dialog said: not until the newer layout is
    // loaded. A refused write leaves the client unable to claim a revision at
    // all, so a second press could only fail again and less clearly.
    const save = page.getByTestId("save-layout");
    await expect(save).toBeDisabled();
    await expect(save).toHaveAttribute("title", SAVE_BLOCKED_REASON);
  });

  test("a conflicting save can hand over to the layout that won", async ({
    page,
  }) => {
    await conflictingStore(page);
    await editableDashboard(page);
    const mine = await nudge(page, "ArrowLeft");

    await page.getByTestId("save-layout").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Load the saved layout" })
      .click();

    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "false",
    );
    await page.getByTestId("edit-layout").click();
    // Column 1 is where the other tab left it, and it is not where this
    // session had it: the grid is re-mounted on what the server returned
    // rather than left holding the edits the user just discarded.
    await expectColumn(page, SUBJECT, 1);
    expect(mine).not.toBe(1);
  });

  test("a save the server refuses for another reason keeps the work on screen", async ({
    page,
  }) => {
    await page.route(LAYOUT_URL, async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      await preferenceError(route, 503, "database_unavailable");
    });

    await editableDashboard(page);
    const mine = await nudge(page, "ArrowLeft");

    await page.getByTestId("save-layout").click();

    // Still editing, still holding the arrangement: a failed write is not a
    // reason to throw away what the user made.
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-editable",
      "true",
    );
    await expectColumn(page, SUBJECT, mine);
    await expect(page.getByTestId("save-layout")).toHaveAttribute(
      "title",
      SAVE_BLOCKED_REASON,
    );
  });

  test("the refusal outlives the session, and a reload clears it", async ({
    page,
  }) => {
    // Refuse the first write and accept everything after it. Leaving edit mode
    // and coming back is the recovery a user would try first, and it does not
    // work -- nothing there re-reads, so the store still cannot claim a
    // revision. Only the reload the button asks for does.
    let refused = false;
    const writes: { revision: number }[] = [];
    await page.route(LAYOUT_URL, async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      if (!refused) {
        refused = true;
        await preferenceError(route, 503, "database_unavailable");
        return;
      }
      const body = route.request().postDataJSON() as {
        revision: number;
        config: DashboardLayoutConfig;
      };
      writes.push(body);
      await json(route, 200, { data: record(body.revision + 1, body.config) });
    });

    await editableDashboard(page);
    await nudge(page, "ArrowLeft");
    await page.getByTestId("save-layout").click();
    await expect(page.getByTestId("save-layout")).toBeDisabled();

    await page.getByTestId("cancel-edit").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Discard changes" })
      .click();
    await page.getByTestId("edit-layout").click();
    await nudge(page, "ArrowLeft");

    // Still blocked: a new session is not a read.
    await expect(page.getByTestId("save-layout")).toBeDisabled();
    await expect(page.getByTestId("save-layout")).toHaveAttribute(
      "title",
      SAVE_BLOCKED_REASON,
    );

    await page.reload();
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);
    await page.getByTestId("edit-layout").click();
    await nudge(page, "ArrowLeft");

    const save = page.getByTestId("save-layout");
    await expect(save).toBeEnabled();
    await save.click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();
    expect(writes).toHaveLength(1);
  });

  // -------------------------------------------------------------------------
  // Removing a widget (D17)
  // -------------------------------------------------------------------------

  test("a widget can be removed, and Cancel brings it back", async ({
    page,
  }) => {
    const writes = await stubLayoutStore(page);
    await editableDashboard(page);

    await expect(widget(page, SUBJECT)).toBeVisible();
    // No confirmation: removal is undone by Cancel and writes nothing until
    // Save, so a modal between the user and every widget they want gone is
    // what actually makes people abandon a layout.
    await removeWidget(page, SUBJECT);
    await expect(widget(page, SUBJECT)).toHaveCount(0);
    await expect(page.getByTestId("save-layout")).toBeEnabled();

    // The rule the whole editor rests on: nothing reaches the server except
    // by Save. Asserted here and not left to the visible result, because a
    // removal that also wrote would look identical on screen -- and the only
    // sign of it would be the next reload, with the widget gone for good.
    expect(writes).toHaveLength(0);

    await page.getByTestId("cancel-edit").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Discard changes" })
      .click();

    await expect(widget(page, SUBJECT)).toBeVisible();
    // And the undo is an undo, not a second write that happens to restore it.
    expect(writes).toHaveLength(0);
  });

  test("the control is not offered outside edit mode", async ({ page }) => {
    await stubLayoutStore(page);
    await page.goto("/");
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(10);

    // A monitoring dashboard gets clicked through fast, and a live remove
    // button on every card is a widget nobody meant to delete.
    await expect(page.getByTestId("remove-widget")).toHaveCount(0);

    await page.getByTestId("edit-layout").click();
    await expect(page.getByTestId("remove-widget")).toHaveCount(10);
  });

  test("removing the focused widget leaves the keyboard on the grid", async ({
    page,
  }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    const first = page.getByTestId("grid-item").first();
    const removedId = await first.getAttribute("data-instance-id");
    if (removedId === null) throw new Error("no instance id on the first cell");

    await first.getByTestId("remove-widget").click();

    // Whatever moved up into the gap, not the body: removing the focused
    // element drops focus to the top of the page otherwise, which for a
    // keyboard user means starting the whole journey over.
    await expect(widget(page, removedId)).toHaveCount(0);
    await expect(page.getByTestId("grid-item").first()).toBeFocused();
  });

  test("emptying the grid puts the keyboard on Add widget", async ({ page }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    // The case the grid alone could not answer: there is no cell left to move
    // to, and the only place the keyboard can go is a toolbar control the
    // grid does not own. It shipped returning early here, which left focus on
    // the body -- the top of the page, with the whole dashboard just gone.
    const buttons = page.getByTestId("remove-widget");
    for (let remaining = await buttons.count(); remaining > 0; remaining--) {
      await buttons.first().click();
      await expect(buttons).toHaveCount(remaining - 1);
    }

    await expect(page.getByTestId("grid-item")).toHaveCount(0);
    await expect(page.getByTestId("add-widget")).toBeFocused();
  });

  test("a widget can be removed in one-column mode", async ({ page }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    // Below the grid's narrow breakpoint there is nothing to arrange -- no
    // columns to move between and no width to size -- and removal was gated
    // on the same flag as arranging, so edit mode opened here with nothing in
    // it. Removing a widget is not a spatial gesture and does not need the
    // space arranging does.
    await page.setViewportSize({ width: 700, height: 900 });
    await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
      "data-grid-mode",
      "narrow",
    );

    await expect(widget(page, SUBJECT)).toBeVisible();
    await removeWidget(page, SUBJECT);
    await expect(widget(page, SUBJECT)).toHaveCount(0);
    await expect(page.getByTestId("save-layout")).toBeEnabled();
  });

  test("a removed widget becomes addable again", async ({ page }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    await removeWidget(page, SUBJECT);

    await page.getByTestId("add-widget").click();
    const option = page.getByTestId("widget-option-active-alerts");
    // The palette disables what is already placed; the removal is what makes
    // this row live again, which is the whole loop D16 and D17 close together.
    await expect(option).toHaveAttribute("aria-disabled", "false");
  });

  test("saving a removal writes the layout without it", async ({ page }) => {
    const writes = await stubLayoutStore(page);
    await editableDashboard(page);

    await removeWidget(page, SUBJECT);
    await page.getByTestId("save-layout").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();

    expect(writes).toHaveLength(1);
    expect(
      writes[0].config.items.map((i) => i.instanceId),
    ).not.toContain(SUBJECT);
  });

  // -------------------------------------------------------------------------
  // Reset (D17)
  // -------------------------------------------------------------------------

  test("Reset asks first, and the answer decides", async ({ page }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    await removeWidget(page, SUBJECT);
    await expect(widget(page, SUBJECT)).toHaveCount(0);

    await page.getByTestId("reset-layout").click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Reset dashboard layout");
    // Declining changes nothing: the arrangement on screen is the only copy
    // of this session's work.
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(widget(page, SUBJECT)).toHaveCount(0);

    await page.getByTestId("reset-layout").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Reset layout" })
      .click();

    await expect(widget(page, SUBJECT)).toBeVisible();
    // The keyboard goes back to the button that opened the dialog: the
    // session is still open, so no exit path restores it.
    await expect(page.getByTestId("reset-layout")).toBeFocused();
  });

  test("Reset writes nothing until Save", async ({ page }) => {
    // Over a STORED layout, not the default. Resetting a dashboard that is
    // already the shipped one leaves the working copy equal to the baseline
    // and so leaves Save correctly disarmed -- which the last test in this
    // file asserts, and which would make this one prove nothing about writes.
    const writes = await stubLayoutStore(page, {
      revision: 3,
      config: STORED_ONE_WIDGET,
    });
    await page.goto("/");
    await expect(page.getByTestId("grid-item")).toHaveCount(1);
    await page.getByTestId("edit-layout").click();

    await page.getByTestId("reset-layout").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Reset layout" })
      .click();

    // One rule, not two: nothing reaches the server except by Save.
    expect(writes).toHaveLength(0);
    // And it is the shipped default that came back.
    await expectColumn(page, SUBJECT, DEFAULT_SUBJECT_COLUMN);

    await page.getByTestId("save-layout").click();
    await expect(page.getByTestId("edit-layout")).toBeVisible();
    expect(writes).toHaveLength(1);
    expect(writes[0].config.items).toHaveLength(10);
  });

  test("Reset restores the shipped default, not the layout last saved", async ({
    page,
  }) => {
    // Reset has to reach past the stored layout (D-6): "restore what I had"
    // is undo, a different feature, and a button that does whichever the
    // reader guessed is worse than one that does the narrower thing.
    await stubLayoutStore(page, { revision: 3, config: STORED_ONE_WIDGET });
    await page.goto("/");
    await expect(page.getByTestId("grid-item")).toHaveCount(1);

    await page.getByTestId("edit-layout").click();
    await page.getByTestId("reset-layout").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Reset layout" })
      .click();

    await expect(page.getByTestId("grid-item")).toHaveCount(10);
    await expect(page.getByTestId("save-layout")).toBeEnabled();
  });

  test("Reset over an untouched default leaves Save disarmed", async ({
    page,
  }) => {
    await stubLayoutStore(page);
    await editableDashboard(page);

    await page.getByTestId("reset-layout").click();
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "Reset layout" })
      .click();

    // Nothing changed, so there is nothing to write. An enabled Save that
    // writes nothing trains people to ignore the one that can.
    await expect(page.getByTestId("save-layout")).toBeDisabled();
  });
});
