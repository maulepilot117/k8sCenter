import { expect, test } from "bun:test";
import { formatMbps, percentile } from "./format.ts";

// --- percentile ---

test("percentile: empty series returns 0", () => {
  expect(percentile([], 95)).toBe(0);
});

test("percentile: null/undefined series returns 0", () => {
  expect(percentile(null, 95)).toBe(0);
  expect(percentile(undefined, 95)).toBe(0);
});

test("percentile: all-non-finite series returns 0", () => {
  expect(percentile([NaN, Infinity, -Infinity], 95)).toBe(0);
});

test("percentile: single-element series returns that element", () => {
  expect(percentile([42], 95)).toBe(42);
});

test("percentile: drops non-finite samples before computing", () => {
  // Effective series is [10, 20] -> p95 interpolates near the top.
  expect(percentile([10, NaN, 20], 100)).toBe(20);
});

test("percentile: exact-rank case (lo === hi)", () => {
  // p0 of a sorted series is the minimum, an exact index.
  expect(percentile([5, 1, 3, 2, 4], 0)).toBe(1);
  expect(percentile([5, 1, 3, 2, 4], 100)).toBe(5);
});

test("percentile: interpolates between bracketing samples", () => {
  // [1,2,3,4,5], p95 -> rank = 0.95*4 = 3.8 -> 4 + (5-4)*0.8 = 4.8
  expect(percentile([1, 2, 3, 4, 5], 95)).toBe(4.8);
});

test("percentile: all-equal series returns that value", () => {
  expect(percentile([7, 7, 7, 7], 95)).toBe(7);
});

test("percentile: handles negative values", () => {
  expect(percentile([-3, -1, -2], 0)).toBe(-3);
});

// --- formatMbps ---

test("formatMbps: non-finite renders em-dash", () => {
  expect(formatMbps(NaN)).toBe("—");
  expect(formatMbps(Infinity)).toBe("—");
});

test("formatMbps: >= 100 rounds to integer", () => {
  expect(formatMbps(100)).toBe("100");
  expect(formatMbps(150.7)).toBe("151");
});

test("formatMbps: < 100 keeps one decimal", () => {
  expect(formatMbps(1.23)).toBe("1.2");
  expect(formatMbps(99.95)).toBe("100");
  expect(formatMbps(0)).toBe("0");
});
