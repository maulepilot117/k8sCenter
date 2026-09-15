import { afterAll, beforeAll, expect, test } from "bun:test";
import {
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  cacheControlFor,
  createStaticHandler,
  resolveStaticFile,
} from "./static.ts";

let root: string;
let clientDir: string;
let outsideDir: string;
let symlinkSupported = true;

beforeAll(() => {
  root = mkdtempSync(join(tmpdir(), "u5-static-"));
  clientDir = join(root, "dist-client");
  outsideDir = join(root, "outside");
  mkdirSync(clientDir, { recursive: true });
  mkdirSync(join(clientDir, "_astro"), { recursive: true });
  mkdirSync(outsideDir, { recursive: true });

  writeFileSync(join(clientDir, "favicon.svg"), "<svg/>");
  writeFileSync(join(clientDir, "_astro", "chunk-abc123.js"), "console.log(1)");
  writeFileSync(join(outsideDir, "secret.txt"), "do not serve");

  try {
    symlinkSync(
      join(outsideDir, "secret.txt"),
      join(clientDir, "escape-link.txt"),
    );
  } catch {
    // Creating symlinks on Windows without elevated privileges/dev mode can
    // fail with EPERM. The escape-via-symlink test below is skipped in that
    // case rather than failing for an unrelated environment reason.
    symlinkSupported = false;
  }
});

afterAll(() => {
  rmSync(root, { recursive: true, force: true });
});

test("resolves a plain file with the non-immutable cache class", () => {
  const result = resolveStaticFile(clientDir, "/favicon.svg");
  expect(result.status).toBe(200);
  expect(result.immutable).toBe(false);
  expect(result.contentType).toBe("image/svg+xml");
  expect(cacheControlFor(Boolean(result.immutable))).toBe(
    "no-cache, no-store, max-age=0, must-revalidate",
  );
});

test("resolves an _astro/ asset with the immutable cache class", () => {
  const result = resolveStaticFile(clientDir, "/_astro/chunk-abc123.js");
  expect(result.status).toBe(200);
  expect(result.immutable).toBe(true);
  expect(cacheControlFor(Boolean(result.immutable))).toBe(
    "public, max-age=31536000, immutable",
  );
});

test("404s a path that does not exist", () => {
  expect(resolveStaticFile(clientDir, "/does-not-exist.js").status).toBe(404);
});

test("404s a directory (not a file)", () => {
  expect(resolveStaticFile(clientDir, "/_astro").status).toBe(404);
});

test("403s a literal '..' traversal", () => {
  expect(resolveStaticFile(clientDir, "/../outside/secret.txt").status).toBe(
    403,
  );
});

test("403s an encoded '..' traversal (%2e%2e)", () => {
  expect(
    resolveStaticFile(clientDir, "/%2e%2e/outside/secret.txt").status,
  ).toBe(403);
});

test("404s (never traverses) a double-encoded '..' — decoding happens exactly once", () => {
  // The raw string has no literal ".." or "%2e" (it has "%25", "2", "e"),
  // so the pre-decode guard lets it through. decodeURIComponent runs once,
  // turning "%252e%252e" into the literal text "%2e%2e" -- not ".." -- so
  // the post-decode guard does not fire either. Resolution then looks for
  // a real directory literally named "%2e%2e", which does not exist: 404,
  // not a traversal. Decoding a SECOND time would produce "..", which is
  // exactly the bug this single-decode design avoids.
  expect(
    resolveStaticFile(clientDir, "/%252e%252e/outside/secret.txt").status,
  ).toBe(404);
});

test("403s a backslash variant", () => {
  expect(resolveStaticFile(clientDir, "/..\\outside\\secret.txt").status).toBe(
    403,
  );
  expect(resolveStaticFile(clientDir, "/foo%5C..%5Coutside").status).toBe(403);
});

test("403s a symlink inside dist/client/ pointing outside it", () => {
  if (!symlinkSupported) {
    console.warn(
      "skipping symlink-escape test: symlink creation unsupported in this environment",
    );
    return;
  }
  expect(resolveStaticFile(clientDir, "/escape-link.txt").status).toBe(403);
});

test("createStaticHandler falls through (false) for '/' so Astro renders the SSR route", () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler({ url: "/", method: "GET" } as never, res as never);
  expect(handled).toBe(false);
});

test("createStaticHandler falls through (false) for a not-found path", () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler(
    { url: "/nope.js", method: "GET" } as never,
    res as never,
  );
  expect(handled).toBe(false);
});

test("createStaticHandler serves a real file with headers and content", async () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler(
    { url: "/favicon.svg", method: "GET" } as never,
    res as never,
  );
  expect(handled).toBe(true);
  expect(res.statusCode).toBe(200);
  expect(res.headers.get("Content-Type")).toBe("image/svg+xml");
  expect(res.headers.get("Cache-Control")).toBe(
    "no-cache, no-store, max-age=0, must-revalidate",
  );
  const body = await res.body();
  expect(body).toBe("<svg/>");
});

test("createStaticHandler answers HEAD with headers and no body", () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler(
    { url: "/favicon.svg", method: "HEAD" } as never,
    res as never,
  );
  expect(handled).toBe(true);
  expect(res.statusCode).toBe(200);
  expect(res.headers.get("Content-Length")).toBe("6");
  expect(res.ended).toBe(true);
});

test("createStaticHandler 405s a non-GET/HEAD method on a real file", () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler(
    { url: "/favicon.svg", method: "POST" } as never,
    res as never,
  );
  expect(handled).toBe(true);
  expect(res.statusCode).toBe(405);
});

test("createStaticHandler 403s a traversal attempt without touching the filesystem for a match", () => {
  const handler = createStaticHandler(clientDir);
  const res = makeFakeRes();
  const handled = handler(
    { url: "/../outside/secret.txt", method: "GET" } as never,
    res as never,
  );
  expect(handled).toBe(true);
  expect(res.statusCode).toBe(403);
});

// --- minimal fake ServerResponse, just enough for the assertions above ---

function makeFakeRes() {
  const headers = new Map<string, string>();
  const chunks: Buffer[] = [];
  let ended = false;
  let statusCode = 200;

  const res = {
    get statusCode() {
      return statusCode;
    },
    set statusCode(v: number) {
      statusCode = v;
    },
    headers,
    setHeader(name: string, value: string) {
      headers.set(name, value);
    },
    end(chunk?: unknown) {
      if (typeof chunk === "string") chunks.push(Buffer.from(chunk));
      ended = true;
    },
    write(chunk: Buffer | string) {
      chunks.push(typeof chunk === "string" ? Buffer.from(chunk) : chunk);
      return true;
    },
    // Minimal stand-in for the Writable side of pipe(): static.ts pipes a
    // ReadStream into this object for the GET-with-body case.
    on() {
      return res;
    },
    once() {
      return res;
    },
    emit() {
      return false;
    },
    get ended() {
      return ended;
    },
    body(): Promise<string> {
      return new Promise((resolvePromise) => {
        // Give createReadStream(...).pipe(res) a tick to flush.
        setTimeout(
          () => resolvePromise(Buffer.concat(chunks).toString("utf-8")),
          20,
        );
      });
    },
  };
  return res;
}
