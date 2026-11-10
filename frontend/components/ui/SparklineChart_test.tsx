/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { SparklineChart } = await import("./SparklineChart.tsx");

test("fluid chart fills its container", () => {
  const { container } = render(
    <SparklineChart data={[0, 10, 5]} color="red" />,
  );
  const svg = container.querySelector("svg") as SVGElement;
  expect(svg.getAttribute("width")).toBe("100%");
  expect(svg.getAttribute("height")).toBe("28");
  expect(svg.getAttribute("viewBox")).toBe("0 0 100 28");
  expect(svg.getAttribute("preserveAspectRatio")).toBe("none");
  expect(svg.getAttribute("class")).toBe("block w-full");

  const line = svg.querySelector("polyline") as SVGElement;
  expect(line.getAttribute("points")).toBe("0,27 50,1 100,14");
  expect(line.getAttribute("stroke")).toBe("red");
  expect(line.getAttribute("stroke-width")).toBe("1.5");
  expect(line.getAttribute("stroke-linecap")).toBe("round");
  expect(line.getAttribute("stroke-linejoin")).toBe("round");
  expect(line.getAttribute("vector-effect")).toBe("non-scaling-stroke");

  const id = svg.querySelector("linearGradient")?.getAttribute("id") ?? "";
  expect(id).not.toBe("");
  expect(svg.querySelector("path")?.getAttribute("fill")).toBe(`url(#${id})`);
  expect(svg.querySelector("path")?.getAttribute("d")).toBe(
    "M0,28 L0,27 L50,1 L100,14 L100,28 Z",
  );
  const stops = [...svg.querySelectorAll("stop")];
  expect(stops[0].getAttribute("stop-color")).toBe("red");
  expect(stops[0].getAttribute("stop-opacity")).toBe("0.3");
  expect(stops[1].getAttribute("stop-opacity")).toBe("0");
});

test("a fixed width is honoured and the stroke is not made non-scaling", () => {
  const { container } = render(
    <SparklineChart data={[1, 2]} color="blue" width={60} height={20} />,
  );
  const svg = container.querySelector("svg") as SVGElement;
  expect(svg.getAttribute("width")).toBe("60");
  expect(svg.getAttribute("viewBox")).toBe("0 0 60 20");
  expect(svg.getAttribute("preserveAspectRatio")).toBeNull();
  expect(svg.getAttribute("class")).toBe("inline-block");
  expect(
    container.querySelector("polyline")?.getAttribute("vector-effect"),
  ).toBeNull();
});

test("non-finite samples are dropped and fewer than two points renders nothing", () => {
  const dropped = render(
    <SparklineChart data={[1, Number.NaN, 2]} color="red" />,
  );
  expect(
    dropped.container.querySelector("polyline")?.getAttribute("points"),
  ).toBe("0,27 100,1");
  dropped.unmount();

  const { container } = render(
    <SparklineChart data={[1, Number.NaN]} color="red" />,
  );
  expect(container.querySelector("svg")).toBeNull();
});
