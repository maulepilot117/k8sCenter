import { expect, test } from "bun:test";
import {
  decodeBearerSubprotocol,
  encodeBearerSubprotocol,
  findBearerInSubprotocols,
  parseSubprotocolHeader,
  selectWsSubprotocol,
  WS_AUTH_BEARER_PROTOCOL_PREFIX,
  WS_AUTH_SENTINEL_PROTOCOL,
} from "./ws-exec-auth.ts";

// --- encode/decode round trip ---

test("encodeBearerSubprotocol then decodeBearerSubprotocol recovers the original token", () => {
  const token = "header.payload.signature-with-lots-of-entropy-1234567890";
  const encoded = encodeBearerSubprotocol(token);
  expect(encoded.startsWith(WS_AUTH_BEARER_PROTOCOL_PREFIX)).toBe(true);
  expect(decodeBearerSubprotocol(encoded)).toBe(token);
});

test("encodeBearerSubprotocol round-trips tokens of every base64 padding length", () => {
  // Padding length depends on (byte length % 3), so exercise all three.
  for (const token of ["a", "ab", "abc", "abcd", "abcde", "abcdef"]) {
    expect(decodeBearerSubprotocol(encodeBearerSubprotocol(token))).toBe(token);
  }
});

// --- RFC 6455 token validity: no '=', '+', '/' ---

test("the encoded subprotocol contains only valid RFC 6455 token characters", () => {
  // A raw JWT is exactly the case this exists for: '.' separated base64url
  // segments that, standard-base64-encoded, would carry '+' / '/' / '='.
  const jwtShaped =
    "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM_Pz8_Pz8ifQ.c2lnbmF0dXJlPz8-Pj4-Pg";
  const encoded = encodeBearerSubprotocol(jwtShaped);
  const payload = encoded.slice(WS_AUTH_BEARER_PROTOCOL_PREFIX.length);
  expect(payload).not.toMatch(/[+/=]/);
  // Full HTTP token grammar (RFC 2616 §2.2): no separators, no whitespace,
  // no control characters. '.', '-', '_' and alphanumerics all qualify.
  expect(encoded).toMatch(/^[A-Za-z0-9._-]+$/);
});

// --- decode failure modes never throw ---

test("decodeBearerSubprotocol returns undefined for a value with the wrong prefix", () => {
  expect(decodeBearerSubprotocol("k8scenter.v1")).toBeUndefined();
  expect(decodeBearerSubprotocol("bearer.something-else.abc")).toBeUndefined();
});

test("decodeBearerSubprotocol returns undefined when the payload is empty", () => {
  expect(
    decodeBearerSubprotocol(WS_AUTH_BEARER_PROTOCOL_PREFIX),
  ).toBeUndefined();
});

test("decodeBearerSubprotocol returns undefined (not a throw) for invalid base64url", () => {
  expect(
    decodeBearerSubprotocol(`${WS_AUTH_BEARER_PROTOCOL_PREFIX}not!valid!!`),
  ).toBeUndefined();
});

// --- header parsing ---

test("parseSubprotocolHeader splits, trims, and drops empty entries", () => {
  expect(parseSubprotocolHeader("foo, bar ,  baz")).toEqual([
    "foo",
    "bar",
    "baz",
  ]);
  expect(parseSubprotocolHeader("")).toEqual([]);
  expect(parseSubprotocolHeader(undefined)).toEqual([]);
  expect(parseSubprotocolHeader(null)).toEqual([]);
  expect(parseSubprotocolHeader("foo,,bar")).toEqual(["foo", "bar"]);
});

// --- finding the bearer among offered subprotocols, order-independent ---

test("findBearerInSubprotocols locates the credential regardless of header order", () => {
  const bearer = encodeBearerSubprotocol("my-token");
  expect(
    findBearerInSubprotocols(`${bearer}, ${WS_AUTH_SENTINEL_PROTOCOL}`),
  ).toBe("my-token");
  expect(
    findBearerInSubprotocols(`${WS_AUTH_SENTINEL_PROTOCOL}, ${bearer}`),
  ).toBe("my-token");
});

test("findBearerInSubprotocols returns undefined when no offered value is credential-bearing", () => {
  expect(findBearerInSubprotocols(WS_AUTH_SENTINEL_PROTOCOL)).toBeUndefined();
  expect(findBearerInSubprotocols(undefined)).toBeUndefined();
  expect(findBearerInSubprotocols("some-other-protocol")).toBeUndefined();
});

// --- server-side echo selection: sentinel only, never the credential ---

test("selectWsSubprotocol echoes the sentinel when the client offered it alongside a credential", () => {
  const bearer = encodeBearerSubprotocol("my-token");
  const protocols = new Set([bearer, WS_AUTH_SENTINEL_PROTOCOL]);
  expect(selectWsSubprotocol(protocols)).toBe(WS_AUTH_SENTINEL_PROTOCOL);
});

test("selectWsSubprotocol never selects the credential-bearing value, even alone", () => {
  const bearer = encodeBearerSubprotocol("my-token");
  expect(selectWsSubprotocol(new Set([bearer]))).toBe(false);
});

test("selectWsSubprotocol selects nothing when the sentinel wasn't offered", () => {
  expect(selectWsSubprotocol(new Set(["some-other-protocol"]))).toBe(false);
  expect(selectWsSubprotocol(new Set())).toBe(false);
});
