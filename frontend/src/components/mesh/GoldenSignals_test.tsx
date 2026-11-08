/** @jsxImportSource react */
import { afterAll, afterEach, expect, test } from "bun:test";
import "react";
import {
  act,
  body,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { MeshGoldenSignals } = await import("./GoldenSignals.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
});
afterAll(() => {
  globalThis.fetch = bunFetch;
});

interface Signals {
  available: boolean;
  reason?: string;
  missingQueries?: string[];
  rps: number;
  errorRate: number;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

const live: Signals = {
  available: true,
  rps: 12.5,
  errorRate: 0.02,
  p50Ms: 5,
  p95Ms: 250,
  p99Ms: 1500,
};

/** Routes by URL; records every request path. */
function stubFetch(
  opts: {
    detected?: string;
    signals?: (url: string) => Signals | Response | Promise<Signals>;
  } = {},
) {
  const urls: string[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input);
    urls.push(url);
    const json = (b: unknown, status = 200) =>
      new Response(JSON.stringify(b), {
        status,
        headers: { "Content-Type": "application/json" },
      });
    if (url.includes("/v1/mesh/status")) {
      return json({
        data: { status: { detected: opts.detected ?? "istio" } },
      });
    }
    const s = await (opts.signals?.(url) ?? live);
    if (s instanceof Response) return s;
    return json({ data: { signals: { mesh: "istio", ...s } } });
  }) as unknown as typeof fetch;
  return urls;
}

test("renders the tiles from the golden-signals response", async () => {
  const urls = stubFetch();
  render(<MeshGoldenSignals namespace="shop" service="web" />);
  await waitFor(() => body().getByText("13 req/s"));
  body().getByText("2.00%");
  body().getByText("250 ms");
  body().getByText("1.50s");
  body().getByText("istio");
  const call = urls.find((u) => u.includes("golden-signals")) ?? "";
  const q = new URL(call, "http://x").searchParams;
  expect(q.get("namespace")).toBe("shop");
  expect(q.get("service")).toBe("web");
  expect(q.has("mesh")).toBe(false);
});

test("shows the unavailable banner when Prometheus is offline", async () => {
  stubFetch({
    signals: () => ({ ...live, available: false, reason: "unreachable" }),
  });
  render(<MeshGoldenSignals namespace="shop" service="web" />);
  await waitFor(() => body().getByText(/Prometheus could not be read/));
});

test("hides itself when no mesh is installed, never asking for signals", async () => {
  const urls = stubFetch({ detected: "" });
  const { container } = render(
    <MeshGoldenSignals namespace="shop" service="web" />,
  );
  await waitFor(() => expect(urls.length).toBe(1));
  await act(async () => {});
  expect(container.innerHTML).toBe("");
  expect(urls.some((u) => u.includes("golden-signals"))).toBe(false);
});

test("hides itself when the request fails or the service is silent", async () => {
  stubFetch({
    signals: () =>
      new Response(JSON.stringify({ error: { message: "no" } }), {
        status: 404,
      }),
  });
  const failed = render(<MeshGoldenSignals namespace="shop" service="web" />);
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(failed.container.innerHTML).toBe("");
  failed.unmount();

  stubFetch({
    signals: () => ({
      ...live,
      rps: 0,
      errorRate: 0,
      p50Ms: 0,
      p95Ms: 0,
      p99Ms: 0,
    }),
  });
  const silent = render(<MeshGoldenSignals namespace="shop" service="web" />);
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(silent.container.innerHTML).toBe("");
});

test("flags partial metrics with an em dash for the failed query", async () => {
  stubFetch({ signals: () => ({ ...live, missingQueries: ["p95"] }) });
  render(<MeshGoldenSignals namespace="shop" service="web" />);
  await waitFor(() => body().getByText("Partial metrics"));
  body().getByText("—");
});

test("a response that lands after the service changed does not overwrite the new one", async () => {
  const release: Record<string, (s: Signals) => void> = {};
  stubFetch({
    signals: (url) =>
      new Promise<Signals>((resolve) => {
        release[new URL(url, "http://x").searchParams.get("service") ?? ""] =
          resolve;
      }),
  });
  const view = render(<MeshGoldenSignals namespace="shop" service="old" />);
  await waitFor(() => expect(release.old).toBeDefined());
  view.rerender(<MeshGoldenSignals namespace="shop" service="new" />);
  await waitFor(() => expect(release.new).toBeDefined());

  await act(async () => {
    release.new({ ...live, rps: 777 });
  });
  await waitFor(() => body().getByText("777 req/s"));
  await act(async () => {
    release.old({ ...live, rps: 111 });
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(body().queryByText("111 req/s")).toBeNull();
  body().getByText("777 req/s");
});

test("unmounting stops the 30s refresh", async () => {
  const realSetInterval = globalThis.setInterval;
  const realClear = globalThis.clearInterval;
  const live30: number[] = [];
  const handlers = new Map<number, () => void>();
  globalThis.setInterval = ((fn: () => void, ms: number) => {
    const id = realSetInterval(() => {}, 1_000_000) as unknown as number;
    if (ms === 30_000) {
      live30.push(id);
      handlers.set(id, fn);
    }
    return id;
  }) as unknown as typeof setInterval;
  globalThis.clearInterval = ((id: number) => {
    const i = live30.indexOf(id);
    if (i >= 0) live30.splice(i, 1);
    realClear(id);
  }) as unknown as typeof clearInterval;
  try {
    stubFetch();
    const view = render(<MeshGoldenSignals namespace="shop" service="web" />);
    await waitFor(() => body().getByText("13 req/s"));
    expect(live30.length).toBe(1);
    await act(async () => view.unmount());
    expect(live30.length).toBe(0);
  } finally {
    globalThis.setInterval = realSetInterval;
    globalThis.clearInterval = realClear;
  }
});
