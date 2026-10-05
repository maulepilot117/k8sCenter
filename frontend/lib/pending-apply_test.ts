import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { logout } from "./auth.ts";
import {
  clearPendingApplies,
  clearStoredAttempt,
  PENDING_ATTEMPT_TTL_MS,
  PENDING_STORAGE_PREFIX,
  readStoredAttempt,
  sha256Hex,
  writeStoredAttempt,
} from "./pending-apply.ts";

// Registered for this file only (sessionStorage), like yaml-apply_test.ts.
beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());
afterEach(() => globalThis.sessionStorage.clear());

const NOW = 1_800_000_000_000;
const KEY = JSON.stringify([
  false,
  null,
  null,
  "local",
  "u-1",
  "kind: ConfigMap",
]);

test("sha256Hex matches the reference implementation", () => {
  const inputs = [
    "",
    "abc",
    "kind: ConfigMap\n",
    "héllo wörld ✓ 日本語 🚀",
    // Padding boundaries: one block, the 55/56 split, and exactly 64 bytes.
    "a".repeat(55),
    "a".repeat(56),
    "a".repeat(63),
    "a".repeat(64),
    "a".repeat(65),
    "a".repeat(119),
    "a".repeat(120),
    "x".repeat(10_000),
    KEY,
  ];
  for (const input of inputs) {
    expect(sha256Hex(input)).toBe(
      createHash("sha256").update(input, "utf8").digest("hex"),
    );
  }
});

test("a stored attempt round-trips for the same request", () => {
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });

  expect(readStoredAttempt(KEY, NOW + 1000)).toEqual({
    id: "id-1",
    requestKey: KEY,
    at: NOW,
  });
  expect(readStoredAttempt(`${KEY} `, NOW + 1000)).toBeNull();
});

test("storage holds a digest, never the request key", () => {
  const secretYaml = "kind: Secret\ndata:\n  password: aHVudGVyMg==";
  const key = JSON.stringify([false, null, null, "local", "u-1", secretYaml]);
  writeStoredAttempt({ id: "id-1", requestKey: key, at: NOW });

  const dump = JSON.stringify({ ...globalThis.sessionStorage });
  expect(dump).not.toContain("aHVudGVyMg");
  expect(dump).not.toContain("Secret");
  expect(dump).toContain(sha256Hex(key));
});

test("an entry whose digest does not match its request is refused and removed", () => {
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });
  const storageKey = PENDING_STORAGE_PREFIX + sha256Hex(KEY);
  // As if another request had landed under this storage key.
  globalThis.sessionStorage.setItem(
    storageKey,
    JSON.stringify({ id: "other", at: NOW, digest: sha256Hex("different") }),
  );

  expect(readStoredAttempt(KEY, NOW)).toBeNull();
  expect(globalThis.sessionStorage.getItem(storageKey)).toBeNull();
});

test("an entry expires after the reuse window and is removed", () => {
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });

  expect(
    readStoredAttempt(KEY, NOW + PENDING_ATTEMPT_TTL_MS - 1),
  ).not.toBeNull();
  expect(readStoredAttempt(KEY, NOW + PENDING_ATTEMPT_TTL_MS)).toBeNull();
  expect(globalThis.sessionStorage.length).toBe(0);
});

test("a malformed entry is ignored and removed", () => {
  const storageKey = PENDING_STORAGE_PREFIX + sha256Hex(KEY);
  globalThis.sessionStorage.setItem(storageKey, "{not json");
  expect(readStoredAttempt(KEY, NOW)).toBeNull();
  expect(globalThis.sessionStorage.getItem(storageKey)).toBeNull();
});

test("clearStoredAttempt removes only that request's entry", () => {
  const other = JSON.stringify([
    false,
    null,
    null,
    "local",
    "u-1",
    "kind: Pod",
  ]);
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });
  writeStoredAttempt({ id: "id-2", requestKey: other, at: NOW });

  clearStoredAttempt(KEY);

  expect(readStoredAttempt(KEY, NOW)).toBeNull();
  expect(readStoredAttempt(other, NOW)?.id).toBe("id-2");
});

test("clearPendingApplies removes every pending entry and nothing else", () => {
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });
  writeStoredAttempt({ id: "id-2", requestKey: `${KEY}2`, at: NOW });
  globalThis.sessionStorage.setItem("unrelated", "keep");

  clearPendingApplies();

  expect(readStoredAttempt(KEY, NOW)).toBeNull();
  expect(readStoredAttempt(`${KEY}2`, NOW)).toBeNull();
  expect(globalThis.sessionStorage.getItem("unrelated")).toBe("keep");
});

test("logout clears pending applies", async () => {
  writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });
  const original = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response("{}", { status: 200 }),
    )) as unknown as typeof fetch;
  try {
    await logout();
  } finally {
    globalThis.fetch = original;
  }

  expect(readStoredAttempt(KEY, NOW)).toBeNull();
  expect(globalThis.sessionStorage.length).toBe(0);
});

test("every helper is a no-op when storage is unavailable", () => {
  const real = Object.getOwnPropertyDescriptor(globalThis, "sessionStorage");
  Object.defineProperty(globalThis, "sessionStorage", {
    configurable: true,
    get() {
      throw new DOMException("blocked", "SecurityError");
    },
  });
  try {
    expect(() => globalThis.sessionStorage).toThrow("blocked");
    writeStoredAttempt({ id: "id-1", requestKey: KEY, at: NOW });
    expect(readStoredAttempt(KEY, NOW)).toBeNull();
    clearStoredAttempt(KEY);
    clearPendingApplies();
  } finally {
    if (real) Object.defineProperty(globalThis, "sessionStorage", real);
  }
});
