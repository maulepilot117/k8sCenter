import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type {
  APIRequestContext,
  Locator,
  Page,
  PlaywrightWorkerArgs,
} from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { bearerHeaders, e2eName } from "../helpers.ts";

/**
 * Namespace Limits: the ResourceQuota/LimitRange dashboard and its wizard.
 *
 * Built only on core Kubernetes objects, so it runs fully on the CI kind
 * cluster. The dashboard lists only namespaces that carry a ResourceQuota or
 * a LimitRange, and kind's own namespaces carry neither, so the suite creates
 * two throwaway namespaces of its own: one with a quota and a limit range,
 * one with a limit range only (the "No Quota" case).
 */

const DASHBOARD = "/config/namespace-limits";
const WIZARD = "/config/namespace-limits/new";

const QUOTA_NS = e2eName("nslimits-quota");
const LIMITS_ONLY_NS = e2eName("nslimits-lr");

const NAMESPACES_YAML = [QUOTA_NS, LIMITS_ONLY_NS]
  .map(
    (name) =>
      `apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ${name}\n  labels:\n    e2e: "true"`,
  )
  .join("\n---\n");

function limitRangeYaml(namespace: string): string {
  return `apiVersion: v1
kind: LimitRange
metadata:
  name: e2e-limits
  namespace: ${namespace}
  labels:
    e2e: "true"
spec:
  limits:
    - type: Container
      default:
        cpu: 200m
        memory: 128Mi
      defaultRequest:
        cpu: 100m
        memory: 64Mi`;
}

const OBJECTS_YAML = [
  `apiVersion: v1
kind: ResourceQuota
metadata:
  name: e2e-quota
  namespace: ${QUOTA_NS}
  labels:
    e2e: "true"
spec:
  hard:
    pods: "10"`,
  limitRangeYaml(QUOTA_NS),
  limitRangeYaml(LIMITS_ONLY_NS),
].join("\n---\n");

/**
 * An API client authenticated as the suite's admin, for fixtures a hook sets
 * up before any page exists. Reads the token fixtures/auth.setup.ts saved.
 */
async function adminApi(
  playwright: PlaywrightWorkerArgs["playwright"],
  baseURL: string | undefined,
): Promise<APIRequestContext> {
  const authFile = join(
    dirname(fileURLToPath(import.meta.url)),
    "../playwright/.auth/admin.json",
  );
  const state = JSON.parse(readFileSync(authFile, "utf8")) as {
    origins: { localStorage: { name: string; value: string }[] }[];
  };
  const token = state.origins
    .flatMap((o) => o.localStorage)
    .find((e) => e.name === "e2e_access_token")?.value;
  if (!token) throw new Error(`no e2e_access_token in ${authFile}`);
  return await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: bearerHeaders(token),
  });
}

async function applyYaml(api: APIRequestContext, yaml: string) {
  const res = await api.post("/api/v1/yaml/apply", {
    headers: { "Content-Type": "text/yaml" },
    data: yaml,
  });
  expect(res.ok(), `yaml apply: ${res.status()} ${await res.text()}`).toBe(
    true,
  );
  expect((await res.json()).data.summary.failed).toBe(0);
}

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

/** The row for one namespace. */
function rowFor(page: Page, namespace: string): Locator {
  return dataRows(page).filter({
    has: page.locator("td:first-child", { hasText: namespace }),
  });
}

/** The dashboard's status filter (not the top bar's namespace selector). */
function statusFilter(page: Page): Locator {
  return page.locator("select:has(option[value='no-quota'])");
}

/** The namespace-column texts across the visible data rows. */
async function namespaceColumn(page: Page): Promise<string[]> {
  return await dataRows(page).locator("td:first-child").allInnerTexts();
}

test.describe("Namespace Limits", () => {
  test.beforeAll(async ({ playwright }, testInfo) => {
    const api = await adminApi(playwright, testInfo.project.use.baseURL);
    try {
      await applyYaml(api, NAMESPACES_YAML);
      await applyYaml(api, OBJECTS_YAML);
      // The dashboard reads the informer cache through a 30s summary cache,
      // so the new namespaces can take that long to appear.
      await expect
        .poll(
          async () => {
            const res = await api.get("/api/v1/limits/namespaces");
            if (!res.ok()) return [];
            return ((await res.json()).data as { namespace: string }[]).map(
              (s) => s.namespace,
            );
          },
          { timeout: 45_000, intervals: [2_000, 3_000, 5_000] },
        )
        .toEqual(expect.arrayContaining([QUOTA_NS, LIMITS_ONLY_NS]));
    } finally {
      await api.dispose();
    }
  });

  test.afterAll(async ({ playwright }, testInfo) => {
    const api = await adminApi(playwright, testInfo.project.use.baseURL);
    try {
      for (const name of [QUOTA_NS, LIMITS_ONLY_NS]) {
        const res = await api.delete(`/api/v1/resources/namespaces/${name}`);
        // 404: already gone, which is what cleanup wants.
        expect(
          res.ok() || res.status() === 404,
          `delete namespace ${name}: ${res.status()}`,
        ).toBe(true);
      }
    } finally {
      await api.dispose();
    }
  });

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

  test("lists namespaces that carry a quota or a limit range", async ({
    page,
  }) => {
    await openDashboard(page);
    await expect(rowFor(page, QUOTA_NS)).toHaveCount(1);
    await expect(rowFor(page, LIMITS_ONLY_NS)).toHaveCount(1);
    // kind's own namespaces carry neither, so they are not listed.
    expect(await namespaceColumn(page)).not.toContain("kube-system");
  });

  test("shows the summary tiles", async ({ page }) => {
    await openDashboard(page);
    // The tiles precede the status filter, whose <option>s carry the same
    // words, so the first exact match is the tile label.
    for (const label of ["With Quota", "Warning", "Critical", "No Quota"]) {
      await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
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
    // Expected rows per filter, from the very response the dashboard renders
    // (a second fetch could race a namespace another spec is deleting).
    // Comparing whole name lists, not per-row checks over whatever rendered,
    // means a filter that does nothing, or empties the table, fails.
    const listed = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/v1/limits/namespaces" &&
        r.request().method() === "GET",
    );
    await openDashboard(page);
    const res = await listed;
    expect(res.ok(), `limits list: ${res.status()}`).toBe(true);
    const summaries = (await res.json()).data as {
      namespace: string;
      status: string;
      hasQuota: boolean;
    }[];
    const expected: Record<string, string[]> = {
      all: summaries.map((s) => s.namespace),
      ok: summaries.filter((s) => s.status === "ok").map((s) => s.namespace),
      warning: summaries
        .filter((s) => s.status === "warning")
        .map((s) => s.namespace),
      critical: summaries
        .filter((s) => s.status === "critical")
        .map((s) => s.namespace),
      "no-quota": summaries.filter((s) => !s.hasQuota).map((s) => s.namespace),
    };
    // The suite's own fixtures pin down the two filters that matter here.
    expect(expected["no-quota"]).toContain(LIMITS_ONLY_NS);
    expect(expected["no-quota"]).not.toContain(QUOTA_NS);
    expect(expected.ok).toContain(QUOTA_NS);

    for (const [value, names] of Object.entries(expected)) {
      await statusFilter(page).selectOption(value);
      if (names.length === 0) {
        await expect(page.getByText("No namespaces found")).toBeVisible();
        continue;
      }
      await expect
        .poll(async () => (await namespaceColumn(page)).sort(), {
          message: `rows shown for status filter "${value}"`,
        })
        .toEqual([...names].sort());
    }
  });

  test("searches namespaces by name", async ({ page }) => {
    await openDashboard(page);
    const search = page.getByPlaceholder("Search namespaces...");

    await search.fill(LIMITS_ONLY_NS);
    await expect
      .poll(() => namespaceColumn(page))
      .toEqual([LIMITS_ONLY_NS]);

    await search.fill("no-such-namespace-e2e");
    await expect(page.getByText("No namespaces found")).toBeVisible();
  });

  // ── Slide-out panel ─────────────────────────────────────────────

  test("opens the detail panel for a namespace row", async ({ page }) => {
    await openDashboard(page);
    await rowFor(page, QUOTA_NS).click();

    await expect(page.getByText("Namespace Details")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: QUOTA_NS, level: 2 }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "ResourceQuotas (1)" }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "LimitRanges (1)" }),
    ).toBeVisible();
  });

  test("closes the detail panel with its close button", async ({ page }) => {
    await openDashboard(page);
    await rowFor(page, LIMITS_ONLY_NS).click();
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
      // Anchored: the top bar's "Select namespace" picker is labelled too.
      const select = page.getByLabel(/^Namespace\b/);
      await expect(select).toBeVisible();
      await expect(select.locator("option[value='default']")).toBeAttached();
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

      // Asserted on the response, so a failed preview (a 429 from the shared
      // wizard rate limiter, say) reports its status instead of timing out
      // on an Apply button that will never render.
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
