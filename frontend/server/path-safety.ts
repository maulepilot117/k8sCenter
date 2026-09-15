/**
 * Traversal detection for proxied request paths, shared by the HTTP and
 * WebSocket guards.
 *
 * Both guards enforce the same rule and previously carried their own copy of
 * it. They now share this one, because the copies had already started to
 * drift and a traversal rule that is true in one proxy and false in the other
 * is worse than either.
 *
 * ## Why the raw form is still checked
 *
 * The original guards deliberately tested the raw, still-percent-encoded
 * path and decoded nothing: a decode step of one's own can be fooled by
 * double-encoding, and the raw test catches a literal `..` and a `%2e` with
 * the same expression. That instinct was right and is kept.
 *
 * ## Why the raw form alone is not enough
 *
 * It is exactly one decoding deep. `%252e%252e` contains no literal `..`,
 * no `//` and no `%2e` -- the characters are `%`, `2`, `5`, `2`, `e` -- so it
 * passes a raw-only check, and any intermediary that decodes twice sees
 * `..`. The same hole swallows `%2f%2f`, which is `//` one decoding away and
 * which the raw pattern never looked for at all.
 *
 * So the rule is applied to the raw form AND to each successive decoding,
 * until the string stops changing. A path is safe only if every form of it
 * is safe. That is stronger than either "check raw" or "decode once, then
 * check", and it does not depend on guessing how many times something
 * downstream will decode.
 *
 * ## Why a malformed escape is rejected outright
 *
 * `decodeURIComponent` throws on `%zz` or a truncated `%2`. Treating that as
 * "cannot decode, so assume safe" is the failure mode this whole module
 * exists to avoid: a path we cannot reason about is not a path we forward.
 */

/** Literal traversal, an empty segment, or a single-encoded dot. */
const TRAVERSAL_PATTERN = /\.\.|\/\/|%2e|%2f/i;

/**
 * Bound on decoding passes.
 *
 * The bound exists so a pathological input cannot turn this into a hot loop.
 * It is NOT a judgement about how deeply an attacker might nest, because
 * that judgement cannot be made safely: a bound that stops decoding and then
 * reports "safe" is a bypass at bound+1, which is what a first cut of this
 * module had -- `%25252525252e%25252525252e` sailed through a four-pass
 * check. Exhausting the bound with escapes left over is therefore treated as
 * unsafe, not as done.
 */
const MAX_DECODE_PASSES = 8;

/**
 * Every form a path can take under repeated decoding, starting with the raw
 * one.
 *
 * Returns `null` when the path cannot be fully reduced -- a malformed escape
 * sequence, or escapes still present after the pass bound. The caller must
 * treat `null` as unsafe. "We ran out of passes" and "this is clean" are not
 * the same answer, and conflating them is the bypass this shape exists to
 * prevent.
 */
export function decodedForms(path: string): string[] | null {
  const forms = [path];
  let current = path;
  for (let i = 0; i < MAX_DECODE_PASSES; i++) {
    if (!current.includes("%")) return forms;
    let next: string;
    try {
      next = decodeURIComponent(current);
    } catch {
      // A malformed escape sequence. We cannot know what a downstream
      // decoder will make of it, so we do not forward it.
      return null;
    }
    if (next === current) return forms;
    forms.push(next);
    current = next;
  }
  // Bound exhausted with escapes still in play: unreducible, so unsafe.
  return current.includes("%") ? null : forms;
}

/**
 * True when no form of this path -- raw or any decoding of it -- contains a
 * traversal sequence, an empty segment, or an encoded form of either.
 */
export function hasTraversal(path: string): boolean {
  const forms = decodedForms(path);
  if (forms === null) return true;
  return forms.some((form) => TRAVERSAL_PATTERN.test(form));
}
