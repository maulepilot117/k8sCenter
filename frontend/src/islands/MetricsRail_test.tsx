import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import { json, render, settle, setupDom } from "@/src/test-utils/render.tsx";

const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: MetricsRail } = await import("./MetricsRail.tsx");

afterEach(() => {
  globalThis.fetch = bunFetch;
  setAccessToken(null);
});

function stubSeries(series: (slug: string) => string[]) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/monitoring/status")) {
      return json({ prometheus: { available: true } });
    }
    const slug = url.split("/monitoring/queries/")[1]?.split("?")[0] ?? "";
    const values = series(slug).map((v, i) => [i * 60, v]);
    return json({ result: values.length ? [{ values }] : [] });
  }) as unknown as typeof globalThis.fetch;
}

const imageNames = (root: HTMLElement) =>
  [...root.querySelectorAll('[role="img"]')].map((e) =>
    e.getAttribute("aria-label"),
  );

test("each KPI sparkline is named for its KPI and states its latest value", async () => {
  setAccessToken("t");
  stubSeries((slug) => (slug === "pods/cpu" ? ["0.1", "0.42"] : ["1", "2"]));
  const { container } = render(<MetricsRail kind="pods" name="web" />);
  await settle();

  const names = imageNames(container);
  expect(names).toContain("CPU, latest 0.42");
  expect(names).toContain("Memory, latest 2");
  expect(names.some((n) => n?.startsWith("Trend"))).toBe(false);
});

test("a KPI with a single sample still draws its sparkline", async () => {
  setAccessToken("t");
  stubSeries((slug) => (slug === "pods/cpu" ? ["0.42"] : []));
  const { container } = render(<MetricsRail kind="pods" name="web" />);
  await settle();

  expect(imageNames(container)).toEqual(["CPU, latest 0.42"]);
});
