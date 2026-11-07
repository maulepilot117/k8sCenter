/** @jsxImportSource preact */
import {
  afterAll,
  afterEach,
  beforeEach,
  expect,
  jest,
  mock,
  test,
} from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { signal } from "@preact/signals";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import type { LogLine } from "@/lib/types/logs.ts";
import {
  selectedCluster,
  selectedClusterGeneration,
} from "@/src/lib/cluster.ts";

/**
 * Log search per cluster (remote logs and shells U14). The explorer reads
 * the selected cluster's Loki status and explains each reason it is
 * unavailable; the Loki live tail names the cluster it streams from and
 * resumes a retryable end on that cluster, as the pod log viewer does
 * (LogViewer_test.tsx). IS_BROWSER is stubbed to true and put back
 * afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: LogExplorer } = await import("./LogExplorer.tsx");
const { default: LogLiveTail } = await import("./LogLiveTail.tsx");

/** Records what the island sends; opening is driven by the test. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  raw: string[] = [];
  sent: Record<string, unknown>[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.raw.push(data);
    this.sent.push(JSON.parse(data));
  }
  /** Records the close; a test fires onclose itself, as a browser would later. */
  close() {
    this.closed = true;
  }
}

const REMOTE = "c-remote";
const REMOTE_GENERATION = "2026-10-01T00:00:00Z";

const realWebSocket = globalThis.WebSocket;
let originalFetch: typeof globalThis.fetch | undefined;
let host: HTMLElement | null = null;
let refreshCalls = 0;
/** When set, a session refresh waits for it before answering. */
let refreshGate: Promise<void> | null = null;
/** What GET /logs/status answers; set per test. */
let statusReply: { status: number; body: unknown } = {
  status: 200,
  body: { data: { detected: false } },
};
/** What GET /logs/query answers; set per test. */
let queryReply: { status: number; body: unknown } = {
  status: 200,
  body: { data: { data: { result: [] } } },
};

function reply(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  originalFetch = globalThis.fetch;
  refreshCalls = 0;
  refreshGate = null;
  statusReply = { status: 200, body: { data: { detected: false } } };
  queryReply = { status: 200, body: { data: { data: { result: [] } } } };
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/auth/refresh")) {
      refreshCalls++;
      if (refreshGate) await refreshGate;
      return reply(200, { data: { accessToken: "fresh-token" } });
    }
    if (url.includes(`/v1/clusters/${REMOTE}`)) {
      return reply(200, { data: { id: REMOTE, name: "prod-east" } });
    }
    if (url.includes("/v1/logs/status")) {
      return reply(statusReply.status, statusReply.body);
    }
    if (url.includes("/v1/logs/query")) {
      return reply(queryReply.status, queryReply.body);
    }
    return reply(200, { data: [] });
  }) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.WebSocket = realWebSocket;
  if (originalFetch) globalThis.fetch = originalFetch;
  setAccessToken(null);
  selectedCluster.value = "local";
  selectedClusterGeneration.value = "local";
});

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

const settle = () =>
  act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
    await new Promise((r) => setTimeout(r, 0));
  });

/** Microtasks only, for tests on fake timers. */
const flush = () =>
  act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });

function selectRemote() {
  selectedCluster.value = REMOTE;
  selectedClusterGeneration.value = REMOTE_GENERATION;
}

function mount(vnode: preact.JSX.Element) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(vnode, host as HTMLElement));
}

function text(): string {
  return host?.textContent ?? "";
}

function byTestId(id: string): Element | null {
  return host?.querySelector(`[data-testid="${id}"]`) ?? null;
}

function buttonNamed(name: string): HTMLButtonElement | undefined {
  return Array.from(host?.querySelectorAll("button") ?? []).find(
    (b) => b.textContent === name,
  );
}

// --- The explorer ---

async function mountExplorer() {
  mount(<LogExplorer />);
  await settle();
}

const unavailable = (reason: string) => ({
  status: 200,
  body: {
    data: {
      detected: false,
      reason,
      message: "server wording",
      lastChecked: "2026-11-05T00:00:00Z",
    },
  },
});

test("each reason Loki is unavailable on a remote cluster has its own copy", async () => {
  selectRemote();
  const reasons = [
    "loki_not_found",
    "loki_unreachable",
    "forbidden",
    "credentials_invalid",
    "unreachable",
    "cluster_unknown",
    "db_unavailable",
    "unsupported_platform",
  ];
  const seen = new Map<string, string>();
  for (const reason of reasons) {
    statusReply = unavailable(reason);
    await mountExplorer();
    const shown = byTestId("loki-unavailable")?.textContent ?? "";
    expect(shown).toContain("prod-east");
    expect(shown).not.toContain("server wording");
    seen.set(reason, shown);
    act(() => render(null, host as HTMLElement));
    host?.remove();
    host = null;
  }
  expect(new Set(seen.values()).size).toBe(reasons.length);
  expect(seen.get("loki_not_found")).toContain("Loki Not Found");
  expect(seen.get("loki_not_found")).toContain("Settings > Clusters");
  expect(seen.get("loki_unreachable")).toContain("Loki Unreachable");
  // Discovery lists Services cluster-wide when no well-known name answers,
  // so a grant without it still fails (loki/discovery.go).
  expect(seen.get("forbidden")).toContain("list on services cluster-wide");
  expect(seen.get("forbidden")).toContain("pods/portforward");
  expect(seen.get("credentials_invalid")).toContain("Credentials Refused");
});

test("a status request that fails without a reason does not claim Loki is missing", async () => {
  selectRemote();
  statusReply = { status: 500, body: {} };
  await mountExplorer();
  const shown = byTestId("loki-unavailable")?.textContent ?? "";
  expect(shown).toContain("Loki Status Unavailable");
  expect(shown).not.toContain("Not Found");
});

test("the local cluster with no Loki keeps the deployment hint", async () => {
  statusReply = unavailable("loki_not_found");
  await mountExplorer();
  expect(byTestId("loki-unavailable")?.textContent).toContain(
    "KUBECENTER_LOKI_URL",
  );
});

test("a status that cannot be read is shown as unavailable for its reason", async () => {
  selectRemote();
  statusReply = {
    status: 403,
    body: { error: { code: 403, message: "nope", reason: "forbidden" } },
  };
  await mountExplorer();
  expect(byTestId("loki-unavailable")?.textContent).toContain(
    "Permission Needed",
  );
});

test("the header names the cluster and its port-forwarded Loki Service", async () => {
  selectRemote();
  statusReply = {
    status: 200,
    body: {
      data: {
        detected: true,
        detectedVia: "port-forward",
        namespace: "monitoring",
        service: "loki-gateway",
      },
    },
  };
  await mountExplorer();
  expect(byTestId("loki-source")?.textContent).toBe(
    "Searching prod-east · Loki: Service monitoring/loki-gateway",
  );
});

test("the header says a configured URL is in use, without showing it", async () => {
  selectRemote();
  statusReply = {
    status: 200,
    body: {
      data: {
        detected: true,
        detectedVia: "binding",
        url: "https://loki.example.com",
      },
    },
  };
  await mountExplorer();
  const header = byTestId("loki-source")?.textContent ?? "";
  expect(header).toBe("Searching prod-east · Loki: configured URL");
});

test("the header names the local cluster's discovered Loki", async () => {
  statusReply = {
    status: 200,
    body: { data: { detected: true, detectedVia: "service-name" } },
  };
  await mountExplorer();
  expect(byTestId("loki-source")?.textContent).toBe(
    "Searching the local cluster · Loki: discovered in-cluster Service",
  );
});

test("a query refused for a reason shows that reason's copy, not the server text", async () => {
  selectRemote();
  statusReply = {
    status: 200,
    body: { data: { detected: true, detectedVia: "port-forward" } },
  };
  queryReply = {
    status: 502,
    body: {
      error: {
        code: 502,
        message: "server wording",
        reason: "credentials_invalid",
      },
    },
  };
  await mountExplorer();
  act(() => buttonNamed("Run")?.click());
  await settle();
  expect(text()).toContain("could not connect to Loki on prod-east");
  expect(text()).not.toContain("server wording");
});

// --- The live tail ---

function tailProps() {
  return {
    active: signal(true),
    query: signal('{app="web"}'),
    namespace: signal("default"),
    lines: signal<LogLine[]>([]),
  };
}

function latestSocket(): FakeWebSocket {
  return FakeWebSocket.instances.at(-1) as FakeWebSocket;
}

function openLatest(): FakeWebSocket {
  const ws = latestSocket();
  act(() => ws.onopen?.());
  return ws;
}

function deliver(ws: FakeWebSocket, frame: Record<string, unknown>) {
  act(() => ws.onmessage?.({ data: JSON.stringify(frame) }));
}

function deliverEntries(ws: FakeWebSocket, entries: Array<[string, string]>) {
  deliver(ws, {
    type: "log",
    payload: { streams: [{ stream: { pod: "web-0" }, values: entries }] },
  });
}

/** Waits out the backoff and opens the socket it starts. */
async function reconnectAfter(ms: number): Promise<FakeWebSocket> {
  const before = FakeWebSocket.instances.length;
  jest.advanceTimersByTime(ms - 1);
  await flush();
  expect(FakeWebSocket.instances.length).toBe(before);
  jest.advanceTimersByTime(1);
  await flush();
  expect(FakeWebSocket.instances.length).toBe(before + 1);
  return openLatest();
}

const T1 = "1762300800000000001";
const T2 = "1762300800000000002";
const T3 = "1762300800000000003";

test("the live tail badge names the cluster the session opened on", async () => {
  selectRemote();
  mount(<LogLiveTail {...tailProps()} />);
  await settle();
  expect(byTestId("session-cluster-badge")?.textContent).toBe("On prod-east");
});

test("the live tail badge names the local cluster", async () => {
  mount(<LogLiveTail {...tailProps()} />);
  await settle();
  expect(byTestId("session-cluster-badge")?.textContent).toBe(
    "On the local cluster",
  );
});

test("a lost Loki connection resumes on the pinned cluster from the last line shown", async () => {
  selectRemote();
  jest.useFakeTimers();
  try {
    const props = tailProps();
    mount(<LogLiveTail {...props} />);
    const first = openLatest();
    // The first subscribe asks for no start.
    expect(first.sent[1]).toEqual({
      type: "subscribe",
      query: '{app="web"}',
      namespace: "default",
      limit: 30,
    });
    deliver(first, { type: "subscribed" });
    deliverEntries(first, [
      [T1, "one"],
      [T2, "two"],
    ]);

    // The stored selection moves on; the open tail must not follow it.
    selectedCluster.value = "c-other";
    selectedClusterGeneration.value = "2026-10-02T00:00:00Z";
    deliver(first, {
      type: "error",
      reason: "loki_unreachable",
      message: "server wording",
      retryable: true,
    });
    expect(text()).toContain("Reconnecting...");

    const second = await reconnectAfter(2_000);
    expect(second.sent[0]).toEqual({
      type: "auth",
      token: "test-token",
      clusterId: REMOTE,
      generation: REMOTE_GENERATION,
    });
    // The start is sent with every digit Loki gave, which a JS number
    // would round.
    expect(second.raw[1]).toContain(`"start":${T2}}`);
    deliver(second, { type: "subscribed" });
    // Loki's start is inclusive: the line at it is replayed and skipped.
    deliverEntries(second, [
      [T2, "two"],
      [T3, "three"],
    ]);
    expect(props.lines.value.map((l) => l.line)).toEqual([
      "one",
      "two",
      "--- reconnected ---",
      "three",
    ]);
    expect(text()).not.toContain("server wording");
  } finally {
    jest.useRealTimers();
  }
});

test("an expired token is refreshed before the tail resumes", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    const first = openLatest();
    deliver(first, { type: "subscribed" });
    deliver(first, { type: "error", reason: "token_expired", retryable: true });
    const second = await reconnectAfter(2_000);
    expect(refreshCalls).toBe(1);
    expect(second.sent[0]).toMatchObject({ token: "fresh-token" });
    // With no line shown yet, the resume asks for no start.
    expect(second.sent[1]).not.toHaveProperty("start");
  } finally {
    jest.useRealTimers();
  }
});

test("a cluster with no Loki ends the tail with its copy and no reconnect", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    const ws = openLatest();
    deliver(ws, {
      type: "error",
      reason: "loki_not_found",
      message: "server wording",
    });
    expect(text()).toContain("No Loki was found on the selected cluster");
    expect(text()).not.toContain("server wording");
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(buttonNamed("Reconnect")).toBeDefined();
  } finally {
    jest.useRealTimers();
  }
});

test("a resume whose socket closes before subscribing counts as a failed try", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    const first = openLatest();
    deliver(first, { type: "subscribed" });
    deliver(first, {
      type: "error",
      reason: "loki_unreachable",
      retryable: true,
    });
    const second = await reconnectAfter(2_000);
    act(() => second.onclose?.());
    expect(text()).toContain("Reconnecting...");
    await reconnectAfter(5_000);
  } finally {
    jest.useRealTimers();
  }
});

test("three failed tries stop and show the end, and Reconnect starts over", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    let ws = openLatest();
    deliver(ws, { type: "subscribed" });
    const end = { type: "error", reason: "loki_unreachable", retryable: true };
    deliver(ws, end);
    for (const delay of [2_000, 5_000, 10_000]) {
      ws = await reconnectAfter(delay);
      deliver(ws, end);
    }
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(text()).toContain(
      "Loki on the selected cluster could not be reached.",
    );
    expect(text()).not.toContain("Reconnecting...");

    act(() => buttonNamed("Reconnect")?.click());
    expect(FakeWebSocket.instances.length).toBe(before + 1);
    ws = openLatest();
    // A fresh set of tries: the next end waits the first delay.
    deliver(ws, end);
    await reconnectAfter(2_000);
  } finally {
    jest.useRealTimers();
  }
});

test("a pending resume opens nothing once the tail is stopped", async () => {
  jest.useFakeTimers();
  try {
    const props = tailProps();
    mount(<LogLiveTail {...props} />);
    const ws = openLatest();
    deliver(ws, { type: "subscribed" });
    deliver(ws, { type: "error", reason: "loki_unreachable", retryable: true });
    act(() => {
      props.active.value = false;
    });
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
  } finally {
    jest.useRealTimers();
  }
});

test("a Loki that subscribes and then drops at once still runs out of tries", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    let ws = openLatest();
    const end = { type: "error", reason: "loki_unreachable", retryable: true };
    // Each attempt confirms the subscription and then ends before any line.
    deliver(ws, { type: "subscribed" });
    deliver(ws, end);
    for (const delay of [2_000, 5_000, 10_000]) {
      ws = await reconnectAfter(delay);
      deliver(ws, { type: "subscribed" });
      deliver(ws, end);
    }
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(text()).toContain(
      "Loki on the selected cluster could not be reached.",
    );
    expect(buttonNamed("Reconnect")).toBeDefined();
  } finally {
    jest.useRealTimers();
  }
});

test("a stream that carried lines earns a fresh set of tries", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    let ws = openLatest();
    const end = { type: "error", reason: "loki_unreachable", retryable: true };
    deliver(ws, { type: "subscribed" });
    deliver(ws, end);
    ws = await reconnectAfter(2_000);
    deliver(ws, { type: "subscribed" });
    deliverEntries(ws, [[T1, "one"]]);
    deliver(ws, end);
    // The lines reset the budget: the next try waits the first delay again.
    await reconnectAfter(2_000);
  } finally {
    jest.useRealTimers();
  }
});

test("a resume waiting on a session refresh opens nothing once the tail is stopped", async () => {
  jest.useFakeTimers();
  try {
    let release = () => {};
    refreshGate = new Promise<void>((r) => {
      release = r;
    });
    const props = tailProps();
    mount(<LogLiveTail {...props} />);
    const ws = openLatest();
    deliver(ws, { type: "subscribed" });
    deliver(ws, { type: "error", reason: "token_expired", retryable: true });
    jest.advanceTimersByTime(2_000);
    await flush();
    // The backoff has fired and the resume is waiting on the refresh.
    expect(refreshCalls).toBe(1);
    const before = FakeWebSocket.instances.length;
    act(() => {
      props.active.value = false;
    });
    release();
    await flush();
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
  } finally {
    jest.useRealTimers();
  }
});

test("a resume with no session left shows it and does not retry", async () => {
  jest.useFakeTimers();
  try {
    mount(<LogLiveTail {...tailProps()} />);
    const first = openLatest();
    deliver(first, { type: "subscribed" });
    deliver(first, {
      type: "error",
      reason: "server_restarting",
      retryable: true,
    });
    setAccessToken(null);
    const second = await reconnectAfter(2_000);
    // With no token the socket first waits on any refresh in flight.
    await flush();
    expect(second.sent).toEqual([]);
    expect(text()).toContain("Not authenticated");
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await flush();
    expect(FakeWebSocket.instances.length).toBe(before);
  } finally {
    jest.useRealTimers();
  }
});

test("a new query starts a fresh tail with no start", async () => {
  const props = tailProps();
  mount(<LogLiveTail {...props} />);
  const first = openLatest();
  deliver(first, { type: "subscribed" });
  deliverEntries(first, [[T1, "one"]]);
  act(() => {
    props.query.value = '{app="api"}';
  });
  const second = openLatest();
  expect(second).not.toBe(first);
  expect(second.sent[1]).toEqual({
    type: "subscribe",
    query: '{app="api"}',
    namespace: "default",
    limit: 30,
  });
});
