import { describe, expect, test } from "bun:test";
import {
  goldenSignalsReasonCopy,
  METRICS_NOT_CONFIGURED_MESSAGE,
  METRICS_NOT_CONFIGURED_REASON,
} from "@/lib/metrics-reasons.ts";

describe("goldenSignalsReasonCopy", () => {
  test("each known reason has plain copy that never prints the code", () => {
    for (const reason of [
      METRICS_NOT_CONFIGURED_REASON,
      "unreachable",
      "metrics_unavailable",
    ]) {
      const copy = goldenSignalsReasonCopy(reason);
      expect(copy).not.toBeNull();
      expect(copy).not.toContain(reason);
    }
  });

  test("no binding reads as the shared not-configured message", () => {
    expect(goldenSignalsReasonCopy(METRICS_NOT_CONFIGURED_REASON)).toBe(
      METRICS_NOT_CONFIGURED_MESSAGE,
    );
  });

  test("an unknown reason is left to the caller", () => {
    expect(goldenSignalsReasonCopy("something_new")).toBeNull();
  });
});
