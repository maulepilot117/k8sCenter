import { describe, expect, test } from "bun:test";
import { coverage } from "./page-coverage.ts";
import type { ResourceListPage } from "./wire-types.ts";

/** `items` is `unknown` so a malformed payload is expressible. */
function page(items: unknown, total?: number): ResourceListPage {
  const n = Array.isArray(items) ? items.length : 0;
  return { items, total: total ?? n } as unknown as ResourceListPage;
}

describe("coverage: total and truncation", () => {
  test("a missing or non-finite total falls back to the counted page", () => {
    expect(coverage(page([1, 2]), 2).total).toBe(2);
    expect(coverage(page([1, 2], Number.NaN), 2).total).toBe(2);
    expect(coverage(page([1, 2], Number.NaN), 2).truncated).toBe(false);
  });

  test("a total above the count marks the page a sample", () => {
    const c = coverage(page([1, 2], 900), 2);
    expect(c.total).toBe(900);
    expect(c.counted).toBe(2);
    expect(c.truncated).toBe(true);
  });
});

// Derived here rather than passed in, so a card cannot be built without the
// signal being present on its view. Whether the card renders it is the card's
// problem; whether it exists is not.
describe("coverage: readable", () => {
  test("a list -- including an empty one -- is readable", () => {
    expect(coverage(page([]), 0).readable).toBe(true);
    expect(coverage(page([1, 2, 3]), 3).readable).toBe(true);
  });

  test("a null, absent or malformed payload is not readable", () => {
    expect(coverage(null, 0).readable).toBe(false);
    expect(coverage(undefined, 0).readable).toBe(false);
    expect(coverage(page(null), 0).readable).toBe(false);
    expect(coverage(page(undefined), 0).readable).toBe(false);
    expect(coverage(page("items"), 0).readable).toBe(false);
    expect(coverage(page(7), 0).readable).toBe(false);
    expect(coverage(page({}), 0).readable).toBe(false);
  });

  test("an unreadable page still reports zero counts, which is the trap", () => {
    // The counts are indistinguishable from a clean cluster's. That is why
    // `readable` exists and why a card must branch on it before its empty
    // state, rather than inferring emptiness from the numbers.
    const bad = coverage(page(null), 0);
    const clean = coverage(page([]), 0);
    expect(bad.counted).toBe(clean.counted);
    expect(bad.total).toBe(clean.total);
    expect(bad.truncated).toBe(clean.truncated);
    expect(bad.readable).not.toBe(clean.readable);
  });
});
