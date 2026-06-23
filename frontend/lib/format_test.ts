import { assertEquals } from "jsr:@std/assert@1";
import { formatMbps, percentile } from "./format.ts";

// --- percentile ---

Deno.test("percentile: empty series returns 0", () => {
  assertEquals(percentile([], 95), 0);
});

Deno.test("percentile: null/undefined series returns 0", () => {
  assertEquals(percentile(null, 95), 0);
  assertEquals(percentile(undefined, 95), 0);
});

Deno.test("percentile: all-non-finite series returns 0", () => {
  assertEquals(percentile([NaN, Infinity, -Infinity], 95), 0);
});

Deno.test("percentile: single-element series returns that element", () => {
  assertEquals(percentile([42], 95), 42);
});

Deno.test("percentile: drops non-finite samples before computing", () => {
  // Effective series is [10, 20] -> p95 interpolates near the top.
  assertEquals(percentile([10, NaN, 20], 100), 20);
});

Deno.test("percentile: exact-rank case (lo === hi)", () => {
  // p0 of a sorted series is the minimum, an exact index.
  assertEquals(percentile([5, 1, 3, 2, 4], 0), 1);
  assertEquals(percentile([5, 1, 3, 2, 4], 100), 5);
});

Deno.test("percentile: interpolates between bracketing samples", () => {
  // [1,2,3,4,5], p95 -> rank = 0.95*4 = 3.8 -> 4 + (5-4)*0.8 = 4.8
  assertEquals(percentile([1, 2, 3, 4, 5], 95), 4.8);
});

Deno.test("percentile: all-equal series returns that value", () => {
  assertEquals(percentile([7, 7, 7, 7], 95), 7);
});

Deno.test("percentile: handles negative values", () => {
  assertEquals(percentile([-3, -1, -2], 0), -3);
});

// --- formatMbps ---

Deno.test("formatMbps: non-finite renders em-dash", () => {
  assertEquals(formatMbps(NaN), "—");
  assertEquals(formatMbps(Infinity), "—");
});

Deno.test("formatMbps: >= 100 rounds to integer", () => {
  assertEquals(formatMbps(100), "100");
  assertEquals(formatMbps(150.7), "151");
});

Deno.test("formatMbps: < 100 keeps one decimal", () => {
  assertEquals(formatMbps(1.23), "1.2");
  assertEquals(formatMbps(99.95), "100");
  assertEquals(formatMbps(0), "0");
});
