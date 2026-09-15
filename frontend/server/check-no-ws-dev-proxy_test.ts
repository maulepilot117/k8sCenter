import { expect, test } from "bun:test";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { filesToScan, sourceHasWsProxy } from "./check-no-ws-dev-proxy.ts";

/**
 * What the guard actually scans.
 *
 * Nothing asserted this before, and the omission cost exactly what you would
 * expect: the scan list was two hardcoded paths with a comment saying to widen
 * it once the Fresh tree's own vite.config.ts was deleted. That tree was
 * deleted and the list was never widened, because no test cared. A dropped
 * entry or a mis-globbed pattern would have passed the same way -- reporting
 * success having scanned nothing.
 */
test("the scan covers the committed dev-configuration surface", () => {
  const scanned = filesToScan().map((f) => f.replace(/\\/g, "/"));
  expect(scanned.some((f) => f.endsWith("/astro.config.mjs"))).toBe(true);
  expect(scanned.some((f) => f.endsWith("/dev-plugin.ts"))).toBe(true);
  // The sanctioned /ws path must stay out of scope, or the guard flags itself.
  expect(scanned.some((f) => f.endsWith("/ws-proxy.ts"))).toBe(false);
  expect(scanned.some((f) => f.endsWith("/ws-allowlist.ts"))).toBe(false);
});

/**
 * And the derivation, not just today's answer: a new dev-server plugin module
 * added beside dev-plugin.ts is loaded by astro.config.mjs's plugin array, so
 * a /ws proxy written into it is the same divergence. The hardcoded list could
 * not see one.
 */
test("a newly added dev plugin module is picked up without editing the guard", () => {
  const dir = mkdtempSync(join(tmpdir(), "k8sc-ws-"));
  const server = join(dir, "server");
  mkdirSync(server, { recursive: true });
  writeFileSync(join(dir, "astro.config.mjs"), "export default {};\n");
  writeFileSync(join(server, "dev-plugin.ts"), "export const a = 1;\n");
  writeFileSync(join(server, "socket-plugin.ts"), "export const b = 2;\n");
  // Not a dev config: must not be scanned.
  writeFileSync(join(server, "ws-proxy.ts"), 'const k = "/ws";\n');
  try {
    const found = filesToScan(dir, server).map((f) => f.replace(/\\/g, "/"));
    expect(found.some((f) => f.endsWith("/socket-plugin.ts"))).toBe(true);
    expect(found.some((f) => f.endsWith("/dev-plugin.ts"))).toBe(true);
    expect(found.some((f) => f.endsWith("/astro.config.mjs"))).toBe(true);
    expect(found.some((f) => f.endsWith("/ws-proxy.ts"))).toBe(false);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("flags a /ws proxy planted in a Vite dev config", () => {
  const planted = `
    export default defineConfig({
      vite: { server: { proxy: { "/ws": { target: "ws://localhost:8080", ws: true } } } }
    });
  `;
  expect(sourceHasWsProxy(planted)).toBe(true);
});

test("flags a /ws rewrite planted in a Vite dev config", () => {
  const planted = `
    server: {
      proxy: {
        "/socket": { target: "ws://localhost:8080", rewrite: (path) => path.replace(/^\\/socket/, "/ws") }
      }
    }
  `;
  expect(sourceHasWsProxy(planted)).toBe(true);
});

test("does not flag ordinary config text mentioning /ws in a comment or string unrelated to proxy/rewrite", () => {
  const benign = `
    // The WebSocket bridge lives at /ws/* and is handled by ws-proxy.ts.
    export default defineConfig({ output: "server" });
  `;
  expect(sourceHasWsProxy(benign)).toBe(false);
});

test("the committed astro.config.mjs has no /ws proxy or rewrite (passes as committed)", () => {
  const source = readFileSync(
    new URL("../astro.config.mjs", import.meta.url),
    "utf-8",
  );
  expect(sourceHasWsProxy(source)).toBe(false);
});

test("the committed dev-plugin.ts has no /ws proxy or rewrite (passes as committed)", () => {
  const source = readFileSync(
    new URL("./dev-plugin.ts", import.meta.url),
    "utf-8",
  );
  expect(sourceHasWsProxy(source)).toBe(false);
});

/**
 * The positive control: the detector must fire on the real thing.
 *
 * This read frontend/vite.config.ts directly until U12 deleted it, which is
 * the right check while that file exists and a broken test the moment it does
 * not.
 *
 * What is frozen below is that file's `server.proxy` stanza, verbatim -- not
 * the whole file, which also carried imports, a plugins array and a
 * resolve/optimizeDeps block that have nothing to do with this detector. The
 * distinction matters only for what a reader should trust: the detector is a
 * whole-source regex, so the stanza exercises exactly the path the real file
 * did, but "frozen verbatim" without this qualification would invite someone
 * to treat this constant as a faithful copy of the deleted config. It is not,
 * and the original is at 5b98284a if one is ever needed.
 *
 * The configuration itself is the one this guard exists to stop anyone
 * recreating in astro.config.mjs (R18: dev and prod must not diverge on /ws
 * again). The synthetic cases below cover shapes; this one covers text that
 * actually shipped.
 */
const FRESH_VITE_WS_PROXY = [
  'import { defineConfig } from "vite";',
  "export default defineConfig({",
  "  server: {",
  "    proxy: {",
  '      "/ws": {',
  '        target: "ws://localhost:8080",',
  "        ws: true,",
  '        rewrite: (path) => path.replace(/^\\/ws/, "/api"),',
  "      },",
  "    },",
  "  },",
  "});",
].join(String.fromCharCode(10));

test("the detector fires on the Fresh dev server's real /ws proxy (frozen at U12)", () => {
  expect(sourceHasWsProxy(FRESH_VITE_WS_PROXY)).toBe(true);
});

// --- the shape the previous pattern could not see (review finding #8) ---

test("a /ws entry that is not the first proxy key is still caught", () => {
  // The old pattern anchored on `proxy: {` then scanned [^}]*, which stops at
  // the first closing brace -- so /ws behind any sibling entry was invisible.
  // That is the realistic reintroduction, not the contrived one.
  const config = [
    "export default {",
    "  server: {",
    "    proxy: {",
    '      "/api": { target: "http://localhost:8080", changeOrigin: true },',
    '      "/ws": { target: "ws://localhost:8080", ws: true },',
    "    },",
    "  },",
    "};",
  ].join(String.fromCharCode(10));
  expect(sourceHasWsProxy(config)).toBe(true);
});

test("a trailing-slash /ws/ key is caught", () => {
  expect(sourceHasWsProxy('proxy: { "/ws/": { ws: true } }')).toBe(true);
});

test("a single-quoted /ws key is caught", () => {
  expect(sourceHasWsProxy("proxy: { '/ws': { ws: true } }")).toBe(true);
});

test("an unrelated config is still clean", () => {
  expect(
    sourceHasWsProxy(
      "export default { integrations: [], vite: { plugins: [] } };",
    ),
  ).toBe(false);
});
