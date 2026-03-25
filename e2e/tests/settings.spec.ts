import { test, expect } from "../fixtures/base.ts";

test.describe("Settings pages", () => {
  test("general settings page loads", async ({ page }) => {
    await page.goto("/settings/general");
    await expect(
      page.getByRole("heading", { name: /settings/i }),
    ).toBeVisible();
  });

  test("users page shows admin user", async ({ page }) => {
    await page.goto("/settings/users");

    // Admin user should appear in the user list
    await expect(
      page.getByRole("cell", { name: "admin" }).first(),
    ).toBeVisible();
  });

  test("audit log page renders", async ({ page }) => {
    await page.goto("/settings/audit");

    // Audit table should render
    await expect(page.getByRole("table")).toBeVisible();
  });

  test("monitoring status page loads", async ({ page }) => {
    await page.goto("/monitoring");
    // Should load without error — heading should be visible
    await expect(
      page.getByRole("heading").first(),
    ).toBeVisible();
  });
});
