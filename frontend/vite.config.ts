import { defineConfig } from "vite";
import { fresh } from "@fresh/plugin-vite";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [fresh(), tailwindcss()],
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
