import { createServer } from "node:http";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequestListener } from "./dispatch.ts";
import { createStaticHandler } from "./static.ts";
import { attachWsProxy } from "./ws-proxy.ts";

/**
 * The production entry point (KD5/KD6/KTD2). One node:http server owns the
 * socket: it serves dist/client/ (U5 step 3), bridges /ws/* to the backend
 * (U5 step 2/3), sets the five security headers on every response (KTD3),
 * and mounts Astro's built middleware-mode handler as the fallthrough
 * (U5 step 4). Port 8000 by default, matching the Helm contract (R13).
 */

function getPort(): number {
  const raw = process.env.PORT;
  const parsed = raw ? Number(raw) : Number.NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 8000;
}

async function main() {
  const here = fileURLToPath(new URL(".", import.meta.url));
  const clientDir = join(here, "..", "dist", "client");
  // dist/server/entry.mjs is @astrojs/node's middleware-mode build output --
  // it only exists after `astro build` (astro.config.mjs, KTD2) -- so this
  // import is dynamic and resolved at startup, not at type-check time.
  const entryUrl = new URL("../dist/server/entry.mjs", import.meta.url).href;
  const { handler } = (await import(entryUrl)) as {
    handler: (
      req: unknown,
      res: unknown,
      next?: () => void,
    ) => void | Promise<void>;
  };

  const serveStatic = createStaticHandler(clientDir);
  const listener = createRequestListener({
    astroHandler: handler,
    serveStatic,
  });

  const server = createServer(listener);
  attachWsProxy(server);

  const port = getPort();
  server.listen(port, () => {
    console.log(`[k8scenter] listening on :${port}`);
  });
}

if (import.meta.main) {
  main().catch((err) => {
    console.error("[k8scenter] fatal startup error:", err);
    process.exit(1);
  });
}
