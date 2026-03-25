import { test, expect } from "../fixtures/base.ts";

test.describe("Resource detail page", () => {
  test.beforeEach(async ({ page }) => {
    // Navigate directly to coredns deployment detail page (always exists in kind)
    await page.goto("/workloads/deployments/kube-system/coredns");
    await expect(page.getByText("coredns")).toBeVisible();
  });

  test("Overview tab renders deployment details", async ({ page }) => {
    await expect(page.getByText(/replicas|available|ready/i)).toBeVisible();
  });

  test("YAML tab shows editor", async ({ page }) => {
    await page.getByRole("tab", { name: "YAML" }).click();
    await expect(page.locator(".cm-editor")).toBeVisible();
  });

  test("Events tab activates", async ({ page }) => {
    await page.getByRole("tab", { name: "Events" }).click();
    // Should show events table or "No events" message
    const hasEvents = page.getByRole("table");
    const noEvents = page.getByText(/no events/i);
    await expect(hasEvents.or(noEvents)).toBeVisible();
  });

  test("Pods tab shows related pods", async ({ page }) => {
    await page.getByRole("tab", { name: "Pods" }).click();
    await expect(page.getByText(/coredns/i)).toBeVisible();
  });
});
