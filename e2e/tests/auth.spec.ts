import { test, expect } from "../fixtures/base.ts";

test.describe("Auth @smoke", () => {
  test("dashboard loads when authenticated via storageState", async ({
    page,
  }) => {
    // storageState has the refresh cookie — first API call triggers
    // transparent token refresh, then dashboard loads normally
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: /cluster overview/i }),
    ).toBeVisible();
  });

  test("unauthenticated user cannot access dashboard", async ({
    browser,
  }) => {
    // Create a fresh context without storageState (no cookies, no token)
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto("/");
    // Without auth, either:
    // 1. Redirects to /login (client-side after hydration)
    // 2. Shows login form on the page
    // 3. Dashboard content is not visible
    await page.waitForTimeout(3000); // Allow hydration + redirect
    const url = page.url();
    const hasLogin =
      url.includes("/login") ||
      (await page.getByLabel("Username").isVisible().catch(() => false));
    const hasDashboard = await page
      .getByRole("heading", { name: /cluster overview/i })
      .isVisible()
      .catch(() => false);

    // Either we're on login page OR dashboard is not showing
    expect(hasLogin || !hasDashboard).toBeTruthy();

    await context.close();
  });
});
