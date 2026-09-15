import { expect, test } from "bun:test";
import { decodedForms, hasTraversal } from "./path-safety.ts";

// --- what a legitimate path looks like ---

for (const safe of [
  "v1/ws/resources",
  "v1/resources/pods/default/my-pod",
  "v1/resources/pods/kube-system/coredns-1234-abcd/logs",
  "v1/auth/oidc/keycloak/callback",
  "v1/extensions/resources/acme.io/widgets/cluster-scoped/thing",
  // A percent that decodes to something harmless must still pass.
  "v1/resources/configmaps/default/name%2Dwith%2Ddashes",
]) {
  test(`allows ${safe}`, () => {
    expect(hasTraversal(safe)).toBe(false);
  });
}

// --- single-encoding: what the original guard already caught ---

for (const [label, hostile] of [
  ["literal dot-dot", "v1/../secrets"],
  ["dot-dot mid-path", "v1/resources/../../etc/passwd"],
  ["empty segment", "v1//resources"],
  ["lowercase %2e", "v1/%2e%2e/secrets"],
  ["uppercase %2E", "v1/%2E%2E/secrets"],
] as const) {
  test(`refuses ${label}`, () => {
    expect(hasTraversal(hostile)).toBe(true);
  });
}

// --- double-encoding: the gap this module was written to close ---
//
// `%252e` is `%`, `2`, `5`, `2`, `e`. It contains no literal `..`, no `//`
// and no `%2e`, so a guard that tests only the raw string lets it through --
// and anything downstream that decodes twice sees `..`. The reviewers
// classified this as pre-existing and still open; it is closed here.

for (const [label, hostile] of [
  ["double-encoded dot-dot", "v1/%252e%252e/secrets"],
  ["double-encoded, uppercase", "v1/%252E%252E/secrets"],
  ["double-encoded slash", "v1/resources%252f%252fsecrets"],
  ["triple-encoded dot-dot", "v1/%25252e%25252e/secrets"],
  ["mixed single and double encoding", "v1/%2e%252e/secrets"],
] as const) {
  test(`refuses ${label}`, () => {
    expect(hasTraversal(hostile)).toBe(true);
  });
}

test("a single-encoded slash is refused (the raw pattern never looked for it)", () => {
  // `%2f%2f` is `//` one decoding away. The previous expression tested for
  // `..`, `//` and `%2e`, so this shape was never checked at all.
  expect(hasTraversal("v1/resources%2f%2fsecrets")).toBe(true);
});

// --- malformed input is refused, never assumed safe ---

for (const malformed of ["v1/%zz/secrets", "v1/%2/secrets", "v1/%"]) {
  test(`refuses malformed escape ${malformed}`, () => {
    expect(hasTraversal(malformed)).toBe(true);
    expect(decodedForms(malformed)).toBeNull();
  });
}

// --- the decoding walk itself ---

test("decodedForms returns the raw form first and stops when stable", () => {
  expect(decodedForms("v1/ws/resources")).toEqual(["v1/ws/resources"]);
});

test("decodedForms peels one layer at a time", () => {
  const forms = decodedForms("v1/%252e");
  expect(forms).not.toBeNull();
  expect(forms?.[0]).toBe("v1/%252e");
  expect(forms?.[1]).toBe("v1/%2e");
  expect(forms?.[2]).toBe("v1/.");
});

/** n-times percent-encoded "..". encodeURIComponent will not do this: `.`
 *  is unreserved, so it never encodes dots -- a first version of this test
 *  used it and was therefore asserting on the literal string "..". */
function nestEncoded(depth: number): string {
  let p = "%2e%2e";
  for (let i = 1; i < depth; i++) p = p.replaceAll("%", "%25");
  return p;
}

test("nesting deeper than the decode bound does not escape the guard", () => {
  // A bound that stops decoding and reports "safe" is a bypass at bound+1.
  // The first cut of this module had exactly that: a four-pass check let
  // "v1/%25252525252e%25252525252e" through. Exhausting the bound with
  // escapes still present is now unsafe, so depth stops mattering.
  for (let depth = 1; depth <= 20; depth++) {
    expect(
      hasTraversal(`v1/${nestEncoded(depth)}`),
      `traversal nested ${depth} deep escaped the guard`,
    ).toBe(true);
  }
});

test("an unreducible path is refused rather than assumed clean", () => {
  expect(decodedForms(`v1/${nestEncoded(20)}`)).toBeNull();
});

test("the decode walk is bounded and returns promptly", () => {
  const started = Date.now();
  expect(hasTraversal(`v1/${nestEncoded(40)}`)).toBe(true);
  expect(Date.now() - started).toBeLessThan(1_000);
});
