import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type {
  RubrikNamespaceDetail,
  RubrikNamespaceList,
  RubrikObject,
  RubrikStatus,
} from "@/lib/rubrik-types.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The Velero dashboard loads status, backups, restores, schedules and
 * locations together. It explains a missing Velero, lists the tab's items
 * with a search box, opens the matching wizard, refreshes on demand, and
 * deletes a backup or schedule after a browser confirm (reloading after).
 * The overview adds counts, a health donut and a read-only assurance strip.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
const bunConfirm = globalThis.confirm;
const bunAlert = globalThis.alert;
setupDom({ browser: true });

const { default: VeleroDashboard } = await import("./VeleroDashboard.tsx");
const { resetRubrikStatusForTest } = await import("@/src/lib/rubrik-status.ts");

class QuietWebSocket {
  static readonly OPEN = 1;
  readyState = 0;
  send() {}
  close() {}
  addEventListener() {}
  removeEventListener() {}
}

interface Call {
  method: string;
  url: string;
}
let calls: Call[] = [];
let confirmAnswer = true;
let confirmMessages: string[] = [];
let alerts: string[] = [];

const backup = (name: string, extra: Record<string, unknown> = {}) => ({
  name,
  namespace: "velero",
  phase: "Completed",
  snapshotVolumes: true,
  itemsBackedUp: 5,
  totalItems: 5,
  warnings: 0,
  errors: 0,
  startTime: "2026-01-01T00:00:00Z",
  ...extra,
});
const restore = (name: string, backupName: string) => ({
  name,
  namespace: "velero",
  phase: "Completed",
  backupName,
  itemsRestored: 1,
  totalItems: 1,
  warnings: 0,
  errors: 0,
});
const schedule = (name: string) => ({
  name,
  namespace: "velero",
  phase: "Enabled",
  schedule: "0 2 * * *",
  paused: false,
});

interface Api {
  detected?: boolean;
  backups?: unknown[];
  restores?: unknown[];
  schedules?: unknown[];
  failList?: boolean;
  /** Lists answered as denied: an empty list with metadata.accessDenied. */
  denied?: Array<"backups" | "restores" | "schedules">;
  deleteStatus?: number;
  assurance?: { status: number; body?: unknown };
  /** Rubrik's answers; absent means the provider is off (every path 404). */
  rubrik?: {
    status: unknown;
    list?: RubrikNamespaceList;
    /** The list read fails with this status and reason. */
    listFail?: { status: number; reason: string };
    detail?: RubrikNamespaceDetail;
  };
}
let api: Api = {};

function fail(status: number, reason?: string) {
  return new Response(
    JSON.stringify({ error: { code: status, message: "x", reason } }),
    { status, headers: { "Content-Type": "application/json" } },
  );
}

beforeEach(() => {
  calls = [];
  alerts = [];
  confirmMessages = [];
  confirmAnswer = true;
  api = { detected: true };
  resetRubrikStatusForTest();
  globalThis.WebSocket = QuietWebSocket as unknown as typeof WebSocket;
  globalThis.confirm = ((m: string) => {
    confirmMessages.push(m);
    return confirmAnswer;
  }) as typeof confirm;
  globalThis.alert = ((m: string) => {
    alerts.push(m);
  }) as typeof alert;
  setAccessToken("t");
  window.history.replaceState(null, "", "/");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    calls.push({ method, url });
    if (method === "DELETE") {
      return api.deleteStatus ? fail(api.deleteStatus) : json({});
    }
    if (url.includes("/velero/assurance/status")) {
      const a = api.assurance ?? { status: 500 };
      return a.status === 200 ? json(a.body) : fail(a.status);
    }
    if (url.includes("/v1/rubrik/")) {
      const r = api.rubrik;
      if (!r) return fail(404);
      if (url.endsWith("/rubrik/status")) return json(r.status);
      if (url.endsWith("/rubrik/namespaces")) {
        return r.listFail
          ? fail(r.listFail.status, r.listFail.reason)
          : json(r.list);
      }
      if (/\/rubrik\/namespaces\/[^/]+$/.test(url) && r.detail) {
        return json(r.detail);
      }
      return fail(404);
    }
    if (api.failList) return fail(500);
    if (url.endsWith("/velero/status")) {
      return json({
        detected: api.detected ?? true,
        bslCount: 1,
        vslCount: 0,
        lastChecked: "2026-01-01T00:00:00Z",
      });
    }
    for (const list of api.denied ?? []) {
      if (url.endsWith(`/velero/${list}`)) {
        return new Response(
          JSON.stringify({
            data: [],
            metadata: { total: 0, accessDenied: true },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
    }
    if (url.endsWith("/velero/backups")) return json(api.backups ?? []);
    if (url.endsWith("/velero/restores")) return json(api.restores ?? []);
    if (url.endsWith("/velero/schedules")) return json(api.schedules ?? []);
    if (url.endsWith("/velero/locations")) {
      return json({
        backupStorageLocations: [
          {
            name: "default-bsl",
            namespace: "velero",
            provider: "aws",
            bucket: "b",
            phase: "Available",
            default: true,
          },
        ],
        volumeSnapshotLocations: [],
      });
    }
    return json({});
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  // Drop the quiet socket from the shared ws store; left in CONNECTING, it
  // stops a later test file's subscription from opening its own socket.
  disconnectWS();
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  globalThis.confirm = bunConfirm;
  globalThis.alert = bunAlert;
  setAccessToken(null);
  window.history.replaceState(null, "", "/");
  resetRubrikStatusForTest();
});

const listCalls = () =>
  calls.filter((c) => c.method === "GET" && c.url.includes("/v1/velero/"));

test("loads all five resources and lists backups with a count", async () => {
  api.backups = [backup("nightly-1"), backup("nightly-2", { errors: 2 })];
  const view = render(<VeleroDashboard initialTab="backups" />);
  expect(view.container.textContent).toContain("Loading…");
  await settle();
  const urls = listCalls().map((c) => c.url);
  for (const part of [
    "status",
    "backups",
    "restores",
    "schedules",
    "locations",
  ]) {
    expect(urls.some((u) => u.endsWith(`/v1/velero/${part}`))).toBe(true);
  }
  const text = view.container.textContent ?? "";
  expect(view.container.querySelector("h1")?.textContent).toBe("Backups");
  expect(text).toContain("2 backups");
  expect(text).toContain("nightly-1");
  expect(text).toContain("nightly-2");
  expect(text).toContain("0W/2E");
});

test("a single item is not pluralised", async () => {
  api.backups = [backup("only")];
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain("1 backup");
  expect(view.container.textContent).not.toContain("1 backups");
});

test("explains that Velero is not installed", async () => {
  api.detected = false;
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain("Velero Not Detected");
  expect(view.container.querySelector("input")).toBeNull();
  expect(body().queryByRole("button", { name: /New Backup/ })).not.toBeNull();
});

test("shows an error when the data cannot be loaded", async () => {
  api.failList = true;
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain("Failed to load Velero data");
  expect(view.container.querySelector("input")).toBeNull();
});

test("the search box narrows backups by name or schedule", async () => {
  api.backups = [
    backup("alpha"),
    backup("beta", { scheduleName: "weekly" }),
    backup("gamma"),
  ];
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  const input = view.container.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value: "WEEK" } });
  });
  expect(view.container.textContent).toContain("beta");
  expect(view.container.textContent).not.toContain("alpha");
  expect(view.container.textContent).not.toContain("gamma");
  await act(async () => {
    fireEvent.change(input, { target: { value: "" } });
  });
  expect(view.container.textContent).toContain("alpha");
});

test("restores and schedules tabs list their items and search them", async () => {
  api.restores = [restore("r-1", "b-a"), restore("r-2", "b-b")];
  api.schedules = [schedule("daily"), schedule("hourly")];
  const restores = render(<VeleroDashboard initialTab="restores" />);
  await settle();
  expect(restores.container.querySelector("h1")?.textContent).toBe("Restores");
  expect(restores.container.textContent).toContain("2 restores");
  const input = restores.container.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value: "b-b" } });
  });
  expect(restores.container.textContent).toContain("r-2");
  expect(restores.container.textContent).not.toContain("r-1");
  restores.unmount();

  const schedules = render(<VeleroDashboard initialTab="schedules" />);
  await settle();
  expect(schedules.container.querySelector("h1")?.textContent).toBe(
    "Schedules",
  );
  expect(schedules.container.textContent).toContain("2 schedules");
  const sInput = schedules.container.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.change(sInput, { target: { value: "hour" } });
  });
  expect(schedules.container.textContent).toContain("hourly");
  expect(schedules.container.textContent).not.toContain("daily");
});

test("each tab's create button opens its own wizard", async () => {
  api.backups = [backup("a")];
  for (const [tab, label, title] of [
    ["backups", "New Backup", "Create Backup"],
    ["restores", "New Restore", "Create Restore"],
    ["schedules", "New Schedule", "Create Schedule"],
  ] as const) {
    const view = render(<VeleroDashboard initialTab={tab} />);
    await settle();
    expect(document.body.textContent).not.toContain(title);
    await act(async () => {
      fireEvent.click(body().getByRole("button", { name: new RegExp(label) }));
    });
    expect(document.body.textContent).toContain(title);
    view.unmount();
  }
});

test("?action=create opens the wizard for the tab", async () => {
  window.history.replaceState(null, "", "/backup/schedules?action=create");
  render(<VeleroDashboard initialTab="schedules" />);
  await settle();
  expect(document.body.textContent).toContain("Create Schedule");
});

test("Refresh reloads the data", async () => {
  api.backups = [backup("a")];
  render(<VeleroDashboard initialTab="backups" />);
  await settle();
  const before = listCalls().length;
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  });
  await settle();
  expect(listCalls().length).toBe(before * 2);
  body().getByRole("button", { name: "Refresh" });
});

test("deleting a backup asks first, then sends DELETE and reloads", async () => {
  api.backups = [backup("nightly-1")];
  render(<VeleroDashboard initialTab="backups" />);
  await settle();
  const before = listCalls().length;

  confirmAnswer = false;
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Delete" }));
  });
  await settle();
  expect(confirmMessages[0]).toBe(
    'Delete backup "nightly-1"? This will remove the backup data.',
  );
  expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);

  confirmAnswer = true;
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Delete" }));
  });
  await settle();
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes).toHaveLength(1);
  expect(deletes[0]?.url).toContain("/v1/velero/backups/velero/nightly-1");
  expect(listCalls().length).toBe(before * 2);
  expect(alerts).toEqual([]);
});

test("a failed backup delete alerts and leaves the button usable", async () => {
  api.backups = [backup("nightly-1")];
  api.deleteStatus = 500;
  render(<VeleroDashboard initialTab="backups" />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Delete" }));
  });
  await settle();
  expect(alerts).toEqual(["Failed to delete backup"]);
  expect(body().getByRole("button", { name: "Delete" })).toBeTruthy();
});

test("deleting a schedule asks first, then sends DELETE and reloads", async () => {
  api.schedules = [schedule("daily")];
  render(<VeleroDashboard initialTab="schedules" />);
  await settle();
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Delete" }));
  });
  await settle();
  expect(confirmMessages[0]).toBe('Delete schedule "daily"?');
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes).toHaveLength(1);
  expect(deletes[0]?.url).toContain("/v1/velero/schedules/velero/daily");
});

test("the overview shows counts, backup health, recent backups and locations", async () => {
  api.backups = [
    backup("ok-1"),
    backup("bad-1", { phase: "Failed" }),
    backup("ok-2"),
  ];
  api.restores = [restore("r-1", "ok-1")];
  api.schedules = [schedule("daily"), schedule("hourly")];
  api.assurance = {
    status: 200,
    body: {
      enabled: true,
      collection: "ok",
      collectionSource: "this_replica",
      policyCount: 1,
      open: { total: 1, byCondition: {} },
    },
  };
  const view = render(<VeleroDashboard initialTab="overview" />);
  await settle();
  await settle();
  const text = view.container.textContent ?? "";
  expect(view.container.querySelector("h1")?.textContent).toBe(
    "Backup & Restore",
  );
  expect(text).toContain("Backup Health");
  expect(text).toContain("Completed");
  expect(text).toContain("Recent Backups");
  expect(text).toContain("ok-1");
  expect(text).toContain("Backup Storage Locations");
  expect(text).toContain("default-bsl");
  expect(
    view.container.querySelector('input[placeholder="Search…"]'),
  ).toBeNull();
  expect(
    view.container.querySelector('[data-testid="velero-assurance-open-count"]')
      ?.textContent,
  ).toBe("1 open exception");
});

test("the assurance strip hides on 501 and shows a state word on 403", async () => {
  api.assurance = { status: 501 };
  const hidden = render(<VeleroDashboard initialTab="overview" />);
  await settle();
  await settle();
  expect(
    hidden.container.querySelector('[data-testid="velero-assurance-strip"]'),
  ).toBeNull();
  hidden.unmount();

  api.assurance = { status: 403 };
  const forbidden = render(<VeleroDashboard initialTab="overview" />);
  await settle();
  await settle();
  const state = forbidden.container.querySelector(
    '[data-testid="velero-assurance-state"]',
  );
  expect(state?.getAttribute("data-state")).toBe("forbidden");
  expect(
    forbidden.container.querySelector(
      '[data-testid="velero-assurance-honesty"]',
    ),
  ).not.toBeNull();
});

test("a tab whose list is denied says so instead of showing an empty table", async () => {
  api.denied = ["backups"];
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(body().getByTestId("velero-denied-note").textContent).toBe(
    "You may not list Velero backups on this cluster.",
  );
  expect(view.container.textContent).not.toContain("0 backups");
  expect(view.container.textContent).toContain("Not visible to you");
  expect(view.container.querySelector("input[type=search], input")).toBeNull();
});

test("the overview names every denied list, so a zero count is not read as none", async () => {
  api.denied = ["backups", "schedules"];
  api.restores = [restore("r1", "b1")];
  const view = render(<VeleroDashboard initialTab="overview" />);
  await settle();
  expect(body().getByTestId("velero-denied-note").textContent).toBe(
    "You may not list Velero backups and schedules on this cluster, so the counts below leave them out.",
  );
  // No tile, bar or donut claims a zero for a denied list.
  const tiles = (href: string) =>
    view.container.querySelectorAll(`a[href="${href}"]`).length;
  expect(tiles("/backup/backups")).toBe(0);
  expect(tiles("/backup/schedules")).toBe(0);
  expect(tiles("/backup/restores")).toBe(1);
  expect(body().getByTestId("velero-health-denied").textContent).toContain(
    "Not visible to you",
  );
  expect(view.container.querySelector('[aria-label="No backups"]')).toBeNull();
  const resources = view.container.textContent ?? "";
  expect(resources).toContain("Restores");
  expect(resources).not.toContain("Schedules");
});

test("a list that becomes readable on Refresh drops its denial note", async () => {
  api.denied = ["backups"];
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  body().getByTestId("velero-denied-note");
  api.denied = [];
  api.backups = [backup("now-visible")];
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  });
  await settle();
  expect(body().queryByTestId("velero-denied-note")).toBeNull();
  expect(view.container.textContent).toContain("now-visible");
  expect(view.container.textContent).toContain("1 backup");
});

test("a permitted empty list shows no denial note", async () => {
  render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(body().queryByTestId("velero-denied-note")).toBeNull();
});

// ---------------------------------------------------------------------------
// Rubrik objects on the Backups page

function rubrikStatusOf(
  state = "mapped",
  over: Partial<RubrikStatus> = {},
): RubrikStatus {
  return {
    enabled: true,
    preview: true,
    writesEnabled: false,
    cluster: { id: "local", state: state as RubrikStatus["cluster"]["state"] },
    ...over,
  };
}

function rubrikObject(over: Partial<RubrikObject> = {}): RubrikObject {
  return {
    family: "namespace",
    id: "obj-shop",
    name: "shop",
    coverage: { namespaces: ["shop"] },
    configuredSla: { id: "sla-gold", name: "Gold" },
    effectiveSla: { id: "sla-gold", name: "Gold" },
    compliance: "in_compliance",
    newestSnapshot: "2026-01-01T00:00:00Z",
    mayHoldClusterScoped: false,
    canSnapshot: false,
    canChangeSla: false,
    canRestore: false,
    ...over,
  };
}

function rubrikListOf(objects: RubrikObject[] = [rubrikObject()]) {
  return {
    noAccess: false,
    namespaces: objects.map((o) => ({
      namespace: o.name,
      protected: true,
      compliance: o.compliance,
      objectIds: [o.id],
      actionTargets: [o.id],
      canSnapshot: false,
      canChangeSla: false,
      canRestore: false,
    })),
    objects,
    freshness: { fetchedAt: new Date().toISOString(), stale: false },
  } satisfies RubrikNamespaceList;
}

const rubrikReads = () =>
  calls.filter(
    (c) => c.method === "GET" && c.url.endsWith("/rubrik/namespaces"),
  ).length;

test("Rubrik objects are listed with the Velero backups, a provider each", async () => {
  api.backups = [backup("nightly-1")];
  api.rubrik = { status: rubrikStatusOf(), list: rubrikListOf() };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  const text = view.container.textContent ?? "";
  expect(text).toContain("1 backup · 1 Rubrik object");
  expect(text).toContain("nightly-1");
  expect(text).toContain("SLA Gold");
  expect(text).toContain("In compliance");
  const providers = body()
    .getAllByRole("row")
    .slice(1)
    .map(
      (r: HTMLElement) => r.querySelectorAll('[role="cell"]')[1]?.textContent,
    );
  expect(providers).toEqual(["Velero", "Rubrik"]);
});

test("Rubrik rows show on a cluster without Velero", async () => {
  api.detected = false;
  // Whatever the backups endpoint says, Velero is not there to list.
  api.backups = [backup("ghost")];
  api.rubrik = { status: rubrikStatusOf(), list: rubrikListOf() };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain("Velero Not Detected");
  expect(view.container.textContent).toContain("SLA Gold");
  expect(view.container.textContent).not.toContain("ghost");
  expect(view.container.querySelector("input")).not.toBeNull();
});

test("Rubrik rows show when Velero's backups are denied, and no Velero row does", async () => {
  api.backups = [backup("hidden")];
  api.denied = ["backups"];
  api.rubrik = { status: rubrikStatusOf(), list: rubrikListOf() };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  body().getByTestId("velero-denied-note");
  expect(view.container.textContent).toContain("SLA Gold");
  expect(view.container.textContent).not.toContain("hidden");
});

test("the search box narrows Rubrik rows by name or SLA", async () => {
  api.rubrik = {
    status: rubrikStatusOf(),
    list: rubrikListOf([
      rubrikObject(),
      rubrikObject({
        id: "obj-cart",
        name: "cart",
        effectiveSla: { id: "sla-silver", name: "Silver" },
      }),
    ]),
  };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  const input = view.container.querySelector("input") as HTMLInputElement;
  await act(async () => {
    fireEvent.change(input, { target: { value: "SILVER" } });
  });
  const text = view.container.textContent ?? "";
  expect(text).toContain("cart");
  expect(text).not.toContain("SLA Gold");
});

test("Snapshots opens the Rubrik detail; closing it re-reads the rows in place", async () => {
  const object = rubrikObject();
  const list = rubrikListOf([object]);
  api.rubrik = {
    status: rubrikStatusOf(),
    list,
    detail: {
      namespace: list.namespaces[0],
      objects: [
        {
          ...object,
          snapshots: [{ id: "s-1", time: "2026-01-01T00:00:00Z" }],
          jobs: [],
        },
      ],
      freshness: { fetchedAt: new Date().toISOString(), stale: false },
    },
  };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(rubrikReads()).toBe(1);
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Snapshots of shop" }));
  });
  await settle();
  expect(calls.some((c) => c.url.endsWith("/v1/rubrik/namespaces/shop"))).toBe(
    true,
  );
  const panel = body().getByRole("dialog", { name: "Rubrik details for shop" });
  expect(panel.textContent).toContain("Available");
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Close" }));
  });
  await settle();
  expect(body().queryByRole("dialog")).toBeNull();
  expect(rubrikReads()).toBe(2);
  expect(view.container.textContent).toContain("SLA Gold");
});

test("an RSC failure says so above the backups", async () => {
  api.backups = [backup("nightly-1")];
  api.rubrik = {
    status: rubrikStatusOf(),
    listFail: { status: 503, reason: "unreachable" },
  };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain(
    "Rubrik: RSC could not be reached",
  );
  expect(view.container.textContent).toContain("nightly-1");
});

test("an unset-up cluster says so, with the setup link for admins", async () => {
  api.rubrik = { status: rubrikStatusOf("not_configured") };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain(
    "Rubrik is not set up for this cluster.",
  );
  expect(
    body().getByRole("link", { name: "Set up Rubrik" }).getAttribute("href"),
  ).toBe("/settings/general#rubrik");
});

test("a status without the cluster's state reads as Rubrik not loaded", async () => {
  api.rubrik = {
    status: { enabled: true, preview: true, writesEnabled: false },
  };
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).toContain(
    "The Rubrik data could not be loaded.",
  );
});

test("with Rubrik off the Backups page shows no Rubrik row or note", async () => {
  api.backups = [backup("nightly-1")];
  const view = render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(view.container.textContent).not.toContain("Rubrik");
  expect(view.container.textContent).toContain("1 backup");
});

test("Refresh reads Rubrik again too", async () => {
  api.rubrik = { status: rubrikStatusOf(), list: rubrikListOf() };
  render(<VeleroDashboard initialTab="backups" />);
  await settle();
  expect(rubrikReads()).toBe(1);
  await act(async () => {
    fireEvent.click(body().getByRole("button", { name: "Refresh" }));
  });
  await settle();
  expect(rubrikReads()).toBe(2);
});
