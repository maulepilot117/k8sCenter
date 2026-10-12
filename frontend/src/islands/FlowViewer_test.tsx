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
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import {
  selectedCluster,
  selectedClusterGeneration,
  UNKNOWN_GENERATION,
} from "@/src/lib/cluster.ts";

/**
 * The live flow viewer names its cluster in the WebSocket auth frame (#608
 * KTD10): the browser cannot set headers on a WebSocket, and the server
 * refuses a flows stream that names no cluster rather than serving local.
 * The generation is sent so a re-registered cluster is refused, except for
 * the `unknown` sentinel, which names no registration.
 *
 * As in NamespaceTopology_test.tsx, IS_BROWSER is stubbed to true for this
 * island and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: FlowViewer } = await import("./FlowViewer.tsx");

/** Records what the island sends; opening is driven by the test. */
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  sent: unknown[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {}
}

const realWebSocket = globalThis.WebSocket;
let originalFetch: typeof globalThis.fetch | undefined;
let host: HTMLElement | null = null;

beforeEach(() => {
  FakeWebSocket.instances = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ data: [] }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof globalThis.fetch;
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

/** Mounts the viewer, opens its socket and returns the auth frame sent. */
function authFrame(): Record<string, unknown> {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<FlowViewer />, host as HTMLElement));
  const ws = FakeWebSocket.instances.at(-1) as FakeWebSocket;
  act(() => ws.onopen?.());
  return ws.sent[0] as Record<string, unknown>;
}

test("the auth frame carries the selected cluster and its generation", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  expect(authFrame()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "c1",
    generation: "2026-10-01T12:30:00Z",
  });
});

test("the local cluster is named explicitly", () => {
  expect(authFrame()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "local",
    generation: "local",
  });
});

test("the unknown generation sentinel is not sent", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = UNKNOWN_GENERATION;
  const frame = authFrame();
  expect(frame.clusterId).toBe("c1");
  expect("generation" in frame).toBe(false);
});

// --- Reconnects and end states (#608 KTD11, U12) ---

const settle = () =>
  act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });

/** Mounts the viewer and opens its first socket. */
function mountViewer(): FakeWebSocket {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<FlowViewer />, host as HTMLElement));
  const ws = latestSocket();
  act(() => ws.onopen?.());
  return ws;
}

function latestSocket(): FakeWebSocket {
  return FakeWebSocket.instances.at(-1) as FakeWebSocket;
}

function deliver(ws: FakeWebSocket, frame: Record<string, unknown>) {
  act(() => ws.onmessage?.({ data: JSON.stringify(frame) }));
}

/** Waits out the backoff and opens the socket it starts. */
async function reconnectAfter(ms: number): Promise<FakeWebSocket> {
  const before = FakeWebSocket.instances.length;
  jest.advanceTimersByTime(ms - 1);
  await settle();
  expect(FakeWebSocket.instances.length).toBe(before);
  jest.advanceTimersByTime(1);
  await settle();
  expect(FakeWebSocket.instances.length).toBe(before + 1);
  const ws = latestSocket();
  act(() => ws.onopen?.());
  return ws;
}

test("an expired token is refreshed before reconnecting, and a live stream resets the tries", async () => {
  globalThis.fetch = (async (input: string | URL | Request) =>
    new Response(
      JSON.stringify({
        data: String(input).includes("/auth/refresh")
          ? { accessToken: "fresh-token" }
          : [],
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    )) as unknown as typeof globalThis.fetch;
  jest.useFakeTimers();
  try {
    const first = mountViewer();
    deliver(first, { type: "subscribed" });
    deliver(first, {
      type: "error",
      reason: "token_expired",
      retryable: true,
    });
    expect(host?.textContent).toContain("Reconnecting...");

    const second = await reconnectAfter(2_000);
    expect((second.sent[0] as Record<string, unknown>).token).toBe(
      "fresh-token",
    );
    deliver(second, { type: "subscribed" });
    expect(host?.textContent).toContain("Live");

    // The tries were reset: the next end waits the first delay again.
    deliver(second, {
      type: "error",
      reason: "relay_restarted",
      retryable: true,
    });
    await reconnectAfter(2_000);
  } finally {
    jest.useRealTimers();
  }
});

test("after a relay restart, a not-ready relay is retried until the stream is back", async () => {
  jest.useFakeTimers();
  try {
    const first = mountViewer();
    deliver(first, { type: "subscribed" });
    deliver(first, {
      type: "error",
      reason: "relay_restarted",
      retryable: true,
    });
    const second = await reconnectAfter(2_000);
    deliver(second, {
      type: "error",
      reason: "relay_not_ready",
      retryable: true,
    });
    const third = await reconnectAfter(5_000);
    deliver(third, { type: "subscribed" });
    expect(host?.textContent).toContain("Live");
    expect(host?.textContent).not.toContain("The live flow stream ended.");
  } finally {
    jest.useRealTimers();
  }
});

test("three failed tries end the stream with Reconnect, which starts over", async () => {
  jest.useFakeTimers();
  try {
    let ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    const end = { type: "error", reason: "relay_not_ready", retryable: true };
    deliver(ws, end);
    for (const delay of [2_000, 5_000, 10_000]) {
      ws = await reconnectAfter(delay);
      deliver(ws, end);
    }
    jest.advanceTimersByTime(30_000);
    await settle();
    expect(host?.textContent).toContain("The live flow stream ended.");
    const button = Array.from(host?.querySelectorAll("button") ?? []).find(
      (b) => b.textContent === "Reconnect",
    );
    expect(button).toBeDefined();

    const before = FakeWebSocket.instances.length;
    act(() => button?.click());
    expect(FakeWebSocket.instances.length).toBe(before + 1);
    ws = latestSocket();
    act(() => ws.onopen?.());
    expect(host?.textContent).not.toContain("The live flow stream ended.");
    // A fresh set of tries: the next end waits the first delay.
    deliver(ws, end);
    await reconnectAfter(2_000);
  } finally {
    jest.useRealTimers();
  }
});

test("a removed cluster ends the stream with its message and no reconnect", async () => {
  jest.useFakeTimers();
  try {
    const ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    deliver(ws, {
      type: "error",
      reason: "cluster_removed",
      message: "server wording",
    });
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(60_000);
    await settle();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(host?.textContent).toContain(
      "This cluster was removed from k8sCenter",
    );
    expect(host?.textContent).not.toContain("server wording");
    const labels = Array.from(host?.querySelectorAll("button") ?? []).map(
      (b) => b.textContent,
    );
    expect(labels).not.toContain("Reconnect");
  } finally {
    jest.useRealTimers();
  }
});
