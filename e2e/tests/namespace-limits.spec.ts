import type { Locator, Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";

/**
 * Namespace Limits: the ResourceQuota/LimitRange dashboard and its wizard.
 *
 * Built only on core Kubernetes objects, so it runs fully on the CI kind
 * cluster: the dashboard lists kind's own namespaces (default, kube-system,
 * ...), none of which carry a ResourceQuota.
 */

const DASHBOARD = "/config/namespace-limits";
const WIZARD = "/config/namespace-limits/new";

/** Open the dashboard and wait for its first fetch to settle. */
async function openDashboard(page: Page) {
  await page.goto(DASHBOARD);
  // The header buttons render only once loading finishes.
  await expect(page.getByRole("button", { name: "Refresh" })).toBeVisible({
    timeout: 15_000,
  });
}

/** Data rows of the dashboard table (excludes the "No namespaces found" row). */
function dataRows(page: Page): Locator {
  return page
    .getByRole("table")
    .locator("tbody tr")
    .filter({ hasNotText: "No namespaces found" });
}

/** The dashboard's status filter (not the top bar's namespace selector). */
function statusFilter(page: Page): Locator {
  return page.locator("select:has(option[value='no-quota'])");
}

/** The cell texts of one column (0-based) across the visible data rows. */
async function columnTexts(page: Page, column: number): Promise<string[]> {
  return await dataRows(page)
    .locator(`td:nth-child(${column + 1})`)
    .allInnerTexts();
}

const COLUMNS = {
  namespace: 0,
  status: 4,
  quotas: 5,
} as const;

test.describe("Namespace Limits", () => {
  // ── Navigation ──────────────────────────────────────────────────

  test("shows a Namespace Limits link in the Config navigation", async ({
    page,
  }) => {
    await page.goto("/config/configmaps");
    const link = page.getByRole("link", {
      name: "Namespace Limits",
      exact: true,
    });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute("href", DASHBOARD);
  });

  test("shows the Config sub-navigation on the dashboard", async ({ page }) => {
    await page.goto(DASHBOARD);
    for (const name of ["ConfigMaps", "Resource Quotas", "Namespace Limits"]) {
      await expect(page.getByRole("link", { name })).toBeVisible();
    }
  });

  test("renders the dashboard heading and description", async ({ page }) => {
    await openDashboard(page);
    await expect(
      page.getByRole("heading", { name: "Namespace Limits", level: 1 }),
    ).toBeVisible();
    await expect(
      page.getByText("ResourceQuota and LimitRange management"),
    ).toBeVisible();
  });

  // ── Dashboard table ─────────────────────────────────────────────

  test("lists the cluster's namespaces", async ({ page }) => {
    await openDashboard(page);
    await expect(dataRows(page).first()).toBeVisible();
    expect(await columnTexts(page, COLUMNS.namespace)).toContain("default");
  });

  test("shows the summary tiles", async ({ page }) => {
    await openDashboard(page);
    // Tile labels; exact so the status filter's <option>s do not match.
    for (const label of ["With Quota", "Warning", "Critical", "No Quota"]) {
      await expect(
        page.locator("div").getByText(label, { exact: true }).first(),
      ).toBeVisible();
    }
  });

  test("has table headers for the key columns", async ({ page }) => {
    await openDashboard(page);
    for (const name of ["Namespace", "CPU", "Memory", "Status"]) {
      await expect(
        page.getByRole("columnheader", { name, exact: true }),
      ).toBeVisible();
    }
  });

  // ── Filters ─────────────────────────────────────────────────────

  test("has a status filter with every status", async ({ page }) => {
    await openDashboard(page);
    const filter = statusFilter(page);
    await expect(filter).toBeVisible();
    for (const value of ["all", "ok", "warning", "critical", "no-quota"]) {
      await expect(filter.locator(`option[value='${value}']`)).toBeAttached();
    }
  });

  test("filters namespaces by status", async ({ page }) => {
    await openDashboard(page);

    await statusFilter(page).selectOption("no-quota");
    await expect(dataRows(page).first()).toBeVisible();
    for (const quotas of await columnTexts(page, COLUMNS.quotas)) {
      expect(quotas.trim()).toBe("0");
    }

    await statusFilter(page).selectOption("ok");
    for (const status of await columnTexts(page, COLUMNS.status)) {
      expect(status.trim().toLowerCase()).toBe("ok");
    }
  });

  test("searches namespaces by name", async ({ page }) => {
    await openDashboard(page);
    await page.getByPlaceholder("Search namespaces...").fill("default");

    await expect(dataRows(page).first()).toBeVisible();
    for (const name of await columnTexts(page, COLUMNS.namespace)) {
      expect(name).toContain("default");
    }

    await page
      .getByPlaceholder("Search namespaces...")
      .fill("no-such-namespace-e2e");
    await expect(page.getByText("No namespaces found")).toBeVisible();
  });

  // ── Slide-out panel ─────────────────────────────────────────────

  test("opens the detail panel for a namespace row", async ({ page }) => {
    await openDashboard(page);
    await dataRows(page)
      .filter({ hasText: "default" })
      .first()
      .click();

    await expect(page.getByText("Namespace Details")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: /^ResourceQuotas \(\d+\)$/ }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: /^LimitRanges \(\d+\)$/ }),
    ).toBeVisible();
  });

  test("closes the detail panel with its close button", async ({ page }) => {
    await openDashboard(page);
    await dataRows(page).first().click();
    await expect(page.getByText("Namespace Details")).toBeVisible();

    await page.getByRole("button", { name: "Close panel" }).click();
    await expect(page.getByText("Namespace Details")).toBeHidden();
  });

  // ── Header actions ──────────────────────────────────────────────

  test("Create Limits opens the limits wizard", async ({ page }) => {
    await openDashboard(page);
    await page.getByRole("button", { name: "Create Limits" }).click();
    await expect(
      page.getByText("Create Namespace Limits", { exact: true }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(
      page.getByText("Create Namespace Limits", { exact: true }),
    ).toBeHidden();
    await expect(page).toHaveURL(new RegExp(`${DASHBOARD}$`));
  });

  test("Refresh refetches the dashboard data", async ({ page }) => {
    await openDashboard(page);
    const refetch = page.waitForResponse(
      (res) =>
        new URL(res.url()).pathname === "/api/v1/limits/namespaces" &&
        res.request().method() === "GET",
    );
    await page.getByRole("button", { name: "Refresh" }).click();
    expect((await refetch).ok()).toBe(true);
    await expect(page.getByRole("button", { name: "Refresh" })).toBeEnabled();
  });

  // ── Wizard ──────────────────────────────────────────────────────

  test.describe("wizard", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto(WIZARD);
      await expect(
        page.getByText("Create Namespace Limits", { exact: true }),
      ).toBeVisible();
    });

    test("offers the four presets", async ({ page }) => {
      for (const preset of ["Small", "Standard", "Large", "Custom"]) {
        await expect(
          page.getByRole("button", { name: new RegExp(`^${preset}\\b`) }),
        ).toBeVisible();
      }
    });

    test("has a namespace selector", async ({ page }) => {
      await expect(page.getByLabel("Namespace", { exact: false })).toBeVisible();
    });

    test("advances to the quota values step", async ({ page }) => {
      await page.getByRole("button", { name: /^Standard\b/ }).click();
      await page.getByRole("button", { name: /^Continue/ }).click();
      await expect(page.getByText("CPU Hard Limit")).toBeVisible();
    });

    test("shows the generated YAML on the review step", async ({ page }) => {
      await page.getByRole("button", { name: /^Standard\b/ }).click();

      const preview = page.waitForResponse(
        (res) =>
          new URL(res.url()).pathname ===
            "/api/v1/wizards/namespace-limits/preview" &&
          res.request().method() === "POST",
      );
      // Namespace & Preset -> Quota Values -> LimitRange Values -> Review.
      for (let step = 0; step < 3; step++) {
        await page.getByRole("button", { name: /^Continue/ }).click();
      }

      const res = await preview;
      expect(res.ok(), `preview: ${res.status()}`).toBe(true);
      const yaml = (await res.json()).data.yaml as string;
      expect(yaml).toContain("kind: ResourceQuota");
      expect(yaml).toContain("kind: LimitRange");

      await expect(
        page.getByText("Review the generated YAML below"),
      ).toBeVisible();
      await expect(page.getByRole("button", { name: /^Apply/ })).toBeVisible();
    });
  });
});
