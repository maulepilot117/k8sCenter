import { expect, test } from "bun:test";
import {
  act,
  fireEvent,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { ResourceAreaChart } = await import("./ResourceAreaChart.tsx");

// 14:00 local, so tick and tooltip labels do not depend on the machine's zone.
const END = new Date(2026, 10, 10, 14, 0).getTime();

const series = (n: number, base: number) =>
  Array.from({ length: n }, (_, i) => base + i);

async function openTooltip(container: HTMLElement) {
  const chart = container.querySelector('[tabindex="0"]') as Element;
  expect(chart).not.toBeNull();
  await act(async () => {
    fireEvent.focus(chart);
  });
  return {
    chart,
    tip: container.querySelector('[role="status"]') as HTMLElement,
  };
}

test("shows the placeholder when neither series has a sample", () => {
  const { container } = render(
    <ResourceAreaChart cpuData={null} memData={[]} end={END} />,
  );
  expect(container.textContent).toContain(
    "Trend data unavailable (Prometheus not connected)",
  );
  expect(container.querySelector("svg")).toBeNull();
});

test("a single sample is drawn rather than treated as no data", async () => {
  const { container } = render(
    <ResourceAreaChart cpuData={[42]} memData={null} end={END} />,
  );
  await settle();
  expect(container.textContent).not.toContain("Trend data unavailable");
  const { tip } = await openTooltip(container);
  expect(tip.textContent).toContain("14:00");
  expect(tip.textContent).toContain("42%");
});

test("is a labelled figure that says what it shows", async () => {
  const { getByRole } = render(
    <ResourceAreaChart
      cpuData={series(30, 10)}
      memData={series(30, 30)}
      window="1h0m0s"
      step="2m0s"
      end={END}
    />,
  );
  await settle();
  const figure = getByRole("figure");
  expect(figure.getAttribute("aria-label")).toBe(
    "CPU and memory utilization over the last hour",
  );
});

test("a one hour window of 30 points on a 2m step runs from 13:02 to now", async () => {
  const { container } = render(
    <ResourceAreaChart
      cpuData={series(30, 10)}
      memData={series(30, 30)}
      window="1h0m0s"
      step="2m0s"
      end={END}
    />,
  );
  await settle();
  // The axis starts an hour before now: its first tick is 13:00.
  const labels = [...container.querySelectorAll("text")].map(
    (t) => t.textContent,
  );
  expect(labels).toContain("13:00");

  // Oldest sample sits 58 minutes before now, with both series' values.
  const { chart, tip } = await openTooltip(container);
  expect(tip.textContent).toContain("13:02");
  expect(tip.textContent).toContain("CPU");
  expect(tip.textContent).toContain("10%");
  expect(tip.textContent).toContain("Memory");
  expect(tip.textContent).toContain("30%");

  // Stepping once moves two minutes on.
  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("13:04");
  expect(tip.textContent).toContain("11%");
  expect(tip.textContent).toContain("31%");
});

test("the newest sample lands on now", async () => {
  const { container } = render(
    <ResourceAreaChart
      cpuData={[10, 20, 77]}
      memData={[30, 40, 66]}
      window="15m0s"
      step="30s"
      end={END}
    />,
  );
  await settle();
  const { chart, tip } = await openTooltip(container);
  expect(tip.textContent).toContain("13:59");
  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("14:00");
  expect(tip.textContent).toContain("77%");
  expect(tip.textContent).toContain("66%");
});

test("a null in the middle of a series keeps the samples before it on their steps", async () => {
  // Four steps on the 2m grid: 13:54, 13:56, 13:58, 14:00. CPU has a gap at
  // 13:56 and memory one at 13:58. Compacting the gaps out would put CPU's 10
  // at 13:56 and memory's 50 at 13:56 too; kept in place, each sample sits on
  // its own step and the two series line up.
  const { container } = render(
    <ResourceAreaChart
      cpuData={[10, null, 30, 40]}
      memData={[50, 60, null, 80]}
      window="1h0m0s"
      step="2m0s"
      end={END}
    />,
  );
  await settle();
  const { chart, tip } = await openTooltip(container);
  expect(tip.textContent).toContain("13:54");
  expect(tip.textContent).toContain("10%");
  expect(tip.textContent).toContain("50%");

  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("13:56");
  expect(tip.textContent).toContain("60%");
  expect(tip.textContent).not.toContain("10%");

  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("13:58");
  expect(tip.textContent).toContain("30%");
  expect(tip.textContent).not.toContain("60%");

  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("14:00");
  expect(tip.textContent).toContain("40%");
  expect(tip.textContent).toContain("80%");
});

test("a gap is drawn over the same way whether one series or both lost the step", async () => {
  // Step 1 is a gap in CPU only, so its row still carries memory. Step 3 is a
  // gap in both, so it has no row at all. Both must look alike: each area is
  // one unbroken line over its finite samples, as the sparklines draw them.
  const { container } = render(
    <ResourceAreaChart
      cpuData={[10, null, 30, null, 50]}
      memData={[60, 70, 80, null, 100]}
      window="1h0m0s"
      step="2m0s"
      end={END}
    />,
  );
  await settle();
  const curves = [...container.querySelectorAll(".recharts-area-curve")];
  expect(curves).toHaveLength(2);
  for (const curve of curves) {
    const d = curve.getAttribute("d") ?? "";
    // Recharts starts a new subpath with M at every break in a line.
    expect(d.match(/M/g)).toHaveLength(1);
  }
});

test("a missing memory series draws CPU alone", async () => {
  const { container } = render(
    <ResourceAreaChart
      cpuData={series(5, 20)}
      memData={null}
      window="1h0m0s"
      step="2m0s"
      end={END}
    />,
  );
  await settle();
  const { tip } = await openTooltip(container);
  expect(tip.textContent).toContain("CPU");
  expect(tip.textContent).toContain("20%");
  expect(tip.textContent).not.toContain("Memory");
});

test("without window and step it uses the one hour, two minute grid", async () => {
  const { container } = render(
    <ResourceAreaChart
      cpuData={series(30, 10)}
      memData={series(30, 30)}
      end={END}
    />,
  );
  await settle();
  const { tip } = await openTooltip(container);
  expect(tip.textContent).toContain("13:02");
});

test("a window given as 24h on a 48m step spans the day in 48-minute steps", async () => {
  const { container, getByRole } = render(
    <ResourceAreaChart
      cpuData={series(31, 10)}
      memData={null}
      window="24h0m0s"
      step="48m0s"
      end={END}
    />,
  );
  await settle();
  expect(getByRole("figure").getAttribute("aria-label")).toBe(
    "CPU and memory utilization over the last 24 hours",
  );
  // The oldest of 31 samples is a day before the newest, and the next is
  // one 48-minute step after it.
  const { chart, tip } = await openTooltip(container);
  expect(tip.textContent).toContain("14:00");
  expect(tip.textContent).toContain("10%");
  await act(async () => {
    fireEvent.keyDown(chart, { key: "ArrowRight" });
  });
  expect(tip.textContent).toContain("14:48");
  expect(tip.textContent).toContain("11%");
});
