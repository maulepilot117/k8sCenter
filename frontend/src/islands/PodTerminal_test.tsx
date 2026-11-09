/** @jsxImportSource react */
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  expect,
  mock,
  test,
} from "bun:test";
import { refreshSession, setAccessToken } from "@/lib/api.ts";
import {
  selectedCluster,
  selectedClusterGeneration,
} from "@/src/lib/cluster.ts";
import {
  act,
  body,
  render,
  setBrowser,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The pod terminal authenticates in-band like every other stream: a browser
 * WebSocket carries no X-Cluster-ID header, so the token and the session's
 * cluster go in the first frame, and no credential rides on the handshake.
 */
// Bun's own WebSocket, captured before setupDom registers happy-dom's.
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

/** Stands in for xterm, which needs a real canvas. */
class FakeTerminal {
  /** Every line written to any terminal, oldest first. */
  static lines: string[] = [];
  static instances: FakeTerminal[] = [];
  opened: HTMLElement | null = null;
  disposed = false;
  constructor() {
    FakeTerminal.instances.push(this);
  }
  open(el: HTMLElement) {
    this.opened = el;
  }
  loadAddon() {}
  focus() {}
  write() {}
  writeln(line: string) {
    FakeTerminal.lines.push(line);
  }
  onData() {}
  onResize() {}
  dispose() {
    this.disposed = true;
  }
}
mock.module("@xterm/xterm", () => ({ Terminal: FakeTerminal }));
mock.module("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));
mock.module("@xterm/addon-web-links", () => ({ WebLinksAddon: class {} }));

const { default: PodTerminal } = await import("./PodTerminal.tsx");

/** Records what the island sends; opening is driven by the test. */
class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static instances: FakeWebSocket[] = [];
  sent: unknown[] = [];
  binaryType = "";
  readyState = 0;
  closeCalls = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(
    public url: string,
    public protocols?: string | string[],
  ) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.closeCalls++;
    this.readyState = 3;
  }
}

/** Records observers so a test can see them disconnected. */
class FakeResizeObserver {
  static instances: FakeResizeObserver[] = [];
  disconnected = false;
  observed: Element[] = [];
  constructor() {
    FakeResizeObserver.instances.push(this);
  }
  observe(el: Element) {
    this.observed.push(el);
  }
  disconnect() {
    this.disconnected = true;
  }
}

const realWebSocket = globalThis.WebSocket;
const realResizeObserver = globalThis.ResizeObserver;
const realFetch = globalThis.fetch;

// The island opens a terminal only into a visible element, and happy-dom
// lays nothing out.
// The harness removes happy-dom in its own afterAll, which runs before this
// file's, so the prototype is held here.
const elementPrototype = HTMLElement.prototype;
beforeAll(() => {
  for (const prop of ["offsetWidth", "offsetHeight"]) {
    Object.defineProperty(elementPrototype, prop, {
      configurable: true,
      get: () => 100,
    });
  }
});

beforeEach(() => {
  FakeWebSocket.instances = [];
  FakeTerminal.lines = [];
  FakeTerminal.instances = [];
  FakeResizeObserver.instances = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.ResizeObserver =
    FakeResizeObserver as unknown as typeof ResizeObserver;
  setAccessToken("test-token");
});

afterEach(() => {
  globalThis.fetch = realFetch;
  globalThis.WebSocket = realWebSocket;
  globalThis.ResizeObserver = realResizeObserver;
  setAccessToken(null);
  selectedCluster.value = "local";
  selectedClusterGeneration.value = "local";
});

afterAll(() => {
  for (const prop of ["offsetWidth", "offsetHeight"]) {
    delete (elementPrototype as unknown as Record<string, unknown>)[prop];
  }
  globalThis.WebSocket = bunWebSocket;
});

function mount() {
  return render(
    <PodTerminal namespace="default" name="web-0" containers={["app"]} />,
  );
}

/** Clicks Open Terminal; returns once the click's synchronous part ran. */
function clickOpen() {
  const button = body().getByRole("button", { name: "Open Terminal" });
  act(() => button.click());
}

/** Waits for the session's async imports to finish and its socket to open. */
async function nextSocket(): Promise<FakeWebSocket> {
  for (let i = 0; i < 50 && FakeWebSocket.instances.length === 0; i++) {
    await act(async () => {
      await new Promise((res) => setTimeout(res, 0));
    });
  }
  const ws = FakeWebSocket.instances.at(-1);
  if (!ws) throw new Error("the terminal never opened a socket");
  return ws;
}

test("the terminal's first frame names the selected remote cluster, and the handshake carries no credential", async () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  mount();
  clickOpen();
  const ws = await nextSocket();

  expect(ws.url).toBe("ws://localhost/ws/v1/ws/exec/default/web-0/app");
  expect(ws.protocols).toBeUndefined();
  act(() => ws.onopen?.());
  expect(ws.sent[0]).toEqual({
    type: "auth",
    token: "test-token",
    clusterId: "c1",
    generation: "2026-10-01T12:30:00Z",
  });
});

test("a session stays on the cluster selected when it was opened", async () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  mount();
  clickOpen();
  // Switched while the session's imports are still loading.
  selectedCluster.value = "c2";
  selectedClusterGeneration.value = "2026-10-02T08:00:00Z";
  const ws = await nextSocket();

  act(() => ws.onopen?.());
  expect((ws.sent[0] as Record<string, unknown>).clusterId).toBe("c1");
});

test("a remote cluster's refusal shows the unsupported copy, not the server's wording", async () => {
  selectedCluster.value = "c1";
  selectedClusterGeneration.value = "2026-10-01T12:30:00Z";
  mount();
  clickOpen();
  const ws = await nextSocket();

  act(() => ws.onopen?.());
  act(() =>
    ws.onmessage?.({
      data: JSON.stringify({
        type: "error",
        message: "pod exec is not available on remote clusters",
        reason: "unsupported_platform",
      }),
    }),
  );
  act(() => ws.onclose?.());

  // Both places the error appears: the reconnect overlay and the terminal.
  const text = document.body.textContent ?? "";
  expect(text).toContain("Terminal is not available for this cluster yet.");
  expect(text).not.toContain("pod exec is not available on remote clusters");
  const pane = FakeTerminal.lines.join("\n");
  expect(pane).toContain("Terminal is not available for this cluster yet.");
  expect(pane).not.toContain("pod exec is not available on remote clusters");
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

test("a session opened during the startup refresh authenticates once it lands", async () => {
  setAccessToken(null);
  const { release } = gatedRefresh();
  const boot = refreshSession();

  mount();
  clickOpen();
  await act(async () => {
    await new Promise((res) => setTimeout(res, 20));
  });
  expect(FakeWebSocket.instances).toHaveLength(0);

  release();
  await boot;
  const ws = await nextSocket();
  act(() => ws.onopen?.());
  expect(ws.sent[0]).toMatchObject({ type: "auth", token: "fresh-token" });
});

test("unmounting closes the socket, disposes the terminal and disconnects the resize observer", async () => {
  const view = mount();
  clickOpen();
  const ws = await nextSocket();
  act(() => ws.onopen?.());
  expect(ws.closeCalls).toBe(0);
  expect(FakeTerminal.instances).toHaveLength(1);
  expect(FakeTerminal.instances[0].disposed).toBe(false);
  expect(FakeResizeObserver.instances).toHaveLength(1);
  expect(FakeResizeObserver.instances[0].disconnected).toBe(false);

  act(() => view.unmount());

  expect(ws.closeCalls).toBe(1);
  expect(FakeTerminal.instances[0].disposed).toBe(true);
  expect(FakeResizeObserver.instances[0].disconnected).toBe(true);
});

test("the terminal mounts after server render and hydration", async () => {
  const { hydrateRoot } = await import("react-dom/client");
  const { renderToString } = await import("react-dom/server");
  const element = (
    <PodTerminal namespace="default" name="web-0" containers={["app"]} />
  );

  setBrowser(false);
  const html = renderToString(element);
  expect(html).toBe("");

  const container = document.createElement("div");
  container.innerHTML = html;
  document.body.appendChild(container);
  setBrowser(true);
  let root: ReturnType<typeof hydrateRoot> | undefined;
  await act(() => {
    root = hydrateRoot(container, element);
  });
  await act(() => {
    root?.render(element);
  });

  const button = [...container.querySelectorAll("button")].find((b) =>
    b.textContent?.includes("Open Terminal"),
  ) as HTMLButtonElement;
  act(() => button.click());
  const ws = await nextSocket();

  expect(FakeTerminal.instances).toHaveLength(1);
  expect(container.contains(FakeTerminal.instances[0].opened)).toBe(true);
  expect(ws.url).toBe("ws://localhost/ws/v1/ws/exec/default/web-0/app");
  await act(() => root?.unmount());
  container.remove();
});
