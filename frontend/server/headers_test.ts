import { expect, test } from "bun:test";
import {
  applySecurityHeaders,
  rawHeaderLines,
  SECURITY_HEADERS,
} from "./headers.ts";

test("SECURITY_HEADERS has exactly the five headers ported from main.ts", () => {
  const names = SECURITY_HEADERS.map(([name]) => name);
  expect(names).toEqual([
    "Content-Security-Policy",
    "X-Frame-Options",
    "X-Content-Type-Options",
    "Referrer-Policy",
    "Permissions-Policy",
  ]);
});

test("CSP does not force upgrade-insecure-requests (the reason Fresh's csp() was rejected)", () => {
  const [, csp] = SECURITY_HEADERS[0];
  expect(csp).not.toContain("upgrade-insecure-requests");
  expect(csp).toContain("frame-ancestors 'none'");
  expect(csp).toContain("default-src 'self'");
});

test("applySecurityHeaders sets all five headers on a ServerResponse-shaped object", () => {
  const set = new Map<string, string>();
  const fakeRes = {
    setHeader: (name: string, value: string) => set.set(name, value),
  };
  // biome-ignore lint/suspicious/noExplicitAny: minimal fake, only setHeader is used
  applySecurityHeaders(fakeRes as any);

  expect(set.get("X-Frame-Options")).toBe("DENY");
  expect(set.get("X-Content-Type-Options")).toBe("nosniff");
  expect(set.get("Referrer-Policy")).toBe("strict-origin-when-cross-origin");
  expect(set.get("Permissions-Policy")).toBe(
    "camera=(), microphone=(), geolocation=()",
  );
  expect(set.get("Content-Security-Policy")).toContain("default-src 'self'");
  expect(set.size).toBe(5);
});

test("rawHeaderLines renders one 'Name: value' line per header, same order and values", () => {
  const lines = rawHeaderLines();
  expect(lines).toHaveLength(5);
  expect(lines[1]).toBe("X-Frame-Options: DENY");
  expect(lines[2]).toBe("X-Content-Type-Options: nosniff");
  expect(lines[3]).toBe("Referrer-Policy: strict-origin-when-cross-origin");
  expect(lines[4]).toBe(
    "Permissions-Policy: camera=(), microphone=(), geolocation=()",
  );
  expect(lines[0].startsWith("Content-Security-Policy: ")).toBe(true);
});
