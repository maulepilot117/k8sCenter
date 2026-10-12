/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
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
