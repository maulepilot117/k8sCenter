import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/base.ts";
import { E2E_PASSWORD, E2E_USERNAME, loginViaApi } from "../helpers.ts";
import {
  encodeBearerSubprotocol,
  WS_AUTH_BEARER_PROTOCOL_PREFIX,
  WS_AUTH_SENTINEL_PROTOCOL,
} from "../../frontend/src/lib/ws-exec-auth.ts";

/**
 * Every WebSocket channel the bridge relays (U11 step 5, covers AE2 / R4).
 *
 * websocket.spec.ts proves ONE channel -- resource events -- and proves it
 * the way an operator uses it, through the app's own socket. The other five
 * paths in frontend/server/ws-allowlist.ts had no coverage at all, which is
 * the gap the migration plan calls out: the WS surfaces are the ones with
 * the least regression coverage, and a live failure there means it was
 * never tested.
 *
 * These tests drive raw browser sockets instead of the app's islands. That
 * is deliberate: the thing under test is the bridge in
 * frontend/server/ws-proxy.ts -- its allowlist, its credential handling and
 * its close-code remap -- not LogViewer's or PodTerminal's UI. A raw socket
 * reaches every channel including the ones whose UI needs a component
 * (Loki, Hubble) that the fixture cluster does not run.
 *
 * What the fixture cluster can and cannot prove
 * --------------------------------------------
 * e2e/fixtures/k8s/ seeds a namespace and a cluster-admin binding into kind.
 * There is no Loki, no Hubble/Cilium, and no Alertmanager, and the backend
 * mounts /api/v1/ws/flows and /api/v1/ws/logs-search only when those
 * components are discovered (backend/internal/server/routes.go). So for
 * those channels the assertion is the DOCUMENTED UNAVAILABLE behaviour, not
 * a skip: the bridge completes the client handshake before its own backend
 * leg resolves, so the socket opens and is then torn down with a remapped
 * close code and no data frame. Asserting that is what keeps "the channel
 * is wired end to end" distinct from "the channel silently hangs".
 *
 * /ws/v1/ws/alerts is a special case worth naming: the allowlist permits it
 * (ported verbatim from the Fresh route, R6) but the backend mounts no such
 * route -- see routes.go, which has resources, flows, logs-search, logs and
 * exec and nothing else. It is covered here as the allowlisted-but-unserved
 * case rather than quietly dropped.
 *
 * Credential handling
 * -------------------
 * Four channels authenticate in-band: the first message the client sends
 * carries the JWT, and the bridge queues it until the backend leg opens.
 * Exec cannot -- backend/internal/server/routes.go gates it with
 * middleware.Auth at upgrade time -- and a browser cannot put a header on a
 * WebSocket handshake, so the credential travels in a
 * Sec-WebSocket-Protocol value (src/lib/ws-exec-auth.ts). The server must
 * echo back only the plain sentinel; echoing the credential-bearing value
 * would put the token in a response header. Both properties are asserted
 * below, as is the one that motivated the whole design: the credential
 * never appears in a URL, which an ingress in front of the frontend pod
 * logs by default.
 */

/** Namespace seeded by e2e/fixtures/k8s/ -- the one that reliably exists. */
const NAMESPACE = "e2e-test";
/** Deliberately absent: these tests assert routing and auth, not workloads. */
const POD = "e2e-nonexistent";
const CONTAINER = "app";

/** Result of one raw socket probe, collected inside the page. */
interface WsProbe {
  /** The URL the browser actually opened, after its own normalisation. */
  url: string;
  opened: boolean;
  /** The subprotocol the server echoed, or "" when it echoed none. */
  protocol: string;
  /** Text frames received, oldest first. */
  frames: string[];
  closed: boolean;
  closeCode: number | null;
  closeReason: string;
}

interface WsProbeOptions {
  /** Path under the frontend origin, e.g. "/ws/v1/ws/resources". */
  path: string;
  /** Subprotocols to offer; empty for every channel except exec. */
  protocols?: string[];
  /** Messages to send once the socket opens, in order. */
  send?: string[];
  /** Stop after this many text frames. */
  maxFrames?: number;
  /** Give up after this long and report whatever was collected. */
  waitMs?: number;
}

/**
 * Opens one WebSocket from inside the page and reports what happened.
 *
 * Runs in the browser rather than through a Node client on purpose: the
 * browser is the only client that cannot set handshake headers, which is
 * the entire reason the exec channel's credential design exists. A Node
 * client would pass a test the product would fail.
 */
async function probeWs(
  page: Page,
  options: WsProbeOptions,
): Promise<WsProbe> {
  return await page.evaluate(
    ({ path, protocols, send, maxFrames, waitMs }) => {
      return new Promise<WsProbe>((resolve) => {
        const scheme = location.protocol === "https:" ? "wss:" : "ws:";
        const target = `${scheme}//${location.host}${path}`;
        const result: WsProbe = {
          url: target,
          opened: false,
          protocol: "",
          frames: [],
          closed: false,
          closeCode: null,
          closeReason: "",
        };

        let socket: WebSocket;
        try {
          socket = protocols.length
            ? new WebSocket(target, protocols)
            : new WebSocket(target);
        } catch {
          resolve(result);
          return;
        }
        // Report the URL the browser normalised to, not the one we asked
        // for -- that is the string an ingress would log.
        result.url = socket.url;

        let settled = false;
        const finish = () => {
          if (settled) return;
          settled = true;
          clearTimeout(timer);
          try {
            socket.close();
          } catch {
            /* already closing */
          }
          resolve(result);
        };
        const timer = setTimeout(finish, waitMs);

        socket.onopen = () => {
          result.opened = true;
          result.protocol = socket.protocol;
          for (const message of send) socket.send(message);
        };
        socket.onmessage = (event) => {
          if (typeof event.data !== "string") return;
          result.frames.push(event.data);
          if (result.frames.length >= maxFrames) finish();
        };
        socket.onclose = (event) => {
          result.closed = true;
          result.closeCode = event.code;
          result.closeReason = event.reason;
          finish();
        };
      });
    },
    {
      path: options.path,
      protocols: options.protocols ?? [],
      send: options.send ?? [],
      maxFrames: options.maxFrames ?? 1,
      waitMs: options.waitMs ?? 10_000,
    },
  );
}

/**
 * The invariant that holds for every channel, asserted on every probe: no
 * credential in the URL of the leg the browser opened.
 *
 * The bridge's backend leg cannot carry one either, and not by convention:
 * checkWsPath (ws-allowlist.ts) splits the query string off before the path
 * is validated, and bridgeConnection builds the outbound URL from that
 * stripped path -- so a query parameter cannot survive into the backend
 * URL even if a client puts one there. That leg is invisible from a
 * browser, so it is asserted where it can be: ws-proxy_test.ts and
 * ws-allowlist_test.ts under `bun test`.
 */
function expectNoCredentialInUrl(probe: WsProbe, token: string) {
  expect(probe.url, "a WS URL must carry no query string").not.toContain("?");
  expect(probe.url).not.toContain(token);
  expect(probe.url).not.toContain("access_token");
  expect(probe.url).not.toContain(WS_AUTH_BEARER_PROTOCOL_PREFIX);
}

/**
 * The same assertion, against a URL the APPLICATION built.
 *
 * `expectNoCredentialInUrl` above checks `probe.url`, which `probeWs`
 * constructs from this file's own literal -- so it restates the test's input
 * and cannot fail no matter what the app does. That is worth keeping as a
 * guard on the probe itself, but it is not evidence about the product.
 *
 * `page.on("websocket")` observes every socket the page actually opens, so
 * this one fails if the island ever puts the credential back in the query
 * string. That is the property the whole subprotocol design exists for.
 */
async function expectAppOpensCredentialFreeSockets(
  page: Page,
  token: string,
  open: () => Promise<void>,
): Promise<string[]> {
  const urls: string[] = [];
  page.on("websocket", (ws) => urls.push(ws.url()));
  await open();
  for (const url of urls) {
    expect(url, "the app must not put a credential in a WS URL").not.toContain(
      "?",
    );
    expect(url).not.toContain(token);
    expect(url).not.toContain("access_token");
    expect(url).not.toContain(WS_AUTH_BEARER_PROTOCOL_PREFIX);
  }
  return urls;
}

/**
 * The shape a channel takes when the backend does not serve it: the bridge
 * accepts the client handshake immediately (it cannot know yet whether the
 * backend leg will succeed), then tears the socket down when its own
 * outbound handshake fails, with no data frame ever delivered.
 *
 * The close code is asserted as remapCloseCode's contract rather than as
 * the literal 1000 it produces today: a failed outbound handshake surfaces
 * as 1006, which is receive-only and throws if passed to .close(), so the
 * bridge remaps it into range. "In range and never 1006" is the property
 * that must hold; the concrete value is 1000.
 */
function expectChannelUnavailable(probe: WsProbe, channel: string) {
  expect(
    probe.opened,
    `${channel}: the bridge should complete the client handshake before its backend leg resolves`,
  ).toBe(true);
  expect(probe.closed, `${channel}: the socket should be torn down`).toBe(true);
  expect(
    probe.frames,
    `${channel}: no data frame can arrive from a channel the backend does not serve`,
  ).toEqual([]);
  expect(probe.closeCode).not.toBeNull();
  expect(
    probe.closeCode,
    `${channel}: remapCloseCode must never surface 1006 to the client`,
  ).not.toBe(1006);
  expect(probe.closeCode).toBeGreaterThanOrEqual(1000);
  expect(probe.closeCode).toBeLessThanOrEqual(4999);
}

test.describe("WebSocket channels", () => {
  // Resolved once per worker, the way api-routes.spec.ts does it:
  // localStorage is origin-scoped, so one navigation has to happen before
  // storageState's token can be read back.
  //
  // The stored token is preferred over a fresh login on purpose. Every auth
  // endpoint shares one 5-request-per-minute bucket per IP (CLAUDE.md,
  // Configuration), and websocket.spec.ts already spends two of them
  // immediately before this file runs -- a login per worker here would sit
  // one flaky minute away from a 429 that looks like a WebSocket failure.
  // The fallback exists for the case where storageState carries no token at
  // all, where one request is the only option.
  let accessToken: string | null = null;

  test.beforeEach(async ({ page }) => {
    // A page with no resource table, so the only socket in flight is the one
    // each test opens itself.
    await page.goto("/privacy");
    if (accessToken === null) {
      accessToken = await page.evaluate(() =>
        localStorage.getItem("e2e_access_token"),
      );
    }
    if (accessToken === null) {
      accessToken = await loginViaApi(page, E2E_USERNAME, E2E_PASSWORD);
    }
  });

  /**
   * Also covered end to end by websocket.spec.ts, which asserts a live
   * create/delete reaching a rendered table. Kept here too because this
   * spec's claim is "every channel in the allowlist is exercised", and a
   * reader checking that claim should not have to know that one of the six
   * lives in a different file.
   */
  test("resources authenticates in-band and acknowledges", async ({ page }) => {
    const probe = await probeWs(page, {
      path: "/ws/v1/ws/resources",
      send: [JSON.stringify({ type: "auth", token: accessToken })],
    });

    expect(probe.opened).toBe(true);
    expect(
      probe.frames.length,
      "no frame means the hub never answered the in-band auth message",
    ).toBeGreaterThanOrEqual(1);
    expect(
      probe.frames[0],
      "the hub answers an accepted auth with auth_ok",
    ).toContain("auth_ok");
    expectNoCredentialInUrl(probe, accessToken as string);
  });

  /**
   * Log tail. The handler's protocol is auth, then a filter message, then
   * the stream; it confirms with a "subscribed" frame BEFORE opening the
   * k8s log stream, so that frame proves auth, the namespace/pod path
   * params and the RBAC check all landed. The pod does not exist, so the
   * documented next frame is the stream-open failure -- asserted rather
   * than skipped, because "no error either" would mean the handler never
   * got that far.
   */
  test("log tail streams and reports a missing pod", async ({ page }) => {
    const probe = await probeWs(page, {
      path: `/ws/v1/ws/logs/${NAMESPACE}/${POD}/${CONTAINER}`,
      send: [
        JSON.stringify({ type: "auth", token: accessToken }),
        JSON.stringify({ container: CONTAINER, tailLines: 10 }),
      ],
      maxFrames: 2,
    });

    expect(probe.opened).toBe(true);
    expect(
      probe.frames.length,
      "expected a subscribed frame then a stream-open error; got " +
        JSON.stringify(probe.frames),
    ).toBeGreaterThanOrEqual(2);
    expect(probe.frames[0]).toContain('"subscribed"');
    expect(
      probe.frames[1],
      "a tail of a pod that does not exist must report the failure, not hang",
    ).toContain('"error"');
    expectNoCredentialInUrl(probe, accessToken as string);
  });

  /**
   * Exec, the one channel whose credential rides on the handshake.
   *
   * Three things are asserted and each protects a separate decision:
   *
   *  1. The server echoes the sentinel and ONLY the sentinel. `ws` defaults
   *     to echoing whichever subprotocol the client listed first, which here
   *     is the credential-bearing one -- selectWsSubprotocol exists solely to
   *     stop that, and without this assertion it can be removed silently.
   *  2. The credential is nowhere in the URL (expectNoCredentialInUrl).
   *  3. A backend-originated frame arrives. That is the load-bearing one:
   *     the backend gates exec with middleware.Auth at upgrade time, so a
   *     frame from the handler can only exist if the token made it out of
   *     the subprotocol, into an Authorization header on the outbound leg,
   *     and past that middleware. The pod does not exist, so the frame is
   *     the handler's "no shell found in container" error -- which is a
   *     backend answer, not a bridge one.
   */
  test("exec carries its credential in a subprotocol, not a URL", async ({
    page,
  }) => {
    const probe = await probeWs(page, {
      path: `/ws/v1/ws/exec/${NAMESPACE}/${POD}/${CONTAINER}`,
      protocols: [
        encodeBearerSubprotocol(accessToken as string),
        WS_AUTH_SENTINEL_PROTOCOL,
      ],
      waitMs: 15_000,
    });

    expect(probe.opened).toBe(true);
    expect(probe.protocol).toBe(WS_AUTH_SENTINEL_PROTOCOL);
    expect(
      probe.protocol,
      "echoing the credential-bearing subprotocol would put the token in a response header",
    ).not.toContain(WS_AUTH_BEARER_PROTOCOL_PREFIX);
    expectNoCredentialInUrl(probe, accessToken as string);

    expect(
      probe.frames.length,
      "no frame means the backend's middleware.Auth rejected the outbound leg -- " +
        "the subprotocol credential did not survive the bridge",
    ).toBeGreaterThanOrEqual(1);
    expect(probe.frames[0]).toContain('"error"');
  });

  /**
   * The negative control for the test above, and the reason it proves
   * anything. Without it, "the socket opened and the sentinel came back"
   * would pass just as happily with the credential discarded -- the bridge
   * completes the client handshake regardless of what the backend does.
   * Offering only the sentinel leaves the outbound leg anonymous, the
   * backend's middleware.Auth 401s it, and the client socket is closed with
   * no frame ever delivered.
   */
  test("exec without the credential subprotocol is refused by the backend", async ({
    page,
  }) => {
    const probe = await probeWs(page, {
      path: `/ws/v1/ws/exec/${NAMESPACE}/${POD}/${CONTAINER}`,
      protocols: [WS_AUTH_SENTINEL_PROTOCOL],
    });

    expectChannelUnavailable(probe, "exec (anonymous)");
    expectNoCredentialInUrl(probe, accessToken as string);
  });

  /**
   * Allowlisted by the frontend, mounted by nothing. The allowlist was
   * ported verbatim from the Fresh route (R6) and kept this entry; the
   * backend has no /api/v1/ws/alerts handler. That is not a bug this spec
   * fixes -- it is a fact this spec pins, so the day the route appears (or
   * the day the allowlist entry is dropped) somebody has to come here and
   * say which one they meant.
   */
  test("alerts is allowlisted but not served by the backend", async ({
    page,
  }) => {
    const probe = await probeWs(page, {
      path: "/ws/v1/ws/alerts",
      send: [JSON.stringify({ type: "auth", token: accessToken })],
    });

    expectChannelUnavailable(probe, "alerts");
    expectNoCredentialInUrl(probe, accessToken as string);
  });

  /**
   * Hubble flows. The backend mounts this route only when a Hubble client
   * was discovered, and the kind fixture runs no Cilium -- so the
   * unavailable path is the real one here, and the streaming path can only
   * be covered by a cluster fixture that installs Cilium.
   */
  test("flows reports unavailable without Hubble", async ({ page }) => {
    const probe = await probeWs(page, {
      path: "/ws/v1/ws/flows",
      send: [JSON.stringify({ type: "auth", token: accessToken })],
    });

    expectChannelUnavailable(probe, "flows");
    expectNoCredentialInUrl(probe, accessToken as string);
  });

  /**
   * Loki log search. Same shape as flows, one layer further in: the route
   * IS mounted (the Loki handler is always constructed), but the handler
   * answers 503 before the upgrade when discovery found no Loki service, so
   * the observable outcome at the browser is identical.
   */
  test("logs-search reports unavailable without Loki", async ({ page }) => {
    const probe = await probeWs(page, {
      path: "/ws/v1/ws/logs-search",
      send: [
        JSON.stringify({ type: "auth", token: accessToken }),
        JSON.stringify({
          type: "subscribe",
          query: '{namespace="e2e-test"}',
          namespace: NAMESPACE,
          limit: 5,
        }),
      ],
    });

    expectChannelUnavailable(probe, "logs-search");
    expectNoCredentialInUrl(probe, accessToken as string);
  });
});

/**
 * The exec credential, as the product actually sends it.
 *
 * Everything else in this file drives a probe socket the spec builds itself.
 * This one drives the real terminal island and watches what the browser
 * opens, because the credential-in-URL claim is only worth anything when it
 * is measured on the application's own URL.
 */
test("the pod terminal opens a socket with no credential in its URL", async ({
  page,
}) => {
  const accessToken = await page.evaluate(() =>
    globalThis.localStorage.getItem("kc.accessToken"),
  );
  test.skip(!accessToken, "no access token in this session");

  const urls = await expectAppOpensCredentialFreeSockets(
    page,
    accessToken as string,
    async () => {
      // The pod does not exist in the kind fixture; the island still builds
      // and opens the socket, which is the part under test.
      await page.goto(
        `/workloads/pods/${NAMESPACE}/${POD}?tab=terminal`,
        { waitUntil: "domcontentloaded" },
      );
      await page.waitForTimeout(2_000);
    },
  );

  const exec = urls.filter((u) => u.includes("/ws/v1/ws/exec/"));
  expect(
    exec.length,
    "the terminal island did not open an exec socket -- if the tab moved, " +
      "update this test rather than deleting it: it is the only place the " +
      "app's own URL is checked",
  ).toBeGreaterThan(0);
});
