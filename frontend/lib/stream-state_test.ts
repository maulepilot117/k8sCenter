import { expect, test } from "bun:test";
import {
  isReplayedLine,
  laterThan,
  logLineTimestamp,
  streamAuthFrame,
  streamErrorMessage,
} from "@/lib/stream-state.ts";
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

test("every lifecycle end has its own copy and never the server's wording", () => {
  const reasons = [
    "token_expired",
    "server_restarting",
    "cluster_removed",
    "access_revoked",
    "signed_out",
    "idle_timeout",
    "unreachable",
    "cluster_unknown",
    "credentials_invalid",
    "db_unavailable",
  ];
  const seen = new Set<string>();
  for (const reason of reasons) {
    const msg = streamErrorMessage(
      { reason, message: "server wording" },
      "Live log streaming",
    );
    expect(msg).not.toContain("server wording");
    expect(msg).not.toContain("stopped unexpectedly");
    seen.add(msg);
  }
  expect(seen.size).toBe(reasons.length);
});

test("a removed cluster and revoked access say the stream ended", () => {
  expect(
    streamErrorMessage({ reason: "cluster_removed" }, "Live log streaming"),
  ).toContain("removed from k8sCenter");
  expect(
    streamErrorMessage({ reason: "access_revoked" }, "Live log streaming"),
  ).toContain("access was revoked");
});

test("a log line's timestamp compares by value, not by text", () => {
  // Kubernetes trims trailing zeros from the fraction, so ".5Z" is later
  // than ".25Z" although it sorts earlier as text.
  const a = logLineTimestamp("2026-11-03T15:00:01.5Z later");
  const b = logLineTimestamp("2026-11-03T15:00:01.25Z earlier");
  expect(a).not.toBeNull();
  expect(b).not.toBeNull();
  expect(laterThan(a as [string, number], b as [string, number])).toBe(true);
  expect(laterThan(b as [string, number], a as [string, number])).toBe(false);
  expect(logLineTimestamp("2026-11-03T15:00:01Z whole second")).toEqual([
    "2026-11-03T15:00:01",
    0,
  ]);
  expect(logLineTimestamp("no timestamp here")).toBeNull();
});

test("a resumed line at or before the last one shown is a replay", () => {
  const last = logLineTimestamp("2026-11-03T15:00:02.5Z shown") as [
    string,
    number,
  ];
  expect(isReplayedLine("2026-11-03T15:00:01Z old", last)).toBe(true);
  expect(isReplayedLine("2026-11-03T15:00:02.5Z same", last)).toBe(true);
  expect(isReplayedLine("2026-11-03T15:00:02.500001Z new", last)).toBe(false);
  expect(isReplayedLine("no timestamp", last)).toBe(false);
  expect(isReplayedLine("2026-11-03T15:00:01Z anything", null)).toBe(false);
});
