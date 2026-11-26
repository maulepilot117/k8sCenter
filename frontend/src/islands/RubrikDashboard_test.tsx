import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type { RubrikClusterState, RubrikStatus } from "@/lib/rubrik-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * Rubrik namespace pages: the provider and setup states, the namespace list
 * (protection, SLA per object, compliance, freshness), the RSC-failing and
 * noAccess answers, and the detail panel's snapshots and jobs. fetch is
 * routed by method and URL; each test sets the replies it needs.
 */
setupDom({ browser: true });

const { default: RubrikDashboard } = await import("./RubrikDashboard.tsx");
const { resetRubrikStatusForTest } = await import("@/src/lib/rubrik-status.ts");

const bunFetch = globalThis.fetch;
afterAll(() => {
  globalThis.fetch = bunFetch;
});

const FETCHED = new Date(Date.now() - 2 * 3_600_000).toISOString();

function status(state: RubrikClusterState = "mapped"): RubrikStatus {
  return {
    enabled: true,
    preview: true,
    writesEnabled: false,
    cluster: { id: "local", state },
  };
}

function object(over: Record<string, unknown> = {}) {
  return {
    family: "namespace",
    id: "obj-shop",
    name: "shop",
    coverage: { namespaces: ["shop"] },
    configuredSla: { id: "sla-gold", name: "Gold" },
    effectiveSla: { id: "sla-gold", name: "Gold" },
    compliance: "in_compliance",
    mayHoldClusterScoped: false,
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
    ...over,
  };
}

function row(over: Record<string, unknown> = {}) {
  return {
    namespace: "shop",
    protected: true,
    compliance: "in_compliance",
    lastSnapshot: FETCHED,
    objectIds: ["obj-shop"],
    actionTargets: ["obj-shop"],
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
    ...over,
  };
}

function list(over: Record<string, unknown> = {}) {
  return {
    noAccess: false,
    namespaces: [
      row(),
      row({
        namespace: "batch",
        protected: false,
        compliance: "unknown",
        lastSnapshot: undefined,
        objectIds: [],
        actionTargets: [],
      }),
    ],
    objects: [object()],
    freshness: { fetchedAt: FETCHED, stale: false },
    ...over,
  };
}

const reply = (code: number, payload?: unknown): Response =>
  new Response(payload === undefined ? null : JSON.stringify(payload), {
    status: code,
    headers: { "Content-Type": "application/json" },
  });
const ok = (data: unknown) => reply(200, { data });
const fail = (code: number, reason: string | undefined, message: string) =>
  reply(code, { error: { code, message, reason } });

let calls: string[] = [];
let routes: Record<string, () => Response> = {};

beforeEach(() => {
  calls = [];
  resetRubrikStatusForTest();
  setAccessToken("t");
  routes = {
    "GET /api/v1/rubrik/status": () => ok(status()),
    "GET /api/v1/rubrik/namespaces": () => ok(list()),
  };
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const key = `${init?.method ?? "GET"} ${String(input)}`;
    calls.push(key);
    const route = routes[key];
    return route ? route() : fail(599, "unstubbed", key);
  }) as unknown as typeof globalThis.fetch;
});

afterEach(() => {
  resetRubrikStatusForTest();
});

const nsRow = (ns: string) => body().getByTestId(`rubrik-ns-${ns}`);

test("lists each namespace with its protection, SLA, compliance and fetch time", async () => {
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  body().getByText("Preview");
  body().getByText(/Fetched from RSC/);
  const shop = nsRow("shop");
  expect(shop.textContent).toContain("Protected");
  expect(shop.textContent).toContain("Gold");
  expect(shop.textContent).toContain("In compliance");
  const batch = nsRow("batch");
  expect(batch.textContent).toContain("Not protected");
  expect(batch.textContent).toContain("None");
  unmount();
});

test("a namespace with two protection sets shows each set's SLA", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(
      list({
        namespaces: [row({ objectIds: ["set-a", "set-b"] })],
        objects: [
          object({
            family: "protectionSet",
            id: "set-a",
            name: "web-tier",
            effectiveSla: { id: "g", name: "Gold" },
          }),
          object({
            family: "protectionSet",
            id: "set-b",
            name: "db-tier",
            effectiveSla: { id: "s", name: "Silver" },
          }),
        ],
      }),
    );
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  const shop = nsRow("shop");
  expect(shop.textContent).toContain("web-tier: Gold");
  expect(shop.textContent).toContain("db-tier: Silver");
  unmount();
});

test("AE6: stale data after an auth failure says so with the last fetch time", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(
      list({
        namespaces: [row()],
        freshness: { fetchedAt: FETCHED, stale: true, error: "auth_failed" },
      }),
    );
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  const alert = body().getByRole("alert").textContent ?? "";
  expect(alert).toContain("RSC refused k8sCenter's client ID or secret");
  expect(alert).toContain("fetched 2h ago");
  expect(nsRow("shop").textContent).toContain("Protected");
  expect(body().queryByText("Not protected")).toBeNull();
  unmount();
});

test("AE6: an RSC failure with no earlier answer shows no namespace as unprotected", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    fail(400, "auth_failed", "RSC refused the client ID or secret");
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  const alert = body().getByRole("alert").textContent ?? "";
  expect(alert).toContain("RSC refused k8sCenter's client ID or secret");
  expect(alert).toContain("protection is not shown");
  expect(body().queryByText("Not protected")).toBeNull();
  expect(body().queryByRole("table")).toBeNull();
  // Retry asks again.
  routes["GET /api/v1/rubrik/namespaces"] = () => ok(list());
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Retry" }));
  });
  await settle();
  expect(nsRow("shop").textContent).toContain("Protected");
  unmount();
});

test("a noAccess answer explains how access is granted and names no namespace", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok({ noAccess: true, namespaces: [], objects: [] });
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  body().getByText(/You do not have Rubrik read access/);
  body().getByText("rubrik.kubecenter.io");
  expect(body().queryByRole("table")).toBeNull();
  expect(body().queryByText("Not protected")).toBeNull();
  unmount();
});

test("with the provider disabled the page says it is not enabled", async () => {
  routes["GET /api/v1/rubrik/status"] = () =>
    fail(404, undefined, "404 page not found");
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  body().getByText(/The Rubrik provider is not enabled/);
  expect(calls).not.toContain("GET /api/v1/rubrik/namespaces");
  unmount();
});

test("a cluster that is not set up says so and lists nothing", async () => {
  routes["GET /api/v1/rubrik/status"] = () => ok(status("not_set_up"));
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  body().getByText(/Rubrik is not set up for this cluster/);
  expect(calls).not.toContain("GET /api/v1/rubrik/namespaces");
  unmount();
});

test("an admin on an unmapped cluster is sent to the settings page", async () => {
  routes["GET /api/v1/rubrik/status"] = () => ok(status("not_mapped"));
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  const link = body().getByRole("link", { name: "Rubrik settings" });
  expect(link.getAttribute("href")).toBe("/settings/rubrik");
  unmount();
});

test("the detail panel shows snapshots and jobs, with RSC's failure reason", async () => {
  routes["GET /api/v1/rubrik/namespaces/shop"] = () =>
    ok({
      namespace: row(),
      freshness: { fetchedAt: FETCHED, stale: false },
      objects: [
        {
          ...object(),
          snapshots: [
            {
              id: "snap-1",
              time: "2026-11-25T10:00:00Z",
              expires: "2026-12-25T10:00:00Z",
            },
            { id: "snap-2", time: "2026-11-20T10:00:00Z", expired: true },
          ],
          jobs: [
            {
              id: "job-1",
              objectId: "obj-shop",
              type: "Backup",
              status: "failed",
              startTime: "2026-11-24T10:00:00Z",
              failureReason: "Volume snapshot class not found",
            },
          ],
        },
      ],
    });
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "shop" }));
  });
  await settle();
  const panel = body().getByRole("complementary", {
    name: "Rubrik details for shop",
  });
  expect(panel.textContent).toContain("Available");
  expect(panel.textContent).toContain("Expired");
  fireEvent.click(body().getByRole("tab", { name: "Jobs" }));
  expect(panel.textContent).toContain("Failed");
  expect(panel.textContent).toContain("Volume snapshot class not found");
  fireEvent.keyDown(document, { key: "Escape" });
  expect(
    body().queryByRole("complementary", { name: "Rubrik details for shop" }),
  ).toBeNull();
  unmount();
});

test("a history part that failed says so without hiding the other", async () => {
  routes["GET /api/v1/rubrik/namespaces/shop"] = () =>
    ok({
      namespace: row(),
      freshness: { fetchedAt: FETCHED, stale: false },
      objects: [
        { ...object(), snapshots: [], snapshotsError: "unreachable", jobs: [] },
      ],
    });
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "shop" }));
  });
  await settle();
  body().getByText(/Snapshots could not be fetched: RSC could not be reached/);
  fireEvent.click(body().getByRole("tab", { name: "Jobs" }));
  body().getByText("No recent jobs.");
  unmount();
});

test("a detail 404 says the namespace is no longer visible", async () => {
  routes["GET /api/v1/rubrik/namespaces/shop"] = () =>
    fail(404, undefined, "namespace not found");
  const { unmount } = render(<RubrikDashboard />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "shop" }));
  });
  await settle();
  body().getByText("This namespace is no longer visible to you in Rubrik.");
  unmount();
});
