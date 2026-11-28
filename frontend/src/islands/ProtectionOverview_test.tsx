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
  rubrikActions,
  rubrikCell,
  veleroCell,
} from "@/lib/protection-overview.ts";
import type {
  RubrikClusterState,
  RubrikNamespaceList,
  RubrikNamespaceRow,
  RubrikObject,
  RubrikOperation,
  RubrikStatus,
} from "@/lib/rubrik-types.ts";
import type { Backup } from "@/lib/velero-types.ts";
import {
  act,
  body,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

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

function rubrikObject(over: Partial<RubrikObject> = {}): RubrikObject {
  return {
    family: "namespace",
    id: "obj-a",
    name: "team-a",
    coverage: { namespaces: ["team-a"] },
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
  test("an include list names namespaces; patterns are not expanded, as in Backup Assurance", () => {
    const b = backup({ includedNamespaces: ["shop", "team-*"] });
    expect(backupCovers(b, "shop")).toBe(true);
    expect(backupCovers(b, "cart")).toBe(false);
    // velero/assurance.go coversNamespace: exact names or "*" only.
    expect(backupCovers(b, "team-x")).toBe(false);
  });
  test("an exclude entry naming the namespace wins, even over *", () => {
    const b = backup({
      includedNamespaces: ["*"],
      excludedNamespaces: ["kube-system"],
    });
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
  test("a protected namespace carries its compliance and each SLA domain once", () => {
    const cell = rubrikCell("team-a", {
      kind: "loaded",
      list: rubrikList({
        namespaces: [
          nsRow({
            compliance: "out_of_compliance",
            objectIds: ["obj-a", "obj-b", "obj-c"],
          }),
        ],
        objects: [
          rubrikObject(),
          rubrikObject({ id: "obj-b", effectiveSla: { id: "", name: "" } }),
          rubrikObject({ id: "obj-c" }),
          // Not one of the namespace's objects.
          rubrikObject({
            id: "obj-z",
            effectiveSla: { id: "sla-bronze", name: "Bronze" },
          }),
        ],
      }),
    });
    expect(cell).toMatchObject({
      kind: "protected",
      compliance: "out_of_compliance",
      slas: ["Gold", "No SLA"],
    });
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

describe("rubrikActions", () => {
  test("only a loaded answer with a row for the namespace offers actions", () => {
    expect(rubrikActions("team-a", { kind: "loading" })).toBeUndefined();
    expect(
      rubrikActions("team-a", {
        kind: "loaded",
        list: rubrikList({ noAccess: true }),
      }),
    ).toBeUndefined();
    expect(
      rubrikActions("team-b", { kind: "loaded", list: rubrikList() }),
    ).toBeUndefined();
  });

  test("the objects are the row's own and its action targets, nothing else", () => {
    const row = nsRow({ objectIds: ["obj-a"], actionTargets: ["obj-t"] });
    const actions = rubrikActions("team-a", {
      kind: "loaded",
      list: rubrikList({
        namespaces: [row],
        objects: [
          rubrikObject(),
          rubrikObject({ id: "obj-t" }),
          rubrikObject({ id: "obj-z" }),
        ],
      }),
    });
    expect(actions?.namespace).toEqual(row);
    expect(actions?.objects.map((o) => o.id)).toEqual(["obj-a", "obj-t"]);
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
    "GET /api/v1/resources/namespaces?limit=500": () =>
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
  expect(link?.getAttribute("href")).toBe("/settings/general#rubrik");
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
  routes["GET /api/v1/resources/namespaces?limit=500"] = () =>
    fail(500, undefined, "boom");
  const view = await open();
  body().getByText("The namespaces could not be loaded.");
  expect(body().queryByRole("table")).toBeNull();
  view.unmount();
});

// ---------------------------------------------------------------------------
// Review fixes

const click = async (name: string) => {
  const { act, fireEvent } = await import("@/src/test-utils/render.tsx");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name }));
  });
  await settle();
};

test("AE6: an RSC failure reports the last fetch this page saw, never unprotected", async () => {
  // The server sends no connection to a non-admin, and no lastSuccessAt at
  // all today: the time comes from the last list the page loaded.
  const fetchedAt = hoursAgo(2);
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(rubrikList({ freshness: { fetchedAt, stale: false } }));
  const view = await open();
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("In compliance");
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    fail(400, "auth_failed", "RSC refused the client ID or secret");
  await click("Refresh");
  for (const ns of ["team-a", "team-b"]) {
    const text = cell(ns, "rubrik-cell")?.textContent ?? "";
    expect(text).toContain("RSC unavailable");
    expect(text).toContain("last fetched 2h ago");
  }
  expect(body().queryByText("Not protected")).toBeNull();
  view.unmount();
});

test("an RSC failure with no earlier answer says so, still never unprotected", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    fail(502, "unreachable", "RSC could not be reached");
  const view = await open();
  const text = cell("team-a", "rubrik-cell")?.textContent ?? "";
  expect(text).toContain("RSC unavailable");
  expect(text).toContain("no earlier answer to show");
  expect(body().queryByText("Not protected")).toBeNull();
  view.unmount();
});

test("a remote cluster whose Velero cannot be checked is an error, not not-installed", async () => {
  routes["GET /api/v1/velero/status"] = () =>
    ok({
      detected: false,
      reason: "unreachable",
      bslCount: 0,
      vslCount: 0,
      lastChecked: hoursAgo(0),
    });
  const view = await open();
  expect(cell("team-a", "velero-cell")?.textContent).toBe("Could not load");
  expect(body().queryByText("Velero not installed")).toBeNull();
  view.unmount();
  // discovery_missing does mean absent.
  routes["GET /api/v1/velero/status"] = () =>
    ok({
      detected: false,
      reason: "discovery_missing",
      bslCount: 0,
      vslCount: 0,
      lastChecked: hoursAgo(0),
    });
  resetRubrikStatusForTest();
  const again = await open();
  expect(cell("team-a", "velero-cell")?.textContent).toBe(
    "Velero not installed",
  );
  again.unmount();
});

test("the namespace list follows its continue token past the first page", async () => {
  routes["GET /api/v1/resources/namespaces?limit=500"] = () =>
    reply(200, {
      data: [{ metadata: { name: "team-a" } }],
      metadata: { total: 2, continue: "1" },
    });
  routes["GET /api/v1/resources/namespaces?limit=500&continue=1"] = () =>
    reply(200, {
      data: [{ metadata: { name: "team-z" } }],
      metadata: { total: 2 },
    });
  const view = await open();
  body().getByTestId("overview-team-a");
  body().getByTestId("overview-team-z");
  expect(body().queryByText(/cut short/)).toBeNull();
  view.unmount();
});

test("a namespace list cut short by a remote paging cap says so", async () => {
  routes["GET /api/v1/resources/namespaces?limit=500"] = () =>
    reply(200, {
      data: [{ metadata: { name: "team-a" } }],
      metadata: { total: 1, truncated: true },
    });
  const view = await open();
  body().getByText(/The namespace list was cut short/);
  view.unmount();
});

test("an empty backup list does not claim a namespace has no backup", async () => {
  routes["GET /api/v1/velero/backups"] = () => ok([]);
  const view = await open();
  expect(cell("team-a", "velero-cell")?.textContent).toBe(
    "No backups visible to you",
  );
  expect(body().queryByText("No backup")).toBeNull();
  view.unmount();
});

test("a backup list the user may not read says so, not that there are none", async () => {
  routes["GET /api/v1/velero/backups"] = () =>
    reply(200, { data: [], metadata: { total: 0, accessDenied: true } });
  const view = await open();
  expect(cell("team-a", "velero-cell")?.textContent).toBe(
    "You may not list Velero backups",
  );
  expect(body().queryByText("No backups visible to you")).toBeNull();
  expect(body().queryByText("No backup")).toBeNull();
  view.unmount();
});

test("a Velero denial is said once for the page, even with no rows to show it in", async () => {
  // Namespaces forbidden and no Rubrik rows: the table is empty, so only the
  // page-level note can say why Velero shows nothing.
  routes["GET /api/v1/resources/namespaces?limit=500"] = () =>
    fail(403, undefined, "forbidden");
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(rubrikList({ namespaces: [] }));
  routes["GET /api/v1/velero/backups"] = () =>
    reply(200, { data: [], metadata: { total: 0, accessDenied: true } });
  const view = await open();
  body().getByText("No namespaces to show on this cluster.");
  body().getByText(/You may not list Velero backups on this cluster/);
  view.unmount();
});

test("a backup still finishing in a PartiallyFailed sub-phase is not a failure yet", async () => {
  const cellNow = veleroCell("shop", {
    kind: "loaded",
    backups: [
      backup({
        name: "now",
        phase: "WaitingForPluginOperationsPartiallyFailed",
        completionTime: undefined,
        startTime: hoursAgo(0),
      }),
      backup({
        name: "finishing",
        phase: "FinalizingPartiallyFailed",
        startTime: hoursAgo(1),
      }),
      backup({ name: "earlier", completionTime: hoursAgo(5) }),
    ],
  });
  expect(cellNow).toMatchObject({
    kind: "protected",
    last: { name: "earlier" },
  });
});

test("no Rubrik column flashes in while the status loads with the provider off", async () => {
  let release: (r: Response) => void = () => {};
  routes["GET /api/v1/rubrik/status"] = () =>
    new Promise<Response>((resolve) => {
      release = resolve;
    }) as unknown as Response;
  const view = await open();
  // The base data are in; the status is not.
  body().getByTestId("overview-team-a");
  expect(body().queryByRole("columnheader", { name: /Rubrik/ })).toBeNull();
  const { act } = await import("@/src/test-utils/render.tsx");
  await act(async () => {
    release(fail(404, undefined, "404 page not found"));
  });
  await settle();
  expect(body().queryByRole("columnheader", { name: /Rubrik/ })).toBeNull();
  view.unmount();
});

test("a user who may not list namespaces sees the namespaces the providers show", async () => {
  routes["GET /api/v1/resources/namespaces?limit=500"] = () =>
    fail(403, undefined, "forbidden");
  routes["GET /api/v1/velero/backups"] = () => ok([]);
  const view = await open();
  body().getByText(/You may not list this cluster's namespaces/);
  // team-a comes from the Rubrik answer.
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("In compliance");
  expect(body().queryByText("The namespaces could not be loaded.")).toBeNull();
  view.unmount();
});

// ---------------------------------------------------------------------------
// SLA, compliance and writes in the Rubrik column

/** A list where team-a may be snapshotted and have its SLA changed. */
function writableList(): RubrikNamespaceList {
  return rubrikList({
    namespaces: [
      nsRow({
        canSnapshot: true,
        canChangeSla: true,
        actionTargets: ["obj-a"],
      }),
    ],
    objects: [rubrikObject({ canSnapshot: true, canChangeSla: true })],
  });
}

test("the Rubrik cell shows the SLA domain and an out-of-compliance badge", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(
      rubrikList({
        namespaces: [nsRow({ compliance: "out_of_compliance" })],
        objects: [rubrikObject()],
      }),
    );
  const view = await open();
  const text = cell("team-a", "rubrik-cell")?.textContent ?? "";
  expect(text).toContain("Out of compliance");
  expect(text).toContain("SLA Gold");
  expect(text).toContain("Last snapshot");
  view.unmount();
});

test("unknown compliance still reads protected", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(rubrikList({ namespaces: [nsRow({ compliance: "unknown" })] }));
  const view = await open();
  expect(cell("team-a", "rubrik-cell")?.textContent).toContain("Protected");
  view.unmount();
});

test("with writes off no write control shows, whatever the row allows", async () => {
  routes["GET /api/v1/rubrik/namespaces"] = () => ok(writableList());
  const view = await open();
  expect(body().queryByRole("button", { name: "Take snapshot" })).toBeNull();
  expect(body().queryByRole("button", { name: "Change SLA" })).toBeNull();
  view.unmount();
});

test("with writes on the row offers what its flags allow", async () => {
  routes["GET /api/v1/rubrik/status"] = () =>
    ok(status("mapped", { writesEnabled: true }));
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    ok(
      rubrikList({
        namespaces: [nsRow({ canSnapshot: true, actionTargets: ["obj-a"] })],
        objects: [rubrikObject({ canSnapshot: true })],
      }),
    );
  const view = await open();
  const row = body().getByTestId("overview-team-a");
  expect(row.textContent).toContain("Take snapshot");
  expect(row.textContent).not.toContain("Change SLA");
  view.unmount();
});

test("a snapshot taken from the overview shows its status and re-reads Rubrik in place", async () => {
  routes["GET /api/v1/rubrik/status"] = () =>
    ok(status("mapped", { writesEnabled: true }));
  routes["GET /api/v1/rubrik/namespaces"] = () => ok(writableList());
  const op: RubrikOperation = {
    id: "op-1",
    kind: "snapshot",
    clusterId: "local",
    namespace: "team-a",
    objectId: "obj-a",
    state: "succeeded",
    createdAt: hoursAgo(0),
    updatedAt: hoursAgo(0),
    finishedAt: hoursAgo(0),
  };
  routes["POST /api/v1/rubrik/namespaces/team-a/snapshots"] = () =>
    reply(201, { data: op });
  const view = await open();
  const reads = () =>
    calls.filter((c) => c === "GET /api/v1/rubrik/namespaces").length;
  expect(reads()).toBe(1);
  // Hold the re-read open, so the column is seen while it is in flight.
  routes["GET /api/v1/rubrik/namespaces"] = () =>
    new Promise<Response>(() => {}) as unknown as Response;
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Take snapshot" }));
  });
  await settle();
  await act(async () => {
    fireEvent.click(
      body()
        .getAllByRole("button", { name: "Take snapshot" })
        .at(-1) as HTMLElement,
    );
  });
  await settle();
  expect(calls).toContain("POST /api/v1/rubrik/namespaces/team-a/snapshots");
  const ops = body().getByLabelText("Rubrik operations for team-a");
  expect(ops.textContent).toContain("Succeeded");
  // The settled write reads Rubrik again, and the column keeps what it
  // showed meanwhile instead of going back to loading.
  expect(reads()).toBe(2);
  const text = cell("team-a", "rubrik-cell")?.textContent ?? "";
  expect(text).toContain("SLA Gold");
  expect(text).not.toContain("Loading");
  view.unmount();
});

test("a write's status does not follow the page to another cluster", async () => {
  const { selectedCluster } = await import("@/src/lib/cluster.ts");
  routes["GET /api/v1/rubrik/status"] = () =>
    ok(status("mapped", { writesEnabled: true }));
  routes["GET /api/v1/rubrik/namespaces"] = () => ok(writableList());
  routes["POST /api/v1/rubrik/namespaces/team-a/snapshots"] = () =>
    reply(201, {
      data: {
        id: "op-1",
        kind: "snapshot",
        clusterId: "local",
        namespace: "team-a",
        objectId: "obj-a",
        state: "succeeded",
        createdAt: hoursAgo(0),
        updatedAt: hoursAgo(0),
      },
    });
  const before = selectedCluster.value;
  const view = await open();
  try {
    await act(async () => {
      fireEvent.click(body().getByRole("button", { name: "Take snapshot" }));
    });
    await settle();
    await act(async () => {
      fireEvent.click(
        body()
          .getAllByRole("button", { name: "Take snapshot" })
          .at(-1) as HTMLElement,
      );
    });
    await settle();
    body().getByLabelText("Rubrik operations for team-a");
    await act(async () => {
      selectedCluster.value = "other-cluster";
    });
    await settle();
    expect(body().queryByLabelText("Rubrik operations for team-a")).toBeNull();
  } finally {
    await act(async () => {
      selectedCluster.value = before;
    });
    view.unmount();
  }
});
