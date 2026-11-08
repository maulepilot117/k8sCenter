import { expect, test } from "bun:test";
import { withUniqueKeys } from "./unique-keys.ts";

test("identical rows get distinct keys and keep their order", () => {
  const rows = ["a", "b", "a", "a"];
  expect(withUniqueKeys(rows, (r) => r)).toEqual([
    { item: "a", key: "a#0" },
    { item: "b", key: "b#0" },
    { item: "a", key: "a#1" },
    { item: "a", key: "a#2" },
  ]);
});
