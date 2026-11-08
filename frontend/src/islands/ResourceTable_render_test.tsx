/** @jsxImportSource react */
import { afterAll, beforeAll, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { disconnectWS } from "@/src/lib/ws.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch and WebSocket, captured before happy-dom replaces them.
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
const bunRaf = globalThis.requestAnimationFrame;
const bunCancelRaf = globalThis.cancelAnimationFrame;
setupDom({ browser: true });

const { default: ResourceTable } = await import("./ResourceTable.tsx");

function pod(name: string, uid = name): K8sResource {
  return {
    metadata: {
      name,
      uid,
      namespace: "default",
      creationTimestamp: "2026-01-01T00:00:00Z",
    },
    status: { phase: "Running" },
  } as unknown as K8sResource;
}

function stubList(items: K8sResource[]) {
  // Only the resource list returns the rows; saved views and the rest are empty.
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const isList = String(input).includes("/v1/resources/");
    return new Response(
      JSON.stringify({ data: isList ? items : [], metadata: {} }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }) as unknown as typeof fetch;
}

/** A WebSocket the test drives: it opens, authenticates and delivers events. */
class FakeWebSocket {
  static OPEN = 1;
  static CONNECTING = 0;
  static instances: FakeWebSocket[] = [];
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: string[] = [];
  constructor(_url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
  receive(msg: unknown) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
}

/** requestAnimationFrame the test flushes by hand. */
let frames: Array<() => void> = [];
function flushFrame() {
  const pending = frames;
  frames = [];
  for (const f of pending) f();
}

beforeAll(() => {
  setAccessToken("test-token");
});

beforeEach(() => {
  frames = [];
  FakeWebSocket.instances = [];
  disconnectWS();
});

afterAll(() => {
  for (const s of FakeWebSocket.instances) s.readyState = 3;
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
  globalThis.requestAnimationFrame = bunRaf;
  globalThis.cancelAnimationFrame = bunCancelRaf;
  setAccessToken(null);
});

test("the toolbar count reads N of M match only while a filter narrows the list", async () => {
  stubList([pod("alpha"), pod("beta"), pod("alphabet")]);
  const view = render(
    <ResourceTable kind="pods" title="Pods" enableWS={false} />,
  );
  await waitFor(() => view.getByText("alphabet"));
  // Nothing narrows the list, and the page header carries the count.
  expect(view.queryByTestId("toolbar-count")).toBeNull();

  const search = view.getByPlaceholderText("Search pods...");
  await act(async () => {
    fireEvent.input(search, { target: { value: "alpha" } });
  });
  await waitFor(() =>
    expect(view.getByTestId("toolbar-count").textContent).toBe("2 of 3 match"),
  );

  await act(async () => {
    fireEvent.input(search, { target: { value: "" } });
  });
  await waitFor(() => expect(view.queryByTestId("toolbar-count")).toBeNull());
});

async function mountWithSocket() {
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.requestAnimationFrame = ((cb: () => void) => {
    frames.push(cb);
    return frames.length;
  }) as unknown as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = (() => {}) as typeof cancelAnimationFrame;
  stubList([]);
  const view = render(<ResourceTable kind="pods" title="Pods" />);
  await waitFor(() => view.getByText("No pods found"));
  await waitFor(() => expect(FakeWebSocket.instances.length).toBe(1));
  const sock = FakeWebSocket.instances[0];
  await act(async () => {
    sock.readyState = 1;
    sock.onopen?.();
  });
  // The ws store sends auth on open (after reading the token), then waits.
  await waitFor(() =>
    expect(sock.sent.some((m) => m.includes('"type":"auth"'))).toBe(true),
  );
  await act(async () => {
    sock.receive({ type: "auth_ok" });
  });
  await waitFor(() =>
    expect(sock.sent.some((m) => m.includes('"subscribe"'))).toBe(true),
  );
  return { view, sock };
}

function deliver(sock: FakeWebSocket, names: string[]) {
  for (const name of names) {
    sock.receive({
      type: "event",
      id: "pods-all",
      eventType: "ADDED",
      object: pod(name),
    });
  }
}

const names = (from: number, count: number) =>
  Array.from(
    { length: count },
    (_, i) => `burst-${String(from + i).padStart(3, "0")}`,
  );

test("a burst of 50 row updates inside one animation frame all reach the table", async () => {
  const { view, sock } = await mountWithSocket();
  const burst = names(0, 50);
  await act(async () => {
    deliver(sock, burst);
  });
  // Queued for the frame, not applied yet.
  expect(view.queryByText(burst[0])).toBeNull();
  expect(frames.length).toBe(1);

  await act(async () => {
    flushFrame();
  });
  for (const n of burst) view.getByText(n);
  expect(body().queryByText("No pods found")).toBeNull();
});

test("rows from a later frame are added to the rows an earlier frame delivered", async () => {
  const { view, sock } = await mountWithSocket();
  const first = names(0, 5);
  const second = names(5, 5);
  await act(async () => {
    deliver(sock, first);
    flushFrame();
  });
  for (const n of first) view.getByText(n);
  await act(async () => {
    deliver(sock, second);
    flushFrame();
  });
  for (const n of [...first, ...second]) view.getByText(n);
});
