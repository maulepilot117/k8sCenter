import { expect, test } from "../fixtures/base.ts";

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

  test("unauthenticated user is redirected to the login page", async ({
    browser,
  }) => {
    // storageState MUST be cleared explicitly. A bare browser.newContext()
    // inside the test runner inherits the project's storageState, cookies and
    // all -- this context previously arrived holding the admin refresh_token,
    // so the app did exactly the right thing and served the dashboard, and
    // the assertion that it should not have was the thing in the wrong. That
    // is what made this look like a redirect-timing flake for long enough to
    // get skipped in CI.
    const context = await browser.newContext({
      storageState: { cookies: [], origins: [] },
    });
    try {
      const page = await context.newPage();
      await page.goto("/");

      // The guarantee, waited for rather than slept past: the app serves the
      // SSR shell first, then hydrates, discovers it cannot authenticate, and
      // redirects. Measured at ~600ms; 15s is slack for a loaded CI box, not
      // a guess about how long hydration takes.
      await page.waitForURL(/\/login(\?|$)/, { timeout: 15_000 });

      await expect(page.getByLabel("Username")).toBeVisible();
      await expect(
        page.getByRole("heading", { name: /cluster overview/i }),
      ).toHaveCount(0);
    } finally {
      await context.close();
    }
  });
});
