import {
  type APIRequestContext,
  type APIResponse,
  type Browser,
  type BrowserContext,
  expect,
  type Page,
} from "@playwright/test";

/** Get the stored E2E access token from the browser context's localStorage */
export async function getAuthHeaders(
  page: Page,
): Promise<Record<string, string>> {
  const token = await page.evaluate(() =>
    localStorage.getItem("e2e_access_token")
  );
  return {
    "X-Requested-With": "XMLHttpRequest",
    "Content-Type": "application/json",
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

/**
 * localStorage key the app restores its cluster target from. Mirrors
 * TARGET_KEY in frontend/src/lib/cluster.ts (readPersistedTarget /
 * persistTarget); update both together if that storage contract changes.
 *
 * The legacy "k8scenter.selectedCluster" key is consulted only when this one
 * is absent, so a spec that writes the legacy key after the app has run once
 * stays on whatever this key holds and passes without ever switching.
 */
export const CLUSTER_TARGET_KEY = "k8scenter.clusterTarget";

/**
 * The JSON value stored under CLUSTER_TARGET_KEY for `clusterId`, in the
 * shape persistTarget writes. The local cluster's generation is the literal
 * "local"; a remote whose real generation the spec does not know is restored
 * under "unknown", which the app replaces once the cluster list resolves.
 */
export function clusterTargetValue(clusterId: string): string {
  return JSON.stringify({
    clusterId,
    generation: clusterId === "local" ? "local" : "unknown",
  });
}

/**
 * Select `clusterId` before the next load, the way ClusterSwitcher persists
 * it. An init script, so it runs on every later navigation in that page or
 * context too.
 */
export async function seedClusterTarget(
  target: Page | BrowserContext,
  clusterId: string,
): Promise<void> {
  // Serialized into the browser: it cannot close over this module, so the
  // key and value travel as arguments.
  await target.addInitScript(
    ([key, value]: [string, string]) => localStorage.setItem(key, value),
    [CLUSTER_TARGET_KEY, clusterTargetValue(clusterId)] as [string, string],
  );
}

/**
 * Select `clusterId` in an already-loaded page. Takes effect on the next
 * load; the running page keeps its current target until then.
 */
export async function setClusterTarget(
  page: Page,
  clusterId: string,
): Promise<void> {
  await page.evaluate(
    ([key, value]: [string, string]) => localStorage.setItem(key, value),
    [CLUSTER_TARGET_KEY, clusterTargetValue(clusterId)] as [string, string],
  );
}

/** Generate a unique E2E resource name (8-char random suffix) */
export function e2eName(kind: string): string {
  const rand = Math.random().toString(36).slice(2, 10);
  return `e2e-${kind}-${rand}`;
}

/** Delete a namespaced k8s resource via the API */
export async function deleteResource(
  request: APIRequestContext,
  kind: string,
  namespace: string,
  name: string,
) {
  await request.delete(`/api/v1/resources/${kind}/${namespace}/${name}`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });
}

/** Delete a cluster-scoped k8s resource via the API */
export async function deleteClusterResource(
  request: APIRequestContext,
  kind: string,
  name: string,
) {
  await request.delete(`/api/v1/resources/${kind}/${name}`, {
    headers: { "X-Requested-With": "XMLHttpRequest" },
    failOnStatusCode: false,
  });
}

/** Wait for the resource table to finish loading (data rows present) */
export async function waitForTableLoaded(page: Page) {
  await expect(page.getByRole("table")).toBeVisible();
  // Wait for spinner to disappear AND at least the table to be stable
  await expect(page.locator(".animate-spin")).not.toBeVisible();
}

/**
 * Shape of a saved-view config, mirroring SavedViewConfig in
 * frontend/lib/preference-types.ts. Declared loosely on purpose: specs seed
 * values the UI would refuse (an unsupported sortKey, for instance) to prove
 * the restore path reports the degradation instead of hiding it.
 */
export type SavedViewConfigSeed = {
  schemaVersion: number;
  resourceKind: string;
  namespace: string;
  search: string;
  statusFilter: string;
  sortKey: string;
  sortDir: string;
};

/** The two preference collections, as they appear in the API path. */
type PreferenceKind = "views" | "pins";

/**
 * Create a preference record directly through the API and return its id.
 *
 * Goes through page.request so the call carries the page's own cluster and
 * identity, which is what makes a seeded record belong to the same user the
 * browser is logged in as.
 */
async function createPreferenceRecord(
  page: Page,
  kind: PreferenceKind,
  name: string,
  config: SavedViewConfigSeed | PinConfigSeed,
  clusterId?: string,
): Promise<string> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.post(`/api/v1/preferences/${kind}`, {
    headers: clusterId ? { ...headers, "X-Cluster-ID": clusterId } : headers,
    data: { name, config },
  });
  if (!res.ok()) {
    throw new Error(
      `create ${kind} record "${name}" failed: ${res.status()} ${await res
        .text()}`,
    );
  }
  return (await res.json()).data.id as string;
}

/**
 * Remove every preference record of one kind the current user owns, on every
 * cluster.
 *
 * Exhaustive rather than scoped to the active cluster: a spec that seeds an
 * other-cluster record would otherwise leave it behind, and the per-user
 * ceilings would eventually start failing unrelated specs.
 *
 * Teardown failures throw rather than passing quietly. A cleanup that returns
 * on a failed list, or ignores a failed delete, lets the suite report success
 * while records survive into the next run -- and the symptom then appears in
 * some unrelated spec that trips a ceiling. A 404 on the delete is the one
 * tolerated outcome: the record is already gone, which is what was wanted.
 */
async function deleteAllPreferenceRecords(
  page: Page,
  kind: PreferenceKind,
): Promise<void> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.get(`/api/v1/preferences/${kind}`, {
    headers,
  });
  if (!res.ok()) {
    throw new Error(
      `cleanup could not list ${kind}: ${res.status()} ${await res.text()}`,
    );
  }
  const body = await res.json();
  for (const record of body.data ?? []) {
    const del = await page.request.delete(
      `/api/v1/preferences/${kind}/${record.id}`,
      { headers, failOnStatusCode: false },
    );
    if (!del.ok() && del.status() !== 404) {
      throw new Error(
        `cleanup could not delete ${kind} ${record.id}: ${del.status()}`,
      );
    }
  }
}

/** Create a saved view directly through the API and return its record id. */
export async function createSavedView(
  page: Page,
  name: string,
  config: SavedViewConfigSeed,
  clusterId?: string,
): Promise<string> {
  return await createPreferenceRecord(page, "views", name, config, clusterId);
}

/** Remove every saved view the current user owns, on every cluster. */
export async function deleteAllSavedViews(page: Page): Promise<void> {
  await deleteAllPreferenceRecords(page, "views");
}

/**
 * Shape of a pin config, mirroring PinConfig in
 * frontend/lib/preference-types.ts. Loose for the same reason the saved-view
 * seed is: specs seed uids the UI would never write, to reach the replaced
 * and unverified classifications.
 */
export type PinConfigSeed = {
  schemaVersion: number;
  resourceKind: string;
  group: string;
  version: string;
  namespace: string;
  name: string;
  uid: string;
  displayKind: string;
};

/** Create a pin directly through the API and return its record id. */
export async function createPin(
  page: Page,
  name: string,
  config: PinConfigSeed,
  clusterId?: string,
): Promise<string> {
  return await createPreferenceRecord(page, "pins", name, config, clusterId);
}

/** Remove every pin the current user owns, on every cluster. */
export async function deleteAllPins(page: Page): Promise<void> {
  await deleteAllPreferenceRecords(page, "pins");
}

/**
 * Apply the Bearer-token fetch injection that fixtures/base.ts applies.
 *
 * A context created with browser.newContext() does NOT get the base fixture,
 * so its requests go out anonymous even though storageState restored the
 * cookie and the stored token: the app bounces to /login, and API calls answer
 * 401 before any ownership check runs. Any spec that opens its own context has
 * to re-apply this, or it is testing the login page.
 *
 * Pass `token` to authenticate as somebody other than the stored user;
 * omit it to use whatever the context's storageState carries.
 */
export async function attachAuthInjection(
  page: Page,
  token?: string,
): Promise<void> {
  await page.addInitScript((injected: string | null) => {
    const t = injected ?? localStorage.getItem("e2e_access_token");
    if (!t) return;
    if (injected) localStorage.setItem("e2e_access_token", injected);
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (input, init) => {
      const url = typeof input === "string"
        ? input
        : input instanceof URL
        ? input.href
        : (input as Request).url;
      if (url.includes("/api/") || url.includes("/ws/")) {
        init = init || {};
        const h = new Headers(init.headers);
        if (!h.has("Authorization")) h.set("Authorization", `Bearer ${t}`);
        init.headers = h;
      }
      return originalFetch(input, init);
    };
  }, token ?? null);
}

/**
 * Log in through the API and return the access token.
 *
 * The page must belong to the identity being logged in: the login answers
 * with an httpOnly refresh cookie that lands in the page's cookie jar. To
 * sign in as a second user, use createSecondUser, which logs in from that
 * user's own context.
 */
export async function loginViaApi(
  page: Page,
  username: string,
  password: string,
): Promise<string> {
  const res = await page.request.post("/api/v1/auth/login", {
    headers: {
      "Content-Type": "application/json",
      "X-Requested-With": "XMLHttpRequest",
    },
    data: { username, password },
  });
  if (!res.ok()) {
    throw new Error(
      `loginViaApi(${username}) failed: ${res.status()} ${await res.text()}`,
    );
  }
  return (await res.json()).data.accessToken as string;
}

/**
 * Bearer headers for a specific token, for asserting what another identity
 * can reach. Distinct from getAuthHeaders, which reads the page's own token.
 */
export function bearerHeaders(token: string): Record<string, string> {
  return {
    "X-Requested-With": "XMLHttpRequest",
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
  };
}

/**
 * POSTs, waiting out 429s from the shared 5-per-minute auth bucket with
 * backoff (honouring Retry-After). Fails loudly if the bucket never clears,
 * rather than skipping: a skipped isolation test looks like a passing one.
 *
 * For the requests a second-identity spec makes against that bucket:
 * creating the user (`POST /api/v1/users`) and logging it in
 * (`POST /api/v1/auth/login`). createSecondUser makes both.
 *
 * The page/context must belong to the identity being logged in.
 * `page.request` shares its context's cookie jar, and a login answers with
 * the httpOnly refresh cookie, so logging user B in through user A's page
 * replaces A's refresh cookie with B's: A's next token refresh then silently
 * turns that page into B (in incidents.spec.ts the owner's revoke failed as
 * "only the incident owner may revoke its grants"). Create the account
 * through the admin's page, but log it in through a page of its own.
 */
export async function postWithBackoff(
  page: Page,
  url: string,
  what: string,
  init: { headers: Record<string, string>; data: unknown },
): Promise<APIResponse> {
  const deadline = Date.now() + 130_000;
  for (let attempt = 1; ; attempt++) {
    const res = await page.request.post(url, {
      ...init,
      failOnStatusCode: false,
    });
    if (res.status() !== 429) return res;
    if (Date.now() > deadline) {
      throw new Error(
        `${what}: still rate limited (429) after ${attempt} attempts; the shared auth bucket never cleared, so cross-user isolation was NOT checked`,
      );
    }
    const retryAfter = Number(res.headers()["retry-after"]);
    const waitS = Number.isFinite(retryAfter) && retryAfter > 0
      ? Math.min(retryAfter, 30)
      : Math.min(5 * 2 ** (attempt - 1), 30);
    await page.waitForTimeout(waitS * 1000);
  }
}

/**
 * A unique name backed by crypto.randomUUID rather than Math.random.
 *
 * e2eName is fine for k8s object names, but a value that becomes an account
 * identity is a security context, and CodeQL flags Math.random there --
 * correctly, since a predictable identity is a weakness even in a test.
 */
export function e2eSecureName(kind: string): string {
  return `e2e${kind}${crypto.randomUUID().replace(/-/g, "").slice(0, 12)}`;
}

/** One teardown action; it may return nothing or a promise of anything. */
export type Cleanup = () => unknown;

/**
 * Runs every cleanup in order, each one whether or not an earlier one threw,
 * then throws if any failed. A teardown that stopped at its first failure
 * would leave the rest behind for an unrelated later spec to trip over.
 */
export async function runCleanups(cleanups: readonly Cleanup[]): Promise<void> {
  const failures: unknown[] = [];
  for (const cleanup of cleanups) {
    try {
      await cleanup();
    } catch (err) {
      failures.push(err);
    }
  }
  if (failures.length === 1) throw failures[0];
  if (failures.length > 1) {
    throw new AggregateError(
      failures,
      `${failures.length} cleanups failed: ${failures.map(String).join("; ")}`,
    );
  }
}

/**
 * Runs `body`, then every cleanup (see runCleanups), whatever happened.
 *
 * A cleanup failure never masks the test's own failure: when `body` threw,
 * that error is the one rethrown and the cleanup failures are logged beside
 * it. A bare `finally` that throws would replace the test's error with the
 * teardown's, and the report would blame the wrong thing.
 *
 * Put everything that creates server state inside `body`, so a failure part
 * way through setup is still cleaned up. Cleanups run after `body`, so they
 * can read variables `body` assigned.
 */
export async function withCleanup(
  body: () => Promise<void>,
  cleanups: readonly Cleanup[],
): Promise<void> {
  try {
    await body();
  } catch (err) {
    try {
      await runCleanups(cleanups);
    } catch (cleanupErr) {
      console.error("cleanup also failed after the test failed:", cleanupErr);
    }
    throw err;
  }
  await runCleanups(cleanups);
}

/**
 * A second local user, signed in from a browser context of its own.
 *
 * `token` authenticates its API calls (with bearerHeaders); `page` is in its
 * own context with the auth injection applied, so its UI runs as this user
 * alone. `remove` closes that context and deletes the account.
 */
export interface SecondUser {
  id: string;
  username: string;
  token: string;
  context: BrowserContext;
  page: Page;
  remove: () => Promise<void>;
}

/**
 * Creates a local viewer with no Kubernetes RBAC, logs it in, and returns it
 * (see SecondUser). `label` names it in failure messages ("the collaborator").
 *
 * The admin's page creates the account; the LOGIN, and every later call the
 * user makes, goes through the user's own fresh context, never the admin's
 * (see postWithBackoff for the refresh-cookie swap the other way invites).
 * The context starts from an empty storage state, so the admin's refresh
 * cookie and stored token never reach it either.
 *
 * Both POSTs spend the shared 5-per-minute auth bucket, so a caller should
 * allow for postWithBackoff's waits in its timeout. The id comes from
 * /auth/me, which is the identity grants and ownership are keyed on.
 *
 * `remove` reads the admin page's token when it runs rather than reusing
 * headers captured at creation, so it authenticates with whatever token that
 * page holds at teardown.
 */
export async function createSecondUser(
  adminPage: Page,
  browser: Browser,
  label: string,
): Promise<SecondUser> {
  const username = e2eSecureName("user");
  const password = `e2e-${crypto.randomUUID()}`;
  const context = await browser.newContext({
    storageState: { cookies: [], origins: [] },
  });
  let accountId: string | undefined;
  const remove = () =>
    runCleanups([
      () => context.close(),
      async () => {
        if (!accountId) return;
        const res = await adminPage.request.delete(
          `/api/v1/users/${accountId}`,
          { headers: await getAuthHeaders(adminPage), failOnStatusCode: false },
        );
        if (!res.ok() && res.status() !== 404) {
          throw new Error(
            `could not delete ${label} (${accountId}): ${res.status()}`,
          );
        }
      },
    ]);
  try {
    const created = await postWithBackoff(
      adminPage,
      "/api/v1/users",
      `create ${label}`,
      {
        headers: await getAuthHeaders(adminPage),
        data: {
          username,
          password,
          k8sUsername: username,
          k8sGroups: [],
          roles: ["viewer"],
        },
      },
    );
    if (!created.ok()) {
      throw new Error(
        `creating ${label} failed: ${created.status()} ${await created.text()}`,
      );
    }
    accountId = (await created.json())?.data?.id as string | undefined;
    if (!accountId) {
      throw new Error(`created ${label}, but the response carried no id to delete it by`);
    }
    const page = await context.newPage();
    const login = await postWithBackoff(
      page,
      "/api/v1/auth/login",
      `log in as ${label}`,
      {
        headers: {
          "Content-Type": "application/json",
          "X-Requested-With": "XMLHttpRequest",
        },
        data: { username, password },
      },
    );
    if (!login.ok()) {
      throw new Error(
        `${label} login failed: ${login.status()} ${await login.text()}`,
      );
    }
    const token = (await login.json()).data.accessToken as string;
    // The namespace selects /auth/me's one-namespace fast path; only the id
    // is used, and `default` always exists.
    const me = await page.request.get("/api/v1/auth/me?namespace=default", {
      headers: bearerHeaders(token),
      failOnStatusCode: false,
    });
    const id = (await me.json().catch(() => null))?.data?.user?.id as
      | string
      | undefined;
    if (!me.ok() || !id) {
      throw new Error(`/auth/me returned no user id for ${label}: ${me.status()}`);
    }
    await attachAuthInjection(page, token);
    return { id, username, token, context, page, remove };
  } catch (err) {
    try {
      await remove();
    } catch (cleanupErr) {
      console.error(`removing ${label} also failed:`, cleanupErr);
    }
    throw err;
  }
}

/**
 * The credentials fixtures/auth.setup.ts provisions. Kept beside the helpers
 * that need them so a spec never has to restate them.
 */
export const E2E_USERNAME = "admin";
export const E2E_PASSWORD = "admin123";

/**
 * Sign in through the real login form, in a context that has NOT had the
 * fixture's token injection applied.
 *
 * Websocket specs need this; ordinary specs do not. The access token lives in
 * a module-level closure in frontend/lib/api.ts, and frontend/lib/ws.ts reads
 * that same closure to authenticate the socket. The fixture's injection puts
 * an Authorization header on outgoing fetches but never populates that
 * closure, so the socket opens, finds no token, closes, and backs off -- live
 * updates then appear to be broken when only the harness is.
 *
 * The obvious alternative, dropping the injection globally so the app
 * refreshes its own token, does not work: refresh tokens rotate
 * (auth/session.go Rotate), storageState carries exactly one refresh cookie,
 * and the first spec to spend it logs out every spec that follows. A real
 * login mints a fresh session instead of consuming the shared one, so it
 * costs nothing the other specs depend on.
 *
 * Use it with a context of your own:
 *
 *     const context = await browser.newContext();
 *     const page = await context.newPage();
 *     await loginThroughUI(page);
 */
export async function loginThroughUI(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Username").fill(E2E_USERNAME);
  await page.getByLabel("Password").fill(E2E_PASSWORD);
  await page.getByRole("button", { name: /sign in/i }).click();
  // Landing on the dashboard is what proves the token is in memory.
  await page.waitForURL((url) => url.pathname === "/");

  // Mint a second token and stash it under the key getAuthHeaders reads, so
  // the API helpers in this file work in this context as they do everywhere
  // else. This does NOT reinstate the fixture's injection -- that is an init
  // script and nothing here installs one -- so the app keeps authenticating
  // its socket with the in-memory token the login above minted, which is the
  // entire reason for logging in.
  const token = await page.evaluate(async (creds) => {
    const res = await fetch("/api/v1/auth/login", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "XMLHttpRequest",
      },
      body: JSON.stringify(creds),
    });
    return (await res.json()).data.accessToken as string;
  }, { username: E2E_USERNAME, password: E2E_PASSWORD });
  await page.evaluate(
    (t: string) => localStorage.setItem("e2e_access_token", t),
    token,
  );
}

/**
 * Start watching for the resource socket's subscribe acknowledgement.
 *
 * Call BEFORE navigating, then await the returned check before publishing the
 * event under test. The page's socket connects a beat after hydration, and an
 * event published before the subscription exists is delivered to nobody -- a
 * spec that skips this is asserting against an event the server never sent to
 * it, which is the actual reason these tests were called "timing-sensitive".
 *
 *     const subscribed = watchForSubscription(page, "configmaps");
 *     await page.goto(...);
 *     await expect.poll(subscribed, { timeout: 20_000 }).toBe(true);
 */
export function watchForSubscription(page: Page, kind: string): () => boolean {
  let seen = false;
  page.on("websocket", (ws) => {
    if (!ws.url().includes("/ws/")) return;
    ws.on("framereceived", (frame) => {
      const payload = String(frame.payload);
      if (payload.includes('"subscribed"') && payload.includes(kind)) {
        seen = true;
      }
    });
  });
  return () => seen;
}

/**
 * What `document.activeElement` is, named the way the dashboard grid names
 * things: its instance id, else its test id, else its tag, else `"none"`.
 *
 * Shared rather than copied into each spec because the fallback chain is the
 * assertion. A spec that read only `data-instance-id` would report `"none"`
 * for the remove button and the toolbar alike, and a keyboard order that
 * skipped one of them would still look right.
 */
export function focusedElementName(page: Page): Promise<string> {
  return page.evaluate(() => {
    const el = document.activeElement;
    return (
      el?.getAttribute("data-instance-id") ??
      el?.getAttribute("data-testid") ??
      el?.tagName ??
      "none"
    );
  });
}
