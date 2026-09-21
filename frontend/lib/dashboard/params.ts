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

/**
 * The param key whose value names a Kubernetes Service inside that namespace.
 *
 * Exactly the server's `paramKeyService`. The second key, and the first whose
 * legal values are not knowable ahead of time AND are not re-authorized on
 * read -- which is why it is the only key with a value SHAPE (see
 * `PARAM_VALUE_SHAPE`). A namespace that is nonsense is inert, because the
 * read path checks it against the live cluster and withholds the placement
 * when it does not authorize. Nothing performs that check for a service name:
 * there is no per-service grant to test it against, and the backing route
 * authorizes the namespace. So a service value that reached storage
 * unvalidated would be exactly the "unvalidated caller text in a stored
 * layout" D-8 exists to prevent, and its shape is the whole defence.
 */
export const PARAM_KEY_SERVICE = "service";

/** Every param key a widget may declare. See `PARAM_KEY_NAMESPACE`. */
export const KNOWN_PARAM_KEYS: readonly string[] = [
  PARAM_KEY_NAMESPACE,
  PARAM_KEY_SERVICE,
];

/**
 * Keys whose open values must additionally match a shape, and the shape.
 *
 * Mirrors `paramValueShapes` in backend/internal/preferences/dashboard.go,
 * pattern for pattern. A key absent from this table is bounded only by the
 * generic length and control-character rules, which is what `namespace` has
 * been since P3 and stays -- tightening it here would start refusing saves of
 * layouts the server has been accepting, which is the drift this module
 * exists to prevent rather than cause.
 *
 * A Service name is a DNS-1035 label: lowercase alphanumerics and dashes,
 * starting with a letter, at most 63 characters. Stricter than a namespace's
 * DNS-1123 label on purpose -- it is what Kubernetes itself enforces on the
 * object -- and strict enough that a URL, a path, a PromQL expression and a
 * shell fragment are all refused by the same rule rather than by four
 * blocklists.
 */
export const PARAM_VALUE_SHAPE: Readonly<Record<string, RegExp>> = {
  [PARAM_KEY_SERVICE]: /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/,
};

/**
 * Which key a field's value space is drawn from, for the keys that have one.
 *
 * Mirrors nothing on the server, and does not need to: this is a collection
 * rule, not a storage rule. The server validates a service name's shape
 * whatever namespace it arrived beside, because a placement is validated as a
 * whole and a service that does not exist in its namespace is not a thing the
 * server can tell apart from one that was deleted a minute ago.
 *
 * It matters here because the dialog cannot offer a service list before it
 * knows which namespace to list from, and because a service chosen in one
 * namespace is meaningless in another.
 */
export const PARAM_KEY_PARENT: Readonly<Record<string, string>> = {
  [PARAM_KEY_SERVICE]: PARAM_KEY_NAMESPACE,
};

/** The key `key`'s value space is scoped by, or null when it has none. */
export function paramParentKey(key: string): string | null {
  return PARAM_KEY_PARENT[key] ?? null;
}

/** Why a field cannot be used yet, or null when it can. */
export type ParamFieldBlock = "awaiting-parent" | "no-options";

export interface ParamFieldState {
  enabled: boolean;
  blocked: ParamFieldBlock | null;
}

/**
 * Whether a field can be used, and if not, why.
 *
 * Two reasons, and they need different copy: a service field before a
 * namespace is chosen is waiting for something the user is about to do, while
 * a service field in a namespace whose services the user cannot list is
 * waiting for nothing at all.
 *
 * Both outcomes are a DISABLED SELECT. Neither is a text input, and that is
 * the load-bearing part: "there is nothing to offer, so let them type it"
 * would put an unvalidated service name into a stored layout, which is
 * precisely the shape D-8 exists to keep out (and the reason
 * `PARAM_VALUE_SHAPE` exists as the second line of defence for the client
 * that skips the dialog).
 */
export function paramFieldState(
  key: string,
  values: Readonly<Record<string, string>>,
  options: readonly string[],
): ParamFieldState {
  const parent = paramParentKey(key);
  if (parent !== null && (values[parent] ?? "") === "") {
    return { enabled: false, blocked: "awaiting-parent" };
  }
  if (options.length === 0) return { enabled: false, blocked: "no-options" };
  return { enabled: true, blocked: null };
}

/**
 * `values` with one key set, and every key that depended on it cleared.
 *
 * Unconditional on the parent's new value, including when it is the value the
 * parent already had: the dependent field's OPTIONS are refetched either way,
 * and a value kept across a refetch is a value that was never checked against
 * the list it is supposedly drawn from.
 *
 * The failure this prevents is silent. A golden-signals widget re-pointed
 * from prod to staging while still carrying prod's service name reads a
 * service that does not exist in staging, and the mesh answers zeros -- a
 * card reporting no traffic for a service that is simply not there, which is
 * absence rendering as good news (R1) one level down from where the shell can
 * see it.
 */
export function withParamValue(
  values: Readonly<Record<string, string>>,
  key: string,
  value: string,
): Record<string, string> {
  const next: Record<string, string> = { ...values, [key]: value };
  for (const [child, parent] of Object.entries(PARAM_KEY_PARENT)) {
    if (parent === key && child in next) next[child] = "";
  }
  return next;
}

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
  // The first source keyed by TWO values. `/v1/mesh/golden-signals` requires
  // both and answers 400 with either missing, and -- more to the point here --
  // two cards on two services in one namespace are two different reads. Keyed
  // on the namespace alone they would collapse into one cache entry showing
  // whichever service landed last, under two cards each labelled with its own.
  "mesh-golden-signals": [PARAM_KEY_NAMESPACE, PARAM_KEY_SERVICE],
  // The Hubble REST route requires `?namespace=` and answers 400 without one,
  // like the scanning route above: a mandatory scope rather than a choice.
  "hubble-flows": [PARAM_KEY_NAMESPACE],
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
  key: string,
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
  if (allowed.length > 0) {
    return allowed.includes(value) ? null : "Choose one of the offered values.";
  }
  // Open-valued, so the generic bounds above were the whole check until a key
  // arrived whose value nothing downstream re-authorizes. `PARAM_VALUE_SHAPE`
  // names those keys and carries the reasoning; a key absent from it keeps the
  // behaviour it shipped with.
  const shape = PARAM_VALUE_SHAPE[key];
  if (shape !== undefined && !shape.test(value)) {
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
