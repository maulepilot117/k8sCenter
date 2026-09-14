import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";
import { dispatchApi, rejectMalformed } from "./dispatch.ts";
import { applySecurityHeaders } from "./headers.ts";
import { applyClusterScopedCrdRewrite } from "./rewrites.ts";
import { attachWsProxy, handleWsHttpRequest } from "./ws-proxy.ts";

/**
 * Wires the same dispatch logic used in production (headers, the
 * malformed-request guard, the /ws/* bridge, and the /api/* BFF proxy)
 * into `astro dev`'s own Vite dev server (R18).
 *
 * `astro dev` owns its own node:http server via Vite -- there is no outer
 * server for this plugin to be mounted inside the way prod.ts's is. So the
 * fix runs the other direction: reach into Vite's `configureServer` hook,
 * install our headers/guard/ws-426/api middleware at the very front of
 * Vite's middleware stack (calling `server.middlewares.use` directly
 * inside `configureServer`, rather than returning a post-hook function,
 * registers it *before* Vite's own internal middlewares -- so it also
 * covers static assets and Astro's own dev rendering, not just this
 * plugin's own routes), and attach ws-proxy.ts's identical 'upgrade'
 * handler to the httpServer Vite already created. U1 proved this exact
 * shape end to end: dev and the built server run through the same
 * allowlist, traversal guard, queueing, and exec Bearer-header logic. U6
 * adds the same guarantee for /api/*: without this branch, `astro dev`
 * has nothing that answers /api/v1/* at all (the Fresh route files under
 * routes/api/ are not Astro routes), so every API call would 404.
 *
 * Static asset serving is deliberately not wired here: in dev, Vite serves
 * `public/` and the module graph itself, and that is not the code path this
 * unit is scoped to change.
 */
export function bunServerDevPlugin(): Plugin {
  return {
    name: "k8scenter-bun-server-dev",
    configureServer(server) {
      server.middlewares.use(
        (req: IncomingMessage, res: ServerResponse, next: () => void) => {
          applySecurityHeaders(res);
          if (rejectMalformed(req, res)) return;
          if (handleWsHttpRequest(req, res)) return;
          if (dispatchApi(req, res)) return;
          applyClusterScopedCrdRewrite(req);
          next();
        },
      );

      if (server.httpServer) {
        attachWsProxy(server.httpServer);
      }
    },
  };
}
