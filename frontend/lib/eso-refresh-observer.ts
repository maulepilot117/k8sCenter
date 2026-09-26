/**
 * Refresh-outcome observer for ExternalSecret force-sync (Release B, U19b;
 * plan D6). Two parts:
 *
 * - `reduceObserver`, a pure reducer: no fetch, no timers, no signals; time
 *   arrives as `nowMs` on events.
 * - `createRefreshPoller`, the lifecycle around it: the poll schedule, the
 *   deadline timer and one AbortController per poll. Fetch and timers are
 *   injected, so it runs under a fake clock in tests and the island only
 *   wires it to the real ones.
 *
 * The rule it enforces (AE5): an ES that was already Ready=True never counts
 * as the outcome of a new request. Only evidence that moved relative to the
 * pre-patch baseline the 202 returned does, and never a comparison between
 * ESO's clock and the backend's.
 */

import type { Status } from "./eso-types.ts";

/** How far the baseline lets the UI attribute a change to the request. */
export type Correlation = "strong" | "weak";

/** The pre-patch observation a force-sync 202 returns (backend U19a). */
export interface Baseline {
  uid: string;
  resourceVersion: string;
  generation?: number;
  refreshTime?: string;
  readyLastTransitionTime?: string;
  syncedResourceVersion?: string;
  requestedAt: string;
}

/** The 202 body of POST .../force-sync. */
export interface ForceSyncAccepted {
  status: "force-syncing";
  /** Absent from a backend that predates U19a. */
  correlation?: Correlation;
  baseline?: Baseline;
}

/** The fields of one ExternalSecret detail poll the observer judges. */
export interface Sample {
  uid: string;
  status: Status;
  /** ESO's status.refreshTime. */
  lastSyncTime?: string;
  syncedResourceVersion?: string;
  readyReason?: string;
  readyMessage?: string;
}

export type ObserverPhase =
  | "idle"
  | "requested"
  | "awaitingObservation"
  | "observedSuccess"
  | "observedFailure"
  | "timeout"
  | "cancelled"
  | "accessLost"
  | "targetChanged";

export interface ObserverState {
  phase: ObserverPhase;
  baseline?: Baseline;
  correlation?: Correlation;
  /** The ES status the page showed before the request. */
  priorStatus?: Status;
  /** Polls completed so far; drives nextPollDelayMs. */
  attempt: number;
  consecutiveErrors: number;
  deadlineMs?: number;
  /** Set on a timeout caused by unreadable polls rather than silence. */
  detail?: "observation_unavailable";
  outcome?: {
    reason?: string;
    message?: string;
    /** True only when a new reconcile ties the failure to this request. */
    attributable?: boolean;
  };
}

export type ObserverEvent =
  | { type: "request" }
  /** The request was refused, or accepted without a baseline to observe. */
  | { type: "requestFailed" }
  | {
      type: "accepted";
      baseline: Baseline;
      correlation: Correlation;
      priorStatus?: Status;
      nowMs: number;
    }
  | { type: "sample"; sample: Sample; nowMs: number }
  | { type: "forbidden" }
  | { type: "notFound" }
  | { type: "error" }
  | { type: "clusterChanged" }
  | { type: "tick"; nowMs: number }
  | { type: "cancel" };

/** How long the observer waits for new evidence before timing out. */
export const OBSERVE_TIMEOUT_MS = 90_000;

/** Consecutive unreadable polls after which observation is given up. */
const MAX_CONSECUTIVE_ERRORS = 3;

export const INITIAL_OBSERVER_STATE: ObserverState = Object.freeze({
  phase: "idle",
  attempt: 0,
  consecutiveErrors: 0,
});

const TERMINAL: ReadonlySet<ObserverPhase> = new Set([
  "observedSuccess",
  "observedFailure",
  "timeout",
  "cancelled",
  "accessLost",
  "targetChanged",
]);

export function isTerminal(phase: ObserverPhase): boolean {
  return TERMINAL.has(phase);
}

function parseMs(t: string | undefined): number | null {
  if (!t) return null;
  const ms = Date.parse(t);
  return Number.isNaN(ms) ? null : ms;
}

/**
 * True when `s` shows a reconcile that happened after the baseline was taken.
 *
 * - strong: `refreshTime` strictly later. ESO sets it on each successful
 *   sync, and both values come from ESO's clock.
 * - weak: `syncedResourceVersion` changed. ESO derives it from the ES's
 *   generation and metadata, which the force-sync annotation itself changes,
 *   so the next completed sync moves it.
 *
 * Deliberately not evidence: a changed `resourceVersion` (the force-sync
 * patch always moves it) and any timestamp compared against `requestedAt`
 * (that is the backend's clock, the ES carries ESO's).
 */
export function isNewEvidence(b: Baseline, s: Sample, c: Correlation): boolean {
  if (c === "strong") {
    const before = parseMs(b.refreshTime);
    const after = parseMs(s.lastSyncTime);
    return before !== null && after !== null && after > before;
  }
  return (
    !!s.syncedResourceVersion &&
    s.syncedResourceVersion !== b.syncedResourceVersion
  );
}

/**
 * True when `s` shows a failure that began after the request: the ES now
 * reports SyncFailed and did not before. A failed sync moves neither
 * refreshTime nor syncedResourceVersion, so this is how a failure becomes
 * visible at all. An ES that was already failing cannot be told apart from
 * one that failed again, so it keeps waiting.
 */
function isNewFailure(prior: Status | undefined, s: Sample): boolean {
  return s.status === "SyncFailed" && prior !== "SyncFailed";
}

function applySample(
  st: ObserverState,
  s: Sample,
  nowMs: number,
): ObserverState {
  const b = st.baseline;
  if (!b || !st.correlation) return st;
  if (s.uid !== b.uid) return { ...st, phase: "targetChanged" };

  const polled = { ...st, attempt: st.attempt + 1, consecutiveErrors: 0 };
  // The bound comes first: evidence that only arrives after it is not
  // reported as this request's outcome.
  if (expired(polled, nowMs)) return { ...polled, phase: "timeout" };
  if (isNewEvidence(b, s, st.correlation)) {
    // Evidence of a new reconcile: read the outcome from the new sample.
    return s.status === "SyncFailed"
      ? {
          ...polled,
          phase: "observedFailure",
          outcome: {
            reason: s.readyReason,
            message: s.readyMessage,
            attributable: st.correlation === "strong",
          },
        }
      : { ...polled, phase: "observedSuccess" };
  }
  if (isNewFailure(st.priorStatus, s)) {
    // Only a status change: the pre-request status is the page's, which may
    // be stale, so this is never worded as the request's result.
    return {
      ...polled,
      phase: "observedFailure",
      outcome: {
        reason: s.readyReason,
        message: s.readyMessage,
        attributable: false,
      },
    };
  }
  return polled;
}

function expired(st: ObserverState, nowMs: number): boolean {
  return st.deadlineMs !== undefined && nowMs >= st.deadlineMs;
}

/**
 * Advances the observer by one event. Terminal phases return the same state
 * object for every event except a new request, so a late poll can never
 * overwrite an outcome already shown.
 */
export function reduceObserver(
  st: ObserverState,
  e: ObserverEvent,
): ObserverState {
  if (e.type === "request") {
    return { ...INITIAL_OBSERVER_STATE, phase: "requested" };
  }
  if (isTerminal(st.phase) || st.phase === "idle") return st;

  switch (e.type) {
    case "requestFailed":
      return st.phase === "requested" ? INITIAL_OBSERVER_STATE : st;
    case "accepted":
      if (st.phase !== "requested") return st;
      return {
        ...INITIAL_OBSERVER_STATE,
        phase: "awaitingObservation",
        baseline: e.baseline,
        correlation: e.correlation,
        priorStatus: e.priorStatus,
        deadlineMs: e.nowMs + OBSERVE_TIMEOUT_MS,
      };
    case "cancel":
      return { ...st, phase: "cancelled" };
    case "clusterChanged":
      return { ...st, phase: "targetChanged" };
  }

  if (st.phase !== "awaitingObservation") return st;
  switch (e.type) {
    case "sample":
      return applySample(st, e.sample, e.nowMs);
    case "forbidden":
      return { ...st, phase: "accessLost" };
    case "notFound":
      return { ...st, phase: "targetChanged" };
    case "error": {
      const consecutiveErrors = st.consecutiveErrors + 1;
      return consecutiveErrors >= MAX_CONSECUTIVE_ERRORS
        ? {
            ...st,
            consecutiveErrors,
            phase: "timeout",
            detail: "observation_unavailable",
          }
        : { ...st, consecutiveErrors, attempt: st.attempt + 1 };
    }
    case "tick":
      return expired(st, e.nowMs) ? { ...st, phase: "timeout" } : st;
  }
}

const POLL_DELAYS_MS = [1000, 2000, 3000];
const MAX_POLL_DELAY_MS = 5000;

/** Delay before poll number `attempt` (0-based): 1 s, 2 s, 3 s, then 5 s. */
export function nextPollDelayMs(attempt: number): number {
  return POLL_DELAYS_MS[attempt] ?? MAX_POLL_DELAY_MS;
}

export type ObserverTone = "info" | "success" | "danger" | "muted" | "warning";

function failureText(st: ObserverState): string {
  const { reason, message } = st.outcome ?? {};
  const why = [reason, message].filter(Boolean).join(": ");
  const lead = st.outcome?.attributable
    ? "Sync failed after your request"
    : "A sync failure was observed after your request";
  return why ? `${lead}: ${why}` : `${lead}.`;
}

/**
 * The line the page shows for a phase, or null when nothing should show.
 * Weak correlation never claims the request caused what was observed.
 */
export function describeObserver(
  st: ObserverState,
): { text: string; tone: ObserverTone } | null {
  switch (st.phase) {
    case "requested":
      return { text: "Requesting a sync…", tone: "info" };
    case "awaitingObservation":
      return {
        text:
          st.correlation === "strong"
            ? "Sync requested. Waiting for External Secrets Operator to reconcile…"
            : "Sync requested. Waiting for a change to this ExternalSecret…",
        tone: "info",
      };
    case "observedSuccess":
      return {
        text:
          st.correlation === "strong"
            ? "Synced. External Secrets Operator reconciled after your request."
            : "A new sync was observed after your request.",
        tone: "success",
      };
    case "observedFailure":
      return { text: failureText(st), tone: "danger" };
    case "timeout":
      return {
        text:
          st.detail === "observation_unavailable"
            ? "Request accepted, but this ExternalSecret could not be read to confirm the outcome."
            : "Request accepted. No new reconciliation observed within 90 s.",
        tone: "muted",
      };
    case "accessLost":
      return {
        text: "You no longer have access to this ExternalSecret. The refresh may still be in progress.",
        tone: "warning",
      };
    case "targetChanged":
      return {
        text: "This ExternalSecret was replaced or the cluster changed. Discarded the pending observation.",
        tone: "warning",
      };
    default:
      return null;
  }
}

/** What the poller needs from its host. Everything with side effects. */
export interface PollerDeps {
  /** One ES read. Must reject once `signal` aborts. */
  fetchSample: (signal: AbortSignal) => Promise<Sample | undefined>;
  /** The HTTP status of a failed read, if it has one. */
  statusOf: (err: unknown) => number | undefined;
  /** Applies an event to the observer and returns the resulting state. */
  dispatch: (e: ObserverEvent) => ObserverState;
  now: () => number;
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
}

export interface RefreshPoller {
  /** Starts polling for an observer in awaitingObservation; no-op if running. */
  begin: (state: ObserverState) => void;
  /** Clears both timers and aborts an in-flight poll; nothing is dispatched. */
  stop: () => void;
  readonly running: boolean;
}

/**
 * Runs one serial chain of polls for an accepted force-sync. A single chain
 * is guaranteed: `begin` while running is ignored, and each poll schedules
 * the next only after it settles. The deadline is its own timer, so a poll
 * that never answers is aborted and the observer still times out on time.
 */
export function createRefreshPoller(deps: PollerDeps): RefreshPoller {
  let pollTimer: unknown;
  let deadlineTimer: unknown;
  let inFlight: AbortController | undefined;
  let running = false;

  const stop = () => {
    if (pollTimer !== undefined) deps.clearTimer(pollTimer);
    if (deadlineTimer !== undefined) deps.clearTimer(deadlineTimer);
    inFlight?.abort();
    pollTimer = deadlineTimer = inFlight = undefined;
    running = false;
  };

  // Continue only while the observer is still waiting.
  const stillWaiting = (st: ObserverState) => {
    if (st.phase === "awaitingObservation") return true;
    stop();
    return false;
  };

  const schedule = (attempt: number) => {
    pollTimer = deps.setTimer(poll, nextPollDelayMs(attempt));
  };

  async function poll() {
    pollTimer = undefined;
    if (!stillWaiting(deps.dispatch({ type: "tick", nowMs: deps.now() }))) {
      return;
    }
    const abort = new AbortController();
    inFlight = abort;
    let st: ObserverState;
    try {
      const sample = await deps.fetchSample(abort.signal);
      if (abort.signal.aborted) return;
      st = deps.dispatch(
        sample
          ? { type: "sample", sample, nowMs: deps.now() }
          : { type: "error" },
      );
    } catch (err) {
      if (abort.signal.aborted) return;
      const status = deps.statusOf(err);
      st = deps.dispatch({
        type:
          status === 403 ? "forbidden" : status === 404 ? "notFound" : "error",
      });
    }
    inFlight = undefined;
    if (stillWaiting(st)) schedule(st.attempt);
  }

  const begin = (state: ObserverState) => {
    if (running || state.phase !== "awaitingObservation") return;
    running = true;
    const remaining = Math.max(
      0,
      (state.deadlineMs ?? deps.now()) - deps.now(),
    );
    deadlineTimer = deps.setTimer(() => {
      deadlineTimer = undefined;
      inFlight?.abort();
      stillWaiting(deps.dispatch({ type: "tick", nowMs: deps.now() }));
    }, remaining);
    schedule(state.attempt);
  };

  return {
    begin,
    stop,
    get running() {
      return running;
    },
  };
}
