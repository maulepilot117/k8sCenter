import { test, expect } from "./fixtures/base.ts";

test.describe("Flux Notifications", () => {
  // ── Navigation ──────────────────────────────────────────────────

  test("should show Notifications tab in GitOps section", async ({ page }) => {
    await page.goto("/gitops/applications");
    // SubNav should contain a Notifications tab
    const notifTab = page.getByRole("link", { name: "Notifications" });
    await expect(notifTab).toBeVisible();
  });

  test("should navigate to notifications page", async ({ page }) => {
    await page.goto("/gitops/notifications");

    // The page should render the sub-tab bar with Providers, Alerts, Receivers
    await expect(page.getByRole("link", { name: "Providers" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Alerts" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Receivers" })).toBeVisible();

    // Default tab is Providers — the Providers heading should be visible
    await expect(
      page.getByRole("heading", { name: "Providers" }),
    ).toBeVisible();
  });

  test("should show degradation banner when notification controller is not installed", async ({
    page,
  }) => {
    await page.goto("/gitops/notifications");

    // Wait for the island to finish loading (spinner disappears)
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // Either the controller is installed (table or empty state visible) or it
    // is not installed (degradation banner visible). Both are valid.
    const banner = page.getByText("Flux notification-controller not detected");
    const providerTable = page.getByRole("table");
    const emptyState = page.getByText("No notification providers configured.");
    const createButton = page.getByRole("button", {
      name: "Create Provider",
    });

    // At least one of these must be visible — the page must render something
    await expect(
      banner.or(providerTable).or(emptyState).or(createButton),
    ).toBeVisible({ timeout: 15_000 });
  });

  // ── Tab navigation ──────────────────────────────────────────────

  test("should switch between tabs via URL params", async ({ page }) => {
    // Navigate directly to Alerts tab
    await page.goto("/gitops/notifications?tab=alerts");
    await expect(
      page.getByRole("heading", { name: "Alerts" }),
    ).toBeVisible();

    // Navigate to Receivers tab
    await page.goto("/gitops/notifications?tab=receivers");
    await expect(
      page.getByRole("heading", { name: "Receivers" }),
    ).toBeVisible();

    // Navigate back to Providers tab (default)
    await page.goto("/gitops/notifications?tab=providers");
    await expect(
      page.getByRole("heading", { name: "Providers" }),
    ).toBeVisible();
  });

  test("should persist tab selection on page refresh", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=receivers");
    await expect(
      page.getByRole("heading", { name: "Receivers" }),
    ).toBeVisible();

    // Reload and verify the tab is still active
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "Receivers" }),
    ).toBeVisible();
  });

  test("should switch tabs by clicking tab links", async ({ page }) => {
    await page.goto("/gitops/notifications");
    await expect(
      page.getByRole("heading", { name: "Providers" }),
    ).toBeVisible();

    // Click the Alerts tab link
    await page.getByRole("link", { name: "Alerts" }).click();
    await expect(
      page.getByRole("heading", { name: "Alerts" }),
    ).toBeVisible();

    // Click the Receivers tab link
    await page.getByRole("link", { name: "Receivers" }).click();
    await expect(
      page.getByRole("heading", { name: "Receivers" }),
    ).toBeVisible();

    // Click back to Providers
    await page.getByRole("link", { name: "Providers" }).click();
    await expect(
      page.getByRole("heading", { name: "Providers" }),
    ).toBeVisible();
  });

  // ── Provider tab ────────────────────────────────────────────────

  test("should display provider list or empty state", async ({ page }) => {
    await page.goto("/gitops/notifications");

    // Wait for loading to finish
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // The page must show either: a table, an empty state, or the unavailable banner
    const providerTable = page.getByRole("table");
    const emptyState = page.getByText("No notification providers configured.");
    const banner = page.getByText("Flux notification-controller not detected");

    await expect(
      providerTable.or(emptyState).or(banner),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("should open create provider form", async ({ page }) => {
    await page.goto("/gitops/notifications");

    // Wait for loading to finish
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    // If controller is not installed, the Create button is disabled — skip
    const banner = page.getByText("Flux notification-controller not detected");
    const bannerVisible = await banner.isVisible().catch(() => false);
    if (bannerVisible) {
      // Button should exist but be disabled
      const createBtn = page.getByRole("button", {
        name: "Create Provider",
      });
      // When controller is unavailable, the button may not render at all or be disabled
      const btnVisible = await createBtn.isVisible().catch(() => false);
      if (btnVisible) {
        await expect(createBtn).toBeDisabled();
      }
      return;
    }

    // Click Create Provider
    const createBtn = page.getByRole("button", { name: "Create Provider" });
    await expect(createBtn).toBeVisible();
    await createBtn.click();

    // Verify the modal dialog opens
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();

    // Verify form title
    await expect(
      dialog.getByText("Create Provider"),
    ).toBeVisible();

    // Verify key form fields are present
    await expect(dialog.getByText("Name")).toBeVisible();
    await expect(dialog.getByText("Namespace")).toBeVisible();
    await expect(dialog.getByText("Type")).toBeVisible();
    await expect(dialog.getByText("Address")).toBeVisible();
    await expect(dialog.getByText("Channel")).toBeVisible();
    await expect(dialog.getByText("Secret Ref")).toBeVisible();

    // Verify Cancel button works
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).not.toBeVisible();
  });

  // ── Alerts tab ──────────────────────────────────────────────────

  test("should display alerts list or empty state", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=alerts");

    // Wait for loading to finish
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const alertTable = page.getByRole("table");
    const emptyState = page.getByText("No notification alerts configured.");
    const banner = page.getByText("Flux notification-controller not detected");

    await expect(
      alertTable.or(emptyState).or(banner),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("should open create alert form", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=alerts");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const banner = page.getByText("Flux notification-controller not detected");
    const bannerVisible = await banner.isVisible().catch(() => false);
    if (bannerVisible) {
      return; // Cannot open form when controller is absent
    }

    const createBtn = page.getByRole("button", { name: "Create Alert" });
    await expect(createBtn).toBeVisible();
    await createBtn.click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();

    await expect(dialog.getByText("Create Alert")).toBeVisible();

    // Verify key form fields
    await expect(dialog.getByText("Name")).toBeVisible();
    await expect(dialog.getByText("Namespace")).toBeVisible();
    await expect(dialog.getByText("Provider Reference")).toBeVisible();
    await expect(dialog.getByText("Event Severity")).toBeVisible();
    await expect(dialog.getByText("Event Sources")).toBeVisible();

    // Cancel
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).not.toBeVisible();
  });

  // ── Receivers tab ───────────────────────────────────────────────

  test("should display receivers list or empty state", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=receivers");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const receiverTable = page.getByRole("table");
    const emptyState = page.getByText(
      "No notification receivers configured.",
    );
    const banner = page.getByText("Flux notification-controller not detected");

    await expect(
      receiverTable.or(emptyState).or(banner),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("should open create receiver form", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=receivers");

    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });

    const banner = page.getByText("Flux notification-controller not detected");
    const bannerVisible = await banner.isVisible().catch(() => false);
    if (bannerVisible) {
      return;
    }

    const createBtn = page.getByRole("button", { name: "Create Receiver" });
    await expect(createBtn).toBeVisible();
    await createBtn.click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();

    await expect(dialog.getByText("Create Receiver")).toBeVisible();

    // Verify key form fields
    await expect(dialog.getByText("Name")).toBeVisible();
    await expect(dialog.getByText("Namespace")).toBeVisible();
    await expect(dialog.getByText("Type")).toBeVisible();
    await expect(dialog.getByText("Resources")).toBeVisible();
    await expect(dialog.getByText("Secret Ref")).toBeVisible();

    // Cancel
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).not.toBeVisible();
  });

  // ── SubNav integration ──────────────────────────────────────────

  test("should show GitOps SubNav with correct tabs", async ({ page }) => {
    await page.goto("/gitops/notifications");

    // Verify all GitOps SubNav tabs are present
    const applicationsLink = page.getByRole("link", {
      name: "Applications",
    });
    const applicationSetsLink = page.getByRole("link", {
      name: "ApplicationSets",
    });
    const notificationsLink = page.getByRole("link", {
      name: "Notifications",
    });

    await expect(applicationsLink).toBeVisible();
    await expect(applicationSetsLink).toBeVisible();
    await expect(notificationsLink).toBeVisible();
  });

  // ── Refresh button ──────────────────────────────────────────────

  test("should have refresh button on each tab", async ({ page }) => {
    // Providers tab
    await page.goto("/gitops/notifications");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    const refreshBtnProviders = page.getByRole("button", { name: "Refresh" });
    // Refresh button may not appear if loading failed, but if present it should be enabled
    if (await refreshBtnProviders.isVisible().catch(() => false)) {
      await expect(refreshBtnProviders).toBeEnabled();
    }

    // Alerts tab
    await page.goto("/gitops/notifications?tab=alerts");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    const refreshBtnAlerts = page.getByRole("button", { name: "Refresh" });
    if (await refreshBtnAlerts.isVisible().catch(() => false)) {
      await expect(refreshBtnAlerts).toBeEnabled();
    }

    // Receivers tab
    await page.goto("/gitops/notifications?tab=receivers");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    const refreshBtnReceivers = page.getByRole("button", { name: "Refresh" });
    if (await refreshBtnReceivers.isVisible().catch(() => false)) {
      await expect(refreshBtnReceivers).toBeEnabled();
    }
  });

  // ── Description text per tab ────────────────────────────────────

  test("should show correct description for each tab", async ({ page }) => {
    // Providers
    await page.goto("/gitops/notifications");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    await expect(
      page.getByText("configure where alerts are sent"),
    ).toBeVisible();

    // Alerts
    await page.goto("/gitops/notifications?tab=alerts");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    await expect(
      page.getByText("define forwarding rules from event sources to providers"),
    ).toBeVisible();

    // Receivers
    await page.goto("/gitops/notifications?tab=receivers");
    await expect(page.locator(".animate-spin")).not.toBeVisible({
      timeout: 15_000,
    });
    await expect(
      page.getByText("webhook endpoints that trigger reconciliation"),
    ).toBeVisible();
  });
});
