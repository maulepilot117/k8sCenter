import { describe, expect, test } from "bun:test";
import {
  classifyHPA,
  classifyPDB,
  hpaHref,
  hpaStatusView,
  pdbHref,
  pdbRiskView,
  rankHPAs,
  rankPDBs,
} from "./autoscaling.ts";
import type { ResourceListPage } from "./wire-types.ts";

// The derived state behind `hpa-status` and `pdb-risk`.
//
// Both widgets read raw Kubernetes objects off the generic list route and both
// have to answer a question the object does not carry a field for: is THIS
// autoscaler out of room, and is THIS budget blocking a drain. Neither is a
// field read, so both live here under test rather than inside the components
// (D-10, KTD8).
//
// The direction the module errs in is the same one the workload roll-up errs
// in. An autoscaler whose metrics are unreadable reports `unknown`, never a
// zero that renders as "plenty of headroom", and a budget whose selector
// matches no pod is informational rather than a risk -- it allows no
// disruption because there is nothing to disrupt, which is not the state that
// blocks a node drain.

function hpa(
  over: {
    name?: string;
    namespace?: string;
    min?: number;
    max?: number;
    current?: number;
    desired?: number;
    /** [currentUtilization, targetUtilization] for a cpu Resource metric. */
    cpu?: [number | null, number | null];
    targetKind?: string;
    targetName?: string;
  } = {},
): unknown {
  const metrics =
    over.cpu && over.cpu[1] !== null
      ? [
          {
            type: "Resource",
            resource: {
              name: "cpu",
              target: { type: "Utilization", averageUtilization: over.cpu[1] },
            },
          },
        ]
      : [];
  const currentMetrics =
    over.cpu && over.cpu[0] !== null
      ? [
          {
            type: "Resource",
            resource: {
              name: "cpu",
              current: { averageUtilization: over.cpu[0] },
            },
          },
        ]
      : [];
  return {
    metadata: { name: over.name ?? "api", namespace: over.namespace ?? "prod" },
    spec: {
      minReplicas: over.min ?? 1,
      maxReplicas: over.max ?? 10,
      scaleTargetRef: {
        kind: over.targetKind ?? "Deployment",
        name: over.targetName ?? "api",
      },
      metrics,
    },
    status: {
      currentReplicas: over.current ?? 1,
      desiredReplicas: over.desired ?? over.current ?? 1,
      currentMetrics,
    },
  };
}

function pdb(
  over: {
    name?: string;
    namespace?: string;
    allowed?: number;
    healthy?: number;
    desired?: number;
    expected?: number;
  } = {},
): unknown {
  return {
    metadata: { name: over.name ?? "web", namespace: over.namespace ?? "prod" },
    status: {
      disruptionsAllowed: over.allowed ?? 1,
      currentHealthy: over.healthy ?? 3,
      desiredHealthy: over.desired ?? 2,
      expectedPods: over.expected ?? 3,
    },
  };
}

function page(items: unknown[], total?: number): ResourceListPage {
  return { items, total: total ?? items.length };
}

describe("classifyHPA", () => {
  test("an autoscaler at its ceiling with demand above target is saturated", () => {
    // The state worth surfacing. The replica numbers alone do not say it:
    // 10/10 is what a healthy pinned autoscaler and an out-of-room one both
    // print, and only the metric tells them apart.
    const h = classifyHPA(hpa({ max: 10, current: 10, cpu: [92, 70] }));
    expect(h.atCeiling).toBe(true);
    expect(h.demand).toBe("above");
    expect(h.state).toBe("saturated");
  });

  test("an autoscaler at its ceiling with demand met is not flagged", () => {
    // At max and coping. Nothing is wrong: it scaled up, the load was served
    // and it has not been asked for more than it can give. Flagging this
    // would put a permanent warning on every workload sized to its ceiling.
    const h = classifyHPA(hpa({ max: 10, current: 10, cpu: [40, 70] }));
    expect(h.atCeiling).toBe(true);
    expect(h.demand).toBe("met");
    expect(h.state).toBe("at-ceiling");
  });

  test("demand exactly at target counts as met, not above", () => {
    const h = classifyHPA(hpa({ max: 4, current: 4, cpu: [70, 70] }));
    expect(h.demand).toBe("met");
    expect(h.state).toBe("at-ceiling");
  });

  test("an autoscaler with no current metrics is unknown, not zero", () => {
    // The whole point. A missing currentMetrics means the metrics server has
    // not reported, and reading that as 0% utilization renders an autoscaler
    // nobody can measure as one with all the headroom in the world.
    const h = classifyHPA(hpa({ max: 10, current: 10, cpu: [null, 70] }));
    expect(h.utilization).toBeNull();
    expect(h.demand).toBe("unknown");
    expect(h.state).not.toBe("saturated");
  });

  test("a target with no readable utilization leaves demand unknown", () => {
    // An HPA driven by a Pods, Object or External metric carries a quantity
    // string, not a percent. Guessing its units to produce a number would be
    // worse than saying nothing.
    const h = classifyHPA({
      metadata: { name: "q", namespace: "prod" },
      spec: {
        maxReplicas: 5,
        metrics: [
          { type: "External", external: { target: { averageValue: "100" } } },
        ],
      },
      status: { currentReplicas: 5, desiredReplicas: 5, currentMetrics: [] },
    });
    expect(h.demand).toBe("unknown");
    expect(h.utilization).toBeNull();
  });

  test("metrics are paired by resource, not by array position", () => {
    // spec.metrics and status.currentMetrics are not ordered together, and
    // pairing them by index would compare memory usage against a CPU target.
    const h = classifyHPA({
      metadata: { name: "api", namespace: "prod" },
      spec: {
        maxReplicas: 10,
        metrics: [
          {
            type: "Resource",
            resource: {
              name: "cpu",
              target: { type: "Utilization", averageUtilization: 80 },
            },
          },
          {
            type: "Resource",
            resource: {
              name: "memory",
              target: { type: "Utilization", averageUtilization: 50 },
            },
          },
        ],
      },
      status: {
        currentReplicas: 10,
        desiredReplicas: 10,
        currentMetrics: [
          {
            type: "Resource",
            resource: { name: "memory", current: { averageUtilization: 90 } },
          },
          {
            type: "Resource",
            resource: { name: "cpu", current: { averageUtilization: 10 } },
          },
        ],
      },
    });
    // Paired by index this reads cpu 10 vs cpu 80 and memory 90 vs memory 50
    // the wrong way round; paired by resource, memory is over its target.
    expect(h.demand).toBe("above");
    expect(h.utilization).toBe(90);
    expect(h.targetUtilization).toBe(50);
  });

  test("an autoscaler below its ceiling and settled is steady", () => {
    const h = classifyHPA(hpa({ max: 10, current: 3, cpu: [30, 70] }));
    expect(h.atCeiling).toBe(false);
    expect(h.state).toBe("steady");
  });

  test("an autoscaler asking for more replicas than it has is scaling", () => {
    const h = classifyHPA(
      hpa({ max: 10, current: 3, desired: 6, cpu: [90, 70] }),
    );
    expect(h.state).toBe("scaling");
  });

  test("the ceiling is judged on desired replicas, not current", () => {
    // desired is what the controller decided this cycle; current lags it
    // while pods come up. An autoscaler that has just asked for its maximum
    // is at its ceiling whether or not the pods have started.
    const h = classifyHPA(
      hpa({ max: 10, current: 7, desired: 10, cpu: [95, 70] }),
    );
    expect(h.atCeiling).toBe(true);
    expect(h.state).toBe("saturated");
  });

  test("an unreadable object is unknown rather than a zeroed autoscaler", () => {
    for (const bad of [null, undefined, 42, "x", {}, { spec: 3 }]) {
      const h = classifyHPA(bad);
      expect(h.state).toBe("unknown");
      expect(h.atCeiling).toBe(false);
      expect(h.max).toBeNull();
    }
  });

  test("a non-finite replica count is unreadable, not a number", () => {
    const h = classifyHPA({
      metadata: { name: "a", namespace: "b" },
      spec: { maxReplicas: Number.NaN },
      status: { currentReplicas: Number.POSITIVE_INFINITY },
    });
    expect(h.max).toBeNull();
    expect(h.current).toBeNull();
    expect(h.state).toBe("unknown");
  });

  test("the scale target is rendered as Kind/name, and is empty when unreadable", () => {
    expect(
      classifyHPA(hpa({ targetKind: "StatefulSet", targetName: "db" })).target,
    ).toBe("StatefulSet/db");
    expect(classifyHPA({ metadata: {}, spec: {}, status: {} }).target).toBe("");
  });
});

describe("rankHPAs", () => {
  test("saturated leads, then pinned at the ceiling, then scaling", () => {
    const steady = classifyHPA(
      hpa({ name: "steady", current: 2, cpu: [10, 70] }),
    );
    const scaling = classifyHPA(
      hpa({ name: "scaling", current: 2, desired: 5, cpu: [90, 70] }),
    );
    const ceiling = classifyHPA(
      hpa({ name: "ceiling", max: 4, current: 4, cpu: [20, 70] }),
    );
    const saturated = classifyHPA(
      hpa({ name: "saturated", max: 4, current: 4, cpu: [99, 70] }),
    );
    const order = rankHPAs([steady, scaling, ceiling, saturated]).map(
      (h) => h.name,
    );
    expect(order).toEqual(["saturated", "ceiling", "scaling", "steady"]);
  });

  test("a tie orders by namespace then name, so refreshes do not reshuffle", () => {
    const a = classifyHPA(hpa({ namespace: "b", name: "a", current: 1 }));
    const b = classifyHPA(hpa({ namespace: "a", name: "z", current: 1 }));
    expect(rankHPAs([a, b]).map((h) => `${h.namespace}/${h.name}`)).toEqual([
      "a/z",
      "b/a",
    ]);
  });

  test("the input array is not reordered", () => {
    const list = [
      classifyHPA(hpa({ name: "steady", current: 1 })),
      classifyHPA(
        hpa({ name: "saturated", max: 2, current: 2, cpu: [99, 10] }),
      ),
    ];
    rankHPAs(list);
    expect(list.map((h) => h.name)).toEqual(["steady", "saturated"]);
  });
});

describe("hpaStatusView", () => {
  test("an empty payload does not throw and claims nothing", () => {
    for (const p of [null, undefined, page([]), { items: null } as never]) {
      const view = hpaStatusView(p, 5);
      expect(view.hpas).toEqual([]);
      expect(view.saturated).toBe(0);
      expect(view.atCeiling).toBe(0);
      expect(view.total).toBe(0);
      expect(view.truncated).toBe(false);
    }
  });

  test("headline counts cover the whole page, and the list is capped", () => {
    const items = [
      hpa({ name: "s1", max: 2, current: 2, cpu: [99, 50] }),
      hpa({ name: "s2", max: 2, current: 2, cpu: [98, 50] }),
      hpa({ name: "c1", max: 2, current: 2, cpu: [10, 50] }),
      hpa({ name: "q1", max: 9, current: 1, cpu: [10, 50] }),
    ];
    const view = hpaStatusView(page(items), 2);
    expect(view.hpas).toHaveLength(2);
    expect(view.saturated).toBe(2);
    expect(view.atCeiling).toBe(1);
    expect(view.counted).toBe(4);
  });

  test("a capped page is reported as a sample of the population", () => {
    const view = hpaStatusView(page([hpa(), hpa({ name: "b" })], 900), 10);
    expect(view.counted).toBe(2);
    expect(view.total).toBe(900);
    expect(view.truncated).toBe(true);
  });

  test("a complete page is not reported as truncated", () => {
    const view = hpaStatusView(page([hpa()]), 10);
    expect(view.truncated).toBe(false);
  });

  test("autoscalers this build cannot read are counted, not hidden", () => {
    // Dropping them would shrink the list silently; the card says how many it
    // could not read instead.
    const view = hpaStatusView(page([hpa(), null, "nope"]), 10);
    expect(view.unknown).toBe(2);
    expect(view.counted).toBe(3);
  });
});

describe("classifyPDB", () => {
  test("a budget allowing zero disruptions is blocked", () => {
    // The case that blocks a node drain: cordon the node and the eviction API
    // refuses every pod this budget covers.
    const p = classifyPDB(pdb({ allowed: 0, expected: 3, healthy: 2 }));
    expect(p.state).toBe("blocked");
    expect(p.disruptionsAllowed).toBe(0);
  });

  test("a budget allowing some disruptions is healthy", () => {
    expect(classifyPDB(pdb({ allowed: 1 })).state).toBe("healthy");
    expect(classifyPDB(pdb({ allowed: 5 })).state).toBe("healthy");
  });

  test("a budget matching no pods is informational, not a risk", () => {
    // Zero allowed because there is nothing to disrupt. Filing this under
    // "blocks a drain" is a false alarm on every budget whose workload has
    // been removed or whose selector has drifted.
    const p = classifyPDB(pdb({ allowed: 0, expected: 0, healthy: 0 }));
    expect(p.state).toBe("no-pods");
  });

  test("a budget with no readable status is unknown, not healthy", () => {
    for (const bad of [
      null,
      undefined,
      7,
      {},
      { metadata: { name: "x" } },
      { metadata: { name: "x" }, status: { disruptionsAllowed: "1" } },
    ]) {
      expect(classifyPDB(bad).state).toBe("unknown");
    }
  });

  test("the older PodDisruptionsAllowed spelling is read too", () => {
    // policy/v1beta1 spelled the field with a leading capital. The informer
    // serves v1, but a payload carrying only the old spelling must not read
    // as an unknown budget.
    const p = classifyPDB({
      metadata: { name: "x", namespace: "y" },
      status: { PodDisruptionsAllowed: 0, expectedPods: 2 },
    });
    expect(p.state).toBe("blocked");
  });

  test("identity is carried so a row can link to the budget", () => {
    const p = classifyPDB(pdb({ name: "web", namespace: "shop" }));
    expect(p.name).toBe("web");
    expect(p.namespace).toBe("shop");
  });
});

describe("rankPDBs", () => {
  test("blocked leads, then unreadable, then budgets with no pods", () => {
    const healthy = classifyPDB(pdb({ name: "ok", allowed: 2 }));
    const none = classifyPDB(pdb({ name: "none", allowed: 0, expected: 0 }));
    const unknown = classifyPDB({
      metadata: { name: "huh", namespace: "prod" },
    });
    const blocked = classifyPDB(
      pdb({ name: "stuck", allowed: 0, expected: 3 }),
    );
    const order = rankPDBs([healthy, none, unknown, blocked]).map(
      (p) => p.name,
    );
    expect(order).toEqual(["stuck", "huh", "none", "ok"]);
  });
});

describe("pdbRiskView", () => {
  test("an empty payload does not throw and claims nothing", () => {
    for (const p of [null, undefined, page([]), { items: 5 } as never]) {
      const view = pdbRiskView(p, 5);
      expect(view.pdbs).toEqual([]);
      expect(view.blocked).toBe(0);
      expect(view.total).toBe(0);
    }
  });

  test("only budgets worth looking at are listed, and healthy ones are counted", () => {
    // A list of every budget on the cluster is an inventory, not a finding.
    const items = [
      pdb({ name: "stuck", allowed: 0, expected: 3 }),
      pdb({ name: "empty", allowed: 0, expected: 0 }),
      pdb({ name: "fine", allowed: 2 }),
      pdb({ name: "also-fine", allowed: 1 }),
    ];
    const view = pdbRiskView(page(items), 10);
    expect(view.pdbs.map((p) => p.name)).toEqual(["stuck", "empty"]);
    expect(view.blocked).toBe(1);
    expect(view.noPods).toBe(1);
    expect(view.healthy).toBe(2);
  });

  test("headline counts cover the whole page while the list is capped", () => {
    const items = [
      pdb({ name: "a", allowed: 0, expected: 1 }),
      pdb({ name: "b", allowed: 0, expected: 1 }),
      pdb({ name: "c", allowed: 0, expected: 1 }),
    ];
    const view = pdbRiskView(page(items), 2);
    expect(view.pdbs).toHaveLength(2);
    expect(view.blocked).toBe(3);
  });

  test("a capped page is reported as a sample of the population", () => {
    const view = pdbRiskView(page([pdb()], 700), 10);
    expect(view.total).toBe(700);
    expect(view.counted).toBe(1);
    expect(view.truncated).toBe(true);
  });
});

describe("hrefs", () => {
  test("a row links to its own resource page (R7)", () => {
    expect(hpaHref("prod", "api")).toBe("/scaling/hpas/prod/api");
    expect(pdbHref("prod", "web")).toBe("/scaling/pdbs/prod/web");
  });

  test("path segments are encoded rather than interpolated raw", () => {
    expect(hpaHref("a/b", "c d")).toBe("/scaling/hpas/a%2Fb/c%20d");
    expect(pdbHref("a/b", "c d")).toBe("/scaling/pdbs/a%2Fb/c%20d");
  });
});
