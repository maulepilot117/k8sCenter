import type { Page, Request, Route } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { attachAuthInjection, getAuthHeaders } from "../helpers.ts";

/**
 * Backup assurance — recovery readiness page (Release F U36, AE8).
 *
 * Two kinds of test live here:
 *
 * 1. Live tests against the real backend: the honesty banner, sidebar
 *    reachability, the release boundary (no restore trigger), and AE8, which
 *    seeds a policy through the API, waits for the background collector's
 *    next pass and checks a fresh browser context sees what it opened. The
 *    CI kind cluster has PostgreSQL but no Velero, so the collector's pass
 *    there fails collection and opens a `collection_unknown` exception —
 *    which is exactly the state the page must render as unknown, never as
 *    "no backups". AE8 is skipped when the status endpoint reports the
 *    collector disabled (no database).
 *
 * 2. Rendering tests over stubbed /v1/velero/assurance replies, for the
 *    states the CI cluster cannot produce (paused schedules, partial runs,
 *    a non-admin, a revoked permission, a remote cluster, no database) and
 *    for the policy-edit error contracts. These fixtures model the U35 wire
 *    shapes; they do not prove backend authorization, which the Go handler
 *    tests in backend/internal/velero/assurance_handler_test.go cover.
 */

const HONESTY_TEXT =
  "Backup assurance observes what Velero reported. It does not verify that a restore would succeed. No restore has been attempted.";

const PAGE = "/backup/assurance";
const API = "/api/v1/velero/assurance";

const root = (page: Page) => page.getByTestId("backup-assurance");
const policyForm = (page: Page) => page.getByTestId("assurance-policy-form");

// ---------------------------------------------------------------------------
// Stub fixtures
// ---------------------------------------------------------------------------

type Condition =
  | "overdue"
  | "failed"
  | "partially_failed"
  | "paused"
  | "never_run"
  | "location_unavailable"
  | "collection_unknown";

function counts(open: Partial<Record<Condition, number>> = {}) {
  const byCondition: Record<Condition, number> = {
    overdue: 0,
    failed: 0,
    partially_failed: 0,
    paused: 0,
    never_run: 0,
    location_unavailable: 0,
    collection_unknown: 0,
    ...open,
  };
  const total = Object.values(byCondition).reduce((a, b) => a + b, 0);
  return { total, byCondition };
}

const RUNTIME = {
  holder: "kubecenter-0-1",
  lastTickAt: new Date().toISOString(),
  lastRunAt: new Date().toISOString(),
  lastCollection: "ok",
  findingCount: 0,
  lastErrorAt: null,
  leaseHeld: true,
  lease: {
    holder: "kubecenter-0-1",
    fence: 3,
    expiresAt: new Date(Date.now() + 120_000).toISOString(),
    expired: false,
  },
  deliveryBacklog: { pending: 0, failed: 0 },
};

function status(
  over: Record<string, unknown> = {},
  { admin = true }: { admin?: boolean } = {},
) {
  return {
    enabled: true,
    collection: "ok",
    collectionSource: "this_replica",
    policyCount: 1,
    open: counts(),
    ...(admin ? { runtime: RUNTIME } : {}),
    ...over,
  };
}

let seq = 0;
function exception(
  condition: Condition,
  over: Record<string, unknown> = {},
  detail: Record<string, unknown> = {},
) {
  seq++;
  const opened = new Date(Date.now() - 3_600_000).toISOString();
  return {
    id: `00000000-0000-4000-8000-${String(seq).padStart(12, "0")}`,
    policyId: "11111111-1111-4111-8111-111111111111",
    subject: {
      kind: "schedule",
      namespace: "velero",
      name: `nightly-${seq}`,
      uid: `uid-${seq}`,
    },
    condition,
    state: "open",
    severity: condition === "failed" ? "critical" : "warning",
    openedAt: opened,
    lastObservedAt: new Date().toISOString(),
    resolvedAt: null,
    observationCount: 4,
    lastSuccessAt: null,
    subjectStatus: "found",
    detail: { expectedRunKnown: true, lastOutcome: "success", ...detail },
    ...over,
  };
}

const POLICY = {
  id: "22222222-2222-4222-8222-222222222222",
  scopeKind: "schedule",
  scopeNamespace: "velero",
  scopeName: "nightly",
  maxAgeSeconds: 86_400,
  graceSeconds: 3_600,
  treatPartialAs: "failure",
  alertOnPaused: true,
  enabled: true,
  createdBy: "admin",
  createdAt: new Date().toISOString(),
  updatedBy: "",
  updatedAt: null,
  revision: 3,
  scheduleStatus: "found",
};

interface Reply {
  status?: number;
  body: unknown;
}

function errorReply(
  code: number,
  reason: string,
  message: string,
  extra?: Record<string, unknown>,
): Reply {
  return {
    status: code,
    body: { error: { code, message, reason, ...(extra ? { extra } : {}) } },
  };
}

/**
 * Answers /api/v1/velero/{status,assurance/*} from `replies`, keyed
 * "METHOD /path" relative to /api/v1/velero (query string ignored). A value
 * may be a function, so a test can change an answer mid-session. Every
 * request is recorded. Anything unstubbed under /assurance fails loudly with
 * a 599 so a missing fixture cannot pass as an empty list.
 */
async function stubAssurance(
  page: Page,
  replies: Record<string, Reply | ((req: Request) => Reply)>,
) {
  const calls: Array<{ method: string; path: string; search: string }> = [];
  await page.route("**/api/v1/velero/**", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname.replace(/^\/api\/v1\/velero/, "");
    calls.push({ method: req.method(), path, search: url.search });
    const key = `${req.method()} ${path}`;
    const found = replies[key];
    const reply: Reply | undefined =
      typeof found === "function" ? found(req) : found;
    if (reply) {
      await route.fulfill({
        status: reply.status ?? 200,
        contentType: "application/json",
        body: JSON.stringify(reply.body),
      });
      return;
    }
    if (path === "/status") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ data: { detected: true, bslCount: 1, vslCount: 0 } }),
      });
      return;
    }
    await route.fulfill({
      status: 599,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: 599, message: `unstubbed ${key}` } }),
    });
  });
  return calls;
}

function list(rows: unknown[], total = rows.length): Reply {
  return { body: { data: rows, metadata: { total } } };
}

async function openPage(page: Page) {
  await page.goto(PAGE);
  await expect(root(page)).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("assurance-surface-state")).toBeVisible();
}

// ---------------------------------------------------------------------------
// Live tests
// ---------------------------------------------------------------------------

test.describe("Backup assurance (live)", () => {
  test("backup assurance page loads and shows the honesty banner", async ({
    page,
  }) => {
    await page.goto(PAGE);
    const banner = page.getByTestId("assurance-honesty-banner");
    await expect(banner).toBeVisible({ timeout: 15_000 });
    await expect(banner).toHaveText(HONESTY_TEXT);
    await expect(
      page.getByRole("heading", { name: "Backup Assurance", level: 1 }),
    ).toBeVisible();
    // The banner is persistent: it offers nothing to dismiss it with.
    await expect(banner.getByRole("button")).toHaveCount(0);
  });

  test("assurance nav item is reachable from the sidebar", async ({ page }) => {
    await page.goto("/backup/backups");
    await page.getByRole("link", { name: "Assurance", exact: true }).click();
    await expect(page).toHaveURL(/\/backup\/assurance$/);
    await expect(
      page.getByRole("heading", { name: "Backup Assurance", level: 1 }),
    ).toBeVisible();
  });

  test("assurance page exposes no restore trigger", async ({ page }) => {
    await page.goto(PAGE);
    await expect(page.getByTestId("assurance-surface-state")).toBeVisible({
      timeout: 15_000,
    });
    // Release boundary: Release F cannot start a restore. Nothing on this
    // page — no button and no link — names a restore or a rehearsal.
    await expect(
      root(page).getByRole("button", { name: /restore|rehears/i }),
    ).toHaveCount(0);
    await expect(
      root(page).getByRole("link", { name: /restore|rehears/i }),
    ).toHaveCount(0);
    await expect(root(page).locator('a[href*="restore"]')).toHaveCount(0);
  });
});

test.describe.serial("Backup assurance AE8 (live collector)", () => {
  // The collector ticks once a minute; the first assertion waits for the
  // next pass after the policy is created.
  test.setTimeout(150_000);

  let policyId: string | null = null;
  let openTotal = 0;

  test.afterAll(async ({ browser }) => {
    if (!policyId) return;
    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    const page = await context.newPage();
    await page.goto("/backup/backups");
    const headers = await getAuthHeaders(page);
    await page.request.delete(`${API}/policies/${policyId}?confirm=true`, {
      headers,
      failOnStatusCode: false,
    });
    await context.close();
  });

  test("AE8: an exception opened by background evaluation is visible to a fresh client", async ({
    page,
    browser,
  }) => {
    await page.goto("/backup/backups");
    const headers = await getAuthHeaders(page);
    const s = await page.request.get(`${API}/status`, { headers });
    test.skip(
      !s.ok() || !(await s.json()).data?.enabled,
      "backup assurance not enabled (no database or collector)",
    );

    // A namespace-scope policy on a namespace nobody else uses: unique per
    // run, so it cannot collide with an operator's or another run's policy.
    const ns = `e2e-assure-${Math.random().toString(36).slice(2, 10)}`;
    const created = await page.request.post(`${API}/policies`, {
      headers,
      data: { scopeKind: "namespace", scopeNamespace: ns, maxAgeSeconds: 300 },
    });
    expect(created.status(), await created.text()).toBe(201);
    policyId = (await created.json()).data.id as string;

    // Wait for a collector pass AFTER the policy existed: without Velero it
    // fails collection and opens collection_unknown; with Velero, a
    // namespace with no backups is overdue. Either way an exception is
    // observed at or after the policy's creation. Requiring that timestamp
    // keeps a row left over from before this run from satisfying AE8.
    const createdAt = Date.parse(
      (await created.json()).data.createdAt as string,
    );
    const observedSince = async (): Promise<number> => {
      const r = await page.request.get(
        `${API}/exceptions?state=open&limit=500&offset=0`,
        { headers },
      );
      if (!r.ok()) return 0;
      const rows: Array<{ lastObservedAt: string }> = (await r.json()).data;
      return rows.filter((e) => Date.parse(e.lastObservedAt) >= createdAt)
        .length;
    };
    await expect
      .poll(observedSince, { timeout: 120_000, intervals: [2_000, 5_000] })
      .toBeGreaterThan(0);

    const r = await page.request.get(
      `${API}/exceptions?state=open&limit=500&offset=0`,
      { headers },
    );
    openTotal = Math.min((await r.json()).metadata.total as number, 50);

    // A brand-new browser context: nothing carried over from the client
    // that created the policy. The exception exists because the server
    // evaluated it in the background, not because this session asked.
    const fresh = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    try {
      const freshPage = await fresh.newPage();
      await attachAuthInjection(freshPage);
      await openPage(freshPage);
      await expect(freshPage.getByTestId("assurance-exception")).toHaveCount(
        openTotal,
      );
      if (
        (await freshPage
          .getByTestId("assurance-surface-state")
          .getAttribute("data-state")) !== "ok"
      ) {
        // The hard rule on the live page: a non-ok collection never claims
        // that no backups exist.
        await expect(root(freshPage)).not.toContainText(/no backups/i);
      }
    } finally {
      await fresh.close();
    }
  });

  test("AE8: reloading the page does not change the exception count", async ({
    page,
  }) => {
    test.skip(policyId === null, "AE8 policy was not created");
    await openPage(page);
    const cards = page.getByTestId("assurance-exception");
    await expect(cards).toHaveCount(openTotal);
    await page.reload();
    await expect(page.getByTestId("assurance-surface-state")).toBeVisible({
      timeout: 15_000,
    });
    await expect(cards).toHaveCount(openTotal);
  });
});

// ---------------------------------------------------------------------------
// Stubbed rendering tests
// ---------------------------------------------------------------------------

test.describe("Backup assurance (stubbed states)", () => {
  test("no policies renders the explicit no-policies empty state", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: {
          data: status({
            collection: "empty",
            collectionSource: "none",
            policyCount: 0,
          }),
        },
      },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-surface-state")).toHaveAttribute(
      "data-state",
      "empty",
    );
    await expect(page.getByTestId("assurance-empty-policies")).toContainText(
      "No freshness policies configured. Backup assurance evaluates nothing until you add one.",
    );
    // Nothing is evaluated, so there is no "all clear" message either.
    await expect(page.getByTestId("assurance-no-open")).toHaveCount(0);
  });

  test('Velero absent renders "not detected", not zero backups', async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /status": { body: { data: { detected: false, bslCount: 0, vslCount: 0 } } },
      "GET /assurance/status": {
        body: {
          data: status({
            collection: "unknown",
            open: counts({ collection_unknown: 1 }),
          }),
        },
      },
      "GET /assurance/exceptions": list([
        exception("collection_unknown", {
          subject: { kind: "cluster", namespace: "", name: "", uid: "" },
          subjectStatus: null,
        }),
      ]),
      "GET /assurance/policies": list([]),
    });
    await openPage(page);
    await expect(
      page.getByTestId("assurance-velero-not-detected"),
    ).toContainText("Velero was not detected");
    await expect(page.getByTestId("assurance-state-word")).toHaveText("unknown");
    await expect(root(page)).not.toContainText(/no backups/i);
    // Zero counts are not evidence when collection failed.
    await expect(page.getByTestId("assurance-count-overdue")).toHaveAttribute(
      "data-unknown",
      "true",
    );
  });

  test('collection unknown renders "unknown", never "no backups exist"', async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: {
          data: status({ collection: "unknown", collectionSource: "lease_holder" }),
        },
      },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    const surface = page.getByTestId("assurance-surface-state");
    await expect(surface).toHaveAttribute("data-state", "unknown");
    await expect(surface).toContainText(
      "Backup state is unknown — Velero could not be observed.",
    );
    for (const c of ["overdue", "failed", "paused", "never_run"]) {
      const tile = page.getByTestId(`assurance-count-${c}`);
      await expect(tile).toHaveAttribute("data-unknown", "true");
      await expect(tile).toContainText("unknown");
    }
    await expect(page.getByTestId("assurance-no-open")).toHaveCount(0);
    await expect(page.getByTestId("assurance-no-open-unknown")).toBeVisible();
    await expect(root(page)).not.toContainText(/no backups/i);
  });

  test("paused schedule renders paused, not overdue", async ({ page }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ paused: 1 }) }) },
      },
      "GET /assurance/exceptions": list([exception("paused")]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-group-paused")).toBeVisible();
    await expect(page.getByTestId("assurance-group-overdue")).toHaveCount(0);
    await expect(
      page.getByTestId("assurance-exception").getByTestId("assurance-condition-chip"),
    ).toHaveText("Paused");
  });

  test("partially failed is shown as its own chip and excluded from success counts", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: {
          data: status({ open: counts({ partially_failed: 1, overdue: 1 }) }),
        },
      },
      "GET /assurance/exceptions": list([
        exception("partially_failed", {}, { lastOutcome: "partial" }),
        // A policy treating partial as success: the run still reads partial.
        exception(
          "overdue",
          { lastSuccessAt: new Date(Date.now() - 172_800_000).toISOString() },
          { lastOutcome: "partial" },
        ),
      ]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    const partial = page.locator(
      '[data-testid="assurance-exception"][data-condition="partially_failed"]',
    );
    await expect(partial.getByTestId("assurance-condition-chip")).toHaveText(
      "Partially failed",
    );
    await expect(
      page
        .locator('[data-testid="assurance-exception"][data-condition="overdue"]')
        .getByTestId("assurance-partial-success-chip"),
    ).toHaveText("last success was partial");
    // No success tally, score or percentage anywhere on the page.
    await expect(root(page)).not.toContainText(/%|success rate|protected/i);
    await expect(
      page.getByTestId("assurance-count-partially_failed"),
    ).toContainText("1");
  });

  test("non-admin cannot see or edit policies", async ({ page }) => {
    const calls = await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ policyCount: 2 }, { admin: false }) },
      },
      "GET /assurance/exceptions": list([]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-policies-readonly")).toContainText(
      "managed by administrators",
    );
    await expect(page.getByRole("button", { name: "Add policy" })).toHaveCount(0);
    await expect(page.getByTestId("assurance-policy-row")).toHaveCount(0);
    await expect(page.getByTestId("assurance-runtime")).toHaveCount(0);
    expect(calls.some((c) => c.path.startsWith("/assurance/policies"))).toBe(
      false,
    );
  });

  test("permission loss mid-session renders forbidden, not empty", async ({
    page,
  }) => {
    let revoked = false;
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ failed: 1 }) }, { admin: false }) },
      },
      "GET /assurance/exceptions": () =>
        revoked
          ? errorReply(403, "forbidden", "forbidden")
          : list([exception("failed")]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-exception")).toHaveCount(1);

    revoked = true;
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByTestId("assurance-forbidden")).toContainText(
      "You do not have permission to view backup exceptions",
    );
    await expect(page.getByTestId("assurance-exception")).toHaveCount(0);
    await expect(page.getByTestId("assurance-no-open")).toHaveCount(0);
  });

  test("a remote cluster selection explains assurance is local-only", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": errorReply(
        501,
        "remote_assurance_unsupported",
        "backup assurance is collected for the local cluster only",
      ),
    });
    await page.goto(PAGE);
    await expect(page.getByTestId("assurance-remote-unsupported")).toContainText(
      "Local cluster only",
    );
    await expect(page.getByTestId("assurance-exception")).toHaveCount(0);
    await expect(page.getByTestId("assurance-honesty-banner")).toBeVisible();
  });

  test("no database renders unavailable, not empty", async ({ page }) => {
    await stubAssurance(page, {
      "GET /assurance/status": errorReply(
        503,
        "database_unavailable",
        "backup assurance unavailable",
        { capability: "backup-assurance" },
      ),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-surface-state")).toHaveAttribute(
      "data-state",
      "unavailable",
    );
    await expect(page.getByTestId("assurance-surface-state")).toContainText(
      "requires PostgreSQL",
    );
    await expect(page.getByTestId("assurance-empty-policies")).toHaveCount(0);
  });

  test("schedule notes say not found and not computable rather than guessing", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ overdue: 1 }) }) },
      },
      "GET /assurance/exceptions": list([
        exception(
          "overdue",
          {
            subjectStatus: "not_found",
            subjectNote: "schedule not found",
            expectedRunNote: "not computable",
          },
          { expectedRunKnown: false },
        ),
      ]),
      "GET /assurance/policies": list([
        { ...POLICY, scheduleStatus: "not_found", scheduleNote: "schedule not found" },
      ]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-subject-note")).toContainText(
      "schedule not found",
    );
    await expect(page.getByTestId("assurance-expected-run-note")).toContainText(
      "Next expected run is not computable for this schedule expression.",
    );
    await expect(page.getByTestId("assurance-policy-row")).toContainText(
      "watches nothing",
    );
  });
});

test.describe("Backup assurance policy editing (stubbed)", () => {
  test("delete asks the server first and requires typed confirmation", async ({
    page,
  }) => {
    const calls = await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
      [`DELETE /assurance/policies/${POLICY.id}`]: (req) =>
        new URL(req.url()).searchParams.get("confirm") === "true"
          ? {
              body: {
                data: { id: POLICY.id, deleted: true, discardedOpenExceptions: 2 },
              },
            }
          : errorReply(400, "confirmation_required", "confirm", {
              openExceptions: 2,
            }),
    });
    await openPage(page);
    await page
      .getByTestId("assurance-policy-row")
      .getByRole("button", { name: "Delete" })
      .click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("2 open exceptions will be discarded");
    const confirm = dialog.getByRole("button", { name: "Delete policy" });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole("textbox").fill(POLICY.scopeName);
    await confirm.click();

    await expect(page.getByTestId("assurance-notice")).toContainText(
      "2 open exceptions were discarded",
    );
    const deletes = calls.filter((c) => c.method === "DELETE");
    expect(deletes.map((c) => c.search)).toEqual(["", "?confirm=true"]);
  });

  test("a stale revision is reported as a conflict, not saved", async ({
    page,
  }) => {
    let sentRevision: unknown;
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
      [`PUT /assurance/policies/${POLICY.id}`]: (req) => {
        sentRevision = req.postDataJSON()?.revision;
        return errorReply(409, "revision_conflict", "changed");
      },
    });
    await openPage(page);
    await page
      .getByTestId("assurance-policy-row")
      .getByRole("button", { name: "Edit" })
      .click();
    await policyForm(page).getByLabel("Maximum age (minutes)").fill("720");
    await page.getByRole("button", { name: "Save changes" }).click();

    await expect(page.getByTestId("assurance-policy-form-error")).toContainText(
      "Someone else changed this policy",
    );
    await expect(page.getByRole("button", { name: "Reload policy" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(sentRevision).toBe(POLICY.revision);
  });

  test("server field errors are shown against their fields", async ({ page }) => {
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([]),
      "POST /assurance/policies": errorReply(
        400,
        "invalid_policy",
        "invalid policy: scopeNamespace must be a valid namespace name",
        {
          fieldErrors: [
            { field: "scopeNamespace", message: "must be a valid namespace name" },
          ],
        },
      ),
    });
    await openPage(page);
    await page.getByRole("button", { name: "Add policy" }).click();
    await policyForm(page)
      .getByLabel("Namespace", { exact: true })
      .fill("Not_Valid");
    await policyForm(page)
      .getByLabel("Schedule", { exact: true })
      .fill("nightly");
    await page.getByRole("button", { name: "Create policy" }).click();

    await expect(page.locator("#assurance-policy-namespace-error")).toHaveText(
      "must be a valid namespace name",
    );
    await expect(
      policyForm(page).getByLabel("Namespace", { exact: true }),
    ).toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });
});
