import { expect, test } from "bun:test";
import { shouldBypassChrome } from "./chrome-bypass.ts";

test("bypasses chrome for the exact /login path", () => {
  expect(shouldBypassChrome("/login")).toBe(true);
});

test("bypasses chrome for the exact /setup path", () => {
  expect(shouldBypassChrome("/setup")).toBe(true);
});

test("bypasses chrome for any /auth/* path", () => {
  expect(shouldBypassChrome("/auth/callback")).toBe(true);
  expect(shouldBypassChrome("/auth/oidc/provider1/mobile-config")).toBe(true);
});

test("does not bypass chrome for a normal path", () => {
  expect(shouldBypassChrome("/")).toBe(false);
  expect(shouldBypassChrome("/workloads/pods")).toBe(false);
});

test("does not bypass chrome for a path that merely starts with the same letters", () => {
  // Ported check uses === for /login and /setup — a route like /login2 or
  // /setup-wizard must NOT bypass, matching the original exact-match
  // semantics rather than a loose prefix check.
  expect(shouldBypassChrome("/login2")).toBe(false);
  expect(shouldBypassChrome("/logins")).toBe(false);
  expect(shouldBypassChrome("/setup-wizard")).toBe(false);
});

test("does not bypass chrome for bare /auth with no trailing slash", () => {
  // startsWith("/auth/") requires the trailing slash — /auth alone is not
  // one of today's routes, and preserving this exact behavior (rather than
  // "helpfully" loosening it) is the parity requirement.
  expect(shouldBypassChrome("/auth")).toBe(false);
});

test("does not bypass chrome for an unrelated path containing 'auth' mid-segment", () => {
  expect(shouldBypassChrome("/settings/auth")).toBe(false);
});
