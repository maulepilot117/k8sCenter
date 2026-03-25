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
    // Create a fresh context without storageState (no cookies, no token)
    const context = await browser.newContext();
    const page = await context.newPage();

    await page.goto("/");
    // The SSR page renders first, then the island hydrates and detects no auth.
    // The redirect to /login happens client-side after hydration.
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 });

    await context.close();
  });
});
