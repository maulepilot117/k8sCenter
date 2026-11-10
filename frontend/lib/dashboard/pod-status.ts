import type { DonutSegment } from "@/components/charts/Donut.tsx";

/**
 * Donut segments for the pod-status phase breakdown.
 *
 * The guard is the sum of the three plotted counts, not pods.total. An
 * all-zero set leaves the donut nothing to draw, so an empty cluster gets one
 * neutral placeholder segment instead of a bare or error-coloured ring.
 * Summing the plotted values also covers a cluster whose pods are all in phases this donut
 * does not plot (Succeeded, Unknown), where pods.total is non-zero but every
 * segment is 0.
 *
 * Each segment carries its phase name so the donut's accessible label can
 * name it; segment order is the order the legend renders beside it. The
 * placeholder is named without a count.
 *
 * This lives in lib/ rather than inline in the widget because the guard has
 * already regressed into a user-visible bug once.
 */
export function podStatusSegments(
  running: number,
  pending: number,
  failed: number,
): DonutSegment[] {
  if (running + pending + failed > 0) {
    return [
      { value: running, color: "var(--success)", label: "Running" },
      { value: pending, color: "var(--warning)", label: "Pending" },
      { value: failed, color: "var(--error)", label: "Failed" },
    ];
  }
  return [
    {
      value: 1,
      color: "var(--border-subtle)",
      label: "No pods",
      placeholder: true,
    },
  ];
}
