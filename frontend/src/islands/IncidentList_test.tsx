/** @jsxImportSource preact */
import { afterAll, afterEach, expect, spyOn, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * The "New incident" form's outstanding create across a reload (#597). A
 * reload is an unmount and a fresh mount: nothing the island held in memory
 * survives it, only what the form recorded for the signed-in user. Requests
 * go through a fetch stub that routes by method and path and records every
 * call; navigation is observed through a spy on `location.assign`.
 */
GlobalRegistrator.register({ url: "http://localhost/" });

const { default: IncidentList } = await import("./IncidentList.tsx");
const { currentUserId, fetchCurrentUser, logout } = await import(
  "@/lib/auth.ts"
);
const { PENDING_INTENT_MAX_AGE_MS } = await import("@/lib/incident-create.ts");

afterAll(() => {
  GlobalRegistrator.unregister();
});

interface Call {
  method: string;
  path: string;
  body: unknown;
}
type Reply = { status: number; body: unknown };
type Route = (call: Call) => Reply | Promise<Reply> | undefined;

let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;
let calls: Call[] = [];
let assigned: string[] = [];
let assignSpy: ReturnType<typeof spyOn> | null = null;
let restoreStorage: (() => void) | null = null;

/** Answers every request with `body`, through a fetch of its own. */
async function withFetch<T>(body: unknown, run: () => Promise<T>): Promise<T> {
  const previous = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof globalThis.fetch;
  try {
    return await run();
  } finally {
    globalThis.fetch = previous;
  }
}

/** Signs `id` in, as the top bar's /auth/me load does. */
const signIn = (id: string) =>
  withFetch(
    {
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
    },
    () => fetchCurrentUser(),
  );

/** Signs out through logout(), with its request answered. */
const signOut = () => withFetch({}, () => logout());

afterEach(async () => {
  unmount();
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  assignSpy?.mockRestore();
  assignSpy = null;
  restoreStorage?.();
  // The signed-in user is a module-wide signal: never leave one behind.
  await signOut();
  setAccessToken(null);
  globalThis.sessionStorage.clear();
});

const json = (status: number, body: unknown): Reply => ({ status, body });
const emptyList = json(200, { data: [], metadata: {} });
const networkError = () => Promise.reject(new TypeError("Failed to fetch"));
const busyReply = json(503, {
  error: { code: 503, message: "busy", reason: "incident_busy" },
});
const noDatabase = json(503, {
  error: {
    code: 503,
    message: "unavailable",
    reason: "incident_persistence_unavailable",
  },
});
const badRequest = json(400, {
  error: { code: 400, message: "invalid", detail: "title is too long" },
});

const UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const incidentId = (n: number) =>
  `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

const isCreate = (c: Call) =>
  c.method === "POST" && c.path === "/api/v1/incidents";
const creates = () => calls.filter(isCreate);
const requestIdOf = (c: Call) =>
  (c.body as { clientRequestId?: string }).clientRequestId;

/**
 * The server's create idempotency (U25c): the first create with a request id
 * makes an incident; the same id with the same inputs replays it; the same
 * id with different inputs is 409 `client_request_id_conflict`.
 */
function idempotentServer() {
  const made = new Map<string, string>();
  const inputs = new Map<string, string>();
  const inputsOf = (c: Call) => {
    const b = c.body as Record<string, unknown>;
    return JSON.stringify([b.title, b.summary, b.windowStart, b.windowEnd]);
  };
  const create = (c: Call): Reply => {
    const rid = requestIdOf(c) ?? `anon-${made.size}`;
    const existing = made.get(rid);
    const incident = (id: string) => ({
      data: {
        incident: {
          id,
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
        },
      },
    });
    if (existing) {
      if (inputs.get(rid) !== inputsOf(c)) {
        return json(409, {
          error: {
            code: 409,
            message: "conflict",
            reason: "client_request_id_conflict",
          },
        });
      }
      return json(200, incident(existing));
    }
    const id = incidentId(made.size + 1);
    made.set(rid, id);
    inputs.set(rid, inputsOf(c));
    return json(201, incident(id));
  };
  return { made, create };
}

function stub(route: Route) {
  calls = [];
  assigned = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const call: Call = {
      method: init?.method ?? "GET",
      path: url.pathname + url.search,
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

/** Routes the list read to an empty page and every create to `create`. */
const serve = (create: (c: Call) => Reply | Promise<Reply>) =>
  stub((c) =>
    c.method === "GET" ? emptyList : isCreate(c) ? create(c) : undefined,
  );

async function settle() {
  for (let i = 0; i < 10; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0));
    });
  }
}

/** Mounts the island (a page load) with `user` signed in. */
async function mount(user: string | null = "u1") {
  if (user !== null && currentUserId() !== user) await signIn(user);
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<IncidentList />, host as HTMLElement));
  await settle();
  return host;
}

/** Unmounts the island and drops the host, as leaving the page does. */
function unmount() {
  if (!host) return;
  act(() => render(null, host as HTMLElement));
  host.remove();
  host = null;
}

/** A reload: the island goes away and a new one mounts. */
async function reload(user: string | null = "u1") {
  unmount();
  return mount(user);
}

function buttonNamed(root: HTMLElement, text: string): HTMLElement {
  const el = [...root.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === text,
  );
  if (!el) throw new Error(`no button ${text}`);
  return el as HTMLElement;
}

async function openForm(root: HTMLElement) {
  act(() => buttonNamed(root, "New incident").click());
  await settle();
}

const field = (root: HTMLElement, id: string) =>
  root.querySelector(`#${id}`) as HTMLInputElement | HTMLTextAreaElement;

async function type(root: HTMLElement, id: string, value: string) {
  act(() => {
    const el = field(root, id);
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await settle();
}

async function submit(root: HTMLElement) {
  act(() => {
    root
      .querySelector("form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
  await settle();
}

async function click(el: HTMLElement) {
  act(() => el.click());
  await settle();
}

const FORM_KEY = (user = "u1") =>
  `kubecenter.capture-pending:${user}|new-incident-form`;
const stored = (user = "u1") =>
  JSON.parse(globalThis.sessionStorage.getItem(FORM_KEY(user)) ?? "null");
const SEEDED_ID = "11111111-1111-4111-8111-111111111111";
/** Seeds the form's record for `user`, as an earlier page load left it. */
const seed = (over: Record<string, unknown> = {}, user = "u1") =>
  globalThis.sessionStorage.setItem(
    FORM_KEY(user),
    JSON.stringify({
      requestId: SEEDED_ID,
      title: "API latency",
      summary: "p99 over 2s",
      windowStart: "2026-10-06T08:00:00.000Z",
      createdAt: Date.now(),
      sentAt: Date.now(),
      ...over,
    }),
  );

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

/** Opens the form, fills a title, and submits it once. */
async function createOnce(root: HTMLElement, title = "API latency") {
  await openForm(root);
  await type(root, "incident-title", title);
  await submit(root);
}

// --- Reload reuses the outstanding create ---------------------------------------

test("a create whose answer was lost is resent with the same id after a reload: one incident", async () => {
  const server = idempotentServer();
  let n = 0;
  serve((c) => {
    const reply = server.create(c);
    // The first attempt commits, but its answer never arrives.
    return ++n === 1 ? networkError() : reply;
  });
  let root = await mount();
  await createOnce(root);
  expect(root.textContent).toContain("may or may not have been created");
  expect(stored().requestId).toBe(requestIdOf(creates()[0]));

  root = await reload();
  await openForm(root);
  // The form reopens with the inputs that create was sent with, and says why.
  expect(field(root, "incident-title").value).toBe("API latency");
  expect(
    root.querySelector('[data-testid="new-incident-restored"]'),
  ).toBeTruthy();
  await submit(root);

  expect(creates()).toHaveLength(2);
  expect(requestIdOf(creates()[0])).toMatch(UUID);
  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(1);
  expect(assigned).toEqual([`/observability/incidents/${incidentId(1)}`]);
  // A confirmed success clears the record.
  expect(stored()).toBeNull();
});

test("a reload while the create is in flight resends its id", async () => {
  const server = idempotentServer();
  let release: () => void = () => {};
  let first = true;
  serve((c) => {
    const reply = server.create(c);
    if (!first) return reply;
    first = false;
    // Never answered: the page reloads before it is.
    return new Promise<Reply>((resolve) => {
      release = () => resolve(reply);
    });
  });
  let root = await mount();
  await createOnce(root);
  // Recorded before the request was sent.
  expect(stored().requestId).toBe(requestIdOf(creates()[0]));

  root = await reload();
  await openForm(root);
  await submit(root);
  release();
  await settle();

  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(1);
});

test("a stored create resent with edited inputs is a conflict, not a second incident", async () => {
  const server = idempotentServer();
  let n = 0;
  serve((c) => {
    const reply = server.create(c);
    return ++n === 1 ? networkError() : reply;
  });
  let root = await mount();
  await createOnce(root, "API latency");

  root = await reload();
  await openForm(root);
  await type(root, "incident-title", "Something else");
  await submit(root);

  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(1);
  expect(
    root.querySelector('[data-testid="new-incident-conflict"]'),
  ).toBeTruthy();
  expect(assigned).toEqual([]);
});

test("the conflict notice survives a reload: nothing is created until Create anyway", async () => {
  const server = idempotentServer();
  let n = 0;
  serve((c) => {
    const reply = server.create(c);
    return ++n === 1 ? networkError() : reply;
  });
  let root = await mount();
  await createOnce(root, "API latency");
  await type(root, "incident-title", "Edited");
  await submit(root);
  expect(
    root.querySelector('[data-testid="new-incident-conflict"]'),
  ).toBeTruthy();

  // "Check your incidents" links to this same page.
  root = await reload();
  await openForm(root);
  expect(
    root.querySelector('[data-testid="new-incident-conflict"]'),
  ).toBeTruthy();
  await type(root, "incident-title", "Edited");
  await submit(root);
  expect(creates()).toHaveLength(2);

  await click(
    root.querySelector(
      '[data-testid="new-incident-create-anyway"]',
    ) as HTMLElement,
  );
  expect(creates()).toHaveLength(3);
  expect(requestIdOf(creates()[2])).not.toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(2);
  expect(stored()).toBeNull();
});

test("a create restored after a reload keeps its id through a 400: an earlier attempt may have committed", async () => {
  seed();
  let n = 0;
  serve(() =>
    ++n === 1
      ? badRequest
      : json(201, { data: { incident: { id: incidentId(9) } } }),
  );
  const root = await mount();
  await openForm(root);
  await submit(root);
  expect(requestIdOf(creates()[0])).toBe(SEEDED_ID);
  expect(stored().requestId).toBe(SEEDED_ID);

  await submit(root);
  expect(requestIdOf(creates()[1])).toBe(SEEDED_ID);
});

// --- When the record is dropped -------------------------------------------------

test("a create refused for good clears the record: no database", async () => {
  let n = 0;
  serve(() => (++n === 1 ? networkError() : noDatabase));
  let root = await mount();
  await createOnce(root);
  expect(stored()).not.toBeNull();
  await submit(root);
  expect(stored()).toBeNull();

  root = await reload();
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
});

test("a first attempt refused with a 400 clears the record; the next submit mints a new id", async () => {
  serve(() => badRequest);
  let root = await mount();
  await createOnce(root);
  expect(root.textContent).toContain("could not be created");
  expect(stored()).toBeNull();

  root = await reload();
  await createOnce(root);
  expect(requestIdOf(creates()[1])).not.toBe(requestIdOf(creates()[0]));
});

test("an outcome-unknown create keeps the record: busy", async () => {
  serve(() => busyReply);
  const root = await mount();
  await createOnce(root);
  expect(stored().requestId).toBe(requestIdOf(creates()[0]));
});

test("a record past the age bound is ignored: the form opens empty and mints a new id", async () => {
  const long = Date.now() - PENDING_INTENT_MAX_AGE_MS - 60_000;
  seed({ createdAt: long, sentAt: long });
  serve(() => busyReply);
  const root = await mount();
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
  expect(
    root.querySelector('[data-testid="new-incident-restored"]'),
  ).toBeNull();
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(requestIdOf(creates()[0])).toMatch(UUID);
  expect(requestIdOf(creates()[0])).not.toBe(SEEDED_ID);
});

test("a record inside the age bound is resent, measured from its last send", async () => {
  seed({ createdAt: Date.now() - 2 * PENDING_INTENT_MAX_AGE_MS });
  serve(() => busyReply);
  const root = await mount();
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("API latency");
  await submit(root);
  expect(requestIdOf(creates()[0])).toBe(SEEDED_ID);
  expect(creates()[0].body).toMatchObject({
    title: "API latency",
    summary: "p99 over 2s",
    windowStart: "2026-10-06T08:00:00.000Z",
  });
});

test("a window end is kept with the record and restored", async () => {
  seed({ windowEnd: "2026-10-06T09:00:00.000Z" });
  serve(() => busyReply);
  const root = await mount();
  await openForm(root);
  expect(field(root, "incident-window-end").value).not.toBe("");
  await submit(root);
  expect(creates()[0].body).toMatchObject({
    windowEnd: "2026-10-06T09:00:00.000Z",
  });
});

// --- Whose record it is ---------------------------------------------------------

test("a record from another user is never read", async () => {
  seed({}, "u2");
  serve(() => busyReply);
  const root = await mount("u1");
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
  await type(root, "incident-title", "Mine");
  await submit(root);
  expect(requestIdOf(creates()[0])).not.toBe(SEEDED_ID);
  // u2's record is left alone, and u1's is its own.
  expect(stored("u2").requestId).toBe(SEEDED_ID);
  expect(stored("u1").requestId).toBe(requestIdOf(creates()[0]));
});

test("a user switch on a mounted list never carries the first user's create over", async () => {
  serve(() => busyReply);
  const root = await mount("u1");
  await createOnce(root);
  await act(async () => {
    await signIn("u2");
  });
  await settle();
  // The form is closed and reopened as u2.
  await click(buttonNamed(root, "Cancel"));
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
  await type(root, "incident-title", "Theirs");
  await submit(root);
  expect(requestIdOf(creates()[1])).not.toBe(requestIdOf(creates()[0]));
});

test("logout clears the record", async () => {
  serve(() => busyReply);
  let root = await mount();
  await createOnce(root);
  expect(stored()).not.toBeNull();

  await signOut();
  expect(stored()).toBeNull();

  root = await reload("u1");
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
});

test("a create answered after logout does not bring the record back", async () => {
  let release: () => void = () => {};
  serve(
    () =>
      new Promise<Reply>((resolve) => {
        release = () => resolve(busyReply);
      }),
  );
  const root = await mount();
  await createOnce(root);
  expect(stored()).not.toBeNull();

  await signOut();
  release();
  await settle();
  expect(stored()).toBeNull();
  // Nor does the island keep it: after signing back in, nothing is restored.
  await act(async () => {
    await signIn("u1");
  });
  await settle();
  await click(buttonNamed(root, "Cancel"));
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("");
  expect(stored()).toBeNull();
});

// --- Without storage ------------------------------------------------------------

test("with sessionStorage throwing, the form still resends its outstanding id", async () => {
  breakStorage("getItem", "setItem", "removeItem");
  const server = idempotentServer();
  let n = 0;
  serve((c) => {
    const reply = server.create(c);
    return ++n === 1 ? networkError() : reply;
  });
  const root = await mount();
  await createOnce(root);
  expect(root.textContent).toContain("may or may not have been created");
  await submit(root);

  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
  expect(server.made.size).toBe(1);
  expect(assigned).toEqual([`/observability/incidents/${incidentId(1)}`]);
});

test("with sessionStorage throwing, Cancel and reopen still restore the outstanding create", async () => {
  breakStorage("getItem", "setItem", "removeItem");
  serve(() => busyReply);
  const root = await mount();
  await createOnce(root);
  await click(buttonNamed(root, "Cancel"));
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("API latency");
  await submit(root);
  expect(requestIdOf(creates()[1])).toBe(requestIdOf(creates()[0]));
});

// --- Before the signed-in user is known ----------------------------------------

const createButton = (root: HTMLElement) =>
  root.querySelector('[data-testid="new-incident-create"]') as HTMLElement;
const conflictNotice = (root: HTMLElement) =>
  root.querySelector('[data-testid="new-incident-conflict"]');
const seedConflict = (over: Record<string, unknown> = {}) =>
  globalThis.sessionStorage.setItem(
    FORM_KEY(),
    JSON.stringify({ conflict: true, sentAt: Date.now(), ...over }),
  );

test("a submit before the signed-in user is known sends nothing; once known, the stored id is resent", async () => {
  seed();
  serve(() => busyReply);
  const root = await mount(null);
  await openForm(root);
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(creates()).toHaveLength(0);
  expect(createButton(root).getAttribute("aria-disabled")).toBe("true");
  expect(createButton(root).getAttribute("aria-describedby")).toBe(
    "new-incident-not-ready",
  );
  expect(
    root.querySelector('[data-testid="new-incident-not-ready"]')?.textContent,
  ).toContain("still loading");

  await act(async () => {
    await signIn("u1");
  });
  await settle();
  expect(createButton(root).getAttribute("aria-disabled")).toBe("false");
  expect(
    root.querySelector('[data-testid="new-incident-not-ready"]'),
  ).toBeNull();
  await submit(root);
  expect(creates()).toHaveLength(1);
  expect(requestIdOf(creates()[0])).toBe(SEEDED_ID);
  expect(stored().requestId).toBe(SEEDED_ID);
});

test("when the user load failed, the create stays inactive and says to reload", async () => {
  serve(() => busyReply);
  // A load that ends without a user, as when /auth/me fails.
  await withFetch({}, () => fetchCurrentUser());
  const root = await mount(null);
  await openForm(root);
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(creates()).toHaveLength(0);
  expect(
    root.querySelector('[data-testid="new-incident-not-ready"]')?.textContent,
  ).toContain("Reload the page");
});

test("a conflict restored after the form opened shows its notice at once, and nothing is sent", async () => {
  seedConflict();
  serve(() => busyReply);
  const root = await mount(null);
  await openForm(root);
  expect(conflictNotice(root)).toBeNull();

  await act(async () => {
    await signIn("u1");
  });
  await settle();
  expect(conflictNotice(root)).toBeTruthy();
  expect(
    root.querySelector('[data-testid="new-incident-create-anyway"]'),
  ).toBeTruthy();
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(creates()).toHaveLength(0);

  await click(
    root.querySelector(
      '[data-testid="new-incident-create-anyway"]',
    ) as HTMLElement,
  );
  expect(creates()).toHaveLength(1);
  expect(requestIdOf(creates()[0])).toMatch(UUID);
});

test("a create restored after the form opened is resent by the next submit", async () => {
  seed();
  serve(() => busyReply);
  const root = await mount(null);
  await openForm(root);
  await act(async () => {
    await signIn("u1");
  });
  await settle();
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(creates()).toHaveLength(1);
  expect(requestIdOf(creates()[0])).toBe(SEEDED_ID);
});

// --- The conflict's age bound ---------------------------------------------------

test("a conflict past the age bound is ignored and cleared: the next submit creates", async () => {
  seedConflict({ sentAt: Date.now() - PENDING_INTENT_MAX_AGE_MS - 60_000 });
  serve(() => busyReply);
  const root = await mount();
  expect(stored()).toBeNull();
  await openForm(root);
  expect(conflictNotice(root)).toBeNull();
  await type(root, "incident-title", "API latency");
  await submit(root);
  expect(creates()).toHaveLength(1);
});

test("a conflict without a send time counts as stale", async () => {
  globalThis.sessionStorage.setItem(
    FORM_KEY(),
    JSON.stringify({ conflict: true }),
  );
  serve(() => busyReply);
  const root = await mount();
  await openForm(root);
  expect(conflictNotice(root)).toBeNull();
});

test("a conflict records when its create was sent, and inside the bound it survives a reload", async () => {
  const server = idempotentServer();
  let n = 0;
  serve((c) => {
    const reply = server.create(c);
    return ++n === 1 ? networkError() : reply;
  });
  const before = Date.now();
  let root = await mount();
  await createOnce(root, "API latency");
  await type(root, "incident-title", "Edited");
  await submit(root);
  expect(stored().conflict).toBe(true);
  expect(stored().sentAt).toBeGreaterThanOrEqual(before);

  root = await reload();
  await openForm(root);
  expect(conflictNotice(root)).toBeTruthy();
});

// --- The form unmounting mid-create --------------------------------------------

test("a create cut off by the form unmounting keeps its id; a later 400 does not drop it", async () => {
  let releaseList: (r: Reply) => void = () => {};
  let gets = 0;
  let posts = 0;
  stub((c) => {
    if (c.method === "GET") {
      // The first page load is held, then answers no database, which
      // unmounts the form while its create is still pending.
      return ++gets === 1
        ? new Promise<Reply>((resolve) => {
            releaseList = resolve;
          })
        : emptyList;
    }
    if (!isCreate(c)) return undefined;
    return ++posts === 1 ? new Promise<Reply>(() => {}) : badRequest;
  });
  let root = await mount();
  await createOnce(root);
  const id = requestIdOf(creates()[0]);
  expect(stored().requestId).toBe(id);

  releaseList(noDatabase);
  await settle();
  expect(root.querySelector("form")).toBeNull();
  // The abort leaves the outcome unknown: the id is kept.
  expect(stored().requestId).toBe(id);

  root = await reload();
  await openForm(root);
  expect(field(root, "incident-title").value).toBe("API latency");
  await submit(root);
  expect(root.querySelector('[role="alert"]')).toBeTruthy();
  expect(stored().requestId).toBe(id);
  await submit(root);
  expect(requestIdOf(creates()[2])).toBe(id);
});

// --- 413 ------------------------------------------------------------------------

const tooLarge = json(413, {
  error: { code: 413, message: "too large", detail: "the body is too large" },
});

test("a first attempt refused with a 413 clears the record it wrote before sending", async () => {
  let release: () => void = () => {};
  serve(
    () =>
      new Promise<Reply>((resolve) => {
        release = () => resolve(tooLarge);
      }),
  );
  const root = await mount();
  await createOnce(root);
  expect(stored().requestId).toBe(requestIdOf(creates()[0]));
  release();
  await settle();
  expect(root.textContent).toContain("could not be created");
  expect(stored()).toBeNull();
});

test("a create restored after a reload keeps its id through a 413, like a 400", async () => {
  seed();
  serve(() => tooLarge);
  const root = await mount();
  await openForm(root);
  await submit(root);
  expect(requestIdOf(creates()[0])).toBe(SEEDED_ID);
  expect(stored().requestId).toBe(SEEDED_ID);
});
