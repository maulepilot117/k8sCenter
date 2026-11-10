import { afterEach, expect, test } from "bun:test";
import { renderToString } from "react-dom/server";
import { act, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: ChartFrame } = await import("./ChartFrame.tsx");
const { ChartTooltip, TimeXAxis, ValueYAxis } = await import("./ChartAxes.tsx");
const { AXIS_CHART_PROPS, BARE_CHART_PROPS } = await import("./chart-theme.ts");
const { Line, LineChart } = await import("recharts");

const MINUTE = 60_000;
const END = new Date(2026, 10, 10, 14, 0).getTime();
const START = END - 60 * MINUTE;
const ROWS = Array.from({ length: 31 }, (_, i) => ({
  t: START + i * 2 * MINUTE,
  v: 40 + (i % 4),
}));

/** Lets Recharts measure its container and draw. */
async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 30));
  });
}

function AxisChart() {
  return (
    <ChartFrame preset="axis" kind="cpu" label="CPU usage over the last hour">
      <LineChart {...AXIS_CHART_PROPS} data={ROWS}>
        <TimeXAxis dataKey="t" start={START} end={END} />
        <ValueYAxis unit="percent" values={ROWS.map((r) => r.v)} />
        <Line dataKey="v" name="CPU" isAnimationActive={false} />
        <ChartTooltip unit="percent" rangeMs={END - START} />
      </LineChart>
    </ChartFrame>
  );
}

// Put the prototype back exactly as it was: leaving an own property behind
// would shadow stubs other test files set on Element.prototype.
const ownRect = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  "getBoundingClientRect",
);
afterEach(() => {
  if (ownRect) {
    Object.defineProperty(
      HTMLElement.prototype,
      "getBoundingClientRect",
      ownRect,
    );
  } else {
    delete (HTMLElement.prototype as Partial<HTMLElement>)
      .getBoundingClientRect;
  }
});

test("no data shows the empty message and no chart", () => {
  const { container, getByText } = render(
    <ChartFrame
      preset="axis"
      kind="cpu"
      label="CPU"
      empty
      emptyMessage="No metric data"
    >
      <LineChart {...AXIS_CHART_PROPS} data={ROWS}>
        <Line dataKey="v" />
      </LineChart>
    </ChartFrame>,
  );
  getByText("No metric data");
  const frame = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.dataset.chartState).toBe("empty");
  expect(container.querySelector("svg")).toBeNull();
});

test("the server renders a box of the preset height before any chart draws", () => {
  const axis = renderToString(
    <ChartFrame preset="axis" kind="cpu" label="CPU">
      <LineChart {...AXIS_CHART_PROPS} data={ROWS}>
        <Line dataKey="v" />
      </LineChart>
    </ChartFrame>,
  );
  expect(axis).toContain('data-chart-preset="axis"');
  expect(axis).toContain("height:200px");
  expect(axis).not.toContain("<svg");

  const tile = renderToString(
    <ChartFrame preset="tile" kind="sparkline" label="CPU">
      <span />
    </ChartFrame>,
  );
  expect(tile).toContain("height:28px");

  const sized = renderToString(
    <ChartFrame
      preset="tile"
      kind="gauge"
      label="Health"
      height={80}
      width={80}
    >
      <span />
    </ChartFrame>,
  );
  expect(sized).toContain("width:80px");
  expect(sized).toContain("height:80px");
});

test("a bare chart is one labelled image and not a tab stop", async () => {
  const { container, getAllByRole } = render(
    <ChartFrame preset="tile" kind="sparkline" label="CPU, latest 43%">
      <LineChart {...BARE_CHART_PROPS} data={ROWS}>
        <Line dataKey="v" isAnimationActive={false} dot={false} />
      </LineChart>
    </ChartFrame>,
  );
  await settle();
  const images = getAllByRole("img");
  expect(images).toHaveLength(1);
  expect(images[0].getAttribute("aria-label")).toBe("CPU, latest 43%");
  expect(container.querySelector("svg")).not.toBeNull();
  expect(container.querySelector('[tabindex="0"]')).toBeNull();
});

test("an axis chart is reachable by keyboard and focus opens its tooltip", async () => {
  const { container, getByRole } = render(<AxisChart />);
  await settle();
  const figure = getByRole("figure", { name: "CPU usage over the last hour" });
  const chart = figure.querySelector('[tabindex="0"]') as Element;
  expect(chart).not.toBeNull();

  await act(async () => {
    fireEvent.focus(chart);
  });
  const tip = container.querySelector('[role="status"]') as HTMLElement;
  expect(tip.textContent).toContain("13:00");
  expect(tip.textContent).toContain("CPU");
  expect(tip.textContent).toContain("40%");

  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("13:02");
  expect(tip.textContent).toContain("41%");
});

test("a 375px axis chart draws no more time ticks than fit", async () => {
  HTMLElement.prototype.getBoundingClientRect = () =>
    ({
      width: 375,
      height: 200,
      top: 0,
      left: 0,
      right: 375,
      bottom: 200,
      x: 0,
      y: 0,
      toJSON() {},
    }) as DOMRect;
  const { container } = render(<AxisChart />);
  await settle();
  const timeLabels = [...container.querySelectorAll("text")]
    .map((t) => t.textContent ?? "")
    .filter((s) => /^\d{2}:\d{2}$/.test(s));
  expect(timeLabels.length).toBeGreaterThanOrEqual(2);
  expect(timeLabels.length).toBeLessThanOrEqual(4);
});
