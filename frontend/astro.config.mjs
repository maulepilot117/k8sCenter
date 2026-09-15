import node from "@astrojs/node";
import preact from "@astrojs/preact";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "astro/config";
import { bunServerDevPlugin } from "./server/dev-plugin.ts";

// U3 stands up the toolchain only -- the outer Bun server that actually
// mounts this adapter's middleware-mode handler is U5's job (KTD2), and the
// pages under src/pages are ported route-by-route in U7 through U9. Until
// then this config exists so `astro check` (part of `bun run check`, see
// package.json) and `astro build` have a valid project to run against.
export default defineConfig({
  // Middleware mode exports a Node-style (req, res) handler instead of
  // running its own listener, so the application's own server (KD5/KTD2)
  // owns the socket and can also own WebSocket upgrades and security
  // headers on every response (KTD3). Astro's own CSP feature stays off
  // for the same reason -- see frontend/main.ts today, ported in U5/U7.
  output: "server",
  // 5173 rather than Astro's own 4321 default. That is the port `make
  // dev-frontend` has always served on, the one CLAUDE.md documents, and the
  // one Playwright's local (non-CI) webServer entry expects. The migration is
  // meant to be invisible to anyone running the app, and a moved dev URL is
  // the most visible thing there is.
  server: { port: 5173 },
  adapter: node({ mode: "middleware" }),
  integrations: [preact()],
  vite: {
    // U5: wires the same headers/malformed-guard/ws-proxy dispatch used by
    // the built server (frontend/server/prod.ts) into `astro dev`'s own
    // Vite dev server, so dev and prod never diverge on /ws again (R18).
    // See frontend/server/dev-plugin.ts for why this is a Vite plugin
    // rather than a second outer server.
    plugins: [tailwindcss(), bunServerDevPlugin()],
    resolve: {
      // KTD5: the failure mode this guards against is not Astro but two
      // resolved copies of these packages -- after which a computed()
      // built from one copy can't see a signal() from the other. Preact
      // islands share state via module-scope @preact/signals (KD nothing
      // fresh-specific here), so both packages must dedupe to one copy.
      //
      // U14: the same failure mode, one dependency family over. Vite's dev
      // optimizer was pre-bundling @codemirror/state into two separate
      // copies -- one under the named `@codemirror_state.js` dep chunk, a
      // second inlined into an anonymous `dist-*.js` chunk pulled in
      // through @codemirror/language's own dependency graph. CodeMirror's
      // `Facet`/`Extension` values are identity-checked (not duck-typed),
      // so `EditorState.create()` in CodeMirrorEditor.tsx threw "Unrecognized
      // extension value in extension set" for every extension built from
      // the copy `EditorState.create` itself didn't come from -- a real,
      // browser-only failure invisible to `astro check`/`astro build`/
      // `bun test`, caught only by loading the U9 harness in an actual
      // browser. Dedupe the whole @codemirror/* family so every package
      // shares one resolved copy of @codemirror/state's Facet identities.
      dedupe: [
        "preact",
        "@preact/signals-core",
        "@codemirror/state",
        "@codemirror/view",
        "@codemirror/commands",
        "@codemirror/language",
        "@codemirror/lang-yaml",
        "@codemirror/search",
        "@codemirror/theme-one-dark",
      ],
    },
  },
});
