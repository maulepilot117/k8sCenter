import { test, expect } from "../fixtures/base.ts";
import { readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";

// Regression guard for the class of bug fixed in PR #163: frontend islands
// called /v1/policy/* while the backend mounted /v1/policies/*. Every caller
// silently 404'd and the page stayed empty. This test walks the frontend
// source tree, pulls out every literal "/v1/..." path, and confirms the
// backend responds to it with something other than 404. It is intentionally
// permissive about other status codes (401/403/400) — any non-404 response
// proves a route is mounted; only a missing route is a bug.

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const FRONTEND_ROOT = path.resolve(__dirname, "../../frontend");

// Directories we scan for string literals containing API paths.
const SCAN_DIRS = ["islands", "lib", "routes", "components"];

// Matches pure-literal "/v1/…" paths with no template expressions or params.
// Drops entries containing ${}, ${, `:`, or spaces so dynamic paths like
// `/v1/resources/pods/${ns}/${name}` and `/v1/resources/:kind` are skipped —
// those aren't resolvable without knowing live cluster state.
const PATH_REGEX = /["'`](\/v1\/[A-Za-z0-9/_-]+)(?:\?[^"'`]*)?["'`]/g;

// Paths we know are *expected* to be absent and should not be tested.
const IGNORE: RegExp[] = [
  // None yet — add here if a legitimate conditional path would cause a 404.
];

function walk(dir: string, out: string[] = []): string[] {
  let entries: string[];
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    const p = path.join(dir, entry);
    const s = statSync(p);
    if (s.isDirectory()) {
      walk(p, out);
    } else if (/\.(ts|tsx|js|jsx)$/.test(entry)) {
      out.push(p);
    }
  }
  return out;
}

function collectPaths(): Set<string> {
  const found = new Set<string>();
  for (const sub of SCAN_DIRS) {
    const files = walk(path.join(FRONTEND_ROOT, sub));
    for (const file of files) {
      const content = readFileSync(file, "utf8");
      for (const match of content.matchAll(PATH_REGEX)) {
        const p = match[1];
        if (IGNORE.some((rx) => rx.test(p))) continue;
        found.add(p);
      }
    }
  }
  return found;
}

test.describe("API route contract @smoke", () => {
  const paths = Array.from(collectPaths()).sort();

  // Token is populated once per worker by the first test that runs under
  // this describe block. localStorage is origin-scoped, so we need at least
  // one page.goto to restore it from storageState before we can read it.
  let accessToken: string | null = null;

  test.beforeEach(async ({ page }) => {
    if (accessToken !== null) return;
    await page.goto("/");
    accessToken = await page.evaluate(() =>
      localStorage.getItem("e2e_access_token"),
    );
  });

  test("at least ten /v1/ paths were discovered", () => {
    // Defensive: if the scan finds nothing, the regex or directory structure
    // changed and this whole suite is silently a no-op.
    expect(paths.length).toBeGreaterThan(10);
  });

  for (const p of paths) {
    test(`GET /api${p} is mounted (not 404)`, async ({ page }) => {
      const headers: Record<string, string> = {
        "X-Requested-With": "XMLHttpRequest",
      };
      if (accessToken) headers["Authorization"] = `Bearer ${accessToken}`;

      const res = await page.request.get(`/api${p}`, {
        headers,
        failOnStatusCode: false,
      });

      expect(
        res.status(),
        `frontend references ${p} but backend returned 404 — route mismatch between frontend/lib/api.ts and backend/internal/server/routes.go`,
      ).not.toBe(404);
    });
  }
});
