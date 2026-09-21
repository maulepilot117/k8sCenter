import { describe, expect, test } from "bun:test";
import { list, listOr, num, numOr, obj, str } from "./narrow.ts";

describe("obj", () => {
  test("an object passes through; null is not an object", () => {
    const o = { a: 1 };
    expect(obj(o)).toBe(o);
    expect(obj(null)).toBeNull();
    expect(obj(undefined)).toBeNull();
    expect(obj("x")).toBeNull();
    expect(obj(7)).toBeNull();
  });

  test("an array is an object, which is what callers index into", () => {
    expect(obj([1, 2])).not.toBeNull();
  });
});

describe("str", () => {
  test("a string passes through; anything else is empty", () => {
    expect(str("a")).toBe("a");
    expect(str("")).toBe("");
    expect(str(null)).toBe("");
    expect(str(7)).toBe("");
    expect(str({})).toBe("");
  });
});

describe("num", () => {
  test("a finite number passes through; non-finite is null", () => {
    expect(num(0)).toBe(0);
    expect(num(-1.5)).toBe(-1.5);
    expect(num(Number.NaN)).toBeNull();
    expect(num(Number.POSITIVE_INFINITY)).toBeNull();
    expect(num("7")).toBeNull();
    expect(num(null)).toBeNull();
  });
});

describe("numOr", () => {
  test("falls back only where the fallback is the contract", () => {
    expect(numOr(3, 1)).toBe(3);
    expect(numOr(0, 1)).toBe(0);
    expect(numOr(Number.NaN, 1)).toBe(1);
    expect(numOr(undefined, 1)).toBe(1);
  });
});

// The distinction these two encode is the one this release exists to
// protect: an empty list is an answer, and a non-list is the absence of one.
// They are tested together because the bug was a module reaching for the
// wrong one, not either behaving incorrectly on its own.
describe("list vs listOr", () => {
  test("an array is returned by both", () => {
    const a = [1, 2];
    expect(list(a)).toBe(a);
    expect(listOr(a)).toBe(a);
  });

  test("an empty array is a real answer to both", () => {
    expect(list([])).toEqual([]);
    expect(listOr([])).toEqual([]);
  });

  test("a non-list is null to list and empty to listOr", () => {
    for (const v of [null, undefined, "items", 7, {}]) {
      expect(list(v)).toBeNull();
      expect(listOr(v)).toEqual([]);
    }
  });
});
