import { expect, test } from "bun:test";
import {
  type Baseline,
  describeObserver,
  INITIAL_OBSERVER_STATE,
  isNewEvidence,
  nextPollDelayMs,
  OBSERVE_TIMEOUT_MS,
  type ObserverEvent,
  type ObserverState,
  reduceObserver,
  type Sample,
} from "./eso-refresh-observer.ts";

const T0 = Date.parse("2026-09-26T12:00:00Z");

const BASELINE: Baseline = {
  uid: "uid-1",
  resourceVersion: "100",
  generation: 3,
  refreshTime: "2026-09-26T11:30:00Z",
  readyLastTransitionTime: "2026-09-26T10:00:00Z",
  syncedResourceVersion: "3-aaa",
  requestedAt: "2026-09-26T12:00:00.000Z",
};

// A poll that sees the ES exactly as it was before the request.
const UNCHANGED: Sample = {
  uid: "uid-1",
  status: "Synced",
  lastSyncTime: "2026-09-26T11:30:00Z",
  syncedResourceVersion: "3-aaa",
};

function run(
  events: ObserverEvent[],
  from: ObserverState = INITIAL_OBSERVER_STATE,
) {
  return events.reduce(reduceObserver, from);
}

function awaiting(
  correlation: "strong" | "weak" = "strong",
  priorStatus: Sample["status"] = "Synced",
) {
  return run([
    { type: "request" },
    {
      type: "accepted",
      baseline: BASELINE,
      correlation,
      priorStatus,
      nowMs: T0,
    },
  ]);
}

const sample = (s: Partial<Sample>, nowMs = T0 + 1000): ObserverEvent => ({
  type: "sample",
  sample: { ...UNCHANGED, ...s },
  nowMs,
});

test("a request that is not accepted returns to idle with no line", () => {
  const s = run([{ type: "request" }, { type: "requestFailed" }]);
  expect(s.phase).toBe("idle");
  expect(describeObserver(s)).toBeNull();
  // Only a pending request can fail; an observation in progress cannot.
  expect(reduceObserver(awaiting(), { type: "requestFailed" }).phase).toBe(
    "awaitingObservation",
  );
});

test("request then accepted awaits observation with a 90 s deadline", () => {
  const s = awaiting();
  expect(s.phase).toBe("awaitingObservation");
  expect(s.deadlineMs).toBe(T0 + OBSERVE_TIMEOUT_MS);
  expect(s.correlation).toBe("strong");
});

test("AE5: pre-existing Ready=True does not satisfy a new request", () => {
  for (const c of ["strong", "weak"] as const) {
    const s = reduceObserver(awaiting(c), sample({}));
    expect(s.phase).toBe("awaitingObservation");
  }
});

test("strong correlation: later refreshTime yields observedSuccess", () => {
  const s = reduceObserver(
    awaiting("strong"),
    sample({ lastSyncTime: "2026-09-26T12:00:05Z" }),
  );
  expect(s.phase).toBe("observedSuccess");
});

test("strong correlation: equal refreshTime stays awaiting", () => {
  // Same instant, different spelling: the backend reformats time.Time.
  const s = reduceObserver(
    awaiting("strong"),
    sample({ lastSyncTime: "2026-09-26T11:30:00.000Z" }),
  );
  expect(s.phase).toBe("awaitingObservation");
});

test("strong correlation: a changed syncedResourceVersion alone is not the proof", () => {
  const s = reduceObserver(
    awaiting("strong"),
    sample({ syncedResourceVersion: "3-bbb" }),
  );
  expect(s.phase).toBe("awaitingObservation");
});

test("weak correlation: changed syncedResourceVersion yields observedSuccess", () => {
  const s = reduceObserver(
    awaiting("weak"),
    sample({ syncedResourceVersion: "3-bbb" }),
  );
  expect(s.phase).toBe("observedSuccess");
});

test("weak correlation: a missing syncedResourceVersion is not a change", () => {
  const s = reduceObserver(
    awaiting("weak"),
    sample({ syncedResourceVersion: undefined }),
  );
  expect(s.phase).toBe("awaitingObservation");
});

test("controller failure yields observedFailure with the reason", () => {
  const s = reduceObserver(
    awaiting("strong", "Synced"),
    sample({
      status: "SyncFailed",
      readyReason: "SecretSyncedError",
      readyMessage: "vault 403",
    }),
  );
  expect(s.phase).toBe("observedFailure");
  expect(s.outcome?.reason).toBe("SecretSyncedError");
  const copy = describeObserver(s);
  expect(copy?.text).toContain("SecretSyncedError");
  expect(copy?.text).toContain("vault 403");
});

test("an ES already failing before the request is not a new failure", () => {
  const s = reduceObserver(
    awaiting("strong", "SyncFailed"),
    sample({ status: "SyncFailed", readyReason: "SecretSyncedError" }),
  );
  expect(s.phase).toBe("awaitingObservation");
});

test("deadline expiry yields timeout, not failure", () => {
  const s = reduceObserver(awaiting(), {
    type: "tick",
    nowMs: T0 + OBSERVE_TIMEOUT_MS,
  });
  expect(s.phase).toBe("timeout");
  const copy = describeObserver(s);
  expect(copy?.text).toContain("No new reconciliation observed within 90 s");
  expect(copy?.text.toLowerCase()).not.toContain("fail");
  expect(copy?.text.toLowerCase()).not.toContain("error");
});

test("a sample carrying new evidence at the deadline still counts", () => {
  const s = reduceObserver(
    awaiting("strong"),
    sample({ lastSyncTime: "2026-09-26T12:01:00Z" }, T0 + OBSERVE_TIMEOUT_MS),
  );
  expect(s.phase).toBe("observedSuccess");
});

test("403 mid-wait yields accessLost", () => {
  const s = reduceObserver(awaiting(), { type: "forbidden" });
  expect(s.phase).toBe("accessLost");
  expect(describeObserver(s)?.text).toContain("may still be in progress");
});

test("404 mid-wait yields targetChanged", () => {
  expect(reduceObserver(awaiting(), { type: "notFound" }).phase).toBe(
    "targetChanged",
  );
});

test("uid change mid-wait yields targetChanged", () => {
  const s = reduceObserver(
    awaiting(),
    sample({ uid: "uid-2", lastSyncTime: "2026-09-26T12:05:00Z" }),
  );
  expect(s.phase).toBe("targetChanged");
});

test("clusterChanged yields targetChanged", () => {
  expect(reduceObserver(awaiting(), { type: "clusterChanged" }).phase).toBe(
    "targetChanged",
  );
});

test("cancel is terminal — later samples are ignored", () => {
  const cancelled = reduceObserver(awaiting(), { type: "cancel" });
  expect(cancelled.phase).toBe("cancelled");
  expect(describeObserver(cancelled)).toBeNull();
  const after = reduceObserver(
    cancelled,
    sample({ lastSyncTime: "2026-09-26T12:05:00Z" }),
  );
  expect(after).toBe(cancelled);
});

test("terminal phases ignore every later event except a new request", () => {
  const done = reduceObserver(
    awaiting("weak"),
    sample({ syncedResourceVersion: "3-bbb" }),
  );
  for (const e of [
    { type: "forbidden" },
    { type: "notFound" },
    { type: "error" },
    { type: "clusterChanged" },
    { type: "tick", nowMs: T0 + 10 * OBSERVE_TIMEOUT_MS },
    { type: "cancel" },
  ] as ObserverEvent[]) {
    expect(reduceObserver(done, e)).toBe(done);
  }
  expect(reduceObserver(done, { type: "request" }).phase).toBe("requested");
});

test("three consecutive errors yield timeout with observation_unavailable", () => {
  const s = run(
    [{ type: "error" }, { type: "error" }, { type: "error" }],
    awaiting(),
  );
  expect(s.phase).toBe("timeout");
  expect(s.detail).toBe("observation_unavailable");
  const copy = describeObserver(s);
  expect(copy?.text).not.toContain("No new reconciliation observed");
});

test("a successful poll resets the error streak", () => {
  const s = run(
    [{ type: "error" }, { type: "error" }, sample({}), { type: "error" }],
    awaiting(),
  );
  expect(s.phase).toBe("awaitingObservation");
  expect(s.consecutiveErrors).toBe(1);
});

test("an unrelated reconcile under weak correlation is labelled, not claimed", () => {
  const s = reduceObserver(
    awaiting("weak"),
    sample({ syncedResourceVersion: "3-bbb" }),
  );
  const text = describeObserver(s)?.text ?? "";
  expect(text).toContain("observed after your request");
  expect(text.toLowerCase()).not.toMatch(
    /\bcaused\b|\byour sync\b|completed your/,
  );
});

test("strong success says ESO reconciled after the request", () => {
  const s = reduceObserver(
    awaiting("strong"),
    sample({ lastSyncTime: "2026-09-26T12:00:05Z" }),
  );
  expect(describeObserver(s)?.text).toContain("after your request");
});

test("each visible phase renders a distinct tone", () => {
  const tones = new Set<string>();
  const states = [
    run([{ type: "request" }]),
    awaiting(),
    reduceObserver(
      awaiting("weak"),
      sample({ syncedResourceVersion: "3-bbb" }),
    ),
    reduceObserver(awaiting(), sample({ status: "SyncFailed" })),
    reduceObserver(awaiting(), {
      type: "tick",
      nowMs: T0 + OBSERVE_TIMEOUT_MS,
    }),
    reduceObserver(awaiting(), { type: "forbidden" }),
  ];
  for (const s of states) tones.add(`${s.phase}:${describeObserver(s)?.tone}`);
  expect(new Set(states.map((s) => describeObserver(s)?.tone)).size).toBe(5);
  expect(tones.size).toBe(6);
});

test("isNewEvidence never compares against the backend clock", () => {
  // A sample stamped long after requestedAt but identical to the baseline is
  // not evidence: requestedAt is on the backend's clock, the ES on ESO's.
  expect(isNewEvidence(BASELINE, { ...UNCHANGED }, "weak")).toBe(false);
  expect(isNewEvidence(BASELINE, { ...UNCHANGED }, "strong")).toBe(false);
});

test("an unparseable sample refreshTime is not evidence", () => {
  expect(
    isNewEvidence(
      BASELINE,
      { ...UNCHANGED, lastSyncTime: "garbage" },
      "strong",
    ),
  ).toBe(false);
});

test("nextPollDelayMs is 1s, 2s, 3s, 5s, 5s… capped", () => {
  expect([0, 1, 2, 3, 4, 20].map(nextPollDelayMs)).toEqual([
    1000, 2000, 3000, 5000, 5000, 5000,
  ]);
});
