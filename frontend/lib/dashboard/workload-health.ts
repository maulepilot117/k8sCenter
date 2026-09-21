/**
 * The controller roll-up behind the workload-health widget.
 *
 * Two payloads meet here and they answer different questions.
 *
 * `GET /v1/resources/counts` answers "which controller kinds may this account
 * list, and how many of each exist". It is the only route that answers the
 * first half: a kind the caller cannot list is OMITTED from its map rather
 * than zeroed, so absence in that payload is a fact about the account, not
 * about the cluster. The three list reads answer "how many of those are
 * ready", and each of them is optional -- a kind whose list has not landed, or
 * came back refused, leaves its row unanswered without blanking the other two.
 *
 * Folding the two together is more than a field read, so it is pure and lives
 * here rather than inside the component (D-10, KTD8).
 *
 * The direction this module errs in is the whole point. A kind it cannot
 * answer for reports `unknown`, never "0 of N ready": rendering an unseeable
 * kind as fully ready is the "absence as good news" the release exists to
 * prevent, and rendering it as fully degraded is a false alarm about workloads
 * nobody here can even look at. Neither is acceptable, so the third state is
 * explicit and the card prints it.
 */

/** The three controller kinds this widget rolls up, in the order the card
 * renders them. Spelled as the counts route spells them -- lowercase plural --
 * because those strings are the map keys. */
export const WORKLOAD_KINDS = [
  "deployments",
  "statefulsets",
  "daemonsets",
] as const;
export type WorkloadKind = (typeof WORKLOAD_KINDS)[number];

/** Display names, which are not the wire spelling. */
export const WORKLOAD_KIND_LABELS: Readonly<Record<WorkloadKind, string>> = {
  deployments: "Deployments",
  statefulsets: "StatefulSets",
  daemonsets: "DaemonSets",
};

/** The full page each row links to (R7). Declared beside the kinds so a row
 * cannot be added without one. */
export const WORKLOAD_KIND_HREFS: Readonly<Record<WorkloadKind, string>> = {
  deployments: "/workloads/deployments",
  statefulsets: "/workloads/statefulsets",
  daemonsets: "/workloads/daemonsets",
};

/**
 * How much is known about one kind.
 *
 * - `unknown` -- the counts payload did not name it. Not visible to this
 *   account, so neither its size nor its health is knowable here.
 * - `pending` -- counts named it, so the size is known, but its list has not
 *   answered yet (still in flight, or failed). Ready-versus-degraded is not
 *   knowable YET, which is a different claim from not knowable at all.
 * - `counted` -- both answered, and the split below is real.
 */
export type WorkloadKindVisibility = "unknown" | "pending" | "counted";

export interface WorkloadKindHealth {
  kind: WorkloadKind;
  visibility: WorkloadKindVisibility;
  /** How many exist, as the counts route reported. Zero when `unknown`. */
  total: number;
  /** How many the list actually returned, which is what `ready` and
   * `degraded` were computed over. */
  counted: number;
  ready: number;
  degraded: number;
  /** `total` exceeds `counted`: the list route's page cap cut the sample
   * short, so the split below describes part of the kind, not all of it. */
  truncated: boolean;
}

export interface WorkloadHealthRollUp {
  /** One entry per kind, always three, always in `WORKLOAD_KINDS` order. */
  kinds: WorkloadKindHealth[];
  /** Summed over `counted` kinds only. A kind nobody can answer for
   * contributes to neither side. */
  ready: number;
  degraded: number;
  total: number;
  /** The kinds the counts payload omitted, in declared order. The card names
   * them rather than leaving three blank rows to be read as zeros. */
  unknownKinds: WorkloadKind[];
  /** Any kind's sample was cut short by the list route's page cap. */
  truncated: boolean;
  /**
   * Every kind that IS visible holds nothing.
   *
   * Kept apart from "all ready" deliberately, for the reason the diagnostics
   * roll-up keeps it apart: zero of zero ready renders as a clean bill of
   * health, and a cluster with no workloads on it is not a healthy cluster,
   * it is an empty one. False when nothing is visible at all, because then
   * emptiness is not something we know either.
   */
  empty: boolean;
}

/** Narrow an unknown to a readable object without asserting its shape. */
function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : null;
}

/** A finite number, or `fallback`. Rejects NaN and Infinity as well as the
 * wrong type: both arrive from a body this build cannot read, and both would
 * poison every sum they touch. */
function num(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/**
 * Whether one controller object has as many ready replicas as it wants.
 *
 * Two field pairs, because a DaemonSet has no `spec.replicas`: its size is
 * however many nodes match its selector, reported in
 * `status.desiredNumberScheduled`. Reading `spec.replicas` for one would make
 * every DaemonSet on the cluster look like a one-replica workload with nothing
 * ready.
 *
 * Two judgements worth stating:
 *
 * - A workload wanting zero replicas is READY. It is deliberately scaled down
 *   (or, for a DaemonSet, matches no node), so it is in the state somebody
 *   asked for. Calling that degraded would put a permanent red count on every
 *   suspended workload.
 * - A workload with no readable status is DEGRADED. Nothing says its replicas
 *   are up, and for a kind the caller CAN list, "we could not tell" must not
 *   round to good news. The kinds we genuinely cannot tell about are the ones
 *   counts omitted, and those never reach this function.
 */
export function workloadReady(kind: WorkloadKind, value: unknown): boolean {
  const o = obj(value);
  if (o === null) return false;
  const status = obj(o.status) ?? {};

  if (kind === "daemonsets") {
    const desired = num(status.desiredNumberScheduled, 0);
    if (desired <= 0) return true;
    return num(status.numberReady, 0) >= desired;
  }

  const spec = obj(o.spec) ?? {};
  // Kubernetes defaults an omitted spec.replicas to 1, so the fallback is 1
  // and not 0: defaulting to 0 would make every default-sized Deployment read
  // as ready before it had a single pod.
  const desired = num(spec.replicas, 1);
  if (desired <= 0) return true;
  return num(status.readyReplicas, 0) >= desired;
}

const EMPTY_KIND = (kind: WorkloadKind): WorkloadKindHealth => ({
  kind,
  visibility: "unknown",
  total: 0,
  counted: 0,
  ready: 0,
  degraded: 0,
  truncated: false,
});

/**
 * Folds the counts map and the three list payloads into the rows the card
 * renders.
 *
 * `counts` is the authority on visibility AND on totals; the lists are the
 * authority on readiness only. That split is why the totals are not simply
 * `items.length`: the list route caps a page at 500 items while counts reports
 * the whole population, so on a large cluster `items.length` is a sample and
 * counts is the truth. Reporting the sample size as the total would quietly
 * shrink a 3000-Deployment cluster to 500 and make a real degraded count read
 * as a much larger share of it than it is.
 *
 * Total in the other sense too: every argument may be null, missing or the
 * wrong shape, and none of that throws. The card renders whatever this
 * returns, and a body nobody can parse resolves to "unknown", which is the
 * one honest thing to say about it.
 */
export function rollUpWorkloadHealth(
  counts: Readonly<Record<string, number>> | null | undefined,
  lists: Readonly<Partial<Record<WorkloadKind, readonly unknown[] | null>>>,
): WorkloadHealthRollUp {
  const countMap = obj(counts);

  const kinds = WORKLOAD_KINDS.map((kind): WorkloadKindHealth => {
    const raw = countMap?.[kind];
    // A key whose value is not a finite number is a body this build cannot
    // read, which is not evidence about the cluster: treat it as absent.
    if (typeof raw !== "number" || !Number.isFinite(raw)) {
      return EMPTY_KIND(kind);
    }
    const total = Math.max(0, Math.trunc(raw));

    const items = lists[kind];
    if (!Array.isArray(items)) {
      // Visible and sized, readiness not in yet.
      return { ...EMPTY_KIND(kind), visibility: "pending", total };
    }

    let ready = 0;
    for (const item of items) {
      if (workloadReady(kind, item)) ready++;
    }
    return {
      kind,
      visibility: "counted",
      total,
      counted: items.length,
      ready,
      degraded: items.length - ready,
      truncated: total > items.length,
    };
  });

  const counted = kinds.filter((k) => k.visibility === "counted");
  const visible = kinds.filter((k) => k.visibility !== "unknown");

  return {
    kinds,
    ready: counted.reduce((sum, k) => sum + k.ready, 0),
    degraded: counted.reduce((sum, k) => sum + k.degraded, 0),
    total: visible.reduce((sum, k) => sum + k.total, 0),
    unknownKinds: kinds
      .filter((k) => k.visibility === "unknown")
      .map((k) => k.kind),
    truncated: kinds.some((k) => k.truncated),
    // At least one kind is visible AND every visible kind holds nothing. The
    // first half matters: with nothing visible there is no cluster state to
    // call empty, only an account that cannot see one.
    empty: visible.length > 0 && visible.every((k) => k.total === 0),
  };
}
