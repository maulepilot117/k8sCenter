import { expect, test } from "bun:test";
import { outcomeTone } from "./velero-utils.ts";

test("outcomeTone: each backend backup outcome has its tone", () => {
  expect(outcomeTone("succeeded")).toBe("success");
  expect(outcomeTone("failed")).toBe("error");
  expect(outcomeTone("inProgress")).toBe("info");
  expect(outcomeTone("unknown")).toBe("neutral");
});

test("outcomeTone: an absent outcome is neutral", () => {
  expect(outcomeTone(undefined)).toBe("neutral");
});
