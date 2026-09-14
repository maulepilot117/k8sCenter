import { type Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import {
  attachAuthInjection,
  bearerHeaders,
  createPin,
  deleteAllPins,
  e2eName,
  e2eSecureName,
  getAuthHeaders,
  loginThroughUI,
  type PinConfigSeed,
  watchForSubscription,
} from "../helpers.ts";

/**
 * Personal resource pins (Release A, U5a + U5b).
 *
 * The behaviour under test is that a pin names one object and keeps naming
 * it. A deleted target, a same-named replacement, a revoked permission and
 * another user's record must each be reported as themselves; none of them may
 * quietly resolve to a different object, and none may render as an ordinary
 * healthy pin.
 *
 * ConfigMaps are the fixture kind on purpose. They are cheap to create and
 * recreate, and ACTIONS_BY_KIND grants them no actions at all, which makes a
 * ConfigMap detail page the exact "zero visible actions" case that the
 * actionButtons restructure had to stop hiding.
 *
 * NOT covered here, and covered in U2's Go tests instead:
 *   - the no-database 503. The E2E harness always runs PostgreSQL (see
 *     playwright.config.ts webServer env), so "database_unavailable" is
 *     unreachable from a browser; TestHandler_NoDatabase_Returns503 owns it.
 *   - stale-revision conflicts. A pin has no PUT, so there is no revision to
 *     go stale.
 */

const NS = "default";
const CM_KIND = "configmaps";
const created: string[] = [];

function detailPath(name: string): string {
  return `/config/configmaps/${NS}/${name}`;
}

function pinConfig(
  name: string,
  uid: string,
  overrides: Partial<PinConfigSeed> = {},
): PinConfigSeed {
  return {
    schemaVersion: 1,
    resourceKind: CM_KIND,
    group: "",
    version: "",
    namespace: NS,
    name,
    uid,
    displayKind: "ConfigMap",
    ...overrides,
  };
}

/** Create a ConfigMap through the API and return its fresh uid. */
async function createConfigMap(page: Page, name: string): Promise<string> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.post(
    `/api/v1/resources/${CM_KIND}/${NS}`,
    {
      headers,
      data: {
        apiVersion: "v1",
        kind: "ConfigMap",
        metadata: { name, namespace: NS },
        data: { seeded: "e2e" },
      },
    },
  );
  if (!res.ok()) {
    throw new Error(
      `createConfigMap(${name}) failed: ${res.status()} ${await res.text()}`,
    );
  }
  if (!created.includes(name)) created.push(name);
  return (await res.json()).data.metadata.uid as string;
}

async function deleteConfigMap(page: Page, name: string): Promise<void> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.delete(
    `/api/v1/resources/${CM_KIND}/${NS}/${name}`,
    { headers, failOnStatusCode: false },
  );
  // 404 is success for teardown -- the object is gone, which is the point.
  // Any other failure is reported rather than swallowed: a suite that leaves
  // ConfigMaps behind should say so instead of going green.
  if (!res.ok() && res.status() !== 404) {
    throw new Error(`could not delete ConfigMap ${name}: ${res.status()}`);
  }
}

/**
 * Provision a second identity, run `body` with it, and always remove it.
 *
 * Two rules this encodes, both learned the hard way:
 *
 * 1. Only a 429 skips. The auth endpoints share one 5-per-minute bucket per
 *    IP, so a rate-limited run is a genuine environment limit. Every other
 *    failure -- a policy rejection, a broken users endpoint, a login that
 *    stops working -- is a regression, and skipping on it would let the
 *    cross-user assertions silently stop running while CI stayed green.
 * 2. Cleanup is in `finally`. The account (and anything it owns) must not
 *    survive a failed assertion: the admin teardown cannot delete another
 *    user's records, so a leak here is permanent.
 */
async function withSecondUser(
  page: Page,
  body: (token: string, username: string) => Promise<void>,
): Promise<void> {
  const username = e2eSecureName("user");
  const password = `e2e-${crypto.randomUUID()}`;
  const headers = await getAuthHeaders(page);

  const createdUser = await page.request.post("/api/v1/users", {
    headers,
    data: {
      username,
      password,
      k8sUsername: username,
      k8sGroups: [],
      roles: ["viewer"],
    },
    failOnStatusCode: false,
  });
  if (createdUser.status() === 429) {
    test.skip(true, "auth rate limiter is saturated; second identity refused");
    return;
  }
  if (!createdUser.ok()) {
    throw new Error(
      `creating the second user failed: ${createdUser.status()} ${await createdUser
        .text()}`,
    );
  }
  const createdId = (await createdUser.json())?.data?.id;

  try {
    const login = await page.request.post("/api/v1/auth/login", {
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "XMLHttpRequest",
      },
      data: { username, password },
      failOnStatusCode: false,
    });
    if (login.status() === 429) {
      test.skip(true, "auth rate limiter is saturated; second login refused");
      return;
    }
    if (!login.ok()) {
      throw new Error(
        `second login failed: ${login.status()} ${await login.text()}`,
      );
    }
    await body((await login.json()).data.accessToken as string, username);
  } finally {
    if (createdId) {
      await page.request.delete(`/api/v1/users/${createdId}`, {
        headers,
        failOnStatusCode: false,
      });
    }
  }
}

/** The pin records the current user owns, straight from the API. */
async function listPins(page: Page): Promise<
  Array<{ id: string; name: string; config: PinConfigSeed }>
> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.get("/api/v1/preferences/pins", { headers });
  return (await res.json()).data ?? [];
}

test.describe.serial("Resource pins", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/config/configmaps");
    // Belt and braces with the cluster spec's own finally: these specs are
    // serial, so a cluster left selected by a failed spec would silently
    // re-point every later one at a cluster that does not exist.
    await page.evaluate(() =>
      localStorage.setItem(
        "k8scenter.clusterTarget",
        JSON.stringify({ clusterId: "local", generation: "local" }),
      )
    );
    await deleteAllPins(page);
  });

  test.afterAll(async ({ browser }) => {
    // afterAll gets no page fixture; open one so cleanup can authenticate.
    const context = await browser.newContext({
      storageState: "playwright/.auth/admin.json",
    });
    const page = await context.newPage();
    await attachAuthInjection(page);
    await page.goto("/config/configmaps");
    await deleteAllPins(page);
    for (const name of created) await deleteConfigMap(page, name);
    await context.close();
  });

  test("pins a resource from detail and shows it in the secondary nav", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);

    await page.goto(detailPath(name));
    await expect(page.getByRole("heading", { name })).toBeVisible();

    const toggle = page.getByTestId("pin-toggle");
    await expect(toggle).toHaveAttribute("data-pin-state", "unpinned");
    await page.getByTestId("pin-button").click();
    await expect(toggle).toHaveAttribute("data-pin-state", "pinned");

    // A routable row, not the unsupported-kind fallback: pins store the
    // adapter slug ("configmaps"), and a link builder that pluralizes it
    // again would render every pin as a dead row.
    const row = page.locator(
      `[data-testid="pinned-row"][data-pin-name="${name}"]`,
    );
    await expect(row).toBeVisible();
    await expect(row).toHaveAttribute("href", detailPath(name));
    await expect(
      page.locator(
        `[data-testid="pinned-row-unsupported"][data-pin-name="${name}"]`,
      ),
    ).toHaveCount(0);

    // The stored record is what proves the pin, not the rendered row.
    const stored = (await listPins(page)).find((p) => p.config.name === name);
    expect(stored?.config.resourceKind).toBe(CM_KIND);
    expect(stored?.config.uid).toBeTruthy();
  });

  test("unpinning from the nav row does not navigate", async ({ page }) => {
    const name = e2eName("cm");
    const uid = await createConfigMap(page, name);
    await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, uid));

    await page.goto(detailPath(name));
    const row = page.locator(
      `[data-testid="pinned-row"][data-pin-name="${name}"]`,
    );
    await expect(row).toBeVisible();

    const before = page.url();
    // The unpin control sits inside the row anchor. Without preventDefault the
    // click navigates before the request is even sent.
    await row.getByTestId("unpin").click();

    await expect(row).toHaveCount(0);
    expect(page.url()).toBe(before);
    // And the detail control agrees, because both read one shared store.
    await expect(page.getByTestId("pin-toggle")).toHaveAttribute(
      "data-pin-state",
      "unpinned",
    );
    expect(await listPins(page)).toHaveLength(0);
  });

  test("a deleted pin target is marked unavailable and does not switch targets", async ({ page }) => {
    const name = e2eName("cm");
    const uid = await createConfigMap(page, name);
    await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, uid));

    await deleteConfigMap(page, name);

    await page.goto("/config/configmaps");
    const row = page.locator(
      `[data-testid="pinned-row"][data-pin-name="${name}"]`,
    );
    await expect(row).toBeVisible();
    await row.click();

    // Lands on the pinned target and says it is gone. The failure this guards
    // is landing somewhere else -- a list page, or another object -- which
    // would read as "the pin still works".
    await expect(page).toHaveURL(new RegExp(`${name}$`));
    await expect(page.getByText(/not found/i).first()).toBeVisible();

    // Deliberately NOT asserting that the pin toggle is absent here: on a
    // cold load ResourceDetail renders no action area at all when the fetch
    // fails, so that assertion holds even with the pin control deleted from
    // the build. It would look like coverage and prove nothing. The live
    // deletion path, where the control IS mounted and must stop claiming the
    // resource is pinned, is covered by its own spec below.

    // And the record survives, still naming the object the user pinned.
    const stored = (await listPins(page)).find((p) => p.config.name === name);
    expect(stored?.config.uid).toBe(uid);
  });

  test("a pin list that cannot be loaded renders unavailable, never unpinned", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);

    // The R3 invariant in its sharpest form: when the preference service
    // cannot answer, the control must not fall back to offering "Pin" as
    // though it knew this resource was unpinned.
    await page.route("**/api/v1/preferences/pins", (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: 503,
            message: "preferences unavailable",
            reason: "database_unavailable",
          },
        }),
      }));

    await page.goto(detailPath(name));
    await expect(page.getByRole("heading", { name })).toBeVisible();

    const toggle = page.getByTestId("pin-toggle");
    await expect(toggle).toHaveAttribute("data-pin-state", "unavailable");
    await expect(page.getByTestId("pin-button")).toBeDisabled();
    // The navigation says the same thing rather than rendering an empty list.
    await expect(page.getByTestId("pinned-unavailable")).toBeVisible();
  });

  test("a recreated same-name resource is reported as replaced, not silently inherited", async ({ page }) => {
    const name = e2eName("cm");
    const originalUid = await createConfigMap(page, name);
    await createPin(
      page,
      `ConfigMap ${NS}/${name}`,
      pinConfig(name, originalUid),
    );

    await deleteConfigMap(page, name);
    const newUid = await createConfigMap(page, name);
    expect(newUid).not.toBe(originalUid);

    await page.goto(detailPath(name));
    await expect(page.getByRole("heading", { name })).toBeVisible();
    await expect(page.getByTestId("pin-toggle")).toHaveAttribute(
      "data-pin-state",
      "replaced",
    );

    // Until the user acts, the stored uid still names the object they pinned.
    // Silently adopting the new one is the bug this spec exists to catch.
    const stored = (await listPins(page)).find((p) => p.config.name === name);
    expect(stored?.config.uid).toBe(originalUid);
  });

  test("re-pinning a replaced object records the new uid", async ({ page }) => {
    const name = e2eName("cm");
    const originalUid = await createConfigMap(page, name);
    await createPin(
      page,
      `ConfigMap ${NS}/${name}`,
      pinConfig(name, originalUid),
    );
    await deleteConfigMap(page, name);
    const newUid = await createConfigMap(page, name);

    await page.goto(detailPath(name));
    const toggle = page.getByTestId("pin-toggle");
    await expect(toggle).toHaveAttribute("data-pin-state", "replaced");

    await page.getByTestId("pin-repin").click();
    await expect(toggle).toHaveAttribute("data-pin-state", "pinned");

    const after = await listPins(page);
    const stored = after.find((p) => p.config.name === name);
    expect(stored?.config.uid).toBe(newUid);
    // Re-pinning replaces the record rather than accumulating a second one.
    expect(after.filter((p) => p.config.name === name)).toHaveLength(1);
  });

  test("a pin stored without verifiable identity is reported as unverified, not ok", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);
    // An empty uid is accepted by the server (it is evidence, not identity)
    // and is what a client that could not read the object would store.
    await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, ""));

    await page.goto(detailPath(name));
    await expect(page.getByRole("heading", { name })).toBeVisible();

    // The name matches, which is exactly why this must not read as "Pinned":
    // a matching name is not evidence that this is the object that was
    // pinned, and four of these in a row would hide one real replacement.
    await expect(page.getByTestId("pin-toggle")).toHaveAttribute(
      "data-pin-state",
      "unknown",
    );
  });

  test("a pin whose namespace access was revoked renders forbidden, not missing", async ({ page, browser }) => {
    const name = e2eName("cm");
    const uid = await createConfigMap(page, name);

    await withSecondUser(page, async (theirToken) => {
      const context = await browser.newContext();
      try {
        const theirPage = await context.newPage();
        await attachAuthInjection(theirPage, theirToken);
        await theirPage.goto("/config/configmaps");

        // Their own pin, pointing at an object their Kubernetes identity
        // cannot read. The pin is theirs; the permission is not.
        await createPin(
          theirPage,
          `ConfigMap ${NS}/${name}`,
          pinConfig(name, uid),
        );
        try {
          await theirPage.goto(detailPath(name));

          // Forbidden and missing are different facts. Telling this user the
          // resource is gone would be a lie -- it exists, they just cannot
          // see it. Scoped to the detail error region so unrelated page
          // chrome can never satisfy the match.
          const banner = theirPage.getByTestId("detail-error");
          await expect(banner).toBeVisible();
          await expect(banner).toHaveText(
            /permission|forbidden|not authorized/i,
          );
          await expect(banner).not.toHaveText(/not found/i);

          // The pin itself survives the failed lookup: the record is not
          // evidence about the object, and losing it would punish the user
          // for an RBAC change they did not make.
          const theirPins = await listPins(theirPage);
          expect(theirPins.some((p) => p.config.name === name)).toBe(true);
        } finally {
          // Their records must go even if an assertion above failed -- the
          // admin teardown cannot reach another user's pins.
          for (const record of await listPins(theirPage)) {
            await theirPage.request.delete(
              `/api/v1/preferences/pins/${record.id}`,
              { headers: bearerHeaders(theirToken), failOnStatusCode: false },
            );
          }
        }
      } finally {
        await context.close();
      }
    });
  });

  test("another user's pins are not listed and their ids 404", async ({ page }) => {
    const name = e2eName("cm");
    const uid = await createConfigMap(page, name);
    const id = await createPin(
      page,
      `ConfigMap ${NS}/${name}`,
      pinConfig(name, uid),
    );

    await withSecondUser(page, async (theirToken) => {
      // Asserted at the API with the other user's own token: the guarantee is
      // that the server scopes every record to its owner, and an anonymous
      // request answers 401 long before that check is reached.
      const theirHeaders = bearerHeaders(theirToken);

      const theirList = await page.request.get("/api/v1/preferences/pins", {
        headers: theirHeaders,
      });
      expect(theirList.status()).toBe(200);
      const theirBody = await theirList.json();
      expect((theirBody.data ?? []).some((r: { id: string }) => r.id === id))
        .toBe(false);

      // 404, not 403: a 403 would confirm the record exists, which is itself
      // a disclosure.
      const deleteAttempt = await page.request.delete(
        `/api/v1/preferences/pins/${id}`,
        { headers: theirHeaders, failOnStatusCode: false },
      );
      expect(deleteAttempt.status()).toBe(404);

      const mine = await listPins(page);
      expect(mine.some((p) => p.id === id)).toBe(true);
    });
  });

  test("the pin toggle is visible on a resource with no available actions", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);
    await page.goto(detailPath(name));
    await expect(page.getByRole("heading", { name })).toBeVisible();

    // ConfigMaps have no entry in ACTIONS_BY_KIND, so this page is the same
    // code path an RBAC-restricted user hits on any kind: zero visible
    // actions. The old condition hid the entire action area in that case,
    // which would have taken the pin control with it. Deterministic where a
    // second user is not -- user creation depends on the auth rate limiter.
    await expect(page.getByTestId("pin-toggle")).toBeVisible();
    await expect(page.getByRole("button", { name: /^Delete$/ })).toHaveCount(0);
  });

  test("the pin toggle is disabled until it knows this resource's pin state", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);

    // Hold the pin list open so the detail page renders before its pin state
    // is known. The uid-undefined half of the same guard is unreachable from
    // a cold load -- ResourceDetail renders no action area at all until the
    // resource resolves -- so this is the branch a browser can actually see.
    //
    // Every request is held, not just the first: holding one and letting a
    // second through would make this pass or fail on how many loaders the
    // page happens to have, which is exactly the coupling the control is
    // meant not to have.
    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    await page.route("**/api/v1/preferences/pins", async (route) => {
      await held;
      await route.continue();
    });

    await page.goto(detailPath(name));
    const toggle = page.getByTestId("pin-toggle");
    await expect(toggle).toHaveAttribute("data-pin-state", "loading");
    // Offering "Pin" before the answer is known would let a second pin be
    // created for an already-pinned resource.
    await expect(page.getByTestId("pin-button")).toBeDisabled();

    release?.();
    await expect(toggle).toHaveAttribute("data-pin-state", "unpinned");
    await expect(page.getByTestId("pin-button")).toBeEnabled();
  });

  test("switching clusters reloads pins and cancels the prior request", async ({ page }) => {
    const name = e2eName("cm");
    const uid = await createConfigMap(page, name);
    await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, uid));

    let release: (() => void) | undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let served = 0;
    await page.route("**/api/v1/preferences/pins", async (route) => {
      served += 1;
      if (served === 1) await held;
      await route.continue();
    });

    await page.goto(detailPath(name));
    try {
      // See the note in saved-views.spec.ts: the restored target is the JSON
      // pair under "k8scenter.clusterTarget", and writing the legacy key here
      // would leave this spec on the local cluster and passing vacuously.
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

      // A pin names one object in one cluster. The held response belongs to
      // the previous cluster and must never paint this one's navigation.
      await expect(
        page.locator(`[data-pin-name="${name}"]`),
      ).toHaveCount(0);
    } finally {
      // Restore on the failure path too: the file is serial, so leaving a
      // nonexistent cluster selected would make every later spec fail for a
      // reason that has nothing to do with what it tests.
      await page.evaluate(() => {
        localStorage.setItem(
          "k8scenter.clusterTarget",
          JSON.stringify({ clusterId: "local", generation: "local" }),
        );
      });
    }
  });

  test("long resource names stay readable in the nav row", async ({ page }) => {
    // 63 characters: a realistic generated name, and enough to overflow a
    // sidebar row.
    const name = `e2e-cm-${"long".repeat(12)}-${
      crypto.randomUUID().slice(0, 8)
    }`
      .slice(0, 63)
      .replace(/-$/, "x");
    const uid = await createConfigMap(page, name);
    await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, uid));

    await page.goto(detailPath(name));
    const row = page.locator(
      `[data-testid="pinned-row"][data-pin-name="${name}"]`,
    );
    await expect(row).toBeVisible();

    // Clipped with an ellipsis and titled with the full identity, rather than
    // pushing the unpin control off the edge of the sidebar.
    const label = row.locator("span").nth(1);
    await expect(label).toHaveCSS("text-overflow", "ellipsis");
    await expect(label).toHaveAttribute("title", `ConfigMap ${NS}/${name}`);

    const rowBox = await row.boundingBox();
    const navBox = await page.getByTestId("pinned-resources").boundingBox();
    expect(rowBox!.width).toBeLessThanOrEqual(navBox!.width + 1);
    await expect(row.getByTestId("unpin")).toBeVisible();
  });

  test("pin and unpin are reachable and labelled for keyboard and screen-reader users", async ({ page }) => {
    const name = e2eName("cm");
    await createConfigMap(page, name);
    await page.goto(detailPath(name));

    const pinButton = page.getByTestId("pin-button");
    await expect(pinButton).toHaveAttribute(
      "aria-label",
      `Pin ConfigMap ${name}`,
    );

    // Operable from the keyboard alone, not only by pointer.
    await pinButton.focus();
    await expect(pinButton).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("pin-toggle")).toHaveAttribute(
      "data-pin-state",
      "pinned",
    );
    await expect(pinButton).toHaveAttribute(
      "aria-label",
      `Unpin ConfigMap ${name}`,
    );

    // The nav control names its target too: "Unpin" alone would be identical
    // on every row for a screen-reader user moving through the list.
    const navUnpin = page
      .locator(`[data-testid="pinned-row"][data-pin-name="${name}"]`)
      .getByTestId("unpin");
    await expect(navUnpin).toHaveAttribute(
      "aria-label",
      `Unpin ConfigMap ${name}`,
    );
    await navUnpin.focus();
    await expect(navUnpin).toBeFocused();
  });
  test("a delete that arrives while the page is open stops the pin reading as healthy", async ({ browser }) => {
    // Own context with a real login: the shared fixture's token injection
    // stops the app from ever populating the in-memory token that lib/ws.ts
    // needs, so a socket opened under it never authenticates and no live
    // event can arrive. See loginThroughUI.
    const context = await browser.newContext();
    try {
      const page = await context.newPage();
      const subscribed = watchForSubscription(page, CM_KIND);
      await loginThroughUI(page);

      const name = e2eName("cm");
      const uid = await createConfigMap(page, name);
      await createPin(page, `ConfigMap ${NS}/${name}`, pinConfig(name, uid));

      await page.goto(detailPath(name));
      const toggle = page.getByTestId("pin-toggle");
      await expect(toggle).toHaveAttribute("data-pin-state", "pinned");

      // A deletion published before this page subscribes is delivered to
      // nobody, and the spec would then assert against an event that was
      // never sent to it.
      await expect.poll(subscribed, { timeout: 20_000 }).toBe(true);

      // Delete it out from under the open page. ResourceDetail keeps
      // rendering the object it already has, so the control must learn the
      // target is gone from the deletion event rather than from the (still
      // matching) uid.
      await deleteConfigMap(page, name);

      await expect(toggle).toHaveAttribute("data-pin-state", "missing");
      await expect(page.getByTestId("pin-button")).toHaveAttribute(
        "aria-label",
        `Unpin ConfigMap ${name}`,
      );

      // Unpinning still works from here -- this is the one screen where the
      // user knows the pin is stale, so it must not be a dead end.
      await page.getByTestId("pin-button").click();
      await expect(toggle).toHaveAttribute("data-pin-state", "unpinned");
      expect(await listPins(page)).toHaveLength(0);
    } finally {
      await context.close();
    }
  });
});
