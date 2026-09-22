import type { Page } from "@playwright/test";
import type {
  DashboardLayoutConfig,
  LayoutItem,
} from "../../frontend/lib/dashboard/types.ts";
import { expect, test } from "../fixtures/base.ts";
import {
  type CatalogSourceReply,
  json,
  stubCatalogSources,
  stubLayoutStore,
} from "./dashboard-layout-stub.ts";

// P5/U13: browser integration of cache, shell, palette, parameters and layout.
// Endpoint fixtures model absent operators and restricted accounts; they do
// not prove backend authorization. AE5 runs against the real Go handler in
// TestHandler_SaveLayout_UnknownWidget_Returns400 with the test database.
// Per-widget arithmetic belongs to frontend/lib/dashboard/*_test.ts; geometry
// and real-database persistence belong to dashboard-grid/layout.spec.ts.
// Layouts here are page-local stubs, including across reload. No shared
// preference is created; context teardown cleans up even on a failed assertion.

const NODES: LayoutItem = {
  instanceId: "catalog-nodes",
  id: "node-conditions",
  x: 0,
  y: 0,
  w: 4,
  h: 5,
};
const DIAGNOSTICS: LayoutItem = {
  instanceId: "catalog-diagnostics",
  id: "diagnostics-summary",
  x: 4,
  y: 0,
  w: 4,
  h: 5,
  params: { namespace: "forbidden" },
};
const widget = (page: Page, id: string) =>
  page.locator('[data-widget-id="' + id + '"]');
const cell = (page: Page, instanceId: string) =>
  page.locator('[data-instance-id="' + instanceId + '"]');
const palette = (page: Page) => page.getByTestId("widget-palette");

async function prepare(
  page: Page,
  items: LayoutItem[] = [NODES],
  replies: Record<string, CatalogSourceReply> = {},
) {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await stubCatalogSources(page, replies);
  const config: DashboardLayoutConfig = {
    schemaVersion: 1,
    scope: "overview",
    columns: 12,
    items,
  };
  return stubLayoutStore(page, { revision: 4, config });
}

async function openEditor(page: Page) {
  await expect(page.getByTestId("edit-layout")).toBeEnabled();
  await page.getByTestId("edit-layout").click();
  await expect(page.getByTestId("save-layout")).toBeDisabled();
}

async function choose(page: Page, id: string) {
  await page.getByTestId("add-widget").click();
  await page.getByTestId("widget-option-" + id).click();
}

test.describe("dashboard catalog", () => {
  test("absent features are unavailable while other widgets render, and cannot be added (AE1)", async ({
    page,
  }) => {
    const writes = await prepare(
      page,
      [
        NODES,
        {
          instanceId: "catalog-policy",
          id: "policy-compliance",
          x: 4,
          y: 0,
          w: 4,
          h: 5,
        },
      ],
      { "/policies/status": { data: { detected: "" } } },
    );
    await page.goto("/");
    const policy = widget(page, "policy-compliance");
    await expect(policy).toHaveAttribute("data-widget-state", "unavailable");
    await expect(policy.getByTestId("widget-unavailable")).toContainText(
      "Not installed on this cluster",
    );
    await expect(widget(page, "node-conditions")).toHaveAttribute(
      "data-widget-state",
      "ready",
    );
    await openEditor(page);
    await page.getByTestId("add-widget").click();
    // R3 must also work for a widget whose data has never been requested.
    const blocked = page.getByTestId("widget-option-policy-violations");
    await expect(blocked).toHaveAttribute("aria-disabled", "true");
    await expect(blocked).toContainText("Not installed on this cluster");
    await blocked.click({ force: true });
    await expect(palette(page)).toBeVisible();
    await expect(page.getByTestId("grid-item")).toHaveCount(2);
    expect(writes).toHaveLength(0);
  });

  test("an installed policy engine with null findings is clear, distinct from a failed read (AE2)", async ({
    page,
  }) => {
    await prepare(
      page,
      [
        NODES,
        {
          instanceId: "catalog-violations",
          id: "policy-violations",
          x: 4,
          y: 0,
          w: 4,
          h: 5,
        },
        { ...DIAGNOSTICS, x: 8 },
      ],
      {
        "/diagnostics/forbidden/summary": { data: null, status: 500 },
      },
    );
    await page.goto("/");
    const violations = widget(page, "policy-violations");
    await expect(violations).toHaveAttribute("data-widget-state", "ready");
    await expect(
      violations.getByTestId("policy-violations-clear"),
    ).toBeVisible();
    await expect(violations.getByTestId("widget-unavailable")).toHaveCount(0);
    await expect(widget(page, "diagnostics-summary")).toHaveAttribute(
      "data-widget-state",
      "error",
    );
    await expect(
      widget(page, "diagnostics-summary").getByTestId("widget-error"),
    ).toContainText("could not be loaded");
    await expect(widget(page, "node-conditions")).toHaveAttribute(
      "data-widget-state",
      "ready",
    );
  });

  test("a non-admin sees permission copy and cannot add the admin-only widget (AE3)", async ({
    page,
  }) => {
    const writes = await prepare(
      page,
      [
        NODES,
        {
          instanceId: "catalog-audit",
          id: "audit-activity",
          x: 4,
          y: 0,
          w: 4,
          h: 5,
        },
      ],
      {
        "/auth/me": {
          data: {
            user: {
              id: "catalog-viewer",
              username: "catalog-viewer",
              provider: "local",
              kubernetesUsername: "catalog-viewer",
              kubernetesGroups: [],
              roles: ["viewer"],
            },
            rbac: { clusterScoped: {}, namespaces: {} },
          },
        },
        "/audit/logs": { data: null, status: 403 },
      },
    );
    await page.goto("/");
    const audit = widget(page, "audit-activity");
    await expect(audit).toHaveAttribute("data-widget-state", "permission");
    await expect(audit).toContainText("You do not have access");
    await expect(
      audit.getByRole("button", { name: /retry|try again/i }),
    ).toHaveCount(0);
    await openEditor(page);
    await cell(page, "catalog-audit").getByTestId("remove-widget").click();
    await page.getByTestId("add-widget").click();
    const blocked = page.getByTestId("widget-option-audit-activity");
    await expect(blocked).toHaveAttribute("aria-disabled", "true");
    await expect(blocked).toContainText("Not permitted for this account");
    await blocked.click({ force: true });
    await expect(palette(page)).toBeVisible();
    await expect(widget(page, "audit-activity")).toHaveCount(0);
    expect(writes).toHaveLength(0);
  });

  test("adding a parameterized widget waits for confirmation, and Cancel adds nothing", async ({
    page,
  }) => {
    const writes = await prepare(page);
    await page.goto("/");
    await openEditor(page);
    await choose(page, "diagnostics-summary");
    await expect(page.getByTestId("widget-param-dialog")).toBeVisible();
    await expect(widget(page, "diagnostics-summary")).toHaveCount(0);
    await page.getByTestId("confirm-widget-params").click();
    await expect(page.getByTestId("widget-param-error")).toContainText(
      "Choose a namespace",
    );
    await page.getByTestId("widget-param-namespace").selectOption("prod");
    await page.getByTestId("cancel-widget-params").click();
    await expect(page.getByTestId("widget-param-dialog")).toHaveCount(0);
    await expect(page.getByTestId("grid-item")).toHaveCount(1);
    await expect(page.getByTestId("save-layout")).toBeDisabled();
    expect(writes).toHaveLength(0);

    await choose(page, "diagnostics-summary");
    await page.getByTestId("widget-param-namespace").selectOption("prod");
    await page.getByTestId("confirm-widget-params").click();
    await expect(widget(page, "diagnostics-summary")).toHaveAttribute(
      "data-widget-state",
      "ready",
    );
    await page.getByTestId("save-layout").click();
    await expect.poll(() => writes.length).toBe(1);
    const placed = writes[0].config.items.find(
      (item) => item.id === "diagnostics-summary",
    );
    expect(placed?.params).toEqual({ namespace: "prod" });
    await expect(page.getByTestId("edit-layout")).toBeEnabled();
    await page.reload();
    await expect(page.getByTestId("diagnostics-namespace")).toHaveText("prod");
    await expect(cell(page, placed!.instanceId)).toBeVisible();
  });

  for (const narrow of [false, true]) {
    test(
      "a refused namespace can be changed without losing placement" +
        (narrow ? " at mobile width" : "") +
        " (AE4)",
      async ({ page }) => {
        const writes = await prepare(page, [NODES, DIAGNOSTICS], {
          "/diagnostics/forbidden/summary": { data: null, status: 403 },
        });
        if (narrow) await page.setViewportSize({ width: 400, height: 900 });
        await page.goto("/");
        await expect(widget(page, "diagnostics-summary")).toHaveAttribute(
          "data-widget-state",
          "permission",
        );
        await openEditor(page);
        const target = cell(page, DIAGNOSTICS.instanceId);
        const geometry = await target.evaluate((el) => ({
          column: (el as HTMLElement).style.gridColumn,
          row: (el as HTMLElement).style.gridRow,
        }));
        // The repair control is keyboard-operable while the card is refused.
        await target.getByTestId("configure-widget").focus();
        await page.keyboard.press("Enter");
        await expect(page.getByTestId("widget-param-namespace")).toHaveValue(
          "forbidden",
        );
        await page
          .getByTestId("widget-param-namespace")
          .selectOption("staging");
        await page.getByTestId("confirm-widget-params").click();
        await expect(page.getByTestId("diagnostics-namespace")).toHaveText(
          "staging",
        );
        await page.getByTestId("save-layout").click();
        await expect.poll(() => writes.length).toBe(1);
        expect(
          writes[0].config.items.find(
            (item) => item.instanceId === DIAGNOSTICS.instanceId,
          ),
        ).toEqual({
          ...DIAGNOSTICS,
          params: { namespace: "staging" },
        });
        await expect(page.getByTestId("edit-layout")).toBeEnabled();
        await page.reload();
        await expect(page.getByTestId("diagnostics-namespace")).toHaveText(
          "staging",
        );
        await expect
          .poll(() =>
            target.evaluate((el) => ({
              column: (el as HTMLElement).style.gridColumn,
              row: (el as HTMLElement).style.gridRow,
            })),
          )
          .toEqual(geometry);
        if (narrow) {
          await expect(page.getByTestId("dashboard-grid")).toHaveAttribute(
            "data-grid-mode",
            "narrow",
          );
          expect(
            await page.evaluate(
              () => document.documentElement.scrollWidth - window.innerWidth,
            ),
          ).toBeLessThanOrEqual(1);
        }
      },
    );
  }

  test("service choices follow the namespace and a forbidden namespace has no free-text fallback", async ({
    page,
  }) => {
    const writes = await prepare(page);
    await page.goto("/");
    await openEditor(page);
    await choose(page, "mesh-golden-signals");
    const namespace = page.getByTestId("widget-param-namespace");
    const service = page.getByTestId("widget-param-service");
    await expect(service).toBeDisabled();
    await namespace.selectOption("prod");
    await expect(service).toBeEnabled();
    await service.selectOption("checkout");
    await namespace.selectOption("staging");
    await expect(service).toHaveValue("");
    await expect(service.locator('option[value="checkout"]')).toHaveCount(0);
    await expect(service.locator('option[value="preview"]')).toHaveCount(1);
    await page.getByTestId("confirm-widget-params").click();
    await expect(page.getByTestId("widget-param-error")).toContainText(
      "Choose a service",
    );
    await namespace.selectOption("forbidden");
    await expect(service).toBeDisabled();
    await expect(
      page.getByTestId("widget-param-dialog").locator("input"),
    ).toHaveCount(0);
    await page.getByTestId("cancel-widget-params").click();
    await expect(widget(page, "mesh-golden-signals")).toHaveCount(0);
    expect(writes).toHaveLength(0);
  });

  test("expensive widget reads are bounded and the queue makes progress", async ({
    page,
  }) => {
    const items = Array.from(
      { length: 8 },
      (_, index): LayoutItem => ({
        instanceId: "load-" + index,
        id: "diagnostics-summary",
        x: (index % 3) * 4,
        y: (Math.floor(index / 3) + 1) * 5,
        w: 4,
        h: 5,
        params: { namespace: "load-" + index },
      }),
    );
    await prepare(page, [NODES, ...items]);
    const releases: Array<() => void> = [];
    let active = 0;
    let peak = 0;
    let started = 0;
    let draining = false;
    await page.route("**/api/v1/diagnostics/load-*/summary", async (route) => {
      started++;
      active++;
      peak = Math.max(peak, active);
      try {
        if (!draining)
          await new Promise<void>((resolve) => releases.push(resolve));
        await json(route, 200, { data: { total: 2, failing: [] } });
      } finally {
        active--;
      }
    });
    try {
      await page.goto("/");
      // Pin the shipped bound, rather than deriving an assertion from the
      // implementation and passing when the limiter itself is removed.
      await expect.poll(() => started).toBe(6);
      // Negative observation: queued requests must stay queued while six are
      // held, not merely arrive a little later than the first six.
      await page.waitForTimeout(250);
      expect(started).toBe(6);
      expect(peak).toBe(6);
      releases.shift()!();
      await expect.poll(() => started).toBe(7);
      expect(peak).toBeLessThanOrEqual(6);
    } finally {
      draining = true;
      for (const release of releases) release();
    }
    await expect(page.locator('[data-widget-state="ready"]')).toHaveCount(9);
    expect(started).toBe(8);
    expect(peak).toBeLessThanOrEqual(6);
  });

  test("search reaches a widget from each of the eight catalog families", async ({
    page,
  }) => {
    await prepare(page);
    await page.goto("/");
    await openEditor(page);
    await page.getByTestId("add-widget").click();
    await expect(palette(page).getByRole("option")).toHaveCount(38);
    for (const [id, query] of [
      ["cpu-tile", "CPU"],
      ["pending-pods", "Pending"],
      ["diagnostics-summary", "Namespace Diagnostics"],
      ["policy-violations", "Policy Violations"],
      ["gitops-app-health", "GitOps"],
      ["velero-backups", "Backup"],
      ["hubble-flows", "Network Flows"],
      ["audit-activity", "Audit"],
    ]) {
      await palette(page).getByRole("combobox").fill(query);
      await expect(page.getByTestId("widget-option-" + id)).toBeVisible();
      expect(await palette(page).getByRole("option").count()).toBeLessThan(38);
    }
  });
});
