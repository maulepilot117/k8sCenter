import type { Page, Route } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import type { EvidenceKind } from "../../frontend/lib/eso-types.ts";

// Release B evidence tabs on the five ESO detail pages (U17 and U18), and the
// force-sync refresh observer on the ExternalSecret page (U19b).
//
// The kind cluster CI runs against has no External Secrets Operator, so the
// ESO endpoints are answered here at the network boundary with the wire
// shapes the backend serves. What these specs pin is the page contract: which
// tabs exist, what each response class renders as, and that controller text
// stays text. The server-side decisions behind those responses -- the
// projection level a caller gets, UID scoping, the store having no collector
// -- are covered by the backend tests for U14a and U15.

const NS = "e2e-evidence";
const ES = "app-creds";
const STORE = "vault-backend";
const ES_UID = "11111111-1111-4111-8111-111111111111";
const STORE_UID = "22222222-2222-4222-8222-222222222222";
const CLUSTER_STORE = "vault-global";
const CES = "shared-creds";
const PUSH = "push-creds";
const CLUSTER_STORE_UID = "33333333-3333-4333-8333-333333333333";
const CES_UID = "44444444-4444-4444-8444-444444444444";
const PUSH_UID = "55555555-5555-4555-8555-555555555555";

const ES_PATH = `/external-secrets/external-secrets/${NS}/${ES}`;
const STORE_PATH = `/external-secrets/stores/${NS}/${STORE}`;
const CLUSTER_STORE_PATH = `/external-secrets/cluster-stores/${CLUSTER_STORE}`;
const CES_PATH = `/external-secrets/cluster-external-secrets/${CES}`;
const PUSH_PATH = `/external-secrets/push-secrets/${NS}/${PUSH}`;

// The UID each kind's events response names. The panel discards a response
// for any other UID, so the stub must answer for the object the page shows.
const EVIDENCE_UIDS: Record<EvidenceKind, string> = {
  externalsecrets: ES_UID,
  secretstores: STORE_UID,
  clustersecretstores: CLUSTER_STORE_UID,
  clusterexternalsecrets: CES_UID,
  pushsecrets: PUSH_UID,
};

// Controller text that would run if the page rendered it as HTML.
const HOSTILE_MESSAGE =
  `<script>window.__esoEvidenceXss = 1</script><img src=x onerror="window.__esoEvidenceXss = 2">`;

const FULL = { level: "full", droppedFields: [] };
const RESTRICTED = {
  level: "outcome-only",
  droppedFields: ["message", "diffKeysAdded", "diffKeysRemoved", "diffKeysChanged"],
};

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

interface Evidence {
  history?: (route: Route) => Promise<void>;
  /** Answers one kind's events request; return undefined for the default. */
  events?: (route: Route, kind: EvidenceKind) => Promise<void> | undefined;
}

/**
 * Serves one object of each ESO kind plus their evidence. Returns
 * the evidence request paths seen, so a spec can assert what a page asked for.
 */
async function serveESO(page: Page, evidence: Evidence = {}) {
  const requested: string[] = [];

  // Anything not answered below reads as ESO absent rather than reaching a
  // backend that has no ESO to ask.
  await page.route("**/api/v1/externalsecrets/**", (route) => {
    requested.push(new URL(route.request().url()).pathname);
    return json(route, 503, {
      error: { code: 503, message: "ESO not detected", reason: "eso_not_detected" },
    });
  });

  await page.route(`**/api/v1/externalsecrets/externalsecrets/${NS}/${ES}`, (route) =>
    json(route, 200, {
      data: {
        namespace: NS,
        name: ES,
        uid: ES_UID,
        status: "Synced",
        storeRef: { kind: "SecretStore", name: STORE },
        targetSecretName: ES,
        refreshInterval: "1h",
      },
    }));

  await page.route(`**/api/v1/externalsecrets/stores/${NS}/${STORE}`, (route) =>
    json(route, 200, {
      data: {
        namespace: NS,
        name: STORE,
        uid: STORE_UID,
        scope: "Namespaced",
        status: "Synced",
        ready: true,
        provider: "vault",
      },
    }));

  await page.route(`**/api/v1/externalsecrets/clusterstores/${CLUSTER_STORE}`, (route) =>
    json(route, 200, {
      data: {
        name: CLUSTER_STORE,
        uid: CLUSTER_STORE_UID,
        scope: "Cluster",
        status: "Synced",
        ready: true,
        provider: "vault",
      },
    }));

  await page.route(`**/api/v1/externalsecrets/clusterexternalsecrets/${CES}`, (route) =>
    json(route, 200, {
      data: {
        name: CES,
        uid: CES_UID,
        status: "Synced",
        storeRef: { kind: "ClusterSecretStore", name: CLUSTER_STORE },
        provisionedNamespaces: ["team-a"],
        failedNamespaces: ["team-b"],
      },
    }));

  await page.route(`**/api/v1/externalsecrets/pushsecrets/${NS}/${PUSH}`, (route) =>
    json(route, 200, {
      data: {
        namespace: NS,
        name: PUSH,
        uid: PUSH_UID,
        status: "Synced",
        sourceSecretName: ES,
        storeRefs: [{ kind: "SecretStore", name: STORE }],
      },
    }));

  await page.route(
    `**/api/v1/externalsecrets/externalsecrets/${NS}/${ES}/history**`,
    (route) => {
      requested.push(new URL(route.request().url()).pathname);
      if (evidence.history) return evidence.history(route);
      return json(route, 200, {
        data: {
          uid: ES_UID,
          clusterId: "local",
          projection: FULL,
          entries: [
            {
              id: 2,
              attemptAt: "2026-09-25T10:05:00Z",
              outcome: "failure",
              reason: "SecretSyncedError",
              diffKeyCounts: { added: 0, removed: 0, changed: 0 },
              message: HOSTILE_MESSAGE,
            },
            {
              id: 1,
              attemptAt: "2026-09-25T10:00:00Z",
              outcome: "success",
              reason: "SecretSynced",
              diffKeyCounts: { added: 1, removed: 0, changed: 0 },
              diffKeysAdded: ["password"],
            },
          ],
        },
      });
    },
  );

  await page.route("**/api/v1/externalsecrets/evidence/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    requested.push(path);
    // .../evidence/{kind}/{namespace|_}/{name}/events
    const kind = path.split("/evidence/")[1].split("/")[0] as EvidenceKind;
    const custom = evidence.events?.(route, kind);
    if (custom) return custom;
    return json(route, 200, {
      data: {
        uid: EVIDENCE_UIDS[kind],
        projection: FULL,
        truncated: false,
        events: [{ type: "Normal", reason: "Valid", count: 3, message: "store validated" }],
      },
    });
  });

  await page.route("**/api/v1/yaml/export/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    requested.push(path);
    const name = path.split("/").pop();
    return json(route, 200, {
      data: `apiVersion: external-secrets.io/v1\nmetadata:\n  name: ${name}\n`,
    });
  });

  return requested;
}

async function openTab(page: Page, name: string) {
  await page.getByRole("tab", { name, exact: true }).click();
}

test.describe("eso evidence — ES and SecretStore", () => {
  test("ES detail renders YAML, Events and History tabs", async ({ page }) => {
    await serveESO(page);
    await page.goto(ES_PATH);
    await expect(page.getByRole("heading", { name: ES })).toBeVisible();

    const tabs = page.getByRole("tablist").first().getByRole("tab");
    await expect(tabs).toHaveText(["Overview", "YAML", "Events", "History", "Chain"]);

    await openTab(page, "YAML");
    await expect(page.getByText("apiVersion: external-secrets.io/v1")).toBeVisible();

    await openTab(page, "Events");
    await expect(page.getByRole("cell", { name: "store validated" })).toBeVisible();

    await openTab(page, "History");
    await expect(page.getByText("SecretSynced", { exact: true })).toBeVisible();

    await expect(page.getByText(/coming in Phase/)).toHaveCount(0);
  });

  test("ES History renders real rows or an explicit unavailable reason", async ({ page }) => {
    await serveESO(page, {
      history: (route) =>
        json(route, 503, {
          error: {
            code: 503,
            message: "history unavailable",
            reason: "history_unavailable",
          },
        }),
    });
    await page.goto(ES_PATH);
    await openTab(page, "History");

    // A failure says why; it is never a blank panel or an empty-history claim.
    const reason = page.locator('[data-evidence-state="history_unavailable"]');
    await expect(reason).toBeVisible();
    await expect(reason).not.toBeEmpty();
    await expect(page.locator('[data-evidence-state="empty"]')).toHaveCount(0);
    await expect(reason.getByRole("button", { name: "Try again" })).toBeVisible();
  });

  test("ES-only reader sees the restricted history projection", async ({ page }) => {
    // The body a caller without Secret read receives: outcome-only, with the
    // message and diff key names dropped by the server.
    await serveESO(page, {
      history: (route) =>
        json(route, 200, {
          data: {
            uid: ES_UID,
            clusterId: "local",
            projection: RESTRICTED,
            entries: [
              {
                id: 1,
                attemptAt: "2026-09-25T10:00:00Z",
                outcome: "success",
                reason: "SecretSynced",
                diffKeyCounts: { added: 1, removed: 0, changed: 0 },
                // Sent anyway, so the assertions below also catch the page
                // rendering fields the projection says are withheld.
                message: "RESTRICTED_MESSAGE",
                diffKeysAdded: ["password"],
              },
            ],
          },
        }),
    });
    await page.goto(ES_PATH);
    await openTab(page, "History");

    await expect(page.locator('[data-evidence-state="redacted"]')).toContainText(
      "requires Secret read",
    );
    await expect(page.getByText("SecretSynced", { exact: true })).toBeVisible();
    // No diff-key chip: the key names are what the projection withholds.
    await expect(page.getByText("Added", { exact: true })).toHaveCount(0);
    await expect(page.locator("li code")).toHaveCount(0);
    await expect(page.getByText("RESTRICTED_MESSAGE")).toHaveCount(0);
  });

  test("SecretStore detail has no History tab", async ({ page }) => {
    await serveESO(page);
    await page.goto(STORE_PATH);
    await expect(page.getByRole("heading", { name: STORE })).toBeVisible();

    const tabs = page.getByRole("tablist").first().getByRole("tab");
    await expect(tabs).toHaveText(["Overview", "YAML", "Events", "Chain"]);
    await expect(page.getByRole("tab", { name: "History" })).toHaveCount(0);
    await expect(
      page.getByText("Reconciliation history is collected per ExternalSecret."),
    ).toBeVisible();
  });

  test("SecretStore never labels ES attempts as its own sync history", async ({ page }) => {
    const requested = await serveESO(page);
    await page.goto(STORE_PATH);
    await expect(page.getByRole("heading", { name: STORE })).toBeVisible();

    await openTab(page, "YAML");
    await expect(page.getByText(`name: ${STORE}`)).toBeVisible();
    await openTab(page, "Events");
    await expect(page.getByRole("cell", { name: "store validated" })).toBeVisible();

    // The store's evidence is the store's own: events and YAML addressed to
    // it, and no ExternalSecret history fetched or shown.
    expect(requested).toContain(
      `/api/v1/externalsecrets/evidence/secretstores/${NS}/${STORE}/events`,
    );
    expect(requested.some((p) => p.endsWith("/history"))).toBe(false);
    await expect(page.getByText(ES, { exact: true })).toHaveCount(0);
  });

  test("no ESO evidence tab contains \"coming in Phase\"", async ({ page }) => {
    // Every evidence tab on all five kinds. The Chain tabs on CES and
    // PushSecret stay placeholders by design and are pinned separately below.
    await serveESO(page);
    for (const [path, tabs] of [
      [ES_PATH, ["YAML", "Events", "History"]],
      [STORE_PATH, ["YAML", "Events"]],
      [CLUSTER_STORE_PATH, ["YAML", "Events"]],
      [CES_PATH, ["YAML", "Events", "Generated ExternalSecrets"]],
      [PUSH_PATH, ["YAML", "Events"]],
    ] as const) {
      await page.goto(path);
      await expect(page.getByRole("tab", { name: "Overview" })).toBeVisible();
      for (const tab of tabs) {
        await openTab(page, tab);
        await expect(page.locator("body")).not.toContainText("coming in Phase");
      }
    }
  });

  test("controller text renders as text", async ({ page }) => {
    await serveESO(page);
    await page.goto(ES_PATH);
    await openTab(page, "History");

    await expect(page.getByText(HOSTILE_MESSAGE)).toBeVisible();
    expect(
      await page.evaluate(
        () => (window as unknown as { __esoEvidenceXss?: number }).__esoEvidenceXss,
      ),
    ).toBeUndefined();
  });
});

test.describe("eso evidence — cluster-scoped and PushSecret", () => {
  test("ClusterSecretStore detail resolves cluster scope", async ({ page }) => {
    const requested = await serveESO(page);
    await page.goto(CLUSTER_STORE_PATH);
    await expect(page.getByRole("heading", { name: CLUSTER_STORE })).toBeVisible();

    await openTab(page, "YAML");
    await expect(page.getByText(`name: ${CLUSTER_STORE}`)).toBeVisible();
    await openTab(page, "Events");
    await expect(page.getByRole("cell", { name: "store validated" })).toBeVisible();

    // Cluster scope is addressed as "_", never as a namespace segment.
    expect(requested).toContain(
      `/api/v1/yaml/export/clustersecretstores/_/${CLUSTER_STORE}`,
    );
    expect(requested).toContain(
      `/api/v1/externalsecrets/evidence/clustersecretstores/_/${CLUSTER_STORE}/events`,
    );
  });

  test("ClusterSecretStore has no History tab", async ({ page }) => {
    const requested = await serveESO(page);
    await page.goto(CLUSTER_STORE_PATH);
    await expect(page.getByRole("heading", { name: CLUSTER_STORE })).toBeVisible();

    const tabs = page.getByRole("tablist").first().getByRole("tab");
    await expect(tabs).toHaveText(["Overview", "YAML", "Events", "Chain"]);
    await expect(page.getByRole("tab", { name: "History" })).toHaveCount(0);
    await expect(
      page.getByText("Reconciliation history is collected per ExternalSecret."),
    ).toBeVisible();
    expect(requested.some((p) => p.endsWith("/history"))).toBe(false);
  });

  test("CES History tab is replaced by Generated ExternalSecrets", async ({ page }) => {
    const requested = await serveESO(page);
    await page.goto(CES_PATH);
    await expect(page.getByRole("heading", { name: CES })).toBeVisible();

    const tabs = page.getByRole("tablist").first().getByRole("tab");
    await expect(tabs).toHaveText([
      "Overview",
      "YAML",
      "Events",
      "Generated ExternalSecrets",
      "Chain",
    ]);
    await expect(page.getByRole("tab", { name: "History" })).toHaveCount(0);

    // The CES's own evidence is addressed at cluster scope ("_").
    await openTab(page, "YAML");
    await expect(page.getByText(`name: ${CES}`)).toBeVisible();
    await openTab(page, "Events");
    await expect(page.getByRole("cell", { name: "store validated" })).toBeVisible();
    expect(requested).toContain(`/api/v1/yaml/export/clusterexternalsecrets/_/${CES}`);
    expect(requested).toContain(
      `/api/v1/externalsecrets/evidence/clusterexternalsecrets/_/${CES}/events`,
    );

    await openTab(page, "Generated ExternalSecrets");
    // Each child links to its own ExternalSecret page, where its history is;
    // a namespace the CES failed to provision is marked, not hidden.
    await expect(page.getByRole("link", { name: `team-a/${CES}` })).toHaveAttribute(
      "href",
      `/external-secrets/external-secrets/team-a/${CES}`,
    );
    const failed = page.getByRole("listitem").filter({ hasText: `team-b/${CES}` });
    await expect(failed.getByRole("link")).toHaveAttribute(
      "href",
      `/external-secrets/external-secrets/team-b/${CES}`,
    );
    await expect(failed.getByText("failed", { exact: true })).toBeVisible();
    const provisioned = page.getByRole("listitem").filter({ hasText: `team-a/${CES}` });
    await expect(provisioned.getByText("failed", { exact: true })).toHaveCount(0);
  });

  test("CES never presents child attempts as its own history", async ({ page }) => {
    const requested = await serveESO(page);
    await page.goto(CES_PATH);
    await openTab(page, "Generated ExternalSecrets");
    await expect(page.getByRole("link", { name: `team-a/${CES}` })).toBeVisible();

    await expect(page.getByRole("heading", { name: /sync history/i })).toHaveCount(0);
    // No child's history is fetched, so none can be rendered under the CES.
    expect(requested.some((p) => p.endsWith("/history"))).toBe(false);
  });

  test("PushSecret shows YAML and Events, no History, and remains read-only", async ({
    page,
  }) => {
    const requested = await serveESO(page);
    await page.goto(PUSH_PATH);
    await expect(page.getByRole("heading", { name: PUSH })).toBeVisible();

    const tabs = page.getByRole("tablist").first().getByRole("tab");
    await expect(tabs).toHaveText(["Overview", "YAML", "Events", "Chain"]);

    await openTab(page, "YAML");
    await expect(page.getByText(`name: ${PUSH}`)).toBeVisible();
    await openTab(page, "Events");
    await expect(page.getByRole("cell", { name: "store validated" })).toBeVisible();

    expect(requested).toContain(`/api/v1/yaml/export/pushsecrets/${NS}/${PUSH}`);
    expect(requested).toContain(
      `/api/v1/externalsecrets/evidence/pushsecrets/${NS}/${PUSH}/events`,
    );
    expect(requested.some((p) => p.endsWith("/history"))).toBe(false);
    // Read-only in v1: no sync, edit, delete or apply control in the page body.
    await expect(
      page.getByRole("main").getByRole("button", { name: /sync|edit|delete|apply/i }),
    ).toHaveCount(0);
  });

  test("namespaced-only identity is refused cluster-scoped events", async ({ page }) => {
    // What the server answers a caller without cluster-wide `list events`.
    const grant = "cluster-wide `list events`";
    await serveESO(page, {
      events: (route, kind) =>
        kind === "clustersecretstores" || kind === "clusterexternalsecrets"
          ? json(route, 403, {
              error: {
                code: 403,
                message: `access denied: this object's events require ${grant}`,
                reason: "events_forbidden",
                extra: { requiredGrant: grant },
              },
            })
          : undefined,
    });

    for (const path of [CLUSTER_STORE_PATH, CES_PATH]) {
      await page.goto(path);
      await openTab(page, "Events");
      // A visible refusal that names the grant, never an empty event list.
      const forbidden = page.locator('[data-evidence-state="forbidden"]');
      await expect(forbidden).toBeVisible();
      await expect(forbidden).toContainText(grant);
      await expect(page.locator('[data-evidence-state="empty"]')).toHaveCount(0);
      await expect(page.getByRole("cell", { name: "store validated" })).toHaveCount(0);
    }
  });

  test("Chain placeholders remain on CES and PushSecret", async ({ page }) => {
    // Out of Release B scope by design (plan: placeholder-tab inventory).
    // Pinned so their survival reads as deliberate, and so this spec changes
    // the day a chain view ships instead of passing silently.
    await serveESO(page);
    for (const path of [CES_PATH, PUSH_PATH]) {
      await page.goto(path);
      await openTab(page, "Chain");
      await expect(page.getByText(/Chain visualization coming in Phase/)).toBeVisible();
    }
  });
});

// Release B U19b: the page reports what it observes after a force-sync, not
// just that the request was accepted. The ES starts Ready=True, so a page
// that equated "Ready" with "done" would claim success on the first poll.
const LAST_SYNC = "2026-09-26T11:30:00Z";
const LATER_SYNC = "2026-09-26T12:00:05Z";

/**
 * Answers force-sync with a strong-correlation 202 and serves the ES with
 * `lastSyncTime(n)` on its n-th read (1-based, the page's own load included).
 * Returns a counter of ES reads.
 */
async function serveForceSync(page: Page, lastSyncTime: (read: number) => string) {
  const reads = { count: 0 };
  await page.route(`**/api/v1/externalsecrets/externalsecrets/${NS}/${ES}`, (route) => {
    reads.count += 1;
    return json(route, 200, {
      data: {
        namespace: NS,
        name: ES,
        uid: ES_UID,
        status: "Synced",
        storeRef: { kind: "SecretStore", name: STORE },
        targetSecretName: ES,
        refreshInterval: "1h",
        lastSyncTime: lastSyncTime(reads.count),
        syncedResourceVersion: "3-aaa",
      },
    });
  });
  await page.route(
    `**/api/v1/externalsecrets/externalsecrets/${NS}/${ES}/force-sync`,
    (route) =>
      json(route, 202, {
        data: {
          status: "force-syncing",
          correlation: "strong",
          baseline: {
            uid: ES_UID,
            resourceVersion: "100",
            refreshTime: LAST_SYNC,
            syncedResourceVersion: "3-aaa",
            requestedAt: new Date().toISOString(),
          },
        },
      }),
  );
  return reads;
}

const observerLine = (page: Page) => page.locator("[data-observer-phase]");

test.describe("eso evidence — refresh observation", () => {
  test("force-sync on a Ready ES shows an explicit awaiting state, never an immediate success", async ({
    page,
  }) => {
    await serveESO(page);
    const reads = await serveForceSync(page, () => LAST_SYNC);
    await page.goto(ES_PATH);
    await page.getByRole("button", { name: "Force sync" }).click();

    await expect(observerLine(page)).toHaveAttribute("data-observer-phase", "awaitingObservation");
    await expect(observerLine(page)).toContainText("Waiting");
    // Two polls of an unchanged, already-Ready ES must not read as success.
    await expect.poll(() => reads.count, { timeout: 10_000 }).toBeGreaterThanOrEqual(3);
    await expect(observerLine(page)).toHaveAttribute("data-observer-phase", "awaitingObservation");
    await expect(page.locator('[data-observer-phase="observedSuccess"]')).toHaveCount(0);
  });

  test("a later refreshTime is reported as a reconcile after the request", async ({ page }) => {
    await serveESO(page);
    // Read 1 is the page load; every poll after the request sees a new sync.
    await serveForceSync(page, (read) => (read === 1 ? LAST_SYNC : LATER_SYNC));
    await page.goto(ES_PATH);
    await page.getByRole("button", { name: "Force sync" }).click();

    const line = page.locator('[data-observer-phase="observedSuccess"]');
    await expect(line).toBeVisible({ timeout: 10_000 });
    await expect(line).toContainText("after your request");
  });

  test("navigating away mid-wait leaves no orphaned banner", async ({ page }) => {
    await serveESO(page);
    const reads = await serveForceSync(page, () => LAST_SYNC);
    await page.goto(ES_PATH);
    await page.getByRole("button", { name: "Force sync" }).click();
    await expect(observerLine(page)).toHaveAttribute("data-observer-phase", "awaitingObservation");

    await page.getByRole("link", { name: `SecretStore/${STORE}` }).click();
    await expect(page.getByRole("heading", { name: STORE })).toBeVisible();
    await expect(observerLine(page)).toHaveCount(0);
    // Nothing keeps polling the ES from the page that was left.
    const after = reads.count;
    await page.waitForTimeout(3_000);
    expect(reads.count).toBe(after);
  });

  test("switching clusters mid-wait discards the observation", async ({ page }) => {
    await serveESO(page);
    await serveForceSync(page, () => LAST_SYNC);
    await page.route("**/api/v1/clusters", (route) =>
      json(route, 200, {
        data: [
          { id: "local", name: "local", isLocal: true, status: "connected" },
          {
            id: "e2e-remote",
            name: "e2e-remote",
            status: "connected",
            createdAt: "2026-09-01T00:00:00Z",
          },
        ],
      }));

    await page.goto(ES_PATH);
    await page.getByRole("button", { name: "Force sync" }).click();
    await expect(observerLine(page)).toHaveAttribute("data-observer-phase", "awaitingObservation");

    await page.getByRole("button", { name: /Change cluster/ }).click();
    await page.getByRole("option", { name: /e2e-remote/ }).click();
    await expect(page.getByRole("heading", { name: ES })).toBeVisible();
    // The pending observation belonged to the previous cluster.
    await expect(observerLine(page)).toHaveCount(0);

    await page.evaluate(() => {
      localStorage.setItem(
        "k8scenter.clusterTarget",
        JSON.stringify({ clusterId: "local", generation: "local" }),
      );
    });
  });
});
