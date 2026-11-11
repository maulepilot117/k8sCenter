import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { type RefObject, useState } from "react";
import { setAccessToken } from "@/lib/api.ts";
import type { LogLine } from "@/lib/types/logs.ts";
import {
  selectedCluster,
  selectedClusterGeneration,
} from "@/src/lib/cluster.ts";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * The live log viewer and the Loki live tail name their cluster in the
 * WebSocket auth frame, as the flow viewer does (FlowViewer_test.tsx). The
 * server cannot read X-Cluster-ID from a browser WebSocket and treats a
 * stream that names no cluster as local, so without the frame a remote
 * selection would stream the local cluster's logs. Bun's own fetch and
 * WebSocket are captured before happy-dom replaces them and put back in
 * afterAll.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

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

beforeEach(() => {
  FakeWebSocket.instances = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ data: { lines: [] } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  setAccessToken(null);
  selectedCluster.value = "local";
  selectedClusterGeneration.value = "local";
});

afterAll(() => {
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
});

/** Opens the newest socket and returns the first frame it sent. */
function openLatest(): Record<string, unknown> {
  const ws = FakeWebSocket.instances.at(-1) as FakeWebSocket;
  act(() => ws.onopen?.());
  return ws.sent[0] as Record<string, unknown>;
}

/** What a test does to the tail the explorer would otherwise drive. */
interface TailControls {
  setQuery: (query: string) => void;
}

/** Owns the live tail's props the way the explorer does. */
function TailHost({ controls }: { controls: RefObject<TailControls | null> }) {
  const [query, setQuery] = useState('{app="web"}');
  const [lines, setLines] = useState<LogLine[]>([]);
  controls.current = { setQuery };
  return (
    <LogLiveTail
      active
      query={query}
      namespace="default"
      lines={lines}
      setLines={setLines}
    />
  );
}

test("the log viewer's follow stream names the selected remote cluster", () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  render(<LogViewer namespace="default" pod="web-0" containers={["web"]} />);
  expect(openLatest()).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "c1",
    generation: "2026-10-01T12:30:00Z",
  });
});

test("the log viewer names the local cluster explicitly", () => {
  render(<LogViewer namespace="default" pod="web-0" containers={["web"]} />);
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
  render(<TailHost controls={{ current: null }} />);
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
  const controls: { current: TailControls | null } = { current: null };
  render(<TailHost controls={controls} />);
  openLatest();

  selectedCluster.value = "c2";
  selectedClusterGeneration.value = "2026-10-02T08:00:00Z";
  act(() => {
    controls.current?.setQuery('{app="api"}');
  });

  expect(FakeWebSocket.instances.length).toBe(2);
  expect(openLatest().clusterId).toBe("c1");
});
