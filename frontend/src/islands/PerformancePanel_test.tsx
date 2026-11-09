/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

/**
 * When every query for a kind comes back empty, the Metrics tab shows one
 * message saying why instead of a grid of "No data" cards. A refresh keeps the
 * last results on screen until the new ones arrive.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: PerformancePanel } = await import("./PerformancePanel.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

const json = (data: unknown) =>
  new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

/** Answers the status probe, and each slug query with `series(slug)`. */
function stubPrometheus(series: (slug: string) => [number, string][] | null) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/monitoring/status")) {
      return json({ prometheus: { available: true } });
    }
    const slug = url.split("/monitoring/queries/")[1]?.split("?")[0] ?? "";
    const values = series(slug);
    return json({
      resultType: "matrix",
      result: values ? [{ metric: {}, values }] : [],
    });
  }) as unknown as typeof globalThis.fetch;
}

const settle = () =>
  act(async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function mount(kind: string) {
  setAccessToken("t");
  const view = render(
    <PerformancePanel kind={kind} name="p" namespace="argocd" />,
  );
  await settle();
  return view.container;
}

/** Captures the refresh timer so a test can fire it. */
function captureRefreshTimer() {
  const realSetInterval = globalThis.setInterval;
  const captured: { fire: (() => void) | null } = { fire: null };
  globalThis.setInterval = ((fn: () => void) => {
    captured.fire = fn;
    return 0;
  }) as unknown as typeof globalThis.setInterval;
  return {
    captured,
    restore: () => {
      globalThis.setInterval = realSetInterval;
    },
  };
}

test("a NetworkPolicy with no Hubble data shows one message naming the cause", async () => {
  stubPrometheus(() => null);
  const root = await mount("networkpolicies");

  expect(root.textContent).toContain("No Metric Data");
  expect(root.textContent).toContain("destinationContext");
  expect(root.textContent).not.toContain("Cilium Forwarded Flows");
});

test("a kind without a specific hint falls back to a generic message", async () => {
  stubPrometheus(() => null);
  const root = await mount("pvcs");

  expect(root.textContent).toContain("No Metric Data");
  expect(root.textContent).toContain("no data for this resource");
});

test("a refresh in flight keeps the last results on screen", async () => {
  const timer = captureRefreshTimer();
  try {
    stubPrometheus(() => null);
    const root = await mount("networkpolicies");
    expect(root.textContent).toContain("No Metric Data");
    expect(timer.captured.fire).not.toBeNull();

    // The refresh's queries never answer, so it stays in flight.
    globalThis.fetch = (() =>
      new Promise(() => {})) as unknown as typeof globalThis.fetch;
    await act(async () => {
      timer.captured.fire?.();
    });
    await settle();
    expect(root.textContent).toContain("No Metric Data");
    expect(root.textContent).not.toContain("Cilium Forwarded Flows");
  } finally {
    timer.restore();
  }
});

test("a refresh keeps the previous values until the new ones arrive", async () => {
  const timer = captureRefreshTimer();
  try {
    stubPrometheus((slug) =>
      slug === "networkpolicies/cilium-forwarded"
        ? [
            [1, "2"],
            [2, "3"],
          ]
        : null,
    );
    const root = await mount("networkpolicies");
    expect(root.textContent).toContain("Cilium Forwarded Flows");
    expect(root.textContent).toContain("3.0");
    expect(timer.captured.fire).not.toBeNull();

    // Hold the refresh's queries with deferred promises.
    const held: ((r: Response) => void)[] = [];
    globalThis.fetch = (() =>
      new Promise<Response>((resolve) => {
        held.push(resolve);
      })) as unknown as typeof globalThis.fetch;
    await act(async () => {
      timer.captured.fire?.();
    });
    await settle();
    expect(held.length).toBeGreaterThan(0);
    expect(root.textContent).toContain("3.0");
    expect(root.textContent).not.toContain("9.0");

    // The forwarded-flows query is the first slug of this kind.
    await act(async () => {
      held[0](
        json({
          resultType: "matrix",
          result: [
            {
              metric: {},
              values: [
                [1, "8"],
                [2, "9"],
              ],
            },
          ],
        }),
      );
      for (const resolve of held.slice(1)) {
        resolve(json({ resultType: "matrix", result: [] }));
      }
    });
    await settle();
    expect(root.textContent).toContain("9.0");
    expect(root.textContent).not.toContain("3.0");
  } finally {
    timer.restore();
  }
});

test("one query with data keeps the chart grid", async () => {
  stubPrometheus((slug) =>
    slug === "networkpolicies/cilium-forwarded"
      ? [
          [1, "2"],
          [2, "3"],
        ]
      : null,
  );
  const root = await mount("networkpolicies");

  expect(root.textContent).not.toContain("No Metric Data");
  expect(root.textContent).toContain("Cilium Forwarded Flows");
  expect(root.textContent).toContain("No data");
});
