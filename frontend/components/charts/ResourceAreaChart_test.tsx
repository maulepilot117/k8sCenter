/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { ResourceAreaChart } = await import("./ResourceAreaChart.tsx");

test("renders a placeholder when neither series has two points", () => {
  const { container } = render(
    <ResourceAreaChart cpuData={[10]} memData={null} />,
  );
  expect(container.querySelector("svg")).toBeNull();
  expect(container.textContent).toContain("Trend data unavailable");
});

test("renders both series with hyphenated SVG attributes", () => {
  const { container } = render(
    <ResourceAreaChart cpuData={[0, 50, 100]} memData={[20, 40]} />,
  );
  const svg = container.querySelector("svg") as SVGElement;
  expect(svg.getAttribute("viewBox")).toBe("0 0 400 80");
  expect(svg.getAttribute("preserveAspectRatio")).toBe("none");
  expect(svg.getAttribute("height")).toBe("80");

  const stops = [...svg.querySelectorAll("stop")];
  expect(stops).toHaveLength(4);
  expect(stops[0].getAttribute("stop-color")).toBe("var(--accent)");
  expect(stops[0].getAttribute("stop-opacity")).toBe("0.35");
  expect(stops[2].getAttribute("stop-color")).toBe("var(--accent-secondary)");

  const gradients = [...svg.querySelectorAll("linearGradient")];
  const cpuId = gradients[0].getAttribute("id") as string;
  const memId = gradients[1].getAttribute("id") as string;
  expect(cpuId).not.toBe("");
  expect(cpuId).not.toBe(memId);

  const paths = [...svg.querySelectorAll("path")];
  expect(paths).toHaveLength(4);
  // memory is drawn first, CPU on top
  expect(paths[0].getAttribute("fill")).toBe(`url(#${memId})`);
  expect(paths[1].getAttribute("d")).toBe("M4.0,61.6 L396.0,47.2");
  expect(paths[1].getAttribute("stroke")).toBe("var(--accent-secondary)");
  expect(paths[1].getAttribute("stroke-width")).toBe("1.8");
  expect(paths[1].getAttribute("stroke-linecap")).toBe("round");
  expect(paths[1].getAttribute("stroke-linejoin")).toBe("round");
  expect(paths[1].getAttribute("vector-effect")).toBe("non-scaling-stroke");
  expect(paths[2].getAttribute("fill")).toBe(`url(#${cpuId})`);
  expect(paths[3].getAttribute("d")).toBe("M4.0,76.0 L200.0,40.0 L396.0,4.0");
  expect(paths[2].getAttribute("d")).toBe(
    "M4.0,76.0 L200.0,40.0 L396.0,4.0 L396.0,76.0 L4.0,76.0 Z",
  );
});

test("one series alone leaves out the other's paths", () => {
  const { container } = render(
    <ResourceAreaChart cpuData={null} memData={[1, 2, 3]} />,
  );
  expect(container.querySelectorAll("path")).toHaveLength(2);
});

test("gradient ids stay stable across re-renders", () => {
  const { container, rerender } = render(
    <ResourceAreaChart cpuData={[1, 2]} memData={[1, 2]} />,
  );
  const id = container.querySelector("linearGradient")?.getAttribute("id");
  rerender(<ResourceAreaChart cpuData={[1, 5]} memData={[1, 2]} />);
  expect(container.querySelector("linearGradient")?.getAttribute("id")).toBe(
    id ?? "",
  );
});
