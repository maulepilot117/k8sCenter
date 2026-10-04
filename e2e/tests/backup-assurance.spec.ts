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

  // Every policy this describe created, across retries: CI retries a failed
  // test, and each attempt creates its own policy.
  const createdPolicies: string[] = [];
  let exceptionId: string | null = null;

  test.afterAll(async ({ browser }) => {
    if (createdPolicies.length === 0) return;
    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    try {
      const page = await context.newPage();
      await page.goto("/backup/backups");
      const headers = await getAuthHeaders(page);
      const failures: string[] = [];
      for (const id of createdPolicies) {
        const res = await page.request.delete(
          `${API}/policies/${id}?confirm=true`,
          { headers, failOnStatusCode: false },
        );
        // 404 policy_not_found means it is already gone, which is the goal.
        if (!res.ok() && res.status() !== 404) {
          failures.push(`${id}: ${res.status()} ${await res.text()}`);
        }
      }
      // A policy left behind keeps opening exceptions and notifications for
      // every later spec on this backend: fail loudly rather than leak it.
      expect(failures, "AE8 policy cleanup").toEqual([]);
    } finally {
      await context.close();
    }
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
    // Attribution needs this test's policy to be the only one: a Velero
    // outage opens ONE cluster-wide collection_unknown exception, owned by
    // whichever policy evaluates first, and another policy's open rows are
    // re-observed on every pass. With other policies present, nothing here
    // could prove the new policy caused what is on screen.
    const existing = await page.request.get(`${API}/policies`, { headers });
    expect(existing.ok(), await existing.text()).toBe(true);
    test.skip(
      ((await existing.json()).data ?? []).length > 0,
      "other backup assurance policies exist; AE8 needs a clean policy set",
    );

    // A namespace-scope policy on a namespace nobody else uses.
    const ns = `e2e-assure-${Math.random().toString(36).slice(2, 10)}`;
    const created = await page.request.post(`${API}/policies`, {
      headers,
      data: { scopeKind: "namespace", scopeNamespace: ns, maxAgeSeconds: 300 },
    });
    expect(created.status(), await created.text()).toBe(201);
    const policyId = (await created.json()).data.id as string;
    createdPolicies.push(policyId);

    // Wait for a collector pass after the policy existed. Without Velero it
    // fails collection and opens collection_unknown; with Velero, a namespace
    // with no backups is overdue. Either way the row is this policy's.
    const ownRow = async (): Promise<string | null> => {
      const r = await page.request.get(
        `${API}/exceptions?state=open&limit=500&offset=0`,
        { headers },
      );
      if (!r.ok()) return null;
      const rows: Array<{ id: string; policyId: string }> = (await r.json())
        .data;
      return rows.find((e) => e.policyId === policyId)?.id ?? null;
    };
    await expect
      .poll(ownRow, { timeout: 120_000, intervals: [2_000, 5_000] })
      .not.toBeNull();
    exceptionId = await ownRow();

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
      const card = freshPage.locator(
        `[data-testid="assurance-exception"][data-exception-id="${exceptionId}"]`,
      );
      await expect(card).toHaveCount(1);
      await expect(card).toHaveAttribute("data-policy-id", policyId);
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
    test.skip(exceptionId === null, "AE8 exception was not observed");
    await openPage(page);
    const cards = page.getByTestId("assurance-exception");
    const own = page.locator(
      `[data-testid="assurance-exception"][data-exception-id="${exceptionId}"]`,
    );
    await expect(own).toHaveCount(1);
    const before = await cards.count();
    await page.reload();
    await expect(page.getByTestId("assurance-surface-state")).toBeVisible({
      timeout: 15_000,
    });
    // Durable server state, not client state: the same exception is still
    // there and the page shows the same number of cards.
    await expect(own).toHaveCount(1);
    await expect(cards).toHaveCount(before);
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
    // The partial run is reported as PartiallyFailed, never as a completed
    // (successful) run, on both rows, including the one whose policy treats
    // partial as success.
    await expect(partial).toContainText("PartiallyFailed");
    await expect(root(page)).not.toContainText("Completed");
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

test.describe("Backup assurance (stubbed states, continued)", () => {
  test("stale collection shows counts as not current, never as all clear", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: {
          data: status({ collection: "stale", open: counts({ failed: 2 }) }),
        },
      },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    const surface = page.getByTestId("assurance-surface-state");
    await expect(surface).toHaveAttribute("data-state", "stale");
    await expect(page.getByTestId("assurance-state-word")).toHaveText("stale");
    await expect(surface).toContainText("Counts may be out of date.");
    // A zero is not evidence when collection is stale; a non-zero still is.
    await expect(page.getByTestId("assurance-count-overdue")).toHaveAttribute(
      "data-unknown",
      "true",
    );
    const failed = page.getByTestId("assurance-count-failed");
    await expect(failed).toHaveAttribute("data-unknown", "false");
    await expect(failed).toContainText("2");
    await expect(page.getByTestId("assurance-no-open")).toHaveCount(0);
    await expect(page.getByTestId("assurance-no-open-unknown")).toContainText(
      "collection is stale",
    );
  });

  test("ok collection with no open exceptions says so, with the honesty caveat", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-no-open")).toHaveText(
      "No open backup exceptions. This is not a recoverability guarantee.",
    );
    await expect(page.getByTestId("assurance-no-open-unknown")).toHaveCount(0);
  });

  test("a failed status read renders unknown with the error, not an empty page", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": errorReply(
        500,
        "",
        "failed to read backup assurance status",
      ),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-surface-state")).toHaveAttribute(
      "data-state",
      "unknown",
    );
    await expect(page.getByTestId("assurance-error")).toContainText(
      "Backup state is unknown",
    );
    await expect(page.getByTestId("assurance-empty-policies")).toHaveCount(0);
  });

  test("the Resolved tab and pagination request the right slice", async ({
    page,
  }) => {
    const firstPage = Array.from({ length: 50 }, () => exception("failed"));
    const secondPage = [exception("failed")];
    const calls = await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ failed: 51 }) }) },
      },
      "GET /assurance/exceptions": (req) => {
        const q = new URL(req.url()).searchParams;
        if (q.get("state") === "resolved") {
          return list([
            exception(
              "failed",
              {
                state: "resolved",
                resolvedAt: new Date().toISOString(),
              },
              { resolutionReason: "condition_cleared" },
            ),
          ]);
        }
        return list(q.get("offset") === "50" ? secondPage : firstPage, 51);
      },
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-exception")).toHaveCount(50);
    await root(page).getByRole("button", { name: "Next" }).click();
    await expect(page.getByTestId("assurance-exception")).toHaveCount(1);
    await expect(root(page)).toContainText("51–51 of 51");

    await root(page).getByRole("button", { name: "Resolved" }).click();
    await expect(page.getByTestId("assurance-exception")).toHaveCount(1);
    await expect(page.getByTestId("assurance-exception")).toContainText(
      "the condition cleared",
    );
    const exCalls = calls.filter((c) => c.path === "/assurance/exceptions");
    for (const c of exCalls) {
      // Every list request names its state, limit and offset explicitly.
      expect(c.search).toMatch(/state=(open|resolved)/);
      expect(c.search).toContain("limit=50");
      expect(c.search).toMatch(/offset=\d+/);
    }
    expect(exCalls.map((c) => c.search)).toEqual([
      "?state=open&limit=50&offset=0",
      "?state=open&limit=50&offset=50",
      "?state=resolved&limit=50&offset=0",
    ]);
  });

  test("a page that emptied on refresh moves back to the last page instead of claiming none", async ({
    page,
  }) => {
    let total = 51;
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ failed: total }) }) },
      },
      "GET /assurance/exceptions": (req) => {
        const offset = new URL(req.url()).searchParams.get("offset");
        const rows = Array.from(
          { length: Math.max(0, Math.min(50, total - Number(offset))) },
          () => exception("failed"),
        );
        return list(rows, total);
      },
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    await root(page).getByRole("button", { name: "Next" }).click();
    await expect(page.getByTestId("assurance-exception")).toHaveCount(1);

    total = 50; // the one row on page two resolved
    await root(page).getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByTestId("assurance-exception")).toHaveCount(50);
    await expect(page.getByTestId("assurance-no-open")).toHaveCount(0);
  });

  test("a slow reply for the previous tab cannot replace the current one", async ({
    page,
  }) => {
    let releaseOpen: () => void = () => {};
    const openGate = new Promise<void>((r) => {
      releaseOpen = r;
    });
    let openCalls = 0;
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ failed: 1 }) }) },
      },
      "GET /assurance/exceptions": (req) =>
        new URL(req.url()).searchParams.get("state") === "resolved"
          ? list([
              exception("paused", {
                state: "resolved",
                resolvedAt: new Date().toISOString(),
              }),
            ])
          : list([exception("failed")]),
      "GET /assurance/policies": list([POLICY]),
    });
    // Registered after the stub, so it runs first (Playwright runs the most
    // recently added matching route first) and holds the second Open reply.
    await page.route("**/api/v1/velero/assurance/exceptions**", async (route) => {
      const state = new URL(route.request().url()).searchParams.get("state");
      if (state === "open" && ++openCalls === 2) await openGate;
      await route.fallback();
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-exception")).toHaveCount(1);
    const toggle = root(page);
    // Resolved -> Open (held) -> Resolved: the held Open reply lands last.
    await toggle.getByRole("button", { name: "Resolved" }).click();
    await expect(page.getByTestId("assurance-group-paused")).toBeVisible();
    await toggle.getByRole("button", { name: "Open", exact: true }).click();
    await toggle.getByRole("button", { name: "Resolved" }).click();
    await expect(page.getByTestId("assurance-group-paused")).toBeVisible();
    releaseOpen();
    await page.waitForTimeout(500);
    await expect(page.getByTestId("assurance-group-paused")).toBeVisible();
    await expect(page.getByTestId("assurance-group-failed")).toHaveCount(0);
  });

  test("a populated page still exposes no restore trigger", async ({ page }) => {
    await stubAssurance(page, {
      "GET /assurance/status": {
        body: { data: status({ open: counts({ failed: 1, overdue: 1 }) }) },
      },
      "GET /assurance/exceptions": list([
        exception("failed", {}, { failureReason: "restore of item failed" }),
        exception("overdue"),
      ]),
      "GET /assurance/policies": list([POLICY]),
    });
    await openPage(page);
    await expect(page.getByTestId("assurance-exception")).toHaveCount(2);
    await page
      .getByTestId("assurance-policy-row")
      .getByRole("button", { name: "Edit" })
      .click();
    await expect(policyForm(page)).toBeVisible();
    await expect(
      root(page).getByRole("button", { name: /restore|rehears/i }),
    ).toHaveCount(0);
    await expect(
      root(page).getByRole("link", { name: /restore|rehears/i }),
    ).toHaveCount(0);
    await expect(root(page).locator('a[href*="restore"]')).toHaveCount(0);
  });
});

test.describe("Backup assurance policy editing (stubbed, success paths)", () => {
  test("creating a namespace policy sends seconds and no schedule name", async ({
    page,
  }) => {
    let body: Record<string, unknown> | undefined;
    let created = false;
    const calls = await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": () => list(created ? [POLICY] : []),
      "POST /assurance/policies": (req) => {
        body = req.postDataJSON();
        created = true;
        return { status: 201, body: { data: POLICY } };
      },
    });
    await openPage(page);
    await page.getByRole("button", { name: "Add policy" }).click();
    await policyForm(page)
      .getByLabel("Scope", { exact: true })
      .selectOption("namespace");
    await policyForm(page)
      .getByLabel("Namespace", { exact: true })
      .fill("velero");
    await policyForm(page).getByLabel("Maximum age (minutes)").fill("90");
    await policyForm(page).getByLabel("Grace (minutes)").fill("15");
    await policyForm(page)
      .getByLabel("Treat PartiallyFailed as")
      .selectOption("success");
    await page.getByRole("button", { name: "Create policy" }).click();

    await expect(page.getByTestId("assurance-notice")).toContainText(
      "Policy created",
    );
    expect(body).toEqual({
      scopeKind: "namespace",
      scopeNamespace: "velero",
      scopeName: "",
      maxAgeSeconds: 5400,
      graceSeconds: 900,
      treatPartialAs: "success",
      alertOnPaused: true,
      enabled: true,
    });
    await expect(page.getByTestId("assurance-policy-row")).toHaveCount(1);
    expect(
      calls.filter((c) => c.method === "GET" && c.path === "/assurance/policies")
        .length,
    ).toBeGreaterThan(1);
  });

  test("saving an edit sends the revision and mutable fields only", async ({
    page,
  }) => {
    let body: Record<string, unknown> | undefined;
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
      [`PUT /assurance/policies/${POLICY.id}`]: (req) => {
        body = req.postDataJSON();
        return { body: { data: { ...POLICY, revision: 4 } } };
      },
    });
    await openPage(page);
    await page
      .getByTestId("assurance-policy-row")
      .getByRole("button", { name: "Edit" })
      .click();
    await expect(
      policyForm(page).getByLabel("Namespace", { exact: true }),
    ).toBeDisabled();
    await policyForm(page).getByLabel("Maximum age (minutes)").fill("720");
    await policyForm(page).getByLabel("Enabled", { exact: true }).uncheck();
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByTestId("assurance-notice")).toHaveText(
      "Policy saved.",
    );
    expect(body).toEqual({
      revision: 3,
      maxAgeSeconds: 43_200,
      graceSeconds: 3_600,
      treatPartialAs: "failure",
      alertOnPaused: true,
      enabled: false,
    });
  });

  test("Reload policy after a conflict reopens the form with the current values", async ({
    page,
  }) => {
    let reloaded = false;
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": () =>
        list([reloaded ? { ...POLICY, revision: 4, maxAgeSeconds: 7_200 } : POLICY]),
      [`PUT /assurance/policies/${POLICY.id}`]: () => {
        reloaded = true;
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
    await page.getByRole("button", { name: "Reload policy" }).click();

    await expect(page.getByTestId("assurance-notice")).toContainText(
      "Reloaded the current values",
    );
    await expect(
      policyForm(page).getByLabel("Maximum age (minutes)"),
    ).toHaveValue("120");
    await expect(page.getByTestId("assurance-policy-form-error")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Save changes" }),
    ).toBeEnabled();
  });

  for (const [reply, expected] of [
    [
      errorReply(409, "policy_exists", "exists"),
      "A policy already exists for this scope",
    ],
    [
      errorReply(400, "scope_immutable", "immutable"),
      "A policy's scope cannot be changed",
    ],
    [
      errorReply(403, "", "forbidden"),
      "Only administrators can change freshness policies",
    ],
  ] as const) {
    test(`a create rejected with ${reply.status} ${
      (reply.body as { error: { reason: string } }).error.reason || "forbidden"
    } explains why`, async ({ page }) => {
      await stubAssurance(page, {
        "GET /assurance/status": { body: { data: status() } },
        "GET /assurance/exceptions": list([]),
        "GET /assurance/policies": list([]),
        "POST /assurance/policies": reply,
      });
      await openPage(page);
      await page.getByRole("button", { name: "Add policy" }).click();
      await policyForm(page)
        .getByLabel("Namespace", { exact: true })
        .fill("velero");
      await policyForm(page)
        .getByLabel("Schedule", { exact: true })
        .fill("nightly");
      await page.getByRole("button", { name: "Create policy" }).click();
      await expect(
        page.getByTestId("assurance-policy-form-error"),
      ).toContainText(expected);
    });
  }

  test("a failed confirmed delete keeps the dialog open with the error", async ({
    page,
  }) => {
    await stubAssurance(page, {
      "GET /assurance/status": { body: { data: status() } },
      "GET /assurance/exceptions": list([]),
      "GET /assurance/policies": list([POLICY]),
      [`DELETE /assurance/policies/${POLICY.id}`]: (req) =>
        new URL(req.url()).searchParams.get("confirm") === "true"
          ? errorReply(500, "", "failed to delete backup assurance policy")
          : errorReply(400, "confirmation_required", "confirm", {
              openExceptions: 1,
            }),
    });
    await openPage(page);
    await page
      .getByTestId("assurance-policy-row")
      .getByRole("button", { name: "Delete" })
      .click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("1 open exception will be discarded");
    await dialog.getByRole("textbox").fill(POLICY.scopeName);
    await dialog.getByRole("button", { name: "Delete policy" }).click();
    await expect(dialog).toContainText(
      "failed to delete backup assurance policy",
    );
    await expect(page.getByTestId("assurance-policy-row")).toHaveCount(1);
  });
});
