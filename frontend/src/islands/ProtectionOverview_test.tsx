import {
  afterAll,
  afterEach,
  beforeEach,
  describe,
  expect,
  test,
} from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  backupCovers,
  type RubrikInput,
  rubrikCell,
  veleroCell,
} from "@/lib/protection-overview.ts";
import type {
  RubrikClusterState,
  RubrikNamespaceList,
  RubrikNamespaceRow,
  RubrikStatus,
} from "@/lib/rubrik-types.ts";
import type { Backup } from "@/lib/velero-types.ts";
import { body, render, settle, setupDom } from "@/src/test-utils/render.tsx";

/**
 * Protection overview (U15): the pure merge in lib/protection-overview.ts
 * and the page that renders it. fetch is routed by method and URL; each
 * test sets the replies it needs.
 */
setupDom({ browser: true });

const { default: ProtectionOverview } = await import(
  "./ProtectionOverview.tsx"
);
const { resetRubrikStatusForTest } = await import("@/src/lib/rubrik-status.ts");

const bunFetch = globalThis.fetch;
afterAll(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
  resetRubrikStatusForTest();
});

const hoursAgo = (h: number) =>
  new Date(Date.now() - h * 3_600_000).toISOString();

function backup(over: Partial<Backup> = {}): Backup {
  return {
    name: "nightly",
    namespace: "velero",
    phase: "Completed",
    snapshotVolumes: true,
    startTime: hoursAgo(3),
    completionTime: hoursAgo(3),
    itemsBackedUp: 10,
    totalItems: 10,
    warnings: 0,
    errors: 0,
    ...over,
  };
}

function nsRow(over: Partial<RubrikNamespaceRow> = {}): RubrikNamespaceRow {
  return {
    namespace: "team-a",
    protected: true,
    compliance: "in_compliance",
    lastSnapshot: hoursAgo(1),
    objectIds: ["obj-a"],
    actionTargets: ["obj-a"],
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
    ...over,
  };
}

function rubrikList(
  over: Partial<RubrikNamespaceList> = {},
): RubrikNamespaceList {
  return {
    noAccess: false,
    namespaces: [nsRow()],
    objects: [],
    freshness: { fetchedAt: hoursAgo(0), stale: false },
    ...over,
  };
}

// ---------------------------------------------------------------------------
// Pure merge

describe("backupCovers", () => {
  test("an empty include list, or *, covers every namespace", () => {
    expect(backupCovers(backup(), "any")).toBe(true);
    expect(backupCovers(backup({ includedNamespaces: ["*"] }), "any")).toBe(
      true,
    );
  });
  test("an include list and Velero's wildcards", () => {
    const b = backup({ includedNamespaces: ["shop", "team-*"] });
    expect(backupCovers(b, "shop")).toBe(true);
    expect(backupCovers(b, "team-x")).toBe(true);
    expect(backupCovers(b, "cart")).toBe(false);
    expect(backupCovers(backup({ includedNamespaces: ["a.b"] }), "aXb")).toBe(
      false,
    );
  });
  test("an exclude entry wins", () => {
    const b = backup({ excludedNamespaces: ["kube-*"] });
    expect(backupCovers(b, "kube-system")).toBe(false);
    expect(backupCovers(b, "shop")).toBe(true);
  });
});

describe("veleroCell", () => {
  test("an all-namespaces backup gives every namespace its time", () => {
    const at = hoursAgo(5);
    const cell = veleroCell("anything", {
      kind: "loaded",
      backups: [backup({ completionTime: at })],
    });
    expect(cell).toEqual({ kind: "protected", last: { name: "nightly", at } });
  });
  test("a failed newest backup keeps the older completed one", () => {
    const good = hoursAgo(26);
    const cell = veleroCell("shop", {
      kind: "loaded",
      backups: [
        backup({
          name: "today",
          phase: "PartiallyFailed",
          completionTime: hoursAgo(2),
        }),
        backup({ name: "yesterday", completionTime: good }),
      ],
    });
    expect(cell).toMatchObject({
      kind: "failed",
      failed: { name: "today" },
      lastGood: { name: "yesterday", at: good },
    });
  });
  test("a backup still running is neither a recovery point nor a failure", () => {
    const cell = veleroCell("shop", {
      kind: "loaded",
      backups: [
        backup({
          name: "now",
          phase: "InProgress",
          completionTime: undefined,
          startTime: hoursAgo(0),
        }),
        backup({ name: "earlier" }),
      ],
    });
    expect(cell).toMatchObject({
      kind: "protected",
      last: { name: "earlier" },
    });
  });
  test("no covering backup is none; not installed stays not installed", () => {
    const loaded = {
      kind: "loaded" as const,
      backups: [backup({ includedNamespaces: ["x"] })],
    };
    expect(veleroCell("shop", loaded)).toEqual({ kind: "none" });
    expect(veleroCell("shop", { kind: "not_installed" })).toEqual({
      kind: "not_installed",
    });
  });
});

describe("rubrikCell", () => {
  test("AE1: a namespace the answer leaves out reads exactly like no access", () => {
    const loaded: RubrikInput = { kind: "loaded", list: rubrikList() };
    const noAccess: RubrikInput = {
      kind: "loaded",
      list: rubrikList({ noAccess: true, namespaces: [] }),
    };
    expect(rubrikCell("team-b", loaded)).toEqual(
      rubrikCell("team-b", noAccess),
    );
    expect(rubrikCell("team-b", loaded)).toEqual({ kind: "not_applicable" });
    expect(rubrikCell("team-a", loaded)).toMatchObject({ kind: "protected" });
  });
  test("AE6: stale data never shows a namespace as unprotected", () => {
    const fetchedAt = hoursAgo(2);
    const cell = rubrikCell("team-a", {
      kind: "loaded",
      list: rubrikList({
        namespaces: [nsRow({ protected: false, lastSnapshot: undefined })],
        freshness: { fetchedAt, stale: true, error: "auth_failed" },
      }),
    });
    expect(cell).toEqual({
      kind: "rsc_failing",
      reason: "auth_failed",
      fetchedAt,
    });
  });
});

// ---------------------------------------------------------------------------
// Page

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

function status(
  state: RubrikClusterState = "mapped",
  over: Partial<RubrikStatus> = {},
): RubrikStatus {
  return {
    enabled: true,
    preview: true,
    writesEnabled: false,
    cluster: { id: "local", state },
    ...over,
  };
}

beforeEach(() => {
  calls = [];
  resetRubrikStatusForTest();
  setAccessToken("t");
  routes = {
    "GET /api/v1/resources/namespaces": () =>
      ok([{ metadata: { name: "team-b" } }, { metadata: { name: "team-a" } }]),
    "GET /api/v1/velero/status": () =>
      ok({
        detected: true,
        bslCount: 1,
        vslCount: 0,
        lastChecked: hoursAgo(0),
      }),
    "GET /api/v1/velero/backups": () =>
      ok([backup({ includedNamespaces: ["team-a"] })]),
    "GET /api/v1/rubrik/status": () => ok(status()),
    "GET /api/v1/rubrik/namespaces": () => ok(rubrikList()),
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

const cell = (ns: string, which: "velero-cell" | "rubrik-cell") =>
  body()
    .getByTestId(`overview-${ns}`)
    .querySelector(`[data-testid="${which}"]`) as HTMLElement | null;

async function open() {
  const view = render(<ProtectionOverview />);
  await settle();
  return view;
}

test("every listed namespace has a row, protected by a provider or not", async () => {
  const view = await open();
  expect(cell("team-a", "velero-cell")?.textContent).toContain("Protected");
  expect(cell("team-a", "velero-cell")?.textContent).toContain("nightly");
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("Last snapshot");
  // team-b: no backup covers it and the Rubrik answer leaves it out.
  expect(cell("team-b", "velero-cell")?.textContent).toBe("No backup");
  expect(cell("team-b", "rubrik-cell")?.textContent).toBe("Not applicable");
  view.unmount();
});

test("AE1: an ungranted namespace's cell renders exactly like not applicable", async () => {
  const first = await open();
  const ungranted = cell("team-b", "rubrik-cell")?.innerHTML;
  first.unmount();
  resetRubrikStatusForTest();
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(rubrikList({ noAccess: true, namespaces: [] }));
  const second = await open();
  expect(cell("team-a", "rubrik-cell")?.innerHTML).toBe(ungranted);
  expect(cell("team-b", "rubrik-cell")?.innerHTML).toBe(ungranted);
  second.unmount();
});

test("a known, unprotected namespace reads Not protected", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(
      rubrikList({
        namespaces: [
          nsRow(),
          nsRow({
            namespace: "team-b",
            protected: false,
            objectIds: [],
            lastSnapshot: undefined,
          }),
        ],
      }),
    );
  const view = await open();
  expect(cell("team-b", "rubrik-cell")?.textContent).toBe("Not protected");
  view.unmount();
});

test("AE6: an RSC failure shows the last fetch time and never unprotected", async () => {
  const last = hoursAgo(2);
  routes["GET /api/v1/rubrik/status"] = () =>
    ok(
      status("mapped", {
        connection: {
          configured: true,
          hasSecret: true,
          health: "auth_failed",
          lastSuccessAt: last,
        },
      }),
    );
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    fail(400, "auth_failed", "RSC refused the client ID or secret");
  const view = await open();
  for (const ns of ["team-a", "team-b"]) {
    const text = cell(ns, "rubrik-cell")?.textContent ?? "";
    expect(text).toContain("RSC unavailable");
    expect(text).toContain("last fetched 2h ago");
  }
  expect(body().queryByText("Not protected")).toBeNull();
  view.unmount();
});

test("with Rubrik disabled the page shows Velero only, with no Rubrik column", async () => {
  routes["GET /api/v1/rubrik/status"] = () =>
    fail(404, undefined, "404 page not found");
  const view = await open();
  expect(body().queryByRole("columnheader", { name: /Rubrik/ })).toBeNull();
  expect(cell("team-a", "rubrik-cell")).toBeNull();
  expect(cell("team-a", "velero-cell")?.textContent).toContain("Protected");
  expect(calls).not.toContain("GET /api/v1/rubrik/namespaces");
  view.unmount();
});

test("an invalid mapping shows not set up, with a setup link for admins only", async () => {
  routes["GET /api/v1/rubrik/status"] = () => ok(status("mapping_invalid"));
  const admin = await open();
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("Not set up");
  const link = body().getAllByRole("link", { name: "Set up Rubrik" })[0];
  expect(link?.getAttribute("href")).toBe("/settings/rubrik");
  expect(calls).not.toContain("GET /api/v1/rubrik/namespaces");
  admin.unmount();

  // A non-admin is sent not_set_up for every setup problem: no link.
  resetRubrikStatusForTest();
  routes["GET /api/v1/rubrik/status"] = () => ok(status("not_set_up"));
  const other = await open();
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("Not set up");
  expect(body().queryByRole("link", { name: "Set up Rubrik" })).toBeNull();
  other.unmount();
});

test("a namespace whose newest backup failed shows it, and the last good time", async () => {
  routes["GET /api/v1/velero/backups"] = () =>
    ok([
      backup({ name: "today", phase: "Failed", completionTime: hoursAgo(1) }),
      backup({ name: "monday", completionTime: hoursAgo(48) }),
    ]);
  const view = await open();
  const text = cell("team-a", "velero-cell")?.textContent ?? "";
  expect(text).toContain("Last backup failed");
  expect(text).toContain("Last good backup 2d ago (monday)");
  view.unmount();
});

test("with Velero not installed every Velero cell says so, never no backup", async () => {
  routes["GET /api/v1/velero/status"] = () =>
    ok({ detected: false, bslCount: 0, vslCount: 0, lastChecked: hoursAgo(0) });
  const view = await open();
  for (const ns of ["team-a", "team-b"]) {
    expect(cell(ns, "velero-cell")?.textContent).toBe("Velero not installed");
  }
  expect(body().queryByText("No backup")).toBeNull();
  view.unmount();
});

test("a namespace list that fails shows the failure and no rows", async () => {
  routes["GET /api/v1/resources/namespaces"] = () =>
    fail(500, undefined, "boom");
  const view = await open();
  body().getByText("The namespaces could not be loaded.");
  expect(body().queryByRole("table")).toBeNull();
  view.unmount();
});
