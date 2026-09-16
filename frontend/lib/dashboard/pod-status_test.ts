import { expect, test } from "bun:test";
import { podStatusSegments } from "./pod-status.ts";

// This guard shipped as a user-visible bug once: an all-zero segment set makes
// Donut collapse every conic-gradient stop to `0% 0%`, and the last colour then
// floods the whole ring, so an empty cluster drew a solid red donut. These
// tests exist so that cannot come back silently.

test("podStatusSegments: real counts produce the three plotted phases in order", () => {
  expect(podStatusSegments(5, 2, 1)).toEqual([
    { value: 5, color: "var(--success)" },
    { value: 2, color: "var(--warning)" },
    { value: 1, color: "var(--error)" },
  ]);
});

test("podStatusSegments: all three zero yields the neutral placeholder", () => {
  // One grey segment, not three zero-value ones. Three zeroes is exactly the
  // input that produced the solid red ring.
  expect(podStatusSegments(0, 0, 0)).toEqual([
    { value: 1, color: "var(--border-subtle)" },
  ]);
});

test("podStatusSegments: a single non-zero phase still plots all three", () => {
  // The donut is a phase breakdown, so a cluster with only running pods still
  // renders the pending and failed keys at zero rather than dropping them.
  expect(podStatusSegments(7, 0, 0)).toEqual([
    { value: 7, color: "var(--success)" },
    { value: 0, color: "var(--warning)" },
    { value: 0, color: "var(--error)" },
  ]);
  expect(podStatusSegments(0, 0, 3)).toEqual([
    { value: 0, color: "var(--success)" },
    { value: 0, color: "var(--warning)" },
    { value: 3, color: "var(--error)" },
  ]);
});

test("podStatusSegments: the guard sums the plotted phases, not the total", () => {
  // A cluster whose pods are all Succeeded or Unknown has a non-zero
  // pods.total but zero in every phase this donut plots. Guarding on the total
  // would send it down the real-segments branch and back into the red ring.
  expect(podStatusSegments(0, 0, 0)).toHaveLength(1);
});

test("podStatusSegments: never returns an empty set", () => {
  // Donut maps segments into gradient stops; an empty array would render no
  // ring at all rather than a placeholder.
  const cases: Array<[number, number, number]> = [
    [0, 0, 0],
    [1, 0, 0],
    [0, 1, 0],
    [0, 0, 1],
    [3, 4, 5],
  ];
  const offenders = cases.filter(
    ([r, p, f]) => podStatusSegments(r, p, f).length === 0,
  );
  expect(offenders).toEqual([]);
});
