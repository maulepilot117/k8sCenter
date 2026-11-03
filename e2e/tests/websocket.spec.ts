import { expect, test } from "../fixtures/base.ts";
import {
  deleteResource,
  e2eName,
  getAuthHeaders,
  loginThroughUI,
  pageCleanupApi,
  waitForTableLoaded,
  watchForSubscription,
  withCleanup,
} from "../helpers.ts";

/**
 * WebSocket live updates.
 *
 * This spec was skipped in CI as "unreliable", which had the skip exactly
 * backwards. CI runs the production server, where websockets work; local runs
 * use the Vite dev server, where every upgrade hung in CONNECTING until
 * vite.config.ts began proxying /ws. The one environment that could prove the
 * feature never ran this test, and the one that ran it could never pass.
 *
 * The flakiness was never informer timing either. Two concrete causes:
 *
 *   1. The shared fixture injects an Authorization header, so the app never
 *      answers 401, never refreshes, and never populates the in-memory token
 *      that lib/ws.ts reads to authenticate the socket. loginThroughUI in a
 *      context of its own fixes that -- see the helper for why dropping the
 *      injection globally is not an option.
 *   2. The spec published its event before the page had subscribed, and an
 *      event published to nobody is never delivered. Waiting for the
 *      subscribe acknowledgement, rather than for the table to finish
 *      loading, is what makes this deterministic.
 */
test.describe("WebSocket live updates", () => {
  test("new resource appears in and disappears from a table live", async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const name = e2eName("ws");
    // Runs while the context is still open: deleting through a closed page
    // would fail, and the ConfigMap would outlive the run.
    await withCleanup(async () => {
      // Registered before any navigation: the acknowledgement arrives during
      // the page load that follows.
      const subscribed = watchForSubscription(page, "configmaps");

      await loginThroughUI(page);
      await page.goto("/config/configmaps");
      await waitForTableLoaded(page);
      await expect.poll(subscribed, { timeout: 20_000 }).toBe(true);

      const headers = await getAuthHeaders(page);

      const created = await page.request.post(
        "/api/v1/resources/configmaps/default",
        {
          headers,
          data: {
            apiVersion: "v1",
            kind: "ConfigMap",
            metadata: { name, namespace: "default", labels: { e2e: "true" } },
            data: { test: "value" },
          },
        },
      );
      expect(created.ok()).toBeTruthy();

      // Arrives over the socket: nothing here reloads the page.
      await expect(page.getByText(name)).toBeVisible({ timeout: 15_000 });

      await deleteResource(
        { request: page.request, headers },
        "configmaps",
        "default",
        name,
      );

      await expect(page.getByText(name)).not.toBeVisible({ timeout: 15_000 });
    }, [
      // A 404 here means the in-body delete already ran.
      async () =>
        deleteResource(await pageCleanupApi(page), "configmaps", "default", name),
      () => context.close(),
    ]);
  });
});
