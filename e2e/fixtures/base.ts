import { test as base, expect } from "@playwright/test";
import { PageErrorWatcher } from "../helpers.ts";

interface PageErrorFixtures {
  /**
   * Page errors this spec provokes on purpose, matched against the error
   * text. Set with `test.use({ allowedPageErrors: [/.../] })`.
   */
  allowedPageErrors: RegExp[];
  /** Watches every page in the test's context; fails the test at teardown. */
  pageErrors: PageErrorWatcher;
}

export const test = base.extend<PageErrorFixtures>({
  allowedPageErrors: [[], { option: true }],

  pageErrors: [
    async ({ context, allowedPageErrors }, use) => {
      const watcher = new PageErrorWatcher(context, allowedPageErrors);
      await use(watcher);
      const unexpected = watcher.unexpected();
      if (unexpected.length > 0) {
        throw new Error(PageErrorWatcher.describe(unexpected));
      }
    },
    { auto: true },
  ],

  page: async ({ page }, use) => {
    // Disable CSS animations/transitions for test stability
    await page.emulateMedia({ reducedMotion: "reduce" });

    // Inject the saved access token into the app's auth state.
    // The auth setup saves the token to localStorage as e2e_access_token.
    // We inject it into the app's api.ts module via a page script that runs
    // before any API calls, restoring the Bearer token for authenticated requests.
    await page.addInitScript(() => {
      const token = localStorage.getItem("e2e_access_token");
      if (token) {
        // Intercept fetch to inject the Bearer token header
        const originalFetch = globalThis.fetch;
        globalThis.fetch = (input, init) => {
          const url =
            typeof input === "string"
              ? input
              : input instanceof URL
                ? input.href
                : (input as Request).url;
          // Only add auth header for API calls to our backend
          if (url.includes("/api/") || url.includes("/ws/")) {
            init = init || {};
            const headers = new Headers(init.headers);
            if (!headers.has("Authorization")) {
              headers.set("Authorization", `Bearer ${token}`);
            }
            init.headers = headers;
          }
          return originalFetch(input, init);
        };
      }
    });

    await use(page);
  },
});

export { expect };
