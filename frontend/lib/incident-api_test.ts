import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { h, render } from "preact";
import { act } from "preact/test-utils";
import { ApiError, setAccessToken } from "./api.ts";
import { switchCluster } from "./cluster.ts";
import {
  addGrant,
  buildIncidentPageQuery,
  captureEvidence,
  createIncident,
  createNote,
  deleteIncident,
  deleteNote,
  exportUrl,
  getIncident,
  incidentErrorNumber,
  isPersistenceUnavailable,
  isRemoteCaptureRefusal,
  listEvidence,
  listGrants,
  listIncidents,
  listNotes,
  removeGrant,
  updateIncident,
  updateNote,
} from "./incident-api.ts";
import type {
  CaptureResponse,
  EvidenceItem,
  GrantView,
  IncidentDetail,
  IncidentView,
  NoteView,
} from "./incident-types.ts";

// The client is a thin typing layer over api.ts, so what is worth pinning is
// the request it puts on the wire (path, method, body, X-Cluster-ID, signal)
// and how it unwraps the envelope. Only fetch is replaced.
//
// The IncidentList island's tests live at the bottom of this file rather than
// in their own IncidentList_test.tsx: the unit's five-file budget (types, API
// client, this test, the island, the page) has no room for a sixth. They use
// `h` instead of JSX because this is a .ts file.

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

interface Recorded {
  url: string;
  method: string;
  clusterHeader: string | null;
  csrfHeader: string | null;
  signal: AbortSignal | null | undefined;
  body: string | null;
}

interface Reply {
  status: number;
  payload?: unknown;
  /** When set, the response is held until this settles (an in-flight request). */
  gate?: Promise<unknown>;
}

/** A promise the test resolves by hand. */
function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve = () => {};
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

const ID = "6f1d3c52-4b1e-4f0a-9c53-0d7a2b8e1f64";
const NOTE = "0b8e2f7a-1c3d-4e5f-8a9b-0c1d2e3f4a5b";

let calls: Recorded[] = [];
let original: typeof globalThis.fetch | undefined;
let host: HTMLElement | null = null;

/** Replies are served in order; the last one repeats. */
function stubFetch(...replies: Reply[]) {
  calls = [];
  original ??= globalThis.fetch;
  let i = 0;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    calls.push({
      url: String(input),
      method: init?.method ?? "GET",
      clusterHeader: headers.get("X-Cluster-ID"),
      csrfHeader: headers.get("X-Requested-With"),
      signal: init?.signal,
      body: typeof init?.body === "string" ? init.body : null,
    });
    const reply = replies[Math.min(i++, replies.length - 1)];
    const respond = () =>
      reply.status === 204
        ? new Response(null, { status: 204 })
        : new Response(JSON.stringify(reply.payload ?? {}), {
            status: reply.status,
            headers: { "Content-Type": "application/json" },
          });
    return reply.gate ? reply.gate.then(respond) : Promise.resolve(respond());
  }) as typeof globalThis.fetch;
}

/** The real `location.assign`, restored after a test that spied on it. */
let originalAssign: Location["assign"] | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (original) globalThis.fetch = original;
  original = undefined;
  if (originalAssign) globalThis.location.assign = originalAssign;
  originalAssign = undefined;
  setAccessToken(null);
  switchCluster("local", "local");
});

function incident(n: number, over: Partial<IncidentView> = {}): IncidentView {
  return {
    id: `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`,
    ownerId: "alice",
    clusterId: "local",
    title: `Incident ${n}`,
    summary: "",
    status: "open",
    windowStart: "2026-10-01T09:00:00Z",
    retentionDays: 30,
    createdAt: "2026-10-01T10:00:00Z",
    updatedAt: "2026-10-01T11:00:00Z",
    role: "owner",
    canAnnotate: true,
    ...over,
  };
}

function note(over: Partial<NoteView> = {}): NoteView {
  return {
    id: NOTE,
    incidentId: ID,
    authorId: "alice",
    body: "rolled back",
    revision: 2,
    createdAt: "2026-10-01T10:00:00Z",
    updatedAt: "2026-10-01T10:05:00Z",
    ...over,
  };
}

function apiError(status: number, reason: string, extra?: object) {
  return {
    status,
    payload: { error: { code: status, message: "refused", reason, extra } },
  };
}

async function rejection(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    if (e instanceof ApiError) return e;
    throw e;
  }
  throw new Error("expected the call to reject");
}

// --- Query building ----------------------------------------------------------

test("buildIncidentPageQuery: empty, limit, continue, both, empty cursor", () => {
  expect(buildIncidentPageQuery()).toBe("");
  expect(buildIncidentPageQuery({})).toBe("");
  expect(buildIncidentPageQuery({ limit: 25 })).toBe("?limit=25");
  expect(buildIncidentPageQuery({ continue: "c/1+=" })).toBe(
    "?continue=c%2F1%2B%3D",
  );
  expect(buildIncidentPageQuery({ limit: 10, continue: "abc" })).toBe(
    "?limit=10&continue=abc",
  );
  // An empty cursor is "no more pages", never sent as `continue=`.
  expect(buildIncidentPageQuery({ continue: "" })).toBe("");
});

// --- Cluster pinning -----------------------------------------------------------

test("incident reads and writes are pinned to the local cluster, not the selection", async () => {
  switchCluster("cluster-remote", "gen-r");
  stubFetch({ status: 200, payload: { data: [] } });
  await listIncidents();
  await getIncident(ID);
  await createNote(ID, "x");
  await deleteIncident(ID);
  for (const c of calls) expect(c.clusterHeader).toBe("local");
});

test("captureEvidence sends the cluster the caller names, so a remote target is refused", async () => {
  switchCluster("local", "local");
  stubFetch(
    apiError(400, "remote_capture_unsupported", {
      selectedCluster: "cluster-remote",
    }),
  );
  const err = await rejection(
    captureEvidence(ID, "cluster-remote", {
      namespace: "shop",
      kind: "Deployment",
      name: "web",
    }),
  );
  expect(calls[0].clusterHeader).toBe("cluster-remote");
  expect(err.status).toBe(400);
  expect(err.reason).toBe("remote_capture_unsupported");
});

// --- Incidents ---------------------------------------------------------------

test("listIncidents unwraps items and the continue cursor and forwards the signal", async () => {
  stubFetch({
    status: 200,
    payload: { data: [incident(1)], metadata: { total: 1, continue: "next" } },
  });
  const controller = new AbortController();
  const page = await listIncidents(
    { limit: 50, continue: "c1" },
    controller.signal,
  );
  expect(calls[0].url).toBe("/api/v1/incidents?limit=50&continue=c1");
  expect(calls[0].method).toBe("GET");
  expect(calls[0].signal).toBe(controller.signal);
  expect(page.items).toEqual([incident(1)]);
  expect(page.continue).toBe("next");
});

test("listIncidents: no cursor in metadata means the last page", async () => {
  stubFetch({ status: 200, payload: { data: [], metadata: { total: 0 } } });
  const page = await listIncidents();
  expect(calls[0].url).toBe("/api/v1/incidents");
  expect(page.items).toEqual([]);
  expect(page.continue).toBeUndefined();
});

test("createIncident POSTs the body with CSRF and returns the summary", async () => {
  const summary = {
    incident: incident(1),
    counts: { visible: 0, withheld: 0 },
  };
  stubFetch({ status: 201, payload: { data: summary } });
  const body = {
    title: "Checkout down",
    summary: "",
    windowStart: "2026-10-01T09:00:00.000Z",
  };
  const res = await createIncident(body);
  expect(calls[0].url).toBe("/api/v1/incidents");
  expect(calls[0].method).toBe("POST");
  expect(calls[0].csrfHeader).toBe("XMLHttpRequest");
  expect(JSON.parse(calls[0].body ?? "")).toEqual(body);
  expect(res).toEqual(summary);
});

test("getIncident pages evidence through the query and returns detail plus cursor", async () => {
  const detail: IncidentDetail = {
    incident: incident(1),
    counts: { visible: 1, withheld: 1 },
    evidence: [],
    withheld: [
      {
        id: "w1",
        evidenceKind: "event_list",
        collectedAt: "2026-10-01T10:00:00Z",
        withheld: true,
        withheldReason: "forbidden",
      },
    ],
  };
  stubFetch({
    status: 200,
    payload: { data: detail, metadata: { total: 1, continue: "ev2" } },
  });
  const res = await getIncident(`${ID}/../x`, { limit: 20 });
  // The id is path-encoded: it cannot climb out of /incidents/{id}.
  expect(calls[0].url).toBe(
    `/api/v1/incidents/${encodeURIComponent(`${ID}/../x`)}?limit=20`,
  );
  expect(res.detail).toEqual(detail);
  expect(res.continue).toBe("ev2");
});

test("updateIncident PUTs only the fields given", async () => {
  stubFetch({
    status: 200,
    payload: { data: { incident: incident(1, { status: "closed" }) } },
  });
  const res = await updateIncident(ID, { status: "closed" });
  expect(calls[0].method).toBe("PUT");
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}`);
  expect(JSON.parse(calls[0].body ?? "")).toEqual({ status: "closed" });
  expect(res.incident.status).toBe("closed");
  expect(res.counts).toBeUndefined();
});

test("deleteIncident sends DELETE and resolves on 204", async () => {
  stubFetch({ status: 204 });
  await deleteIncident(ID);
  expect(calls[0].method).toBe("DELETE");
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}`);
  expect(calls[0].csrfHeader).toBe("XMLHttpRequest");
});

// --- Evidence and capture -------------------------------------------------------

test("listEvidence returns the page and cursor; items narrow on withheld", async () => {
  stubFetch({
    status: 200,
    payload: {
      data: {
        counts: { visible: 1, withheld: 1 },
        evidence: [
          {
            id: "e1",
            incidentId: ID,
            evidenceKind: "object_summary",
            mode: "snapshot",
            source: {
              clusterId: "local",
              apiGroup: "apps",
              resource: "deployments",
              kind: "Deployment",
              namespace: "shop",
              name: "web",
            },
            collectedAt: "2026-10-01T10:00:00Z",
            completeness: "complete",
            redaction: {
              applied: true,
              fieldsRemoved: 1,
              truncated: false,
              secretDerived: false,
            },
            payloadBytes: 12,
          },
        ],
        withheld: [
          {
            id: "w1",
            evidenceKind: "event_list",
            collectedAt: "2026-10-01T10:00:00Z",
            withheld: true,
            withheldReason: "authorization_check_unavailable",
          },
        ],
      },
      metadata: { total: 1, continue: "p2" },
    },
  });
  const res = await listEvidence(ID, { continue: "p1" });
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/evidence?continue=p1`);
  expect(res.continue).toBe("p2");
  const items: EvidenceItem[] = [...res.page.evidence, ...res.page.withheld];
  const scopes = items.map((item) =>
    item.withheld ? `withheld:${item.withheldReason}` : item.source.namespace,
  );
  expect(scopes).toEqual(["shop", "withheld:authorization_check_unavailable"]);
});

test("captureEvidence POSTs only namespace, kind, name and sources", async () => {
  const report: CaptureResponse = {
    completeness: "partial",
    collectedAt: "2026-10-01T10:00:00Z",
    sources: [{ id: "events", completeness: "timed_out", items: 0 }],
    collected: 2,
    inserted: 1,
    deduplicated: 1,
    dropped: 0,
  };
  stubFetch({ status: 200, payload: { data: report } });
  const res = await captureEvidence(ID, "local", {
    namespace: "shop",
    kind: "Pod",
    name: "web-0",
    sources: ["events"],
  });
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/capture`);
  expect(calls[0].method).toBe("POST");
  expect(calls[0].clusterHeader).toBe("local");
  expect(JSON.parse(calls[0].body ?? "")).toEqual({
    namespace: "shop",
    kind: "Pod",
    name: "web-0",
    sources: ["events"],
  });
  expect(res).toEqual(report);
});

test("capture error reasons and numeric extras propagate", async () => {
  stubFetch(
    apiError(413, "evidence_limit_exceeded", {
      limit: "items",
      max: 500,
      current: 499,
      attempted: 3,
    }),
  );
  const err = await rejection(
    captureEvidence(ID, "local", { namespace: "a", kind: "Pod", name: "b" }),
  );
  expect(err.status).toBe(413);
  expect(err.reason).toBe("evidence_limit_exceeded");
  expect(incidentErrorNumber(err, "max")).toBe(500);
  expect(incidentErrorNumber(err, "attempted")).toBe(3);
  // A string extra is not a number.
  expect(incidentErrorNumber(err, "limit")).toBeUndefined();

  for (const reason of [
    "incident_busy",
    "incident_capture_outcome_unknown",
    "incident_closed",
    "scope_limit_exceeded",
  ]) {
    stubFetch(apiError(reason === "incident_busy" ? 503 : 409, reason));
    const e = await rejection(
      captureEvidence(ID, "local", { namespace: "a", kind: "Pod", name: "b" }),
    );
    expect(e.reason).toBe(reason);
  }
});

// --- Notes -------------------------------------------------------------------

test("listNotes follows the continue cursor", async () => {
  stubFetch({
    status: 200,
    payload: { data: [note()], metadata: { total: 1, continue: "n2" } },
  });
  const res = await listNotes(ID, { limit: 100, continue: "n1" });
  expect(calls[0].url).toBe(
    `/api/v1/incidents/${ID}/notes?limit=100&continue=n1`,
  );
  expect(res.items).toEqual([note()]);
  expect(res.continue).toBe("n2");
});

test("createNote POSTs the body only", async () => {
  stubFetch({ status: 201, payload: { data: note({ revision: 1 }) } });
  const res = await createNote(ID, "rolled back");
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/notes`);
  expect(JSON.parse(calls[0].body ?? "")).toEqual({ body: "rolled back" });
  expect(res.revision).toBe(1);
});

test("updateNote PUTs body and revision; a conflict carries the current revision", async () => {
  stubFetch({ status: 200, payload: { data: note({ revision: 3 }) } });
  const res = await updateNote(ID, NOTE, "edited", 2);
  expect(calls[0].method).toBe("PUT");
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/notes/${NOTE}`);
  expect(JSON.parse(calls[0].body ?? "")).toEqual({
    body: "edited",
    revision: 2,
  });
  expect(res.revision).toBe(3);

  stubFetch(apiError(409, "note_revision_conflict", { currentRevision: 5 }));
  const err = await rejection(updateNote(ID, NOTE, "edited", 2));
  expect(err.reason).toBe("note_revision_conflict");
  expect(incidentErrorNumber(err, "currentRevision")).toBe(5);
});

test("deleteNote sends DELETE to the note path", async () => {
  stubFetch({ status: 204 });
  await deleteNote(ID, NOTE);
  expect(calls[0].method).toBe("DELETE");
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/notes/${NOTE}`);
});

// --- Grants ------------------------------------------------------------------

const grant: GrantView = {
  incidentId: ID,
  granteeId: "bob",
  grantedBy: "alice",
  canAnnotate: false,
  createdAt: "2026-10-01T10:00:00Z",
};

test("listGrants returns the array", async () => {
  stubFetch({
    status: 200,
    payload: { data: [grant], metadata: { total: 1 } },
  });
  const res = await listGrants(ID);
  expect(calls[0].url).toBe(`/api/v1/incidents/${ID}/grants`);
  expect(res).toEqual([grant]);
});

test("addGrant returns the grant on 201 and null on the self-grant 204", async () => {
  stubFetch({ status: 201, payload: { data: grant } });
  const res = await addGrant(ID, { granteeId: "bob", canAnnotate: false });
  expect(calls[0].method).toBe("POST");
  expect(JSON.parse(calls[0].body ?? "")).toEqual({
    granteeId: "bob",
    canAnnotate: false,
  });
  expect(res).toEqual(grant);

  stubFetch({ status: 204 });
  expect(await addGrant(ID, { granteeId: "alice", canAnnotate: true })).toBe(
    null,
  );

  stubFetch(apiError(409, "grant_limit_reached", { max: 50 }));
  const err = await rejection(
    addGrant(ID, { granteeId: "carol", canAnnotate: false }),
  );
  expect(err.reason).toBe("grant_limit_reached");
  expect(incidentErrorNumber(err, "max")).toBe(50);
});

test("removeGrant percent-encodes a grantee id containing / and %", async () => {
  stubFetch({ status: 204 });
  await removeGrant(ID, "oidc|team/a 50%");
  expect(calls[0].method).toBe("DELETE");
  expect(calls[0].url).toBe(
    `/api/v1/incidents/${ID}/grants/oidc%7Cteam%2Fa%2050%25`,
  );
});

// --- Export ------------------------------------------------------------------

test("exportUrl builds the download path for both formats", () => {
  expect(exportUrl(ID, "json")).toBe(
    `/api/v1/incidents/${ID}/export?format=json`,
  );
  expect(exportUrl(ID, "markdown")).toBe(
    `/api/v1/incidents/${ID}/export?format=markdown`,
  );
});

// --- Error helpers -------------------------------------------------------------

test("isPersistenceUnavailable recognizes only the no-database reason", async () => {
  stubFetch(
    apiError(503, "incident_persistence_unavailable", {
      requires: "postgresql",
    }),
  );
  const noDb = await rejection(listIncidents());
  expect(isPersistenceUnavailable(noDb)).toBe(true);

  stubFetch(apiError(503, "incident_store_unavailable"));
  const storeDown = await rejection(listIncidents());
  expect(isPersistenceUnavailable(storeDown)).toBe(false);
  expect(isPersistenceUnavailable(new Error("x"))).toBe(false);
});

test("isRemoteCaptureRefusal: the admin 400 and the middleware's reason-less 403", async () => {
  const target = { namespace: "a", kind: "Pod", name: "b" } as const;

  // Admin: the handler's own refusal.
  stubFetch(apiError(400, "remote_capture_unsupported"));
  const admin = await rejection(captureEvidence(ID, "cluster-r", target));
  expect(isRemoteCaptureRefusal(admin, "cluster-r")).toBe(true);

  // Non-admin: ClusterContext answers before the handler, with no reason.
  stubFetch({
    status: 403,
    payload: {
      error: {
        code: 403,
        message: "admin role required for remote cluster access",
      },
    },
  });
  const nonAdmin = await rejection(captureEvidence(ID, "cluster-r", target));
  expect(isRemoteCaptureRefusal(nonAdmin, "cluster-r")).toBe(true);
  // The same 403 on a local capture is the owner-only gate, not a remote refusal.
  expect(isRemoteCaptureRefusal(nonAdmin, "local")).toBe(false);
  expect(isRemoteCaptureRefusal(nonAdmin, "")).toBe(false);

  // A 403 with a reason, other failures and non-ApiErrors are not it.
  stubFetch(apiError(403, "something_else"));
  const reasoned = await rejection(captureEvidence(ID, "cluster-r", target));
  expect(isRemoteCaptureRefusal(reasoned, "cluster-r")).toBe(false);
  stubFetch(apiError(409, "incident_closed"));
  const closed = await rejection(captureEvidence(ID, "cluster-r", target));
  expect(isRemoteCaptureRefusal(closed, "cluster-r")).toBe(false);
  expect(isRemoteCaptureRefusal(new Error("x"), "cluster-r")).toBe(false);
});

// =============================================================================
// IncidentList island
// =============================================================================

const { default: IncidentList } = await import(
  "@/src/islands/IncidentList.tsx"
);

// The repo has no shared wait-for-condition test utility (ChangeReceipt_test
// keeps its own `until`), so this file does the same: `until` waits for a
// condition that must become true, and `flush` (a few macrotask turns) is kept
// for asserting that something did NOT happen, where there is no condition to
// wait for.
const flush = () =>
  act(async () => {
    for (let i = 0; i < 4; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function until(done: () => boolean, limitMs = 500): Promise<void> {
  const deadline = Date.now() + limitMs;
  while (!done()) {
    if (Date.now() > deadline) throw new Error("condition never held");
    await act(async () => {
      await new Promise((r) => setTimeout(r, 2));
    });
  }
}

/** Records navigations instead of performing them; afterEach restores. */
function spyNavigation(): string[] {
  const visited: string[] = [];
  originalAssign ??= globalThis.location.assign;
  globalThis.location.assign = (href: string | URL) => {
    visited.push(String(href));
  };
  return visited;
}

async function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(h(IncidentList, {}), host as HTMLElement));
  await flush();
  return host;
}

function setValue(el: Element | null, value: string) {
  const input = el as HTMLInputElement | HTMLTextAreaElement;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

test("IncidentList: lists incidents with status, role and a detail link", async () => {
  stubFetch({
    status: 200,
    payload: {
      data: [
        incident(1),
        incident(2, {
          role: "collaborator",
          canAnnotate: false,
          status: "closed",
        }),
      ],
      metadata: { total: 2 },
    },
  });
  const root = await mount();
  expect(calls[0].url).toBe("/api/v1/incidents?limit=50");
  expect(calls[0].clusterHeader).toBe("local");
  const rows = root.querySelectorAll("tbody tr");
  expect(rows).toHaveLength(2);
  expect(rows[0].querySelector("a")?.getAttribute("href")).toBe(
    `/observability/incidents/${incident(1).id}`,
  );
  expect(rows[0].textContent).toContain("Open");
  expect(rows[0].textContent).toContain("Owner");
  expect(rows[1].textContent).toContain("Closed");
  expect(rows[1].textContent).toContain("Collaborator");
  expect(rows[1].textContent).toContain("read-only");
  // Last page: no "Load more".
  expect(root.textContent).not.toContain("Load more");
});

test("IncidentList: an empty list says so and offers to create one", async () => {
  stubFetch({ status: 200, payload: { data: [], metadata: { total: 0 } } });
  const root = await mount();
  expect(root.textContent).toContain("No incidents yet");
  const create = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("New incident"),
  );
  expect(create).toBeDefined();
});

test("IncidentList: no database reads as unavailable, never as 'no incidents'", async () => {
  stubFetch(
    apiError(503, "incident_persistence_unavailable", {
      requires: "postgresql",
    }),
  );
  const root = await mount();
  expect(root.textContent).toContain("no database");
  expect(root.textContent).not.toContain("No incidents yet");
  // Nothing to create into, and nothing to retry.
  const labels = [...root.querySelectorAll("button")].map((b) => b.textContent);
  expect(labels.some((l) => l?.includes("New incident"))).toBe(false);
  expect(labels.some((l) => l?.includes("Retry"))).toBe(false);
});

test("IncidentList: a store failure is retryable and distinct from no database", async () => {
  stubFetch(apiError(503, "incident_store_unavailable"), {
    status: 200,
    payload: { data: [incident(1)] },
  });
  const root = await mount();
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "could not be reached",
  );
  expect(root.textContent).not.toContain("no database");
  const retry = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("Retry"),
  );
  act(() => retry?.click());
  await flush();
  expect(calls).toHaveLength(2);
  expect(root.querySelectorAll("tbody tr")).toHaveLength(1);
});

test("IncidentList: Load more follows the continue cursor and appends", async () => {
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "cur/2" } },
    },
    { status: 200, payload: { data: [incident(2)] } },
  );
  const root = await mount();
  const more = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("Load more"),
  );
  expect(more).toBeDefined();
  act(() => more?.click());
  await flush();
  expect(calls[1].url).toBe("/api/v1/incidents?limit=50&continue=cur%2F2");
  expect(root.querySelectorAll("tbody tr")).toHaveLength(2);
  expect(root.textContent).not.toContain("Load more");
});

test("IncidentList: New incident creates with the window and navigates to it", async () => {
  const created = incident(7);
  stubFetch(
    { status: 200, payload: { data: [] } },
    { status: 201, payload: { data: { incident: created } } },
  );
  const visited = spyNavigation();
  const root = await mount();
  const open = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("New incident"),
  );
  act(() => open?.click());
  await flush();

  const form = root.querySelector("form");
  expect(form).not.toBeNull();
  const title = form?.querySelector("#incident-title");
  // Focus moves into the form when it opens.
  expect(document.activeElement).toBe(title ?? null);
  setValue(title ?? null, "Checkout down");
  setValue(form?.querySelector("#incident-summary") ?? null, "5xx on /pay");
  setValue(
    form?.querySelector("#incident-window-start") ?? null,
    "2026-10-01T09:30",
  );
  await flush();
  act(() => {
    form?.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await flush();

  expect(calls[1].method).toBe("POST");
  expect(calls[1].url).toBe("/api/v1/incidents");
  const body = JSON.parse(calls[1].body ?? "");
  expect(body.title).toBe("Checkout down");
  expect(body.summary).toBe("5xx on /pay");
  // datetime-local is local wall time; the wire carries the same instant.
  expect(body.windowStart).toBe(new Date("2026-10-01T09:30").toISOString());
  // An empty end is an ongoing incident: the field is left out.
  expect("windowEnd" in body).toBe(false);
  expect(visited).toEqual([`/observability/incidents/${created.id}`]);
});

test("IncidentList: a window end before its start is refused before any request", async () => {
  stubFetch({ status: 200, payload: { data: [] } });
  const visited = spyNavigation();
  const root = await mount();
  const open = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("New incident"),
  );
  act(() => open?.click());
  await flush();
  const form = root.querySelector("form");
  setValue(form?.querySelector("#incident-title") ?? null, "x");
  setValue(
    form?.querySelector("#incident-window-start") ?? null,
    "2026-10-01T10:00",
  );
  setValue(
    form?.querySelector("#incident-window-end") ?? null,
    "2026-10-01T09:00",
  );
  await flush();
  act(() => {
    form?.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await flush();
  expect(calls).toHaveLength(1);
  expect(visited).toEqual([]);
  expect(root.textContent).toContain("end must not be before its start");
});

test("IncidentList: a create error is shown in the form and nothing navigates", async () => {
  stubFetch(
    { status: 200, payload: { data: [] } },
    {
      status: 400,
      payload: {
        error: {
          code: 400,
          message: "invalid incident input",
          detail: "incident invalid: title must be 1..200 characters",
        },
      },
    },
  );
  const visited = spyNavigation();
  const root = await mount();
  const open = [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("New incident"),
  );
  act(() => open?.click());
  await flush();
  const form = root.querySelector("form");
  setValue(form?.querySelector("#incident-title") ?? null, "x");
  await flush();
  act(() => {
    form?.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await flush();
  expect(visited).toEqual([]);
  expect(form?.querySelector('[role="alert"]')?.textContent).toContain(
    "title must be 1..200 characters",
  );
});

// --- IncidentList: review round 1 -------------------------------------------

function button(
  root: ParentNode,
  label: string,
): HTMLButtonElement | undefined {
  return [...root.querySelectorAll("button")].find((b) =>
    b.textContent?.includes(label),
  );
}

async function openForm(root: HTMLElement): Promise<HTMLFormElement> {
  act(() => button(root, "New incident")?.click());
  await until(() => root.querySelector("form") !== null);
  return root.querySelector("form") as HTMLFormElement;
}

function submit(form: HTMLFormElement) {
  act(() => {
    form.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
}

const listCalls = () => calls.filter((c) => c.method === "GET");

test("IncidentList: Load more after a failed page re-requests the same cursor and appends", async () => {
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "p2" } },
    },
    apiError(503, "incident_store_unavailable"),
    { status: 200, payload: { data: [incident(2)] } },
  );
  const root = await mount();
  act(() => button(root, "Load more")?.click());
  await until(() => root.querySelector('[role="alert"]') !== null);
  // The failed page keeps the rows already shown and offers Retry.
  expect(root.querySelectorAll("tbody tr")).toHaveLength(1);
  expect(button(root, "Retry")).toBeDefined();
  expect(button(root, "Load more")).toBeDefined();

  // Load more again: the identical request is issued, not swallowed.
  act(() => button(root, "Load more")?.click());
  await until(() => root.querySelectorAll("tbody tr").length === 2);
  expect(listCalls().map((c) => c.url)).toEqual([
    "/api/v1/incidents?limit=50",
    "/api/v1/incidents?limit=50&continue=p2",
    "/api/v1/incidents?limit=50&continue=p2",
  ]);
  expect(root.querySelector('[role="alert"]')).toBeNull();
  expect(button(root, "Retry")).toBeUndefined();
});

test("IncidentList: Retry after a failed page re-requests the same cursor and appends", async () => {
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "p2" } },
    },
    apiError(503, "incident_store_unavailable"),
    { status: 200, payload: { data: [incident(2)] } },
  );
  const root = await mount();
  act(() => button(root, "Load more")?.click());
  await until(() => button(root, "Retry") !== undefined);
  act(() => button(root, "Retry")?.click());
  await until(() => root.querySelectorAll("tbody tr").length === 2);
  expect(listCalls()[2].url).toBe("/api/v1/incidents?limit=50&continue=p2");
});

test("IncidentList: a retry clears the stale error while it runs; Retry is inert while busy", async () => {
  const gate = deferred();
  stubFetch(apiError(503, "incident_store_unavailable"), {
    status: 200,
    payload: { data: [incident(1)] },
    gate: gate.promise,
  });
  const root = await mount();
  expect(root.querySelector('[role="alert"]')).not.toBeNull();

  const retry = button(root, "Retry");
  act(() => retry?.click());
  await until(() => listCalls().length === 2);
  // In flight: the old message is gone, Retry stays mounted but inactive.
  expect(root.querySelector('[role="alert"]')).toBeNull();
  expect(button(root, "Retry")?.getAttribute("aria-disabled")).toBe("true");
  expect(root.textContent).toContain("Retrying");
  act(() => button(root, "Retry")?.click());
  await flush();
  expect(listCalls()).toHaveLength(2);

  gate.resolve();
  await until(() => root.querySelectorAll("tbody tr").length === 1);
  expect(button(root, "Retry")).toBeUndefined();
});

test("IncidentList: Load more is inert while its page is loading", async () => {
  const gate = deferred();
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "p2" } },
    },
    { status: 200, payload: { data: [incident(2)] }, gate: gate.promise },
  );
  const root = await mount();
  act(() => button(root, "Load more")?.click());
  await until(() => listCalls().length === 2);
  const more = button(root, "Load more");
  expect(more?.getAttribute("aria-disabled")).toBe("true");
  act(() => more?.click());
  await flush();
  expect(listCalls()).toHaveLength(2);
  gate.resolve();
  await until(() => root.querySelectorAll("tbody tr").length === 2);
});

test("IncidentList: busy list reads say so", async () => {
  stubFetch(apiError(503, "incident_busy"));
  const root = await mount();
  expect(root.querySelector('[role="alert"]')?.textContent).toContain("busy");
});

test("IncidentList: an unknown status renders neutrally; a collaborator who may annotate is not read-only", async () => {
  stubFetch({
    status: 200,
    payload: {
      data: [
        incident(1, {
          status: "archived" as unknown as IncidentView["status"],
          role: "collaborator",
          canAnnotate: true,
        }),
      ],
    },
  });
  const root = await mount();
  const row = root.querySelector("tbody tr");
  expect(row?.textContent).toContain("archived");
  expect(row?.textContent).not.toContain("Open");
  expect(row?.textContent).toContain("Collaborator");
  expect(row?.textContent).not.toContain("read-only");
});

test("IncidentList: Cancel closes the form and returns focus to New incident", async () => {
  stubFetch({ status: 200, payload: { data: [] } });
  const root = await mount();
  const form = await openForm(root);
  act(() => button(form, "Cancel")?.click());
  await until(() => root.querySelector("form") === null);
  await until(() => document.activeElement === button(root, "New incident"));
});

test("IncidentList: Cancel is inert during a create, and the create still opens the incident", async () => {
  const gate = deferred();
  const created = incident(9);
  stubFetch(
    { status: 200, payload: { data: [] } },
    {
      status: 201,
      payload: { data: { incident: created } },
      gate: gate.promise,
    },
  );
  const visited = spyNavigation();
  const root = await mount();
  const form = await openForm(root);
  setValue(form.querySelector("#incident-title"), "x");
  await flush();
  submit(form);
  await until(() => calls.length === 2);
  const cancel = button(form, "Cancel");
  expect(cancel?.getAttribute("aria-disabled")).toBe("true");
  act(() => cancel?.click());
  await flush();
  expect(root.querySelector("form")).not.toBeNull();
  gate.resolve();
  await until(() => visited.length === 1);
  expect(visited).toEqual([`/observability/incidents/${created.id}`]);
});

test("IncidentList: a create that settles after the form unmounted never navigates", async () => {
  const gate = deferred();
  stubFetch(
    { status: 200, payload: { data: [] } },
    {
      status: 201,
      payload: { data: { incident: incident(3) } },
      gate: gate.promise,
    },
  );
  const visited = spyNavigation();
  const root = await mount();
  const form = await openForm(root);
  setValue(form.querySelector("#incident-title"), "x");
  await flush();
  submit(form);
  await until(() => calls.length === 2);
  // The create carries the form's lifetime signal.
  expect(calls[1].signal).toBeInstanceOf(AbortSignal);
  act(() => render(null, root));
  gate.resolve();
  await flush();
  expect(visited).toEqual([]);
});

test("IncidentList: a second submit while creating sends nothing", async () => {
  const gate = deferred();
  stubFetch(
    { status: 200, payload: { data: [] } },
    {
      status: 201,
      payload: { data: { incident: incident(4) } },
      gate: gate.promise,
    },
  );
  spyNavigation();
  const root = await mount();
  const form = await openForm(root);
  setValue(form.querySelector("#incident-title"), "x");
  await flush();
  submit(form);
  await until(() => calls.length === 2);
  submit(form);
  await flush();
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
  gate.resolve();
  await flush();
});

test("IncidentList: client validation refuses an empty title and an unparseable start", async () => {
  stubFetch({ status: 200, payload: { data: [] } });
  spyNavigation();
  const root = await mount();
  const form = await openForm(root);

  setValue(form.querySelector("#incident-title"), "   ");
  await flush();
  submit(form);
  await until(() => form.querySelector('[role="alert"]') !== null);
  expect(form.querySelector('[role="alert"]')?.textContent).toContain(
    "Give the incident a title",
  );

  setValue(form.querySelector("#incident-title"), "x");
  setValue(form.querySelector("#incident-window-start"), "");
  await flush();
  submit(form);
  await until(
    () =>
      form
        .querySelector('[role="alert"]')
        ?.textContent?.includes("window starts") ?? false,
  );
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
});

test("IncidentList: a valid window end is sent on the wire", async () => {
  stubFetch(
    { status: 200, payload: { data: [] } },
    { status: 201, payload: { data: { incident: incident(5) } } },
  );
  const visited = spyNavigation();
  const root = await mount();
  const form = await openForm(root);
  setValue(form.querySelector("#incident-title"), "x");
  setValue(form.querySelector("#incident-window-start"), "2026-10-01T09:00");
  setValue(form.querySelector("#incident-window-end"), "2026-10-01T10:15");
  await flush();
  submit(form);
  await until(() => visited.length === 1);
  const body = JSON.parse(calls[1].body ?? "");
  expect(body.windowStart).toBe(new Date("2026-10-01T09:00").toISOString());
  expect(body.windowEnd).toBe(new Date("2026-10-01T10:15").toISOString());
});

test("IncidentList: create maps no-database and busy to their own messages", async () => {
  for (const [reply, text] of [
    [apiError(503, "incident_persistence_unavailable"), "no database"],
    [apiError(503, "incident_busy"), "busy"],
  ] as const) {
    stubFetch({ status: 200, payload: { data: [] } }, reply);
    const visited = spyNavigation();
    const root = await mount();
    const form = await openForm(root);
    setValue(form.querySelector("#incident-title"), "x");
    await flush();
    submit(form);
    await until(() => form.querySelector('[role="alert"]') !== null);
    expect(form.querySelector('[role="alert"]')?.textContent).toContain(text);
    expect(visited).toEqual([]);
    act(() => render(null, root));
    root.remove();
    host = null;
  }
});

// --- IncidentList: review round 2 -------------------------------------------

test("IncidentList: a retry that finds no database drops Retry and create", async () => {
  stubFetch(
    apiError(503, "incident_store_unavailable"),
    apiError(503, "incident_persistence_unavailable", {
      requires: "postgresql",
    }),
  );
  const root = await mount();
  act(() => button(root, "Retry")?.click());
  await until(() => root.textContent?.includes("no database") ?? false);
  await flush();
  expect(listCalls()).toHaveLength(2);
  expect(button(root, "Retry")).toBeUndefined();
  expect(button(root, "New incident")).toBeUndefined();
  expect(root.querySelector('[role="alert"]')).toBeNull();
});

test("IncidentList: two Load more clicks in the same frame issue one request", async () => {
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "p2" } },
    },
    { status: 200, payload: { data: [incident(2)] } },
  );
  const root = await mount();
  const more = button(root, "Load more");
  // Outside act, so the first click's re-render can land between the two.
  more?.click();
  await Promise.resolve();
  more?.click();
  await until(() => root.querySelectorAll("tbody tr").length === 2);
  await flush();
  expect(listCalls().map((c) => c.url)).toEqual([
    "/api/v1/incidents?limit=50",
    "/api/v1/incidents?limit=50&continue=p2",
  ]);
});

test("IncidentList: a page that settles after unmount writes nothing and does not throw", async () => {
  const gate = deferred();
  stubFetch(
    {
      status: 200,
      payload: { data: [incident(1)], metadata: { continue: "p2" } },
    },
    { status: 200, payload: { data: [incident(2)] }, gate: gate.promise },
  );
  const root = await mount();
  act(() => button(root, "Load more")?.click());
  await until(() => listCalls().length === 2);
  act(() => render(null, root));
  // The in-flight read was cancelled by the unmount, which is what the
  // island's stale-response guard keys on.
  expect(listCalls()[1].signal?.aborted).toBe(true);
  gate.resolve();
  await flush();
  expect(root.childElementCount).toBe(0);
  expect(listCalls()).toHaveLength(2);
});

test("IncidentList: a create that rejects after unmount writes nothing and does not throw", async () => {
  const gate = deferred();
  stubFetch(
    { status: 200, payload: { data: [] } },
    { ...apiError(503, "incident_store_unavailable"), gate: gate.promise },
  );
  const visited = spyNavigation();
  const root = await mount();
  const form = await openForm(root);
  setValue(form.querySelector("#incident-title"), "x");
  await flush();
  submit(form);
  await until(() => calls.length === 2);
  act(() => render(null, root));
  expect(calls[1].signal?.aborted).toBe(true);
  gate.resolve();
  await flush();
  expect(visited).toEqual([]);
  expect(root.childElementCount).toBe(0);
});
