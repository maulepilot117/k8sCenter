import { test, expect } from "../fixtures/base.ts";
import { waitForTableLoaded } from "../helpers.ts";

test.describe("Resource detail page", () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to deployments in kube-system where coredns always exists
    await page.goto("/workloads/deployments");
    await waitForTableLoaded(page);

    // Select kube-system namespace
    const nsSelect = page.getByTestId("namespace-select").or(
      page.getByRole("combobox").first(),
    );
    await nsSelect.selectOption("kube-system");
    await waitForTableLoaded(page);

    // Click on coredns row to open detail page
    await page.getByRole("row").filter({ hasText: "coredns" }).first().click();

    // Wait for detail page to load
    await expect(page.getByText("coredns")).toBeVisible();
  });

  test("Overview tab renders deployment details", async ({ page }) => {
    // Overview tab is active by default
    await expect(page.getByText(/replicas|available|ready/i)).toBeVisible();
  });

  test("YAML tab shows editor", async ({ page }) => {
    await page.getByRole("tab", { name: "YAML" }).or(
      page.getByText("YAML").first(),
    ).click();

    // CodeMirror editor should be visible
    await expect(page.locator(".cm-editor")).toBeVisible();
  });

  test("Events tab activates", async ({ page }) => {
    await page.getByRole("tab", { name: "Events" }).or(
      page.getByText("Events").first(),
    ).click();

    // Should show events table or "No events" message
    const hasEvents = page.getByRole("table");
    const noEvents = page.getByText(/no events/i);
    await expect(hasEvents.or(noEvents)).toBeVisible();
  });

  test("Pods tab shows related pods", async ({ page }) => {
    // Deployments have a Pods tab
    await page.getByRole("tab", { name: "Pods" }).or(
      page.getByText("Pods").first(),
    ).click();

    // Should show pod list with at least 1 coredns pod
    await expect(page.getByText(/coredns/i)).toBeVisible();
  });
});
