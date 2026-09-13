import { expect, test } from "../fixtures/base.ts";
import { deleteAllPins } from "../helpers.ts";

/**
 * Discoverability of the Release A surfaces (DX).
 *
 * Release A shipped saved views and pins, and then advertised them on two
 * surfaces that contained no path to using them: a "Pinned" header above the
 * words "Nothing pinned yet.", and a "Views (0)" button with no accessible
 * name. Both told the user a feature existed and neither told them how to
 * reach it.
 *
 * These specs pin the hints, not the phrasing: each asserts a testid exists
 * and is non-empty, plus the one substring that carries the instruction. Copy
 * can be reworded without breaking them; deleting the hint cannot.
 *
 * NOT covered here, and deliberately: the command palette. Its search index is
 * built once into a useRef (CommandPalette.tsx:205) and cannot react to
 * signals, so a user's pins can never appear in it without a different
 * mechanism. Nothing to assert.
 */

test.describe("discoverability", () => {
  test("the empty pinned section tells the user how to create a pin", async ({ page }) => {
    await page.goto("/");
    await deleteAllPins(page);
    await page.reload();

    const empty = page.getByTestId("pinned-empty");
    await expect(empty).toBeVisible();

    const hint = page.getByTestId("pinned-empty-hint");
    await expect(hint).toBeVisible();
    // The instruction must name the control by the word on the button.
    await expect(hint).toContainText("Pin");
  });

  test("the saved-views trigger has an accessible name that says it saves", async ({ page }) => {
    await page.goto("/workloads/pods");

    const toggle = page.getByTestId("saved-views-toggle");
    await expect(toggle).toBeVisible();

    // The visible label stays short; the accessible name carries the meaning.
    const label = await toggle.getAttribute("aria-label");
    expect(label, "saved-views-toggle must have an aria-label").not.toBeNull();
    expect(label!.toLowerCase()).toContain("save");
  });

  test("the empty saved-views menu names the create action", async ({ page }) => {
    await page.goto("/workloads/pods");
    await page.getByTestId("saved-views-toggle").click();

    const menu = page.getByTestId("saved-views-menu");
    await expect(menu).toBeVisible();

    // Only meaningful when this table has no saved views; skip otherwise
    // rather than deleting another spec's fixtures.
    const empty = page.getByTestId("saved-views-empty");
    if (await empty.count() === 0) {
      test.skip(true, "table already has saved views");
    }

    const hint = page.getByTestId("saved-views-empty-hint");
    await expect(hint).toBeVisible();
    await expect(hint).toContainText("Save current view");
  });
});
