/** @jsxImportSource preact */
import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import { setAccessToken } from "@/lib/api.ts";

/**
 * When every query for a kind comes back empty, the Metrics tab shows one
 * message saying why instead of a grid of "No data" cards. As in
 * NotificationBell_test.tsx, IS_BROWSER is stubbed to true for this island
 * and put back afterwards.
 */
GlobalRegistrator.register({ url: "http://localhost/" });
mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: true }));

const { default: PerformancePanel } = await import("./PerformancePanel.tsx");

const realFetch = globalThis.fetch;
let host: HTMLElement | null = null;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  globalThis.fetch = realFetch;
  setAccessToken(null);
});

afterAll(() => {
  mock.module("@/src/lib/is-browser.ts", () => ({ IS_BROWSER: false }));
  GlobalRegistrator.unregister();
});

/** Answers the status probe, and each slug query with `series(slug)`. */
function stubPrometheus(series: (slug: string) => [number, string][] | null) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    const json = (data: unknown) =>
      new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
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
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <PerformancePanel kind={kind} name="p" namespace="argocd" />,
      host as HTMLElement,
    ),
  );
  await settle();
  return host;
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
