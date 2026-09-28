/**
 * Per-section coverage for the dashboard summary (Release C, D5 / AE3).
 *
 * A remote cluster's summary is assembled from impersonated direct lists, and
 * some of it cannot be assembled at all: there is no remote metrics binding,
 * no remote Alertmanager, and no remote health score. The backend says so one
 * section at a time in `coverage` (`dashboard_remote.go`), and it still fills
 * the numeric fields it could not observe with zeroes -- `alerts: {active: 0}`,
 * `cpu.percentage: 0` -- because those fields are not nullable on the wire.
 *
 * So the zeroes are not data. This module is what the dashboard consults
 * before it prints one: a section whose row is `unavailable` or `forbidden`
 * renders its reason, never its number.
 *
 * A local summary carries no `coverage` at all (`omitempty`), and "no row"
 * means "nothing to disclose": every section renders exactly as it did before
 * coverage existed. The one local exception is cpu/memory usage, whose
 * payload carries its own "not observed" sentinel (see `withheldReason`).
 */
import { durationShort } from "@/lib/format.ts";
import type { HealthSignal } from "@/lib/score-color.ts";
import type { DashboardSummary } from "./wire-types.ts";

/**
 * The sections the backend reports on. Mirrors the `Section` comment on
 * `SectionCoverage` in `dashboard_remote.go`.
 */
export type CoverageSection =
  | "nodes"
  | "pods"
  | "services"
  | "cpu"
  | "memory"
  | "alerts"
  | "health";

/**
 * One section's coverage row, exactly as the backend writes it.
 *
 * `status` and `reasonCode` are typed as plain strings on purpose: they arrive
 * from a server this build does not control, and a newer backend can send a
 * status this build has never seen. `sectionTone` is where that is narrowed,
 * and it narrows an unknown status to the safe answer rather than trusting it.
 */
export interface SectionCoverage {
  section: string;
  status: string;
  reasonCode: string;
  observedAt?: string;
  detail?: string;
}

/**
 * How a section renders.
 *
 * Five tones for five statuses, and they are deliberately not collapsed:
 *
 * - `ok`          the value is shown as-is.
 * - `partial`     the value is shown, flagged as a lower bound -- the list
 *                 behind it was truncated or stopped early.
 * - `stale`       the value is shown with the age of the observation.
 * - `unavailable` the value is withheld and the reason is shown. Something
 *                 k8sCenter cannot read here, or a read that failed.
 * - `forbidden`   the value is withheld because this account may not read
 *                 it. Distinct from `unavailable` because the fix is a
 *                 permission, not a feature or an outage.
 */
export type CoverageTone =
  | "ok"
  | "partial"
  | "stale"
  | "unavailable"
  | "forbidden";

/** The summary as the dashboard receives it once it has opted in. */
export type CoveredSummary = DashboardSummary & {
  coverage?: SectionCoverage[];
};

/**
 * The coverage row for `section`, or null when the summary reports none.
 *
 * Null is the local case and means "render normally". It is not the same as a
 * row saying `ok`, but it renders the same, which is the point: the local path
 * has nothing to disclose.
 */
export function coverageFor(
  summary: CoveredSummary | null | undefined,
  section: CoverageSection,
): SectionCoverage | null {
  const rows = summary?.coverage;
  if (!Array.isArray(rows)) return null;
  return rows.find((r) => r?.section === section) ?? null;
}

/**
 * The render tone for a row.
 *
 * An unrecognised status withholds the value. The alternative -- showing the
 * number beside a status nobody can interpret -- is exactly the manufactured
 * reading this module exists to prevent, and the backend fills unobserved
 * sections with zeroes.
 */
export function sectionTone(cov: SectionCoverage | null): CoverageTone {
  if (cov === null) return "ok";
  switch (cov.status) {
    case "ok":
    case "partial":
    case "stale":
    case "unavailable":
    case "forbidden":
      return cov.status;
    default:
      return "unavailable";
  }
}

/** Whether a tone lets the section's value be shown at all. */
export function isRenderable(tone: CoverageTone): boolean {
  return tone === "ok" || tone === "partial" || tone === "stale";
}

/**
 * Relative age of a row's observation, e.g. "5m ago".
 *
 * `now` is injectable so the arithmetic is testable; components pass nothing.
 * An absent or unparseable timestamp yields null rather than "0s ago", which
 * would claim the reading is brand new.
 */
export function observedAgo(
  cov: SectionCoverage | null,
  now: number = Date.now(),
): string | null {
  const at = cov?.observedAt ? Date.parse(cov.observedAt) : Number.NaN;
  if (!Number.isFinite(at)) return null;
  return `${durationShort(now - at)} ago`;
}

function capitalize(s: string): string {
  return s.length === 0 ? s : s[0].toUpperCase() + s.slice(1);
}

/**
 * What to tell the operator about a section that is not plain `ok`.
 *
 * The backend writes a `detail` on every non-ok row, and it is the most
 * specific thing available ("you do not have permission to list pods across
 * all namespaces on this cluster"), so it wins. The fallbacks cover a row
 * without one, per tone, and never invent specifics.
 */
export function coverageMessage(
  cov: SectionCoverage | null,
  now: number = Date.now(),
): string {
  const tone = sectionTone(cov);
  const detail = cov?.detail?.trim();
  if (tone === "stale") {
    const ago = observedAgo(cov, now);
    const base = detail ? capitalize(detail) : "Last observed value";
    return ago ? `${base} (observed ${ago}).` : `${base}.`;
  }
  if (detail) return `${capitalize(detail)}.`;
  switch (tone) {
    case "ok":
      return "";
    case "partial":
      return "Only part of this cluster could be read, so this is a lower bound.";
    case "forbidden":
      return "Your account does not have permission to read this on this cluster.";
    default:
      return "Not available on this cluster.";
  }
}

/**
 * Why the cpu or memory block carries no observed usage, or null when it does.
 *
 * The local path has no coverage block, so `utilizationFrom` in `dashboard.go`
 * says it in the payload itself, two ways that mean different things:
 *
 * - `{percentage: 0, used: "N/A"}`: capacity is known but no usage was read
 *   (no Prometheus, or a failed or timed-out query).
 * - no block at all: the nodes report no allocatable capacity, so there is
 *   nothing to take a percentage of.
 *
 * Neither is 0%. Mirrors mobile's `Utilization.unavailable`, which also keeps
 * a real zero (`used` is then a quantity) renderable.
 */
function unobservedUsageReason(
  summary: CoveredSummary | null | undefined,
  section: "cpu" | "memory",
): string | null {
  const u = summary?.[section];
  if (!u)
    return "No allocatable capacity is reported for this cluster's nodes.";
  const sentinel =
    u.percentage === 0 &&
    typeof u.used === "string" &&
    u.used.trim().toUpperCase() === "N/A";
  return sentinel
    ? "No usage metrics: Prometheus is not configured or did not answer."
    : null;
}

/**
 * Why `section`'s value must not be shown, or null when it may be.
 *
 * For a card that reads more than the one section its host gates on: each
 * extra section it prints asks here first, and prints an em-dash with this
 * reason instead of the backend's placeholder zero. For cpu and memory that
 * includes a local summary with no usage reading, which no coverage row
 * describes; a coverage row's reason, when there is one, is more specific.
 */
export function withheldReason(
  summary: CoveredSummary | null | undefined,
  section: CoverageSection,
): string | null {
  const cov = coverageFor(summary, section);
  if (!isRenderable(sectionTone(cov))) return coverageMessage(cov);
  if (section === "cpu" || section === "memory") {
    return unobservedUsageReason(summary, section);
  }
  return null;
}

/**
 * A count as a line of prose -- "42 pods" -- with what the reader must know
 * about it before trusting it.
 *
 * `withheldReason` answers only "may this be shown?", which is enough for a
 * bar but not for a sentence: a `partial` section passes it, and printing its
 * total plainly claims a whole cluster that was never read (a list truncated
 * at the page cap, or one whose later page failed -- `listAllRemote`). So:
 *
 * - withheld (`unavailable`, `forbidden`, unknown status): "— pods", note is
 *   the reason. Never the backend's placeholder zero.
 * - `partial`: "≥ 42 pods", note says why it is a lower bound.
 * - `stale`:   "42 pods", note carries the observation age.
 * - `ok`, or no row at all (the local path): "42 pods", note null -- output
 *   identical to the pre-coverage text.
 *
 * `singular` is used only for an exact count of 1; "— nodes" and "≥ 1 node"
 * follow ordinary English.
 */
export function summaryCountLabel(
  summary: CoveredSummary | null | undefined,
  section: CoverageSection,
  count: number,
  plural: string,
  singular: string = plural,
  now: number = Date.now(),
): { text: string; note: string | null } {
  const cov = coverageFor(summary, section);
  const tone = sectionTone(cov);
  if (!isRenderable(tone)) {
    return { text: `— ${plural}`, note: coverageMessage(cov, now) };
  }
  const plain = `${count} ${count === 1 ? singular : plural}`;
  switch (tone) {
    case "partial":
      return { text: `≥ ${plain}`, note: coverageMessage(cov, now) };
    case "stale":
      return { text: plain, note: coverageMessage(cov, now) };
    default:
      return { text: plain, note: null };
  }
}

/** The four signals `computeClusterHealth` weights into the score. */
const WEIGHTED_SIGNALS = ["nodes", "workloads", "pods", "alerts"] as const;

/**
 * The named health signal, or null when the summary carries none.
 *
 * Null is "not reported" (no health block, or an older backend without
 * `signals`), and callers treat it as they treat a missing coverage row:
 * nothing to disclose.
 */
export function healthSignal(
  summary: CoveredSummary | null | undefined,
  name: string,
): HealthSignal | null {
  const signals = summary?.health?.signals;
  if (!Array.isArray(signals)) return null;
  return signals.find((sig) => sig?.name === name) ?? null;
}

/**
 * The first weighted signal the backend could not read, or null.
 *
 * `unknown` is a read that failed -- missing RBAC, an informer still syncing,
 * an Alertmanager query error (`dashboard.go`). `computeClusterHealth` drops
 * it and renormalizes the remaining weights, so the score it returns is a
 * confident number over an input that was never observed: 100% alerts-free
 * because the alerts query errored.
 *
 * `skipped` is deliberately NOT in this set. The backend skips a signal only
 * when there is nothing to evaluate -- no desired workloads, no Running or
 * Pending pods, no Alertmanager configured (`health.go`) -- and renormalizing
 * over the rest is the intended scoring for such a cluster, not a gap in it.
 */
function unresolvedWeightedSignal(
  summary: CoveredSummary | null | undefined,
): HealthSignal | null {
  for (const name of WEIGHTED_SIGNALS) {
    const sig = healthSignal(summary, name);
    if (sig?.status === "unknown") return sig;
  }
  return null;
}

/**
 * Whether the health score may be drawn.
 *
 * False when health is absent, when its score is null (the backend's "no
 * weighted signal resolved" answer, `computeClusterHealth` returning
 * `Status: unknown, Score: nil`), when any weighted signal is `unknown` (see
 * `unresolvedWeightedSignal`), or when the health row is anything but a plain
 * `ok`. Health is a composite: a `partial` or `stale` score is a score
 * computed from inputs that are known to be incomplete, which is precisely
 * the synthesised reading AE3 forbids. The remote path always reports health
 * `unavailable` in v1.
 */
export function shouldRenderHealth(
  summary: CoveredSummary | null | undefined,
): boolean {
  const score = summary?.health?.score;
  if (typeof score !== "number" || !Number.isFinite(score)) return false;
  if (unresolvedWeightedSignal(summary) !== null) return false;
  const cov = coverageFor(summary, "health");
  return cov === null || sectionTone(cov) === "ok";
}

/**
 * Why no score is drawn, most specific first: the health coverage row (the
 * remote path), then the weighted signal that could not be read, then the
 * generic "too few signals" for a null score with nothing more to say.
 */
export function healthUnscoredReason(
  summary: CoveredSummary | null | undefined,
): string {
  const cov = coverageFor(summary, "health");
  if (cov !== null && sectionTone(cov) !== "ok") return coverageMessage(cov);
  const sig = unresolvedWeightedSignal(summary);
  if (sig !== null) {
    const why = sig.reason?.trim();
    return why
      ? `The ${sig.name} signal could not be read (${why}), so no score is shown.`
      : `The ${sig.name} signal could not be read, so no score is shown.`;
  }
  return "Too few health signals resolved to score this cluster.";
}

/**
 * The health score as text: the number when `shouldRenderHealth` allows it,
 * an em-dash otherwise. Never "0" for a score that does not exist.
 */
export function formatHealthScore(
  summary: CoveredSummary | null | undefined,
): string {
  return shouldRenderHealth(summary) ? String(summary?.health?.score) : "—";
}

/**
 * The summary section each summary-backed widget headlines.
 *
 * A widget listed here is replaced by its section's reason when that section
 * is not renderable, and flagged when it is partial or stale -- in one place,
 * `WidgetHost`, rather than in each widget body. A widget that reads several
 * sections (nodes, cluster-health) is listed under the one its card cannot
 * exist without, and masks the others itself.
 *
 * Every widget whose sources include `dashboard-summary` must appear here;
 * coverage_test.ts fails when one does not, because an unlisted widget would
 * print the backend's placeholder zeroes on a remote cluster.
 */
export const WIDGET_SECTIONS: Readonly<Record<string, CoverageSection>> = {
  "cluster-health": "health",
  "cpu-tile": "cpu",
  "memory-tile": "memory",
  "pods-tile": "pods",
  "pod-status": "pods",
  "active-alerts": "alerts",
  nodes: "nodes",
};

/** The coverage row that gates `widgetId`, or null when nothing gates it. */
export function widgetCoverage(
  widgetId: string,
  summary: CoveredSummary | null | undefined,
): SectionCoverage | null {
  const section = WIDGET_SECTIONS[widgetId];
  return section === undefined ? null : coverageFor(summary, section);
}
