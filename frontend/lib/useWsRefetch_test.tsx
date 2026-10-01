/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { disconnectWS } from "@/lib/ws.ts";
import { LOCAL_CLUSTER_ID, switchCluster } from "@/src/lib/cluster.ts";

/**
 * The resource WebSocket is fed by the LOCAL cluster's informers whatever
 * cluster is selected (remote clusters have no informers). A page showing a
 * remote cluster that refetched on those events would refetch on local
 * churn, so under a remote selection the hook must not subscribe at all
 * (R-8 U14, matching ResourceTable).
 *
 * Asserted at the socket: what the browser would open and send.
 */

/** Records every socket the module opens and every frame it sends. */
class FakeSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeSocket[] = [];

  readyState = FakeSocket.CONNECTING;
  sent: Array<Record<string, unknown>> = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(public url: string) {
    FakeSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(JSON.parse(data));
  }

  close() {
    this.readyState = FakeSocket.CLOSED;
  }

  /** Opens the socket and has the server accept the auth frame. */
  accept() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
    this.onmessage?.({ data: JSON.stringify({ type: "auth_ok" }) });
  }

  deliver(id: string) {
    this.onmessage?.({
      data: JSON.stringify({
        type: "event",
        id,
        eventType: "MODIFIED",
        object: {},
      }),
    });
  }
}

const SUB: [string, string, string] = ["gitops-apps", "applications", ""];
const DEBOUNCE_MS = 5;

let host: HTMLElement | null = null;
let originalSocket: typeof globalThis.WebSocket;

beforeAll(() => {
  GlobalRegistrator.register();
  originalSocket = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket as unknown as typeof globalThis.WebSocket;
});

afterAll(() => {
  globalThis.WebSocket = originalSocket;
  GlobalRegistrator.unregister();
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  disconnectWS();
  FakeSocket.instances = [];
  setAccessToken(null);
  switchCluster(LOCAL_CLUSTER_ID, "local");
});

function mount(): { calls: () => number } {
  let calls = 0;
  const fetchFn = () => {
    calls++;
    return Promise.resolve();
  };
  function Probe() {
    useWsRefetch(fetchFn, [SUB], DEBOUNCE_MS);
    return null;
  }
  setAccessToken("test-token");
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<Probe />, host as HTMLElement));
  return { calls: () => calls };
}

const settle = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, DEBOUNCE_MS * 4));
  });

test("the local cluster subscribes and refetches on an event", async () => {
  switchCluster(LOCAL_CLUSTER_ID, "local");
  const probe = mount();

  expect(FakeSocket.instances).toHaveLength(1);
  const socket = FakeSocket.instances[0];
  socket.accept();
  expect(socket.sent).toContainEqual(
    expect.objectContaining({ type: "subscribe", id: SUB[0], kind: SUB[1] }),
  );

  socket.deliver(SUB[0]);
  await settle();
  expect(probe.calls()).toBe(1);
});

test("a remote selection opens no socket and subscribes to nothing", async () => {
  switchCluster("remote-1", "gen-1");
  const probe = mount();

  expect(FakeSocket.instances).toHaveLength(0);
  await settle();
  expect(probe.calls()).toBe(0);
});

test("a remote selection still subscribes to the cluster-agnostic notification feed", async () => {
  // In-app notifications are not cluster data: the feed lists every
  // cluster's notifications and the hub broadcasts their events whatever
  // cluster is selected, so the gate must not silence them.
  switchCluster("remote-1", "gen-1");
  let calls = 0;
  function Feed() {
    useWsRefetch(
      () => {
        calls++;
        return Promise.resolve();
      },
      [["notif-feed", "notifications", ""]],
      DEBOUNCE_MS,
    );
    return null;
  }
  setAccessToken("test-token");
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() => render(<Feed />, host as HTMLElement));

  expect(FakeSocket.instances).toHaveLength(1);
  const socket = FakeSocket.instances[0];
  socket.accept();
  expect(socket.sent).toContainEqual(
    expect.objectContaining({ type: "subscribe", id: "notif-feed" }),
  );
  socket.deliver("notif-feed");
  await settle();
  expect(calls).toBe(1);
});
