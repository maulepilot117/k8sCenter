import { expect, test } from "bun:test";
import {
  type Baseline,
  createRefreshPoller,
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

test("a sample judged at or after the deadline times out, whatever it shows", () => {
  // The 90 s bound is a promise to the operator: evidence that only arrives
  // once it has passed is not reported as the request's outcome.
  for (const late of [
    { lastSyncTime: "2026-09-26T12:01:00Z" },
    { status: "SyncFailed" as const },
  ]) {
    const s = reduceObserver(
      awaiting("strong"),
      sample(late, T0 + OBSERVE_TIMEOUT_MS),
    );
    expect(s.phase).toBe("timeout");
  }
});

test("a failure seen only as a status change is never claimed as caused", () => {
  // The page's pre-request status may be stale, so a SyncFailed transition
  // proves only that the failure was seen after the request.
  const s = reduceObserver(
    awaiting("strong", "Synced"),
    sample({ status: "SyncFailed", readyReason: "SecretSyncedError" }),
  );
  const text = describeObserver(s)?.text ?? "";
  expect(text).toContain("observed after your request");
  expect(text).not.toContain("Sync failed after your request");
});

test("a failure on a sample with a new refreshTime keeps the strong wording", () => {
  const s = reduceObserver(
    awaiting("strong", "SyncFailed"),
    sample({
      status: "SyncFailed",
      lastSyncTime: "2026-09-26T12:00:05Z",
      readyReason: "SecretSyncedError",
    }),
  );
  expect(s.phase).toBe("observedFailure");
  expect(describeObserver(s)?.text).toContain("Sync failed after your request");
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

// --- Poller: the lifecycle the island delegates to -------------------------
//
// Driven by a hand-advanced clock, so the deadline, a hung poll and the
// backoff schedule are exact rather than waited out in real time.

const flush = () => new Promise((r) => setTimeout(r, 0));

function fakeClock() {
  let now = T0;
  let next = 0;
  const timers = new Map<number, { at: number; fn: () => void }>();
  return {
    now: () => now,
    setTimer: (fn: () => void, ms: number) => {
      next += 1;
      timers.set(next, { at: now + ms, fn });
      return next;
    },
    clearTimer: (h: unknown) => {
      timers.delete(h as number);
    },
    pending: () => timers.size,
    /** Runs every timer due within `ms`, in order, letting polls settle. */
    async advance(ms: number) {
      const target = now + ms;
      for (;;) {
        const due = [...timers.entries()]
          .filter(([, t]) => t.at <= target)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        timers.delete(due[0]);
        now = due[1].at;
        due[1].fn();
        await flush();
      }
      now = target;
      await flush();
    },
  };
}

/** A fetch that only settles when the test says so, or rejects on abort. */
function pendingFetch(signal: AbortSignal) {
  let resolve!: (s: Sample | undefined) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<Sample | undefined>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  signal.addEventListener("abort", () =>
    reject(new DOMException("aborted", "AbortError")),
  );
  return { promise, resolve, reject, signal };
}

function pollerHarness() {
  const clock = fakeClock();
  let state: ObserverState = INITIAL_OBSERVER_STATE;
  const fetches: ReturnType<typeof pendingFetch>[] = [];
  const poller = createRefreshPoller({
    fetchSample: (signal) => {
      const f = pendingFetch(signal);
      fetches.push(f);
      return f.promise;
    },
    statusOf: (err) => (err as { status?: number }).status,
    dispatch: (e) => {
      state = reduceObserver(state, e);
      return state;
    },
    now: clock.now,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
  });
  const begin = () => {
    state = run([
      { type: "request" },
      {
        type: "accepted",
        baseline: BASELINE,
        correlation: "strong",
        priorStatus: "Synced",
        nowMs: clock.now(),
      },
    ]);
    poller.begin(state);
  };
  return { clock, fetches, poller, begin, state: () => state };
}

test("poller: polls on the 1 s, 2 s, 3 s, 5 s schedule while nothing changes", async () => {
  const h = pollerHarness();
  h.begin();
  const at: number[] = [];
  for (let i = 0; i < 5; i++) {
    const before = h.fetches.length;
    // Answer each poll with the unchanged ES as soon as it is issued.
    while (h.fetches.length === before) await h.clock.advance(500);
    at.push(h.clock.now() - T0);
    h.fetches[h.fetches.length - 1].resolve({ ...UNCHANGED });
    await flush();
  }
  expect(at).toEqual([1000, 3000, 6000, 11000, 16000]);
  expect(h.state().phase).toBe("awaitingObservation");
});

test("poller: a hung poll is aborted at the 90 s deadline and the observer times out", async () => {
  const h = pollerHarness();
  h.begin();
  await h.clock.advance(1000);
  expect(h.fetches).toHaveLength(1); // issued, never answered
  await h.clock.advance(OBSERVE_TIMEOUT_MS);
  expect(h.state().phase).toBe("timeout");
  expect(h.fetches[0].signal.aborted).toBe(true);
  expect(h.clock.pending()).toBe(0);
});

test("poller: an observed outcome stops polling and the deadline timer", async () => {
  const h = pollerHarness();
  h.begin();
  await h.clock.advance(1000);
  h.fetches[0].resolve({ ...UNCHANGED, lastSyncTime: "2026-09-26T12:00:05Z" });
  await flush();
  expect(h.state().phase).toBe("observedSuccess");
  expect(h.clock.pending()).toBe(0);
});

test("poller: 403 and 404 map to accessLost and targetChanged; others count as errors", async () => {
  for (const [status, phase] of [
    [403, "accessLost"],
    [404, "targetChanged"],
  ] as const) {
    const h = pollerHarness();
    h.begin();
    await h.clock.advance(1000);
    h.fetches[0].reject({ status });
    await flush();
    expect(h.state().phase).toBe(phase);
    expect(h.clock.pending()).toBe(0);
  }
  const h = pollerHarness();
  h.begin();
  await h.clock.advance(1000);
  h.fetches[0].reject({ status: 500 });
  await flush();
  expect(h.state().phase).toBe("awaitingObservation");
  expect(h.state().consecutiveErrors).toBe(1);
});

test("poller: beginning again while running keeps a single polling chain", async () => {
  const h = pollerHarness();
  h.begin();
  h.poller.begin(h.state());
  // One poll timer and one deadline timer, not two of each.
  expect(h.clock.pending()).toBe(2);
  await h.clock.advance(1000);
  expect(h.fetches).toHaveLength(1);
});

test("poller: stop aborts the in-flight poll and a late answer writes nothing", async () => {
  const h = pollerHarness();
  h.begin();
  await h.clock.advance(1000);
  const before = h.state();
  h.poller.stop();
  expect(h.fetches[0].signal.aborted).toBe(true);
  h.fetches[0].resolve({ ...UNCHANGED, lastSyncTime: "2026-09-26T12:00:05Z" });
  await flush();
  expect(h.state()).toBe(before);
  expect(h.clock.pending()).toBe(0);
});
