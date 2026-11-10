import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: BarRow } = await import("./BarRow.tsx");

test("exposes a named meter with its value, range and text", () => {
  const { getByRole } = render(
    <BarRow label="CPU" value={42} max={80} suffix="4.2 cores" />,
  );
  const meter = getByRole("meter", { name: "CPU" });
  expect(meter.getAttribute("aria-valuenow")).toBe("42");
  expect(meter.getAttribute("aria-valuemin")).toBe("0");
  expect(meter.getAttribute("aria-valuemax")).toBe("80");
  expect(meter.getAttribute("aria-valuetext")).toBe("4.2 cores");
});

test("a value over the maximum reports the real value while the bar stays full", () => {
  const { getByRole } = render(<BarRow label="Memory" value={150} max={100} />);
  const meter = getByRole("meter");
  expect(meter.getAttribute("aria-valuenow")).toBe("150");
  expect(meter.getAttribute("aria-valuetext")).toBeNull();
  expect(
    (meter.querySelector("[data-bar-fill]") as HTMLElement).style.width,
  ).toBe("100%");
});
