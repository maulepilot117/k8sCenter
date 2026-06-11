import { assertEquals } from "jsr:@std/assert@1";
import { healthStatusColor, scoreColor } from "./score-color.ts";

// --- scoreColor ---

Deno.test("scoreColor: 90 returns success", () => {
  assertEquals(scoreColor(90), "var(--success)");
});

Deno.test("scoreColor: 100 returns success", () => {
  assertEquals(scoreColor(100), "var(--success)");
});

Deno.test("scoreColor: 90 with alerts category returns accent", () => {
  assertEquals(scoreColor(90, "alerts"), "var(--accent)");
});

Deno.test("scoreColor: 100 with alerts category returns accent", () => {
  assertEquals(scoreColor(100, "alerts"), "var(--accent)");
});

Deno.test("scoreColor: 89 returns warning", () => {
  assertEquals(scoreColor(89), "var(--warning)");
});

Deno.test("scoreColor: 70 returns warning", () => {
  assertEquals(scoreColor(70), "var(--warning)");
});

Deno.test("scoreColor: 69 returns error", () => {
  assertEquals(scoreColor(69), "var(--error)");
});

Deno.test("scoreColor: 0 returns error", () => {
  assertEquals(scoreColor(0), "var(--error)");
});

// --- healthStatusColor ---

Deno.test("healthStatusColor: healthy returns success", () => {
  assertEquals(healthStatusColor("healthy"), "var(--success)");
});

Deno.test("healthStatusColor: degraded returns warning", () => {
  assertEquals(healthStatusColor("degraded"), "var(--warning)");
});

Deno.test("healthStatusColor: critical returns error", () => {
  assertEquals(healthStatusColor("critical"), "var(--error)");
});

Deno.test("healthStatusColor: unknown returns muted", () => {
  assertEquals(healthStatusColor("unknown"), "var(--text-muted)");
});

Deno.test("healthStatusColor: unexpected value returns muted", () => {
  assertEquals(healthStatusColor("something-else"), "var(--text-muted)");
});
