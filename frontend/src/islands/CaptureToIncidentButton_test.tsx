/** @jsxImportSource preact */
import { afterAll, afterEach, expect, spyOn, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The diagnosis-to-incident entry point (U25b). Requests go through a fetch
 * stub that routes by method and path, records every call, and can hold a
 * response open so a second click lands while the first is in flight.
 * Navigation is observed through a spy on `location.assign`.
 */
GlobalRegistrator.register({ url: "http://localhost/" });

const {
  default: CaptureToIncidentButton,
  earliestObservedAt,
  resolveWindowStart,
} = await import("./CaptureToIncidentButton.tsx");

afterAll(() => {
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  path: string;
  cluster: string | null;
  body: unknown;
}

type Reply =
  | { status: number; body: unknown }
  | Promise<{
      status: number;
      body: unknown;
    }>;
type Route = (call: Call) => Reply | undefined;

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
let assigned: string[] = [];
let assignSpy: ReturnType<typeof spyOn> | null = null;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  assignSpy?.mockRestore();
  assignSpy = null;
  setAccessToken(null);
});

const json = (status: number, body: unknown) => ({ status, body });
const empty = json(200, { data: [], metadata: {} });

function stub(route: Route) {
  calls = [];
  assigned = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const headers = new Headers(init?.headers);
    const call: Call = {
      method: init?.method ?? "GET",
      path: url.pathname + url.search,
      cluster: headers.get("X-Cluster-ID"),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    };
    calls.push(call);
    const reply = (await route(call)) ?? json(404, { error: { code: 404 } });
    return new Response(JSON.stringify(reply.body), {
      status: reply.status,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof globalThis.fetch;
  assignSpy = spyOn(globalThis.location, "assign").mockImplementation(
    (to: string | URL) => {
      assigned.push(String(to));
    },
  );
  setAccessToken("test-token");
}

async function settle() {
  for (let i = 0; i < 10; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

async function mount(props: {
  clusterId?: string;
  windowStart?: string;
  kind?: string;
}) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <CaptureToIncidentButton
        clusterId={props.clusterId ?? "local"}
        namespace="team-a"
        kind={props.kind ?? "Pod"}
        name="web"
        windowStart={props.windowStart}
      />,
      host as HTMLElement,
    ),
  );
  await settle();
  return host;
}

function q(root: HTMLElement, testId: string): HTMLElement {
  const el = root.querySelector(`[data-testid="${testId}"]`);
  if (!el) throw new Error(`no ${testId}`);
  return el as HTMLElement;
}

async function click(el: HTMLElement) {
  act(() => el.click());
  await settle();
}

const incident = (over: Record<string, unknown>) => ({
  id: "inc-x",
  ownerId: "u1",
  clusterId: "local",
  title: "x",
  summary: "",
  status: "open",
  windowStart: "2026-10-06T00:00:00Z",
  retentionDays: 30,
  createdAt: "2026-10-06T00:00:00Z",
  updatedAt: "2026-10-06T00:00:00Z",
  role: "owner",
  canAnnotate: true,
  ...over,
});

const created = (id: string) =>
  json(201, { data: { incident: incident({ id }) } });
const captured = json(200, {
  data: {
    completeness: "complete",
    collectedAt: "2026-10-06T00:00:00Z",
    sources: [],
    collected: 3,
    inserted: 3,
    deduplicated: 0,
    dropped: 0,
  },
});

const creates = () =>
  calls.filter((c) => c.method === "POST" && c.path === "/api/v1/incidents");
const captures = () =>
  calls.filter((c) => c.method === "POST" && c.path.endsWith("/capture"));

// --- Window derivation ----------------------------------------------------------

test("earliestObservedAt picks the earliest valid observation", () => {
  expect(
    earliestObservedAt([
      { observedAt: "2026-10-06T10:05:00Z" },
      { ruleName: "no time" },
      { observedAt: "not a time" },
      { observedAt: "2026-10-06T09:59:30Z" },
      { observedAt: 42 },
    ]),
  ).toBe("2026-10-06T09:59:30.000Z");
});

test("earliestObservedAt is undefined when no check reports a time", () => {
  expect(earliestObservedAt([])).toBeUndefined();
  expect(earliestObservedAt([{ ruleName: "a" }, { observedAt: "" }])).toBe(
    undefined,
  );
});

test("resolveWindowStart keeps the diagnosis's start, else now minus an hour", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  expect(resolveWindowStart("2026-10-06T09:00:00.000Z", now)).toBe(
    "2026-10-06T09:00:00.000Z",
  );
  expect(resolveWindowStart(undefined, now)).toBe("2026-10-06T11:00:00.000Z");
});

// --- New incident flow ------------------------------------------------------------

test("capture into a new incident carries target and window, then opens it", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created("inc-1");
    if (c.path === "/api/v1/incidents/inc-1/capture") return captured;
  });
  const root = await mount({ windowStart: "2026-10-06T09:59:30.000Z" });
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-incident-dialog")).toBeTruthy();
  await click(q(root, "capture-to-new-incident"));

  expect(creates()).toHaveLength(1);
  const body = creates()[0].body as Record<string, string>;
  expect(body.windowStart).toBe("2026-10-06T09:59:30.000Z");
  expect(body.title).toBe("Pod/web in team-a");
  expect(creates()[0].cluster).toBe("local");

  expect(captures()).toHaveLength(1);
  expect(captures()[0].path).toBe("/api/v1/incidents/inc-1/capture");
  expect(captures()[0].cluster).toBe("local");
  expect(captures()[0].body).toEqual({
    namespace: "team-a",
    kind: "Pod",
    name: "web",
  });
  expect(assigned).toEqual(["/observability/incidents/inc-1"]);
});

test("without a diagnosis time the window starts an hour before the click", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created("inc-2");
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  const before = Date.now();
  await click(q(root, "capture-to-new-incident"));
  const after = Date.now();
  const start = Date.parse(
    (creates()[0].body as Record<string, string>).windowStart,
  );
  expect(start).toBeGreaterThanOrEqual(before - 3_600_000);
  expect(start).toBeLessThanOrEqual(after - 3_600_000);
});

test("a double click creates and captures exactly once", async () => {
  let release: () => void = () => {};
  const held = new Promise<{ status: number; body: unknown }>((resolve) => {
    release = () => resolve(created("inc-3"));
  });
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return held;
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  const button = q(root, "capture-to-new-incident");
  act(() => {
    button.click();
    button.click();
  });
  await settle();
  expect(button.getAttribute("aria-disabled")).toBe("true");
  await click(button);
  release();
  await settle();
  expect(creates()).toHaveLength(1);
  expect(captures()).toHaveLength(1);
  expect(assigned).toEqual(["/observability/incidents/inc-3"]);
});

test("an outcome-unknown capture is retried into the same incident", async () => {
  let attempt = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created("inc-4");
    attempt++;
    if (attempt === 1) {
      return json(503, {
        error: {
          code: 503,
          message: "outcome unknown",
          reason: "incident_capture_outcome_unknown",
        },
      });
    }
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("may or may not have been recorded");
  expect(root.textContent).toContain("Retrying is safe");
  expect(assigned).toEqual([]);
  const link = root.querySelector('a[href="/observability/incidents/inc-4"]');
  expect(link).not.toBeNull();

  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    "/api/v1/incidents/inc-4/capture",
    "/api/v1/incidents/inc-4/capture",
  ]);
  expect(assigned).toEqual(["/observability/incidents/inc-4"]);
});

test("a busy incident says so and stays in the dialog", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created("inc-5");
    return json(503, {
      error: { code: 503, message: "busy", reason: "incident_busy" },
    });
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("The incident is busy");
  expect(q(root, "capture-to-new-incident").getAttribute("aria-disabled")).toBe(
    "false",
  );
});

// --- Existing incident flow --------------------------------------------------------

test("existing incidents list only open ones the caller owns, and capture into the pick", async () => {
  stub((c) => {
    if (c.method === "GET" && c.path.startsWith("/api/v1/incidents?")) {
      return json(200, {
        data: [
          incident({ id: "mine-open", title: "Mine open" }),
          incident({
            id: "mine-closed",
            title: "Mine closed",
            status: "closed",
          }),
          incident({
            id: "shared",
            title: "Shared with me",
            role: "collaborator",
          }),
        ],
        metadata: {},
      });
    }
    if (c.path === "/api/v1/incidents/mine-open/capture") return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  const rows = root.querySelectorAll(
    '[data-testid="capture-to-existing-incident"]',
  );
  expect(rows).toHaveLength(1);
  expect(rows[0].textContent).toContain("Mine open");
  expect(root.textContent).not.toContain("Mine closed");
  expect(root.textContent).not.toContain("Shared with me");

  await click(rows[0] as HTMLElement);
  expect(creates()).toHaveLength(0);
  expect(captures()).toHaveLength(1);
  expect(captures()[0].path).toBe("/api/v1/incidents/mine-open/capture");
  expect(assigned).toEqual(["/observability/incidents/mine-open"]);
});

// --- Inactive states ------------------------------------------------------------------

test("no database: the button is inactive with an explanation and opens nothing", async () => {
  stub(() =>
    json(503, {
      error: {
        code: 503,
        message: "no database",
        reason: "incident_persistence_unavailable",
      },
    }),
  );
  const root = await mount({});
  const trigger = q(root, "capture-to-incident");
  expect(trigger.getAttribute("aria-disabled")).toBe("true");
  expect(trigger.getAttribute("title")).toContain("no database");
  const probes = calls.length;
  await click(trigger);
  expect(root.querySelector('[data-testid="capture-to-incident-dialog"]')).toBe(
    null,
  );
  expect(calls).toHaveLength(probes);
});

test("a remote cluster: inactive, local-cluster only, and no request at all", async () => {
  stub(() => empty);
  const root = await mount({ clusterId: "c-remote" });
  const trigger = q(root, "capture-to-incident");
  expect(trigger.getAttribute("aria-disabled")).toBe("true");
  expect(trigger.getAttribute("title")).toContain("local-cluster only");
  await click(trigger);
  expect(root.querySelector('[data-testid="capture-to-incident-dialog"]')).toBe(
    null,
  );
  expect(calls).toHaveLength(0);
});

test("a server-side remote refusal reads as local-cluster only", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created("inc-6");
    return json(400, {
      error: {
        code: 400,
        message: "remote",
        reason: "remote_capture_unsupported",
      },
    });
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("local-cluster only");
  expect(assigned).toEqual([]);
});

test("a kind capture does not support is inactive", async () => {
  stub(() => empty);
  const root = await mount({ kind: "ConfigMap" });
  const trigger = q(root, "capture-to-incident");
  expect(trigger.getAttribute("aria-disabled")).toBe("true");
  expect(trigger.getAttribute("title")).toContain("ConfigMap");
});
