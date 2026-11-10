import { describe, expect, test } from "bun:test";
import {
  formatValue,
  maxTickCount,
  timeTickFormatter,
  timeTicks,
  valueDomain,
} from "./chart-theme.ts";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

describe("maxTickCount", () => {
  test("caps ticks by the width available", () => {
    expect(maxTickCount(375)).toBe(4);
    expect(maxTickCount(800)).toBe(10);
  });

  test("never drops below the two ends of the axis", () => {
    expect(maxTickCount(0)).toBe(2);
    expect(maxTickCount(90)).toBe(2);
  });
});

describe("timeTicks", () => {
  const end = new Date(2026, 10, 10, 14, 37, 12).getTime();

  test("a 1h range ticks on whole minutes inside the range, within the cap", () => {
    const ticks = timeTicks(end - HOUR, end, 4);
    expect(ticks.length).toBeGreaterThanOrEqual(2);
    expect(ticks.length).toBeLessThanOrEqual(4);
    for (const t of ticks) {
      expect(t).toBeGreaterThanOrEqual(end - HOUR);
      expect(t).toBeLessThanOrEqual(end);
      expect(new Date(t).getSeconds()).toBe(0);
      expect(new Date(t).getMinutes() % 5).toBe(0);
    }
  });

  test("a 24h range ticks on whole hours", () => {
    const ticks = timeTicks(end - 24 * HOUR, end, 4);
    expect(ticks.length).toBeLessThanOrEqual(4);
    for (const t of ticks) expect(new Date(t).getMinutes()).toBe(0);
  });

  test("ticks are evenly spaced and ascending", () => {
    const ticks = timeTicks(end - 6 * HOUR, end, 8);
    const gaps = ticks.slice(1).map((t, i) => t - ticks[i]);
    expect(new Set(gaps).size).toBe(1);
    expect(gaps[0]).toBeGreaterThan(0);
  });

  test("an empty range has no ticks", () => {
    expect(timeTicks(end, end, 4)).toEqual([]);
  });
});

describe("timeTickFormatter", () => {
  const at = new Date(2026, 10, 10, 14, 35).getTime();

  test("a 1h range shows hours and minutes", () => {
    expect(timeTickFormatter(HOUR)(at)).toBe("14:35");
  });

  test("a 24h range shows the hour", () => {
    expect(
      timeTickFormatter(24 * HOUR)(new Date(2026, 10, 10, 9).getTime()),
    ).toBe("09:00");
  });

  test("a range longer than a day shows the date", () => {
    expect(timeTickFormatter(7 * 24 * HOUR)(at)).toBe("Nov 10");
  });
});

describe("formatValue", () => {
  test("renders cores, binary bytes and percent", () => {
    expect(formatValue(0.5, "cores")).toBe("0.5 cores");
    expect(formatValue(1.5 * 1024 ** 3, "bytes")).toBe("1.5 GiB");
    expect(formatValue(42, "percent")).toBe("42%");
  });

  test("small byte counts and fractional percents stay readable", () => {
    expect(formatValue(512, "bytes")).toBe("512 B");
    expect(formatValue(2048, "bytes")).toBe("2 KiB");
    expect(formatValue(42.25, "percent")).toBe("42.3%");
    expect(formatValue(1, "cores")).toBe("1 core");
  });
});

describe("valueDomain", () => {
  test("a constant series gets a padded domain instead of a zero-height one", () => {
    const [lo, hi] = valueDomain([5, 5, 5]) as [number, number];
    expect(lo).toBeLessThan(5);
    expect(hi).toBeGreaterThan(5);
  });

  test("a constant zero series stays non-negative", () => {
    expect(valueDomain([0, 0])).toEqual([0, 1]);
  });

  test("a varying series is left to the chart's own scale", () => {
    expect(valueDomain([1, 3, 2])).toEqual(["auto", "auto"]);
  });

  test("non-finite samples are ignored", () => {
    const [lo, hi] = valueDomain([4, Number.NaN, 4]) as [number, number];
    expect(lo).toBeLessThan(4);
    expect(hi).toBeGreaterThan(4);
  });
});
