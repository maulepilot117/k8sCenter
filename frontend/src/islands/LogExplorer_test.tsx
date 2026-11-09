/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

/**
 * The explorer owns the tail and loading state and hands it to the filter
 * bar, which owns the filter controls. These tests drive the whole screen:
 * each control feeds the next lookup, a search carries every selected filter,
 * and the Run and Live Tail buttons follow the explorer's state. Bun's own
 * fetch and WebSocket are captured before happy-dom replaces them and put
 * back in afterAll.
 */
const bunFetch = globalThis.fetch;
const bunWebSocket = globalThis.WebSocket;
setupDom({ browser: true });

const { default: LogExplorer } = await import("./LogExplorer.tsx");
const { selectedNamespace } = await import("@/src/lib/namespace.ts");

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  sent: Record<string, unknown>[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.closed = true;
  }
}

let requested: string[] = [];
/** When set, a query waits for it before answering. */
let queryGate: Promise<void> | null = null;

function reply(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  FakeWebSocket.instances = [];
  requested = [];
  queryGate = null;
  globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    requested.push(url);
    if (url.includes("/v1/logs/status")) return reply({ detected: true });
    if (url.includes("/labels/namespace/values")) {
      return reply(["team-a", "team-b"]);
    }
    if (url.includes("/labels/pod/values")) return reply(["web-0", "web-1"]);
    if (url.includes("/labels/container/values")) {
      return reply(["app", "sidecar"]);
    }
    if (url.includes("/v1/logs/query")) {
      if (queryGate) await queryGate;
      return reply({
        data: {
          result: [
            {
              stream: { pod: "web-0", namespace: "team-a" },
              values: [["1762300800000000001", "hello from web"]],
            },
          ],
        },
      });
    }
    return reply({ data: { result: [] } });
  }) as unknown as typeof globalThis.fetch;
  setAccessToken("test-token");
});

afterEach(() => {
  setAccessToken(null);
  selectedNamespace.value = "all";
});

afterAll(() => {
  globalThis.fetch = bunFetch;
  globalThis.WebSocket = bunWebSocket;
});

const settle = () =>
  act(async () => {
    for (let round = 0; round < 4; round++) {
      for (let i = 0; i < 20; i++) await Promise.resolve();
      await new Promise((r) => setTimeout(r, 0));
    }
  });

function selects(): HTMLSelectElement[] {
  return [...document.querySelectorAll("select")];
}

function optionTexts(select: HTMLSelectElement): string[] {
  return [...select.options].map((o) => o.textContent ?? "");
}

function button(name: string): HTMLButtonElement {
  const found = [...document.querySelectorAll("button")].find(
    (b) => b.textContent?.trim() === name,
  );
  if (!found) throw new Error(`no button named ${name}`);
  return found as HTMLButtonElement;
}

function choose(select: HTMLSelectElement, value: string) {
  fireEvent.change(select, { target: { value } });
}

function lookups(kind: string): string[] {
  return requested.filter((u) => u.includes(`/labels/${kind}/values`));
}

function lastQuery(): URLSearchParams {
  const url = requested.filter((u) => u.includes("/v1/logs/query")).at(-1);
  if (!url) throw new Error("no query was sent");
  return new URL(url, "http://localhost").searchParams;
}

/** Mounts the explorer and waits for the namespace list to load. */
async function mountExplorer() {
  render(<LogExplorer />);
  await waitFor(() => expect(selects()[0].options.length).toBeGreaterThan(1));
}

test("choosing a namespace loads its pods and choosing a pod loads its containers", async () => {
  await mountExplorer();
  const [namespace, pod, container] = selects();
  expect(optionTexts(namespace)).toEqual([
    "All Namespaces",
    "team-a",
    "team-b",
  ]);
  expect(lookups("pod")).toEqual([]);

  choose(namespace, "team-a");
  await waitFor(() => expect(pod.options.length).toBe(3));
  expect(lookups("pod").length).toBe(1);
  expect(decodeURIComponent(lookups("pod")[0])).toContain(
    '{namespace="team-a"}',
  );
  expect(lookups("pod")[0]).toContain("namespace=team-a");
  expect(optionTexts(pod)).toEqual(["All Pods", "web-0", "web-1"]);
  expect(lookups("container")).toEqual([]);

  choose(pod, "web-1");
  await waitFor(() => expect(container.options.length).toBe(3));
  expect(lookups("container").length).toBe(1);
  expect(decodeURIComponent(lookups("container")[0])).toContain(
    '{namespace="team-a",pod="web-1"}',
  );
  expect(optionTexts(container)).toEqual(["All Containers", "app", "sidecar"]);
  expect(pod.value).toBe("web-1");

  // Changing the namespace clears the pod and container choices.
  choose(namespace, "team-b");
  await settle();
  expect(pod.value).toBe("");
  expect(container.value).toBe("");
});

test("a search sends a query that reflects every selected filter", async () => {
  await mountExplorer();
  const [namespace, pod, container, severity, timePreset] = selects();
  choose(namespace, "team-a");
  await waitFor(() => expect(pod.options.length).toBe(3));
  choose(pod, "web-0");
  await waitFor(() => expect(container.options.length).toBe(3));
  choose(container, "app");
  choose(severity, "error");
  choose(timePreset, "6h");
  const search = document.querySelector(
    'input[placeholder="Search text..."]',
  ) as HTMLInputElement;
  fireEvent.change(search, { target: { value: "boom" } });

  const before = Date.now();
  act(() => button("Run").click());
  await waitFor(() => expect(body().getByText("hello from web")).toBeTruthy());

  const sent = lastQuery();
  expect(sent.get("query")).toBe(
    '{namespace="team-a",pod=~"web-0.*",container="app"} | level=~"(?i)error" |= "boom"',
  );
  const end = Date.parse(sent.get("end") as string);
  expect(end - Date.parse(sent.get("start") as string)).toBe(
    6 * 60 * 60 * 1000,
  );
  expect(end).toBeGreaterThanOrEqual(before);
});

test("the LogQL toggle sends the text as the whole query", async () => {
  await mountExplorer();
  const [namespace] = selects();
  choose(namespace, "team-a");
  await settle();
  act(() => button("LogQL").click());
  const input = document.querySelector(
    'input[placeholder="LogQL query..."]',
  ) as HTMLInputElement;
  fireEvent.change(input, { target: { value: '{app="x"} |= "y"' } });
  act(() => button("Run").click());
  await waitFor(() => expect(body().getByText("hello from web")).toBeTruthy());
  expect(lastQuery().get("query")).toBe('{app="x"} |= "y"');
});

test("Run is disabled while a search is loading and comes back after", async () => {
  await mountExplorer();
  let release = () => {};
  queryGate = new Promise<void>((r) => {
    release = r;
  });
  act(() => button("Run").click());
  await settle();
  expect(button("Run").disabled).toBe(true);
  expect(body().getByText("Searching logs...")).toBeTruthy();
  release();
  await waitFor(() => expect(body().getByText("hello from web")).toBeTruthy());
  expect(button("Run").disabled).toBe(false);
});

test("live tail flips the bar into the tailing state and stop returns it", async () => {
  selectedNamespace.value = "team-a";
  await mountExplorer();
  expect(selects()[0].value).toBe("team-a");

  act(() => button("Live Tail").click());
  await settle();
  expect(button("Stop")).toBeTruthy();
  expect(FakeWebSocket.instances.length).toBe(1);
  const ws = FakeWebSocket.instances[0];
  act(() => ws.onopen?.());
  expect(ws.sent[1]).toEqual({
    type: "subscribe",
    query: '{namespace="team-a"}',
    namespace: "team-a",
    limit: 30,
  });
  // The tail replaces the search results.
  expect(body().queryByText(/No log entries found/)).toBeNull();

  act(() => button("Stop").click());
  await settle();
  expect(button("Live Tail")).toBeTruthy();
  expect(ws.closed).toBe(true);
  expect(body().getByText(/No log entries found/)).toBeTruthy();
});
