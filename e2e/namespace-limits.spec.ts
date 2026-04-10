import { expect, test } from "./fixtures/base.ts";

test.describe("Namespace Limits", () => {
  // ── Navigation ──────────────────────────────────────────────────

  test("should show Namespace Limits tab in Config section", async ({ page }) => {
    await page.goto("/config/namespaces");
    // SubNav should contain a Namespace Limits tab
    const limitsTab = page.getByRole("link", { name: "Namespace Limits" });
    await expect(limitsTab).toBeVisible();
  });

  test("should navigate to namespace limits dashboard", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    // Wait for loading to finish
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // The page should render the main heading
    await expect(
      page.getByRole("heading", { name: "Namespace Limits" }),
    ).toBeVisible();

    // Description text should be visible
    await expect(
      page.getByText("ResourceQuota and LimitRange management"),
    ).toBeVisible();
  });

  // ── Dashboard Table ─────────────────────────────────────────────

  test("should display dashboard table with namespace rows", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    // Wait for loading to finish
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Either a table with data or an empty state message should appear
    const table = page.getByRole("table");
    const emptyState = page.getByText("No namespaces found");

    await expect(table.or(emptyState)).toBeVisible({ timeout: 15_000 });
  });

  test("should display summary cards with counts", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Summary cards should be visible
    await expect(page.getByText("With Quota")).toBeVisible();
    await expect(page.getByText("Warning")).toBeVisible();
    await expect(page.getByText("Critical")).toBeVisible();
    await expect(page.getByText("No Quota")).toBeVisible();
  });

  test("should have table headers for key columns", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Check for table column headers
    const table = page.getByRole("table");
    const tableVisible = await table.isVisible().catch(() => false);

    if (tableVisible) {
      await expect(page.getByRole("columnheader", { name: "Namespace" }))
        .toBeVisible();
      await expect(page.getByRole("columnheader", { name: "CPU" }))
        .toBeVisible();
      await expect(page.getByRole("columnheader", { name: "Memory" }))
        .toBeVisible();
      await expect(page.getByRole("columnheader", { name: "Status" }))
        .toBeVisible();
    }
  });

  // ── Filter Dropdown ─────────────────────────────────────────────

  test("should have status filter dropdown", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Find the status filter select
    const statusFilter = page.locator("select");
    await expect(statusFilter).toBeVisible();

    // Verify filter options exist
    await expect(statusFilter.locator("option[value='all']")).toBeAttached();
    await expect(statusFilter.locator("option[value='ok']")).toBeAttached();
    await expect(statusFilter.locator("option[value='warning']"))
      .toBeAttached();
    await expect(statusFilter.locator("option[value='critical']"))
      .toBeAttached();
    await expect(
      statusFilter.locator("option[value='no-quota']"),
    ).toBeAttached();
  });

  test("should filter namespaces by status", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Select "OK" status filter
    const statusFilter = page.locator("select");
    await statusFilter.selectOption("ok");

    // After filtering, either we have rows with OK status or empty state
    // The filter is applied and page should not error
    await expect(
      page.getByRole("table").or(page.getByText("No namespaces found")),
    ).toBeVisible();
  });

  // ── Search Bar ──────────────────────────────────────────────────

  test("should have search bar for filtering namespaces", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Search bar should be present
    const searchInput = page.getByPlaceholder("Search namespaces...");
    await expect(searchInput).toBeVisible();

    // Type in search bar
    await searchInput.fill("default");

    // Table should still be visible (either with results or empty)
    await expect(
      page.getByRole("table").or(page.getByText("No namespaces found")),
    ).toBeVisible();
  });

  // ── Slide-out Panel ─────────────────────────────────────────────

  test("should open slide-out panel when clicking a namespace row", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Check if there are any table rows to click
    const table = page.getByRole("table");
    const tableVisible = await table.isVisible().catch(() => false);

    if (!tableVisible) {
      // No data, skip this test
      return;
    }

    // Get the first data row (skip header)
    const rows = page.locator("tbody tr");
    const rowCount = await rows.count();

    if (rowCount === 0) {
      // Empty table, skip
      return;
    }

    // Click the first row
    await rows.first().click();

    // Slide-out panel should appear with "Namespace Details"
    await expect(page.getByText("Namespace Details")).toBeVisible({
      timeout: 5_000,
    });

    // Panel should show ResourceQuotas and LimitRanges sections
    await expect(page.getByText("ResourceQuotas")).toBeVisible();
    await expect(page.getByText("LimitRanges")).toBeVisible();
  });

  test("should close slide-out panel when clicking close button", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Check if there are any table rows to click
    const rows = page.locator("tbody tr");
    const rowCount = await rows.count();

    if (rowCount === 0) {
      return;
    }

    // Click the first row to open panel
    await rows.first().click();
    await expect(page.getByText("Namespace Details")).toBeVisible({
      timeout: 5_000,
    });

    // Click the close button
    const closeButton = page.getByLabel("Close panel");
    await closeButton.click();

    // Panel should be closed
    await expect(page.getByText("Namespace Details")).not.toBeVisible();
  });

  // ── Create Limits Button ────────────────────────────────────────

  test("should have Create Limits button", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const createButton = page.getByRole("link", { name: "Create Limits" });
    await expect(createButton).toBeVisible();
  });

  test("should navigate to wizard when clicking Create Limits", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const createButton = page.getByRole("link", { name: "Create Limits" });
    await createButton.click();

    // Should navigate to the wizard page
    await expect(page).toHaveURL("/config/namespace-limits/new");
  });

  // ── Refresh Button ──────────────────────────────────────────────

  test("should have refresh button", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const refreshButton = page.getByRole("button", { name: "Refresh" });
    await expect(refreshButton).toBeVisible();
  });

  test("should refresh data when clicking refresh button", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const refreshButton = page.getByRole("button", { name: "Refresh" });
    await refreshButton.click();

    // Button should show "Refreshing..." temporarily
    await expect(
      page.getByRole("button", { name: "Refreshing..." }),
    ).toBeVisible();

    // Then return to "Refresh"
    await expect(refreshButton).toBeVisible({ timeout: 5_000 });
  });

  // ── Wizard ──────────────────────────────────────────────────────

  test("should display wizard with preset selection", async ({ page }) => {
    await page.goto("/config/namespace-limits/new");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Wizard should have preset options
    await expect(page.getByText("Small")).toBeVisible();
    await expect(page.getByText("Standard")).toBeVisible();
    await expect(page.getByText("Large")).toBeVisible();
    await expect(page.getByText("Custom")).toBeVisible();
  });

  test("should have namespace selector in wizard", async ({ page }) => {
    await page.goto("/config/namespace-limits/new");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Namespace selector should be present
    await expect(page.getByText("Namespace")).toBeVisible();
  });

  test("should navigate through wizard steps", async ({ page }) => {
    await page.goto("/config/namespace-limits/new");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Select a preset (Standard)
    await page.getByText("Standard").click();

    // Find and click Next button
    const nextButton = page.getByRole("button", { name: "Next" });
    if (await nextButton.isVisible().catch(() => false)) {
      await nextButton.click();

      // Should be on step 2 (Quota Values)
      await expect(page.getByText("CPU Hard Limit")).toBeVisible({
        timeout: 5_000,
      });
    }
  });

  test("should show YAML preview in final wizard step", async ({ page }) => {
    await page.goto("/config/namespace-limits/new");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Select a preset
    await page.getByText("Standard").click();

    // Navigate through steps to review
    const nextButton = page.getByRole("button", { name: "Next" });

    // Step 1 -> 2
    if (await nextButton.isVisible().catch(() => false)) {
      await nextButton.click();
      await expect(page.locator(".animate-spin")).not.toBeVisible({
        timeout: 5_000,
      });
    }

    // Step 2 -> 3
    if (await nextButton.isVisible().catch(() => false)) {
      await nextButton.click();
      await expect(page.locator(".animate-spin")).not.toBeVisible({
        timeout: 5_000,
      });
    }

    // Step 3 -> 4 (Review)
    if (await nextButton.isVisible().catch(() => false)) {
      await nextButton.click();
      await expect(page.locator(".animate-spin")).not.toBeVisible({
        timeout: 5_000,
      });
    }

    // Review step should show YAML content
    await expect(
      page.getByText("ResourceQuota").or(page.getByText("kind:")),
    ).toBeVisible({ timeout: 5_000 });
  });

  // ── SubNav Integration ──────────────────────────────────────────

  test("should show Config SubNav with correct tabs", async ({ page }) => {
    await page.goto("/config/namespace-limits");

    // Verify Config SubNav tabs are present
    const namespacesLink = page.getByRole("link", { name: "Namespaces" });
    const limitsLink = page.getByRole("link", { name: "Namespace Limits" });

    await expect(namespacesLink).toBeVisible();
    await expect(limitsLink).toBeVisible();
  });
});
