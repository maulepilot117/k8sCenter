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
} from "@/src/lib/cluster.ts";

/**
 * The live log viewer resumes a tail that ends with a retryable reason, on
 * the cluster it opened on, and names that cluster (remote logs and shells
 * U8, AE4). As in FlowViewer_test.tsx, IS_BROWSER is stubbed to true for
 * this island and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: LogViewer } = await import("./LogViewer.tsx");

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

const REMOTE = "c-remote";
const REMOTE_GENERATION = "2026-10-01T00:00:00Z";

const realWebSocket = globalThis.WebSocket;
const realRaf = globalThis.requestAnimationFrame;
const realCancelRaf = globalThis.cancelAnimationFrame;
let originalFetch: typeof globalThis.fetch | undefined;
let host: HTMLElement | null = null;

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  // The viewer batches lines once per animation frame. A timer-backed frame
  // lets the fake timers drive the flush.
  globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) =>
    setTimeout(() => cb(0), 16)) as unknown as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = ((id: number) =>
    clearTimeout(id)) as unknown as typeof cancelAnimationFrame;
  originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/auth/refresh"))
      return json({ accessToken: "fresh-token" });
    if (url.includes(`/v1/clusters/${REMOTE}`)) {
      return json({ id: REMOTE, name: "prod-east" });
    }
    return json([]);
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
  globalThis.requestAnimationFrame = realRaf;
  globalThis.cancelAnimationFrame = realCancelRaf;
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
  });

function latestSocket(): FakeWebSocket {
  return FakeWebSocket.instances.at(-1) as FakeWebSocket;
}

/** Mounts the viewer on one container and opens its first socket. */
function mountViewer(): FakeWebSocket {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <LogViewer namespace="default" pod="web-1" containers={["app"]} />,
      host as HTMLElement,
    ),
  );
  const ws = latestSocket();
  act(() => ws.onopen?.());
  return ws;
}

function deliver(ws: FakeWebSocket, frame: Record<string, unknown>) {
  act(() => ws.onmessage?.({ data: JSON.stringify(frame) }));
}

/** Delivers log lines and lets the viewer's frame-batched flush run. */
async function deliverLines(ws: FakeWebSocket, lines: string[]) {
  for (const data of lines) deliver(ws, { type: "log", data });
  jest.advanceTimersByTime(50);
  await settle();
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

function shownLines(): string[] {
  const text = host?.querySelector("pre")?.textContent ?? "";
  return text.split("\n").filter((l) => l !== "");
}

function reconnectButton(): HTMLButtonElement | undefined {
  return Array.from(host?.querySelectorAll("button") ?? []).find(
    (b) => b.textContent === "Reconnect",
  );
}

// Covers AE4.
test("an expired token is refreshed and the tail resumes on the cluster it opened on", async () => {
  selectedCluster.value = REMOTE;
  selectedClusterGeneration.value = REMOTE_GENERATION;
  jest.useFakeTimers();
  try {
    const first = mountViewer();
    expect(first.sent[0]).toEqual({
      type: "auth",
      token: "test-token",
      clusterId: REMOTE,
      generation: REMOTE_GENERATION,
    });
    deliver(first, { type: "subscribed" });

    // The stored selection moves on; the open tail must not follow it.
    selectedCluster.value = "c-other";
    selectedClusterGeneration.value = "2026-10-02T00:00:00Z";
    deliver(first, { type: "error", reason: "token_expired", retryable: true });
    expect(host?.textContent).toContain("Reconnecting...");

    const second = await reconnectAfter(2_000);
    expect(second.sent[0]).toEqual({
      type: "auth",
      token: "fresh-token",
      clusterId: REMOTE,
      generation: REMOTE_GENERATION,
    });
    deliver(second, { type: "subscribed" });
    expect(host?.textContent).toContain("Streaming");
  } finally {
    jest.useRealTimers();
  }
});

test("a non-retryable end shows its copy and does not reconnect", async () => {
  jest.useFakeTimers();
  try {
    const ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    deliver(ws, {
      type: "error",
      reason: "cluster_removed",
      message: "server wording",
    });
    expect(host?.textContent).toContain("removed from k8sCenter");
    expect(host?.textContent).not.toContain("server wording");
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await settle();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(reconnectButton()).toBeDefined();
  } finally {
    jest.useRealTimers();
  }
});

test("three failed tries stop and show the end, and Reconnect starts over", async () => {
  jest.useFakeTimers();
  try {
    let ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    const end = { type: "error", reason: "server_restarting", retryable: true };
    deliver(ws, end);
    for (const delay of [2_000, 5_000, 10_000]) {
      ws = await reconnectAfter(delay);
      deliver(ws, end);
    }
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(30_000);
    await settle();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(host?.textContent).toContain("could not reconnect");
    expect(host?.textContent).not.toContain("Reconnecting...");

    const button = reconnectButton();
    expect(button).toBeDefined();
    act(() => button?.click());
    expect(FakeWebSocket.instances.length).toBe(before + 1);
    ws = latestSocket();
    act(() => ws.onopen?.());
    // A fresh set of tries: the next end waits the first delay.
    deliver(ws, end);
    await reconnectAfter(2_000);
  } finally {
    jest.useRealTimers();
  }
});

test("a resume skips the replayed lines and marks the join once", async () => {
  jest.useFakeTimers();
  try {
    const first = mountViewer();
    deliver(first, { type: "subscribed" });
    const shown = [
      "2026-11-03T15:00:01Z one",
      "2026-11-03T15:00:02.25Z two",
      "2026-11-03T15:00:02.5Z three",
    ];
    await deliverLines(first, shown);
    expect(shownLines()).toEqual(shown);

    deliver(first, {
      type: "error",
      reason: "server_restarting",
      retryable: true,
    });
    const second = await reconnectAfter(2_000);
    deliver(second, { type: "subscribed" });
    // The resumed tail replays its last lines, then continues.
    await deliverLines(second, [...shown, "2026-11-03T15:00:03Z four"]);

    expect(shownLines()).toEqual([
      ...shown,
      "--- reconnected ---",
      "2026-11-03T15:00:03Z four",
    ]);
  } finally {
    jest.useRealTimers();
  }
});

test("the badge names the local cluster", async () => {
  mountViewer();
  await settle();
  expect(
    host?.querySelector('[data-testid="session-cluster-badge"]')?.textContent,
  ).toBe("On the local cluster");
});

test("the badge names a remote cluster by its registered name", async () => {
  selectedCluster.value = REMOTE;
  selectedClusterGeneration.value = REMOTE_GENERATION;
  mountViewer();
  await settle();
  await act(() => new Promise((r) => setTimeout(r, 0)));
  expect(
    host?.querySelector('[data-testid="session-cluster-badge"]')?.textContent,
  ).toBe("On prod-east");
});
