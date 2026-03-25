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

  test("redirects to login when unauthenticated", async ({ browser }) => {
    // Create a fresh context without storageState (no cookies)
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto("/");
    // Should redirect to /login since there are no auth cookies
    await expect(page).toHaveURL(/\/login/);

    await context.close();
  });
});
