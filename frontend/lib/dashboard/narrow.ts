/**
 * Narrowing helpers shared by every module that turns a backend envelope into
 * something a card renders.
 *
 * These shipped as private copies inside each of those modules. By the time
 * the catalog closed there were ten, and they had stopped agreeing: some read
 * a non-array list as "not a list", others as "an empty list". That is not a
 * style difference. A card built on the second reading prints a clean,
 * confident zero over a payload it could not read -- which is the one failure
 * this release exists to prevent.
 *
 * `page-coverage.ts` set the precedent and the rule: a third copy is where a
 * helper moves to a shared module. This is the tenth.
 *
 * The two list readings both have legitimate uses, so both are here and each
 * is named for what it does. The choice between them is now something a
 * reader can see at the call site instead of something they have to go and
 * look up in whichever private copy this module happened to keep.
 *
 * Pure: no DOM, no fetch, no signals (D-10).
 */

/**
 * A record, or null when the value is not one.
 *
 * `null` is not a record, and neither is an array. The array exclusion is
 * load-bearing rather than pedantic: an envelope reader does
 * `obj(body)?.[field]`, and on a bare array that yields `undefined` -- which
 * every caller then reads as "the field was absent", i.e. an empty result. So
 * a route answering with a naked list instead of its envelope would render as
 * a clean, empty card. Refusing the array makes it unreadable instead, which
 * is what it is.
 *
 * The private copies this replaced had forked on exactly this: the modules
 * that had learned to distinguish unreadable from empty excluded arrays, and
 * the ones that had not, did not. The stricter reading is the shared one.
 */
export function obj(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

/**
 * A string, or "" when the value is not one.
 *
 * The empty string is the right fallback because every consumer treats a name
 * it could not read as a name it does not have, and branches on `=== ""`.
 */
export function str(value: unknown): string {
  return typeof value === "string" ? value : "";
}

/**
 * A finite number, or null.
 *
 * Null rather than a fallback: a replica count we could not read is not zero
 * and not one, and a consumer that sums a guess reports a total that never
 * existed. Callers branch on the null instead.
 */
export function num(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

/**
 * A finite number, or `fallback`.
 *
 * For the callers whose arithmetic genuinely has a correct default -- a
 * desired-replica count that is absent because the field is optional, not
 * because it was unreadable. Prefer `num` and branch; reach for this only
 * when the fallback is part of the contract rather than a way to avoid the
 * branch.
 */
export function numOr(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/**
 * An array, or NULL when the value is not one.
 *
 * This is the contract for a payload's own list -- the `items` of a page, the
 * rows of an envelope. A backend that answers with a null, a missing field or
 * an object where a list belongs has told us it could not give us the list,
 * and that is different in kind from giving us an empty one. Only the caller
 * knows how to say so, which is why this refuses to decide by coercing.
 */
export function list<T = unknown>(value: unknown): T[] | null {
  return Array.isArray(value) ? (value as T[]) : null;
}

/**
 * An array, or an empty one when the value is not one.
 *
 * For a field INSIDE an item that is already known to be readable -- a pod's
 * container statuses, an autoscaler's metrics. One malformed item degrading
 * to "no containers" is the documented behaviour of these modules: it costs
 * the reader one row, where refusing the whole page over it would blank a
 * card describing several hundred readable ones.
 *
 * Never use this for a payload's own list; that is `list`.
 */
export function listOr<T = unknown>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}
