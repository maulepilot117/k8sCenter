import { expect, test } from "bun:test";
import { renderToString } from "react-dom/server";
import { render, settle, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: Donut } = await import("./Donut.tsx");

/** Drawn sectors: Recharts gives every pie sector this class. */
function sectors(container: HTMLElement) {
  return container.querySelectorAll(".recharts-sector");
}

const PODS = [
  { value: 3, color: "var(--success)", label: "Running" },
  { value: 1, color: "var(--warning)", label: "Pending" },
  { value: 0, color: "var(--error)", label: "Failed" },
];

test("a zero segment draws no sector but is still named in the label", async () => {
  const { container, getByRole } = render(
    <Donut segments={PODS} center={<span>4 pods</span>} />,
  );
  await settle();
  expect(sectors(container)).toHaveLength(2);
  const img = getByRole("img");
  expect(img.getAttribute("aria-label")).toBe(
    "Running: 3, Pending: 1, Failed: 0",
  );
});

test("the centre content sits in the hole and the frame keeps its size", async () => {
  const { container, getByText } = render(
    <Donut
      size={100}
      thickness={10}
      segments={PODS}
      center={<span>4 pods</span>}
    />,
  );
  await settle();
  const frame = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.dataset.chart).toBe("donut");
  expect(frame.style.width).toBe("100px");
  expect(frame.style.height).toBe("100px");
  const hole = getByText("4 pods").parentElement as HTMLElement;
  expect(frame.contains(hole)).toBe(true);
  expect(hole.style.inset).toBe("10px");
});

test("a placeholder segment draws a ring and is named without a value", async () => {
  const { container, getByRole } = render(
    <Donut
      segments={[
        {
          value: 1,
          color: "var(--border-subtle)",
          label: "No pods",
          placeholder: true,
        },
      ]}
    />,
  );
  await settle();
  expect(sectors(container)).toHaveLength(1);
  expect(getByRole("img").getAttribute("aria-label")).toBe("No pods");
});

test("no segments keeps the default size and says there is no data", () => {
  const { container, getByRole } = render(<Donut segments={[]} />);
  const frame = container.querySelector("[data-chart-frame]") as HTMLElement;
  expect(frame.style.width).toBe("108px");
  expect(frame.style.height).toBe("108px");
  expect(getByRole("img").getAttribute("aria-label")).toBe("No data");
});

test("the server renders the sized box and centre content with no chart", () => {
  const html = renderToString(
    <Donut size={100} thickness={10} segments={PODS} center={<b>4 pods</b>} />,
  );
  expect(html).toContain("width:100px");
  expect(html).toContain("height:100px");
  expect(html).toContain("4 pods");
  expect(html).not.toContain("<svg");
});
