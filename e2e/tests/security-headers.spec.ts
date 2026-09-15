import { expect, test } from "../fixtures/base.ts";
import { SECURITY_HEADERS } from "../../frontend/server/headers.ts";

/**
 * The five security headers, on three different kinds of response
 * (U11 step 5, covers R12).
 *
 * The E2E suite asserted no response header at all before this spec: it is
 * a behaviour suite, and a header is not behaviour anyone clicks. That is
 * exactly why a missing one survives -- the app looks identical without
 * `frame-ancestors 'none'`, right up until it is framed. The migration plan
 * lists "a security header missing on any response class" as a rollback
 * trigger, and nothing was in a position to notice.
 *
 * Three response kinds, because they leave the server through three
 * different branches of frontend/server/dispatch.ts and only the first one
 * is obvious:
 *
 *   1. an HTML page      -- Astro's handler, the fallthrough branch
 *   2. a hashed asset    -- server/static.ts, which answers before Astro
 *   3. a 404             -- Astro's own not-found page, a third path again
 *
 * applySecurityHeaders() runs at the very top of the dispatch so all three
 * inherit it (KTD3), which is a design decision this spec is the evidence
 * for. The same headers are applied in dev through server/dev-plugin.ts, so
 * this spec passes against either server (R18).
 *
 * The expected values are imported from server/headers.ts rather than
 * retyped. A copy here would pass whatever the server does as long as
 * somebody updated both -- which makes the spec a spelling checker, not a
 * guard. Importing the module means changing a header value in one place
 * changes what is asserted, and DELETING one fails the count assertion
 * below.
 */

const HEADER_NAMES = SECURITY_HEADERS.map(([name]) => name);

/**
 * Asserts the full header set on one response. Playwright lower-cases
 * header names in `headers()`, per HTTP/2 convention; headers.ts spells
 * them in canonical case for the raw-socket rejection path
 * (rawHeaderLines), so the lookup is normalised here.
 */
function expectSecurityHeaders(
  headers: Record<string, string>,
  what: string,
): void {
  for (const [name, value] of SECURITY_HEADERS) {
    expect(
      headers[name.toLowerCase()],
      `${what} is missing or has the wrong ${name}`,
    ).toBe(value);
  }
}

test.describe("Security headers @smoke", () => {
  /**
   * Defensive, in the same spirit as api-routes.spec.ts's path-count guard:
   * expectSecurityHeaders loops over whatever headers.ts exports, so an
   * empty or shortened list would make every assertion below pass while
   * asserting nothing.
   */
  test("all five headers are declared in one place", () => {
    expect(SECURITY_HEADERS.length).toBe(5);
    expect(HEADER_NAMES).toEqual([
      "Content-Security-Policy",
      "X-Frame-Options",
      "X-Content-Type-Options",
      "Referrer-Policy",
      "Permissions-Policy",
    ]);
  });

  test("an HTML page carries every header", async ({ request }) => {
    const res = await request.get("/", { maxRedirects: 0 });
    expect(res.status()).toBe(200);
    expect(res.headers()["content-type"]).toContain("text/html");
    expectSecurityHeaders(res.headers(), "the dashboard HTML response");
  });

  /**
   * A static asset, discovered from the page rather than hardcoded: the
   * filenames under /_astro/ carry a content hash that changes on every
   * build, so any literal here would be stale within a commit.
   *
   * Under the built server that discovery finds a hashed /_astro/ asset,
   * served by server/static.ts's immutable branch. Under `astro dev` there
   * is no /_astro/ bucket at all -- Vite serves the module graph instead --
   * so the fallback takes the first same-origin asset the page references.
   * Both are static responses that never reach Astro's handler, which is
   * the branch this test exists to cover; the immutable cache header is
   * asserted only in the case that actually has one.
   */
  test("a static asset carries every header", async ({ request }) => {
    const html = await (await request.get("/", { maxRedirects: 0 })).text();
    const references = [
      ...html.matchAll(/(?:src|href)="(\/[^"]+\.(?:js|css|mjs|svg|woff2?))"/g),
    ].map((match) => match[1]);

    expect(
      references.length,
      "the dashboard HTML referenced no same-origin static asset -- the " +
        "discovery regex is stale, and this test would otherwise assert nothing",
    ).toBeGreaterThan(0);

    const hashed = references.find((href) => href.startsWith("/_astro/"));
    const asset = hashed ?? references[0];

    const res = await request.get(asset, { maxRedirects: 0 });
    expect(res.status(), `${asset} did not resolve`).toBe(200);
    expectSecurityHeaders(res.headers(), `the static asset ${asset}`);

    if (hashed) {
      // server/static.ts's immutable/no-cache split, which only the hashed
      // bucket gets. Asserted here rather than in its own test because it is
      // the same response and the same branch.
      expect(res.headers()["cache-control"]).toBe(
        "public, max-age=31536000, immutable",
      );
    }
  });

  test("a 404 carries every header", async ({ request }) => {
    const res = await request.get("/no-such-page-e2e-security-headers", {
      maxRedirects: 0,
    });
    expect(res.status()).toBe(404);
    expectSecurityHeaders(res.headers(), "the 404 response");
  });
});
