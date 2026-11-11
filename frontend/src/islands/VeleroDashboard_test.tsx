import { afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
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
  deleteStatus?: number;
  assurance?: { status: number; body?: unknown };
}
let api: Api = {};

function fail(status: number) {
  return new Response(
    JSON.stringify({ error: { code: status, message: "x" } }),
    { status, headers: { "Content-Type": "application/json" } },
  );
}

beforeEach(() => {
  calls = [];
  alerts = [];
  confirmMessages = [];
  confirmAnswer = true;
  api = { detected: true };
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
    if (api.failList) return fail(500);
    if (url.endsWith("/velero/status")) {
      return json({
        detected: api.detected ?? true,
        bslCount: 1,
        vslCount: 0,
        lastChecked: "2026-01-01T00:00:00Z",
      });
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
});

const listCalls = () => calls.filter((c) => c.method === "GET");

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
