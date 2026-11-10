import { expect, test } from "bun:test";
import { renderToString } from "react-dom/server";
import { render, settle, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: Gauge } = await import("./Gauge.tsx");

test("a gauge is one image whose label states the value and the caption", async () => {
  const { container } = render(<Gauge value={73} sublabel="Healthy" />);
  await settle();
  const f = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(f.dataset.chart).toBe("gauge");
  expect(f.getAttribute("role")).toBe("img");
  const label = f.getAttribute("aria-label") ?? "";
  expect(label).toContain("73%");
  expect(label).toContain("Healthy");
});

test("a name leads the label and the centre text shows the label", async () => {
  const { container } = render(
    <Gauge value={92} name="Cluster health" label="92" sublabel="Healthy" />,
  );
  await settle();
  const f = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(f.getAttribute("aria-label")).toBe("Cluster health: 92, Healthy");
  expect(container.textContent).toBe("92Healthy");
});

test("values clamp to 0..100", async () => {
  const { container } = render(<Gauge value={250} />);
  await settle();
  const f = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(f.getAttribute("aria-label")).toBe("100%");
});

test("the server render holds the box and the centre text, with no svg", () => {
  const html = renderToString(
    <Gauge value={73} size={140} label="73%" sublabel="Healthy" />,
  );
  expect(html).toContain('data-chart="gauge"');
  expect(html).toContain("width:140px");
  expect(html).toContain("height:140px");
  expect(html).toContain("73%");
  expect(html).toContain("Healthy");
  expect(html).not.toContain("<svg");
});
