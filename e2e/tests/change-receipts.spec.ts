import type { Locator, Page, Route } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import {
  attachAuthInjection,
  bearerHeaders,
  e2eName,
  e2eSecureName,
  getAuthHeaders,
  postWithBackoff,
} from "../helpers.ts";

/**
 * Change receipts and pre-apply GitOps ownership (Release E U31).
 *
 * Two kinds of test live here:
 *
 * 1. Live tests against the real backend. The CI kind cluster runs with
 *    PostgreSQL, so tracked apply (U30a, `?trackedOperationId=`) records real
 *    receipts and the receipt page reads them back. These cover AE7 (a partial
 *    bundle whose receipt survives reload), repair (never prefilled),
 *    inconclusive verification of an unsupported kind, a Secret receipt and
 *    cross-user isolation.
 *
 *    The kind cluster has no Argo CD and no Flux CD, so a live ownership
 *    check there can only ever answer "no controller" (or "did not respond").
 *    The live ownership test asserts exactly that honest answer, and that
 *    nothing claims the object is managed.
 *
 * 2. Rendering tests over stubbed replies, for states the CI cluster cannot
 *    produce: a confirmed Argo owner, conflicting versus unknown ownership,
 *    an unknown execution outcome and a receipt store that answers 503.
 *    These fixtures model the wire shapes in frontend/lib/change-types.ts;
 *    they do not prove backend behaviour, which the Go tests in
 *    backend/internal/changes and backend/internal/yaml cover.
 */

const NS = "e2e-test";
const PAGE = "/tools/yaml-apply";

/**
 * The one honest answer on the CI kind cluster, which runs neither Argo CD
 * nor Flux CD. Deliberately exact: "did not respond" or "no evidence" here
 * would mean discovery regressed, and must fail rather than pass.
 */
const NO_CONTROLLER =
  "No GitOps controller (Argo CD or Flux CD) is installed on this cluster.";

const createdConfigMaps: string[] = [];

function configMap(name: string, ns = NS, value = "v1"): string {
  return [
    "apiVersion: v1",
    "kind: ConfigMap",
    "metadata:",
    `  name: ${name}`,
    `  namespace: ${ns}`,
    "  labels:",
    '    e2e: "true"',
    "data:",
    `  key: ${value}`,
  ].join("\n");
}

function secret(name: string, ns: string, value: string): string {
  return [
    "apiVersion: v1",
    "kind: Secret",
    "metadata:",
    `  name: ${name}`,
    `  namespace: ${ns}`,
    "  labels:",
    '    e2e: "true"',
    "stringData:",
    `  token: ${value}`,
  ].join("\n");
}

async function deleteConfigMap(page: Page, name: string): Promise<void> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.delete(
    `/api/v1/resources/configmaps/${NS}/${name}`,
    { headers, failOnStatusCode: false },
  );
  if (!res.ok() && res.status() !== 404) {
    throw new Error(`could not delete ConfigMap ${name}: ${res.status()}`);
  }
}

/** A tracked apply through the API, as the YAML Apply page sends it. */
async function trackedApply(
  page: Page,
  yaml: string,
  extra: Record<string, string> = {},
): Promise<{ operationId: string; state: string }> {
  const headers = await getAuthHeaders(page);
  const operationId = crypto.randomUUID();
  const query = new URLSearchParams({ trackedOperationId: operationId, ...extra });
  const res = await page.request.post(`/api/v1/yaml/apply?${query}`, {
    headers: { ...headers, "Content-Type": "text/yaml" },
    data: yaml,
  });
  if (!res.ok()) {
    throw new Error(`tracked apply failed: ${res.status()} ${await res.text()}`);
  }
  const tracking = (await res.json())?.data?.tracking;
  if (!tracking?.operationId) {
    throw new Error("tracked apply returned no tracking block (is U30a deployed?)");
  }
  return { operationId: tracking.operationId, state: tracking.state };
}

async function openYamlApply(page: Page, path = PAGE): Promise<void> {
  // Block the Monaco CDN so the plain textarea fallback renders.
  await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
  await page.goto(path);
  await expect(page.getByRole("heading", { name: "YAML Apply" })).toBeVisible();
}

async function fillYaml(page: Page, yaml: string): Promise<void> {
  const textarea = page.locator("textarea");
  await textarea.fill(yaml);
  await expect(textarea).toHaveValue(yaml);
}

/** The YAML Apply page's error banner (validate, apply or refusal errors). */
const pageError = (page: Page) => page.getByRole("alert");

/**
 * Waits for `success`, racing the page's error banner, so a 429 or any other
 * error fails the test with the on-screen cause instead of a bare timeout
 * (docs/solutions/yaml-rate-limiter-e2e-flake.md, rule 4).
 */
async function expectOrPageError(
  page: Page,
  success: Locator,
  what: string,
  timeout = 15_000,
): Promise<void> {
  const error = pageError(page);
  await expect(success.or(error).first()).toBeVisible({ timeout });
  if (!(await success.first().isVisible()) && (await error.first().isVisible())) {
    throw new Error(
      `${what}: the page showed an error instead: ${await error.first().innerText()}`,
    );
  }
}

async function validate(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Validate", exact: true }).click();
  await expectOrPageError(
    page,
    page.getByText(/resources? validated/),
    "validate",
  );
}

/** Applies and waits for the change-record panel, naming any error shown. */
async function applyAndAwaitRecord(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  const record = page.getByRole("region", { name: "Change record" });
  await expectOrPageError(page, record, "tracked apply");
  return record;
}

const trackingBox = (page: Page) =>
  page.getByRole("checkbox", { name: "Keep a record of this change" });

/**
 * Asserts tracking is on by default. When the /changes probe failed (a 429
 * on GET /v1/changes, say), the checkbox is off and the page explains why;
 * that explanation becomes the failure message instead of "not checked".
 */
async function expectTrackingOn(page: Page): Promise<void> {
  const box = trackingBox(page);
  await expect(box).toBeEnabled({ timeout: 10_000 });
  if (!(await box.isChecked())) {
    const note = page.locator("#yaml-apply-tracking-note");
    const why = (await note.count()) > 0 ? await note.innerText() : "no reason shown";
    throw new Error(`change tracking is off by default: ${why}`);
  }
}

/** Waits for the ownership list, racing the ownership-check failure line. */
async function ownershipList(page: Page): Promise<Locator> {
  const list = page.getByRole("list", { name: "GitOps ownership" });
  const failed = page
    .getByRole("region", { name: "GitOps ownership" })
    .getByRole("status");
  await expect(list.or(failed).first()).toBeVisible({ timeout: 15_000 });
  if (!(await list.isVisible())) {
    const text = await failed.first().innerText();
    if (!/Checking which GitOps controllers/.test(text)) {
      throw new Error(`ownership check failed: ${text}`);
    }
    await expect(list).toBeVisible({ timeout: 15_000 });
  }
  return list;
}

const stateRegion = (page: Page) =>
  page.getByRole("region", { name: "Change state" });

const objectsTable = (page: Page) =>
  page.getByRole("table", { name: "Objects in this change" });

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

function ownershipResult(
  object: Record<string, unknown>,
  confidence: string,
  reason: string,
  extra: Record<string, unknown> = {},
) {
  return {
    object: { clusterId: "local", ...object },
    controller: "none",
    confidence,
    reason,
    identityBasis: "group-kind-namespace-name",
    uidConfirmed: false,
    writableGitSource: false,
    observedAt: new Date().toISOString(),
    ...extra,
  };
}

const ARGO_APP = {
  appId: "argo:argocd:storefront",
  tool: "argocd",
  kind: "Application",
  namespace: "argocd",
  name: "storefront",
  source: {
    repoURL: "https://git.example.com/storefront.git",
    path: "deploy",
    targetRevision: "main",
  },
  suspended: false,
};

async function expectNoGitWriteAffordance(page: Page): Promise<void> {
  const gitWrite = /pull request|open a pr|commit|push to git|edit in git/i;
  await expect(page.getByRole("link", { name: gitWrite })).toHaveCount(0);
  await expect(page.getByRole("button", { name: gitWrite })).toHaveCount(0);
  await expect(page.getByText("git.example.com")).toHaveCount(0);
}

test.afterAll(async ({ browser }) => {
  if (createdConfigMaps.length === 0) return;
  const context = await browser.newContext({
    storageState: "playwright/.auth/admin.json",
  });
  const page = await context.newPage();
  await attachAuthInjection(page);
  await page.goto("/");
  for (const name of createdConfigMaps) await deleteConfigMap(page, name);
  await context.close();
});

// ---------------------------------------------------------------------------
// Live tests
// ---------------------------------------------------------------------------

test.describe("Change receipts (live)", () => {
  test("applies three documents, two succeed one fails, and the receipt survives reload", async ({
    page,
  }) => {
    const ok1 = e2eName("cm");
    const ok2 = e2eName("cm");
    const bad = e2eName("cm");
    const missingNs = e2eName("missing");
    createdConfigMaps.push(ok1, ok2);
    try {
      await openYamlApply(page);
      await expectTrackingOn(page);
      await fillYaml(
        page,
        [configMap(ok1), configMap(ok2), configMap(bad, missingNs)].join("\n---\n"),
      );
      await validate(page);
      const record = await applyAndAwaitRecord(page);
      await expect(record.getByText("Partially applied", { exact: true })).toBeVisible();
      await record.getByRole("link", { name: /View change receipt/ }).click();

      await expect(page).toHaveURL(/\/changes\/[0-9a-f-]{36}$/);
      await expect(
        page.getByRole("heading", { name: "Change receipt", level: 1 }),
      ).toBeVisible();
      await expect(
        stateRegion(page).getByText("Partially applied", { exact: true }),
      ).toBeVisible();
      await expect(stateRegion(page)).toContainText("3 documents: 2 created, 1 failed");
      await expect(objectsTable(page).getByRole("row")).toHaveCount(4);
      const before = await objectsTable(page).innerText();

      await page.reload();
      await expect(
        stateRegion(page).getByText("Partially applied", { exact: true }),
      ).toBeVisible();
      await expect(stateRegion(page)).toContainText("3 documents: 2 created, 1 failed");
      expect(await objectsTable(page).innerText()).toBe(before);
    } finally {
      for (const name of [ok1, ok2, bad]) await deleteConfigMap(page, name);
    }
  });

  test("applied plus pending verification renders two separate badges", async ({
    page,
  }) => {
    const name = e2eName("cm");
    createdConfigMaps.push(name);
    try {
      await openYamlApply(page);
      await expectTrackingOn(page);
      await fillYaml(page, configMap(name));
      await validate(page);
      const record = await applyAndAwaitRecord(page);
      // The apply response carries the stored verification state, which is
      // "pending" until the receipt is first verified.
      await expect(record.getByText("Applied", { exact: true })).toBeVisible();
      await expect(
        record.getByText("Verification pending", { exact: true }),
      ).toBeVisible();
      await expect(record).not.toContainText("Verified");
    } finally {
      await deleteConfigMap(page, name);
    }
  });

  test("inconclusive verification renders neutrally, not as success", async ({
    page,
  }) => {
    const name = e2eName("cm");
    createdConfigMaps.push(name);
    try {
      await page.goto("/");
      const { operationId } = await trackedApply(page, configMap(name));
      await page.goto(`/changes/${operationId}`);
      const state = stateRegion(page);
      // A ConfigMap has no readiness postcondition: the verdict is
      // inconclusive, with the reason spelled out, never a green "Verified".
      await expect(
        state.getByText("Verification inconclusive", { exact: true }),
      ).toBeVisible({ timeout: 20_000 });
      await expect(state).toContainText(
        "This kind has no supported readiness postcondition, so k8sCenter cannot verify it.",
      );
      await expect(state.getByText("Verified", { exact: true })).toHaveCount(0);
      const color = await state
        .getByText("Verification inconclusive", { exact: true })
        .evaluate((el) => getComputedStyle(el).color);
      // Exactly the neutral tone: a warn, crit or info colour would be as
      // wrong as green for "this proves nothing either way".
      const tone = (cssVar: string) =>
        page.evaluate((v) => {
          const probe = document.createElement("span");
          probe.style.color = `var(${v})`;
          document.body.appendChild(probe);
          const c = getComputedStyle(probe).color;
          probe.remove();
          return c;
        }, cssVar);
      expect(color).toBe(await tone("--text-muted"));
      expect(color).not.toBe(await tone("--success"));
    } finally {
      await deleteConfigMap(page, name);
    }
  });

  test("repair opens a fresh preview and never prefills from the receipt", async ({
    page,
  }) => {
    const good = e2eName("cm");
    const bad = e2eName("cm");
    const retry = e2eName("cm");
    createdConfigMaps.push(good, retry);
    try {
      await page.goto("/");
      const { operationId, state } = await trackedApply(
        page,
        [configMap(good), configMap(bad, e2eName("missing"))].join("\n---\n"),
      );
      expect(state).toBe("partial");

      await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
      await page.goto(`/changes/${operationId}`);
      await page.getByRole("link", { name: "Retry failed objects" }).click();

      await expect(page).toHaveURL(
        new RegExp(`${PAGE}\\?repairOf=${operationId}&cluster=local$`),
      );
      await expect(page.getByText(`Repairing change ${operationId.slice(0, 8)}.`)).toBeVisible();
      // Nothing came from the receipt: the editor still holds the placeholder.
      await expect(page.locator("textarea")).toHaveValue(/^# Paste or type/);
      await expect(page.getByRole("button", { name: "Apply", exact: true })).toBeDisabled();

      // The operator supplies current content; it is applied as a NEW change
      // that only links back to the original.
      await expectTrackingOn(page);
      await fillYaml(page, configMap(retry));
      await validate(page);
      const record = await applyAndAwaitRecord(page);
      await record.getByRole("link", { name: /View change receipt/ }).click();
      await expect(page).not.toHaveURL(new RegExp(operationId));
      await expect(
        page.getByRole("link", { name: operationId, exact: true }),
      ).toHaveAttribute("href", `/changes/${operationId}`);
    } finally {
      for (const name of [good, bad, retry]) await deleteConfigMap(page, name);
    }
  });

  test("a Secret-bearing receipt offers no content reuse and shows no error text", async ({
    page,
  }) => {
    const value = `e2e-secret-${crypto.randomUUID()}`;
    await page.goto("/");
    // The namespace does not exist, so the Secret fails and nothing is created.
    const { operationId, state } = await trackedApply(
      page,
      secret(e2eName("secret"), e2eName("missing"), value),
    );
    expect(state).toBe("failed");

    await page.goto(`/changes/${operationId}`);
    await expect(
      stateRegion(page).getByText("Failed", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(
        "The original content is not stored. Re-create the Secret manifest to retry.",
      ),
    ).toBeVisible();
    await expect(page.getByRole("link", { name: "Retry failed objects" })).toHaveCount(0);
    // The failure is shown as an outcome only: no API error text, and the
    // submitted value appears nowhere.
    await expect(objectsTable(page)).not.toContainText("not found");
    await expect(page.locator("body")).not.toContainText(value);
  });

  test("an Argo-owned object shows a controller warning before apply (live: no controller in kind)", async ({
    page,
  }) => {
    // The CI cluster has neither Argo CD nor Flux CD, so the honest live
    // answer is "no controller"; the confirmed-owner copy is covered by the
    // stubbed test below.
    await openYamlApply(page);
    await fillYaml(page, configMap(e2eName("cm")));
    await validate(page);
    const list = await ownershipList(page);
    await expect(list.getByRole("listitem")).toHaveCount(1);
    await expect(list.getByRole("listitem")).toContainText(NO_CONTROLLER);
    await expect(list).not.toContainText("Managed by");
    await expect(
      page.getByText(/managed by a GitOps controller/),
    ).toHaveCount(0);
  });

  test("a second user cannot open another user's receipt (404)", async ({
    page,
    browser,
  }) => {
    // Never skipped: a 429 from the shared auth bucket is waited out with
    // backoff, and the test fails loudly if it never clears, so the
    // cross-user isolation check cannot silently stop running in CI.
    test.setTimeout(180_000);
    const name = e2eName("cm");
    createdConfigMaps.push(name);
    await page.goto("/");
    const { operationId } = await trackedApply(page, configMap(name));
    const headers = await getAuthHeaders(page);

    const username = e2eSecureName("user");
    const password = `e2e-${crypto.randomUUID()}`;
    const created = await postWithBackoff(page, "/api/v1/users", "create the second user", {
      headers,
      data: {
        username,
        password,
        k8sUsername: username,
        k8sGroups: [],
        roles: ["viewer"],
      },
    });
    if (!created.ok()) {
      throw new Error(
        `creating the second user failed: ${created.status()} ${await created.text()}`,
      );
    }
    const userId = (await created.json())?.data?.id;
    try {
      const login = await postWithBackoff(page, "/api/v1/auth/login", "log in as the second user", {
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
        },
        data: { username, password },
      });
      if (!login.ok()) {
        throw new Error(`second login failed: ${login.status()} ${await login.text()}`);
      }
      const token = (await login.json()).data.accessToken as string;

      const direct = await page.request.get(`/api/v1/changes/${operationId}`, {
        headers: bearerHeaders(token),
        failOnStatusCode: false,
      });
      expect(direct.status()).toBe(404);

      const context = await browser.newContext();
      try {
        const other = await context.newPage();
        await attachAuthInjection(other, token);
        await other.goto(`/changes/${operationId}`);
        await expect(other.getByRole("alert")).toContainText(
          "Change receipt not found",
        );
        await expect(other.getByText(name)).toHaveCount(0);
      } finally {
        await context.close();
      }
    } finally {
      if (userId) {
        await page.request.delete(`/api/v1/users/${userId}`, {
          headers,
          failOnStatusCode: false,
        });
      }
      await deleteConfigMap(page, name);
    }
  });

  test("the receipts list is reachable from the Tools navigation", async ({
    page,
  }) => {
    await page.goto("/tools/yaml-apply");
    await page.getByRole("link", { name: "Recorded Changes" }).first().click();
    await expect(page).toHaveURL(/\/changes$/);
    await expect(
      page.getByRole("heading", { name: "Recorded changes", level: 1 }),
    ).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Stubbed rendering tests
// ---------------------------------------------------------------------------

test.describe("Change receipts (stubbed states)", () => {
  test("no Git PR affordance is rendered for a confirmed Argo object", async ({
    page,
  }) => {
    await page.route("**/api/v1/changes/ownership", (route) => {
      const body = route.request().postDataJSON() as {
        objects: Array<Record<string, unknown>>;
      };
      return json(route, 200, {
        data: {
          clusterId: "local",
          results: body.objects.map((o) =>
            ownershipResult(o, "confirmed", "confirmed-argo-status", {
              controller: "argocd",
              apps: [ARGO_APP],
            }),
          ),
        },
      });
    });
    await openYamlApply(page);
    await fillYaml(page, configMap(e2eName("cm")));
    await validate(page);

    await expect(
      page.getByText("1 of these objects is managed by a GitOps controller", {
        exact: false,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole("list", { name: "GitOps ownership" }),
    ).toContainText(
      "Managed by Argo CD application argo:argocd:storefront. Applying here changes the live object; the controller may revert it on its next sync.",
    );
    await expectNoGitWriteAffordance(page);
  });

  test("ambiguous ownership renders a distinct explanation from unknown ownership", async ({
    page,
  }) => {
    const a = e2eName("cm");
    const b = e2eName("cm");
    await page.route("**/api/v1/changes/ownership", (route) => {
      const body = route.request().postDataJSON() as {
        objects: Array<Record<string, unknown>>;
      };
      return json(route, 200, {
        data: {
          clusterId: "local",
          results: [
            ownershipResult(body.objects[0], "conflicting", "both-claim", {
              controller: "both",
              apps: [
                ARGO_APP,
                {
                  ...ARGO_APP,
                  appId: "flux-ks:flux-system:storefront",
                  tool: "fluxcd",
                  kind: "Kustomization",
                  namespace: "flux-system",
                },
              ],
            }),
            ownershipResult(body.objects[1], "unknown", "hints-only", {
              evidence: [
                {
                  kind: "instance-label",
                  tool: "argocd",
                  rawValue: "storefront",
                },
              ],
            }),
          ],
        },
      });
    });
    await openYamlApply(page);
    await fillYaml(page, [configMap(a), configMap(b)].join("\n---\n"));
    await validate(page);

    const items = page
      .getByRole("list", { name: "GitOps ownership" })
      .getByRole("listitem");
    await expect(items).toHaveCount(2);
    await expect(items.nth(0)).toContainText("Two controllers claim this object");
    await expect(items.nth(1)).toContainText(
      "This object carries GitOps labels or annotations, but no controller's inventory confirms it. Ownership is unknown.",
    );
    await expect(items.nth(1)).not.toContainText("Two controllers");
    await expectNoGitWriteAffordance(page);
  });

  test("unknown execution outcome is explained and offers no one-click retry", async ({
    page,
  }) => {
    const id = crypto.randomUUID();
    await page.route("**/api/v1/yaml/apply**", (route) =>
      json(route, 200, {
        data: {
          results: [
            { index: 0, kind: "ConfigMap", name: "x", action: "failed", error: "not applied" },
          ],
          summary: { total: 1, created: 0, configured: 0, unchanged: 0, failed: 1 },
          tracking: {
            operationId: id,
            receiptUrl: `/v1/changes/${id}`,
            state: "unknown",
            clusterId: "local",
            clusterGeneration: "local",
            contentDigest: "sha256:0",
            recordedThrough: 0,
            notAttempted: 0,
            unrecorded: 1,
            replayed: false,
            containsSecret: false,
            objects: [],
            verification: { state: "pending", url: `/v1/changes/${id}/verification` },
            warnings: [],
          },
        },
      }),
    );
    await openYamlApply(page);
    await fillYaml(page, configMap(e2eName("cm")));
    await validate(page);
    const record = await applyAndAwaitRecord(page);
    await expect(record.getByText("Outcome unknown", { exact: true })).toBeVisible();
    await expect(record).toContainText("no one-click retry");
    await expect(record).toContainText("1 document has no recorded outcome");
    await expect(page.getByRole("button", { name: /retry/i })).toHaveCount(0);

    // The receipt page says the same, and still offers no retry.
    await page.route(new RegExp(`/api/v1/changes/${id}(/verification)?$`), (route) =>
      json(route, 200, {
        data: {
          operationId: id,
          receiptUrl: `/v1/changes/${id}`,
          ownerUsername: "admin",
          state: "unknown",
          clusterId: "local",
          clusterGeneration: "local",
          targetGenerationChanged: false,
          contentDigest: "sha256:0",
          documentCount: 1,
          recordedThrough: 0,
          force: false,
          containsSecret: false,
          verification: { state: "inconclusive", url: "" },
          createdAt: new Date().toISOString(),
          access: "owner",
          summary: { total: 1, created: 0, configured: 0, unchanged: 0, failed: 0, notRecorded: 1 },
          objects: [],
          redactedObjects: 0,
          checks: [],
          redactedChecks: 0,
          ownership: [],
          redactedOwnership: 0,
        },
      }),
    );
    await record.getByRole("link", { name: /View change receipt/ }).click();
    await expect(
      stateRegion(page).getByText("Outcome unknown", { exact: true }),
    ).toBeVisible();
    await expect(stateRegion(page)).toContainText("Check the live objects");
    await expect(page.getByRole("link", { name: "Retry failed objects" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /retry/i })).toHaveCount(0);
  });

  test("receipt page degrades to an explicit unavailable state when /changes 503s", async ({
    page,
  }) => {
    const unavailable = (route: Route) =>
      json(route, 503, {
        error: {
          code: 503,
          message: "change receipts require a database",
        },
      });
    await page.route(/\/api\/v1\/changes(\?.*)?$/, unavailable);
    await page.route(/\/api\/v1\/changes\/[0-9a-f-]{36}(\/verification)?$/, unavailable);

    // YAML Apply: tracking is off, disabled, and says why.
    await openYamlApply(page);
    await expect(trackingBox(page)).toBeDisabled();
    await expect(trackingBox(page)).not.toBeChecked();
    await expect(page.getByText(/Change records are unavailable/)).toBeVisible();

    // The list says unavailable, not "no changes".
    await page.goto("/changes");
    await expect(page.getByRole("alert")).toContainText(
      "Change records are unavailable",
    );
    await expect(page.getByText("No recorded changes yet.")).toHaveCount(0);

    // A receipt says unavailable, not "not found".
    await page.goto(`/changes/${crypto.randomUUID()}`);
    await expect(page.getByRole("alert")).toContainText(
      "Change records are unavailable",
    );
    await expect(page.getByText("Change receipt not found")).toHaveCount(0);
  });
});
