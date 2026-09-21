/**
 * Which of five outcomes a widget box shows, and why.
 *
 * `WidgetHost` used to decide this inline across three booleans. Two more
 * outcomes -- "the cluster does not run this feature" and "this account may
 * not read it" -- turn that into a decision worth a test: the difference
 * between an operator reading "cert-manager is not installed" and reading an
 * empty, healthy-looking card about certificates that do not exist is the
 * whole point of R1 and R2. This repo has no component test harness (D-10), so
 * the decision lives here and the shell only renders what this returns.
 *
 * Pure: no DOM, no fetch, no signals. The source states arrive through an
 * injected lookup rather than by reading the live cache.
 */
import type { SourceState } from "./data.ts";
import { sourceKeyFor } from "./params.ts";
import type { DataSourceKey, WidgetDef } from "./types.ts";

/**
 * The five outcomes, and what each one claims:
 *
 * - `loading` -- nothing is known yet.
 * - `permission` -- this account may not read what the widget needs. Standing,
 *   not transient, so the shell offers no retry.
 * - `error` -- a read failed. Transient as far as anyone here can tell.
 * - `unavailable` -- the feature is not installed on this cluster. Not a
 *   failure and not a delay: nothing is wrong and nothing is coming.
 * - `ready` -- the widget renders, and says whatever its own data says,
 *   including "nothing to report".
 */
export type WidgetState =
  | "loading"
  | "permission"
  | "error"
  | "unavailable"
  | "ready";

export interface WidgetResolution {
  state: WidgetState;
  /**
   * The failed source whose message the error or permission card shows, and
   * null in every other state.
   */
  blocking: SourceState | null;
  /**
   * A source that failed but still holds data from an earlier fetch, which is
   * what the stale notice above a rendered widget reports. Non-null only in
   * the `ready` state -- nothing is rendered for it to sit above otherwise.
   */
  stale: SourceState | null;
}

/** The part of a widget definition this module reads. Narrowed so a test can
 * build one without a `render`, and so nothing here can start depending on the
 * rest of the definition. */
export type WidgetSourceDecl = Pick<WidgetDef, "sources"> &
  Partial<Pick<WidgetDef, "optionalSources" | "familyStatus">>;

/**
 * Every key the widget needs fetched, including its family status.
 *
 * The family status is not listed in `sources` by the widget author -- the
 * point of the declaration is that the widget does not know about it -- so the
 * consumer that drives the cache has to add it here rather than reading
 * `def.sources` directly. Duplicates are dropped so a widget that names the
 * key both ways does not request it twice.
 */
export function sourcesOf(def: WidgetSourceDecl): DataSourceKey[] {
  const keys = [...def.sources];
  if (def.familyStatus !== undefined && !keys.includes(def.familyStatus)) {
    keys.push(def.familyStatus);
  }
  return keys;
}

/**
 * Whether a family status payload reports its feature installed.
 *
 * Three payload shapes across eight families, one field. The boolean families
 * (cert-manager, External Secrets, Velero, and volume snapshots, whose
 * `metadata.available` flag the fetcher normalises into this shape) report
 * absence as `detected: false`; the string families (policy, GitOps, service
 * mesh, scanning) report it as
 * `detected: ""` and otherwise name which implementation was found --
 * "kyverno", "fluxcd", "both". Anything else counts as present.
 *
 * An unreadable payload counts as ABSENT, which is the deliberate direction:
 * the Definition of Done forbids rendering an absent feature as a healthy one,
 * and a widget that says "not installed" when the status route changed shape
 * is a visible bug someone fixes, where one that renders an empty green card
 * is a bug nobody sees.
 */
export function featurePresent(data: unknown): boolean {
  if (data === null || typeof data !== "object") return false;
  const detected = (data as { detected?: unknown }).detected;
  return (
    detected !== undefined &&
    detected !== null &&
    detected !== false &&
    detected !== ""
  );
}

/**
 * Resolves one widget's outcome from the state of the sources it declared.
 *
 * The order, and why it is this one:
 *
 * 1. **Everything required has landed** -- the widget can render. This is
 *    checked first because stale data outranks an error: the cache keeps the
 *    last good value across a failed refresh, so a transient 500 leaves a
 *    working widget on screen with an inline notice rather than replacing it
 *    with an error card. That is the view an operator needs most while a
 *    cluster is degrading, and it is the behavior that shipped.
 *    1a. A declared family that reports its feature absent stops here:
 *        `unavailable`, and `render` is never called. The widget therefore
 *        never learns it is unavailable and never has to decide whether its
 *        own empty list means "nothing to report" or "no operator installed"
 *        -- which it cannot decide, because the backend returns 200 with an
 *        empty array either way (KTD1).
 * 2. **A required source failed** -- and a forbidden one outranks any other
 *    failure, because the two states differ in what they offer the user: a
 *    retry on a 403 is a lie, and the state that carries none has to win when
 *    both land together.
 * 3. Otherwise nothing is known yet: the skeleton. Idle (never requested --
 *    the consumer calls `ensure`, not this) and in flight both land here, so
 *    they never need distinguishing.
 *
 * A source the widget declared optional does not gate it, in any of the above:
 * gating on every declared source made each one another way for the widget to
 * disappear -- a slow or failed trend request blanking a percentage the
 * summary endpoint already returned. A family status is never optional; it is
 * required whatever `optionalSources` says, since a widget rendered before its
 * family status has landed is the case the declaration exists to prevent.
 */
export function resolveWidgetState(
  def: WidgetSourceDecl,
  stateOf: (key: string) => SourceState,
  params: Readonly<Record<string, string>> = {},
): WidgetResolution {
  const optional = def.optionalSources ?? [];
  const declared = sourcesOf(def);
  // A source is DECLARED by name and READ under a key. For everything that
  // takes no parameters the two are the same string, which is why every
  // caller that predates parameters still resolves correctly. For a
  // parameterized one they differ, and looking up the declared name would
  // read a key nothing ever fetched -- idle, so the widget would sit in the
  // skeleton for good rather than failing visibly.
  //
  // The optional and family-status comparisons stay on the declared names,
  // because that is what a widget author writes.
  const keys = declared.map((k) => ({
    declared: k,
    key: sourceKeyFor(k, params),
  }));
  const required = keys.filter(
    (k) => k.declared === def.familyStatus || !optional.includes(k.declared),
  );
  const requiredStates = required.map((k) => stateOf(k.key));

  // Asked before the every-source-landed gate below, and deliberately so.
  //
  // This used to sit inside that gate, which silently assumed an absent
  // feature's own data route still answers -- 200 with an empty list, the way
  // a CRD-discovered list handler does. Two routes in this catalog do not:
  // the Hubble flows route answers 503 when Hubble is absent, and the mesh
  // golden-signals route answers 400 when no mesh is detected. On exactly the
  // clusters the unavailable state exists for, those widgets never reached it
  // -- their required source errored, so they fell through to the error card,
  // in warning colour, quoting the backend, offering a retry that could never
  // succeed. That is this release's own rule inverted.
  //
  // Once the family status itself has landed and reports the feature absent,
  // no other source can change the answer: there is nothing to load, and
  // whatever its data route said is a symptom of the same absence.
  if (def.familyStatus !== undefined) {
    const family = stateOf(sourceKeyFor(def.familyStatus, params));
    if (family.data !== null && !featurePresent(family.data)) {
      return { state: "unavailable", blocking: null, stale: null };
    }
  }

  if (requiredStates.every((s) => s.data !== null)) {
    // The notice claims last-known data, so it appears only when the failed
    // source still holds some: an optional source that failed before ever
    // loading has nothing stale to show, and the widget renders without it.
    const stale =
      keys
        .map((k) => stateOf(k.key))
        .find((s) => s.error !== null && s.data !== null) ?? null;
    return { state: "ready", blocking: null, stale };
  }

  const refused = requiredStates.find((s) => s.errorKind === "permission");
  if (refused) {
    return { state: "permission", blocking: refused, stale: null };
  }

  const failed = requiredStates.find((s) => s.error !== null);
  if (failed) {
    return { state: "error", blocking: failed, stale: null };
  }

  return { state: "loading", blocking: null, stale: null };
}
