import { expect, test } from "bun:test";
import { connectionStatusColor } from "./status-colors.ts";

test("each cluster connection status maps to a theme status colour", () => {
  expect(connectionStatusColor("connected")).toBe("var(--success)");
  expect(connectionStatusColor("Degraded")).toBe("var(--warning)");
  expect(connectionStatusColor("disconnected")).toBe("var(--error)");
  expect(connectionStatusColor("error")).toBe("var(--error)");
  expect(connectionStatusColor(undefined)).toBe("var(--text-muted)");
});
