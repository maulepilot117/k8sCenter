import { expect, test } from "bun:test";
import { render, settle, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: RadialGauge } = await import("./RadialGauge.tsx");

/** Radii of every arc in an SVG path's `d`, e.g. "A 49,49,0,..." -> 49. */
function arcRadii(d: string): number[] {
  return [...d.matchAll(/A\s*([\d.]+),/g)].map((m) => Number(m[1]));
}

// Recharts exposes no other handle on a ring's geometry, so this one test reads
// the radii of the track it draws.
test("the ring fills the requested thickness, out to the frame's edge", async () => {
  const { container } = render(
    <RadialGauge
      percent={40}
      size={100}
      thickness={12}
      color="var(--success)"
      label="Health: 40%"
    />,
  );
  await settle();
  const track = container.querySelector(
    ".recharts-radial-bar-background-sector",
  );
  const radii = arcRadii(track?.getAttribute("d") ?? "");
  expect(Math.max(...radii)).toBeCloseTo(49, 0);
  expect(Math.min(...radii)).toBeCloseTo(37, 0);
});
