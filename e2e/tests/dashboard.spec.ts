import { test, expect } from "../fixtures/base.ts";

test.describe("Dashboard @smoke", () => {
  test("loads with real cluster data", async ({ page }) => {
    await page.goto("/");

    // Dashboard should show cluster overview heading
    await expect(
      page.getByRole("heading", { name: /cluster overview/i }),
    ).toBeVisible();

    // Stat cards should display real data from the kind cluster
    await expect(page.getByText("Nodes")).toBeVisible();
  });
});
