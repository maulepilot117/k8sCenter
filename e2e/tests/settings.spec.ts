import { test, expect } from "../fixtures/base.ts";

test.describe("Settings pages", () => {
  test("general settings page loads", async ({ page }) => {
    await page.goto("/settings/general");
    await expect(page.getByText(/settings|general/i)).toBeVisible();
  });

  test("users page shows admin user", async ({ page }) => {
    await page.goto("/settings/users");

    // Admin user should appear in the user list
    await expect(page.getByText("admin")).toBeVisible();

    // "you" badge should be next to our own username
    await expect(page.getByText("you", { exact: true })).toBeVisible();
  });

  test("audit log page renders", async ({ page }) => {
    await page.goto("/settings/audit");

    // Audit table should render (auth setup + login actions should have created entries)
    await expect(
      page.getByRole("table").or(page.getByText(/audit/i)),
    ).toBeVisible();
  });

  test("monitoring status page loads", async ({ page }) => {
    await page.goto("/monitoring");
    // Should load without error — will show "not configured" since no Prometheus in kind
    await expect(page.getByText(/monitoring|prometheus/i)).toBeVisible();
  });
});
