import type { DonutSegment } from "@/components/charts/Donut.tsx";

/**
 * Donut segments for the pod-status phase breakdown.
 *
 * The guard is the sum of the three plotted counts, not pods.total. Donut
 * divides by the summed segment values, so an all-zero set collapses every
 * conic-gradient stop to `0% 0%` and the last color floods the whole ring --
 * an empty cluster drew a solid red donut until that was fixed. Summing the
 * plotted values also covers a cluster whose pods are all in phases this donut
 * does not plot (Succeeded, Unknown), where pods.total is non-zero but every
 * segment is 0.
 *
 * Donut renders value and color only, so no label is passed; segment order is
 * the order the legend renders beside it.
 *
 * This lives in lib/ rather than inline in the widget because the guard has
 * already regressed into a user-visible bug once, and a pure function is the
 * only shape this repo can unit-test (it has no component test harness).
 */
export function podStatusSegments(
  running: number,
  pending: number,
  failed: number,
): DonutSegment[] {
  if (running + pending + failed > 0) {
    return [
      { value: running, color: "var(--success)" },
      { value: pending, color: "var(--warning)" },
      { value: failed, color: "var(--error)" },
    ];
  }
  return [{ value: 1, color: "var(--border-subtle)" }];
}
