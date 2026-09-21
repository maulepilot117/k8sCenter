/**
 * The derived state behind the `hpa-status` and `pdb-risk` widgets.
 *
 * Both read raw Kubernetes objects off the generic list route and both have to
 * answer a question no field on the object carries. An HPA's numbers say
 * `10/10`, which is what a healthy autoscaler sized to its ceiling and one
 * that has run out of room both print; only the metric tells them apart. A
 * PDB's `disruptionsAllowed: 0` is either the thing that will block the next
 * node drain or a budget whose selector matches nothing at all. Neither
 * judgement is a field read, so both live here under test rather than inside
 * the components (D-10, KTD8).
 *
 * The direction this module errs in is the same one `rollUpWorkloadHealth`
 * errs in, for the same reason. An autoscaler whose metrics have not been
 * reported is `unknown`, never a zero that renders as headroom; a budget whose
 * status cannot be read is `unknown`, never a healthy one. Absence must not
 * render as good news.
 *
 * Total, in the sense the pod classifier is total: every argument may be null,
 * missing or the wrong shape, and none of it throws. One unreadable item
 * degrades to `unknown` rather than blanking a card describing several hundred
 * readable ones.
 */
import type { PageCoverage } from "./page-coverage.ts";
import { coverage } from "./page-coverage.ts";
import type { ResourceListPage } from "./wire-types.ts";

/** The full pages these widgets link to (R7). The route kind is `hpas` and
 * `pdbs` -- the adapters' `Kind()`, which is NOT the `horizontalpodautoscalers`
 * spelling the counts route and the RBAC checks use. */
export const HPA_LIST_HREF = "/scaling/hpas";
export const PDB_LIST_HREF = "/scaling/pdbs";

/** A row's link to its own object. Both segments are encoded: they are the
 * only place a name off the wire becomes a URL, and a value with a slash in it
 * would otherwise address a different route entirely. */
export function hpaHref(namespace: string, name: string): string {
  return `${HPA_LIST_HREF}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`;
}

export function pdbHref(namespace: string, name: string): string {
  return `${PDB_LIST_HREF}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`;
}

// --- shared narrowing ------------------------------------------------------

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

/**
 * A finite number, or null.
 *
 * Null rather than a fallback, everywhere in this module: a replica count we
 * could not read is not zero and not one, and every consumer below branches on
 * the difference instead of summing a guess.
 */
function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function itemsOf(page: ResourceListPage | null | undefined): unknown[] {
  if (page === null || page === undefined) return [];
  return arr(page.items);
}

/** Namespace then name, so a tie orders identically on every refresh rather
 * than following whatever order the informer happened to return. */
function byIdentity(
  a: { namespace: string; name: string },
  b: { namespace: string; name: string },
): number {
  return a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name);
}

// --- HorizontalPodAutoscaler ----------------------------------------------

/**
 * Whether the autoscaler is being asked for more than it is delivering.
 *
 * `unknown` is not a failure state. An HPA driven by a Pods, Object or
 * External metric carries a quantity string rather than a percent, and the
 * metrics server not having reported yet leaves `status.currentMetrics` empty
 * on a perfectly healthy autoscaler. Both are "we cannot say", and saying
 * nothing is the only honest rendering of them.
 */
export type HPADemand = "above" | "met" | "unknown";

/**
 * What the card flags, in the order it ranks.
 *
 * - `saturated` -- at its ceiling AND demand is above target. The only state
 *   that is a finding: the autoscaler has asked for everything it is allowed
 *   and the load is still not served, so somebody has to raise `maxReplicas`
 *   or fix the workload. This is the state R8 exists to surface.
 * - `at-ceiling` -- at its ceiling and coping, or at its ceiling with nothing
 *   to measure. Informational: an autoscaler sized to its maximum is a normal
 *   steady state, and flagging it would put a permanent warning on every
 *   workload whose ceiling is its usual size.
 * - `scaling` -- desired and current disagree; pods are coming up or going
 *   away. Transient by definition.
 * - `steady` -- below the ceiling, settled.
 * - `unknown` -- the object could not be read far enough to say any of the
 *   above. Never rendered as any of them.
 */
export type HPAState =
  | "saturated"
  | "at-ceiling"
  | "scaling"
  | "steady"
  | "unknown";

export interface HPAStatus {
  name: string;
  namespace: string;
  /** `Kind/name` of the scale target, or "" when it is unreadable. */
  target: string;
  current: number | null;
  desired: number | null;
  min: number | null;
  max: number | null;
  /** Desired replicas have reached `maxReplicas`. False whenever either is
   * unreadable -- a ceiling we cannot see is not one we can be at. */
  atCeiling: boolean;
  demand: HPADemand;
  /**
   * The utilization of the metric furthest over its target, as a percent, or
   * null when no metric pair is readable. Null renders as an em-dash; it must
   * never render as 0, which on this card reads as idle.
   */
  utilization: number | null;
  /** The target `utilization` is measured against, and null with it. */
  targetUtilization: number | null;
  state: HPAState;
}

const UNREADABLE_HPA: HPAStatus = {
  name: "",
  namespace: "",
  target: "",
  current: null,
  desired: null,
  min: null,
  max: null,
  atCeiling: false,
  demand: "unknown",
  utilization: null,
  targetUtilization: null,
  state: "unknown",
};

/**
 * A stable key for one metric entry, so spec and status can be paired by what
 * the metric IS rather than by where it sits in an array.
 *
 * Kubernetes does not promise that `spec.metrics` and `status.currentMetrics`
 * are ordered together, and pairing by index on a two-metric HPA compares the
 * memory reading against the CPU target -- which produces a confident, wrong
 * "above target" on an autoscaler that is fine. Anything without a utilization
 * percent returns "", which the caller skips.
 */
function metricKey(entry: unknown): string {
  const o = obj(entry);
  if (o === null) return "";
  const type = str(o.type);
  if (type === "Resource") {
    const name = str(obj(o.resource)?.name);
    return name === "" ? "" : `resource:${name}`;
  }
  if (type === "ContainerResource") {
    const cr = obj(o.containerResource);
    const name = str(cr?.name);
    const container = str(cr?.container);
    return name === "" ? "" : `containerResource:${name}:${container}`;
  }
  // Pods, Object and External metrics are quantities, not percentages. There
  // is no unit to compare them in without guessing, so they contribute
  // nothing rather than a number the card would have to label wrongly.
  return "";
}

/** The `averageUtilization` under whichever sub-object a metric entry uses. */
function utilizationOf(
  entry: unknown,
  field: "target" | "current",
): number | null {
  const o = obj(entry);
  if (o === null) return null;
  const inner = obj(o.resource) ?? obj(o.containerResource);
  return num(obj(inner?.[field])?.averageUtilization);
}

/**
 * The worst readable (current, target) utilization pair on the autoscaler.
 *
 * "Worst" is the largest current-over-target ratio, not the largest raw
 * percentage: an HPA targeting 50% memory at 90% is further past its target
 * than one targeting 95% CPU at 96%, and it is the ratio that decides whether
 * the autoscaler wants more replicas.
 */
function worstMetric(
  value: Record<string, unknown>,
): { current: number; target: number } | null {
  const targets = new Map<string, number>();
  for (const m of arr(obj(value.spec)?.metrics)) {
    const key = metricKey(m);
    const target = utilizationOf(m, "target");
    // A target of zero would divide to Infinity below and is not a target any
    // HPA can be configured with.
    if (key !== "" && target !== null && target > 0) targets.set(key, target);
  }
  if (targets.size === 0) return null;

  let worst: { current: number; target: number } | null = null;
  for (const m of arr(obj(value.status)?.currentMetrics)) {
    const key = metricKey(m);
    const target = targets.get(key);
    const current = utilizationOf(m, "current");
    if (target === undefined || current === null) continue;
    if (worst === null || current / target > worst.current / worst.target) {
      worst = { current, target };
    }
  }
  return worst;
}

/**
 * One HorizontalPodAutoscaler object to the fields the card ranks and renders.
 *
 * The ceiling is judged on `status.desiredReplicas`, not `currentReplicas`:
 * desired is what the controller decided this cycle, and current lags it while
 * pods start. An autoscaler that has just asked for its maximum is out of room
 * whether or not the pods are up yet, and waiting for `current` to catch up
 * would hide the finding for exactly as long as the incident is youngest.
 */
export function classifyHPA(value: unknown): HPAStatus {
  const o = obj(value);
  if (o === null) return UNREADABLE_HPA;

  const meta = obj(o.metadata) ?? {};
  const spec = obj(o.spec) ?? {};
  const status = obj(o.status) ?? {};
  const ref = obj(spec.scaleTargetRef) ?? {};

  const current = num(status.currentReplicas);
  const desiredRaw = num(status.desiredReplicas);
  // Fall back to current so an HPA whose status omits desiredReplicas still
  // gets a ceiling verdict instead of sitting in `unknown`.
  const desired = desiredRaw ?? current;
  const max = num(spec.maxReplicas);

  const worst = worstMetric(o);
  const demand: HPADemand =
    worst === null ? "unknown" : worst.current > worst.target ? "above" : "met";

  const refKind = str(ref.kind);
  const refName = str(ref.name);

  const base: HPAStatus = {
    name: str(meta.name),
    namespace: str(meta.namespace),
    target: refKind !== "" && refName !== "" ? `${refKind}/${refName}` : "",
    current,
    desired,
    min: num(spec.minReplicas),
    max,
    atCeiling: max !== null && max > 0 && desired !== null && desired >= max,
    demand,
    utilization: worst?.current ?? null,
    targetUtilization: worst?.target ?? null,
    state: "unknown",
  };

  // Nothing below can be claimed without both numbers. An autoscaler whose
  // replica count or ceiling is unreadable gets `unknown` and the card prints
  // it, rather than a row of dashes that looks settled.
  if (current === null || max === null) return base;

  if (base.atCeiling) {
    return { ...base, state: demand === "above" ? "saturated" : "at-ceiling" };
  }
  if (desired !== null && desired !== current) {
    return { ...base, state: "scaling" };
  }
  return { ...base, state: "steady" };
}

/** Worst first. The order is the judgement: the only actionable state leads,
 * the informational ceiling follows it, and everything settled sinks. */
const HPA_RANK: Readonly<Record<HPAState, number>> = {
  saturated: 0,
  "at-ceiling": 1,
  scaling: 2,
  unknown: 3,
  steady: 4,
};

/** Returns a new array; the input is not reordered. */
export function rankHPAs(hpas: readonly HPAStatus[]): HPAStatus[] {
  return [...hpas].sort(
    (a, b) => HPA_RANK[a.state] - HPA_RANK[b.state] || byIdentity(a, b),
  );
}

export interface HPAStatusView extends PageCoverage {
  /** Ranked and capped at the caller's limit. */
  hpas: HPAStatus[];
  /** Counted across the whole page, not the capped list, so the headline does
   * not shrink when the list does. */
  saturated: number;
  /** At the ceiling and NOT saturated -- the informational half. The two are
   * disjoint so the header can add them without double-counting. */
  atCeiling: number;
  unknown: number;
}

export function hpaStatusView(
  page: ResourceListPage | null | undefined,
  limit: number,
): HPAStatusView {
  const hpas = itemsOf(page).map(classifyHPA);
  return {
    ...coverage(page, hpas.length),
    hpas: rankHPAs(hpas).slice(0, limit),
    saturated: hpas.filter((h) => h.state === "saturated").length,
    atCeiling: hpas.filter((h) => h.state === "at-ceiling").length,
    unknown: hpas.filter((h) => h.state === "unknown").length,
  };
}

// --- PodDisruptionBudget ---------------------------------------------------

/**
 * What a budget is doing to the next node drain.
 *
 * - `blocked` -- it covers pods and allows no disruption. The eviction API
 *   refuses every one of them, so a `kubectl drain` on any node running them
 *   hangs. This is the finding.
 * - `no-pods` -- it allows no disruption because its selector matches nothing.
 *   Informational, not a risk: there is nothing to evict. Filing it under
 *   "blocks a drain" would raise a false alarm on every budget whose workload
 *   was removed or whose selector drifted -- and those are common.
 * - `healthy` -- at least one disruption is allowed.
 * - `unknown` -- the status could not be read. Never rendered as healthy.
 */
export type PDBState = "blocked" | "no-pods" | "healthy" | "unknown";

export interface PDBStatus {
  name: string;
  namespace: string;
  disruptionsAllowed: number | null;
  currentHealthy: number | null;
  desiredHealthy: number | null;
  expectedPods: number | null;
  state: PDBState;
}

const UNREADABLE_PDB: PDBStatus = {
  name: "",
  namespace: "",
  disruptionsAllowed: null,
  currentHealthy: null,
  desiredHealthy: null,
  expectedPods: null,
  state: "unknown",
};

export function classifyPDB(value: unknown): PDBStatus {
  const o = obj(value);
  if (o === null) return UNREADABLE_PDB;

  const meta = obj(o.metadata) ?? {};
  const status = obj(o.status) ?? {};

  // policy/v1beta1 spelled the field with a leading capital. The informer is
  // pinned to policy/v1, so the lowercase spelling is what arrives today --
  // but a payload carrying only the old one is a budget we CAN read, and
  // filing it under `unknown` would be a worse answer than reading it.
  const allowed =
    num(status.disruptionsAllowed) ?? num(status.PodDisruptionsAllowed);
  const expected = num(status.expectedPods);

  const base: PDBStatus = {
    name: str(meta.name),
    namespace: str(meta.namespace),
    disruptionsAllowed: allowed,
    currentHealthy: num(status.currentHealthy),
    desiredHealthy: num(status.desiredHealthy),
    expectedPods: expected,
    state: "unknown",
  };

  if (allowed === null) return base;
  if (allowed > 0) return { ...base, state: "healthy" };
  // Zero allowed. Which of the two zeros it is depends on whether the budget
  // covers anything at all, and `expectedPods` is the only field that says.
  // An unreadable `expectedPods` alongside a zero allowance is treated as
  // blocking, because that is the direction that does not hide a drain
  // blocker behind a missing field.
  return { ...base, state: expected === 0 ? "no-pods" : "blocked" };
}

const PDB_RANK: Readonly<Record<PDBState, number>> = {
  blocked: 0,
  unknown: 1,
  "no-pods": 2,
  healthy: 3,
};

/** Returns a new array; the input is not reordered. */
export function rankPDBs(pdbs: readonly PDBStatus[]): PDBStatus[] {
  return [...pdbs].sort(
    (a, b) => PDB_RANK[a.state] - PDB_RANK[b.state] || byIdentity(a, b),
  );
}

export interface PDBRiskView extends PageCoverage {
  /**
   * Ranked and capped, and NOT every budget on the cluster: a healthy one has
   * nothing for anyone to do, and listing all of them turns a findings card
   * into an inventory that happens to be sorted. The header's counts cover
   * the whole page, so the healthy ones are still reported -- just not row by
   * row.
   */
  pdbs: PDBStatus[];
  blocked: number;
  noPods: number;
  unknown: number;
  healthy: number;
}

export function pdbRiskView(
  page: ResourceListPage | null | undefined,
  limit: number,
): PDBRiskView {
  const pdbs = itemsOf(page).map(classifyPDB);
  const notable = pdbs.filter((p) => p.state !== "healthy");
  return {
    ...coverage(page, pdbs.length),
    pdbs: rankPDBs(notable).slice(0, limit),
    blocked: pdbs.filter((p) => p.state === "blocked").length,
    noPods: pdbs.filter((p) => p.state === "no-pods").length,
    unknown: pdbs.filter((p) => p.state === "unknown").length,
    healthy: pdbs.filter((p) => p.state === "healthy").length,
  };
}
