import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { getAuthHeaders } from "../helpers.ts";

/**
 * Flux notification-controller UI: Providers, Alerts and Receivers.
 *
 * The CI kind cluster does not install Flux, so these pages run in their
 * degraded state there: each tab shows the "notification-controller not
 * detected" banner with its Create button disabled. Tests that need a live
 * controller (the list itself, the create forms) skip with a reason when the
 * status route reports it unavailable; the degraded state has its own test,
 * which skips the other way.
 */

const TABS = [
  {
    tab: "providers",
    heading: "Providers",
    kind: "Provider",
    description: "configure where alerts are sent",
  },
  {
    tab: "alerts",
    heading: "Alerts",
    kind: "Alert",
    description: "define forwarding rules from event sources to providers",
  },
  {
    tab: "receivers",
    heading: "Receivers",
    kind: "Receiver",
    description: "webhook endpoints that trigger reconciliation",
  },
] as const;

const BANNER = "Flux notification-controller not detected";

/** Whether the backend reports the notification-controller available. */
async function controllerAvailable(page: Page): Promise<boolean> {
  const res = await page.request.get("/api/v1/gitops/notifications/status", {
    headers: await getAuthHeaders(page),
  });
  expect(res.ok(), `notification status: ${res.status()}`).toBe(true);
  return (await res.json()).data?.available === true;
}

/** Open a tab and wait for its island to finish loading. */
async function openTab(page: Page, tab: string) {
  await page.goto(`/gitops/notifications?tab=${tab}`);
  // The header's Refresh button renders only once the first fetch settles.
  await expect(page.getByRole("button", { name: "Refresh" })).toBeVisible({
    timeout: 15_000,
  });
}

function tabHeading(page: Page, name: string) {
  return page.getByRole("heading", { name, exact: true, level: 1 });
}

test.describe("Flux notifications", () => {
  // ── Navigation ──────────────────────────────────────────────────

  test("shows a Notifications link in the GitOps navigation", async ({
    page,
  }) => {
    await page.goto("/gitops/applications");
    await expect(
      page.getByRole("link", { name: "Notifications", exact: true }),
    ).toBeVisible();
  });

  test("shows the GitOps sub-navigation on the notifications page", async ({
    page,
  }) => {
    await page.goto("/gitops/notifications");
    for (const name of ["Applications", "ApplicationSets", "Notifications"]) {
      await expect(page.getByRole("link", { name, exact: true })).toBeVisible();
    }
  });

  test("renders the tab bar with Providers selected by default", async ({
    page,
  }) => {
    await page.goto("/gitops/notifications");
    for (const { heading } of TABS) {
      await expect(
        page.getByRole("link", { name: heading, exact: true }),
      ).toBeVisible();
    }
    await expect(tabHeading(page, "Providers")).toBeVisible();
  });

  // ── Tab navigation ──────────────────────────────────────────────

  test("selects the tab from the ?tab= parameter", async ({ page }) => {
    for (const { tab, heading } of [TABS[1], TABS[2], TABS[0]]) {
      await page.goto(`/gitops/notifications?tab=${tab}`);
      await expect(tabHeading(page, heading)).toBeVisible();
    }
  });

  test("keeps the selected tab across a reload", async ({ page }) => {
    await page.goto("/gitops/notifications?tab=receivers");
    await expect(tabHeading(page, "Receivers")).toBeVisible();
    await page.reload();
    await expect(tabHeading(page, "Receivers")).toBeVisible();
  });

  test("switches tabs by clicking the tab links", async ({ page }) => {
    await page.goto("/gitops/notifications");
    await expect(tabHeading(page, "Providers")).toBeVisible();

    for (const { tab, heading } of [TABS[1], TABS[2], TABS[0]]) {
      await page.getByRole("link", { name: heading, exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`\\?tab=${tab}$`));
      await expect(tabHeading(page, heading)).toBeVisible();
    }
  });

  // ── Per-tab chrome ──────────────────────────────────────────────

  test("shows each tab's description", async ({ page }) => {
    for (const { tab, description } of TABS) {
      await openTab(page, tab);
      await expect(page.getByText(description)).toBeVisible();
    }
  });

  test("shows an enabled Refresh button on each tab", async ({ page }) => {
    for (const { tab } of TABS) {
      await openTab(page, tab);
      await expect(page.getByRole("button", { name: "Refresh" })).toBeEnabled();
    }
  });

  // ── Controller absent ───────────────────────────────────────────

  test("shows the not-detected banner and disables Create when the controller is absent", async ({
    page,
  }) => {
    await page.goto("/gitops/notifications");
    test.skip(
      await controllerAvailable(page),
      "Flux notification-controller is installed on this cluster",
    );

    for (const { tab, kind } of TABS) {
      await openTab(page, tab);
      await expect(page.getByText(BANNER)).toBeVisible();
      await expect(
        page.getByRole("button", { name: `Create ${kind}`, exact: true }),
      ).toBeDisabled();
      // Not-installed is not a load failure: no error text, no table.
      await expect(
        page.getByText(`Failed to load notification ${kind.toLowerCase()}s`),
      ).toBeHidden();
      await expect(page.getByRole("table")).toBeHidden();
    }
  });

  // ── Controller present ──────────────────────────────────────────

  test.describe("with the controller installed", () => {
    test.beforeEach(async ({ page }) => {
      await page.goto("/gitops/notifications");
      test.skip(
        !(await controllerAvailable(page)),
        "Flux notification-controller is not installed on this cluster",
      );
    });

    for (const { tab, kind } of TABS) {
      test(`lists ${tab} or shows the empty state`, async ({ page }) => {
        await openTab(page, tab);
        const table = page.getByRole("table");
        const empty = page.getByText(
          `No notification ${kind.toLowerCase()}s configured.`,
        );
        await expect(table.or(empty).first()).toBeVisible();
        await expect(page.getByText(BANNER)).toBeHidden();
      });
    }

    test("opens and cancels the create provider form", async ({ page }) => {
      await openTab(page, "providers");
      await page
        .getByRole("button", { name: "Create Provider", exact: true })
        .first()
        .click();

      const dialog = page.getByRole("dialog", { name: "Create Provider" });
      await expect(dialog).toBeVisible();
      for (const label of [
        "Name",
        "Namespace",
        "Type",
        "Address",
        "Channel",
        "Secret Ref",
      ]) {
        await expect(dialog.getByText(label, { exact: true })).toBeVisible();
      }

      await dialog.getByRole("button", { name: "Cancel" }).click();
      await expect(dialog).toBeHidden();
    });

    test("opens and cancels the create alert form", async ({ page }) => {
      await openTab(page, "alerts");
      await page
        .getByRole("button", { name: "Create Alert", exact: true })
        .first()
        .click();

      const dialog = page.getByRole("dialog", { name: "Create Alert" });
      await expect(dialog).toBeVisible();
      for (const label of [
        "Name",
        "Namespace",
        "Provider Reference",
        "Event Severity",
        "Event Sources",
      ]) {
        await expect(dialog.getByText(label, { exact: true })).toBeVisible();
      }

      await dialog.getByRole("button", { name: "Cancel" }).click();
      await expect(dialog).toBeHidden();
    });

    test("opens and cancels the create receiver form", async ({ page }) => {
      await openTab(page, "receivers");
      await page
        .getByRole("button", { name: "Create Receiver", exact: true })
        .first()
        .click();

      const dialog = page.getByRole("dialog", { name: "Create Receiver" });
      await expect(dialog).toBeVisible();
      for (const label of [
        "Name",
        "Namespace",
        "Type",
        "Resources",
        "Secret Ref",
      ]) {
        await expect(dialog.getByText(label, { exact: true })).toBeVisible();
      }

      await dialog.getByRole("button", { name: "Cancel" }).click();
      await expect(dialog).toBeHidden();
    });
  });
});
