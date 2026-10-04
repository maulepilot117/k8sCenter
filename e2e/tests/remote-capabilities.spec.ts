import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { getAuthHeaders, seedClusterTarget } from "../helpers.ts";

/**
 * Release C's two-cluster evidence (U12): AE2 (a preview and apply stay pinned
 * to the cluster they were reviewed against) and AE3 (a remote dashboard shows
 * what it observed and never a manufactured number). R-8 (U15) adds the
 * routed-feature and carve-out capability rows and a node drain call.
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
 * original id is invalid for anything after it, and it deregisters its own
 * replacement, so no fixture registration remains once the suite ends.
 */

// No traces for this file. The eviction case POSTs the saved registration
// body, which carries a ServiceAccount token, and under the config's
// trace: "on-first-retry" a CI retry would record that request body into
// trace.zip and upload it with the report. trace is a worker-scoped option,
// so Playwright accepts it only at file level, not on one case or describe;
// the cost is that the other cases here lose their retry traces too.
test.use({ trace: "off" });

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

/**
 * Previews `yaml` on the remote cluster, then applies it pinned to the target
 * that preview reports (D4) -- the flow the YAML page drives. Server-side
 * apply, so re-applying an existing object is a no-op and any case can call
 * this to establish its own fixture.
 */
async function previewAndApplyPinned(
  page: Page,
  yaml: string,
): Promise<{ targetCluster: string; targetGeneration: string }> {
  const validated = await page.request.post("/api/v1/yaml/validate", {
    headers: await headersFor(page, REMOTE!, "text/yaml"),
    data: yaml,
  });
  expect(validated.status()).toBe(200);
  const preview = (await validated.json()).data;
  const applied = await page.request.post(pinnedApplyUrl(preview), {
    headers: await headersFor(page, REMOTE!, "text/yaml"),
    data: yaml,
  });
  expect(applied.status()).toBe(200);
  expect((await applied.json()).data.summary.failed).toBe(0);
  return preview;
}

/** The apply URL carrying the pin a preview reported. */
function pinnedApplyUrl(preview: {
  targetCluster: string;
  targetGeneration: string;
}): string {
  return `/api/v1/yaml/apply?targetCluster=${encodeURIComponent(
    preview.targetCluster,
  )}&targetGeneration=${encodeURIComponent(preview.targetGeneration)}`;
}

/**
 * Fails loudly unless kubectl can reach the LOCAL cluster. Without it, every
 * kubectl failure (binary missing, wrong context, API unreachable) would read
 * as "the local cluster does not serve the kind" and pass vacuously.
 */
function assertLocalKubectlReachable(): void {
  try {
    execFileSync(
      "kubectl",
      ["--context", LOCAL_CONTEXT, "get", "--raw", "/version"],
      { stdio: "pipe" },
    );
  } catch (err) {
    const stderr = (err as { stderr?: Buffer }).stderr?.toString().trim();
    throw new Error(
      `kubectl cannot reach the local cluster (context "${LOCAL_CONTEXT}"; ` +
        `set K8SCENTER_LOCAL_KUBE_CONTEXT): ${stderr || (err as Error).message}`,
    );
  }
}

/**
 * True when the LOCAL cluster serves the Widget kind at all. False ONLY when
 * kubectl says the resource type does not exist; any other failure is
 * rethrown so it cannot masquerade as the answer.
 */
function localHasWidgetKind(): boolean {
  try {
    execFileSync(
      "kubectl",
      ["--context", LOCAL_CONTEXT, "get", "widgets.k8scenter.test", "-A"],
      { stdio: "pipe" },
    );
    return true;
  } catch (err) {
    const stderr = (err as { stderr?: Buffer }).stderr?.toString() ?? "";
    if (/the server doesn't have a resource type/i.test(stderr)) {
      return false;
    }
    throw err;
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
    for (const id of [
      "yaml.validate",
      "yaml.apply",
      "yaml.diff",
      "yaml.export",
      "dashboard.summary",
    ]) {
      expect(byId.get(id)?.platformSupported, id).toBe(true);
      expect(byId.get(id)?.reasonCode, id).not.toBe("unsupported_platform");
    }
    // R-8: the features routed to the selected cluster are declared supported,
    // and the carve-outs that stay local are declared unsupported (U13; the
    // policy rows since #530).
    for (const id of [
      "node.drain",
      "gitops.applications",
      "velero.backups",
      "storage.snapshots",
      "storage.classes",
      "flux.notifications",
      "alert.rules",
      "gateway.read",
      "mesh.routing",
      "mesh.mtls",
      "eso.read",
      "certmanager.certificates",
      "policy.read",
    ]) {
      expect(byId.get(id)?.platformSupported, id).toBe(true);
      expect(byId.get(id)?.reasonCode, id).not.toBe("unsupported_platform");
    }
    for (const id of [
      "cni.config",
      "mesh.golden_signals",
      "eso.history",
      "eso.metrics",
      "topology.graph",
      "diagnostics.read",
      "policy.compliance_history",
      "velero.assurance",
    ]) {
      expect(byId.get(id)?.platformSupported, id).toBe(false);
      expect(byId.get(id)?.reasonCode, id).toBe("unsupported_platform");
    }

    // And the YAML page, opened on the remote cluster, says so before the
    // operator types anything: no "not supported" notice for Apply.
    await seedClusterTarget(page, REMOTE!);
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
    // HandleValidate reports an unmappable kind as a 200 carrying a
    // per-document RESTMapping failure (yaml/differ.go diffOne), not as an
    // HTTP error -- so an auth or transport failure cannot satisfy this.
    const local = await page.request.post("/api/v1/yaml/validate", {
      headers: await headersFor(page, "local", "text/yaml"),
      data: yaml,
    });
    expect(local.status()).toBe(200);
    const localPreview = (await local.json()).data;
    expect(localPreview.valid).toBe(false);
    expect(localPreview.documents).toHaveLength(1);
    expect(localPreview.documents[0].valid).toBe(false);
    expect(localPreview.documents[0].errors[0].message).toMatch(
      /^unknown resource type k8scenter\.test\/v1, Kind=Widget: /,
    );
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
    const mismatched = await page.request.post(pinnedApplyUrl(preview), {
      headers: await headersFor(page, "local", "text/yaml"),
      data: yaml,
    });
    expect(mismatched.status()).toBe(409);
    expect((await mismatched.json()).error.reason).toBe("cluster_pin_mismatch");

    // Addressed to the pinned target, the same apply lands there.
    const pinned = await page.request.post(pinnedApplyUrl(preview), {
      headers: await headersFor(page, REMOTE!, "text/yaml"),
      data: yaml,
    });
    expect(pinned.status()).toBe(200);
    expect((await pinned.json()).data.summary.failed).toBe(0);
  });

  test("a remote-only object is never created on the local cluster", async ({
    page,
  }) => {
    // Establish the widget here rather than relying on the previous case, so
    // this case also stands alone under --grep. Same name and same pinned
    // flow, so in a full run it is a server-side-apply no-op.
    await previewAndApplyPinned(page, widgetYaml("e2e-pinned-widget"));

    // The target-scoped export resolves the kind through the header
    // cluster's discovery (yaml/handler.go resolveGVR), which matches the
    // plural resource name -- "widgets", not the Kind or a group-qualified
    // name. /resources/{kind} would not do: it serves built-in kinds only,
    // from the LOCAL informer cache.
    const res = await page.request.get(
      `/api/v1/yaml/export/widgets/${FIXTURE_NS}/e2e-pinned-widget`,
      { headers: await headersFor(page, REMOTE!) },
    );
    // Present on the remote...
    expect(res.status()).toBe(200);
    const exported = (await res.json()).data as string;
    expect(exported).toMatch(/^kind: Widget$/m);
    expect(exported).toMatch(/^\s+name: e2e-pinned-widget$/m);
    // ...and the local cluster does not even serve the kind. Asserted on the
    // local cluster's side of the wire, not on an error string.
    assertLocalKubectlReachable();
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

    await seedClusterTarget(page, REMOTE!);
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

    await seedClusterTarget(page, REMOTE!);
    await page.goto("/");
    const health = page.locator('[data-widget-id="cluster-health"]');
    await expect(health).toHaveAttribute(
      "data-widget-state",
      "coverage-unavailable",
    );
    await expect(
      health.getByTestId("widget-coverage-unavailable"),
    ).toBeVisible();
    // No gauge ring at all: an empty ring would read as a score of zero. The
    // coverage card's own glyph is an <svg><circle>, so match what only the
    // Gauge draws -- its progress arc carries stroke-dashoffset -- and check
    // the widget body (whose unscored placeholder stands in for the gauge)
    // is not rendered either.
    await expect(health.locator("circle[stroke-dashoffset]")).toHaveCount(0);
    await expect(health.getByTestId("health-unscored")).toHaveCount(0);
  });

  test("switching clusters clears the previous cluster's dashboard numbers", async ({
    page,
  }) => {
    await seedClusterTarget(page, "local");
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

  test("a drain on the remote is authorized by the remote cluster's RBAC", async ({
    page,
  }) => {
    // A per-run name no real node carries, so no call here can cordon or evict
    // anything: the drain's first cluster write is the cordon PATCH, which
    // returns NotFound. That holds even on a remote supplied via
    // KUBECENTER_REMOTE_CONTEXT whose existing bindings grant more than the
    // fixture's read-only role.
    const node = `e2e-no-such-node-${Date.now().toString(36)}`;
    const drain = async (clusterId: string) =>
      page.request.post(`/api/v1/resources/nodes/${node}/drain`, {
        headers: await headersFor(page, clusterId),
        data: {},
      });

    // The fixture identity on the remote may read nodes but not update them,
    // and the drain's access check runs on the target cluster before any task
    // starts, so the remote refuses it. The detail names the RBAC verb, so a
    // 403 from anything other than that access check does not pass.
    const denied = await drain(REMOTE!);
    expect(denied.status()).toBe(403);
    expect((await denied.json()).error.detail).toContain(
      "lacks 'update' on 'nodes'",
    );

    // The same request on local, where the admin is cluster-admin, passes that
    // check and starts a task (which then fails on the missing node). The
    // difference is what shows the 403 came from the remote, not local.
    const local = await drain("local");
    expect(local.status()).toBe(202);
    expect((await local.json()).data.taskID).toBeTruthy();
  });

  // Must stay last: see the header.
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
    test.info().annotations.push({
      type: "K8SCENTER_REMOTE_CLUSTER_ID",
      description: newId,
    });

    // The replacement is deregistered whatever the assertions do, so after
    // this case no fixture registration remains: the original was deleted
    // above and the replacement is deleted in the finally below.
    try {
      expect(newId).not.toBe(REMOTE);

      // The remote-only kind resolves under the new registration...
      const fresh = await validateOn(newId);
      expect(fresh.status()).toBe(200);
      expect((await fresh.json()).data.valid).toBe(true);
      // ...and the old id no longer names a cluster: nothing cached under it
      // answers.
      const stale = await validateOn(REMOTE!);
      expect(stale.ok()).toBe(false);
    } finally {
      const cleanup = await page.request.delete(`/api/v1/clusters/${newId}`, {
        headers: await getAuthHeaders(page),
      });
      expect(cleanup.ok(), `deregister replacement ${newId}`).toBe(true);
    }
  });
});
