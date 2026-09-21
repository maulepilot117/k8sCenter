/**
 * One pod classification, and the two rankings the pod widgets select with.
 *
 * `pending-pods` and `pod-restarts` read the same list from the same route and
 * both have to decide what state a pod is in. Written twice, that is two
 * chances for two cards on one dashboard to disagree about the same pod -- one
 * calling it pending while the other calls it crash-looping. So it is written
 * once here and each widget selects over the result (D-10, KTD8).
 *
 * Everything below is defensive about its input on purpose. These are raw
 * Kubernetes objects off the generic list route, not a shape this build
 * controls, and one unreadable item must degrade to `unknown` rather than
 * throw -- otherwise a single malformed pod blanks a card describing several
 * hundred readable ones.
 */
import type { ResourceListPage } from "./wire-types.ts";

/**
 * What a pod is doing, as far as its status can say.
 *
 * `crash-looping` is not a Kubernetes phase. A pod whose container keeps
 * dying reports phase `Running`, because the kubelet keeps restarting it --
 * so a classifier reading the phase alone files the single most actionable
 * state on the cluster under "running". It is promoted to a state of its own
 * here, and it outranks the phase.
 *
 * `unschedulable` is likewise a refinement of `Pending`: both are pods that
 * are not up, but one is waiting on a container start and will usually come
 * up by itself, while the other is waiting on capacity, a taint or a node
 * selector and will wait forever until somebody changes something. They need
 * different actions, so they are different states.
 */
export const POD_STATES = [
  "running",
  "succeeded",
  "failed",
  "crash-looping",
  "unschedulable",
  "pending",
  "unknown",
] as const;
export type PodState = (typeof POD_STATES)[number];

/** The waiting reason that means a container is in a restart loop. Exactly
 * one string: an image-pull failure is a container that has never started, so
 * there is no restart history to rank it by and it belongs on the pending
 * card with its reason shown, not in a restart ranking. */
const CRASH_LOOP_REASON = "CrashLoopBackOff";

export interface PodHealth {
  name: string;
  namespace: string;
  state: PodState;
  /** Summed across every container status the pod carries, init containers
   * included. Zero when the pod has no container statuses at all, which is
   * "no restart history", not "no restarts observed". */
  restarts: number;
  /**
   * The waiting reason of a container that has not started, or the reason the
   * scheduler gave for refusing the pod. Empty when neither is readable.
   *
   * This is what tells two pending pods apart on the card -- `ImagePullBackOff`
   * and `Unschedulable` need different people to do different things -- so it
   * is carried even for states that do not branch on it.
   */
  reason: string;
  /** Milliseconds since `metadata.creationTimestamp`, or null when that field
   * is missing or unparseable. Null sorts last everywhere below: an age we
   * cannot read must not win a ranking that means "longest stuck". */
  ageMs: number | null;
}

function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function arr(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

/** Every container status a pod carries, regular and init. Init containers
 * are included because an init container in a restart loop keeps the pod
 * down exactly as an app container does, and a ranking that ignored them
 * would report the stuck pod as having no restarts. */
function containerStatuses(status: Record<string, unknown>): unknown[] {
  return [
    ...arr(status.containerStatuses),
    ...arr(status.initContainerStatuses),
  ];
}

function restartsOf(statuses: readonly unknown[]): number {
  let total = 0;
  for (const cs of statuses) {
    const o = obj(cs);
    const count = o?.restartCount;
    if (typeof count === "number" && Number.isFinite(count) && count > 0) {
      total += Math.trunc(count);
    }
  }
  return total;
}

/** The first non-empty `state.waiting.reason` among the pod's containers. */
function waitingReasonOf(statuses: readonly unknown[]): string {
  for (const cs of statuses) {
    const waiting = obj(obj(obj(cs)?.state)?.waiting);
    const reason = str(waiting?.reason);
    if (reason !== "") return reason;
  }
  return "";
}

/** The reason on a `PodScheduled: False` condition, and "" when the pod is
 * scheduled or the condition is absent. An absent condition is a pod the
 * scheduler has not looked at yet, which is not the same as one it refused --
 * claiming "unschedulable" there would be an alarm about a decision nobody
 * has made. */
function scheduleRefusalOf(status: Record<string, unknown>): {
  refused: boolean;
  reason: string;
} {
  for (const c of arr(status.conditions)) {
    const o = obj(c);
    if (str(o?.type) !== "PodScheduled") continue;
    if (str(o?.status) !== "False") return { refused: false, reason: "" };
    return { refused: true, reason: str(o?.reason) };
  }
  return { refused: false, reason: "" };
}

const UNREADABLE: PodHealth = {
  name: "",
  namespace: "",
  state: "unknown",
  restarts: 0,
  reason: "",
  ageMs: null,
};

/**
 * One pod object to the fields both widgets rank and render.
 *
 * `now` is injected rather than read from the clock so the age arithmetic is
 * testable; every caller inside the app leaves it defaulted.
 *
 * The precedence is deliberate and is the only judgement in this function:
 * terminal phases first (a Succeeded pod is finished, whatever its containers
 * once did), then the crash loop (which hides under phase `Running`), then the
 * pending refinement, then the plain phases. Anything else is `unknown` --
 * including a body with no status at all, which is a pod this build cannot
 * read rather than a healthy one.
 */
export function classifyPod(
  value: unknown,
  now: number = Date.now(),
): PodHealth {
  const o = obj(value);
  if (o === null) return UNREADABLE;

  const meta = obj(o.metadata) ?? {};
  const status = obj(o.status) ?? {};
  const statuses = containerStatuses(status);
  const waiting = waitingReasonOf(statuses);
  const schedule = scheduleRefusalOf(status);
  const phase = str(status.phase);

  const created = Date.parse(str(meta.creationTimestamp));
  const base: PodHealth = {
    name: str(meta.name),
    namespace: str(meta.namespace),
    state: "unknown",
    restarts: restartsOf(statuses),
    reason: waiting !== "" ? waiting : schedule.reason,
    ageMs: Number.isNaN(created) ? null : now - created,
  };

  if (phase === "Succeeded") return { ...base, state: "succeeded" };
  if (phase === "Failed") return { ...base, state: "failed" };
  if (waiting === CRASH_LOOP_REASON) {
    return { ...base, state: "crash-looping" };
  }
  if (phase === "Pending") {
    return { ...base, state: schedule.refused ? "unschedulable" : "pending" };
  }
  if (phase === "Running") return { ...base, state: "running" };
  return base;
}

/** Classifies a whole page. Total: a null payload, a missing `items` or an
 * `items` that is not an array all yield an empty list. */
export function classifyPods(
  page: ResourceListPage | null | undefined,
  now: number = Date.now(),
): PodHealth[] {
  if (page === null || page === undefined) return [];
  return arr(page.items).map((item) => classifyPod(item, now));
}

/** Namespace then name, so a tie orders identically on every refresh rather
 * than following whatever order the informer happened to return. */
function byIdentity(a: PodHealth, b: PodHealth): number {
  return a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name);
}

/**
 * Worst-first for the restart card.
 *
 * Crash-looping pods lead regardless of count, because the loop is the
 * actionable thing: a pod that restarted forty times last week and has been up
 * since is history, while one looping right now is an outage in progress, and
 * a pure count sort buries the second under the first. Within that, higher
 * counts lead; a pod with no restart history has nothing to rank and sorts
 * last rather than being treated as a zero that beats something.
 *
 * Returns a new array; the input is not reordered.
 */
export function rankByRestarts(pods: readonly PodHealth[]): PodHealth[] {
  return [...pods].sort((a, b) => {
    const loop =
      Number(b.state === "crash-looping") - Number(a.state === "crash-looping");
    if (loop !== 0) return loop;
    if (a.restarts !== b.restarts) return b.restarts - a.restarts;
    return byIdentity(a, b);
  });
}

/**
 * Longest-stuck-first for the pending card.
 *
 * Unschedulable leads, because it is the state that will not resolve on its
 * own. Within a state, oldest first: a pod pending for an hour is a finding,
 * one pending for ten seconds is a deploy in progress. An unreadable age
 * sorts after every readable one -- it is not evidence of being new, and it
 * must not take the top of a list that means "waiting longest".
 */
export function rankPending(pods: readonly PodHealth[]): PodHealth[] {
  return [...pods].sort((a, b) => {
    const blocked =
      Number(b.state === "unschedulable") - Number(a.state === "unschedulable");
    if (blocked !== 0) return blocked;
    if (a.ageMs !== b.ageMs) {
      if (a.ageMs === null) return 1;
      if (b.ageMs === null) return -1;
      return b.ageMs - a.ageMs;
    }
    return byIdentity(a, b);
  });
}

/** What both views report about the page they were computed over. `total` is
 * the route's own count of the whole population and `counted` is how much of
 * it this page carried, so `truncated` says the card's numbers describe a
 * sample. The list route caps a page at 500 items, and a ranking over the
 * first 500 of 3000 pods is not "the worst pods on the cluster". */
interface PageCoverage {
  total: number;
  counted: number;
  truncated: boolean;
}

function coverage(
  page: ResourceListPage | null | undefined,
  counted: number,
): PageCoverage {
  const total =
    typeof page?.total === "number" && Number.isFinite(page.total)
      ? page.total
      : counted;
  return { total, counted, truncated: total > counted };
}

export interface PendingPodsView extends PageCoverage {
  /** Ranked and capped at the caller's limit. */
  pods: PodHealth[];
  /** Counted across the whole page, not just the capped list, so the card's
   * headline does not shrink when the list does. */
  unschedulable: number;
  pending: number;
}

export function pendingPodsView(
  page: ResourceListPage | null | undefined,
  limit: number,
  now: number = Date.now(),
): PendingPodsView {
  const pods = classifyPods(page, now);
  const waiting = pods.filter(
    (p) => p.state === "pending" || p.state === "unschedulable",
  );
  return {
    ...coverage(page, pods.length),
    pods: rankPending(waiting).slice(0, limit),
    unschedulable: waiting.filter((p) => p.state === "unschedulable").length,
    pending: waiting.filter((p) => p.state === "pending").length,
  };
}

export interface PodRestartsView extends PageCoverage {
  /** Ranked and capped. Pods with no loop and no restarts are left off
   * entirely: the card is a list of things worth looking at, and on a healthy
   * cluster the empty list IS the finding. */
  pods: PodHealth[];
  crashLooping: number;
  /** Restarted at some point but not currently looping. */
  restarting: number;
  totalRestarts: number;
}

export function podRestartsView(
  page: ResourceListPage | null | undefined,
  limit: number,
  now: number = Date.now(),
): PodRestartsView {
  const pods = classifyPods(page, now);
  const notable = pods.filter(
    (p) => p.state === "crash-looping" || p.restarts > 0,
  );
  return {
    ...coverage(page, pods.length),
    pods: rankByRestarts(notable).slice(0, limit),
    crashLooping: pods.filter((p) => p.state === "crash-looping").length,
    restarting: pods.filter(
      (p) => p.state !== "crash-looping" && p.restarts > 0,
    ).length,
    totalRestarts: pods.reduce((sum, p) => sum + p.restarts, 0),
  };
}
