import { describe, expect, test } from "bun:test";
import { statusFilterFromQuery } from "@/src/islands/ResourceTable.tsx";

const POD_CHIPS = ["All", "Running", "Pending", "Failed"];

describe("statusFilterFromQuery", () => {
  test("opens on the chip the dashboard's Failed pods link names", () => {
    expect(statusFilterFromQuery("?status=failed", POD_CHIPS)).toBe("failed");
  });

  test("matches the chip case-insensitively", () => {
    expect(statusFilterFromQuery("?status=Pending", POD_CHIPS)).toBe("pending");
  });

  test("ignores a status this table has no chip for", () => {
    // Progressing is a Deployment chip, not a pod one.
    expect(statusFilterFromQuery("?status=progressing", POD_CHIPS)).toBeNull();
  });

  test("leaves the filter alone without a status parameter", () => {
    expect(statusFilterFromQuery("", POD_CHIPS)).toBeNull();
    expect(statusFilterFromQuery("?status=", POD_CHIPS)).toBeNull();
    expect(statusFilterFromQuery("?action=create", POD_CHIPS)).toBeNull();
  });
});
