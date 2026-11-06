import { expect, test } from "bun:test";
import { summarizeSeries } from "./PromQLQuery.tsx";

test("a range series reduces to its latest value, range and sample count", () => {
  expect(
    summarizeSeries([
      [1, "3"],
      [2, "1.5"],
      [3, "7"],
      [4, "2"],
    ]),
  ).toEqual({
    samples: 4,
    numbers: [3, 1.5, 7, 2],
    latest: 2,
    min: 1.5,
    max: 7,
  });
});

test("NaN and infinite samples count as samples but not as numbers", () => {
  const s = summarizeSeries([
    [1, "NaN"],
    [2, "4"],
    [3, "+Inf"],
  ]);
  expect(s).toEqual({ samples: 3, numbers: [4], latest: 4, min: 4, max: 4 });
});

test("a series with no finite samples has no latest value", () => {
  expect(summarizeSeries([[1, "NaN"]])).toEqual({
    samples: 1,
    numbers: [],
    latest: null,
    min: null,
    max: null,
  });
});
