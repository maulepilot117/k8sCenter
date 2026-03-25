import { test, expect } from "../fixtures/base.ts";
import { e2eName, waitForTableLoaded, getAuthHeaders } from "../helpers.ts";

// WebSocket live update test — depends on informer cache propagation timing
// which can be slow in CI. Skip in CI, run locally for verification.
test.describe("WebSocket live updates", () => {
  // deno-lint-ignore no-explicit-any
  (test as any).skip(
    !!process.env.CI,
    "WebSocket timing is unreliable in CI — run locally",
  );

  test("new resource appears in table via WebSocket", async ({
    page,
    request,
  }) => {
    await page.goto("/config/configmaps");
    await waitForTableLoaded(page);

    const name = e2eName("ws");
    const headers = await getAuthHeaders(page);

    const createRes = await request.post(
      `/api/v1/resources/configmaps/default`,
      {
        headers,
        data: {
          apiVersion: "v1",
          kind: "ConfigMap",
          metadata: {
            name,
            namespace: "default",
            labels: { e2e: "true" },
          },
          data: { test: "value" },
        },
      },
    );
    expect(createRes.ok()).toBeTruthy();

    await expect(page.getByText(name)).toBeVisible({ timeout: 15_000 });

    await request.delete(`/api/v1/resources/configmaps/default/${name}`, {
      headers,
      failOnStatusCode: false,
    });

    await expect(page.getByText(name)).not.toBeVisible({ timeout: 15_000 });
  });
});
