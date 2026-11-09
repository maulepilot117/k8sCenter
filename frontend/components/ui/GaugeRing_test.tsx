/** @jsxImportSource react */
import { expect, test } from "bun:test";
import { act, render, setupDom, waitFor } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { GaugeRing } = await import("./GaugeRing.tsx");

const R = (80 - 6) / 2;
const CIRC = 2 * Math.PI * R;

function progress(container: Element): Element {
  return container.querySelectorAll("circle")[1];
}

test("arc starts hidden and animates to the target offset after 50ms", async () => {
  const { container } = render(<GaugeRing value={25} color="red" />);
  const svg = container.querySelector("svg") as SVGElement;
  expect(svg.getAttribute("viewBox")).toBe("0 0 80 80");
  const arc = progress(container);
  expect(arc.getAttribute("stroke-dasharray")).toBe(String(CIRC));
  expect(arc.getAttribute("stroke-dashoffset")).toBe(String(CIRC));
  expect(arc.getAttribute("stroke")).toBe("red");
  expect(arc.getAttribute("stroke-width")).toBe("6");
  expect(arc.getAttribute("stroke-linecap")).toBe("round");
  expect(arc.getAttribute("transform")).toBe("rotate(-90 40 40)");
  expect(arc.getAttribute("style")).toContain("stroke-dashoffset 1s");
  expect(
    container.querySelectorAll("circle")[0].getAttribute("stroke-width"),
  ).toBe("6");

  await waitFor(() =>
    expect(progress(container).getAttribute("stroke-dashoffset")).toBe(
      String(CIRC - 0.25 * CIRC),
    ),
  );
  expect(container.textContent).toBe("25%");
});

test("a changed value re-animates to the new offset", async () => {
  const { container, rerender } = render(<GaugeRing value={25} color="red" />);
  await waitFor(() =>
    expect(progress(container).getAttribute("stroke-dashoffset")).toBe(
      String(CIRC - 0.25 * CIRC),
    ),
  );
  rerender(<GaugeRing value={100} color="red" />);
  await act(async () => {
    await new Promise((r) => setTimeout(r, 100));
  });
  expect(progress(container).getAttribute("stroke-dashoffset")).toBe("0");
});

test("unmounting before the delay cancels the timer without error", async () => {
  const { unmount } = render(<GaugeRing value={50} color="red" />);
  unmount();
  await new Promise((r) => setTimeout(r, 100));
});

test("indeterminate fills the arc and skips the gradient", async () => {
  const { container } = render(
    <GaugeRing value={0} color="gray" secondaryColor="blue" indeterminate />,
  );
  expect(container.querySelector("linearGradient")).toBeNull();
  await waitFor(() =>
    expect(progress(container).getAttribute("stroke-dashoffset")).toBe("0"),
  );
  expect(progress(container).getAttribute("stroke")).toBe("gray");
});

test("secondary color adds a gradient referenced by the arc", () => {
  const { container } = render(
    <GaugeRing
      value={60}
      color="red"
      secondaryColor="blue"
      label="CPU"
      displayValue="6/10"
    />,
  );
  const grad = container.querySelector("linearGradient") as Element;
  const id = grad.getAttribute("id") ?? "";
  expect(id).not.toBe("");
  expect(grad.getAttribute("x2")).toBe("1");
  const stops = [...grad.querySelectorAll("stop")];
  expect(stops[0].getAttribute("stop-color")).toBe("red");
  expect(stops[1].getAttribute("stop-color")).toBe("blue");
  expect(progress(container).getAttribute("stroke")).toBe(`url(#${id})`);
  expect(container.textContent).toContain("6/10");
  expect(container.textContent).toContain("CPU");
});
