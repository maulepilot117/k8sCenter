import type { Browser, Locator, Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
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
 * Persistent incident investigations, end to end (Release D U24c, AE6).
 *
 * Runs against the real backend, the CI kind cluster and PostgreSQL 17. The
 * second identity follows the pattern change-receipts.spec.ts ships: the
 * admin creates a local viewer through POST /api/v1/users, logs it in through
 * the shared auth bucket with backoff, drives its API with bearerHeaders and
 * its UI with attachAuthInjection in a fresh context, and deletes it in
 * `finally`. No extra Playwright project or setup file is needed.
 *
 * What the kind cluster can and cannot prove:
 *
 *   - The admin is bound to cluster-admin (e2e/fixtures/k8s); the viewer has
 *     no RoleBinding at all. So the collaborator branch here shows the grant
 *     conveying standing to ask and NO Kubernetes authority (Q1 P2): the
 *     collaborator sees the record and the notes, and every evidence item as
 *     a withheld placeholder.
 *   - The viewer's Kubernetes permissions never change during the run, so
 *     the revoked-Kubernetes-permission branch (Q1 P7) is not exercised here.
 *     "Revocation" below is the removal of the incident grant. P7 is proven by
 *     the Go tests in backend/internal/incidents/handler_test.go
 *     (TestSourcePermissionChangeWithholdsItemWithoutMetadataLeak) and
 *     handler_actions_test.go (TestGrantDoesNotConveyKubernetesAuthority).
 *   - Retention and the no-database 503 are covered by Go tests: this suite
 *     always has PostgreSQL.
 */

const NS = "e2e-test";
const INCIDENTS = "/observability/incidents";
const LAST_APPLIED = "kubectl.kubernetes.io/last-applied-configuration";
const NOT_SHARED =
  "Incident not found. It may not exist, or it may not be shared with you.";

/**
 * A Pod that carries a last-applied-configuration annotation holding a
 * stringData canary, the way `kubectl apply` of a Secret-bearing manifest
 * leaves one behind. Capture must strip the annotation, so the canary is how
 * the export check proves something was actually removed rather than never
 * present.
 */
function canaryPod(name: string, canary: string): string {
  const lastApplied = JSON.stringify({
    apiVersion: "v1",
    kind: "Secret",
    stringData: { token: canary },
  });
  return [
    "apiVersion: v1",
    "kind: Pod",
    "metadata:",
    `  name: ${name}`,
    `  namespace: ${NS}`,
    "  labels:",
    '    e2e: "true"',
    "  annotations:",
    `    ${LAST_APPLIED}: '${lastApplied}'`,
    "spec:",
    // Deletion should not wait out the default 30s grace period.
    "  terminationGracePeriodSeconds: 0",
    "  containers:",
    "    - name: pause",
    "      image: registry.k8s.io/pause:3.10",
    "      imagePullPolicy: IfNotPresent",
  ].join("\n");
}

const podPath = (name: string) => `/api/v1/resources/pods/${NS}/${name}`;

async function applyYaml(page: Page, yaml: string): Promise<void> {
  const headers = await getAuthHeaders(page);
  const res = await page.request.post("/api/v1/yaml/apply", {
    headers: { ...headers, "Content-Type": "text/yaml" },
    data: yaml,
    failOnStatusCode: false,
  });
  expect(res.ok(), `yaml apply: ${res.status()} ${await res.text()}`).toBe(
    true,
  );
  expect((await res.json()).data.summary.failed).toBe(0);
}

/** The status the resource API answers for one pod (informer-backed). */
async function podStatus(page: Page, name: string): Promise<number> {
  const res = await page.request.get(podPath(name), {
    headers: await getAuthHeaders(page),
    failOnStatusCode: false,
  });
  return res.status();
}

async function deletePod(page: Page, name: string): Promise<void> {
  const res = await page.request.delete(podPath(name), {
    headers: await getAuthHeaders(page),
    failOnStatusCode: false,
  });
  if (!res.ok() && res.status() !== 404) {
    throw new Error(`could not delete pod ${name}: ${res.status()}`);
  }
}

/** Creates an incident through the API and returns its id. */
async function createIncidentViaApi(page: Page, title: string): Promise<string> {
  const res = await page.request.post("/api/v1/incidents", {
    headers: await getAuthHeaders(page),
    data: {
      title,
      summary: "",
      windowStart: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    },
    failOnStatusCode: false,
  });
  if (!res.ok()) {
    throw new Error(`create incident: ${res.status()} ${await res.text()}`);
  }
  return (await res.json()).data.incident.id as string;
}

async function deleteIncident(page: Page, id: string): Promise<void> {
  const res = await page.request.delete(`/api/v1/incidents/${id}`, {
    headers: await getAuthHeaders(page),
    failOnStatusCode: false,
  });
  if (!res.ok() && res.status() !== 404) {
    throw new Error(`could not delete incident ${id}: ${res.status()}`);
  }
}

/** A local viewer user and its access token; `remove` deletes the account. */
interface SecondUser {
  id: string;
  token: string;
  remove: () => Promise<void>;
}

/**
 * Creates a viewer with no Kubernetes RBAC and logs it in. Both calls spend
 * the shared 5-per-minute auth bucket, so they go through postWithBackoff;
 * the id comes from /auth/me, which is the identity grants are keyed on.
 */
async function createSecondUser(page: Page): Promise<SecondUser> {
  const headers = await getAuthHeaders(page);
  const username = e2eSecureName("collab");
  const password = `e2e-${crypto.randomUUID()}`;
  const created = await postWithBackoff(page, "/api/v1/users", "create the collaborator", {
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
      `creating the collaborator failed: ${created.status()} ${await created.text()}`,
    );
  }
  const accountId = (await created.json())?.data?.id as string | undefined;
  const remove = async () => {
    if (!accountId) return;
    await page.request.delete(`/api/v1/users/${accountId}`, {
      headers,
      failOnStatusCode: false,
    });
  };
  try {
    const login = await postWithBackoff(page, "/api/v1/auth/login", "log in as the collaborator", {
      headers: {
        "Content-Type": "application/json",
        "X-Requested-With": "XMLHttpRequest",
      },
      data: { username, password },
    });
    if (!login.ok()) {
      throw new Error(`collaborator login failed: ${login.status()} ${await login.text()}`);
    }
    const token = (await login.json()).data.accessToken as string;
    const me = await page.request.get(`/api/v1/auth/me?namespace=${NS}`, {
      headers: bearerHeaders(token),
    });
    const id = (await me.json())?.data?.user?.id as string | undefined;
    if (!id) throw new Error("/auth/me returned no user id for the collaborator");
    return { id, token, remove };
  } catch (err) {
    await remove();
    throw err;
  }
}

/**
 * A page in a context of its own, authenticated as `token` and nobody else:
 * the empty storage state keeps the admin's refresh cookie and stored token
 * out of it, so nothing can silently refresh it into the admin.
 */
async function pageAs(browser: Browser, token: string): Promise<Page> {
  const context = await browser.newContext({
    storageState: { cookies: [], origins: [] },
  });
  const page = await context.newPage();
  await attachAuthInjection(page, token);
  return page;
}

const captureRegion = (page: Page) =>
  page.getByRole("region", { name: "Capture evidence" });
const timeline = (page: Page) =>
  page.getByRole("list", { name: "Evidence timeline" });
const notesRegion = (page: Page) => page.getByRole("region", { name: "Notes" });
const sharingRegion = (page: Page) =>
  page.getByRole("region", { name: "Sharing" });
const countsLine = (page: Page) => page.locator('[data-counts="true"]');

/** Opens an incident and waits for its header. */
async function openIncident(page: Page, id: string, title: string) {
  await page.goto(`${INCIDENTS}/${id}`);
  await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible({
    timeout: 15_000,
  });
}

/** The caller's whole-incident counts, as the workspace header shows them. */
async function readCounts(page: Page): Promise<{ visible: number; withheld: number }> {
  const line = countsLine(page);
  await expect(line).toHaveText(/^\d+ visible to you, \d+ withheld$/);
  const m = /^(\d+) visible to you, (\d+) withheld$/.exec(
    (await line.innerText()).trim(),
  );
  if (!m) throw new Error("unreadable evidence counts");
  return { visible: Number(m[1]), withheld: Number(m[2]) };
}

/** Fills and submits the capture form, then waits for its result. */
async function capture(
  page: Page,
  kind: string,
  name: string,
): Promise<Locator> {
  const region = captureRegion(page);
  await region.getByLabel("Namespace").fill(NS);
  await region.getByLabel("Kind").selectOption(kind);
  await region.getByLabel("Name", { exact: true }).fill(name);
  await region.getByRole("button", { name: "Capture", exact: true }).click();
  const result = region.locator('[data-capture-result="true"]');
  // Racing the panel's error line, so a refused capture fails with its own
  // message instead of a bare timeout. The collector's request budget is 27s.
  const failure = region.getByText(
    /Nothing was recorded|may or may not have been recorded|capture target is invalid|Only the incident owner|is busy|not available on this deployment/,
  );
  await expect(result.or(failure).first()).toBeVisible({ timeout: 35_000 });
  if (await failure.first().isVisible()) {
    throw new Error(`capture refused: ${await failure.first().innerText()}`);
  }
  return result;
}

/** The capture result's row for one source. */
const sourceRow = (result: Locator, label: string) =>
  result.getByRole("listitem").filter({ hasText: label });

test.describe("Incidents (AE6)", () => {
  test("capture, source deletion, cross-user 404, handoff, revocation and filtered export", async ({
    page,
    browser,
  }) => {
    // Two waits on the shared auth bucket (create user, log in) can each
    // back off for up to ~130s before failing loudly.
    test.setTimeout(360_000);
    const pod = e2eName("pod");
    const canary = `e2e-canary-${crypto.randomUUID()}`;
    const title = `AE6 ${e2eName("incident")}`;
    const note = `Handoff note ${crypto.randomUUID()}`;
    let incidentId: string | undefined;
    let collaborator: SecondUser | undefined;
    let collabPage: Page | undefined;

    try {
      await page.goto(INCIDENTS);
      await applyYaml(page, canaryPod(pod, canary));
      // Capture's diagnostics and the live link both read through the
      // informer cache, so wait until it has the pod.
      await expect.poll(() => podStatus(page, pod), { timeout: 30_000 }).toBe(200);
      const live = await page.request.get(podPath(pod), {
        headers: await getAuthHeaders(page),
      });
      // The canary is really on the object, so its absence later is redaction.
      expect(
        (await live.json()).data?.metadata?.annotations?.[LAST_APPLIED],
      ).toContain(canary);

      await test.step("capture: create the incident and capture diagnostics, object and events", async () => {
        await expect(
          page.getByRole("heading", { name: "Incidents", level: 1 }),
        ).toBeVisible();
        await page.getByRole("button", { name: "New incident" }).click();
        await page.getByLabel("Title", { exact: true }).fill(title);
        await page.getByRole("button", { name: "Create incident" }).click();
        await expect(page).toHaveURL(/\/observability\/incidents\/[0-9a-f-]{36}$/);
        incidentId = new URL(page.url()).pathname.split("/").pop();
        await expect(
          page.getByRole("heading", { name: title, level: 1 }),
        ).toBeVisible();
        await expect(page.getByText("No evidence yet.")).toBeVisible();

        const result = await capture(page, "Pod", pod);
        await expect(result).toContainText(/^Capture (complete|partial): \d+ new items? recorded/);
        for (const source of [
          "Diagnostic checks",
          "Object snapshot and live link",
          "Events",
        ]) {
          await expect(sourceRow(result, source)).toHaveCount(1);
        }
        // The object source read the pod: a snapshot plus a live link.
        await expect(
          sourceRow(result, "Object snapshot and live link").getByText("Complete", {
            exact: true,
          }),
        ).toBeVisible();

        const rows = timeline(page).getByRole("listitem");
        await expect(rows.filter({ hasText: "Diagnostic check" }).first()).toBeVisible();
        await expect(rows.filter({ hasText: "Events" }).first()).toBeVisible();
        await expect(rows.filter({ hasText: "Snapshot" }).filter({ hasText: "Object summary" })).toHaveCount(1);
        await expect(page.getByText("The object still exists.")).toBeVisible({
          timeout: 15_000,
        });
        // Capture stripped the annotation: the snapshot says so and the
        // canary is nowhere on the page.
        await expect(page.locator("body")).not.toContainText(canary);
      });

      const before = await readCounts(page);
      // The owner is cluster-admin: everything captured is readable.
      expect(before.visible).toBeGreaterThan(0);
      expect(before.withheld).toBe(0);

      await test.step("deletion: stored evidence outlives the pod; the live link says so", async () => {
        await deletePod(page, pod);
        await expect.poll(() => podStatus(page, pod), { timeout: 60_000 }).toBe(404);
        await page.reload();
        await expect(page.getByRole("heading", { name: title, level: 1 })).toBeVisible();
        expect(await readCounts(page)).toEqual(before);
        await expect(
          page.getByText("No longer present: the object was deleted after capture."),
        ).toBeVisible({ timeout: 15_000 });
        await expect(page.getByText("The object still exists.")).toHaveCount(0);
        // The retained snapshot is still readable, payload and all.
        const snapshot = timeline(page)
          .getByRole("listitem")
          .filter({ hasText: "Snapshot" })
          .filter({ hasText: "Object summary" });
        await snapshot.getByText(/^Captured data \(\d+ bytes\)$/).click();
        await expect(snapshot.locator("pre")).toContainText(pod);
        await expect(snapshot.locator("pre")).not.toContainText(canary);
      });

      await test.step("note: the owner leaves a note for the collaborator", async () => {
        const notes = notesRegion(page);
        await notes.getByLabel("Add a note").fill(note);
        await notes.getByRole("button", { name: "Add note" }).click();
        await expect(notes.getByRole("list", { name: "Notes" })).toContainText(note);
      });

      collaborator = await createSecondUser(page);
      const collab = collaborator;
      const incidentApi = `/api/v1/incidents/${incidentId}`;
      const collabGet = async () =>
        (
          await page.request.get(incidentApi, {
            headers: bearerHeaders(collab.token),
            failOnStatusCode: false,
          })
        ).status();
      collabPage = await pageAs(browser, collab.token);
      const other = collabPage;

      await test.step("cross-user: before any grant the collaborator gets 404, not 403", async () => {
        expect(await collabGet()).toBe(404);
        await other.goto(`${INCIDENTS}/${incidentId}`);
        await expect(other.getByText(NOT_SHARED)).toBeVisible({ timeout: 15_000 });
        await expect(other.locator("body")).not.toContainText(title);
      });

      await test.step("handoff: the owner shares; the collaborator sees the record and notes", async () => {
        const sharing = sharingRegion(page);
        await sharing.getByLabel("User id").fill(collab.id);
        await sharing.getByRole("button", { name: "Share", exact: true }).click();
        await expect(sharing.getByText(`Shared with ${collab.id}.`)).toBeVisible();
        await expect(sharing.getByRole("table")).toContainText(collab.id);

        expect(await collabGet()).toBe(200);
        await openIncident(other, incidentId as string, title);
        await expect(other.getByText("Shared with you (read-only)")).toBeVisible();
        await expect(
          notesRegion(other).getByRole("list", { name: "Notes" }),
        ).toContainText(note);
        // The grant is standing to ask, not Kubernetes authority (Q1 P2):
        // this viewer has no RBAC in kind, so every item is withheld, and a
        // withheld row says nothing about what it is about.
        expect(await readCounts(other)).toEqual({
          visible: 0,
          withheld: before.visible,
        });
        const withheld = timeline(other).locator('[data-withheld="true"]');
        await expect(withheld).toHaveCount(before.visible);
        await expect(withheld.first()).toContainText(
          "You do not currently have access to this evidence's scope.",
        );
        await expect(other.locator("body")).not.toContainText(pod);
        // Owner-only controls are not offered to a collaborator.
        await expect(captureRegion(other)).toHaveCount(0);
        await expect(sharingRegion(other)).toHaveCount(0);
      });

      await test.step("revocation: the owner removes the grant; the next load is 404", async () => {
        const sharing = sharingRegion(page);
        await sharing
          .getByRole("row")
          .filter({ hasText: collab.id })
          .getByRole("button", { name: "Remove" })
          .click();
        await expect(sharing.getByText(`Stopped sharing with ${collab.id}.`)).toBeVisible();
        await expect(sharing.getByText("Not shared with anyone.")).toBeVisible();

        expect(await collabGet()).toBe(404);
        await other.reload();
        await expect(other.getByText(NOT_SHARED)).toBeVisible({ timeout: 15_000 });
        await expect(other.locator("body")).not.toContainText(note);
      });

      await test.step("filtered export: the JSON download carries no secret material", async () => {
        const download = page.waitForEvent("download");
        await page
          .getByRole("group", { name: "Export" })
          .getByRole("button", { name: "JSON", exact: true })
          .click();
        const file = await (await download).path();
        const text = await readFile(file, "utf8");
        expect(text).not.toContain("stringData");
        expect(text).not.toContain(LAST_APPLIED);
        expect(text).not.toContain(canary);

        const doc = JSON.parse(text);
        expect(doc.incident.id).toBe(incidentId);
        expect(doc.truncated).toBe(false);
        // The export is the same filtered view the page shows.
        const shown = await readCounts(page);
        expect(doc.counts).toEqual(shown);
        expect(doc.evidence).toHaveLength(shown.visible);
        expect(doc.withheld).toHaveLength(shown.withheld);
        expect(doc.notes.map((n: { body: string }) => n.body)).toContain(note);
        // ...and the annotation was there to remove: capture recorded it.
        const objectSnapshot = doc.evidence.find(
          (e: { evidenceKind: string; mode: string }) =>
            e.evidenceKind === "object_summary" && e.mode === "snapshot",
        );
        expect(objectSnapshot?.redaction?.rules).toContain("last-applied-config");
      });
    } finally {
      await collabPage?.context().close();
      await collaborator?.remove();
      if (incidentId) await deleteIncident(page, incidentId);
      await deletePod(page, pod);
    }
  });

  test("a failing source is reported per source, not as a blanket error", async ({
    page,
  }) => {
    const title = `Partial ${e2eName("incident")}`;
    // No such Service: the object and diagnostics sources cannot find it,
    // while the events source still runs (and finds nothing).
    const missing = e2eName("svc");
    await page.goto(INCIDENTS);
    const id = await createIncidentViaApi(page, title);
    try {
      await openIncident(page, id, title);
      const result = await capture(page, "Service", missing);
      await expect(result).toContainText(/^Capture partial:/);

      const object = sourceRow(result, "Object snapshot and live link");
      await expect(object.getByText("Failed", { exact: true })).toBeVisible();
      await expect(object).toContainText("target not found");
      const diagnostics = sourceRow(result, "Diagnostic checks");
      await expect(diagnostics.getByText("Failed", { exact: true })).toBeVisible();
      await expect(
        sourceRow(result, "Events").getByText("Complete", { exact: true }),
      ).toBeVisible();

      // What did succeed was stored and is shown, bound to a name only.
      const rows = timeline(page).getByRole("listitem");
      await expect(rows.filter({ hasText: "Events" })).toHaveCount(1);
      await expect(
        rows.filter({ hasText: "Identified by name only" }),
      ).toHaveCount(1);
      await expect(page.getByText("No evidence yet.")).toHaveCount(0);
    } finally {
      await deleteIncident(page, id);
    }
  });

  test("a stale note edit shows the conflict banner and keeps the draft", async ({
    page,
  }) => {
    const title = `Conflict ${e2eName("incident")}`;
    const original = `Original ${crypto.randomUUID()}`;
    const first = `First save ${crypto.randomUUID()}`;
    const draft = `Second tab draft ${crypto.randomUUID()}`;
    await page.goto(INCIDENTS);
    const id = await createIncidentViaApi(page, title);
    const res = await page.request.post(`/api/v1/incidents/${id}/notes`, {
      headers: await getAuthHeaders(page),
      data: { body: original },
    });
    expect(res.ok(), `create note: ${res.status()}`).toBe(true);

    // A second tab of the same user. A page opened on the context does not
    // get the base fixture's token injection, so it is applied here.
    const second = await page.context().newPage();
    await attachAuthInjection(second);
    try {
      for (const tab of [page, second]) {
        await openIncident(tab, id, title);
        const notes = notesRegion(tab);
        await expect(notes.getByRole("list", { name: "Notes" })).toContainText(
          original,
        );
        await notes.getByRole("button", { name: "Edit", exact: true }).click();
        await expect(notes.getByLabel("Edit note")).toHaveValue(original);
      }

      const firstNotes = notesRegion(page);
      await firstNotes.getByLabel("Edit note").fill(first);
      await firstNotes.getByRole("button", { name: "Save", exact: true }).click();
      await expect(firstNotes.getByRole("list", { name: "Notes" })).toContainText(
        first,
      );
      await expect(firstNotes.getByLabel("Edit note")).toHaveCount(0);

      const secondNotes = notesRegion(second);
      await secondNotes.getByLabel("Edit note").fill(draft);
      await secondNotes.getByRole("button", { name: "Save", exact: true }).click();
      await expect(
        secondNotes.getByText(/Someone saved this note after you started editing/),
      ).toBeVisible();
      await expect(secondNotes.getByText(/Your draft is kept below/)).toBeVisible();
      await expect(secondNotes.getByLabel("Edit note")).toHaveValue(draft);

      // Reloading brings the current text beside the draft, which survives.
      await secondNotes.getByRole("button", { name: "Reload notes" }).click();
      await expect(secondNotes.getByText("Current saved text")).toBeVisible();
      await expect(secondNotes).toContainText(first);
      await expect(secondNotes.getByLabel("Edit note")).toHaveValue(draft);
    } finally {
      await second.close();
      await deleteIncident(page, id);
    }
  });
});
