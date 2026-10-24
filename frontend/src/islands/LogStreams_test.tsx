/** @jsxImportSource preact */
import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
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
 * The live log viewer and the Loki live tail name their cluster in the
 * WebSocket auth frame, as the flow viewer does (FlowViewer_test.tsx). The
 * server cannot read X-Cluster-ID from a browser WebSocket and treats a
 * stream that names no cluster as local, so without the frame a remote
 * selection would stream the local cluster's logs.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: LogViewer } = await import("./LogViewer.tsx");
const { default: LogLiveTail } = await import("./LogLiveTail.tsx");

/** Records what an island sends; opening is driven by the test. */
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
    new Response(JSON.stringify({ data: { lines: [] } }), {
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

function mount(vnode: preact.JSX.Element) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(vnode, host as HTMLElement));
}

/** Opens the newest socket and returns the first frame it sent. */
function openLatest(): Record<string, unknown> {
  const ws = FakeWebSocket.instances.at(-1) as FakeWebSocket;
  act(() => ws.onopen?.());
  return ws.sent[0] as Record<string, unknown>;
}

function liveTailProps() {
  return {
    active: signal(true),
    query: signal('{app="web"}'),
    namespace: signal("default"),
    lines: signal<LogLine[]>([]),
  };
}

test("the log viewer's follow stream names the selected remote cluster", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  mount(<LogViewer namespace="default" pod="web-0" containers={["web"]} />);
  expect(openLatest()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "c1",
    generation: "2026-10-01T12:30:00Z",
  });
});

test("the log viewer names the local cluster explicitly", () => {
  mount(<LogViewer namespace="default" pod="web-0" containers={["web"]} />);
  expect(openLatest()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "local",
    generation: "local",
  });
});

test("the Loki live tail names the selected remote cluster", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  mount(<LogLiveTail {...liveTailProps()} />);
  expect(openLatest()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "c1",
    generation: "2026-10-01T12:30:00Z",
  });
});

test("a later live tail reuses the cluster pinned by the first", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  const props = liveTailProps();
  mount(<LogLiveTail {...props} />);
  openLatest();

  selectedCluster.value = "c2";
  selectedClusterGeneration.value = "2026-10-02T08:00:00Z";
  act(() => {
    props.query.value = '{app="api"}';
  });

  expect(FakeWebSocket.instances.length).toBe(2);
  expect(openLatest().clusterId).toBe("c1");
});
