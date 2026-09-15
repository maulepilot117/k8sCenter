import { createReadStream, existsSync, realpathSync, statSync } from "node:fs";
import type { IncomingMessage, ServerResponse } from "node:http";
import { extname, join, resolve, sep } from "node:path";

/**
 * Serves dist/client/ with path containment and cache headers equivalent to
 * Fresh's staticFiles() (R12, U5 step 3/4).
 *
 * The runtime image holds the compiled binary and bun.lock beside
 * dist/client/ (KTD6), so a containment slip here exposes them — hence the
 * belt-and-suspenders checks: reject the raw traversal signal before
 * normalising, resolve-and-prefix-check after normalising, and a realpath
 * check to defeat a symlink planted inside dist/client/ that points outside
 * it.
 */

const MIME_TYPES: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".mjs": "text/javascript",
  ".css": "text/css",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".avif": "image/avif",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".txt": "text/plain; charset=utf-8",
  ".map": "application/json",
  ".wasm": "application/wasm",
  ".xml": "application/xml",
};

export interface StaticResolution {
  status: 200 | 403 | 404;
  filePath?: string;
  immutable?: boolean;
  contentType?: string;
}

/**
 * Memoised `realpathSync` for the client directory.
 *
 * Keyed by the input path so a test that points the handler at a temp
 * directory is not served another directory's cached answer.
 */
const realClientDirCache = new Map<string, string>();
function realClientDir(dir: string): string {
  const hit = realClientDirCache.get(dir);
  if (hit !== undefined) return hit;
  const resolvedReal = realpathSync(dir);
  realClientDirCache.set(dir, resolvedReal);
  return resolvedReal;
}

/**
 * Pure path-resolution + containment logic (no response I/O), so tests can
 * exercise it directly against a fixture directory.
 */
export function resolveStaticFile(
  clientDirAbs: string,
  rawUrlPath: string,
): StaticResolution {
  const rawPath = (rawUrlPath ?? "/").split("?")[0] ?? "/";

  // Reject the raw traversal signal (and its backslash / %2e variants)
  // before any decoding or normalising touches the string — same posture
  // as the WS allowlist guard in ws-allowlist.ts.
  if (rawPath.includes("\\") || /\.\.|%2e/i.test(rawPath)) {
    return { status: 403 };
  }

  let decoded: string;
  try {
    decoded = decodeURIComponent(rawPath);
  } catch {
    return { status: 403 };
  }
  if (decoded.includes("\\") || decoded.includes("..")) {
    return { status: 403 };
  }

  const clientDirResolved = resolve(clientDirAbs);
  const target = resolve(join(clientDirResolved, decoded));
  if (
    target !== clientDirResolved &&
    !target.startsWith(clientDirResolved + sep)
  ) {
    return { status: 403 };
  }

  if (!existsSync(target)) {
    return { status: 404 };
  }
  const stat = statSync(target);
  if (!stat.isFile()) {
    return { status: 404 };
  }

  // Symlink-escape guard: the resolved path passed containment above, but a
  // symlink *inside* dist/client/ can still point outside it. realpath
  // follows symlinks; re-check containment against the followed path.
  let real: string;
  let clientDirReal: string;
  try {
    real = realpathSync(target);
    // The client directory's real path is fixed for the life of the process,
    // so it is resolved once and memoised rather than re-walked on every
    // static request. This sits on the per-navigation hot path: the built
    // site emits 200-odd hashed chunks and the chrome mounts eight islands.
    clientDirReal = realClientDir(clientDirResolved);
  } catch {
    return { status: 404 };
  }
  if (real !== clientDirReal && !real.startsWith(clientDirReal + sep)) {
    return { status: 403 };
  }

  const ext = extname(target);
  const contentType = MIME_TYPES[ext] ?? "application/octet-stream";

  // Mirrors @fresh/core's staticFiles() immutable/no-cache split (its
  // src/middlewares/static_files.ts): build-hashed assets get a one-year
  // immutable cache, everything else revalidates on every request. Astro's
  // equivalent of Fresh's hashed `_fresh/js/<BUILD_ID>/` bucket is the
  // `_astro/` output directory — every filename in it already embeds a
  // content hash, so it is safe to cache forever.
  const normalizedDecoded = decoded.replace(/^\/+/, "");
  const immutable = normalizedDecoded.startsWith("_astro/");

  return { status: 200, filePath: target, immutable, contentType };
}

export function cacheControlFor(immutable: boolean): string {
  return immutable
    ? "public, max-age=31536000, immutable"
    : "no-cache, no-store, max-age=0, must-revalidate";
}

/**
 * Returns a request handler that serves dist/client/ for GET/HEAD requests
 * whose path resolves to a real file under it. Returns a boolean from the
 * handler: true means "handled, stop here"; false means "not a static
 * asset, fall through to Astro" — which is what happens for "/" (Astro's
 * SSR route) and for any not-found path, so Astro's own 404 page renders
 * rather than a bare-text one.
 */
export function createStaticHandler(clientDirAbs: string) {
  return function serveStatic(
    req: IncomingMessage,
    res: ServerResponse,
  ): boolean {
    const urlPath = req.url ?? "/";
    if (urlPath === "/" || urlPath.startsWith("/?")) {
      return false;
    }

    const resolved = resolveStaticFile(clientDirAbs, urlPath);

    if (resolved.status === 404) {
      return false;
    }
    if (resolved.status === 403) {
      res.statusCode = 403;
      res.end("Forbidden");
      return true;
    }

    if (req.method !== "GET" && req.method !== "HEAD") {
      res.statusCode = 405;
      res.end("Method Not Allowed");
      return true;
    }

    const filePath = resolved.filePath as string;
    const stat = statSync(filePath);
    res.setHeader("Content-Type", resolved.contentType as string);
    res.setHeader(
      "Cache-Control",
      cacheControlFor(Boolean(resolved.immutable)),
    );
    res.setHeader("Content-Length", String(stat.size));
    res.statusCode = 200;

    if (req.method === "HEAD") {
      res.end();
      return true;
    }

    // An 'error' listener is not optional here. The status line is already
    // written, so a read failure after this point (EACCES, EMFILE, a file
    // unlinked between statSync and open) has nowhere to be reported -- and
    // an unhandled stream 'error' is an uncaughtException on a non-request
    // stack, which ends the process for every user. Mirrors the rule this
    // repo already applies to background goroutines on the backend.
    const stream = createReadStream(filePath);
    stream.on("error", (err) => {
      console.error("[static] read failed after headers were sent:", err);
      res.destroy();
    });
    stream.pipe(res);
    return true;
  };
}
