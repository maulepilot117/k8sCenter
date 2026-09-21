/**
 * Widget parameters: what a value has to satisfy, what makes two placements
 * the same, and the cache key a parameterized read is issued under.
 *
 * This module is pure: no DOM, no fetch, no signals (D-10). `WidgetParamDialog`
 * is a component and therefore untestable in this repo, and every rule it
 * enforces is a rule the server enforces too -- so the rules live here, under
 * test, and the dialog only renders what they answer.
 *
 * Mirroring is the whole point. Each bound below has a twin in
 * `ValidateDashboardLayout` (backend/internal/preferences/dashboard.go), and
 * the two exist so the dialog can refuse a value the server would refuse
 * BEFORE the round trip -- a rejected save costs the user their whole
 * arrangement, not just the one field. A drift between the two is therefore
 * not a cosmetic difference: it is either a dialog that accepts something the
 * server will throw the layout out for, or one that refuses a value the
 * server would have taken.
 *
 * No user-authored queries and no URLs, ever (D-8): a parameterized widget
 * takes validated values only, and a value is either drawn from a closed set
 * the widget declared or bounded generically because its legal values are not
 * knowable ahead of time (a namespace name).
 */
import type { DataSourceKey, LayoutItem } from "./types.ts";

/**
 * The param key whose value names a Kubernetes namespace.
 *
 * Exactly the server's `paramKeyNamespace`, and exact on purpose: the read
 * path recognises a namespace by this key and re-authorizes its value on every
 * read (R5). A widget that spelled it `ns`, or `Namespace`, would store and
 * render identically and silently opt out of that re-authorization, with
 * nothing anywhere to notice. `registerWidget` refuses a param key this module
 * does not know for that reason.
 */
export const PARAM_KEY_NAMESPACE = "namespace";

/** Every param key a widget may declare. See `PARAM_KEY_NAMESPACE`. */
export const KNOWN_PARAM_KEYS: readonly string[] = [PARAM_KEY_NAMESPACE];

/** Mirrors `maxParamKeyLen` in backend/internal/preferences/dashboard.go. */
export const MAX_PARAM_KEY_LEN = 32;
/** Mirrors `maxParamValueLen`. 253 is a Kubernetes name's own bound. */
export const MAX_PARAM_VALUE_LEN = 253;

/**
 * Which of a source's reads depend on the widget's parameters, and on which
 * of them.
 *
 * A source key is the cache's identity, so a read whose response depends on a
 * parameter needs that parameter IN the key -- otherwise diagnostics for prod
 * and diagnostics for staging are one cache entry and the two widgets show
 * whichever landed last. A source absent from this table is global: its key
 * never widens, however many parameters the widget carrying it declares, so
 * `cluster-info` is fetched once for the page rather than once per namespace.
 *
 * Declared per source rather than per widget for that reason: a parameterized
 * widget generally reads some global sources too.
 */
export const PARAMETERIZED_SOURCE_PARAMS: Readonly<
  Partial<Record<DataSourceKey, readonly string[]>>
> = {
  "diagnostics-summary": [PARAM_KEY_NAMESPACE],
  // The scanning route REQUIRES `?namespace=` and answers 400 without it, so
  // unlike diagnostics -- whose namespace is a scoping choice -- this one has
  // no unparameterized form to fall back to. Keyed the same way for the same
  // reason: two vulnerability cards pointed at different namespaces are two
  // cache entries, and two pointed at the same one are a single fetch.
  "vulnerability-reports": [PARAM_KEY_NAMESPACE],
};

/** C0 and C1 -- Unicode category Cc, which is what Go's `unicode.IsControl`
 * reports for the range a JSON string can carry. */
const CONTROL_CHARS = /\p{Cc}/u;

/** Code points, not UTF-16 units: the server counts runes
 * (`utf8.RuneCountInString`), and a value of 253 astral characters is one the
 * server accepts. `String.length` would refuse it at 127. */
function runeCount(value: string): number {
  return [...value].length;
}

/**
 * Why `value` is not acceptable, or null when it is.
 *
 * `allowed` is the widget's declared value set for this key. Empty means the
 * legal values are not knowable here -- a namespace name -- so the generic
 * bounds are the whole check, which is deliberately NOT the same as the key
 * being undeclared (that is `missingParamKeys`' business and the server's).
 *
 * The messages are the dialog's copy. They name the rule rather than echoing
 * the value, because a value long enough to break the bound is too long to
 * read back.
 */
export function paramValueError(
  value: string,
  allowed: readonly string[],
): string | null {
  if (value === "") return "Choose a value.";
  if (runeCount(value) > MAX_PARAM_VALUE_LEN) {
    return `Use ${MAX_PARAM_VALUE_LEN} characters or fewer.`;
  }
  if (CONTROL_CHARS.test(value)) {
    return "Control characters are not allowed.";
  }
  if (allowed.length > 0 && !allowed.includes(value)) {
    return "Choose one of the offered values.";
  }
  return null;
}

/**
 * The declared keys `values` has no usable value for, in declaration order.
 *
 * A widget is placed only once this is empty: a placement missing a value it
 * declared would render a widget reading an endpoint with a hole in its path.
 */
export function missingParamKeys(
  declared: Readonly<Record<string, readonly string[]>> | undefined,
  values: Readonly<Record<string, string>>,
): string[] {
  if (declared === undefined) return [];
  return Object.keys(declared).filter((k) => (values[k] ?? "") === "");
}

/** `n:s`, the encoding both functions below are built out of. Length-prefixed
 * rather than delimited so it stays injective for any value. */
function lenPrefixed(s: string): string {
  return `${s.length}:${s}`;
}

/**
 * A param map rendered order-independently, so two placements differing only
 * in key order compare equal.
 *
 * Mirrors `canonicalParams` in backend/internal/preferences/dashboard.go,
 * including its encoding and the reason for it: this string is the
 * duplicate-detection identity, so it has to be injective, and a delimiter is
 * injective only while no value can contain it -- a precondition living in
 * another function. Length prefixes depend on nothing.
 *
 * The two implementations have to agree, because this is what decides whether
 * the client offers a placement the server will refuse as a duplicate.
 */
export function canonicalParams(
  params: Readonly<Record<string, string>> | undefined,
): string {
  if (params === undefined) return "";
  const keys = Object.keys(params).sort();
  return keys.map((k) => lenPrefixed(k) + lenPrefixed(params[k])).join("");
}

/**
 * The placement `values` would duplicate, or null when it would not.
 *
 * Same widget, same parameters -- the identity the server enforces on save
 * (`seenIdentity` in the validator). Two placements of a parameterized widget
 * with DIFFERENT parameters are two legitimate views, which is the reason
 * `instanceId` exists at all; two with the same parameters are the same card
 * twice.
 *
 * `exceptInstanceId` is the placement being re-parameterized. Without it,
 * re-opening the dialog on a widget and confirming its current namespace
 * unchanged would report the widget as a collision with itself.
 */
export function duplicatePlacementOf(
  items: readonly LayoutItem[],
  widgetId: string,
  values: Readonly<Record<string, string>>,
  exceptInstanceId?: string,
): LayoutItem | null {
  const identity = canonicalParams(values);
  return (
    items.find(
      (i) =>
        i.id === widgetId &&
        i.instanceId !== exceptInstanceId &&
        canonicalParams(i.params) === identity,
    ) ?? null
  );
}

/**
 * The cache key one source is read under, for a widget carrying `params`.
 *
 * A source this table does not name is global and keeps its bare key, so the
 * page reads it once however many parameterized widgets are placed. A named
 * one carries the values of the keys it declared, sorted, in the same
 * length-prefixed encoding `canonicalParams` uses and for the same reason.
 *
 * Note what this does NOT do: it never folds in a parameter the source did
 * not declare. A widget may carry parameters only some of its sources read,
 * and keying every source on all of them would split one shared cache entry
 * into one per widget.
 */
export function sourceKeyFor(
  base: string,
  params: Readonly<Record<string, string>> = {},
): string {
  const declared = PARAMETERIZED_SOURCE_PARAMS[base as DataSourceKey];
  if (declared === undefined) return base;
  const parts = [...declared]
    .sort()
    .filter((k) => (params[k] ?? "") !== "")
    .map((k) => lenPrefixed(k) + lenPrefixed(params[k]));
  return parts.length === 0 ? base : `${base}:${parts.join("")}`;
}

/**
 * The source and parameters a key was built from.
 *
 * The cache holds opaque string keys and has to get back to the fetcher that
 * serves them and the values that fetcher needs. Decoding rather than carrying
 * the pair alongside keeps `ensure`, `state` and `signalFor` taking a single
 * string, which is what lets every existing caller and every existing test go
 * on passing a bare source key unchanged.
 *
 * Total: a key this cannot parse comes back as a bare base with no parameters
 * rather than throwing, because the caller's next move is a fetcher lookup and
 * an unknown base already has none.
 */
export function decodeSourceKey(key: string): {
  base: string;
  params: Record<string, string>;
} {
  const at = key.indexOf(":");
  if (at === -1) return { base: key, params: {} };

  const base = key.slice(0, at);
  const params: Record<string, string> = {};
  let rest = key.slice(at + 1);
  while (rest.length > 0) {
    const pair: string[] = [];
    for (let i = 0; i < 2; i++) {
      const colon = rest.indexOf(":");
      if (colon <= 0) return { base, params: {} };
      const len = Number(rest.slice(0, colon));
      if (!Number.isInteger(len) || len < 0) return { base, params: {} };
      const from = colon + 1;
      if (from + len > rest.length) return { base, params: {} };
      pair.push(rest.slice(from, from + len));
      rest = rest.slice(from + len);
    }
    params[pair[0]] = pair[1];
  }
  return { base, params };
}

/**
 * Every cache key a widget declaring `sources` needs fetched, under `params`.
 *
 * The consumer that drives the cache calls this instead of reading the source
 * list directly, so a parameterized source is requested once per distinct
 * value and a global one once for the page. Duplicates are dropped: two of a
 * widget's sources can resolve to the same key only if both are global and
 * equal, which is already deduped by `ensure`, but doing it here keeps the
 * list a caller can compare.
 */
export function widgetSourceKeys(
  sources: readonly string[],
  params: Readonly<Record<string, string>> = {},
): string[] {
  return [...new Set(sources.map((s) => sourceKeyFor(s, params)))];
}
