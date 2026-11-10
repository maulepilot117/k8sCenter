import { expect, test } from "bun:test";
import { renderToString } from "react-dom/server";
import { render, settle, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: Sparkline } = await import("./Sparkline.tsx");

function frameOf(container: HTMLElement): HTMLElement {
  return container.querySelector("[data-chart-frame]") as HTMLElement;
}

test("twenty points are one image named for the series and its latest value", async () => {
  const data = Array.from({ length: 20 }, (_, i) => i);
  const { container, getAllByRole } = render(
    <Sparkline data={data} name="CPU usage" height={32} />,
  );
  await settle();
  const images = getAllByRole("img");
  expect(images).toHaveLength(1);
  expect(images[0].getAttribute("aria-label")).toBe("CPU usage, latest 19");
  const frame = frameOf(container);
  expect(frame.dataset.chart).toBe("sparkline");
  expect(frame.dataset.chartState).toBe("ready");
  expect(frame.style.height).toBe("32px");
  expect(container.querySelector("svg")).not.toBeNull();
});

test("a series with no name still has a label", () => {
  const { getByRole } = render(<Sparkline data={[1, 2, 3]} />);
  expect(getByRole("img").getAttribute("aria-label")).toBe("Trend, latest 3");
});

test("one point and a constant series both draw", async () => {
  const one = render(<Sparkline data={[7]} name="Pods" />);
  await settle();
  expect(frameOf(one.container).dataset.chartState).toBe("ready");
  expect(one.container.querySelector("svg")).not.toBeNull();
  one.unmount();

  const flat = render(<Sparkline data={[5, 5, 5, 5]} name="Pods" />);
  await settle();
  expect(frameOf(flat.container).dataset.chartState).toBe("ready");
  expect(flat.container.querySelector("svg")).not.toBeNull();
});

test("an empty series, or only non-finite samples, draws nothing", () => {
  const empty = render(<Sparkline data={[]} />);
  expect(frameOf(empty.container).dataset.chartState).toBe("empty");
  expect(empty.container.querySelector("svg")).toBeNull();
  empty.unmount();

  const bad = render(<Sparkline data={[Number.NaN, Infinity]} />);
  expect(frameOf(bad.container).dataset.chartState).toBe("empty");
});

test("non-finite samples are dropped before the latest value is read", () => {
  const { getByRole } = render(
    <Sparkline data={[1, 2, Number.NaN]} name="Pods" />,
  );
  expect(getByRole("img").getAttribute("aria-label")).toBe("Pods, latest 2");
});

test("the line is drawn in the given stroke", async () => {
  const { container } = render(
    <Sparkline data={[1, 3, 2]} stroke="var(--accent-secondary)" />,
  );
  await settle();
  // Read from the path Recharts renders for the series line.
  const line = container.querySelector(".recharts-area-curve") as Element;
  expect(line.getAttribute("stroke")).toBe("var(--accent-secondary)");
});

test("the server renders the frame box at the given height and no svg", () => {
  const html = renderToString(<Sparkline data={[1, 2, 3]} height={34} />);
  expect(html).toContain('data-chart="sparkline"');
  expect(html).toContain("height:34px");
  expect(html).not.toContain("<svg");
});
