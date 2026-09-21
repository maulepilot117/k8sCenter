import { expect, test } from "bun:test";
import { durationShort, formatMbps, lastDelta, percentile } from "./format.ts";

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

// --- lastDelta ---

test("lastDelta: null, undefined or a too-short series has no delta", () => {
  expect(lastDelta(null)).toBeNull();
  expect(lastDelta(undefined)).toBeNull();
  expect(lastDelta([])).toBeNull();
  expect(lastDelta([5])).toBeNull();
});

test("lastDelta: is a percent change, not a difference", () => {
  // MetricTile renders this number followed by a literal "%", so a pod count
  // of 10 rising to 13 must read 30, not 3.
  expect(lastDelta([10, 13])).toBe(30);
  expect(lastDelta([40, 44])).toBe(10);
  expect(lastDelta([100, 103])).toBe(3);
});

test("lastDelta: uses only the final two samples", () => {
  expect(lastDelta([999, 1, 2])).toBe(100);
});

test("lastDelta: a fall is negative", () => {
  expect(lastDelta([10, 5])).toBe(-50);
});

test("lastDelta: rounds to a whole percent", () => {
  expect(lastDelta([3, 4])).toBe(33);
});

test("lastDelta: a previous sample of zero has no delta", () => {
  // Not Infinity, and not 100: there is no meaningful percent change from
  // nothing, so the tile renders no delta rather than a misleading one.
  expect(lastDelta([0, 5])).toBeNull();
  expect(lastDelta([0, 0])).toBeNull();
});

test("lastDelta: a non-finite endpoint has no delta", () => {
  expect(lastDelta([1, Number.NaN])).toBeNull();
  expect(lastDelta([Number.NaN, 1])).toBeNull();
  expect(lastDelta([1, Number.POSITIVE_INFINITY])).toBeNull();
});

// --- durationShort ---
//
// `age` above takes a timestamp and reads the clock itself. The pod widgets
// hold an already-computed millisecond age (injected clock, so the ranking is
// testable), so they need the other half on its own.

test("durationShort: an unmeasurable age renders an em-dash", () => {
  // Null means the creationTimestamp was missing or unparseable. A pod whose
  // age nobody can read must not render as "0s", which reads as brand new and
  // is the opposite of what an unreadable field tells us.
  expect(durationShort(null)).toBe("—");
  expect(durationShort(Number.NaN)).toBe("—");
});

test("durationShort: a negative age clamps to zero rather than counting up", () => {
  // Clock skew between the API server and the browser. "-3s" is noise.
  expect(durationShort(-3000)).toBe("0s");
});

test("durationShort: steps through seconds, minutes, hours and days", () => {
  expect(durationShort(5_000)).toBe("5s");
  expect(durationShort(59_000)).toBe("59s");
  expect(durationShort(60_000)).toBe("1m");
  expect(durationShort(90 * 60_000)).toBe("1h");
  expect(durationShort(36 * 3_600_000)).toBe("1d");
});
