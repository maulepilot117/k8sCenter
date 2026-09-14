import { createServer } from "node:http";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
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

/**
 * Where `dist/` lives at runtime.
 *
 * Under `bun run server/prod.ts` that is one directory up from this file, and
 * `import.meta.url` finds it. Under the compiled launcher it is not: U10 ships
 * this entry through `bun build --compile`, whose `import.meta.url` points at
 * a virtual path inside the binary (`/$bunfs/...`) with no `dist/` beside it.
 * The same lookup is why KTD6 cannot compile the whole server — `@astrojs/node`
 * walks `import.meta.url` for its own asset directory and finds nothing.
 *
 * So the container passes the real root explicitly and the source tree keeps
 * working unchanged. Both paths are exercised: `bun run start` locally, the
 * binary in the image.
 */
function getAppRoot(): string {
  const fromEnv = process.env.K8SCENTER_APP_ROOT;
  if (fromEnv) return fromEnv;
  return join(fileURLToPath(new URL(".", import.meta.url)), "..");
}

function getPort(): number {
  const raw = process.env.PORT;
  const parsed = raw ? Number(raw) : Number.NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 8000;
}

async function main() {
  const appRoot = getAppRoot();
  const clientDir = join(appRoot, "dist", "client");
  // dist/server/entry.mjs is @astrojs/node's middleware-mode build output --
  // it only exists after `astro build` (astro.config.mjs, KTD2) -- so this
  // import is dynamic and resolved at startup, not at type-check time. It
  // stays on disk rather than being embedded, which is the half of KTD6 the
  // compiled launcher deliberately does not own.
  const entryUrl = pathToFileURL(
    join(appRoot, "dist", "server", "entry.mjs"),
  ).href;
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
