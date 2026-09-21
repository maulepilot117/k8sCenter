import { describe, expect, test } from "bun:test";
import {
  FLOW_VERDICTS,
  GATEWAY_API_PAGE_HREF,
  gatewayRoutesView,
  goldenSignalsServiceHref,
  goldenSignalsView,
  HUBBLE_FLOWS_PAGE_HREF,
  hubbleFlowsView,
} from "./networking.ts";

// The three networking cards' derived logic. Every rule below exists because
// the obvious reading of the payload is wrong in the reassuring direction:
//
//  - golden signals answers with ZEROS for a query Prometheus could not
//    serve, and a zero error rate is the sentence an operator most wants to
//    see;
//  - a flow verdict this build does not recognise is not a forwarded flow;
//  - a Gateway with no Programmed condition is not a working Gateway.
//
// None of it is a field read, so none of it belongs in a component (D-10).

// ---------------------------------------------------------------------------
// golden signals
// ---------------------------------------------------------------------------

/** The envelope the route answers with: `{ status, signals }`. */
function signals(over: Record<string, unknown> = {}) {
  return {
    status: { detected: "istio" },
    signals: {
      mesh: "istio",
      namespace: "prod",
      service: "checkout",
      available: true,
      rps: 12.5,
      errorRate: 0.02,
      p50Ms: 4,
      p95Ms: 18,
      p99Ms: 42,
      ...over,
    },
  };
}

describe("goldenSignalsView", () => {
  test("an empty payload does not throw and reports itself unreadable", () => {
    for (const payload of [null, undefined, "", 0, [], {}, { signals: 7 }]) {
      const view = goldenSignalsView(payload);
      expect(view.readable).toBe(false);
      expect(view.rps).toBeNull();
      expect(view.errorPercent).toBeNull();
    }
  });

  test("a complete answer carries every signal", () => {
    const view = goldenSignalsView(signals());
    expect(view.readable).toBe(true);
    expect(view.available).toBe(true);
    expect(view.namespace).toBe("prod");
    expect(view.service).toBe("checkout");
    expect(view.mesh).toBe("istio");
    expect(view.rps).toBe(12.5);
    expect(view.errorPercent).toBe(2);
    expect(view.p50Ms).toBe(4);
    expect(view.p95Ms).toBe(18);
    expect(view.p99Ms).toBe(42);
    expect(view.missing).toEqual([]);
  });

  test("a genuinely error-free service reads zero, and a nearly error-free one does not", () => {
    // The reason the fraction goes through `coveragePercent` rather than
    // Math.round: one request in a thousand failing is 0.1%, which plain
    // rounding prints as 0% -- a card claiming NOTHING is failing while
    // something is. 0 is reserved for the absolute.
    expect(goldenSignalsView(signals({ errorRate: 0 })).errorPercent).toBe(0);
    expect(goldenSignalsView(signals({ errorRate: 0.001 })).errorPercent).toBe(
      1,
    );
    expect(goldenSignalsView(signals({ errorRate: 1 })).errorPercent).toBe(100);
    expect(goldenSignalsView(signals({ errorRate: 0.999 })).errorPercent).toBe(
      99,
    );
  });

  test("an unavailable metrics backend yields no numbers at all", () => {
    // `available: false` comes with rps: 0 and errorRate: 0 in the payload,
    // because Go zero-values them. Rendering those would say "no traffic, no
    // errors" about a service nobody measured.
    const view = goldenSignalsView(
      signals({ available: false, reason: "metrics_unavailable" }),
    );
    expect(view.readable).toBe(true);
    expect(view.available).toBe(false);
    expect(view.reason).toBe("metrics_unavailable");
    expect(view.rps).toBeNull();
    expect(view.errorPercent).toBeNull();
    expect(view.p95Ms).toBeNull();
  });

  test("a partial answer nulls exactly the signals whose queries failed", () => {
    // The same defect one query at a time. `missingQueries` names the PromQL
    // identifiers the route could not answer, and each one answered with a
    // zero -- so the p99 below is real and the error rate is not.
    const view = goldenSignalsView(
      signals({ errorRate: 0, missingQueries: ["errorDen"] }),
    );
    expect(view.missing).toEqual(["errorDen"]);
    expect(view.errorPercent).toBeNull();
    expect(view.rps).toBe(12.5);
    expect(view.p99Ms).toBe(42);
  });

  test("either half of the error ratio failing nulls the rate", () => {
    expect(
      goldenSignalsView(signals({ missingQueries: ["errorNum"] })).errorPercent,
    ).toBeNull();
    expect(
      goldenSignalsView(signals({ missingQueries: ["errorDen"] })).errorPercent,
    ).toBeNull();
  });

  test("a failed latency query nulls only its own quantile", () => {
    const view = goldenSignalsView(signals({ missingQueries: ["p95"] }));
    expect(view.p50Ms).toBe(4);
    expect(view.p95Ms).toBeNull();
    expect(view.p99Ms).toBe(42);
  });

  test("a value that is not a finite number is not a zero", () => {
    const view = goldenSignalsView(
      signals({ rps: "12", errorRate: null, p50Ms: Number.NaN }),
    );
    expect(view.rps).toBeNull();
    expect(view.errorPercent).toBeNull();
    expect(view.p50Ms).toBeNull();
  });

  test("the service link addresses the service the numbers describe", () => {
    expect(goldenSignalsServiceHref("prod", "checkout")).toBe(
      "/networking/services/prod/checkout",
    );
    // Encoded, because this is a stored value becoming a URL. The shape check
    // in params.ts already refuses a slash; encoding is the second answer to
    // the same question, for a value that reached storage another way.
    expect(goldenSignalsServiceHref("a/b", "c d")).toBe(
      "/networking/services/a%2Fb/c%20d",
    );
  });
});

// ---------------------------------------------------------------------------
// Hubble flows
// ---------------------------------------------------------------------------

function flow(over: Record<string, unknown> = {}) {
  return {
    time: "2026-09-20T10:00:00Z",
    verdict: "FORWARDED",
    direction: "INGRESS",
    srcNamespace: "prod",
    srcPod: "checkout-abc",
    dstNamespace: "prod",
    dstPod: "cart-def",
    protocol: "TCP",
    dstPort: 8080,
    ...over,
  };
}

describe("hubbleFlowsView", () => {
  test("an empty payload does not throw and reports itself unreadable", () => {
    for (const payload of [null, undefined, "", 0, {}]) {
      const view = hubbleFlowsView(payload, 5);
      expect(view.readable).toBe(false);
      expect(view.total).toBe(0);
      expect(view.rows).toEqual([]);
    }
  });

  test("an empty list is readable and clear, not unreadable", () => {
    // The distinction the family status buys: Hubble is installed (the shell
    // checked) and saw nothing, which is a finding rather than a gap.
    const view = hubbleFlowsView([], 5);
    expect(view.readable).toBe(true);
    expect(view.total).toBe(0);
    expect(view.blockedPercent).toBeNull();
    expect(view.rows).toEqual([]);
  });

  test("verdicts are tallied and the blocked share is computed over the whole batch", () => {
    const view = hubbleFlowsView(
      [
        flow(),
        flow(),
        flow({ verdict: "DROPPED", dropReason: "POLICY_DENIED" }),
        flow({ verdict: "ERROR" }),
        flow({ verdict: "AUDIT" }),
      ],
      5,
    );
    expect(view.readable).toBe(true);
    expect(view.total).toBe(5);
    expect(view.counts).toEqual({
      forwarded: 2,
      dropped: 1,
      error: 1,
      audit: 1,
      unknown: 0,
    });
    // Dropped and errored, not audited: an audit verdict is a policy that
    // WOULD have dropped the flow and did not, so counting it as blocked
    // would report traffic as broken that is flowing.
    expect(view.blockedPercent).toBe(40);
  });

  test("a verdict this build does not recognise is counted apart, never as forwarded", () => {
    const view = hubbleFlowsView(
      [flow({ verdict: "VERDICT_UNKNOWN" }), flow({ verdict: 7 })],
      5,
    );
    expect(view.counts.unknown).toBe(2);
    expect(view.counts.forwarded).toBe(0);
  });

  test("a single dropped flow does not round away to nothing", () => {
    const many = Array.from({ length: 999 }, () => flow());
    const view = hubbleFlowsView([...many, flow({ verdict: "DROPPED" })], 5);
    expect(view.blockedPercent).toBe(1);
  });

  test("rows lead with what is broken, newest first, and stop at the limit", () => {
    const view = hubbleFlowsView(
      [
        flow({ time: "2026-09-20T10:00:00Z" }),
        flow({ time: "2026-09-20T10:00:01Z", verdict: "DROPPED" }),
        flow({ time: "2026-09-20T10:00:09Z", verdict: "ERROR" }),
        flow({ time: "2026-09-20T10:00:05Z", verdict: "DROPPED" }),
      ],
      2,
    );
    expect(view.rows.map((r) => r.time)).toEqual([
      "2026-09-20T10:00:09Z",
      "2026-09-20T10:00:05Z",
    ]);
    expect(view.shown).toBe(2);
    expect(view.blocked).toBe(3);
  });

  test("a forwarded flow is shown only when nothing was blocked", () => {
    // The card is about what is not getting through. With drops to show it
    // shows drops; with none, showing the traffic that IS flowing is more
    // useful than an empty panel.
    const view = hubbleFlowsView([flow(), flow()], 5);
    expect(view.rows).toHaveLength(2);
    expect(view.blocked).toBe(0);
  });

  test("an endpoint is named by pod, then service, then address", () => {
    const rows = hubbleFlowsView(
      [
        flow({ verdict: "DROPPED" }),
        flow({
          verdict: "DROPPED",
          srcPod: "",
          srcNamespace: "",
          srcService: "prod/gateway",
        }),
        flow({
          verdict: "DROPPED",
          srcPod: "",
          srcNamespace: "",
          srcIP: "10.0.0.7",
        }),
      ],
      5,
    ).rows;
    expect(rows.map((r) => r.source)).toEqual([
      "prod/checkout-abc",
      "prod/gateway",
      "10.0.0.7",
    ]);
  });

  test("an entry naming neither endpoint is left out and counted", () => {
    const view = hubbleFlowsView(
      [flow({ verdict: "DROPPED" }), "not a flow", { verdict: "DROPPED" }],
      5,
    );
    expect(view.rows).toHaveLength(1);
    expect(view.unreadableRows).toBe(2);
    // Counted in the total anyway: the route returned three flows and the
    // card must not claim it saw one.
    expect(view.total).toBe(3);
  });

  test("every verdict bucket has a counter, so a new one cannot land silently", () => {
    const view = hubbleFlowsView([], 5);
    expect(Object.keys(view.counts).sort()).toEqual([...FLOW_VERDICTS].sort());
  });

  test("the flows page is the REST route's own surface", () => {
    expect(HUBBLE_FLOWS_PAGE_HREF).toBe("/networking/flows");
  });
});

// ---------------------------------------------------------------------------
// Gateway API
// ---------------------------------------------------------------------------

function gateway(over: Record<string, unknown> = {}) {
  return {
    name: "edge",
    namespace: "infra",
    gatewayClassName: "istio",
    listeners: [{ name: "http", port: 80, protocol: "HTTP" }],
    attachedRouteCount: 2,
    conditions: [
      { type: "Accepted", status: "True" },
      { type: "Programmed", status: "True" },
    ],
    ...over,
  };
}

function route(over: Record<string, unknown> = {}) {
  return {
    name: "checkout",
    namespace: "prod",
    hostnames: ["shop.example"],
    parentRefs: [{ kind: "Gateway", name: "edge", namespace: "infra" }],
    backendCount: 1,
    ...over,
  };
}

describe("gatewayRoutesView", () => {
  test("an empty payload does not throw and reports itself unreadable", () => {
    for (const payload of [null, undefined, "", 0, {}]) {
      const view = gatewayRoutesView(payload, payload, 5);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("either half being unreadable makes the card unreadable", () => {
    expect(gatewayRoutesView([gateway()], null, 5).readable).toBe(false);
    expect(gatewayRoutesView(null, [route()], 5).readable).toBe(false);
  });

  test("gateways with no routes at all still render", () => {
    // Gateway API installed, a Gateway declared, nothing routed through it --
    // which is a finding, and a different one from "no Gateway API".
    const view = gatewayRoutesView([gateway({ attachedRouteCount: 0 })], [], 5);
    expect(view.readable).toBe(true);
    expect(view.gatewayCount).toBe(1);
    expect(view.routeCount).toBe(0);
    expect(view.rows).toHaveLength(1);
    expect(view.rows[0].attachedRoutes).toBe(0);
  });

  test("routes spread across several gateways are attributed to each", () => {
    const view = gatewayRoutesView(
      [
        gateway({ name: "edge", attachedRouteCount: 2 }),
        gateway({ name: "internal", attachedRouteCount: 1 }),
      ],
      [
        route({ name: "a" }),
        route({ name: "b" }),
        route({
          name: "c",
          parentRefs: [{ kind: "Gateway", name: "internal" }],
          namespace: "infra",
        }),
      ],
      5,
    );
    expect(view.gatewayCount).toBe(2);
    expect(view.routeCount).toBe(3);
    expect(view.unattached).toBe(0);
    expect(view.rows.map((r) => r.name).sort()).toEqual(["edge", "internal"]);
  });

  test("a parentRef with no namespace means the route's own namespace", () => {
    // Gateway API's default, and getting it wrong in the other direction
    // would report every same-namespace route as an orphan.
    const view = gatewayRoutesView(
      [gateway({ name: "edge", namespace: "prod" })],
      [route({ namespace: "prod", parentRefs: [{ name: "edge" }] })],
      5,
    );
    expect(view.unattached).toBe(0);
  });

  test("a route whose parent is not a gateway we can see is counted as unattached", () => {
    const view = gatewayRoutesView(
      [gateway({ name: "edge", namespace: "infra" })],
      [
        route(),
        route({ name: "orphan", parentRefs: [{ name: "gone" }] }),
        route({ name: "parentless", parentRefs: [] }),
      ],
      5,
    );
    expect(view.routeCount).toBe(3);
    expect(view.unattached).toBe(2);
  });

  test("a parentRef naming something other than a Gateway does not attach", () => {
    const view = gatewayRoutesView(
      [gateway({ name: "edge", namespace: "infra" })],
      [
        route({
          parentRefs: [{ kind: "Service", name: "edge", namespace: "infra" }],
        }),
      ],
      5,
    );
    expect(view.unattached).toBe(1);
  });

  test("a gateway that is not programmed is counted and sorted to the front", () => {
    const view = gatewayRoutesView(
      [
        gateway({ name: "healthy" }),
        gateway({
          name: "broken",
          conditions: [
            { type: "Accepted", status: "True" },
            { type: "Programmed", status: "False" },
          ],
        }),
      ],
      [],
      5,
    );
    expect(view.notReady).toBe(1);
    expect(view.rows[0].name).toBe("broken");
    expect(view.rows[0].ready).toBe(false);
  });

  test("a gateway with no conditions is unknown, never ready", () => {
    // A Gateway the controller has not touched has no Programmed condition at
    // all. Reading its absence as health is the exact failure this release
    // exists to prevent, one object down.
    const view = gatewayRoutesView(
      [gateway({ name: "fresh", conditions: [] })],
      [],
      5,
    );
    expect(view.rows[0].ready).toBeNull();
    expect(view.notReady).toBe(1);
  });

  test("an entry that names no gateway is left out and counted", () => {
    const view = gatewayRoutesView(
      [gateway(), "not a gateway", { namespace: "infra" }],
      [],
      5,
    );
    expect(view.rows).toHaveLength(1);
    expect(view.unreadableRows).toBe(2);
    expect(view.gatewayCount).toBe(3);
  });

  test("rows stop at the limit and the view says how many there were", () => {
    const view = gatewayRoutesView(
      Array.from({ length: 9 }, (_, i) => gateway({ name: `gw-${i}` })),
      [],
      3,
    );
    expect(view.rows).toHaveLength(3);
    expect(view.gatewayCount).toBe(9);
  });

  test("the Gateway API page is under its own prefix, not the mesh one", () => {
    expect(GATEWAY_API_PAGE_HREF).toBe("/networking/gateway-api");
  });
});
