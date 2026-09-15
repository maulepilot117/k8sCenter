import { expect, test } from "../fixtures/base.ts";
import { readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";

/**
 * Route inventory (U11 step 5, covers AE1 / R11).
 *
 * navigation.spec.ts walks 49 hand-typed nav hrefs. That is the operator's
 * path through the app, not the app's route table: the Astro tree under
 * src/pages/ has ~190 files, so a typo'd or unported route outside the nav
 * had nothing failing on it. The Bun migration re-homed every one of them
 * (U8), which is precisely when a silently-dropped route is cheapest to
 * introduce and most expensive to notice.
 *
 * So this spec builds the inventory the way api-routes.spec.ts builds the
 * API-path inventory: from the filesystem, not from a list somebody has to
 * remember to update. Each file is classified by reading it, and each
 * category is asserted against its own expected outcome rather than one
 * flat "didn't 500" count:
 *
 *   - rendering pages    -> 200 + text/html, in one hop
 *   - redirect handlers  -> the exact 302 Location the file itself declares
 *   - non-routable paths -> not reachable as URLs
 *   - error surfaces     -> their own status (403/404/500), by design
 *
 * The requests go through the `request` fixture rather than page.goto: what
 * is under test is the router and the SSR shell, not hydration, and ~190
 * browser navigations would cost more runtime than the rest of the suite
 * combined.
 *
 * A detail page for a resource that does not exist is a "not found" UI
 * state rendered by the island after it fetches, NOT a server error -- the
 * frontmatter of these pages performs no fetch of its own (that is what
 * makes the shell renderable at all). So 200 is the correct expectation
 * even for the deliberately-nonexistent names substituted below, and a 500
 * is a real failure rather than an artefact of the fixture cluster being
 * bare.
 */

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PAGES_ROOT = path.resolve(__dirname, "../../frontend/src/pages");

/**
 * Values substituted for each dynamic segment Astro's router accepts.
 *
 * `[namespace]` / `[ns]` get the one namespace e2e/fixtures/k8s/ actually
 * seeds, so those URLs address something real. Everything else names a
 * resource that deliberately does NOT exist: the assertion is about the
 * route resolving and the shell rendering, and pinning it to a live object
 * would make the spec depend on cluster contents it does not control.
 *
 * Unmapped dynamic segments fail the inventory test below rather than
 * silently producing a literal "[whatever]" in the URL -- a new segment
 * name is exactly the kind of change that would otherwise turn a route
 * into a quiet 404 here.
 */
const DYNAMIC_SEGMENTS: Record<string, string> = {
  "[namespace]": "e2e-test",
  "[ns]": "e2e-test",
  "[name]": "e2e-nonexistent",
  "[kind]": "Deployment",
  // GitOps detail pages take the composite id documented in CLAUDE.md
  // ("tool:namespace:name") and decodeURIComponent it in frontmatter, so it
  // is encoded here -- an un-encoded ":" would still route, but encoding it
  // also exercises the decode the page actually performs.
  "[id]": encodeURIComponent("argocd:e2e-test:e2e-nonexistent"),
  // CRD group/resource for the extensions tree. Nothing needs to be
  // installed for the shell to render; SchemaForm/CRDResourceList discover
  // the CRD client-side and report its absence themselves.
  "[group]": "example.com",
  "[resource]": "widgets",
};

/** Page files that are error surfaces, and the status each must answer with. */
const ERROR_SURFACE_STATUS: Record<string, number> = {
  "403.astro": 403,
  "404.astro": 404,
  "500.astro": 500,
};

/**
 * Paths that must NOT resolve to a page.
 *
 * Astro treats any `_`-prefixed file or directory under src/pages/ as
 * private and non-routable (that rule is the entire reason KTD11's rewrite
 * in frontend/server/rewrites.ts exists). The Fresh tree kept its shell,
 * error and build artefacts at names of this shape, and U12 deletes that
 * tree -- these assert none of them became a reachable URL in the port.
 *
 * `_astro/` is deliberately absent: that IS a real, served directory (the
 * hashed asset bucket -- see server/static.ts's immutable-cache branch).
 * `/extensions/:group/:resource/_/:name` is deliberately absent too: the
 * KTD11 rewrite makes that one `_` path routable on purpose.
 */
const NON_ROUTABLE_PATHS = [
  "/_app",
  "/_layout",
  "/_error",
  "/_islands/Counter",
  "/_frsh/js/build-id/main.js",
];

const SKIP_DIRS = new Set(["node_modules", ".git", ".astro"]);

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
    if (statSync(p).isDirectory()) {
      walk(p, out);
    } else if (entry.endsWith(".astro")) {
      out.push(p);
    }
  }
  return out;
}

type Category = "rendering" | "redirect" | "error-surface";

interface RouteEntry {
  /** Path relative to src/pages, POSIX-separated (e.g. "workloads/pods.astro"). */
  file: string;
  /** The URL the route resolves at, with dynamic segments substituted. */
  url: string;
  category: Category;
  /** Redirects only: the destination and status the file itself declares. */
  redirectTo?: string;
  redirectStatus?: number;
  /** Error surfaces only: the status the page sets on itself. */
  errorStatus?: number;
}

/** Turns "config/configmaps/[namespace]/[name].astro" into a request URL. */
function toUrl(relFile: string): string {
  const segments = relFile.replace(/\.astro$/, "").split("/");
  if (segments[segments.length - 1] === "index") segments.pop();
  const substituted = segments.map((segment) => {
    if (!segment.startsWith("[")) return segment;
    const value = DYNAMIC_SEGMENTS[segment];
    if (!value) {
      throw new Error(
        `unmapped dynamic segment "${segment}" in ${relFile} -- add it to ` +
          `DYNAMIC_SEGMENTS so this route is actually exercised`,
      );
    }
    return value;
  });
  return `/${substituted.join("/")}`;
}

/**
 * Pulls the destination and status out of a `Astro.redirect("/x", 302)`
 * call. Read from the source rather than listed here on purpose: a spec
 * that restates the destination only proves somebody typed it twice.
 */
const REDIRECT_REGEX =
  /Astro\.redirect\(\s*["']([^"']+)["']\s*(?:,\s*(\d+))?\s*\)/;

function classify(): RouteEntry[] {
  return walk(PAGES_ROOT).map((absolute) => {
    const file = path.relative(PAGES_ROOT, absolute).split(path.sep).join("/");
    const source = readFileSync(absolute, "utf8");
    const url = toUrl(file);

    const redirect = REDIRECT_REGEX.exec(source);
    if (redirect) {
      return {
        file,
        url,
        category: "redirect" as const,
        redirectTo: redirect[1],
        redirectStatus: redirect[2] ? Number(redirect[2]) : 302,
      };
    }

    const errorStatus = ERROR_SURFACE_STATUS[file];
    if (errorStatus) {
      return { file, url, category: "error-surface" as const, errorStatus };
    }

    return { file, url, category: "rendering" as const };
  });
}

const routes = classify();
const rendering = routes.filter((r) => r.category === "rendering");
const redirects = routes.filter((r) => r.category === "redirect");
const errorSurfaces = routes.filter((r) => r.category === "error-surface");

test.describe("Route inventory", () => {
  /**
   * Defensive floors, the same guard api-routes.spec.ts's `paths.length > 10`
   * provides: if the scan walks the wrong tree (U12 deletes the Fresh one,
   * and a stale PAGES_ROOT would resolve to nothing), every per-route test
   * below silently stops existing and the suite passes vacuously.
   */
  test("the page tree was discovered and every category is populated", () => {
    expect(
      routes.length,
      `no .astro pages found under ${PAGES_ROOT} -- PAGES_ROOT is stale`,
    ).toBeGreaterThan(150);
    expect(rendering.length).toBeGreaterThan(150);
    // Six today (U8's inventory). A floor rather than an equality so adding
    // a seventh redirect is not a test failure; losing one is.
    expect(redirects.length).toBeGreaterThanOrEqual(6);
    expect(errorSurfaces.length).toBe(3);
  });

  /**
   * Astro would not route a `_`-prefixed page file even if one existed, so
   * a file added there is dead weight that looks live. Nothing in the tree
   * has one today; this fails the moment that stops being true.
   */
  test("no page file is `_`-prefixed, which Astro would not route", () => {
    const underscored = routes.filter((r) =>
      r.file.split("/").some((segment) => segment.startsWith("_")),
    );
    expect(
      underscored.map((r) => r.file),
      "Astro treats `_`-prefixed segments under src/pages/ as private -- " +
        "these files are unreachable. See frontend/server/rewrites.ts for " +
        "how the one intentional `_` URL (KTD11) is handled instead.",
    ).toEqual([]);
  });

  test.describe("rendering pages", () => {
    for (const route of rendering) {
      test(`${route.url} renders (${route.file})`, async ({ request }) => {
        // maxRedirects: 0 so a page that quietly grew a redirect fails here
        // instead of being followed into a 200 somewhere else.
        const res = await request.get(route.url, { maxRedirects: 0 });

        expect(
          res.status(),
          `${route.file} -> ${route.url} did not render. A detail page for a ` +
            `resource that does not exist must still return its shell; a 500 ` +
            `means the frontmatter threw, and a 404 means the route did not ` +
            `port.`,
        ).toBe(200);
        expect(res.headers()["content-type"]).toContain("text/html");
      });
    }
  });

  test.describe("redirect handlers", () => {
    for (const route of redirects) {
      test(`${route.url} redirects to ${route.redirectTo}`, async ({
        request,
      }) => {
        // One hop, asserted as a hop: following the redirect and checking the
        // final page would also pass if the handler 200'd and the app
        // client-side routed afterwards, which is a different behaviour with
        // a different (flash-of-wrong-page) failure mode.
        const res = await request.get(route.url, { maxRedirects: 0 });

        expect(res.status()).toBe(route.redirectStatus);
        expect(res.headers()["location"]).toBe(route.redirectTo);
      });
    }
  });

  /**
   * 403/404/500 are pages in the tree (Astro routes all three) whose whole
   * job is to answer with a non-200 status -- 404.astro and 500.astro are
   * also what Astro serves for an unmatched route and an unhandled render
   * exception respectively, and 403.astro is reached through
   * src/middleware.ts's rewrite. Asserting their status directly is what
   * keeps `Astro.response.status = ...` in each file from being dropped: the
   * markup would still render, just with a 200 that lies to every crawler,
   * monitor and fetch() caller.
   */
  test.describe("error surfaces", () => {
    for (const route of errorSurfaces) {
      test(`${route.url} answers ${route.errorStatus}`, async ({ request }) => {
        const res = await request.get(route.url, { maxRedirects: 0 });
        expect(res.status()).toBe(route.errorStatus);
        expect(res.headers()["content-type"]).toContain("text/html");
      });
    }
  });

  test.describe("non-routable paths", () => {
    for (const url of NON_ROUTABLE_PATHS) {
      test(`${url} is not reachable`, async ({ request }) => {
        const res = await request.get(url, { maxRedirects: 0 });
        expect(
          res.status(),
          `${url} resolved -- a shell/private path must not be a URL`,
        ).toBe(404);
      });
    }
  });

  /**
   * KTD11's rewrite, asserted from the outside. The operator-facing URL for
   * a cluster-scoped CRD detail page carries a literal `_` namespace
   * segment, which Astro cannot express as a page file; server/rewrites.ts
   * rewrites it onto the `cluster-scoped` segment before the router sees it.
   * The rendering-pages loop above already covers the `cluster-scoped`
   * spelling (it is a real page file); this covers the spelling operators
   * actually get, and asserts it is REWRITTEN rather than redirected -- a
   * redirect would change the address bar, which R19 forbids.
   */
  test("the cluster-scoped CRD `_` URL is rewritten, not redirected", async ({
    request,
  }) => {
    const underscored = "/extensions/example.com/widgets/_/e2e-nonexistent";
    const res = await request.get(underscored, { maxRedirects: 0 });
    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"]).toContain("text/html");
  });
});
