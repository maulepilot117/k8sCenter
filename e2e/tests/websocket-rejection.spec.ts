import { expect, test } from "../fixtures/base.ts";
import { readFileSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { checkWsPath } from "../../frontend/server/ws-allowlist.ts";

/**
 * WebSocket rejection, asserted identically against the dev server and the
 * built server (U11 step 5, covers R6 / R18).
 *
 * R18 is not "the bridge rejects bad paths" -- ws-allowlist_test.ts proves
 * that under `bun test`, against the pure function, in milliseconds. R18 is
 * that the two servers do not DIVERGE: `astro dev` and `bun run start` are
 * different processes mounting the same dispatch two different ways
 * (server/dev-plugin.ts reaches into Vite's middleware stack and its
 * httpServer; server/prod.ts owns a node:http server outright), and the
 * original Fresh setup is exactly where that divergence bit -- websockets
 * worked in CI and hung locally, so the one environment that could prove
 * the feature never ran the test and the one that ran it could never pass.
 *
 * A unit test cannot see that class of bug, because both servers import the
 * same module and the bug is in the mounting. Only two live servers can.
 *
 * How both servers get covered
 * ----------------------------
 * playwright.config.ts starts ONE frontend server -- `astro dev` locally,
 * `bun run start` in CI -- so within a single run there is only one base
 * URL to talk to. Rather than spawn a second server from inside a spec
 * (which would make an already timing-sensitive suite depend on a cold
 * Vite start inside a 30-second test budget), this spec parameterises over
 * base URLs: the configured one always, plus whatever E2E_ALT_BASE_URL
 * points at. The CI job sets that to the other server, and the identical
 * assertions then run against both in one pass.
 *
 * KNOWN GAP, stated rather than papered over: with E2E_ALT_BASE_URL unset,
 * this spec proves the rejection contract on ONE server per run -- the dev
 * server locally, the built server in CI -- so the cross-server claim rests
 * on the two runs plus the shared-implementation assertion at the bottom of
 * this file. That assertion is what makes the single-server case worth
 * something: it fails if dev and prod ever stop routing through the same
 * ws-proxy.ts entry points, which is the only way they could start
 * answering differently.
 */

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SERVER_DIR = path.resolve(__dirname, "../../frontend/server");

interface RejectionCase {
  label: string;
  /** Sent verbatim; see the note on URL normalisation below. */
  path: string;
  status: 400 | 404;
  /**
   * checkWsPath's `reason`, which distinguishes the two 404s: "unknown" is
   * an endpoint under /ws/ that is not allowlisted, "outside" is a path the
   * bridge does not own at all. Only "outside" may be left alone under
   * `astro dev` (Vite's HMR socket lives there), so a payload's reason
   * decides whether both servers are even expected to answer the same way.
   * Every case here is deliberately one the bridge owns.
   */
  reason: "invalid" | "unknown";
  body: string;
}

/**
 * The payloads, and why each is spelled the way it is.
 *
 * Every one of these has to survive WHATWG URL normalisation, because
 * Playwright resolves a request path through `new URL(path, baseURL)`
 * before it goes out. That parser collapses dot segments -- and it treats
 * `%2e` as a dot while doing it, so the obvious traversal payload
 * `/ws/v1/ws/logs/%2e%2e/%2e%2e/etc` arrives at the server as
 * `/ws/v1/etc` and tests the WRONG branch (unknown endpoint, not invalid
 * path). The encoded-slash form below is a real traversal shape that is
 * NOT a dot segment, so it reaches the server intact and hits the guard it
 * is meant to hit.
 *
 * checkWsPath is imported and asserted against each payload for the same
 * reason: if a future Playwright or Node version normalises one of these
 * differently, the live status will stop matching the pure function's and
 * this spec will say so, instead of quietly testing a different path.
 */
const REJECTIONS: RejectionCase[] = [
  {
    label: "encoded traversal",
    // "../../etc" with the slashes percent-encoded, so it is one path
    // segment and no parser rewrites it away.
    path: "/ws/v1/ws/%2e%2e%2f%2e%2e%2fetc",
    status: 400,
    reason: "invalid",
    body: "Invalid WS path",
  },
  {
    label: "literal dot-dot inside a segment",
    path: "/ws/v1/ws/logs/..foo/bar/baz",
    status: 400,
    reason: "invalid",
    body: "Invalid WS path",
  },
  {
    label: "empty path segment",
    // The guard rejects "//" as well as "..": a doubled slash is how a
    // proxy in front can be talked into re-splitting a path.
    path: "/ws/v1//ws/resources",
    status: 400,
    reason: "invalid",
    body: "Invalid WS path",
  },
  {
    label: "outside the v1 namespace",
    path: "/ws/admin",
    status: 400,
    reason: "invalid",
    body: "Invalid WS path",
  },
  {
    label: "unknown endpoint under v1",
    path: "/ws/v1/ws/definitely-not-a-channel",
    status: 404,
    reason: "unknown",
    body: "Unknown WS endpoint",
  },
];

/** An allowlisted path, to prove the guard rejects by rule and not by reflex. */
const ALLOWED_PATH = "/ws/v1/ws/resources";

/**
 * The base URLs the assertions run against. "" means the configured
 * baseURL (relative request), so the primary leg needs no environment at
 * all; a second entry appears when E2E_ALT_BASE_URL names the other server.
 */
const ALT_BASE_URL = process.env.E2E_ALT_BASE_URL;
const BASES: Array<{ name: string; prefix: string }> = [
  { name: "configured server", prefix: "" },
  ...(ALT_BASE_URL
    ? [{ name: `alternate server (${ALT_BASE_URL})`, prefix: ALT_BASE_URL }]
    : []),
];

test.describe("WebSocket rejection", () => {
  for (const base of BASES) {
    test.describe(base.name, () => {
      for (const rejection of REJECTIONS) {
        test(`${rejection.label} -> ${rejection.status}`, async ({
          request,
        }) => {
          // The pure function and the live server must agree. This also
          // catches the payload being mangled in transit (see the note on
          // REJECTIONS above) rather than letting it test a different rule.
          const decision = checkWsPath(rejection.path);
          expect(decision.ok).toBe(false);
          expect(decision.ok === false && decision.status).toBe(
            rejection.status,
          );
          expect(decision.ok === false && decision.reason).toBe(
            rejection.reason,
          );

          const res = await request.get(`${base.prefix}${rejection.path}`, {
            maxRedirects: 0,
          });

          expect(
            res.status(),
            `${rejection.path} must be refused identically on every server`,
          ).toBe(rejection.status);
          expect(await res.text()).toBe(rejection.body);

          // The rejection leaves through dispatch's own `res`, which
          // inherits the headers applied at the top of the chain. A
          // rejection that loses them is still a rejection, and still a
          // regression (R12).
          expect(res.headers()["x-content-type-options"]).toBe("nosniff");
        });
      }

      test("an allowlisted path answers 426, not a rejection", async ({
        request,
      }) => {
        // The control. Without it, a guard that refused everything would
        // pass every test above -- and /ws/* would be dead on both servers.
        expect(checkWsPath(ALLOWED_PATH).ok).toBe(true);

        const res = await request.get(`${base.prefix}${ALLOWED_PATH}`, {
          maxRedirects: 0,
        });
        expect(res.status()).toBe(426);
        expect(await res.text()).toBe("Expected WebSocket upgrade");
      });
    });
  }

  /**
   * The same rejection over a real upgrade handshake rather than a plain
   * GET. These are two different code paths in ws-proxy.ts:
   * handleWsHttpRequest answers on the ServerResponse, while an actual
   * upgrade is answered by writeRawRejection on the raw net.Socket -- it
   * has no ServerResponse to call setHeader on, which is why headers.ts
   * exposes rawHeaderLines() at all.
   *
   * A browser cannot read the rejection's status code (the WebSocket API
   * exposes no handshake response), so what is asserted is the only thing
   * it can observe and the only thing that matters to a caller: the socket
   * never opens. Runs against the configured server only -- a second origin
   * would need its own page load, and the HTTP leg above already carries
   * the cross-server claim.
   */
  test("a non-allowlisted upgrade never opens", async ({ page }) => {
    await page.goto("/privacy");

    const opened = await page.evaluate(() => {
      return new Promise<boolean>((resolve) => {
        const scheme = location.protocol === "https:" ? "wss:" : "ws:";
        const socket = new WebSocket(
          `${scheme}//${location.host}/ws/v1/ws/definitely-not-a-channel`,
        );
        const timer = setTimeout(() => resolve(false), 8_000);
        socket.onopen = () => {
          clearTimeout(timer);
          resolve(true);
        };
        socket.onclose = () => {
          clearTimeout(timer);
          resolve(false);
        };
      });
    });

    expect(
      opened,
      "the bridge accepted an upgrade for a path its own allowlist refuses",
    ).toBe(false);
  });

  /**
   * The assertion that carries the cross-server claim when only one server
   * is running: dev and prod cannot answer differently if they route
   * through the same entry points.
   *
   * Source-level on purpose, in the same spirit as api-routes.spec.ts
   * reading the frontend tree: the failure being guarded against is
   * somebody adding a second, subtly different WS branch to one of the two
   * servers, and no amount of probing ONE of them can see that.
   */
  test("dev and prod mount one shared WS implementation", () => {
    const devPlugin = readFileSync(
      path.join(SERVER_DIR, "dev-plugin.ts"),
      "utf8",
    );
    const dispatch = readFileSync(path.join(SERVER_DIR, "dispatch.ts"), "utf8");
    const prod = readFileSync(path.join(SERVER_DIR, "prod.ts"), "utf8");

    // Both non-upgrade chains call the same 426/reject branch...
    for (const [name, source] of [
      ["dev-plugin.ts", devPlugin],
      ["dispatch.ts", dispatch],
    ] as const) {
      expect(source, `${name} no longer imports the shared WS branch`).toMatch(
        /import\s*\{[^}]*handleWsHttpRequest[^}]*\}\s*from\s*"\.\/ws-proxy\.ts"/s,
      );
      expect(source).toContain("handleWsHttpRequest(req, res)");
    }

    // ...and both servers attach the same 'upgrade' listener.
    expect(devPlugin).toContain("attachWsProxy(");
    expect(prod).toContain("attachWsProxy(");

    // One allowlist, not two. A second copy of this logic anywhere under
    // server/ is the divergence this whole spec exists to prevent.
    expect(
      readFileSync(path.join(SERVER_DIR, "ws-allowlist.ts"), "utf8"),
    ).toContain("export function checkWsPath");
    for (const source of [devPlugin, dispatch, prod]) {
      expect(source).not.toContain("function checkWsPath");
    }
  });
});
