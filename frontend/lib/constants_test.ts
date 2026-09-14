import { expect, test } from "bun:test";
import { BACKEND_URL, readBackendUrlFromEnv } from "./constants.ts";

// Regression test for the bug U6 fixed: `typeof Deno !== "undefined"` is
// always false under Bun, so the old implementation silently ignored
// `process.env.BACKEND_URL` and fell back to the localhost default no
// matter what the Helm chart set on the frontend Deployment (R13).

test("BACKEND_URL is a string, exercising the module's own top-level env read", () => {
  // BACKEND_URL is computed once at import time; this only proves the
  // module loads and produces *some* URL. The env-honouring behaviour
  // itself is proven below via the exported, callable helper, which can
  // be invoked after mutating process.env within the test.
  expect(typeof BACKEND_URL).toBe("string");
  expect(BACKEND_URL.length).toBeGreaterThan(0);
});

test("readBackendUrlFromEnv reads BACKEND_URL from process.env under Bun (the bug)", () => {
  const prev = process.env.BACKEND_URL;
  process.env.BACKEND_URL = "http://backend.internal:9999";
  try {
    expect(readBackendUrlFromEnv()).toBe("http://backend.internal:9999");
  } finally {
    if (prev === undefined) {
      delete process.env.BACKEND_URL;
    } else {
      process.env.BACKEND_URL = prev;
    }
  }
});

test("readBackendUrlFromEnv returns undefined when BACKEND_URL is unset under Bun", () => {
  const prev = process.env.BACKEND_URL;
  delete process.env.BACKEND_URL;
  try {
    expect(readBackendUrlFromEnv()).toBeUndefined();
  } finally {
    if (prev !== undefined) {
      process.env.BACKEND_URL = prev;
    }
  }
});
