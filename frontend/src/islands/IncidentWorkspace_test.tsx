/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, jest, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { ApiError, setAccessToken } from "@/lib/api.ts";
import type {
  Completeness,
  Evidence,
  IncidentDetail,
  IncidentView,
  NoteView,
  WithheldEvidence,
} from "@/lib/incident-types.ts";

const { default: IncidentEvidenceTimeline } = await import(
  "./IncidentEvidenceTimeline.tsx"
);
const { default: IncidentWorkspace } = await import("./IncidentWorkspace.tsx");
const { captureErrorText } = await import(
  "@/src/components/incidents/CapturePanel.tsx"
);

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

const ID = "00000000-0000-4000-8000-000000000001";

interface Call {
  url: string;
  method: string;
  headers: Headers;
  body?: string;
}
type Route = (call: Call) => Response | Promise<Response> | undefined;

let calls: Call[] = [];
let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
  setAccessToken(null);
});

const json = (status: number, body: unknown, headers: HeadersInit = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });

/** Routes each request to the first route that answers; unrouted is a 500. */
function stubFetch(...routes: Route[]) {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? "GET",
      headers: new Headers(init?.headers),
      body: typeof init?.body === "string" ? init.body : undefined,
    };
    calls.push(call);
    for (const r of routes) {
      const res = r(call);
      if (res) return Promise.resolve(res);
    }
    return Promise.resolve(
      json(500, { error: { code: 500, message: "unrouted" } }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function mount(node: preact.JSX.Element) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(node, host as HTMLElement));
  await flush();
  return host;
}

function incident(over: Partial<IncidentView> = {}): IncidentView {
  return {
    id: ID,
    ownerId: "alice",
    clusterId: "local",
    title: "Checkout is down",
    summary: "",
    status: "open",
    windowStart: "2026-10-01T09:00:00Z",
    retentionDays: 30,
    createdAt: "2026-10-01T09:00:00Z",
    updatedAt: "2026-10-01T09:00:00Z",
    role: "owner",
    canAnnotate: true,
    ...over,
  };
}

function evidence(n: number, over: Partial<Evidence> = {}): Evidence {
  return {
    id: `e${n}`,
    incidentId: ID,
    evidenceKind: "object_summary",
    mode: "snapshot",
    source: {
      clusterId: "local",
      apiGroup: "apps",
      resource: "deployments",
      kind: "Deployment",
      namespace: "shop",
      name: "checkout",
      uid: "uid-1",
    },
    collectedAt: `2026-10-01T10:0${n}:00Z`,
    completeness: "complete",
    redaction: {
      applied: false,
      fieldsRemoved: 0,
      truncated: false,
      secretDerived: false,
    },
    payloadBytes: 10,
    payload: { ok: true },
    ...over,
  };
}

const withheld = (n: number): WithheldEvidence => ({
  id: `w${n}`,
  evidenceKind: "event_list",
  collectedAt: `2026-10-01T10:0${n}:00Z`,
  withheld: true,
  withheldReason: "forbidden",
});

function detail(
  over: Partial<IncidentView> = {},
  ev: Evidence[] = [],
): IncidentDetail {
  return {
    incident: incident(over),
    counts: { visible: ev.length, withheld: 0 },
    evidence: ev,
    withheld: [],
  };
}

const routeNotes =
  (notes: NoteView[] = []): Route =>
  (c) =>
    c.method === "GET" && c.url.includes("/notes")
      ? json(200, { data: notes, metadata: {} })
      : undefined;
const routeGrants: Route = (c) =>
  c.method === "GET" && c.url.includes("/grants")
    ? json(200, { data: [] })
    : undefined;

// --- Timeline ----------------------------------------------------------------

test("each row labels both timestamps, and an unknown observation time is never the capture time", async () => {
  const root = await mount(
    <IncidentEvidenceTimeline
      items={[
        evidence(2, { sourceObservedAt: "2026-10-01T09:58:00Z" }),
        evidence(1),
      ]}
    />,
  );
  const rows = [...root.querySelectorAll("ol > li")];
  expect(rows).toHaveLength(2);
  const first = rows[0].textContent ?? "";
  expect(first).toContain("Observed");
  expect(first).toContain("Captured");
  expect(
    rows[0].querySelector('time[datetime="2026-10-01T09:58:00Z"]'),
  ).not.toBeNull();
  const second = rows[1].textContent ?? "";
  expect(second).toContain("observation time unknown");
  // Only the capture time is rendered as a <time>: no fallback.
  const times = [...rows[1].querySelectorAll("time")].map((t) =>
    t.getAttribute("datetime"),
  );
  expect(times).toEqual(["2026-10-01T10:01:00Z"]);
});

test("the five completeness states render as five distinct labels", async () => {
  const states: Completeness[] = [
    "complete",
    "partial",
    "failed",
    "forbidden",
    "timed_out",
  ];
  const root = await mount(
    <IncidentEvidenceTimeline
      items={states.map((s, i) => evidence(i, { completeness: s }))}
    />,
  );
  const text = root.textContent ?? "";
  for (const label of [
    "Complete",
    "Partial",
    "Failed",
    "Forbidden",
    "Timed out",
  ]) {
    expect(text).toContain(label);
  }
});

test("a withheld placeholder is an explicit row that discloses no scope", async () => {
  const root = await mount(
    <IncidentEvidenceTimeline items={[evidence(2), withheld(1)]} />,
  );
  const row = root.querySelector('li[data-withheld="true"]');
  expect(row).not.toBeNull();
  const text = row?.textContent ?? "";
  expect(text).toContain("Withheld");
  expect(text).toContain("You do not currently have access");
  expect(text).not.toContain("shop");
  expect(text).not.toContain("checkout");
  expect(text).not.toContain("Deployment");
});

test("redaction metadata is shown", async () => {
  const root = await mount(
    <IncidentEvidenceTimeline
      items={[
        evidence(1, {
          redaction: {
            applied: true,
            fieldsRemoved: 3,
            truncated: true,
            secretDerived: true,
          },
        }),
      ]}
    />,
  );
  const text = root.textContent ?? "";
  expect(text).toContain("3 fields removed");
  expect(text).toContain("Truncated");
  expect(text).toContain("Secret");
});

for (const [status, phrase] of [
  [404, "No longer present"],
  [403, "No longer authorized"],
] as const) {
  test(`a live link whose object answers ${status} reads "${phrase}"`, async () => {
    stubFetch((c) =>
      c.url === "/api/v1/resources/deployments/shop/checkout"
        ? json(status, { error: { code: status, message: "x" } })
        : undefined,
    );
    const root = await mount(
      <IncidentEvidenceTimeline
        items={[evidence(1, { mode: "live_link", payload: undefined })]}
      />,
    );
    expect(root.textContent).toContain("Live link");
    expect(root.textContent).toContain(phrase);
    expect(calls[0].headers.get("X-Cluster-ID")).toBe("local");
    // No link to an object that is gone or unreadable.
    expect(root.querySelector('a[href^="/workloads/"]')).toBeNull();
  });
}

test("a live link to a present object deep-links to its detail page", async () => {
  stubFetch((c) =>
    c.url === "/api/v1/resources/deployments/shop/checkout"
      ? json(200, { data: { metadata: { uid: "uid-1" } } })
      : undefined,
  );
  const root = await mount(
    <IncidentEvidenceTimeline
      items={[evidence(1, { mode: "live_link", payload: undefined })]}
    />,
  );
  expect(
    root.querySelector('a[href="/workloads/deployments/shop/checkout"]'),
  ).not.toBeNull();
});

// --- Workspace -----------------------------------------------------------------

test("a collaborator sees no owner-only panels", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
        ? json(200, {
            data: detail({ role: "collaborator", canAnnotate: false }),
          })
        : undefined,
    routeNotes(),
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const text = root.textContent ?? "";
  expect(text).toContain("Checkout is down");
  expect(text).toContain("read-only");
  expect(text).not.toContain("Capture evidence");
  expect(text).not.toContain("Sharing");
  expect(text).not.toContain("Close incident");
  expect(calls.some((c) => c.url.includes("/grants"))).toBe(false);
});

test("the owner sees capture, sharing, status and the withheld count", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
        ? json(200, {
            data: { ...detail(), counts: { visible: 0, withheld: 4 } },
          })
        : undefined,
    routeNotes(),
    routeGrants,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const text = root.textContent ?? "";
  expect(text).toContain("Capture evidence");
  expect(text).toContain("Sharing");
  expect(text).toContain("Close incident");
  expect(text).toContain("4 withheld");
  expect(root.querySelector('[aria-live="polite"]')).not.toBeNull();
});

test("Load more after a failed page fetches that page again", async () => {
  let attempts = 0;
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
        ? json(200, {
            data: detail({}, [evidence(2)]),
            metadata: { continue: "c2" },
          })
        : undefined,
    (c) => {
      if (!c.url.includes("/evidence?")) return undefined;
      attempts++;
      return attempts === 1
        ? json(500, { error: { code: 500, message: "boom" } })
        : json(200, {
            data: {
              counts: { visible: 2, withheld: 0 },
              evidence: [evidence(1)],
              withheld: [],
            },
            metadata: {},
          });
    },
    routeNotes(),
    routeGrants,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const loadMore = () =>
    [...root.querySelectorAll("button")].find(
      (b) => b.textContent === "Load more",
    );
  act(() => loadMore()?.click());
  await flush();
  expect(attempts).toBe(1);
  expect(root.textContent).toContain("Could not load more evidence.");
  act(() => loadMore()?.click());
  await flush();
  expect(attempts).toBe(2);
  expect(calls.filter((c) => c.url.includes("continue=c2"))).toHaveLength(2);
  expect(
    root.querySelectorAll("ol[aria-label='Evidence timeline'] > li"),
  ).toHaveLength(2);
  expect(loadMore()).toBeUndefined();
});

/** Clicks the export button for `label`, capturing what was saved. */
async function clickExport(root: HTMLElement, label: "JSON" | "Markdown") {
  const saved: string[] = [];
  const realCreate = URL.createObjectURL;
  const realRevoke = URL.revokeObjectURL;
  const realClick = HTMLAnchorElement.prototype.click;
  URL.createObjectURL = () => "blob:x";
  URL.revokeObjectURL = () => {};
  HTMLAnchorElement.prototype.click = function (this: HTMLAnchorElement) {
    saved.push(this.download);
  };
  try {
    const button = [...root.querySelectorAll("button")].find(
      (b) => b.textContent === label,
    );
    act(() => button?.click());
    await flush();
  } finally {
    URL.createObjectURL = realCreate;
    URL.revokeObjectURL = realRevoke;
    HTMLAnchorElement.prototype.click = realClick;
  }
  return saved;
}

const routeDetail =
  (over: Partial<IncidentView> = {}): Route =>
  (c) =>
    c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
      ? json(200, { data: detail(over) })
      : undefined;

test("export downloads with the bearer token and the requested format", async () => {
  setAccessToken("tok");
  stubFetch(routeDetail({ role: "collaborator" }), routeNotes(), (c) =>
    c.url.includes("/export?")
      ? new Response("# incident", {
          status: 200,
          headers: {
            "Content-Type": "text/markdown",
            "Content-Disposition":
              'attachment; filename="incident-x-20261001.md"',
          },
        })
      : undefined,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const saved = await clickExport(root, "Markdown");
  const call = calls.find((c) => c.url.includes("/export?"));
  expect(call?.url).toBe(`/api/v1/incidents/${ID}/export?format=markdown`);
  expect(call?.headers.get("Authorization")).toBe("Bearer tok");
  expect(call?.headers.get("X-Cluster-ID")).toBe("local");
  expect(saved).toEqual(["incident-x-20261001.md"]);
});

test("a rate-limited export says so", async () => {
  stubFetch(routeDetail(), routeNotes(), routeGrants, (c) =>
    c.url.includes("/export?")
      ? json(429, { error: { code: 429, message: "too many requests" } })
      : undefined,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const saved = await clickExport(root, "JSON");
  expect(saved).toEqual([]);
  expect(root.querySelector('[role="group"] [role="alert"]')?.textContent).toBe(
    "Exports are rate-limited. Try again shortly.",
  );
});

test("capture errors: busy names the retry delay, outcome-unknown says retry is safe", () => {
  const busy = new ApiError(
    503,
    503,
    "too many captures in progress; retry shortly",
    {
      error: { reason: "incident_busy" },
    },
  );
  expect(captureErrorText(busy)).toContain("retry in about 1 second");
  busy.headers = new Headers({ "Retry-After": "4" });
  expect(captureErrorText(busy)).toContain("retry in about 4 seconds");
  expect(captureErrorText(busy)).toContain("too many captures in progress");
  const unknown = new ApiError(503, 503, "x", {
    error: { reason: "incident_capture_outcome_unknown" },
  });
  expect(captureErrorText(unknown)).toContain(
    "may or may not have been recorded",
  );
  expect(captureErrorText(unknown)).toContain("Retrying is safe");
  const remote = new ApiError(400, 400, "x", {
    error: { reason: "remote_capture_unsupported" },
  });
  expect(captureErrorText(remote)).toContain("local cluster only");
});

test("a capture is pinned to the local cluster and its per-source results are shown", async () => {
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
        ? json(200, { data: detail() })
        : undefined,
    (c) =>
      c.method === "POST" && c.url.endsWith("/capture")
        ? json(200, {
            data: {
              completeness: "partial",
              collectedAt: "2026-10-01T10:00:00Z",
              sources: [
                { id: "diagnostics", completeness: "complete", items: 2 },
                {
                  id: "events",
                  completeness: "timed_out",
                  detail: "source did not finish within its time limit",
                  items: 0,
                },
              ],
              collected: 2,
              inserted: 2,
              deduplicated: 0,
              dropped: 0,
            },
          })
        : undefined,
    routeNotes(),
    routeGrants,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  const set = (id: string, v: string) => {
    const el = root.querySelector(`#${id}`) as HTMLInputElement;
    act(() => {
      el.value = v;
      el.dispatchEvent(new Event("input", { bubbles: true }));
    });
  };
  set("capture-namespace", "shop");
  set("capture-name", "checkout");
  const form = root.querySelector("#capture-namespace")?.closest("form");
  act(() => {
    form?.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await flush();
  const post = calls.find((c) => c.method === "POST");
  expect(post?.headers.get("X-Cluster-ID")).toBe("local");
  expect(JSON.parse(post?.body ?? "{}")).toEqual({
    namespace: "shop",
    kind: "Deployment",
    name: "checkout",
    sources: ["diagnostics", "object", "events"],
  });
  const status = root.querySelector('[aria-live="polite"]')?.textContent ?? "";
  expect(status).toContain("2 new items recorded");
  expect(status).toContain("Timed out");
  expect(status).toContain("source did not finish within its time limit");
});

// --- Review round 1: races, owner mutations, messages ------------------------

const findButton = (root: HTMLElement, label: string) =>
  [...root.querySelectorAll("button")].find((b) => b.textContent === label);
const clickButton = async (root: HTMLElement, label: string) => {
  act(() => findButton(root, label)?.click());
  await flush();
};
function setField(root: HTMLElement, id: string, value: string) {
  const el = root.querySelector(`#${id}`) as HTMLInputElement;
  act(() => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
function submitFormOf(root: HTMLElement, fieldId: string) {
  act(() => {
    root
      .querySelector(`#${fieldId}`)
      ?.closest("form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}
function gate() {
  let release: (r: Response) => void = () => {};
  const promise = new Promise<Response>((resolve) => {
    release = resolve;
  });
  return { promise, release };
}
const captureOk = () =>
  json(200, {
    data: {
      completeness: "complete",
      collectedAt: "2026-10-01T10:00:00Z",
      sources: [],
      collected: 0,
      inserted: 0,
      deduplicated: 0,
      dropped: 0,
    },
  });

test("the owner closes the incident with a PUT of the new status", async () => {
  stubFetch(routeDetail(), routeNotes(), routeGrants, (c) =>
    c.method === "PUT"
      ? json(200, { data: { incident: incident({ status: "closed" }) } })
      : undefined,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  await clickButton(root, "Close incident");
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.url).toBe(`/api/v1/incidents/${ID}`);
  expect(JSON.parse(put?.body ?? "{}")).toEqual({ status: "closed" });
  expect(findButton(root, "Reopen incident")).toBeDefined();
  expect(root.textContent).toContain("Reopen it to capture more evidence");
});

test("a reload that was in flight cannot revert a status change made after it", async () => {
  const reload = gate();
  let gets = 0;
  stubFetch(
    (c) => {
      if (c.method !== "GET" || !c.url.startsWith(`/api/v1/incidents/${ID}?`)) {
        return undefined;
      }
      gets++;
      return gets === 1 ? json(200, { data: detail() }) : reload.promise;
    },
    routeNotes(),
    routeGrants,
    (c) =>
      c.method === "POST" && c.url.endsWith("/capture")
        ? captureOk()
        : undefined,
    (c) =>
      c.method === "PUT"
        ? json(200, { data: { incident: incident({ status: "closed" }) } })
        : undefined,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  // A capture issues a first-page reload, held open here.
  setField(root, "capture-namespace", "shop");
  setField(root, "capture-name", "checkout");
  submitFormOf(root, "capture-namespace");
  await flush();
  expect(gets).toBe(2);
  await clickButton(root, "Close incident");
  expect(findButton(root, "Reopen incident")).toBeDefined();
  // The reload answers with the pre-change record.
  reload.release(json(200, { data: detail() }));
  await flush();
  expect(findButton(root, "Reopen incident")).toBeDefined();
  expect(findButton(root, "Close incident")).toBeUndefined();
});

const grant = (granteeId: string, canAnnotate = false) => ({
  incidentId: ID,
  granteeId,
  grantedBy: "alice",
  canAnnotate,
  createdAt: "2026-10-01T10:00:00Z",
});

test("sharing: add with canAnnotate, self-grant no-op, remove, and the grant limit", async () => {
  let posts = 0;
  stubFetch(routeDetail(), routeNotes(), routeGrants, (c) => {
    if (c.method === "POST" && c.url.endsWith("/grants")) {
      posts++;
      const body = JSON.parse(c.body ?? "{}");
      if (body.granteeId === "alice") {
        return new Response(null, { status: 204 });
      }
      if (posts === 3) {
        return json(409, {
          error: {
            code: 409,
            message: "incident grant limit reached",
            reason: "grant_limit_reached",
            extra: { max: 50 },
          },
        });
      }
      return json(201, { data: grant(body.granteeId, body.canAnnotate) });
    }
    if (c.method === "DELETE") return new Response(null, { status: 204 });
    return undefined;
  });
  const root = await mount(<IncidentWorkspace id={ID} />);

  setField(root, "grant-grantee", "bob");
  const annotate = [...root.querySelectorAll("label")]
    .find((l) => l.textContent?.trim() === "Can add notes")
    ?.querySelector("input") as HTMLInputElement;
  act(() => {
    annotate.checked = true;
    annotate.dispatchEvent(new Event("change", { bubbles: true }));
  });
  submitFormOf(root, "grant-grantee");
  await flush();
  const add = calls.filter((c) => c.method === "POST")[0];
  expect(add.url).toBe(`/api/v1/incidents/${ID}/grants`);
  expect(JSON.parse(add.body ?? "{}")).toEqual({
    granteeId: "bob",
    canAnnotate: true,
  });
  expect(root.querySelector("table")?.textContent).toContain("bob");

  setField(root, "grant-grantee", "alice");
  submitFormOf(root, "grant-grantee");
  await flush();
  expect(root.textContent).toContain("That is you");

  setField(root, "grant-grantee", "carol");
  submitFormOf(root, "grant-grantee");
  await flush();
  expect(root.textContent).toContain(
    "already shared with the maximum number of people (50)",
  );

  await clickButton(root, "Remove");
  const del = calls.find((c) => c.method === "DELETE");
  expect(del?.url).toBe(`/api/v1/incidents/${ID}/grants/bob`);
  expect(root.querySelector("table")).toBeNull();
  expect(root.textContent).toContain("Stopped sharing with bob");
});

test("a grants list issued before an add cannot overwrite the added grant", async () => {
  const first = gate();
  let lists = 0;
  stubFetch(routeDetail(), routeNotes(), (c) => {
    if (c.method === "GET" && c.url.endsWith("/grants")) {
      lists++;
      return lists === 1 ? first.promise : json(200, { data: [grant("bob")] });
    }
    if (c.method === "POST" && c.url.endsWith("/grants")) {
      return json(201, { data: grant("bob") });
    }
    return undefined;
  });
  const root = await mount(<IncidentWorkspace id={ID} />);
  setField(root, "grant-grantee", "bob");
  submitFormOf(root, "grant-grantee");
  await flush();
  expect(root.querySelector("table")?.textContent).toContain("bob");
  // The stale list (read before the add) answers empty.
  first.release(json(200, { data: [] }));
  await flush();
  expect(root.querySelector("table")?.textContent).toContain("bob");
  expect(lists).toBe(2);
});

test("a live link whose name now holds a different object reads as replaced", async () => {
  stubFetch((c) =>
    c.url === "/api/v1/resources/deployments/shop/checkout"
      ? json(200, { data: { metadata: { uid: "uid-2" } } })
      : undefined,
  );
  const root = await mount(
    <IncidentEvidenceTimeline
      items={[evidence(1, { mode: "live_link", payload: undefined })]}
    />,
  );
  expect(root.textContent).toContain("a different object now has this name");
  expect(root.querySelector('a[href^="/workloads/"]')).toBeNull();
});

test("live-link reads run at most six at a time", async () => {
  const held: Array<() => void> = [];
  stubFetch((c) =>
    c.url.startsWith("/api/v1/resources/")
      ? new Promise<Response>((resolve) => {
          held.push(() =>
            resolve(json(200, { data: { metadata: { uid: "uid-1" } } })),
          );
        })
      : undefined,
  );
  const items = Array.from({ length: 8 }, (_, i) =>
    evidence(i, { id: `l${i}`, mode: "live_link", payload: undefined }),
  );
  const root = await mount(<IncidentEvidenceTimeline items={items} />);
  expect(calls).toHaveLength(6);
  for (const release of held.splice(0)) release();
  await flush();
  expect(calls).toHaveLength(8);
  for (const release of held.splice(0)) release();
  await flush();
  expect(root.querySelectorAll('[data-live-state="present"]')).toHaveLength(8);
});

test("capture messages for a closed incident and the evidence limits", () => {
  const closed = new ApiError(409, 409, "x", {
    error: { reason: "incident_closed" },
  });
  expect(captureErrorText(closed)).toBe(
    "This incident is closed. Reopen it to capture evidence.",
  );
  const items = new ApiError(413, 413, "x", {
    error: {
      reason: "evidence_limit_exceeded",
      extra: { limit: "items", max: 500, current: 499, attempted: 3 },
    },
  });
  expect(captureErrorText(items)).toContain("evidence limit (items, max 500)");
  const scopes = new ApiError(409, 409, "x", {
    error: { reason: "scope_limit_exceeded", extra: { max: 20 } },
  });
  expect(captureErrorText(scopes)).toContain("distinct scopes it can (20)");
});

test("a failed evidence page says it could not load more and offers Retry", async () => {
  let pages = 0;
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.startsWith(`/api/v1/incidents/${ID}?`)
        ? json(200, {
            data: detail({}, [evidence(2)]),
            metadata: { continue: "c2" },
          })
        : undefined,
    (c) => {
      if (!c.url.includes("/evidence?")) return undefined;
      pages++;
      return pages === 1
        ? json(400, {
            error: { code: 400, message: "invalid continue cursor" },
          })
        : json(200, {
            data: {
              counts: { visible: 2, withheld: 0 },
              evidence: [evidence(1)],
              withheld: [],
            },
          });
    },
    routeNotes(),
    routeGrants,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  await clickButton(root, "Load more");
  expect(root.textContent).toContain("Could not load more evidence");
  expect(root.textContent).not.toContain("not a valid incident id");
  await clickButton(root, "Retry");
  expect(pages).toBe(2);
  expect(
    root.querySelectorAll("ol[aria-label='Evidence timeline'] > li"),
  ).toHaveLength(2);
});

// --- Review round 2 ---------------------------------------------------------

test("a reload issued while the status PUT is in flight cannot revert it", async () => {
  const put = gate();
  const reload = gate();
  let gets = 0;
  stubFetch(
    (c) => {
      if (c.method !== "GET" || !c.url.startsWith(`/api/v1/incidents/${ID}?`)) {
        return undefined;
      }
      gets++;
      return gets === 1 ? json(200, { data: detail() }) : reload.promise;
    },
    routeNotes(),
    routeGrants,
    (c) =>
      c.method === "POST" && c.url.endsWith("/capture")
        ? captureOk()
        : undefined,
    (c) => (c.method === "PUT" ? put.promise : undefined),
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  await clickButton(root, "Close incident");
  // While the PUT is pending, a capture issues a first-page reload.
  setField(root, "capture-namespace", "shop");
  setField(root, "capture-name", "checkout");
  submitFormOf(root, "capture-namespace");
  await flush();
  expect(gets).toBe(2);
  put.release(
    json(200, { data: { incident: incident({ status: "closed" }) } }),
  );
  await flush();
  expect(findButton(root, "Reopen incident")).toBeDefined();
  // The reload, issued mid-PUT, answers afterwards with the old record.
  reload.release(json(200, { data: detail() }));
  await flush();
  expect(findButton(root, "Reopen incident")).toBeDefined();
  expect(findButton(root, "Close incident")).toBeUndefined();
});

test('a ".." collaborator id is never sent as a revoke path', async () => {
  stubFetch(routeDetail(), routeNotes(), (c) =>
    c.method === "GET" && c.url.endsWith("/grants")
      ? json(200, { data: [grant("..")] })
      : undefined,
  );
  const root = await mount(<IncidentWorkspace id={ID} />);
  // The sharing panel mounts with the incident and then reads its grants.
  await flush();
  expect(findButton(root, "Remove")).toBeDefined();
  await clickButton(root, "Remove");
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  expect(root.textContent).toContain(
    "This collaborator id cannot be removed from the UI.",
  );
});

test("an unmounted timeline frees its live-link slots and drops its queued reads", async () => {
  const held: Array<() => void> = [];
  stubFetch();
  // Reads that never answer on their own, but reject on abort like fetch.
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), method: "GET", headers: new Headers() });
    return new Promise<Response>((resolve, reject) => {
      init?.signal?.addEventListener("abort", () =>
        reject(new DOMException("Aborted", "AbortError")),
      );
      held.push(() =>
        resolve(json(200, { data: { metadata: { uid: "uid-1" } } })),
      );
    });
  }) as typeof globalThis.fetch;
  const links = (prefix: string, n: number) =>
    Array.from({ length: n }, (_, i) =>
      evidence(i, {
        id: `${prefix}${i}`,
        mode: "live_link",
        payload: undefined,
        source: { ...evidence(i).source, name: `${prefix}${i}` },
      }),
    );
  // Six reads in flight and two queued, then the timeline goes away.
  await mount(<IncidentEvidenceTimeline items={links("a", 8)} />);
  expect(calls).toHaveLength(6);
  act(() => render(null, host as HTMLElement));
  await flush();
  // Aborted in-flight reads released their slots, and the queued two never
  // ran: a new timeline gets all six slots at once.
  act(() =>
    render(
      <IncidentEvidenceTimeline items={links("b", 6)} />,
      host as HTMLElement,
    ),
  );
  await flush();
  expect(calls).toHaveLength(12);
  // Every read after the remount is one of the new timeline's: the two
  // queued "a" reads were dropped, not started.
  expect(calls.slice(6).every((c) => c.url.includes("/shop/b"))).toBe(true);
  for (const release of held.splice(0)) release();
  await flush();
});

// --- Review round 3 ---------------------------------------------------------

test("a hung live-link read times out after 10s, shows it, and frees its slot", async () => {
  // Microtask-only flush: setTimeout is faked for this test.
  const settle = () =>
    act(async () => {
      for (let i = 0; i < 20; i++) await Promise.resolve();
    });
  stubFetch();
  // Reads that never answer; like fetch, they reject when aborted.
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(input), method: "GET", headers: new Headers() });
    return new Promise<Response>((_, reject) => {
      init?.signal?.addEventListener("abort", () =>
        reject(new DOMException("Aborted", "AbortError")),
      );
    });
  }) as typeof globalThis.fetch;
  jest.useFakeTimers();
  try {
    const items = Array.from({ length: 7 }, (_, i) =>
      evidence(i, {
        id: `t${i}`,
        mode: "live_link",
        payload: undefined,
        source: { ...evidence(i).source, name: `t${i}` },
      }),
    );
    host = document.createElement("div");
    document.body.appendChild(host);
    act(() =>
      render(<IncidentEvidenceTimeline items={items} />, host as HTMLElement),
    );
    await settle();
    expect(calls).toHaveLength(6);
    expect(host.querySelectorAll('[data-live-state="checking"]')).toHaveLength(
      7,
    );

    jest.advanceTimersByTime(9_999);
    await settle();
    expect(calls).toHaveLength(6);

    jest.advanceTimersByTime(1);
    await settle();
    // The six hung reads gave up and say so; the queued seventh started.
    expect(host.querySelectorAll('[data-live-state="error"]')).toHaveLength(6);
    expect(host.textContent).toContain(
      "The live object could not be checked right now.",
    );
    expect(calls).toHaveLength(7);
    expect(calls[6].url).toContain("/shop/t6");

    // The seventh read times out on its own clock too.
    jest.advanceTimersByTime(10_000);
    await settle();
    expect(host.querySelectorAll('[data-live-state="error"]')).toHaveLength(7);
  } finally {
    jest.useRealTimers();
  }
});
