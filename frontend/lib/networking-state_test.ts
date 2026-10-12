import { expect, test } from "bun:test";
import { ApiError } from "@/lib/api.ts";
import { failureOf, panelState } from "@/lib/networking-state.ts";

function apiError(status: number, reason?: string): ApiError {
  return new ApiError(status, status, "server wording", {
    error: { code: status, message: "server wording", reason },
  });
}

test("a 403 is not permitted and offers no recovery", () => {
  const s = panelState(failureOf(apiError(403, "forbidden")), "BGP status");
  expect(s.kind).toBe("forbidden");
  expect(s.action).toBeNull();
  expect(s.message).toContain("BGP status");
});

test("reason forbidden on another status is still not permitted", () => {
  expect(panelState({ status: 502, reason: "forbidden" }, "IPAM").kind).toBe(
    "forbidden",
  );
});

test("cluster registration changes ask for a reload", () => {
  for (const reason of [
    "cluster_unknown",
    "cluster_pin_required",
    "cluster_pin_mismatch",
    "cluster_generation_mismatch",
  ]) {
    const s = panelState({ status: 409, reason }, "the Cilium configuration");
    expect(s.kind).toBe("reload");
    expect(s.action).toBe("reload");
  }
});

test("a save conflict keeps the edit and an unknown outcome asks for a reload", () => {
  const conflict = panelState({ status: 409, reason: "conflict" }, "config");
  expect(conflict.kind).toBe("conflict");
  expect(conflict.action).toBeNull();
  const unknown = panelState(
    { status: 502, reason: "outcome_unknown" },
    "config",
  );
  expect(unknown.kind).toBe("outcome_unknown");
  expect(unknown.action).toBe("reload");
});

test("unreachable and discovery failures can be retried", () => {
  for (const reason of [
    "unreachable",
    "discovery_unavailable",
    "credentials_invalid",
    undefined,
  ]) {
    const s = panelState({ status: 502, reason }, "subsystem status");
    expect(s.kind).toBe("unavailable");
    expect(s.action).toBe("retry");
  }
});

test("a rate limit is busy and retryable", () => {
  expect(panelState({ status: 429 }, "flows")).toMatchObject({
    kind: "busy",
    action: "retry",
  });
});

test("states never echo the server's message text", () => {
  const s = panelState(failureOf(apiError(502, "unreachable")), "IPAM data");
  expect(s.message).not.toContain("server wording");
});

test("failureOf returns null for an error with no HTTP response", () => {
  expect(failureOf(new TypeError("network down"))).toBeNull();
});

test("a failed save is worded as a save and keeps the same recovery", () => {
  const what = "the Cilium configuration";
  const forbidden = panelState(
    { status: 403, reason: "forbidden" },
    what,
    "save",
  );
  expect(forbidden.message).toContain("permission to change");
  expect(forbidden.action).toBeNull();

  const unavailable = panelState(
    { status: 502, reason: "unreachable" },
    what,
    "save",
  );
  expect(unavailable.kind).toBe("unavailable");
  expect(unavailable.message).toContain("Could not save");
  expect(unavailable.action).toBe("retry");

  expect(panelState({ status: 429 }, what, "save").message).toContain("saving");
});
