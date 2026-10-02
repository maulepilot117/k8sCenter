import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";


/**
 * Velero backup & restore UI.
 *
 * The CI kind cluster does not install Velero, so the section renders its
 * "Velero Not Detected" state there. Everything that does not need Velero's
 * CRDs -- navigation, the not-detected state, and the three wizards, whose
 * previews are generated server-side without touching the cluster -- runs
 * unconditionally. The one test that needs real Velero data skips with a
 * reason when the status route reports it absent.
 */

/** The dashboard's create/refresh buttons render once its fetch settles. */
async function waitForDashboard(page: Page) {
  await expect(page.getByRole("button", { name: "Refresh" })).toBeVisible({
    timeout: 15_000,
  });
}

/**
 * Open the backups page, wait for it to load, and return whether the backend
 * reported Velero installed -- read from the page's own status request, so
 * detection adds no request of its own.
 */
async function openBackups(page: Page): Promise<boolean> {
  const status = page.waitForResponse(
    (r) =>
      new URL(r.url()).pathname === "/api/v1/velero/status" &&
      r.request().method() === "GET",
  );
  await page.goto("/backup/backups");
  const res = await status;
  expect(res.ok(), `velero status: ${res.status()}`).toBe(true);
  await waitForDashboard(page);
  return (await res.json()).data?.detected === true;
}

test.describe("Velero backup section", () => {
  test("/backup renders the Backup & Restore overview", async ({ page }) => {
    await page.goto("/backup");
    await expect(page).toHaveURL(/\/backup$/);
    await expect(
      page.getByRole("heading", { name: "Backup & Restore", level: 1 }),
    ).toBeVisible();
  });

  test("shows the Backup section in the icon rail", async ({ page }) => {
    await page.goto("/backup/backups");
    const railLink = page.getByRole("link", { name: "Backup", exact: true });
    await expect(railLink).toBeVisible();
    await expect(railLink).toHaveAttribute("href", "/backup/backups");
  });

  test("shows the Backups, Restores and Schedules sub-navigation", async ({
    page,
  }) => {
    await page.goto("/backup/backups");
    for (const name of ["Backups", "Restores", "Schedules"]) {
      await expect(page.getByRole("link", { name, exact: true })).toBeVisible();
    }
  });

  test("can navigate to the restores page", async ({ page }) => {
    await page.goto("/backup/backups");
    await page.getByRole("link", { name: "Restores", exact: true }).click();
    await expect(page).toHaveURL(/\/backup\/restores$/);
    await expect(
      page.getByRole("heading", { name: "Restores", level: 1 }),
    ).toBeVisible();
  });

  test("can navigate to the schedules page", async ({ page }) => {
    await page.goto("/backup/backups");
    await page.getByRole("link", { name: "Schedules", exact: true }).click();
    await expect(page).toHaveURL(/\/backup\/schedules$/);
    await expect(
      page.getByRole("heading", { name: "Schedules", level: 1 }),
    ).toBeVisible();
  });

  test("New Backup opens the backup wizard in place", async ({ page }) => {
    await page.goto("/backup/backups");
    await waitForDashboard(page);

    await page.getByRole("button", { name: /New Backup/ }).click();
    await expect(page.getByText("Create Backup", { exact: true })).toBeVisible();
    await expect(page.getByLabel("Backup Name")).toBeVisible();

    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(page.getByText("Create Backup", { exact: true })).toBeHidden();
    await expect(page).toHaveURL(/\/backup\/backups$/);
  });

  test("shows the not-detected state when Velero is absent", async ({
    page,
  }) => {
    test.skip(
      await openBackups(page),
      "Velero is installed on this cluster",
    );

    await expect(page.getByText("Velero Not Detected")).toBeVisible();
    await expect(
      page.getByRole("link", { name: /View Velero Installation Docs/ }),
    ).toBeVisible();
    // A load failure renders an error banner instead; not-installed must not.
    await expect(page.getByText("Failed to load Velero data")).toBeHidden();
  });

  test("lists backups when Velero is installed", async ({ page }) => {
    test.skip(
      !(await openBackups(page)),
      "Velero is not installed on this cluster",
    );

    await expect(page.getByPlaceholder("Search…")).toBeVisible();
    await expect(page.getByText("Velero Not Detected")).toBeHidden();
  });
});

test.describe("Velero backup wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/backups/new");
  });

  test("shows the wizard steps", async ({ page }) => {
    await expect(page.getByText("Configure", { exact: true })).toBeVisible();
    await expect(page.getByText("Review", { exact: true })).toBeVisible();
  });

  test("shows a generated backup name", async ({ page }) => {
    await expect(page.getByLabel("Backup Name")).toHaveValue(
      /^backup-\d{8}-\d{6}$/,
    );
  });

  test("shows the storage location dropdown", async ({ page }) => {
    const select = page.getByLabel("Storage Location");
    await expect(select).toBeVisible();
    await expect(select.locator("option", { hasText: "Default" })).toHaveCount(
      1,
    );
  });

  test("shows the retention dropdown", async ({ page }) => {
    const select = page.getByLabel("Retention (TTL)");
    await expect(select).toBeVisible();
    await expect(select.locator("option", { hasText: "30 days" })).toHaveCount(
      1,
    );
  });

  test("shows the snapshot volumes checkbox, checked by default", async ({
    page,
  }) => {
    await expect(page.getByLabel("Snapshot persistent volumes")).toBeChecked();
  });

  test("Cancel returns to the page the wizard was opened from", async ({
    page,
  }) => {
    // The standalone wizard page closes with history.back(), so it needs a
    // page to go back to.
    await page.goto("/backup/backups");
    await page.goto("/backup/backups/new");
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(page).toHaveURL(/\/backup\/backups$/);
  });
});

test.describe("Velero restore wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/restores/new");
  });

  test("shows the wizard steps", async ({ page }) => {
    await expect(page.getByText("Configure", { exact: true })).toBeVisible();
    await expect(page.getByText("Review", { exact: true })).toBeVisible();
  });

  test("shows the source backup dropdown", async ({ page }) => {
    const select = page.getByLabel("Source Backup");
    await expect(select).toBeVisible();
    await expect(
      select.locator("option", { hasText: "Select a backup..." }),
    ).toHaveCount(1);
  });

  test("shows the restore PVs checkbox", async ({ page }) => {
    await expect(page.getByLabel("Restore persistent volumes")).toBeVisible();
  });
});

test.describe("Velero schedule wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/schedules/new");
  });

  test("shows the wizard steps", async ({ page }) => {
    await expect(page.getByText("Configure", { exact: true })).toBeVisible();
    await expect(page.getByText("Review", { exact: true })).toBeVisible();
  });

  test("shows the schedule name field", async ({ page }) => {
    await expect(page.getByLabel("Schedule Name")).toBeVisible();
  });

  test("shows the cron schedule input", async ({ page }) => {
    await expect(page.getByLabel("Schedule (Cron)")).toBeVisible();
  });

  test("cron preset buttons fill the schedule", async ({ page }) => {
    const presets: [RegExp, string][] = [
      [/^Hourly/, "0 * * * *"],
      [/^Daily/, "0 0 * * *"],
      [/^Weekly/, "0 0 * * 0"],
      [/^Monthly/, "0 0 1 * *"],
    ];
    const cron = page.getByLabel("Schedule (Cron)");
    for (const [name, value] of presets) {
      const button = page.getByRole("button", { name });
      await expect(button).toBeVisible();
      await button.click();
      await expect(cron).toHaveValue(value);
    }
  });

  test("shows the create-paused checkbox", async ({ page }) => {
    await expect(page.getByLabel(/Create paused/)).toBeVisible();
  });
});
