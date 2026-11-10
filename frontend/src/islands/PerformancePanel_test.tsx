/** @jsxImportSource react */
import { afterEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  settle as drawn,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

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

/** Series for `slug`, one sample a minute from the request's own start time. */
function stubRange(
  series: (slug: string, count: number) => string[] | null,
  count = 61,
) {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    if (url.includes("/monitoring/status")) {
      return json({ prometheus: { available: true } });
    }
    const [path, query = ""] = url.split("/monitoring/queries/")[1].split("?");
    const start = Date.parse(new URLSearchParams(query).get("start") ?? "");
    const values = series(path, count);
    return json({
      resultType: "matrix",
      result: values
        ? [
            {
              metric: {},
              values: values.map((v, i) => [start / 1000 + i * 60, v]),
            },
          ]
        : [],
    });
  }) as unknown as typeof globalThis.fetch;
}

const clock = (ms: number) =>
  new Date(ms).toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
  });

function chartNamed(root: HTMLElement, title: string): HTMLElement {
  const card = [...root.querySelectorAll("h3")].find(
    (h) => h.textContent === title,
  )?.parentElement;
  if (!card) throw new Error(`no chart card titled ${title}`);
  return card as HTMLElement;
}

async function focusChart(card: HTMLElement) {
  await drawn();
  const chart = card.querySelector('[tabindex="0"]') as Element;
  expect(chart).not.toBeNull();
  await act(async () => {
    fireEvent.focus(chart);
  });
  return card.querySelector('[role="status"]') as HTMLElement;
}

test("a memory chart reads in MiB and GiB and its tooltip shows the first sample's time", async () => {
  // The backend reports memory in MB: 1536 MB is 1.5 GiB.
  stubRange((slug) =>
    slug === "deployments/memory" ? ["1536", "1537"] : null,
  );
  const root = await mount("deployments");
  await drawn();

  const card = chartNamed(root, "Memory Usage (MB)");
  const frame = card.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.dataset.chartPreset).toBe("axis");
  expect(frame.dataset.chartState).toBe("ready");

  const tip = await focusChart(card);
  expect(tip.textContent).toContain("1.5 GiB");
  // The first sample sits at the start of the queried hour.
  const hourAgo = Date.now() - 3600_000;
  const times = [0, 1].map((m) => clock(hourAgo + m * 60_000));
  expect(times.some((t) => tip.textContent?.includes(t))).toBe(true);
  // The time axis spans the queried hour: it has ticks and the value axis is in GiB.
  const ticks = [...card.querySelectorAll("text")].map((t) => t.textContent);
  expect(ticks.some((t) => /^\d{2}:\d{2}$/.test(t ?? ""))).toBe(true);
  expect(ticks.some((t) => /GiB$/.test(t ?? ""))).toBe(true);
});

test("a CPU chart reads in cores", async () => {
  stubRange((slug) => (slug === "deployments/cpu" ? ["0.42", "0.43"] : null));
  const root = await mount("deployments");
  const card = chartNamed(root, "CPU Usage (cores)");
  const tip = await focusChart(card);
  expect(tip.textContent).toContain("0.42 cores");
  expect(card.textContent).toContain("0.43 cores");
});

test("a single sample draws a flat chart with a padded value axis", async () => {
  stubRange((slug) => (slug === "deployments/cpu" ? ["2"] : null), 1);
  const root = await mount("deployments");
  await drawn();
  const card = chartNamed(root, "CPU Usage (cores)");
  expect(card.querySelector("[data-chart-frame]")).not.toBeNull();
  expect(card.textContent).not.toContain("No data");
  const valueTicks = [...card.querySelectorAll("text")]
    .map((t) => t.textContent ?? "")
    .filter((t) => /cores?$/.test(t));
  // A padded domain gives the axis more than one distinct label.
  expect(new Set(valueTicks).size).toBeGreaterThan(1);
  expect(card.querySelector("circle")).not.toBeNull();
});

test("a refresh in flight keeps the previous axes and line", async () => {
  const timer = captureRefreshTimer();
  try {
    stubRange((slug) => (slug === "deployments/cpu" ? ["0.42", "0.43"] : null));
    const root = await mount("deployments");
    await drawn();
    const before = chartNamed(root, "CPU Usage (cores)");
    const labels = [...before.querySelectorAll("text")].map(
      (t) => t.textContent,
    );
    expect(labels.length).toBeGreaterThan(0);

    globalThis.fetch = (() =>
      new Promise(() => {})) as unknown as typeof globalThis.fetch;
    await act(async () => {
      timer.captured.fire?.();
    });
    await drawn();
    const after = chartNamed(root, "CPU Usage (cores)");
    expect(
      [...after.querySelectorAll("text")].map((t) => t.textContent),
    ).toEqual(labels);
    expect(after.querySelector("path.recharts-area-curve")).not.toBeNull();
  } finally {
    timer.restore();
  }
});
