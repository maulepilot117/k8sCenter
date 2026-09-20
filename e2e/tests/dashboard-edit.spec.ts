import type { Page, Route } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { DashboardLayoutConfig } from "../../frontend/lib/dashboard/types.ts";

// The dashboard editor: entering edit mode, whether Save is offered, what
// Cancel puts back, and what a conflicting save does. The state machine behind
// all four is unit tested in frontend/lib/dashboard/edit-session_test.ts;
// these prove the island routes the user's gestures through it, and that the
// save path talks to the server the way the endpoint expects.
//
// Every test here mocks the preferences endpoint rather than using the real
// one, although the E2E stack does run a Postgres. A layout is stored per
// (user, cluster, scope) and the whole suite shares one login, so a single
// test that really saved would hand its arrangement to every later test that
// loads the dashboard -- including the ones in dashboard-grid.spec.ts that
// assert where the default layout puts things. The server side of this
// contract is covered by the handler tests in
// backend/internal/preferences/handler_test.go.

const LAYOUT_URL = "**/api/v1/preferences/layouts/overview";

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

/** A full preference record, the shape `api()` unwraps from `data`. */
function record(revision: number, config: unknown) {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    kind: "dashboard_layout",
    name: "overview",
    clusterId: "local",
    schemaVersion: 1,
    revision,
    config,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
}

const json = (route: Route, status: number, body: unknown) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

const preferenceError = (route: Route, status: number, reason: string) =>
  json(route, status, {
    error: { code: status, message: "refused", reason },
  });

/**
 * Serves the layout endpoint from memory: 204 until something is saved, the
 * saved record afterwards, with the revision advancing on each write.
 *
 * Returns the writes it saw, so a test can assert what the client actually
 * sent rather than only what came back.
 */
async function stubLayoutStore(page: Page) {
  const writes: { revision: number; config: DashboardLayoutConfig }[] = [];
  let stored: { revision: number; config: DashboardLayoutConfig } | null = null;

  await page.route(LAYOUT_URL, async (route) => {
    const request = route.request();
    if (request.method() === "GET") {
      if (stored === null) {
        await route.fulfill({ status: 204, body: "" });
        return;
      }
      await json(route, 200, { data: record(stored.revision, stored.config) });
      return;
    }
    if (request.method() === "PUT") {
      const body = request.postDataJSON() as {
        revision: number;
        config: DashboardLayoutConfig;
      };
      writes.push(body);
      stored = { revision: body.revision + 1, config: body.config };
      await json(route, 200, { data: record(stored.revision, stored.config) });
      return;
    }
    await route.fallback();
  });

  return writes;
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
});
