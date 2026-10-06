/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
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
const { default: IncidentNotes } = await import("./IncidentNotes.tsx");
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
type Route = (call: Call) => Response | undefined;

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

// --- Notes -------------------------------------------------------------------

const note: NoteView = {
  id: "00000000-0000-4000-8000-0000000000a1",
  incidentId: ID,
  authorId: "alice",
  body: "first take",
  revision: 1,
  createdAt: "2026-10-01T10:00:00Z",
  updatedAt: "2026-10-01T10:00:00Z",
};

test("a revision conflict keeps the draft, explains, and offers a reload", async () => {
  let saved: Call | undefined;
  stubFetch(routeNotes([note]), (c) => {
    if (c.method !== "PUT") return undefined;
    saved = c;
    return json(409, {
      error: {
        code: 409,
        message: "note was modified since it was read",
        reason: "note_revision_conflict",
        extra: { currentRevision: 2 },
      },
    });
  });
  const root = await mount(
    <IncidentNotes incidentId={ID} canAnnotate currentUserId="alice" />,
  );
  const edit = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Edit",
  );
  act(() => edit?.click());
  const area = root.querySelector(
    "textarea#note-edit-00000000-0000-4000-8000-0000000000a1",
  ) as HTMLTextAreaElement;
  act(() => {
    area.value = "my careful rewrite";
    area.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const save = [...root.querySelectorAll("button")].find(
    (b) => b.textContent === "Save",
  );
  act(() => save?.click());
  await flush();
  expect(JSON.parse(saved?.body ?? "{}")).toEqual({
    body: "my careful rewrite",
    revision: 1,
  });
  expect(root.textContent).toContain("revision 2");
  const kept = root.querySelector(
    "textarea#note-edit-00000000-0000-4000-8000-0000000000a1",
  ) as HTMLTextAreaElement;
  expect(kept.value).toBe("my careful rewrite");
  expect(
    [...root.querySelectorAll("button")].some(
      (b) => b.textContent === "Reload notes",
    ),
  ).toBe(true);
});

test("another author's note offers no edit or delete", async () => {
  stubFetch(routeNotes([note]));
  const root = await mount(
    <IncidentNotes incidentId={ID} canAnnotate currentUserId="bob" />,
  );
  const labels = [...root.querySelectorAll("button")].map((b) => b.textContent);
  expect(labels).not.toContain("Edit");
  expect(labels).not.toContain("Delete");
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
  expect(root.textContent).toContain("Could not load the incident's evidence.");
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
