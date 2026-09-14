import { expect, test } from "bun:test";
import { checkWsPath, isExecPath, remapCloseCode } from "./ws-allowlist.ts";

const ALLOWED = [
  "/ws/v1/ws/resources",
  "/ws/v1/ws/logs/default/mypod/main",
  "/ws/v1/ws/exec/default/mypod/main",
  "/ws/v1/ws/alerts",
  "/ws/v1/ws/flows",
  "/ws/v1/ws/logs-search",
];

for (const path of ALLOWED) {
  test(`checkWsPath allows ${path}`, () => {
    const result = checkWsPath(path);
    expect(result.ok).toBe(true);
  });
}

test("checkWsPath rejects a path outside the allowlist with 404", () => {
  const result = checkWsPath("/ws/v1/ws/unknown-endpoint");
  expect(result).toEqual({ ok: false, status: 404 });
});

test("checkWsPath rejects a path with no /ws/ prefix with 404", () => {
  expect(checkWsPath("/api/v1/ws/resources")).toEqual({
    ok: false,
    status: 404,
  });
});

test("checkWsPath rejects a path missing the v1/ prefix with 400", () => {
  expect(checkWsPath("/ws/v2/ws/resources")).toEqual({
    ok: false,
    status: 400,
  });
});

test("checkWsPath rejects '..' traversal with 400, before any allowlist match", () => {
  expect(checkWsPath("/ws/v1/ws/exec/../../../etc/passwd")).toEqual({
    ok: false,
    status: 400,
  });
});

test("checkWsPath rejects '//' traversal with 400", () => {
  expect(checkWsPath("/ws/v1/ws//resources")).toEqual({
    ok: false,
    status: 400,
  });
});

test("checkWsPath rejects '%2e' encoded traversal with 400", () => {
  expect(checkWsPath("/ws/v1/ws/exec/%2e%2e/mypod/main")).toEqual({
    ok: false,
    status: 400,
  });
  expect(checkWsPath("/ws/v1/ws/exec/%2E%2E/mypod/main")).toEqual({
    ok: false,
    status: 400,
  });
});

test("checkWsPath strips the query string before matching", () => {
  const result = checkWsPath("/ws/v1/ws/resources?foo=bar");
  expect(result.ok).toBe(true);
  if (result.ok) expect(result.path).toBe("v1/ws/resources");
});

test("isExecPath is true only for the exec route", () => {
  expect(isExecPath("v1/ws/exec/default/mypod/main")).toBe(true);
  expect(isExecPath("v1/ws/resources")).toBe(false);
  expect(isExecPath("v1/ws/logs/default/mypod/main")).toBe(false);
});

test("remapCloseCode passes through valid codes unchanged", () => {
  expect(remapCloseCode(1000)).toBe(1000);
  expect(remapCloseCode(4000)).toBe(4000);
  expect(remapCloseCode(1011)).toBe(1011);
  expect(remapCloseCode(4999)).toBe(4999);
});

test("remapCloseCode maps 1006 (abnormal closure) to 1000", () => {
  expect(remapCloseCode(1006)).toBe(1000);
});

test("remapCloseCode maps out-of-range codes to 1000", () => {
  expect(remapCloseCode(999)).toBe(1000);
  expect(remapCloseCode(5000)).toBe(1000);
  expect(remapCloseCode(-1)).toBe(1000);
});
