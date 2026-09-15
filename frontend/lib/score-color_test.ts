import { expect, test } from "bun:test";
import { healthStatusColor, scoreColor } from "./score-color.ts";

// --- scoreColor ---

test("scoreColor: 90 returns success", () => {
  expect(scoreColor(90)).toBe("var(--success)");
});

test("scoreColor: 100 returns success", () => {
  expect(scoreColor(100)).toBe("var(--success)");
});

test("scoreColor: 90 with alerts category returns accent", () => {
  expect(scoreColor(90, "alerts")).toBe("var(--accent)");
});

test("scoreColor: 100 with alerts category returns accent", () => {
  expect(scoreColor(100, "alerts")).toBe("var(--accent)");
});

test("scoreColor: 89 returns warning", () => {
  expect(scoreColor(89)).toBe("var(--warning)");
});

test("scoreColor: 70 returns warning", () => {
  expect(scoreColor(70)).toBe("var(--warning)");
});

test("scoreColor: 69 returns error", () => {
  expect(scoreColor(69)).toBe("var(--error)");
});

test("scoreColor: 0 returns error", () => {
  expect(scoreColor(0)).toBe("var(--error)");
});

// --- healthStatusColor ---

test("healthStatusColor: healthy returns success", () => {
  expect(healthStatusColor("healthy")).toBe("var(--success)");
});

test("healthStatusColor: degraded returns warning", () => {
  expect(healthStatusColor("degraded")).toBe("var(--warning)");
});

test("healthStatusColor: critical returns error", () => {
  expect(healthStatusColor("critical")).toBe("var(--error)");
});

test("healthStatusColor: unknown returns muted", () => {
  expect(healthStatusColor("unknown")).toBe("var(--text-muted)");
});

test("healthStatusColor: unexpected value returns muted", () => {
  expect(healthStatusColor("something-else")).toBe("var(--text-muted)");
});
