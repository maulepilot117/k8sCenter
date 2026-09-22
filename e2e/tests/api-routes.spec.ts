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
//
// Repointed at the Astro tree during the Bun migration. The old Fresh
// directories (islands/, lib/, routes/, components/) still exist until U12
// deletes them, so scanning both would double-count the same literals out of
// files that are on their way to being removed -- and, worse, would keep this
// guard passing on the strength of the dead tree alone after the new one
// stopped matching. src/ is the only tree the shipped server renders from.
// The `paths.length > 10` assertion below is what fails loudly if this list
// ever goes stale again.
const SCAN_DIRS = [
  // The Astro tree.
  "src/islands",
  "src/lib",
  "src/pages",
  "src/components",
  "src/layouts",
  "server",
  // The pre-migration tree, until U12 deletes it. This is NOT redundant:
  // the shipped client graph still reaches it -- 149 files under src/ import
  // from @/lib/ and @/components/ -- and scanning src/ alone left 38 live
  // "/v1/" literals unchecked, including /v1/auth/me and /v1/preferences/pins.
  // Those are exactly the paths this guard exists to catch a rename in.
  // Duplicates are harmless: results land in a Set.
  "islands",
  "lib",
  "components",
];

// Matches pure-literal "/v1/…" paths with no template expressions or params.
// Drops entries containing ${}, ${, `:`, or spaces so dynamic paths like
// `/v1/resources/pods/${ns}/${name}` and `/v1/resources/:kind` are skipped —
// those aren't resolvable without knowing live cluster state.
const PATH_REGEX = /["'`](\/v1\/[A-Za-z0-9/_-]+)(?:\?[^"'`]*)?["'`]/g;

// Paths we know are *expected* to be absent and should not be tested.
// Usually this means the literal is used as a concatenation base in the
// frontend (`${basePath}/alerts`) rather than called directly — the backend
// mounts the subpaths but not the base.
const IGNORE: RegExp[] = [
  // Flux notifications uses this as a concat base; real routes are
  // /v1/gitops/notifications/{status,alerts,providers,receivers}.
  /^\/v1\/gitops\/notifications$/,
  // Dashboard comments name this router group; mounted routes live below it,
  // including /v1/externalsecrets/{externalsecrets,status}.
  /^\/v1\/externalsecrets$/,
  // lib/api.ts tests this as a PREFIX, not a route: a 403 from any
  // /v1/auth/* endpoint must not re-enter the permission-refresh hook, since
  // that hook calls /v1/auth/me and would feed itself forever. The subpaths
  // are mounted; the prefix itself is not.
  /^\/v1\/auth\/$/,
  // A dashboard layout is a singleton per (owner, cluster, scope), addressed
  // as /v1/preferences/layouts/{scope} — GET and PUT, no POST and no list,
  // because the scope IS the address in both directions. So unlike the sibling
  // /v1/preferences/{views,pins} constants, which are real collection
  // endpoints, this literal is only ever a concatenation base and the backend
  // mounts nothing at it. Deleting this entry does not restore coverage of the
  // scoped route: that path carries a ${} and is skipped as dynamic either way.
  /^\/v1\/preferences\/layouts$/,
];

// Directory names to skip during the scan: generated build output, vendored
// deps, and other noise that would duplicate literals from the source tree.
const SKIP_DIRS = new Set([
  "_fresh",
  "node_modules",
  "dist",
  ".git",
  ".astro",
  "static",
  "public",
]);

function walk(dir: string, out: string[] = []): string[] {
  let entries: string[];
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const entry of entries) {
    if (SKIP_DIRS.has(entry)) continue;
    const p = path.join(dir, entry);
    const s = statSync(p);
    if (s.isDirectory()) {
      walk(p, out);
      // .astro added with the Bun migration: a page's frontmatter is where a
      // route-level fetch now lives, and it is plain TypeScript. Omitting the
      // extension here would have quietly shrunk this guard's coverage to the
      // islands alone.
      // Test files are excluded: a fixture string is not evidence that the
      // frontend calls that path. server/rewrites_test.ts, for instance,
      // asserts on the literal "/v1/extensions/resources/g/r/_/n" -- a
      // synthetic input to the rewrite function, which the backend has no
      // reason to mount and which this guard would otherwise report as a
      // route mismatch forever.
    } else if (
      /\.(ts|tsx|js|jsx|astro)$/.test(entry) &&
      !/_test\.(ts|tsx)$/.test(entry) &&
      !/\.spec\.(ts|tsx)$/.test(entry)
    ) {
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
