/** @jsxImportSource preact */
import {
  afterAll,
  afterEach,
  beforeAll,
  expect,
  type Mock,
  spyOn,
  test,
} from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { ApiError } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import type {
  EvidenceEventsResponse,
  HistoryEntry,
  HistoryPage,
} from "@/lib/eso-types.ts";
import type { APIResponse } from "@/lib/k8s-types.ts";
import ESOEvidencePanel from "./ESOEvidencePanel.tsx";

/**
 * Effect-level coverage for the evidence panel: the lazy loads, aborts and
 * late-response discards that server rendering cannot exercise (it runs no
 * effects). The panel is mounted into a happy-dom document with the three
 * `esoApi` calls replaced by promises the test settles by hand, so the order
 * in which responses land is under the test's control.
 *
 * The DOM is registered for this file only and removed afterwards, so the
 * rest of the suite keeps running without browser globals.
 */

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Records every call's AbortSignal and hands back one deferred per call. */
function stub<T>(
  method:
    | "getEvidenceEvents"
    | "exportEvidenceYaml"
    | "getExternalSecretHistory",
) {
  const calls: Array<{
    args: unknown[];
    signal?: AbortSignal;
    d: Deferred<APIResponse<T>>;
  }> = [];
  const spy = spyOn(esoApi, method).mockImplementation(((
    ...args: unknown[]
  ) => {
    const d = deferred<APIResponse<T>>();
    const last = args[args.length - 1];
    const opts = args[2] as { signal?: AbortSignal } | undefined;
    const signal =
      last instanceof AbortSignal
        ? last
        : method === "getExternalSecretHistory"
          ? opts?.signal
          : undefined;
    calls.push({ args, signal, d });
    return d.promise;
  }) as never) as Mock<(...a: unknown[]) => unknown>;
  return { calls, spy };
}

let host: HTMLElement;
const spies: Array<{ mockRestore: () => void }> = [];

function mount(vnode: preact.VNode) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(vnode, host));
}

async function settle(fn: () => void) {
  await act(async () => {
    fn();
    await Promise.resolve();
  });
}

afterEach(() => {
  if (host) {
    act(() => render(null, host));
    host.remove();
  }
  for (const s of spies.splice(0)) s.mockRestore();
});

function eventsFor(
  uid: string,
  message = "EVENT_FOR_" + uid,
): APIResponse<EvidenceEventsResponse> {
  return {
    data: {
      uid,
      projection: { level: "full", droppedFields: [] },
      events: [{ type: "Warning", reason: "UpdateFailed", count: 1, message }],
      truncated: false,
    },
  };
}

function entry(id: number, attemptAt: string): HistoryEntry {
  return {
    id,
    attemptAt,
    outcome: "success",
    reason: "SecretSynced",
    diffKeyCounts: { added: 0, removed: 0, changed: 0 },
  };
}

function historyPage(
  uid: string,
  entries: HistoryEntry[],
  next?: string,
): APIResponse<HistoryPage> {
  return {
    data: {
      uid,
      clusterId: "local",
      projection: { level: "full", droppedFields: [] },
      entries,
    },
    metadata: next ? { continue: next } : {},
  };
}

function stateOf(): string | null {
  return (
    host
      .querySelector("[data-evidence-state]")
      ?.getAttribute("data-evidence-state") ?? null
  );
}

// --- Lazy loading ------------------------------------------------------------------

test("only the shown tab loads, and the YAML request carries the object's UID", async () => {
  const yaml = stub<string>("exportEvidenceYaml");
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(yaml.spy, events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="db-creds"
      uid="uid-1"
    />,
  );
  expect(yaml.calls.length).toBe(1);
  expect(events.calls.length).toBe(0); // not viewed, not fetched
  expect(yaml.calls[0].args.slice(0, 4)).toEqual([
    "externalsecrets",
    "apps",
    "db-creds",
    "uid-1",
  ]);

  await settle(() => yaml.calls[0].d.resolve({ data: "kind: ExternalSecret" }));
  expect(host.querySelector("pre")?.textContent).toBe("kind: ExternalSecret");
});

// --- Late responses (R1) ----------------------------------------------------------------

test("a response for the previous target is aborted and never rendered", async () => {
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="db-creds"
      uid="uid-1"
      activeTab="events"
    />,
  );
  expect(events.calls.length).toBe(1);

  // The object is replaced while the first request is still in flight.
  act(() =>
    render(
      <ESOEvidencePanel
        kind="externalsecrets"
        namespace="apps"
        name="db-creds"
        uid="uid-2"
        activeTab="events"
      />,
      host,
    ),
  );
  expect(events.calls[0].signal?.aborted).toBe(true);
  expect(events.calls.length).toBe(2);

  // The old response lands last-but-one; it must not be shown for uid-2.
  await settle(() => events.calls[0].d.resolve(eventsFor("uid-1")));
  expect(host.textContent).not.toContain("EVENT_FOR_uid-1");

  await settle(() => events.calls[1].d.resolve(eventsFor("uid-2")));
  expect(host.textContent).toContain("EVENT_FOR_uid-2");
  expect(host.textContent).not.toContain("EVENT_FOR_uid-1");
});

test("a response naming a different UID shows 'replaced', not a spinner", async () => {
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="db-creds"
      uid="uid-1"
      activeTab="events"
    />,
  );
  // The server resolved the name to a recreated object.
  await settle(() => events.calls[0].d.resolve(eventsFor("uid-new")));

  expect(stateOf()).toBe("replaced");
  expect(host.textContent).not.toContain("EVENT_FOR_uid-new");
  expect(host.querySelector('[aria-label="Loading"]')).toBeNull();
});

test("a YAML 409 uid_mismatch shows 'replaced'", async () => {
  const yaml = stub<string>("exportEvidenceYaml");
  spies.push(yaml.spy);

  mount(
    <ESOEvidencePanel
      kind="secretstores"
      namespace="apps"
      name="vault"
      uid="uid-1"
    />,
  );
  await settle(() =>
    yaml.calls[0].d.reject(
      new ApiError(409, 409, "replaced", {
        error: { code: 409, reason: "uid_mismatch" },
      }),
    ),
  );
  expect(stateOf()).toBe("replaced");
});

test("unmounting aborts in-flight requests", () => {
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="db-creds"
      uid="uid-1"
      activeTab="events"
    />,
  );
  act(() => render(null, host));
  expect(events.calls[0].signal?.aborted).toBe(true);
});

// --- Distinct states (R3) ---------------------------------------------------------------

test("forbidden, empty and loaded each render their own state", async () => {
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="a"
      uid="uid-a"
      activeTab="events"
    />,
  );
  await settle(() =>
    events.calls[0].d.reject(
      new ApiError(
        403,
        403,
        "access denied: this object's events require `list events` in namespace apps",
        {
          error: { code: 403, reason: "events_forbidden" },
        },
      ),
    ),
  );
  expect(stateOf()).toBe("forbidden");
  expect(host.textContent).toContain("`list events` in namespace apps");

  act(() =>
    render(
      <ESOEvidencePanel
        kind="externalsecrets"
        namespace="apps"
        name="b"
        uid="uid-b"
        activeTab="events"
      />,
      host,
    ),
  );
  await settle(() =>
    events.calls[1].d.resolve({
      data: {
        uid: "uid-b",
        projection: { level: "full", droppedFields: [] },
        events: [],
        truncated: false,
      },
    }),
  );
  expect(stateOf()).toBe("empty");
});

test("an outcome-only response shows the redaction note", async () => {
  const events = stub<EvidenceEventsResponse>("getEvidenceEvents");
  spies.push(events.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="a"
      uid="uid-a"
      activeTab="events"
    />,
  );
  await settle(() =>
    events.calls[0].d.resolve({
      data: {
        uid: "uid-a",
        projection: {
          level: "outcome-only",
          droppedFields: ["message", "messageTruncated", "source"],
        },
        events: [{ type: "Warning", reason: "UpdateFailed", count: 1 }],
        truncated: false,
      },
    }),
  );
  expect(stateOf()).toBe("redacted");
  expect(host.textContent).toContain("requires Secret read");
});

// --- History paging ------------------------------------------------------------------------

test("Load more appends the next page; a failed page keeps the rows already shown", async () => {
  const history = stub<HistoryPage>("getExternalSecretHistory");
  spies.push(history.spy);

  mount(
    <ESOEvidencePanel
      kind="externalsecrets"
      namespace="apps"
      name="a"
      uid="uid-a"
      activeTab="history"
    />,
  );
  await settle(() =>
    history.calls[0].d.resolve(
      historyPage("uid-a", [entry(2, "2026-09-10T12:00:02Z")], "CURSOR-1"),
    ),
  );
  expect(host.querySelectorAll("li").length).toBe(1);

  const loadMore = () =>
    [...host.querySelectorAll("button")].find(
      (b) => b.textContent === "Load more",
    ) as HTMLButtonElement;
  act(() => loadMore().click());
  expect((history.calls[1].args[2] as { cursor?: string }).cursor).toBe(
    "CURSOR-1",
  );
  await settle(() =>
    history.calls[1].d.resolve(
      historyPage("uid-a", [entry(1, "2026-09-10T12:00:01Z")], "CURSOR-2"),
    ),
  );
  expect(host.querySelectorAll("li").length).toBe(2);

  act(() => loadMore().click());
  await settle(() =>
    history.calls[2].d.reject(
      new ApiError(503, 503, "down", {
        error: { reason: "history_unavailable" },
      }),
    ),
  );
  expect(host.querySelectorAll("li").length).toBe(2);
  expect(stateOf()).toBe("history_unavailable");
  expect(loadMore().disabled).toBe(false); // the cursor is kept, so it can retry
});
