import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { sourceHasWsProxy } from "./check-no-ws-dev-proxy.ts";

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
 * not. The block below is frozen verbatim from it -- the Fresh dev server's
 * own /ws proxy, the configuration this guard exists to stop anyone
 * recreating in astro.config.mjs (R18: dev and prod must not diverge on /ws
 * again). Freezing it keeps the control rather than letting it disappear with
 * its source; the synthetic cases below cover shapes, this one covers the
 * exact text that was actually shipped.
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
