import { expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { GaugeRing } = await import("./GaugeRing.tsx");

async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 30));
  });
}

function frame(container: Element): HTMLElement {
  return container.querySelector("[data-chart-frame]") as HTMLElement;
}

test("a ring is one image labelled with its value, sized to the frame", async () => {
  const { container } = render(<GaugeRing value={25} color="red" />);
  await settle();
  const f = frame(container);
  expect(f.dataset.chart).toBe("gauge");
  expect(f.getAttribute("role")).toBe("img");
  expect(f.getAttribute("aria-label")).toBe("25%");
  expect(f.style.width).toBe("80px");
  expect(f.style.height).toBe("80px");
  expect(container.textContent).toBe("25%");
});

test("a named ring leads its label with the name and shows the override", async () => {
  const { container } = render(
    <GaugeRing value={60} color="red" label="CPU" displayValue="6/10" />,
  );
  await settle();
  expect(frame(container).getAttribute("aria-label")).toBe("CPU: 6/10");
  expect(container.textContent).toContain("6/10");
  expect(container.textContent).toContain("CPU");
});

test("values clamp to 0..100", async () => {
  const over = render(<GaugeRing value={140} color="red" />);
  await settle();
  expect(frame(over.container).getAttribute("aria-label")).toBe("100%");
  over.unmount();
  const under = render(<GaugeRing value={-5} color="red" />);
  await settle();
  expect(frame(under.container).getAttribute("aria-label")).toBe("0%");
});

test("indeterminate shows no value and says it is not known", async () => {
  const { container } = render(
    <GaugeRing value={0} color="gray" secondaryColor="blue" indeterminate />,
  );
  await settle();
  expect(frame(container).getAttribute("aria-label")).toBe("value not known");
  expect(container.textContent).toBe("—");
  expect(container.querySelector("linearGradient")).toBeNull();
});

test("a secondary colour adds a gradient that the bar fills with", async () => {
  const { container } = render(
    <GaugeRing value={60} color="red" secondaryColor="blue" />,
  );
  await settle();
  const grad = container.querySelector("linearGradient") as Element;
  const id = grad.getAttribute("id") ?? "";
  expect(id).not.toBe("");
  const stops = [...grad.querySelectorAll("stop")];
  expect(stops[0].getAttribute("stop-color")).toBe("red");
  expect(stops[1].getAttribute("stop-color")).toBe("blue");
  expect(container.innerHTML).toContain(`url(#${id})`);
});
