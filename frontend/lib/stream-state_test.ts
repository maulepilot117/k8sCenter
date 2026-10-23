import { expect, test } from "bun:test";
import { streamAuthFrame, streamErrorMessage } from "@/lib/stream-state.ts";
import { UNKNOWN_GENERATION } from "@/src/lib/cluster.ts";

test("the auth frame names the pinned remote cluster and its generation", () => {
  const frame = streamAuthFrame("tok", {
    clusterId: "c-remote",
    generation: "2026-10-01T00:00:00Z",
    epoch: 3,
  });
  expect(frame).toEqual({
    type: "auth",
    token: "tok",
    clusterId: "c-remote",
    generation: "2026-10-01T00:00:00Z",
  });
});

test("the unknown generation sentinel is never sent", () => {
  const frame = streamAuthFrame("tok", {
    clusterId: "local",
    generation: UNKNOWN_GENERATION,
    epoch: 0,
  });
  expect(frame).toEqual({ type: "auth", token: "tok", clusterId: "local" });
});

test("a remote-unsupported refusal says the stream is not available there", () => {
  const msg = streamErrorMessage(
    { reason: "unsupported_platform", message: "server wording" },
    "Live log streaming",
  );
  expect(msg).toContain("Live log streaming");
  expect(msg).toContain("not available");
  expect(msg).not.toContain("server wording");
});

test("a stream that names no cluster asks for a reload", () => {
  for (const reason of [
    "cluster_required",
    "cluster_pin_mismatch",
    "cluster_generation_mismatch",
    "cluster_invalid",
  ]) {
    const msg = streamErrorMessage(
      { reason, message: "x" },
      "Live log streaming",
    );
    expect(msg).toContain("Reload the page");
  }
});

test("a forbidden refusal says the user lacks permission", () => {
  const msg = streamErrorMessage(
    { reason: "forbidden", message: "admin role required" },
    "Live log streaming",
  );
  expect(msg).toContain("permission");
});

test("an unknown reason gets generic copy, not the server's wording", () => {
  const msg = streamErrorMessage(
    { reason: "something_new", message: "internal detail" },
    "Live log streaming",
  );
  expect(msg).not.toContain("internal detail");
  expect(msg).toContain("Live log streaming");
});

test("a frame without a reason keeps the server's fixed message", () => {
  expect(
    streamErrorMessage(
      { message: "invalid container name" },
      "Live log streaming",
    ),
  ).toBe("invalid container name");
});
