import { expect, test } from "bun:test";
import { renderToString } from "react-dom/server";
import { render, settle, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { SparklineChart } = await import("./SparklineChart.tsx");
const { NetworkTile } = await import("./NetworkTile.tsx");
const { MetricTile } = await import("./MetricTile.tsx");

test("the chart fills its container at the given height and is named for the series", async () => {
  const { container, getByRole } = render(
    <SparklineChart
      data={[0, 10, 5]}
      color="var(--accent)"
      name="Memory"
      height={30}
    />,
  );
  await settle();
  const frame = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.dataset.chart).toBe("sparkline");
  expect(frame.style.width).toBe("100%");
  expect(frame.style.height).toBe("30px");
  expect(getByRole("img").getAttribute("aria-label")).toBe("Memory, latest 5");
  expect(container.querySelector("svg")).not.toBeNull();
});

test("the default height is 28 and a fixed width is honoured", () => {
  const { container } = render(
    <SparklineChart data={[1, 2]} color="var(--accent)" />,
  );
  const frame = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.style.height).toBe("28px");
  expect(frame.getAttribute("aria-label")).toBe("Trend, latest 2");

  const fixed = render(
    <SparklineChart data={[1, 2]} color="var(--accent)" width={60} />,
  );
  const sized = fixed.container.querySelector(
    "[data-chart-frame]",
  ) as HTMLElement;
  expect(sized.style.width).toBe("60px");
});

test("one point draws, and no usable samples draw nothing", async () => {
  const one = render(<SparklineChart data={[3]} color="var(--accent)" />);
  await settle();
  expect(one.container.querySelector("svg")).not.toBeNull();
  one.unmount();

  const none = render(
    <SparklineChart data={[Number.NaN]} color="var(--accent)" />,
  );
  const frame = none.container.querySelector(
    "[data-chart-frame]",
  ) as HTMLElement;
  expect(frame.dataset.chartState).toBe("empty");
  expect(none.container.querySelector("svg")).toBeNull();
});

test("the server renders the frame box and no svg", () => {
  const html = renderToString(
    <SparklineChart data={[1, 2, 3]} color="var(--accent)" height={22} />,
  );
  expect(html).toContain("height:22px");
  expect(html).not.toContain("<svg");
});

test("the network tile names its receive and transmit rows", async () => {
  const { getByRole } = render(
    <NetworkTile
      rxP95={4}
      txP95={2}
      rxData={[1, 2, 3]}
      txData={[5]}
      period="1h"
    />,
  );
  await settle();
  getByRole("img", { name: "Network receive, latest 3" });
  getByRole("img", { name: "Network transmit, latest 5" });
});

test("a metric tile names its sparkline for the tile label, even with one sample", async () => {
  const { getByRole } = render(
    <MetricTile label="CPU" value="42" sparkData={[42]} />,
  );
  await settle();
  getByRole("img", { name: "CPU, latest 42" });
});
