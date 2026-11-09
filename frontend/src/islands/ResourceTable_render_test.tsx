/** @jsxImportSource react */
import { afterAll, beforeAll, beforeEach, expect, test } from "bun:test";
import { within } from "@testing-library/preact";
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

let requests: Array<{ url: string; method: string }> = [];
let deletes: string[] = [];

function stubList(items: K8sResource[]) {
  requests = [];
  deletes = [];
  // Only the resource list returns the rows; saved views and the rest are empty.
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push({ url: String(input), method: init?.method ?? "GET" });
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

async function mountWithSocket(initial: K8sResource[] = []) {
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.requestAnimationFrame = ((cb: () => void) => {
    frames.push(cb);
    return frames.length;
  }) as unknown as typeof requestAnimationFrame;
  globalThis.cancelAnimationFrame = (() => {}) as typeof cancelAnimationFrame;
  stubList(initial);
  const view = render(<ResourceTable kind="pods" title="Pods" />);
  await waitFor(() =>
    initial.length > 0
      ? view.getByText(initial[0].metadata.name)
      : view.getByText("No pods found"),
  );
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

function event(eventType: string, object?: K8sResource) {
  return { type: "event", id: "pods-all", eventType, object };
}

test("one frame holding MODIFIED, DELETED and ADDED applies all three", async () => {
  const { view, sock } = await mountWithSocket([
    pod("alpha", "uid-a"),
    pod("beta", "uid-b"),
    pod("gamma", "uid-c"),
  ]);
  await act(async () => {
    sock.receive(event("MODIFIED", pod("alpha-renamed", "uid-a")));
    sock.receive(event("DELETED", pod("beta", "uid-b")));
    sock.receive(event("ADDED", pod("delta", "uid-d")));
  });
  expect(frames.length).toBe(1);
  await act(async () => {
    flushFrame();
  });
  view.getByText("alpha-renamed");
  expect(view.queryByText("alpha")).toBeNull();
  expect(view.queryByText("beta")).toBeNull();
  view.getByText("gamma");
  view.getByText("delta");
});

test("a resync_required message refetches the list instead of applying rows", async () => {
  const { view, sock } = await mountWithSocket([pod("alpha", "uid-a")]);
  const listRequests = () =>
    requests.filter((r) => r.url.includes("/v1/resources/pods")).length;
  const before = listRequests();
  await act(async () => {
    sock.receive(event("ADDED", pod("late", "uid-late")));
    sock.receive({ type: "resync_required", id: "pods-all" });
  });
  await act(async () => {
    flushFrame();
  });
  await waitFor(() => expect(listRequests()).toBe(before + 1));
  // The queued ADDED was dropped with the resync; the refetch owns the rows.
  expect(view.queryByText("late")).toBeNull();
  view.getByText("alpha");
});

test("confirming a row delete twice before the request resolves sends one DELETE", async () => {
  const { view } = await mountWithSocket([pod("alpha", "uid-a")]);
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const inner = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "DELETE") {
      deletes.push(String(input));
      await gate;
      return new Response(null, { status: 204 });
    }
    return inner(input, init);
  }) as unknown as typeof fetch;

  await act(async () => {
    fireEvent.click(view.getByTitle("Actions"));
  });
  await act(async () => {
    fireEvent.click(view.getByText("Delete"));
  });
  await act(async () => {
    fireEvent.input(view.getByPlaceholderText("alpha"), {
      target: { value: "alpha" },
    });
  });
  const dialog = view.getByRole("dialog");
  const confirm = within(dialog).getByText("Delete");
  // Both clicks land before React renders the loading state.
  await act(async () => {
    fireEvent.click(confirm);
    fireEvent.click(confirm);
  });
  expect(deletes.length).toBe(1);
  await act(async () => {
    release();
  });
  expect(deletes.length).toBe(1);
});
