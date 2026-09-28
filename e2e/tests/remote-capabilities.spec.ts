import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { getAuthHeaders } from "../helpers.ts";

/**
 * Release C's two-cluster evidence (U12): AE2 (a preview and apply stay pinned
 * to the cluster they were reviewed against) and AE3 (a remote dashboard shows
 * what it observed and never a manufactured number).
 *
 * The single home for Release C's e2e coverage. It needs a registered remote
 * cluster, which scripts/test-remote-capabilities.sh builds: a second cluster
 * carrying a remote-only CRD (widgets.k8scenter.test) the local cluster does
 * not have, and a narrower identity. Without K8SCENTER_REMOTE_CLUSTER_ID the
 * whole suite skips, so it lands in CI and every checkout without running.
 *
 * Environment:
 *   K8SCENTER_REMOTE_CLUSTER_ID         required; the id the script printed
 *   K8SCENTER_LOCAL_KUBE_CONTEXT        kubectl context of the LOCAL cluster the
 *                                       backend runs against (default kind-e2e)
 *   K8SCENTER_REMOTE_REGISTRATION_FILE  the script's saved registration body;
 *                                       only the re-registration case needs it
 *
 * Serial, and the eviction case runs last: it deletes the registration, so the
 * original id is invalid for anything after it.
 */

const REMOTE = process.env.K8SCENTER_REMOTE_CLUSTER_ID;
const LOCAL_CONTEXT = process.env.K8SCENTER_LOCAL_KUBE_CONTEXT ?? "kind-e2e";
const REGISTRATION_FILE = process.env.K8SCENTER_REMOTE_REGISTRATION_FILE;
const FIXTURE_NS = "k8scenter-remote-fixture";

function widgetYaml(name: string): string {
  return [
    "apiVersion: k8scenter.test/v1",
    "kind: Widget",
    "metadata:",
    `  name: ${name}`,
    `  namespace: ${FIXTURE_NS}`,
    "spec:",
    "  size: small",
  ].join("\n");
}

/** Headers for a call addressed to `clusterId`, as the app would send them. */
async function headersFor(
  page: Page,
  clusterId: string,
  contentType = "application/json",
): Promise<Record<string, string>> {
  return {
    ...(await getAuthHeaders(page)),
    "Content-Type": contentType,
    "X-Cluster-ID": clusterId,
  };
}

/** Selects `clusterId` the way ClusterSwitcher persists it, before any load. */
async function selectCluster(page: Page, clusterId: string): Promise<void> {
  await page.addInitScript((id: string) => {
    localStorage.setItem(
      "k8scenter.clusterTarget",
      JSON.stringify({
        clusterId: id,
        generation: id === "local" ? "local" : "unknown",
      }),
    );
  }, clusterId);
}

/** True when the LOCAL cluster serves the Widget kind at all. */
function localHasWidgetKind(): boolean {
  try {
    execFileSync(
      "kubectl",
      ["--context", LOCAL_CONTEXT, "get", "widgets.k8scenter.test", "-A"],
      { stdio: "pipe" },
    );
    return true;
  } catch {
    return false;
  }
}

test.describe.serial("Remote cluster capabilities", () => {
  test.skip(
    !REMOTE,
    "requires a registered remote cluster — see scripts/test-remote-capabilities.sh",
  );

  // Each test starts from the app shell so getAuthHeaders can read the token.
  test.beforeEach(async ({ page }) => {
    await page.goto("/");
  });

  test("capability disclosure explains an unsupported remote operation before data entry", async ({
    page,
  }) => {
    const res = await page.request.get(`/api/v1/capabilities/${REMOTE}`, {
      headers: await headersFor(page, REMOTE!),
    });
    expect(res.status()).toBe(200);
    const caps = (await res.json()).data.capabilities as Array<{
      operation: string;
      platformSupported: boolean;
      reasonCode: string;
    }>;
    const byId = new Map(caps.map((c) => [c.operation, c]));

    // Unsupported on remote, and said so as unsupported -- not as an error.
    for (const id of ["pod.exec", "resources.counts", "logs.stream"]) {
      expect(byId.get(id)?.platformSupported, id).toBe(false);
      expect(byId.get(id)?.reasonCode, id).toBe("unsupported_platform");
    }
    // Supported since U9a/U9b/U10 -- never reported as unsupported_platform.
    for (const id of ["yaml.validate", "yaml.apply", "dashboard.summary"]) {
      expect(byId.get(id)?.platformSupported, id).toBe(true);
      expect(byId.get(id)?.reasonCode, id).not.toBe("unsupported_platform");
    }

    // And the YAML page, opened on the remote cluster, says so before the
    // operator types anything: no "not supported" notice for Apply.
    await selectCluster(page, REMOTE!);
    await page.route("**/esm.sh/monaco-editor**", (route) => route.abort());
    await page.goto("/tools/yaml-apply");
    await expect(page.getByRole("heading", { name: "YAML Apply" })).toBeVisible();
    await expect(page.getByText(/does not support Apply YAML/i)).toHaveCount(0);
  });

  test("previewing a remote-only CRD resolves against the remote cluster", async ({
    page,
  }) => {
    const yaml = widgetYaml("e2e-preview-widget");

    const remote = await page.request.post("/api/v1/yaml/validate", {
      headers: await headersFor(page, REMOTE!, "text/yaml"),
      data: yaml,
    });
    expect(remote.status()).toBe(200);
    const preview = (await remote.json()).data;
    expect(preview.valid).toBe(true);
    expect(preview.targetCluster).toBe(REMOTE);

    // The same document against the local cluster does not resolve: the kind
    // exists only on the remote, which is what proves which schema was used.
    const local = await page.request.post("/api/v1/yaml/validate", {
      headers: await headersFor(page, "local", "text/yaml"),
      data: yaml,
    });
    const localBody = await local.json();
    expect(localBody.data?.valid === true).toBe(false);
  });

  test("switching clusters after a preview keeps apply pinned to the reviewed target", async ({
    page,
  }) => {
    const yaml = widgetYaml("e2e-pinned-widget");
    const preview = (
      await (
        await page.request.post("/api/v1/yaml/validate", {
          headers: await headersFor(page, REMOTE!, "text/yaml"),
          data: yaml,
        })
      ).json()
    ).data;

    // The operator has since switched to local: the request now carries the
    // local header but the pin from the remote preview. The server refuses
    // and applies nothing (D4).
    const mismatched = await page.request.post(
      `/api/v1/yaml/apply?targetCluster=${encodeURIComponent(
        preview.targetCluster,
      )}&targetGeneration=${encodeURIComponent(preview.targetGeneration)}`,
      { headers: await headersFor(page, "local", "text/yaml"), data: yaml },
    );
    expect(mismatched.status()).toBe(409);
    expect((await mismatched.json()).error.reason).toBe("cluster_pin_mismatch");

    // Addressed to the pinned target, the same apply lands there.
    const pinned = await page.request.post(
      `/api/v1/yaml/apply?targetCluster=${encodeURIComponent(
        preview.targetCluster,
      )}&targetGeneration=${encodeURIComponent(preview.targetGeneration)}`,
      { headers: await headersFor(page, REMOTE!, "text/yaml"), data: yaml },
    );
    expect(pinned.status()).toBe(200);
    expect((await pinned.json()).data.summary.failed).toBe(0);
  });

  test("a remote-only object is never created on the local cluster", async ({
    page,
  }) => {
    const res = await page.request.get(
      `/api/v1/resources/widgets.k8scenter.test/${FIXTURE_NS}/e2e-pinned-widget`,
      { headers: await headersFor(page, REMOTE!) },
    );
    // Present on the remote (created by the previous case)...
    expect(res.status()).toBe(200);
    // ...and the local cluster does not even serve the kind. Asserted on the
    // local cluster's side of the wire, not on an error string.
    await expect.poll(() => localHasWidgetKind(), { timeout: 10_000 }).toBe(
      false,
    );
  });

  test("remote dashboard shows counts and an explicit metrics-unavailable state", async ({
    page,
  }) => {
    const res = await page.request.get(
      "/api/v1/cluster/dashboard-summary?coverage=1",
      { headers: await headersFor(page, REMOTE!) },
    );
    expect(res.status()).toBe(200);
    const summary = (await res.json()).data;
    expect(summary.nodes.total).toBeGreaterThan(0);
    const cov = new Map(
      (summary.coverage as Array<{ section: string; status: string }>).map(
        (r) => [r.section, r.status],
      ),
    );
    expect(cov.get("nodes")).toBe("ok");
    expect(cov.get("cpu")).toBe("unavailable");
    expect(cov.get("memory")).toBe("unavailable");

    await selectCluster(page, REMOTE!);
    await page.goto("/");
    const cpu = page.locator('[data-widget-id="cpu-tile"]');
    await expect(cpu).toHaveAttribute(
      "data-widget-state",
      "coverage-unavailable",
    );
    await expect(cpu).toContainText(/metrics binding/i);
    await expect(cpu).not.toContainText(/^0\s*%/);
  });

  test("remote dashboard shows no health score", async ({ page }) => {
    const summary = (
      await (
        await page.request.get("/api/v1/cluster/dashboard-summary?coverage=1", {
          headers: await headersFor(page, REMOTE!),
        })
      ).json()
    ).data;
    expect(summary.health).toBeNull();

    await selectCluster(page, REMOTE!);
    await page.goto("/");
    const health = page.locator('[data-widget-id="cluster-health"]');
    await expect(health).toHaveAttribute(
      "data-widget-state",
      "coverage-unavailable",
    );
    // No gauge ring at all: an empty ring would read as a score of zero.
    await expect(health.locator("svg circle")).toHaveCount(0);
  });

  test("switching clusters clears the previous cluster's dashboard numbers", async ({
    page,
  }) => {
    await selectCluster(page, "local");
    await page.goto("/");
    await expect(
      page.locator('[data-widget-id="cpu-tile"]'),
    ).toHaveAttribute("data-widget-state", "ready");

    const remoteSummary = page.waitForRequest(
      (r) =>
        r.url().includes("/api/v1/cluster/dashboard-summary?coverage=1") &&
        r.headers()["x-cluster-id"] === REMOTE,
    );
    await page.getByRole("button", { name: /Change cluster/ }).click();
    await page
      .getByRole("listbox", { name: "Clusters" })
      .getByRole("option", { name: /E2E remote fixture/ })
      .click();
    // The switch reloads the page; the dashboard asks the new cluster.
    await remoteSummary;

    // The local cluster's CPU percentage is gone, replaced by the remote's
    // stated unavailability -- never the previous cluster's number.
    await expect(
      page.locator('[data-widget-id="cpu-tile"]'),
    ).toHaveAttribute("data-widget-state", "coverage-unavailable");
  });

  test("deleting the cluster invalidates cached discovery", async ({ page }) => {
    test.skip(
      !REGISTRATION_FILE,
      "needs K8SCENTER_REMOTE_REGISTRATION_FILE to re-register the cluster",
    );
    const yaml = widgetYaml("e2e-eviction-widget");
    const validateOn = async (id: string) =>
      page.request.post("/api/v1/yaml/validate", {
        headers: await headersFor(page, id, "text/yaml"),
        data: yaml,
      });

    // Warm the remote discovery cache under the original id.
    expect((await validateOn(REMOTE!)).status()).toBe(200);

    const del = await page.request.delete(`/api/v1/clusters/${REMOTE}`, {
      headers: await getAuthHeaders(page),
    });
    expect(del.ok()).toBe(true);

    const created = await page.request.post("/api/v1/clusters", {
      headers: await getAuthHeaders(page),
      data: JSON.parse(readFileSync(REGISTRATION_FILE!, "utf8")),
    });
    expect(created.status()).toBe(201);
    const newId = (await created.json()).data.id as string;
    expect(newId).not.toBe(REMOTE);
    test.info().annotations.push({
      type: "K8SCENTER_REMOTE_CLUSTER_ID",
      description: newId,
    });

    // The remote-only kind resolves under the new registration...
    const fresh = await validateOn(newId);
    expect(fresh.status()).toBe(200);
    expect((await fresh.json()).data.valid).toBe(true);
    // ...and the old id no longer names a cluster: nothing cached under it
    // answers.
    const stale = await validateOn(REMOTE!);
    expect(stale.ok()).toBe(false);
  });
});
