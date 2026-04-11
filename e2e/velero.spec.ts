import { test, expect } from "@playwright/test";

/**
 * Velero integration E2E tests.
 *
 * NOTE: These tests require Velero to be installed in the cluster.
 * If Velero is not installed, tests will verify graceful degradation.
 */

test.describe("Velero Backup Section", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup");
  });

  test("redirects /backup to /backup/backups", async ({ page }) => {
    await expect(page).toHaveURL(/\/backup\/backups$/);
  });

  test("shows backup section in navigation", async ({ page }) => {
    // Verify the backup icon is visible in the icon rail
    const backupIcon = page.locator('[data-section="backup"]');
    // Icon rail items may be rendered as tooltips, check the href
    const backupLink = page.locator('a[href="/backup"]');
    await expect(backupLink.or(backupIcon).first()).toBeVisible();
  });

  test("shows sub-navigation tabs", async ({ page }) => {
    await expect(page.getByRole("link", { name: "Backups" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Restores" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Schedules" })).toBeVisible();
  });

  test("can navigate to restores tab", async ({ page }) => {
    await page.getByRole("link", { name: "Restores" }).click();
    await expect(page).toHaveURL(/\/backup\/restores$/);
  });

  test("can navigate to schedules tab", async ({ page }) => {
    await page.getByRole("link", { name: "Schedules" }).click();
    await expect(page).toHaveURL(/\/backup\/schedules$/);
  });

  test("shows New Backup button", async ({ page }) => {
    const newBackupButton = page.getByRole("link", { name: /New Backup/i });
    await expect(newBackupButton).toBeVisible();
  });

  test("can navigate to new backup wizard", async ({ page }) => {
    await page.getByRole("link", { name: /New Backup/i }).click();
    await expect(page).toHaveURL(/\/backup\/backups\/new$/);
    await expect(page.getByRole("heading", { name: "New Backup" })).toBeVisible();
  });
});

test.describe("Velero Backup Wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/backups/new");
  });

  test("shows wizard stepper", async ({ page }) => {
    await expect(page.getByText("Configure")).toBeVisible();
    await expect(page.getByText("Review & Apply")).toBeVisible();
  });

  test("shows backup name field", async ({ page }) => {
    await expect(page.getByLabel(/Backup Name/i)).toBeVisible();
  });

  test("shows storage location dropdown", async ({ page }) => {
    await expect(page.getByText(/Storage Location/i)).toBeVisible();
  });

  test("shows retention TTL dropdown", async ({ page }) => {
    await expect(page.getByText(/Retention/i)).toBeVisible();
  });

  test("shows snapshot volumes checkbox", async ({ page }) => {
    await expect(page.getByLabel(/Snapshot persistent volumes/i)).toBeVisible();
  });

  test("can cancel wizard", async ({ page }) => {
    await page.getByRole("button", { name: "Cancel" }).click();
    await expect(page).toHaveURL(/\/backup\/backups$/);
  });
});

test.describe("Velero Restore Wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/restores/new");
  });

  test("shows wizard stepper", async ({ page }) => {
    await expect(page.getByText("Configure")).toBeVisible();
    await expect(page.getByText("Review & Apply")).toBeVisible();
  });

  test("shows source backup dropdown", async ({ page }) => {
    await expect(page.getByText(/Source Backup/i)).toBeVisible();
  });

  test("shows restore PVs checkbox", async ({ page }) => {
    await expect(page.getByLabel(/Restore persistent volumes/i)).toBeVisible();
  });
});

test.describe("Velero Schedule Wizard", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/backup/schedules/new");
  });

  test("shows wizard stepper", async ({ page }) => {
    await expect(page.getByText("Configure")).toBeVisible();
    await expect(page.getByText("Review")).toBeVisible();
  });

  test("shows schedule name field", async ({ page }) => {
    await expect(page.getByLabel(/Schedule Name/i)).toBeVisible();
  });

  test("shows cron schedule input", async ({ page }) => {
    await expect(page.getByText(/Schedule \(Cron\)/i)).toBeVisible();
  });

  test("shows cron preset buttons", async ({ page }) => {
    await expect(page.getByRole("button", { name: /Hourly/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /Daily/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /Weekly/i })).toBeVisible();
    await expect(page.getByRole("button", { name: /Monthly/i })).toBeVisible();
  });

  test("shows paused checkbox", async ({ page }) => {
    await expect(page.getByLabel(/Create paused/i)).toBeVisible();
  });
});
