import { defineConfig } from "vite";
import { fresh } from "@fresh/plugin-vite";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [fresh(), tailwindcss()],
  server: {
    // WebSocket upgrades never reach the Fresh route that relays them
    // (routes/ws/[...path].ts). Vite runs Fresh handlers as connect
    // middleware, and an HTTP upgrade bypasses middleware entirely -- it is
    // dispatched on the server's own "upgrade" event -- so every socket the
    // app opened in dev sat in CONNECTING until it timed out. That silently
    // disabled live resource updates, log streaming, exec terminals, alerts
    // and flows for anyone running `deno task dev`, while production
    // (`deno task start`, a real Deno server handling the same route) worked.
    //
    // Proxying /ws straight to the backend restores dev parity. The Fresh
    // route this bypasses is itself only a relay to the same endpoint, so the
    // path the browser sees is unchanged.
    proxy: {
      "/ws": {
        target: "ws://localhost:8080",
        ws: true,
        rewrite: (path) => path.replace(/^\/ws/, "/api"),
      },
    },
  },
  // Prevent Vite's node-resolution from walking up to C:\Users\whstu\node_modules
  // and picking up the npm `fresh` HTTP-header utility instead of the JSR
  // @fresh/core package.  The deno plugin (enforce:"pre") resolves bare JSR
  // specifiers from deno.json import-map; this alias guarantees that even if
  // the deno plugin is bypassed in the SSR compat runner, Vite never reaches
  // the wrong ancestor node_modules.
  resolve: {
    alias: {
      "fresh": "jsr:@fresh/core@^2.2.0",
    },
  },
  optimizeDeps: {
    exclude: ["fresh"],
  },
});
