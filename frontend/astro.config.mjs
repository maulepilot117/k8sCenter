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
      dedupe: ["preact", "@preact/signals-core"],
    },
  },
});
