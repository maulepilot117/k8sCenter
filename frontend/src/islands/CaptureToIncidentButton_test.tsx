/** @jsxImportSource preact */
import { afterAll, afterEach, expect, spyOn, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { ApiError, setAccessToken } from "@/lib/api.ts";

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
const { currentUserId, fetchCurrentUser, logout } = await import(
  "@/lib/auth.ts"
);
const { createRefusedForGood } = await import(
  "@/src/components/incidents/errors.ts"
);

/**
 * Signs `id` in, as the top bar's /auth/me load does, through a fetch of its
 * own so no test's recorded calls change.
 */
async function signIn(id: string) {
  const previous = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({
        data: {
          user: {
            id,
            username: id,
            provider: "local",
            kubernetesUsername: id,
            kubernetesGroups: [],
            roles: [],
          },
          rbac: {},
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )) as unknown as typeof globalThis.fetch;
  try {
    await fetchCurrentUser();
  } finally {
    globalThis.fetch = previous;
  }
}

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
  globalThis.sessionStorage.clear();
});

/**
 * Incident ids are UUIDs (incident-api refuses anything else in a path), so
 * each readable test name maps to a stable UUID of its own.
 */
const uuidNames = new Map<string, string>();
function uuidFor(name: string): string {
  let id = uuidNames.get(name);
  if (!id) {
    id = `00000000-0000-4000-8000-${String(uuidNames.size + 1).padStart(12, "0")}`;
    uuidNames.set(name, id);
  }
  return id;
}

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
    // Honour the abort signal like real fetch: a request aborted before or
    // while it is in flight rejects with an AbortError.
    const signal = init?.signal;
    const aborted = () =>
      new DOMException("The operation was aborted.", "AbortError");
    if (signal?.aborted) throw aborted();
    const answer = Promise.resolve(route(call));
    const reply =
      (signal
        ? await Promise.race([
            answer,
            new Promise<never>((_, reject) =>
              signal.addEventListener("abort", () => reject(aborted()), {
                once: true,
              }),
            ),
          ])
        : await answer) ?? json(404, { error: { code: 404 } });
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

interface Props {
  clusterId?: string;
  windowStart?: string;
  kind?: string;
  /** Who is signed in; "u1" unless a test says otherwise. */
  user?: string;
}

/** Renders (or re-renders, with new props) into the current host. */
async function show(props: Props) {
  const user = props.user ?? "u1";
  if (currentUserId() !== user) await signIn(user);
  if (!host) {
    host = document.createElement("div");
    document.body.appendChild(host);
  }
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
  return host as HTMLElement;
}

const mount = show;

/** Unmounts the island and drops the host, as a Re-scan or navigation does. */
function unmount() {
  if (!host) return;
  act(() => render(null, host as HTMLElement));
  host.remove();
  host = null;
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
  id: uuidFor("inc-x"),
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
    if (c.path === "/api/v1/incidents") return created(uuidFor("inc-1"));
    if (c.path === `/api/v1/incidents/${uuidFor("inc-1")}/capture`)
      return captured;
  });
  const root = await mount({ windowStart: "2026-10-06T09:59:30.000Z" });
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-incident-dialog")).toBeTruthy();
  await click(q(root, "capture-to-new-incident"));

  expect(creates()).toHaveLength(1);
  const body = creates()[0].body as Record<string, string>;
  expect(body.windowStart).toBe("2026-10-06T09:59:30.000Z");
  expect(body.title).toBe("Pod/web in team-a");
  expect(body.clientRequestId).toMatch(UUID);
  expect(creates()[0].cluster).toBe("local");

  expect(captures()).toHaveLength(1);
  expect(captures()[0].path).toBe(
    `/api/v1/incidents/${uuidFor("inc-1")}/capture`,
  );
  expect(captures()[0].cluster).toBe("local");
  expect(captures()[0].body).toEqual({
    namespace: "team-a",
    kind: "Pod",
    name: "web",
  });
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-1")}`]);
});

test("without a diagnosis time the window starts an hour before the click", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created(uuidFor("inc-2"));
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
    release = () => resolve(created(uuidFor("inc-3")));
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
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-3")}`]);
});

test("an outcome-unknown capture is retried into the same incident", async () => {
  let attempt = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created(uuidFor("inc-4"));
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
  const link = root.querySelector(
    `a[href="/observability/incidents/${uuidFor("inc-4")}"]`,
  );
  expect(link).not.toBeNull();

  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("inc-4")}/capture`,
    `/api/v1/incidents/${uuidFor("inc-4")}/capture`,
  ]);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-4")}`]);
});

test("a busy incident says so and stays in the dialog", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === "/api/v1/incidents") return created(uuidFor("inc-5"));
    return json(503, {
      error: { code: 503, message: "busy", reason: "incident_busy" },
    });
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("Capture did not run");
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
          incident({ id: uuidFor("mine-open"), title: "Mine open" }),
          incident({
            id: uuidFor("mine-closed"),
            title: "Mine closed",
            status: "closed",
          }),
          incident({
            id: uuidFor("shared"),
            title: "Shared with me",
            role: "collaborator",
          }),
        ],
        metadata: {},
      });
    }
    if (c.path === `/api/v1/incidents/${uuidFor("mine-open")}/capture`)
      return captured;
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
  expect(captures()[0].path).toBe(
    `/api/v1/incidents/${uuidFor("mine-open")}/capture`,
  );
  expect(assigned).toEqual([
    `/observability/incidents/${uuidFor("mine-open")}`,
  ]);
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
    if (c.path === "/api/v1/incidents") return created(uuidFor("inc-6"));
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
  expect(root.textContent).toContain("on the local cluster only");
  expect(assigned).toEqual([]);
});

test("a kind capture does not support is inactive", async () => {
  stub(() => empty);
  const root = await mount({ kind: "ConfigMap" });
  const trigger = q(root, "capture-to-incident");
  expect(trigger.getAttribute("aria-disabled")).toBe("true");
  expect(trigger.getAttribute("title")).toContain("ConfigMap");
});

// --- Helpers for the retry, remount and storage cases ------------------------------------

/** A response the test releases by hand. */
function deferred(reply: { status: number; body: unknown }) {
  let release: () => void = () => {};
  const promise = new Promise<{ status: number; body: unknown }>((resolve) => {
    release = () => resolve(reply);
  });
  return { promise, release: () => release() };
}

const UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const listOf = (items: unknown[], cont?: string) =>
  json(200, { data: items, metadata: cont ? { continue: cont } : {} });
const isProbe = (c: Call) =>
  c.method === "GET" && c.path === "/api/v1/incidents?limit=1";
const isList = (c: Call) =>
  c.method === "GET" && c.path.startsWith("/api/v1/incidents?limit=50");
const isCreate = (c: Call) =>
  c.method === "POST" && c.path === "/api/v1/incidents";
const requestIdOf = (c: Call) =>
  (c.body as { clientRequestId?: string }).clientRequestId;
const TITLE = "Pod/web in team-a";
const SUMMARY = "Opened from the diagnosis of Pod team-a/web.";
const RETRY_LABEL = "Retry capture into the new incident";
const NEW_LABEL = "Capture into a new incident";
const busyReply = json(503, {
  error: { code: 503, message: "busy", reason: "incident_busy" },
});
const networkError = () => Promise.reject(new TypeError("Failed to fetch"));

/**
 * The server's create idempotency (U25c), as the handler implements it: the
 * first create with a request id makes an incident; a later one with the
 * same id and the same title, summary and window replays it (200), and one
 * with a different payload is 409 client_request_id_conflict. `made` maps
 * request id to incident id, so a test can count incidents; `edit` changes
 * a stored incident's payload, as an operator editing it would.
 */
function idempotentServer() {
  const made = new Map<string, string>();
  const payloads = new Map<string, string>();
  const payloadOf = (c: Call) => {
    const b = c.body as Record<string, unknown>;
    return JSON.stringify([b.title, b.summary, b.windowStart, b.windowEnd]);
  };
  const create = (c: Call) => {
    const rid = requestIdOf(c) ?? `anon-${made.size}`;
    const existing = made.get(rid);
    if (existing) {
      if (payloads.get(rid) !== payloadOf(c)) return conflictReply;
      return json(200, { data: { incident: incident({ id: existing }) } });
    }
    const id = uuidFor(`inc-${made.size + 1}`);
    made.set(rid, id);
    payloads.set(rid, payloadOf(c));
    return created(id);
  };
  const edit = (rid: string) => payloads.set(rid, "edited");
  return { made, create, edit };
}

const conflictReply = json(409, {
  error: {
    code: 409,
    message: "conflict",
    reason: "client_request_id_conflict",
  },
});

const PENDING_PREFIX = "kubecenter.capture-pending:";
const pendingStorageKey = (cluster: string, user = "u1") =>
  `${PENDING_PREFIX}${user}|${cluster}|team-a|Pod|web`;
/** Seeds a pending record for the target on `cluster`. */
const seed = (cluster: string, over: Record<string, unknown> = {}) =>
  globalThis.sessionStorage.setItem(
    pendingStorageKey(cluster),
    JSON.stringify({
      requestId: "11111111-1111-4111-8111-111111111111",
      title: TITLE,
      summary: SUMMARY,
      windowStart: "2026-10-06T08:00:00.000Z",
      createdAt: Date.now(),
      ...over,
    }),
  );
const stored = (cluster: string, user = "u1") =>
  JSON.parse(
    globalThis.sessionStorage.getItem(pendingStorageKey(cluster, user)) ??
      "null",
  );

let restoreStorage: (() => void) | null = null;

/**
 * Swaps sessionStorage for a wrapper whose listed methods throw; the others
 * delegate to the real storage.
 */
function breakStorage(...methods: ("getItem" | "setItem" | "removeItem")[]) {
  const real = globalThis.sessionStorage;
  const desc = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  const fail = () => {
    throw new DOMException("blocked", "SecurityError");
  };
  const fake = {
    getItem: (k: string) => real.getItem(k),
    setItem: (k: string, v: string) => real.setItem(k, v),
    removeItem: (k: string) => real.removeItem(k),
    clear: () => real.clear(),
    key: (i: number) => real.key(i),
    get length() {
      return real.length;
    },
  };
  for (const m of methods) fake[m] = fail;
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    value: fake,
  });
  restoreStorage = () => {
    if (desc) Object.defineProperty(globalThis, "sessionStorage", desc);
    restoreStorage = null;
  };
}

afterEach(() => {
  restoreStorage?.();
});

// --- The create idempotency key ---------------------------------------------------------

for (const [label, lost, says] of [
  [
    "a network error",
    networkError,
    "may or may not have been created. Try again",
  ],
  [
    "a 502",
    () => json(502, { error: { code: 502, message: "bad gateway" } }),
    "may or may not have been created. Try again",
  ],
  ["incident_busy", () => busyReply, "The incident store is busy."],
] as const) {
  test(`a create lost to ${label} is retried with the same request id: one incident`, async () => {
    const server = idempotentServer();
    let createCalls = 0;
    stub((c) => {
      if (c.method === "GET") return empty;
      if (isCreate(c)) {
        createCalls++;
        const reply = server.create(c);
        // The first attempt commits, but its answer never arrives.
        return createCalls === 1 ? lost() : reply;
      }
      return captured;
    });
    const root = await mount({});
    await click(q(root, "capture-to-incident"));
    await click(q(root, "capture-to-new-incident"));
    expect(root.textContent).toContain(says);
    expect(root.textContent).toContain("cannot create a second incident");
    expect(captures()).toHaveLength(0);

    await click(q(root, "capture-to-new-incident"));
    expect(creates()).toHaveLength(2);
    expect(requestIdOf(creates()[0])).toMatch(UUID);
    expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
    // The retry resends the same payload, window included.
    expect(creates()[1].body).toEqual(creates()[0].body);
    expect(server.made.size).toBe(1);
    expect(captures().map((c) => c.path)).toEqual([
      `/api/v1/incidents/${uuidFor("inc-1")}/capture`,
    ]);
    expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-1")}`]);
    expect(stored("local")).toBeNull();
  });
}

test("a lost create retried into a 429 keeps the key: the third attempt resends it, one incident", async () => {
  const server = idempotentServer();
  let createCalls = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      createCalls++;
      if (createCalls === 1) {
        server.create(c);
        return networkError();
      }
      if (createCalls === 2) {
        return json(429, {
          error: { code: 429, message: "slow down", detail: "rate limited" },
        });
      }
      return server.create(c);
    }
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("This attempt was refused: rate limited");
  expect(stored("local")).toMatchObject({
    requestId: requestIdOf(creates()[0]),
  });

  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(3);
  expect(requestIdOf(creates()[2])).toBe(requestIdOf(creates()[0]));
  expect(creates()[2].body).toEqual(creates()[0].body);
  expect(server.made.size).toBe(1);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-1")}`]);
});

test("a lost create retried after a re-scan with a new window resends the stored key and payload", async () => {
  const server = idempotentServer();
  let createCalls = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      createCalls++;
      const reply = server.create(c);
      return createCalls === 1
        ? json(502, { error: { code: 502, message: "bad gateway" } })
        : reply;
    }
    return captured;
  });
  let root = await mount({ windowStart: "2026-10-06T09:00:00.000Z" });
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("may or may not have been created");
  unmount();

  // The re-scan reports a different window; the outcome is still unknown.
  root = await mount({ windowStart: "2026-10-06T09:30:00.000Z" });
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(2);
  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(creates()[1].body).toEqual(creates()[0].body);
  expect(creates()[1].body).toMatchObject({
    windowStart: "2026-10-06T09:00:00.000Z",
  });
  expect(server.made.size).toBe(1);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-1")}`]);
});

test("a remount mid-create retries with the stored request id, and the late answer changes nothing", async () => {
  const server = idempotentServer();
  const first = deferred(created(uuidFor("unused")));
  let createCalls = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      createCalls++;
      // The server commits the first create at once; its answer is held.
      const reply = server.create(c);
      return createCalls === 1 ? first.promise.then(() => reply) : reply;
    }
    return captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  act(() => q(root, "capture-to-new-incident").click());
  await settle();
  unmount();

  root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(2);
  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(creates()[1].body).toEqual(creates()[0].body);
  expect(server.made.size).toBe(1);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-1")}`]);
  expect(stored("local")).toBeNull();

  first.release();
  await settle();
  expect(stored("local")).toBeNull();
  expect(captures()).toHaveLength(1);
});

test("unmounting mid-create neither captures nor navigates; the remount captures into it without creating", async () => {
  const held = deferred(created(uuidFor("inc-u")));
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return held.promise;
    return captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  act(() => q(root, "capture-to-new-incident").click());
  await settle();
  unmount();
  held.release();
  await settle();
  expect(captures()).toHaveLength(0);
  expect(assigned).toEqual([]);
  // The create reached the server, so its id is recorded despite the unmount.
  expect(stored("local")).toMatchObject({ id: uuidFor("inc-u") });

  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures()[0].path).toBe(
    `/api/v1/incidents/${uuidFor("inc-u")}/capture`,
  );
});

test("client_request_id_conflict on a retry: no silent create; only Create anyway mints a new key", async () => {
  const server = idempotentServer();
  let createCalls = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      createCalls++;
      const reply = server.create(c);
      if (createCalls === 1) {
        // The first create commits and its answer is lost; the incident is
        // then edited, so the retry's payload no longer matches it.
        server.edit(requestIdOf(c) as string);
        return networkError();
      }
      return reply;
    }
    return captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(2);
  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));

  const notice = q(root, "capture-create-conflict");
  expect(notice.textContent).toContain(
    "An incident for this capture may already exist — check your incidents.",
  );
  expect(
    notice.querySelector('a[href="/observability/incidents"]'),
  ).not.toBeNull();
  expect(root.querySelector('[data-testid="capture-to-new-incident"]')).toBe(
    null,
  );
  expect(stored("local")).toEqual({ conflict: true });

  // The conflict survives a re-scan: still no silent create.
  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-create-conflict")).toBeTruthy();
  expect(creates()).toHaveLength(2);

  await click(q(root, "capture-create-anyway"));
  expect(creates()).toHaveLength(3);
  expect(requestIdOf(creates()[2])).toMatch(UUID);
  expect(requestIdOf(creates()[2])).not.toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(2);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-2")}`]);
  expect(stored("local")).toBeNull();
});

test("a conflict recorded behind an open dialog still stops the next click from creating", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("inc-x2"));
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  // Another mount for the same target records a conflict meanwhile.
  globalThis.sessionStorage.setItem(
    pendingStorageKey("local"),
    JSON.stringify({ conflict: true }),
  );
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(0);
  expect(q(root, "capture-create-conflict")).toBeTruthy();
  // The dialog is not left busy: the explicit action is available.
  expect(q(root, "capture-create-anyway").getAttribute("aria-disabled")).toBe(
    "false",
  );
});

test("a create answered with no database clears the record and says so", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      return json(503, {
        error: {
          code: 503,
          message: "no database",
          reason: "incident_persistence_unavailable",
        },
      });
    }
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain(
    "This deployment has no database, so incidents cannot be recorded.",
  );
  expect(stored("local")).toBeNull();
});

test("a create refused with a 4xx says why and drops the request id", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      return json(400, {
        error: { code: 400, message: "bad", detail: "title too long" },
      });
    }
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain(
    "The incident could not be created: title too long.",
  );
  expect(stored("local")).toBeNull();
});

test("a stored intent is resent exactly as stored, whatever the diagnosis window", async () => {
  seed("local", { windowStart: "2026-10-06T08:00:00.000Z" });
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("inc-s"));
    return captured;
  });
  const root = await mount({ windowStart: "2026-10-06T09:30:00.000Z" });
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(creates()[0].body).toEqual({
    title: TITLE,
    summary: SUMMARY,
    windowStart: "2026-10-06T08:00:00.000Z",
    clientRequestId: "11111111-1111-4111-8111-111111111111",
  });
});

test("with no stored intent, a new key is minted with the current window", async () => {
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("inc-n"));
    return captured;
  });
  const root = await mount({ windowStart: "2026-10-06T09:30:00.000Z" });
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(creates()[0].body).toMatchObject({
    windowStart: "2026-10-06T09:30:00.000Z",
  });
  expect(requestIdOf(creates()[0])).toMatch(UUID);
});

test("without crypto.randomUUID (plain HTTP) the request id is still a v4 UUID", async () => {
  const c = globalThis.crypto as Crypto & { randomUUID?: unknown };
  Object.defineProperty(c, "randomUUID", {
    configurable: true,
    value: undefined,
  });
  try {
    stub((call) => {
      if (call.method === "GET") return empty;
      if (isCreate(call)) return created(uuidFor("inc-h"));
      return captured;
    });
    const root = await mount({});
    await click(q(root, "capture-to-incident"));
    await click(q(root, "capture-to-new-incident"));
    expect(requestIdOf(creates()[0])).toMatch(UUID);
  } finally {
    delete (c as { randomUUID?: unknown }).randomUUID;
  }
  expect(typeof globalThis.crypto.randomUUID).toBe("function");
});

// --- Pending incident across remounts -------------------------------------------------------

test("a remount keeps the created incident: the retry captures into it, never creates again", async () => {
  let attempt = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("inc-r"));
    attempt++;
    return attempt === 1 ? busyReply : captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("Capture did not run");

  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));

  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("inc-r")}/capture`,
    `/api/v1/incidents/${uuidFor("inc-r")}/capture`,
  ]);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("inc-r")}`]);
  // A successful capture clears the pending record: the next one starts fresh.
  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
});

test("a pending incident that refuses the capture for good is forgotten; the next click creates", async () => {
  seed("local", { id: uuidFor("stale") });
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === `/api/v1/incidents/${uuidFor("stale")}/capture`) {
      return json(409, {
        error: { code: 409, message: "closed", reason: "incident_closed" },
      });
    }
    if (isCreate(c)) return created(uuidFor("fresh"));
    if (c.path === `/api/v1/incidents/${uuidFor("fresh")}/capture`)
      return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("This incident is closed");
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
  expect(stored("local")).toBeNull();

  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("stale")}/capture`,
    `/api/v1/incidents/${uuidFor("fresh")}/capture`,
  ]);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("fresh")}`]);
});

const refusals: [string, { status: number; body: unknown }][] = [
  ["404", json(404, { error: { code: 404, message: "not found" } })],
  ["403", json(403, { error: { code: 403, message: "forbidden" } })],
  [
    "evidence_limit_exceeded",
    json(413, {
      error: { code: 413, message: "x", reason: "evidence_limit_exceeded" },
    }),
  ],
  [
    "scope_limit_exceeded",
    json(409, {
      error: { code: 409, message: "x", reason: "scope_limit_exceeded" },
    }),
  ],
];

for (const [label, reply] of refusals) {
  test(`a pending incident refusing capture with ${label} is forgotten`, async () => {
    seed("local", { id: uuidFor("stale") });
    stub((c) => {
      if (c.method === "GET") return empty;
      if (c.path === `/api/v1/incidents/${uuidFor("stale")}/capture`)
        return reply;
    });
    const root = await mount({});
    await click(q(root, "capture-to-incident"));
    await click(q(root, "capture-to-new-incident"));
    expect(stored("local")).toBeNull();
    expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
  });
}

test("capturing into an existing incident clears the pending one", async () => {
  seed("local", { id: uuidFor("pending") });
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c))
      return listOf([incident({ id: uuidFor("e2"), title: "Mine" })]);
    if (c.path === `/api/v1/incidents/${uuidFor("e2")}/capture`)
      return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-existing-incident"));
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("e2")}`]);
  expect(stored("local")).toBeNull();
});

test("a create from an earlier mount that lands during capture-into-existing stays recorded", async () => {
  const lateCreate = deferred(created(uuidFor("late")));
  const existingCapture = deferred(captured);
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c))
      return listOf([incident({ id: uuidFor("e3"), title: "Mine" })]);
    if (isCreate(c)) return lateCreate.promise;
    if (c.path === `/api/v1/incidents/${uuidFor("e3")}/capture`)
      return existingCapture.promise;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  act(() => q(root, "capture-to-new-incident").click());
  await settle();
  unmount();

  root = await mount({});
  await click(q(root, "capture-to-incident"));
  act(() => q(root, "capture-to-existing-incident").click());
  await settle();
  // The earlier mount's create answers while the capture is in flight.
  lateCreate.release();
  await settle();
  existingCapture.release();
  await settle();
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("e3")}`]);
  // The guarded clear left the newly made incident recorded, not orphaned.
  expect(stored("local")).toMatchObject({ id: uuidFor("late") });
});

for (const [label, broken] of [
  ["sessionStorage", false],
  ["the in-memory fallback", true],
] as const) {
  test(`logout forgets the pending record held in ${label}`, async () => {
    if (broken) breakStorage("getItem", "setItem", "removeItem");
    let attempt = 0;
    stub((c) => {
      if (c.path === "/api/v1/auth/logout") return json(200, { data: {} });
      if (c.method === "GET") return empty;
      if (isCreate(c)) return created(uuidFor("lo"));
      attempt++;
      return attempt === 1 ? busyReply : captured;
    });
    let root = await mount({});
    await click(q(root, "capture-to-incident"));
    await click(q(root, "capture-to-new-incident"));
    expect(root.textContent).toContain("Capture did not run");
    unmount();

    await logout();
    root = await mount({});
    await click(q(root, "capture-to-incident"));
    expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
    if (!broken) expect(stored("local")).toBeNull();
  });
}

// --- Round 6: identity scope, age bound, refusal table -------------------------------

test("a record written as one user is never read as another: no retry label, no resend", async () => {
  let attempt = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) {
      return created(uuidFor(creates().length === 1 ? "of-a" : "of-b"));
    }
    attempt++;
    return attempt === 1 ? busyReply : captured;
  });
  let root = await mount({ user: "user-a" });
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("Capture did not run");
  expect(stored("local", "user-a")).toMatchObject({ id: uuidFor("of-a") });
  unmount();

  // The session ends without logout and user B signs in on the same tab.
  root = await mount({ user: "user-b" });
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(2);
  expect(requestIdOf(creates()[1])).not.toBe(requestIdOf(creates()[0]));
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("of-a")}/capture`,
    `/api/v1/incidents/${uuidFor("of-b")}/capture`,
  ]);
  // User A's record is untouched, and A still sees it.
  expect(stored("local", "user-a")).toMatchObject({ id: uuidFor("of-a") });
  unmount();
  root = await mount({ user: "user-a" });
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
});

test("until the signed-in user is known the button is inactive and says why", async () => {
  stub(() => empty);
  const root = await mount({});
  await logout();
  await settle();
  const trigger = q(root, "capture-to-incident");
  expect(trigger.getAttribute("aria-disabled")).toBe("true");
  expect(trigger.getAttribute("title")).toContain("sign-in details");
});

for (const [label, over] of [
  ["older than the bound", { createdAt: Date.now() - 16 * 60 * 1000 }],
  ["without a recorded time", { createdAt: undefined }],
] as const) {
  test(`an unresolved intent ${label} is not resent: the notice, then Create anyway`, async () => {
    seed("local", over);
    stub((c) => {
      if (c.method === "GET") return empty;
      if (isCreate(c)) return created(uuidFor("inc-stale"));
      return captured;
    });
    const root = await mount({});
    await click(q(root, "capture-to-incident"));
    const notice = q(root, "capture-create-conflict");
    expect(notice.textContent).toContain("may already exist");
    expect(
      notice.querySelector('a[href="/observability/incidents"]'),
    ).not.toBeNull();
    expect(root.querySelector('[data-testid="capture-to-new-incident"]')).toBe(
      null,
    );
    expect(creates()).toHaveLength(0);

    await click(q(root, "capture-create-anyway"));
    expect(creates()).toHaveLength(1);
    expect(requestIdOf(creates()[0])).toMatch(UUID);
    expect(requestIdOf(creates()[0])).not.toBe(
      "11111111-1111-4111-8111-111111111111",
    );
    expect(assigned).toEqual([
      `/observability/incidents/${uuidFor("inc-stale")}`,
    ]);
  });
}

test("an unresolved intent inside the bound is still resent", async () => {
  seed("local", { createdAt: Date.now() - 14 * 60 * 1000 });
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("inc-fresh"));
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(requestIdOf(creates()[0])).toBe(
    "11111111-1111-4111-8111-111111111111",
  );
});

test("createRefusedForGood drops the key only for 400, 413 and no database", () => {
  const refusal = (status: number, reason?: string) =>
    new ApiError(status, status, "refused", {
      error: { code: status, message: "refused", reason },
    });
  const table: [string, unknown, boolean][] = [
    ["400", refusal(400), true],
    ["400 invalid id", refusal(400, "invalid_client_request_id"), true],
    ["413", refusal(413), true],
    ["no database", refusal(503, "incident_persistence_unavailable"), true],
    ["401", refusal(401), false],
    ["403", refusal(403), false],
    ["404", refusal(404), false],
    ["408", refusal(408), false],
    ["409 conflict", refusal(409, "client_request_id_conflict"), false],
    ["429", refusal(429), false],
    ["500", refusal(500), false],
    ["502", refusal(502), false],
    ["503 busy", refusal(503, "incident_busy"), false],
    ["network", new TypeError("Failed to fetch"), false],
  ];
  expect(
    table.map(([label, err]) => [label, createRefusedForGood(err)]),
  ).toEqual(table.map(([label, , drops]) => [label, drops]));
});

test("capturing into an existing incident while the record is a conflict clears it", async () => {
  seed("local", { conflict: true });
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c))
      return listOf([incident({ id: uuidFor("e4"), title: "Mine" })]);
    if (c.path === `/api/v1/incidents/${uuidFor("e4")}/capture`)
      return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-create-conflict")).toBeTruthy();
  await click(q(root, "capture-to-existing-incident"));
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("e4")}`]);
  expect(stored("local")).toBeNull();
});

test("capturing into an existing incident after a lost create leaves the notice, not a silent drop", async () => {
  const server = idempotentServer();
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c))
      return listOf([incident({ id: uuidFor("e5"), title: "Mine" })]);
    if (isCreate(c)) {
      server.create(c);
      return networkError();
    }
    if (c.path === `/api/v1/incidents/${uuidFor("e5")}/capture`)
      return captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("may or may not have been created");
  await click(q(root, "capture-to-existing-incident"));
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("e5")}`]);
  // The create committed (server.made), so the next visit says so.
  expect(server.made.size).toBe(1);
  expect(stored("local")).toEqual({ conflict: true });
  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-create-conflict").textContent).toContain(
    "may already exist",
  );
});

test("logout with storage that refuses removals still hides the record (tombstone)", async () => {
  breakStorage("removeItem");
  let attempt = 0;
  stub((c) => {
    if (c.path === "/api/v1/auth/logout") return json(200, { data: {} });
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("tomb"));
    attempt++;
    return attempt === 1 ? busyReply : captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(stored("local")).toMatchObject({ id: uuidFor("tomb") });
  unmount();

  await logout();
  // Storage could not remove it; the same user signs in again.
  expect(stored("local")).toMatchObject({ id: uuidFor("tomb") });
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
});

test("without sessionStorage the pending record survives a remount in memory, and success clears it", async () => {
  breakStorage("getItem", "setItem", "removeItem");
  let attempt = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("mem"));
    attempt++;
    return attempt === 1 ? busyReply : captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  expect(root.textContent).toContain("Capture did not run");

  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("mem")}/capture`,
    `/api/v1/incidents/${uuidFor("mem")}/capture`,
  ]);

  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
});

test("when storage keeps an older value it cannot overwrite or remove, memory shadows it", async () => {
  seed("local", { id: uuidFor("old") });
  breakStorage("setItem", "removeItem");
  let freshCaptures = 0;
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === `/api/v1/incidents/${uuidFor("old")}/capture`) {
      return json(404, { error: { code: 404, message: "gone" } });
    }
    if (isCreate(c)) return created(uuidFor("n1"));
    freshCaptures++;
    return freshCaptures === 1 ? busyReply : captured;
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident")); // old: 404, forgotten
  await click(q(root, "capture-to-new-incident")); // creates n1, capture busy
  expect(root.textContent).toContain("Capture did not run");

  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(1);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("old")}/capture`,
    `/api/v1/incidents/${uuidFor("n1")}/capture`,
    `/api/v1/incidents/${uuidFor("n1")}/capture`,
  ]);

  // Storage still holds "old", but the cleared state wins.
  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
  expect(stored("local")).toMatchObject({ id: uuidFor("old") });

  // Leave no tombstone behind: once storage is empty, a read heals it.
  restoreStorage?.();
  globalThis.sessionStorage.clear();
  unmount();
  await mount({});
});

test("a tombstone stops shadowing once storage no longer holds the value", async () => {
  seed("local", { id: uuidFor("stuck") });
  breakStorage("removeItem");
  stub((c) => {
    if (c.method === "GET") return empty;
    if (c.path === `/api/v1/incidents/${uuidFor("stuck")}/capture`) {
      return json(404, { error: { code: 404, message: "gone" } });
    }
  });
  let root = await mount({});
  await click(q(root, "capture-to-incident"));
  await click(q(root, "capture-to-new-incident"));
  // Storage refused the removal; the tombstone hides the stuck value.
  expect(stored("local")).toMatchObject({ id: uuidFor("stuck") });
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);

  restoreStorage?.();
  globalThis.sessionStorage.clear();
  unmount();
  root = await mount({});
  // The tombstone has healed, so a value written to storage now shows.
  seed("local", { id: uuidFor("later") });
  unmount();
  root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
});

test("the same namespace/kind/name on two clusters keep separate pending records", async () => {
  seed("c-other", { id: uuidFor("other") });
  stub((c) => {
    if (c.method === "GET") return empty;
    if (isCreate(c)) return created(uuidFor("here"));
    return captured;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(NEW_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("here")}/capture`,
  ]);
  expect(stored("c-other")).toMatchObject({ id: uuidFor("other") });
});

test("an empty cluster id and the local id share one pending record", async () => {
  seed("local", { id: uuidFor("seeded") });
  stub((c) => {
    if (c.method === "GET") return empty;
    return captured;
  });
  const root = await mount({ clusterId: "" });
  await click(q(root, "capture-to-incident"));
  expect(q(root, "capture-to-new-incident").textContent).toBe(RETRY_LABEL);
  await click(q(root, "capture-to-new-incident"));
  expect(creates()).toHaveLength(0);
  expect(captures().map((c) => c.path)).toEqual([
    `/api/v1/incidents/${uuidFor("seeded")}/capture`,
  ]);
});

// --- Picker and lifetimes ---------------------------------------------------------------

test("a cluster flip mid-load leaves no stuck flag, and the probe re-runs", async () => {
  const held = deferred(
    listOf([incident({ id: uuidFor("mine"), title: "Mine" })]),
  );
  let lists = 0;
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c)) {
      lists++;
      return lists === 1
        ? held.promise
        : listOf([incident({ id: uuidFor("mine"), title: "Mine" })]);
    }
    if (c.path.endsWith("/capture")) return captured;
  });
  let root = await show({});
  await click(q(root, "capture-to-incident"));
  expect(root.textContent).toContain("Loading your incidents");

  root = await show({ clusterId: "c-remote" });
  expect(root.querySelector('[data-testid="capture-to-incident-dialog"]')).toBe(
    null,
  );
  root = await show({});
  held.release();
  await settle();
  expect(calls.filter(isProbe)).toHaveLength(2);

  await click(q(root, "capture-to-incident"));
  expect(root.textContent).not.toContain("Loading your incidents");
  const rows = root.querySelectorAll(
    '[data-testid="capture-to-existing-incident"]',
  );
  expect(rows).toHaveLength(1);
  expect(q(root, "capture-to-new-incident").getAttribute("aria-disabled")).toBe(
    "false",
  );
  await click(rows[0] as HTMLElement);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("mine")}`]);
});

test("the picker pages with the continue token", async () => {
  stub((c) => {
    if (isProbe(c)) return empty;
    if (c.path === "/api/v1/incidents?limit=50") {
      return listOf([incident({ id: uuidFor("p1"), title: "Page one" })], "c2");
    }
    if (c.path === "/api/v1/incidents?limit=50&continue=c2") {
      return listOf([incident({ id: uuidFor("p2"), title: "Page two" })]);
    }
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  const more = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Load more",
  );
  expect(more).toBeTruthy();
  await click(more as HTMLElement);
  expect(
    calls.some((c) => c.path === "/api/v1/incidents?limit=50&continue=c2"),
  ).toBe(true);
  expect(root.textContent).toContain("Page one");
  expect(root.textContent).toContain("Page two");
});

test("a failed picker load says so and Retry recovers", async () => {
  let attempt = 0;
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c)) {
      attempt++;
      return attempt === 1
        ? json(500, { error: { code: 500, message: "boom" } })
        : listOf([incident({ id: uuidFor("ok"), title: "Recovered" })]);
    }
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(root.textContent).toContain("Could not load your open incidents.");
  const retry = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Retry",
  );
  await click(retry as HTMLElement);
  expect(root.textContent).not.toContain("Could not load");
  expect(root.textContent).toContain("Recovered");
});

test("a picker load that finds no database closes the dialog and deactivates the button", async () => {
  stub((c) => {
    if (isProbe(c)) return empty;
    return json(503, {
      error: {
        code: 503,
        message: "no db",
        reason: "incident_persistence_unavailable",
      },
    });
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  expect(root.querySelector('[data-testid="capture-to-incident-dialog"]')).toBe(
    null,
  );
  expect(q(root, "capture-to-incident").getAttribute("aria-disabled")).toBe(
    "true",
  );
});

// --- Capture errors (the workspace's shared wording) ----------------------------------------

const errorCases: [
  string,
  { status: number; body: unknown } | "network",
  string,
][] = [
  [
    "incident_closed",
    json(409, {
      error: { code: 409, message: "x", reason: "incident_closed" },
    }),
    "This incident is closed",
  ],
  [
    "incident_capture_unavailable",
    json(503, {
      error: {
        code: 503,
        message: "x",
        reason: "incident_capture_unavailable",
      },
    }),
    "Evidence capture is not available",
  ],
  [
    "evidence_limit_exceeded",
    json(413, {
      error: { code: 413, message: "x", reason: "evidence_limit_exceeded" },
    }),
    "exceed the incident's evidence limit",
  ],
  [
    "scope_limit_exceeded with max",
    json(409, {
      error: {
        code: 409,
        message: "x",
        reason: "scope_limit_exceeded",
        extra: { max: 20 },
      },
    }),
    "most distinct scopes it can (20)",
  ],
  [
    "scope_limit_exceeded without max",
    json(409, {
      error: { code: 409, message: "x", reason: "scope_limit_exceeded" },
    }),
    "most distinct scopes it can. Nothing",
  ],
  [
    "403",
    json(403, { error: { code: 403, message: "x" } }),
    "Only the incident owner may capture evidence.",
  ],
  [
    "400 with detail",
    json(400, { error: { code: 400, message: "x", detail: "kind unknown" } }),
    "The capture target is invalid: kind unknown.",
  ],
  ["non-ApiError", "network", "Capture failed. Nothing was recorded."],
];

for (const [label, reply, text] of errorCases) {
  test(`capture into an existing incident: ${label} is explained and the dialog stays`, async () => {
    stub((c) => {
      if (isProbe(c)) return empty;
      if (isList(c))
        return listOf([incident({ id: uuidFor("e1"), title: "Mine" })]);
      if (c.path === `/api/v1/incidents/${uuidFor("e1")}/capture`) {
        return reply === "network" ? networkError() : reply;
      }
    });
    const root = await mount({});
    await click(q(root, "capture-to-incident"));
    await click(q(root, "capture-to-existing-incident"));
    expect(root.textContent).toContain(text);
    expect(q(root, "capture-to-incident-dialog")).toBeTruthy();
    expect(assigned).toEqual([]);
    expect(
      q(root, "capture-to-existing-incident").getAttribute("aria-disabled"),
    ).toBe("false");
  });
}

test("a double click on an existing incident captures once", async () => {
  const held = deferred(captured);
  stub((c) => {
    if (isProbe(c)) return empty;
    if (isList(c))
      return listOf([incident({ id: uuidFor("d1"), title: "Mine" })]);
    if (c.path === `/api/v1/incidents/${uuidFor("d1")}/capture`)
      return held.promise;
  });
  const root = await mount({});
  await click(q(root, "capture-to-incident"));
  const row = q(root, "capture-to-existing-incident");
  act(() => {
    row.click();
    row.click();
  });
  await settle();
  await click(row);
  held.release();
  await settle();
  expect(captures()).toHaveLength(1);
  expect(assigned).toEqual([`/observability/incidents/${uuidFor("d1")}`]);
});
