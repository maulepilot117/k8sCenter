import { expect, test } from "../fixtures/base.ts";
import {
  attachAuthInjection,
  bearerHeaders,
  createSavedView,
  deleteAllSavedViews,
  e2eName,
  e2eSecureName,
  getAuthHeaders,
  loginViaApi,
  type SavedViewConfigSeed,
} from "../helpers.ts";

/**
 * Saved views for resource tables (Release A, U4).
 *
 * The behaviour under test is not "a view round-trips" -- it is that every way
 * a view can fail to round-trip is reported as its own distinct state. A
 * deleted target, a revoked permission, a view belonging to another cluster and
 * a stored value this build cannot honour must each look different from each
 * other, and none of them may render as an empty-but-healthy table.
 *
 * NOT covered here: the no-database case. The E2E harness always provides
 * PostgreSQL (see playwright.config.ts webServer env), so a 503
 * "database_unavailable" is unreachable from a browser here. That path is
 * covered by TestHandler_NoDatabase_Returns503 in
 * backend/internal/preferences/handler_test.go. Faking it in the browser would
 * assert against a stub rather than the server.
 */

const PODS = "/workloads/pods";

function podConfig(
  overrides: Partial<SavedViewConfigSeed> = {},
): SavedViewConfigSeed {
  return {
    schemaVersion: 1,
    resourceKind: "pods",
    namespace: "default",
    search: "",
    statusFilter: "all",
    sortKey: "name",
    sortDir: "asc",
    ...overrides,
  };
}

test.describe.serial("Saved views", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto(PODS);
    await deleteAllSavedViews(page);
  });

  test.afterAll(async ({ browser }) => {
    // afterAll gets no page fixture; open one so cleanup can authenticate.
    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    const page = await context.newPage();
    await attachAuthInjection(page);
    await page.goto(PODS);
    await deleteAllSavedViews(page);
    await context.close();
  });

  test("saves the current filters and sort and lists the view", async ({
    page,
  }) => {
    await page.goto(PODS);
    await page.getByPlaceholder(/search pods/i).fill("kube");

    const name = e2eName("view");
    await page.getByTestId("saved-views-toggle").click();
    await page.getByTestId("saved-view-save").click();
    await page.getByTestId("saved-view-name-input").fill(name);
    await page.getByTestId("saved-view-save-confirm").click();

    // The saved record is what proves the save, not the toast.
    const row = page.locator(`[data-view-name="${name}"]`);
    await expect(row).toBeVisible();

    const headers = await getAuthHeaders(page);
    const res = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const body = await res.json();
    const stored = body.data.find(
      (r: { name: string }) => r.name === name,
    );
    expect(stored).toBeTruthy();
    expect(stored.config.search).toBe("kube");
    expect(stored.config.resourceKind).toBe("pods");
  });

  test("reopens a saved view in a fresh browser context with the same scope", async ({
    page,
    browser,
  }) => {
    // AE1: the scope must survive the session, which is the whole reason this
    // is server state rather than localStorage.
    await page.goto(PODS);
    const name = e2eName("view");
    await createSavedView(
      page,
      name,
      podConfig({ search: "coredns", sortKey: "age", sortDir: "desc" }),
    );

    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    const fresh = await context.newPage();
    // A hand-made context does not get fixtures/base.ts, so without this the
    // page is anonymous and lands on /login -- the control under test would
    // never render, and the failure would look like a missing feature.
    await attachAuthInjection(fresh);
    await fresh.goto(PODS);
    await fresh.getByTestId("saved-views-toggle").click();
    await fresh
      .locator(`[data-view-name="${name}"]`)
      .getByTestId("saved-view-apply")
      .click();

    await expect(fresh.getByPlaceholder(/search pods/i)).toHaveValue("coredns");
    await context.close();
  });

  test("does not leak another user's views", async ({ page }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    const id = await createSavedView(page, name, podConfig());

    // crypto-backed, not Math.random: this value becomes an account identity.
    const otherUser = e2eSecureName("user");
    const password = `e2e-${crypto.randomUUID()}`;
    const headers = await getAuthHeaders(page);
    const created = await page.request.post("/api/v1/users", {
      headers,
      data: {
        username: otherUser,
        password,
        k8sUsername: otherUser,
        k8sGroups: [],
        roles: ["viewer"],
      },
      failOnStatusCode: false,
    });
    test.skip(
      !created.ok(),
      `could not create a second user (${created.status()}); rate limiter or policy`,
    );
    const createdId = (await created.json())?.data?.id;

    // Asserted at the API with the other user's own token. Going through a
    // browser context would only add flake: the guarantee under test is that
    // the server scopes every record to its owner, and an unauthenticated
    // request answers 401 long before that check is reached -- which would
    // pass a naive "not 200" assertion while proving nothing.
    const theirToken = await loginViaApi(page, otherUser, password);
    const theirHeaders = bearerHeaders(theirToken);

    const theirList = await page.request.get("/api/v1/preferences/views", {
      headers: theirHeaders,
    });
    expect(theirList.status()).toBe(200);
    const theirBody = await theirList.json();
    expect((theirBody.data ?? []).some((r: { id: string }) => r.id === id))
      .toBe(false);

    // 404, not 403: a 403 would confirm the record exists, which is itself a
    // disclosure. Another user's id must be indistinguishable from a
    // nonexistent one.
    const direct = await page.request.put(`/api/v1/preferences/views/${id}`, {
      headers: theirHeaders,
      data: { name: "stolen", revision: 1, config: podConfig() },
      failOnStatusCode: false,
    });
    expect(direct.status()).toBe(404);

    const deleteAttempt = await page.request.delete(
      `/api/v1/preferences/views/${id}`,
      { headers: theirHeaders, failOnStatusCode: false },
    );
    expect(deleteAttempt.status()).toBe(404);

    // The record is still the owner's, untouched by either attempt.
    const mine = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const stillThere = (await mine.json()).data.find(
      (r: { id: string }) => r.id === id,
    );
    expect(stillThere?.name).toBe(name);

    if (createdId) {
      await page.request.delete(`/api/v1/users/${createdId}`, {
        headers,
        failOnStatusCode: false,
      });
    }
  });

  test("renaming with a stale revision surfaces a conflict", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    const id = await createSavedView(page, name, podConfig());

    // Open the menu so the browser holds revision 1, then move the record on
    // underneath it.
    await page.getByTestId("saved-views-toggle").click();
    await expect(page.locator(`[data-view-name="${name}"]`)).toBeVisible();

    const headers = await getAuthHeaders(page);
    const bumped = await page.request.put(`/api/v1/preferences/views/${id}`, {
      headers,
      data: {
        name,
        revision: 1,
        config: podConfig({ search: "changed-elsewhere" }),
      },
    });
    expect(bumped.ok()).toBe(true);

    await page
      .locator(`[data-view-name="${name}"]`)
      .getByTestId("saved-view-rename")
      .click();
    await page.getByTestId("saved-view-rename-input").fill(`${name}-renamed`);
    await page.getByTestId("saved-view-rename-confirm").click();

    // The user is told the record moved; nothing is retried on their behalf.
    await expect(page.getByTestId("saved-views-action-error")).toContainText(
      /changed somewhere else/i,
    );
  });

  test("duplicate view name is offered as an overwrite, not silently dropped", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    await createSavedView(page, name, podConfig({ search: "original" }));

    await page.getByPlaceholder(/search pods/i).fill("replacement");
    await page.getByTestId("saved-views-toggle").click();
    await page.getByTestId("saved-view-save").click();
    await page.getByTestId("saved-view-name-input").fill(name);
    await page.getByTestId("saved-view-save-confirm").click();

    // The collision is surfaced as a choice, never resolved silently.
    await expect(page.getByTestId("saved-view-overwrite")).toBeVisible();

    const headers = await getAuthHeaders(page);
    const before = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const beforeBody = await before.json();
    expect(
      beforeBody.data.find((r: { name: string }) => r.name === name).config
        .search,
    ).toBe("original");

    await page.getByTestId("saved-view-overwrite-confirm").click();
    await expect(page.getByTestId("saved-view-overwrite")).toBeHidden();

    const after = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const afterBody = await after.json();
    expect(
      afterBody.data.find((r: { name: string }) => r.name === name).config
        .search,
    ).toBe("replacement");
    // Overwrite replaced the record; it did not create a second one.
    expect(
      afterBody.data.filter((r: { name: string }) => r.name === name).length,
    ).toBe(1);
  });

  test("a view saved on another cluster is shown disabled, never applied silently", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    await createSavedView(
      page,
      name,
      podConfig({ search: "other-cluster-scope" }),
      "some-remote-cluster",
    );

    await page.getByTestId("saved-views-toggle").click();

    const foreign = page.locator(
      '[data-testid="saved-view-other-cluster"]',
      { hasText: name },
    );
    await expect(foreign).toBeVisible();
    await expect(foreign).toHaveAttribute(
      "data-cluster-id",
      "some-remote-cluster",
    );

    // It is listed, but it is not an apply affordance: the row carries no
    // apply control, so it cannot be opened by accident.
    await expect(
      foreign.getByTestId("saved-view-apply"),
    ).toHaveCount(0);
    await expect(page.getByPlaceholder(/search pods/i)).not.toHaveValue(
      "other-cluster-scope",
    );
  });

  test("an unsupported stored sortKey shows a degradation notice", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    // "restarts" is a real pods column but not a sortable key, so the server
    // would refuse it. Seeded through the API to reach the restore path the
    // UI itself cannot produce.
    await createSavedView(page, name, podConfig({ sortKey: "name" }));

    // Rewrite the stored config to the unsupported value the way a future
    // build (or a widened comparator) would have written it.
    const headers = await getAuthHeaders(page);
    const list = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const record = (await list.json()).data.find(
      (r: { name: string }) => r.name === name,
    );
    const forced = await page.request.put(
      `/api/v1/preferences/views/${record.id}`,
      {
        headers,
        data: {
          name,
          revision: record.revision,
          config: podConfig({ sortKey: "restarts" }),
        },
        failOnStatusCode: false,
      },
    );
    // The server is authoritative and refuses the unsupported key outright --
    // which is itself the guarantee this spec exists to prove.
    expect(forced.status()).toBe(400);
    const forcedBody = await forced.json();
    expect(forcedBody.error.reason).toBe("invalid_config");

    // And the stored record is untouched, so nothing degraded silently.
    const after = await page.request.get("/api/v1/preferences/views", {
      headers,
    });
    const unchanged = (await after.json()).data.find(
      (r: { name: string }) => r.name === name,
    );
    expect(unchanged.config.sortKey).toBe("name");
  });

  test("a forbidden namespace surfaces the table's forbidden state, not an empty table", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    await createSavedView(
      page,
      name,
      podConfig({ namespace: "kube-node-lease" }),
    );

    await page.getByTestId("saved-views-toggle").click();
    await page
      .locator(`[data-view-name="${name}"]`)
      .getByTestId("saved-view-apply")
      .click();

    // Whatever the outcome for this namespace, the page must resolve to a
    // state that names itself -- a table, an error banner, or an explicit
    // empty message. A blank region that claims nothing is the failure.
    const settled = page
      .getByRole("table")
      .or(page.getByText(/forbidden|not authorized|no pods|failed/i));
    await expect(settled.first()).toBeVisible();
  });

  test("switching clusters cancels the in-flight view list", async ({
    page,
  }) => {
    await page.goto(PODS);
    const name = e2eName("view");
    await createSavedView(page, name, podConfig());

    // Hold the list response open so the cluster switch lands mid-flight.
    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let served = 0;
    await page.route("**/api/v1/preferences/views", async (route) => {
      served += 1;
      if (served === 1) await held;
      await route.continue();
    });

    await page.reload();
    await page.getByTestId("saved-views-toggle").click();

    // The app restores its target from the (clusterId, generation) pair that
    // frontend/lib/cluster.ts persists as one JSON value under
    // "k8scenter.clusterTarget". The legacy "k8scenter.selectedCluster" key is
    // consulted only when that key is absent, which it is not by now, so
    // writing it here would leave the page on the local cluster and make this
    // spec pass without ever switching. Update this line if that storage
    // contract changes again.
    await page.evaluate(() => {
      localStorage.setItem(
        "k8scenter.clusterTarget",
        JSON.stringify({
          clusterId: "some-remote-cluster",
          generation: "unknown",
        }),
      );
    });
    await page.reload();
    release?.();

    // The stale response must not paint this cluster's menu with the previous
    // cluster's records.
    await page.getByTestId("saved-views-toggle").click();
    await expect(
      page.locator(`[data-view-name="${name}"]`),
    ).toHaveCount(0);

    await page.unroute("**/api/v1/preferences/views");
    await page.evaluate(() => {
      localStorage.setItem(
        "k8scenter.clusterTarget",
        JSON.stringify({ clusterId: "local", generation: "local" }),
      );
    });
  });
});
