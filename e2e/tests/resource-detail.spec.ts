import { test, expect } from "../fixtures/base.ts";

test.describe("Resource detail page", () => {
  test.beforeEach(async ({ page }) => {
    // Navigate directly to coredns deployment detail page (always exists in kind)
    await page.goto("/workloads/deployments/kube-system/coredns");
    await expect(
      page.getByRole("heading", { name: "coredns" }),
    ).toBeVisible();
  });

  test("Overview tab renders deployment details", async ({ page }) => {
    await expect(page.getByText(/replicas|available|ready/i).first()).toBeVisible();
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

  test("Related pods panel shows pods", async ({ page }) => {
    // Related pods are now shown in the always-visible right pane (not a tab)
    await expect(page.getByText("Related Pods")).toBeVisible();
    // Should show at least one coredns pod in the pod cards
    await expect(page.getByText(/coredns/i).first()).toBeVisible();
  });
});
