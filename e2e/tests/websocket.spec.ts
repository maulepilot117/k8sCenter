import { test, expect } from "../fixtures/base.ts";
import { e2eName, waitForTableLoaded, getAuthHeaders } from "../helpers.ts";

test.describe("WebSocket live updates", () => {
  test("new resource appears in table via WebSocket", async ({
    page,
    request,
  }) => {
    // Navigate to configmaps first to establish auth context
    await page.goto("/config/configmaps");
    await waitForTableLoaded(page);

    const name = e2eName("ws");

    // Get auth headers from the page's localStorage (set by auth setup)
    const headers = await getAuthHeaders(page);

    // Create a ConfigMap via BFF proxy with explicit auth
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

    // Assert the new row appears via WebSocket within 15s (no page reload)
    await expect(page.getByText(name)).toBeVisible({ timeout: 15_000 });

    // Delete the resource
    await request.delete(`/api/v1/resources/configmaps/default/${name}`, {
      headers,
      failOnStatusCode: false,
    });

    // Assert the row disappears via WebSocket within 15s
    await expect(page.getByText(name)).not.toBeVisible({ timeout: 15_000 });
  });
});
