import { describe, expect, test } from "bun:test";
import {
  clusterTopologyView,
  labelSelectorSelects,
  RELATED_LIMIT,
  serviceSelects,
  stepTopologyZoom,
  TOPOLOGY_MIN_ZOOM,
  topologyMaxZoom,
} from "./topology.ts";
import type { ResourceListPage } from "./wire-types.ts";

// The graph behind `cluster-topology`. Which pod a service fronts, which
// workload owns a pod and which claim a pod mounts are joins across seven
// payloads, not field reads, so they live here under test (D-10, KTD8).

const page = (items: unknown[], total = items.length): ResourceListPage => ({
  items,
  total,
});

const node = (name: string, ready = true) => ({
  metadata: { name },
  status: {
    conditions: [{ type: "Ready", status: ready ? "True" : "False" }],
  },
});

const pod = (
  ns: string,
  name: string,
  opts: {
    labels?: Record<string, string>;
    nodeName?: string;
    phase?: string;
    ready?: boolean;
    claims?: string[];
    containers?: string[];
  } = {},
) => ({
  metadata: { name, namespace: ns, labels: opts.labels ?? {} },
  spec: {
    nodeName: opts.nodeName,
    containers: (opts.containers ?? ["app"]).map((c) => ({ name: c })),
    volumes: (opts.claims ?? []).map((claimName) => ({
      name: claimName,
      persistentVolumeClaim: { claimName },
    })),
  },
  status: {
    phase: opts.phase ?? "Running",
    containerStatuses: [{ name: "app", ready: opts.ready ?? true }],
  },
});

const service = (ns: string, name: string, selector?: object) => ({
  metadata: { name, namespace: ns },
  spec: { selector },
});

const deployment = (
  ns: string,
  name: string,
  matchLabels: Record<string, string>,
  replicas = 1,
  readyReplicas = 1,
) => ({
  metadata: { name, namespace: ns },
  spec: { replicas, selector: { matchLabels } },
  status: { readyReplicas },
});

const pvc = (ns: string, name: string, phase = "Bound") => ({
  metadata: { name, namespace: ns },
  spec: { volumeName: `pv-${name}` },
  status: { phase },
});

describe("selectors", () => {
  test("an empty service selector selects nothing", () => {
    expect(serviceSelects({}, { app: "web" })).toBe(false);
  });

  test("a service selector needs every label", () => {
    expect(serviceSelects({ app: "web" }, { app: "web", tier: "x" })).toBe(
      true,
    );
    expect(serviceSelects({ app: "web", tier: "y" }, { app: "web" })).toBe(
      false,
    );
  });

  test("a label selector honours matchExpressions", () => {
    const sel = {
      matchLabels: { app: "web" },
      matchExpressions: [
        { key: "tier", operator: "In", values: ["front", "edge"] },
        { key: "canary", operator: "DoesNotExist" },
      ],
    };
    expect(labelSelectorSelects(sel, { app: "web", tier: "edge" })).toBe(true);
    expect(labelSelectorSelects(sel, { app: "web", tier: "back" })).toBe(false);
    expect(
      labelSelectorSelects(sel, { app: "web", tier: "front", canary: "1" }),
    ).toBe(false);
  });

  test("an empty, missing or unknown-operator selector fails closed", () => {
    expect(labelSelectorSelects({}, { app: "web" })).toBe(false);
    expect(labelSelectorSelects(undefined, { app: "web" })).toBe(false);
    expect(
      labelSelectorSelects(
        { matchExpressions: [{ key: "app", operator: "Gt", values: ["1"] }] },
        { app: "2" },
      ),
    ).toBe(false);
  });
});

describe("clusterTopologyView", () => {
  const full = () =>
    clusterTopologyView({
      nodes: page([node("n1"), node("n2", false)]),
      services: page([service("shop", "web", { app: "web" })]),
      deployments: page([deployment("shop", "web", { app: "web" }, 2, 2)]),
      statefulsets: page([]),
      daemonsets: page([]),
      pods: page([
        pod("shop", "web-1", {
          labels: { app: "web" },
          nodeName: "n1",
          claims: ["data"],
        }),
        pod("shop", "web-2", { labels: { app: "web" }, nodeName: "n2" }),
      ]),
      pvcs: page([pvc("shop", "data")]),
    });

  test("draws one node per object, with /-joined ids", () => {
    const view = full();
    expect(view.nodes.map((n) => n.id).sort()).toEqual([
      "node//n1",
      "node//n2",
      "pod/shop/web-1",
      "pod/shop/web-2",
      "pvc/shop/data",
      "service/shop/web",
      "workload/shop/Deployment/web",
    ]);
  });

  test("ids do not collide across a dash in the namespace", () => {
    const view = clusterTopologyView({
      pods: page([pod("a-b", "c"), pod("a", "b-c")]),
    });
    expect(new Set(view.nodes.map((n) => n.id)).size).toBe(2);
  });

  test("joins the five rows by their real relationships", () => {
    const kinds = full()
      .edges.map((e) => `${e.kind} ${e.from} ${e.to}`)
      .sort();
    expect(kinds).toEqual([
      "node-service node//n1 service/shop/web",
      "node-service node//n2 service/shop/web",
      "pod-pvc pod/shop/web-1 pvc/shop/data",
      "service-workload service/shop/web workload/shop/Deployment/web",
      "workload-pod workload/shop/Deployment/web pod/shop/web-1",
      "workload-pod workload/shop/Deployment/web pod/shop/web-2",
    ]);
  });

  test("a service reaches a bare pod directly", () => {
    const view = clusterTopologyView({
      services: page([service("ns", "s", { app: "x" })]),
      pods: page([pod("ns", "p", { labels: { app: "x" } })]),
    });
    expect(view.edges).toEqual([
      { from: "service/ns/s", to: "pod/ns/p", kind: "service-pod" },
    ]);
  });

  test("selectors do not cross namespaces", () => {
    const view = clusterTopologyView({
      services: page([service("a", "s", { app: "x" })]),
      pods: page([pod("b", "p", { labels: { app: "x" } })]),
    });
    expect(view.edges).toEqual([]);
  });

  test("grades health from status, not from kind", () => {
    const view = clusterTopologyView({
      nodes: page([node("bad", false)]),
      deployments: page([
        deployment("ns", "down", { app: "d" }, 3, 0),
        deployment("ns", "part", { app: "p" }, 3, 1),
      ]),
      pods: page([
        pod("ns", "crash", { ready: false }),
        pod("ns", "pending", { phase: "Pending" }),
        pod("ns", "failed", { phase: "Failed" }),
      ]),
      pvcs: page([pvc("ns", "lost", "Lost")]),
    });
    const health = Object.fromEntries(
      view.nodes.map((n) => [n.id, `${n.health} ${n.status}`]),
    );
    expect(health["node//bad"]).toBe("error NotReady");
    expect(health["workload/ns/Deployment/down"]).toBe("error 0/3 ready");
    expect(health["workload/ns/Deployment/part"]).toBe("warning 1/3 ready");
    expect(health["pod/ns/crash"]).toBe("warning Running, not ready");
    expect(health["pod/ns/pending"]).toBe("warning Pending");
    expect(health["pod/ns/failed"]).toBe("error Failed");
    expect(health["pvc/ns/lost"]).toBe("error Lost");
  });

  test("a selector service with no pods warns only over a complete pod page", () => {
    const svc = service("ns", "orphan", { app: "gone" });
    const complete = clusterTopologyView({
      services: page([svc]),
      pods: page([]),
    });
    expect(complete.nodes[0].health).toBe("warning");

    const capped = clusterTopologyView({
      services: page([svc]),
      pods: page([], 900),
    });
    expect(capped.nodes[0].health).toBe("healthy");

    const unread = clusterTopologyView({ services: page([svc]) });
    expect(unread.nodes[0].health).toBe("healthy");
  });

  test("related groups carry the full count past the display limit", () => {
    const pods = Array.from({ length: RELATED_LIMIT + 3 }, (_, i) =>
      pod("ns", `p${i}`, { nodeName: "n1" }),
    );
    const view = clusterTopologyView({
      nodes: page([node("n1")]),
      pods: page(pods),
    });
    const n1 = view.nodes.find((n) => n.id === "node//n1");
    expect(n1?.related).toEqual([
      {
        kind: "pod",
        items: expect.any(Array),
        total: RELATED_LIMIT + 3,
      },
    ]);
    expect(n1?.related[0].items).toHaveLength(RELATED_LIMIT);
  });

  test("reports unavailable, truncated and skipped rather than drawing them as empty", () => {
    const view = clusterTopologyView({
      pods: page([pod("ns", "p"), { metadata: {} }, "junk"], 800),
    });
    expect(view.rows.node.available).toBe(false);
    expect(view.rows.pod).toEqual({
      available: true,
      total: 800,
      counted: 1,
      truncated: true,
      readable: true,
    });
    expect(view.skipped).toBe(2);
  });

  test("workload coverage sums the three controller pages", () => {
    const view = clusterTopologyView({
      deployments: page([deployment("ns", "a", { app: "a" })], 600),
      statefulsets: page([]),
    });
    expect(view.rows.workload).toMatchObject({
      available: true,
      total: 600,
      counted: 1,
      truncated: true,
    });
  });

  test("survives every page being absent or malformed", () => {
    expect(() =>
      clusterTopologyView({
        nodes: null,
        pods: { items: null as unknown as unknown[], total: Number.NaN },
      }),
    ).not.toThrow();
  });

  test("lays every row out inside the canvas", () => {
    const view = full();
    for (const n of view.nodes) {
      expect(n.x).toBeGreaterThan(0);
      expect(n.x).toBeLessThan(view.width);
      expect(n.y).toBeGreaterThan(0);
      expect(n.y).toBeLessThan(view.height);
    }
  });

  test("encodes names into hrefs", () => {
    const view = clusterTopologyView({
      deployments: page([deployment("ns", "a b", { app: "x" })]),
    });
    expect(view.nodes[0].href).toBe("/workloads/deployments/ns/a%20b");
  });
});

describe("canvas", () => {
  const pods = (n: number) =>
    page(Array.from({ length: n }, (_, i) => pod("ns", `p${i}`)));

  // The canvas used to grow as tall as it was wide over 2.3. A card is far
  // wider than that, so a 100-pod map was fitted by its height and drawn in
  // the middle half of the card at a scale no label could be read at.
  test("a wide cluster keeps its rows a fixed distance apart", () => {
    const wide = clusterTopologyView({ pods: pods(105) });
    const wider = clusterTopologyView({ pods: pods(300) });
    expect(wider.width).toBeGreaterThan(wide.width);
    expect(wider.height).toBe(wide.height);
    // Wider than the default 8x6 card (about 975x217), so the fitted map
    // spans the card's width rather than a letterboxed strip of it.
    expect(wide.width / wide.height).toBeGreaterThan(975 / 217);
  });

  test("a small cluster is no taller than a wide one", () => {
    const small = clusterTopologyView({ pods: pods(1) });
    const wide = clusterTopologyView({ pods: pods(105) });
    expect(small.height).toBeLessThanOrEqual(wide.height);
    expect(small.height).toBeGreaterThan(0);
  });
});

describe("zoom", () => {
  const wide = { width: 6572, height: 560 };

  test("an unmeasured canvas gets the fixed ceiling", () => {
    expect(topologyMaxZoom(wide, 0, 0)).toBe(4);
    expect(topologyMaxZoom(wide, Number.NaN, 200)).toBe(4);
  });

  test("a map that already fits at full size keeps the fixed ceiling", () => {
    expect(topologyMaxZoom({ width: 400, height: 400 }, 975, 600)).toBe(4);
  });

  test("a wide map can always be zoomed until its smallest label reads", () => {
    const boxW = 975;
    const boxH = 217;
    const max = topologyMaxZoom(wide, boxW, boxH);
    const fit = Math.min(boxW / wide.width, boxH / wide.height);
    // The smallest label is drawn at 8 canvas units.
    expect(8 * fit * max).toBeGreaterThanOrEqual(12 - 1e-9);
    expect(max).toBeGreaterThan(4);
  });

  test("steps are multiplicative and reversible", () => {
    expect(stepTopologyZoom(1, 1, 4)).toBe(1.25);
    expect(stepTopologyZoom(1, -1, 4)).toBe(0.8);
    expect(stepTopologyZoom(stepTopologyZoom(1, 1, 4), -1, 4)).toBe(1);
  });

  test("steps clamp to both ends", () => {
    expect(stepTopologyZoom(3.9, 1, 4)).toBe(4);
    expect(stepTopologyZoom(4, 1, 4)).toBe(4);
    expect(stepTopologyZoom(0.55, -1, 4)).toBe(TOPOLOGY_MIN_ZOOM);
  });

  test("a large ceiling is reachable in a handful of clicks", () => {
    const max = topologyMaxZoom(wide, 975, 217);
    let z = 1;
    let clicks = 0;
    while (z < max && clicks < 50) {
      z = stepTopologyZoom(z, 1, max);
      clicks++;
    }
    expect(z).toBe(max);
    expect(clicks).toBeLessThanOrEqual(12);
  });
});
