/** @jsxImportSource preact */
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  expect,
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
 * The pod terminal authenticates in-band like every other stream (KTD2): a
 * browser WebSocket carries no X-Cluster-ID header, so the token and the
 * session's cluster go in the first frame, and no credential rides on the
 * handshake.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

/** Stands in for xterm, which needs a real canvas. */
class FakeTerminal {
  /** Every line written to any terminal, oldest first. */
  static lines: string[] = [];
  open() {}
  loadAddon() {}
  focus() {}
  write() {}
  writeln(line: string) {
    FakeTerminal.lines.push(line);
  }
  onData() {}
  onResize() {}
  dispose() {}
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
  static OPEN = 1;
  static instances: FakeWebSocket[] = [];
  sent: unknown[] = [];
  binaryType = "";
  readyState = 0;
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
  close() {}
}

const realWebSocket = globalThis.WebSocket;
const realResizeObserver = globalThis.ResizeObserver;
let host: HTMLElement | null = null;

// The island opens a terminal only into a visible element, and happy-dom
// lays nothing out.
beforeAll(() => {
  for (const prop of ["offsetWidth", "offsetHeight"]) {
    Object.defineProperty(HTMLElement.prototype, prop, {
      configurable: true,
      get: () => 100,
    });
  }
});

beforeEach(() => {
  FakeWebSocket.instances = [];
  FakeTerminal.lines = [];
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.ResizeObserver = class {
    observe() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  setAccessToken("test-token");
});

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.WebSocket = realWebSocket;
  globalThis.ResizeObserver = realResizeObserver;
  setAccessToken(null);
  selectedCluster.value = "local";
  selectedClusterGeneration.value = "local";
});

afterAll(() => {
  for (const prop of ["offsetWidth", "offsetHeight"]) {
    delete (HTMLElement.prototype as unknown as Record<string, unknown>)[prop];
  }
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

function mount() {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <PodTerminal namespace="default" name="web-0" containers={["app"]} />,
      host as HTMLElement,
    ),
  );
}

/** Clicks Open Terminal; returns once the click's synchronous part ran. */
function clickOpen() {
  const button = [...(host as HTMLElement).querySelectorAll("button")].find(
    (b) => b.textContent?.includes("Open Terminal"),
  ) as HTMLButtonElement;
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
  const text = (host as HTMLElement).textContent ?? "";
  expect(text).toContain("Terminal is not available for this cluster yet.");
  expect(text).not.toContain("pod exec is not available on remote clusters");
  const pane = FakeTerminal.lines.join("\n");
  expect(pane).toContain("Terminal is not available for this cluster yet.");
  expect(pane).not.toContain("pod exec is not available on remote clusters");
});
