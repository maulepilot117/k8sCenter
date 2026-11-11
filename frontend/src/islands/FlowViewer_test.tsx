import { afterEach, beforeEach, expect, jest, test } from "bun:test";
import { refreshSession, setAccessToken } from "@/lib/api.ts";
import {
  selectedCluster,
  selectedClusterGeneration,
  UNKNOWN_GENERATION,
} from "@/src/lib/cluster.ts";
import {
  act,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The live flow viewer names its cluster in the WebSocket auth frame (#608
 * KTD10): the browser cannot set headers on a WebSocket, and the server
 * refuses a flows stream that names no cluster rather than serving local.
 * The generation is sent so a re-registered cluster is refused, except for
 * the `unknown` sentinel, which names no registration.
 */
setupDom({ browser: true });

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
  closed = false;
  close() {
    this.closed = true;
  }
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
  host = null;
  globalThis.WebSocket = realWebSocket;
  if (originalFetch) globalThis.fetch = originalFetch;
  setAccessToken(null);
  selectedCluster.value = "local";
  selectedClusterGeneration.value = "local";
});

/** Mounts the viewer, opens its socket and returns the auth frame sent. */
function authFrame(): Record<string, unknown> {
  host = render(<FlowViewer />).container as HTMLElement;
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
  host = render(<FlowViewer />).container as HTMLElement;
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

test("a stream that never subscribes times out and reconnects", async () => {
  jest.useFakeTimers();
  try {
    mountViewer();
    jest.advanceTimersByTime(19_999);
    await settle();
    expect(host?.textContent).toContain("Connecting...");
    jest.advanceTimersByTime(1);
    await settle();
    expect(host?.textContent).toContain("Reconnecting...");
    const next = await reconnectAfter(2_000);
    deliver(next, { type: "subscribed" });
    expect(host?.textContent).toContain("Live");
  } finally {
    jest.useRealTimers();
  }
});

test("a stream that subscribes in time is not timed out", async () => {
  jest.useFakeTimers();
  try {
    const ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    const before = FakeWebSocket.instances.length;
    jest.advanceTimersByTime(60_000);
    await settle();
    expect(FakeWebSocket.instances.length).toBe(before);
    expect(host?.textContent).toContain("Live");
  } finally {
    jest.useRealTimers();
  }
});

/** Answers /auth/refresh once `gate` is released, with a fresh token. */
function gatedRefresh(): { release: () => void } {
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  globalThis.fetch = (async () => {
    await gate;
    return new Response(
      JSON.stringify({ data: { accessToken: "fresh-token" } }),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      },
    );
  }) as unknown as typeof globalThis.fetch;
  return { release };
}

test("a stream opened during the startup refresh authenticates once it lands", async () => {
  setAccessToken(null);
  const { release } = gatedRefresh();
  const boot = refreshSession();

  host = render(<FlowViewer />).container as HTMLElement;
  await settle();
  expect(FakeWebSocket.instances).toHaveLength(0);

  release();
  await boot;
  await settle();
  await new Promise((r) => setTimeout(r, 0));
  const ws = latestSocket();
  expect(ws).toBeDefined();
  act(() => ws.onopen?.());
  expect(ws.sent[0]).toMatchObject({ type: "auth", token: "fresh-token" });
});

// --- Filters, batching and cleanup ---

const jsonResponse = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

/** Answers the namespace list, and records every other request's URL. */
function stubNamespacesAndFlows(urls: string[]) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/resources/namespaces")) {
      return jsonResponse([
        { metadata: { name: "default" } },
        { metadata: { name: "prod" } },
      ]);
    }
    urls.push(url);
    return jsonResponse([]);
  }) as unknown as typeof globalThis.fetch;
}

/** Holds animation frames until a test runs them. */
function captureFrames() {
  const realRaf = globalThis.requestAnimationFrame;
  const realCancel = globalThis.cancelAnimationFrame;
  const pending = new Map<number, FrameRequestCallback>();
  let next = 1;
  globalThis.requestAnimationFrame = ((cb: FrameRequestCallback) => {
    pending.set(next, cb);
    return next++;
  }) as typeof globalThis.requestAnimationFrame;
  globalThis.cancelAnimationFrame = ((id: number) => {
    pending.delete(id);
  }) as typeof globalThis.cancelAnimationFrame;
  return {
    pending,
    run: () => {
      const callbacks = [...pending.values()];
      pending.clear();
      act(() => {
        for (const cb of callbacks) cb(0);
      });
    },
    restore: () => {
      globalThis.requestAnimationFrame = realRaf;
      globalThis.cancelAnimationFrame = realCancel;
    },
  };
}

const flowFrame = (pod: string) => ({
  type: "flow",
  data: {
    time: "2026-10-01T12:00:00Z",
    verdict: "FORWARDED",
    direction: "INGRESS",
    srcNamespace: "default",
    srcPod: pod,
    dstNamespace: "default",
    dstPod: "db",
    protocol: "TCP",
  },
});

function pickFilter(label: "Namespace" | "Verdict", value: string) {
  const select = Array.from(host?.querySelectorAll("label") ?? [])
    .find((l) => l.textContent === label)
    ?.parentElement?.querySelector("select") as HTMLSelectElement;
  act(() => {
    fireEvent.change(select, { target: { value } });
  });
}

test("changing a filter reconnects with the new filter, and a flow still on the old stream is ignored", async () => {
  stubNamespacesAndFlows([]);
  const frames = captureFrames();
  try {
    const first = mountViewer();
    await settle();
    deliver(first, { type: "subscribed" });

    pickFilter("Namespace", "prod");
    pickFilter("Verdict", "DROPPED");
    const next = latestSocket();
    expect(next).not.toBe(first);
    act(() => next.onopen?.());
    expect(next.sent[1]).toEqual({ namespace: "prod", verdict: "DROPPED" });

    deliver(first, flowFrame("stale-pod"));
    deliver(next, { type: "subscribed" });
    deliver(next, flowFrame("fresh-pod"));
    frames.run();
    expect(host?.textContent).toContain("fresh-pod");
    expect(host?.textContent).not.toContain("stale-pod");
    expect(host?.textContent).toContain("1 flows");
  } finally {
    frames.restore();
  }
});

test("flows arriving in one frame are batched, newest first", async () => {
  const frames = captureFrames();
  try {
    const ws = mountViewer();
    deliver(ws, { type: "subscribed" });
    deliver(ws, flowFrame("pod-a"));
    deliver(ws, flowFrame("pod-b"));
    expect(host?.textContent).toContain("0 flows");
    expect(frames.pending.size).toBe(1);

    frames.run();
    expect(host?.textContent).toContain("2 flows");
    const rows = Array.from(host?.querySelectorAll("tbody tr") ?? []);
    expect(rows[0].textContent).toContain("pod-b");
    expect(rows[1].textContent).toContain("pod-a");

    deliver(ws, flowFrame("pod-c"));
    frames.run();
    expect(host?.querySelectorAll("tbody tr")[0].textContent).toContain(
      "pod-c",
    );
    expect(host?.textContent).toContain("3 flows");
  } finally {
    frames.restore();
  }
});

test("a failed socket falls back to the flows request with the current filters", async () => {
  const urls: string[] = [];
  stubNamespacesAndFlows(urls);
  const first = mountViewer();
  await settle();
  pickFilter("Namespace", "prod");
  pickFilter("Verdict", "DROPPED");
  const next = latestSocket();
  expect(next).not.toBe(first);

  act(() => next.onerror?.());
  await settle();
  const fallback = urls.find((u) => u.includes("/hubble/flows")) ?? "";
  expect(fallback).toContain("namespace=prod");
  expect(fallback).toContain("verdict=DROPPED");
  expect(fallback).toContain("limit=200");
});

test("leaving the page closes the socket and drops a pending frame", async () => {
  const frames = captureFrames();
  try {
    const view = render(<FlowViewer />);
    host = view.container as HTMLElement;
    const ws = latestSocket();
    act(() => ws.onopen?.());
    deliver(ws, { type: "subscribed" });
    deliver(ws, flowFrame("pod-a"));
    expect(frames.pending.size).toBe(1);

    act(() => view.unmount());
    expect(ws.closed).toBe(true);
    expect(frames.pending.size).toBe(0);
    const before = FakeWebSocket.instances.length;
    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBe(before);
    });
  } finally {
    frames.restore();
  }
});
