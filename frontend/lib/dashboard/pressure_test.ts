import { describe, expect, test } from "bun:test";
import {
  claimHref,
  NODE_CONDITION_KINDS,
  namespaceHref,
  nodeConditionsView,
  nodeHref,
  PRESSURE_CRITICAL_PERCENT,
  PRESSURE_WARNING_PERCENT,
  pressureLevel,
  quotaPressureView,
  storageCapacityView,
} from "./pressure.ts";
import type { ResourceListPage } from "./wire-types.ts";

// The derived state behind `quota-pressure`, `node-conditions` and
// `storage-capacity` -- the reliability family.
//
// All three answer a question their payload does not carry a field for. Which
// namespace is closest to running out of quota is a ranking over whichever
// resources each quota happens to constrain, not a field. Which nodes are
// under pressure is four specific conditions out of the dozen a Node object
// carries. Which volumes are nearly full is a Prometheus vector that has to be
// parsed before it is anything. None of that is a field read, so it lives here
// under test rather than inside the components (D-10, KTD8).
//
// The direction every function errs in is the same one the rest of the
// dashboard errs in: a payload this build cannot read resolves to "we could
// not read it", never to an empty list. An empty list on these three cards
// reads as a cluster with no quota pressure, no node under pressure and no
// volume near full, which is the "absence as good news" this release exists to
// remove.

// --------------------------------------------------------------------------
// Quota pressure
// --------------------------------------------------------------------------

/** One row of `GET /v1/limits/namespaces`, which answers with the handler's
 * `NamespaceSummary` list. A namespace carrying only a LimitRange and no quota
 * IS in that list, with `hasQuota: false` and a zero utilization -- which is
 * the whole reason the ranking cannot simply sort the payload. */
function summary(over: {
  namespace: string;
  hasQuota?: boolean;
  highestUtilization?: number;
  cpuUsedPercent?: number;
  memoryUsedPercent?: number;
  status?: string;
  quotaCount?: number;
}): unknown {
  return {
    namespace: over.namespace,
    hasQuota: over.hasQuota ?? true,
    hasLimitRange: false,
    highestUtilization: over.highestUtilization ?? 0,
    cpuUsedPercent: over.cpuUsedPercent,
    memoryUsedPercent: over.memoryUsedPercent,
    status: over.status ?? "ok",
    quotaCount: over.quotaCount ?? 1,
    limitRangeCount: 0,
  };
}

describe("pressureLevel", () => {
  test("the two thresholds are the boundaries, inclusive", () => {
    expect(pressureLevel(PRESSURE_WARNING_PERCENT - 0.1)).toBe("ok");
    expect(pressureLevel(PRESSURE_WARNING_PERCENT)).toBe("warning");
    expect(pressureLevel(PRESSURE_CRITICAL_PERCENT - 0.1)).toBe("warning");
    expect(pressureLevel(PRESSURE_CRITICAL_PERCENT)).toBe("critical");
    // A volume reporting more used than it has capacity for is over, not okay.
    expect(pressureLevel(140)).toBe("critical");
  });
});

describe("quotaPressureView", () => {
  test("ranks by proportion used, not by absolute value", () => {
    // `tiny` is a two-pod namespace at 92% of its quota. `bulk` is a large one
    // at 60%, and it is the bigger consumer by every absolute measure the
    // payload carries -- its CPU and memory percentages sum higher, and it
    // holds more quotas. Ranking on any of those puts `bulk` first, and the
    // operator who needs to act is the one running `tiny`.
    const view = quotaPressureView(
      [
        summary({
          namespace: "bulk",
          highestUtilization: 60,
          cpuUsedPercent: 60,
          memoryUsedPercent: 58,
          quotaCount: 3,
        }),
        summary({
          namespace: "tiny",
          highestUtilization: 92,
          cpuUsedPercent: 92,
          memoryUsedPercent: 10,
          quotaCount: 1,
        }),
      ],
      10,
    );

    expect(view.readable).toBe(true);
    expect(view.rows.map((r) => r.namespace)).toEqual(["tiny", "bulk"]);
    expect(view.rows[0].percent).toBe(92);
  });

  test("a namespace with no quota is omitted from the ranking entirely", () => {
    // Not ranked at zero. The handler lists a namespace that has only a
    // LimitRange, and a LimitRange constrains nothing the dashboard can call
    // pressure -- putting it at the bottom of a pressure ranking asserts it is
    // comfortably within a quota it does not have.
    const view = quotaPressureView(
      [
        summary({ namespace: "prod", highestUtilization: 71 }),
        summary({ namespace: "sandbox", hasQuota: false }),
      ],
      10,
    );

    expect(view.rows.map((r) => r.namespace)).toEqual(["prod"]);
    expect(view.quotaed).toBe(1);
    expect(view.unquotaed).toBe(1);
  });

  test("a quota constraining only one resource ranks on that resource alone", () => {
    // `pods: 48/50` and nothing else. The payload carries no CPU or memory
    // percentage for it at all, and the ranking is on `highestUtilization`,
    // which is the maximum across whichever resources the quota actually
    // constrains. A view that ranked on CPU would put this namespace last.
    const view = quotaPressureView(
      [
        summary({
          namespace: "cpu-heavy",
          highestUtilization: 70,
          cpuUsedPercent: 70,
          memoryUsedPercent: 65,
        }),
        summary({
          namespace: "pod-bound",
          highestUtilization: 96,
          status: "critical",
        }),
      ],
      10,
    );

    expect(view.rows.map((r) => r.namespace)).toEqual([
      "pod-bound",
      "cpu-heavy",
    ]);
    expect(view.rows[0].cpuPercent).toBeNull();
    expect(view.rows[0].memoryPercent).toBeNull();
    expect(view.rows[0].level).toBe("critical");
  });

  test("the server's own status wins over the derived level", () => {
    // The handler honours `k8scenter.io/warn-threshold` on the quota, so a
    // namespace an operator has told the cluster to warn about at 50% arrives
    // already labelled. Re-deriving from the default thresholds would overrule
    // a threshold the operator set on purpose.
    const view = quotaPressureView(
      [
        summary({
          namespace: "early-warn",
          highestUtilization: 55,
          status: "warning",
        }),
      ],
      10,
    );
    expect(view.rows[0].level).toBe("warning");
  });

  test("an unrecognised status falls back to the derived level", () => {
    const view = quotaPressureView(
      [
        summary({
          namespace: "odd",
          highestUtilization: 97,
          status: "elevated",
        }),
      ],
      10,
    );
    expect(view.rows[0].level).toBe("critical");
  });

  test("counts at-risk namespaces across the whole payload, not the capped list", () => {
    const view = quotaPressureView(
      [
        summary({ namespace: "a", highestUtilization: 99, status: "critical" }),
        summary({ namespace: "b", highestUtilization: 91, status: "warning" }),
        summary({ namespace: "c", highestUtilization: 85, status: "warning" }),
        summary({ namespace: "d", highestUtilization: 12 }),
      ],
      1,
    );
    expect(view.rows).toHaveLength(1);
    expect(view.atRisk).toBe(3);
    expect(view.quotaed).toBe(4);
  });

  test("equal pressure ties break on namespace name", () => {
    const view = quotaPressureView(
      [
        summary({ namespace: "zeta", highestUtilization: 40 }),
        summary({ namespace: "alpha", highestUtilization: 40 }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.namespace)).toEqual(["alpha", "zeta"]);
  });

  test("an empty payload is readable and reports nothing, without throwing", () => {
    const view = quotaPressureView([], 10);
    expect(view.readable).toBe(true);
    expect(view.rows).toEqual([]);
    expect(view.quotaed).toBe(0);
    expect(view.unquotaed).toBe(0);
    expect(view.atRisk).toBe(0);
  });

  test("a payload this build cannot read is unreadable, not empty", () => {
    for (const bad of [null, undefined, {}, "nope", 7]) {
      const view = quotaPressureView(bad, 10);
      expect(view.readable).toBe(false);
      expect(view.rows).toEqual([]);
    }
  });

  test("an unreadable row is skipped rather than ranked at zero", () => {
    const view = quotaPressureView(
      [
        null,
        { hasQuota: true },
        summary({ namespace: "ok", highestUtilization: 10 }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.namespace)).toEqual(["ok"]);
    expect(view.quotaed).toBe(1);
  });

  test("a namespace whose utilization is unreadable is not ranked as idle", () => {
    // `highestUtilization` arriving as a string or absent is not zero. A
    // namespace with a quota whose pressure we cannot read has to be counted
    // as quota'd and left out of the ranking, not placed at the bottom.
    const view = quotaPressureView(
      [
        { namespace: "broken", hasQuota: true, highestUtilization: "62" },
        summary({ namespace: "fine", highestUtilization: 5 }),
      ],
      10,
    );
    expect(view.rows.map((r) => r.namespace)).toEqual(["fine"]);
    expect(view.quotaed).toBe(2);
    expect(view.unreadableRows).toBe(1);
  });

  test("links point at the namespace and the full page", () => {
    expect(namespaceHref("kube system")).toBe(
      "/cluster/namespaces/kube%20system",
    );
  });
});

// --------------------------------------------------------------------------
// Node conditions
// --------------------------------------------------------------------------

function node(
  name: string,
  conditions: Array<[string, string]>,
): Record<string, unknown> {
  return {
    metadata: { name },
    status: {
      conditions: conditions.map(([type, status]) => ({ type, status })),
    },
  };
}

const READY: Array<[string, string]> = [
  ["Ready", "True"],
  ["MemoryPressure", "False"],
  ["DiskPressure", "False"],
  ["PIDPressure", "False"],
];

/**
 * `items` is deliberately `unknown` rather than `unknown[]`: a malformed
 * payload is one of the cases these views have to distinguish, and a helper
 * that could only build well-formed pages made that case unwritable.
 */
function page(items: unknown, total?: number): ResourceListPage {
  const n = Array.isArray(items) ? items.length : 0;
  return { items, total: total ?? n } as unknown as ResourceListPage;
}

describe("nodeConditionsView", () => {
  test("surfaces disk, memory and PID pressure and not-ready", () => {
    const view = nodeConditionsView(
      page([
        node("n-disk", [
          ["Ready", "True"],
          ["DiskPressure", "True"],
        ]),
        node("n-mem", [
          ["Ready", "True"],
          ["MemoryPressure", "True"],
        ]),
        node("n-pid", [
          ["Ready", "True"],
          ["PIDPressure", "True"],
        ]),
        node("n-down", [["Ready", "False"]]),
      ]),
      10,
    );

    const byName = Object.fromEntries(view.nodes.map((n) => [n.name, n]));
    expect(byName["n-disk"].conditions).toEqual(["disk-pressure"]);
    expect(byName["n-mem"].conditions).toEqual(["memory-pressure"]);
    expect(byName["n-pid"].conditions).toEqual(["pid-pressure"]);
    expect(byName["n-down"].conditions).toEqual(["not-ready"]);
    expect(view.affected).toBe(4);
  });

  test("a node with none of them is not listed", () => {
    // The card is a findings list, not an inventory. A healthy node has
    // nothing for anyone to do with it, and the full condition list -- the
    // dozen entries a Node actually carries -- belongs on the node page.
    const view = nodeConditionsView(
      page([node("healthy", READY), node("sick", [["Ready", "False"]])]),
      10,
    );
    expect(view.nodes.map((n) => n.name)).toEqual(["sick"]);
    expect(view.clear).toBe(1);
    expect(view.affected).toBe(1);
  });

  test("a cluster where every node is healthy reports the clear state", () => {
    const view = nodeConditionsView(
      page([node("a", READY), node("b", READY), node("c", READY)]),
      10,
    );
    expect(view.nodes).toEqual([]);
    expect(view.affected).toBe(0);
    expect(view.clear).toBe(3);
    expect(view.total).toBe(3);
    expect(view.truncated).toBe(false);
  });

  test("Ready reported Unknown is not-ready AND silent", () => {
    // The kubelet stopped answering. That is a different thing from a node
    // that reported itself NotReady, and the card says so -- an operator
    // chasing a NotReady node looks at the workload, one chasing a silent node
    // looks at the machine.
    const view = nodeConditionsView(
      page([node("gone", [["Ready", "Unknown"]])]),
      10,
    );
    expect(view.nodes[0].conditions).toEqual(["not-ready"]);
    expect(view.nodes[0].silent).toBe(true);
  });

  test("a node carrying no Ready condition is treated as not reporting", () => {
    // Absence is not health. A Node object with no readable condition list
    // resolves to not-ready and silent, which is visible and wrong-if-wrong,
    // rather than to a clear node, which is invisible and wrong-if-wrong.
    const view = nodeConditionsView(page([{ metadata: { name: "bare" } }]), 10);
    expect(view.nodes[0].name).toBe("bare");
    expect(view.nodes[0].conditions).toEqual(["not-ready"]);
    expect(view.nodes[0].silent).toBe(true);
    expect(view.clear).toBe(0);
  });

  test("conditions come back in a fixed order, worst first", () => {
    const view = nodeConditionsView(
      page([
        node("n", [
          ["PIDPressure", "True"],
          ["DiskPressure", "True"],
          ["MemoryPressure", "True"],
          ["Ready", "False"],
        ]),
      ]),
      10,
    );
    expect(view.nodes[0].conditions).toEqual([...NODE_CONDITION_KINDS]);
  });

  test("ranks silent above not-ready, then by how many findings, then by name", () => {
    const view = nodeConditionsView(
      page([
        node("one-pressure", [
          ["Ready", "True"],
          ["DiskPressure", "True"],
        ]),
        node("silent", [["Ready", "Unknown"]]),
        node("b-two", [
          ["Ready", "True"],
          ["DiskPressure", "True"],
          ["MemoryPressure", "True"],
        ]),
        node("a-two", [
          ["Ready", "True"],
          ["DiskPressure", "True"],
          ["MemoryPressure", "True"],
        ]),
        node("down", [["Ready", "False"]]),
      ]),
      10,
    );
    expect(view.nodes.map((n) => n.name)).toEqual([
      "silent",
      "down",
      "a-two",
      "b-two",
      "one-pressure",
    ]);
  });

  test("the list is capped but the counts are not", () => {
    const view = nodeConditionsView(
      page([
        node("a", [["Ready", "False"]]),
        node("b", [["Ready", "False"]]),
        node("c", [["Ready", "False"]]),
        node("d", READY),
      ]),
      2,
    );
    expect(view.nodes).toHaveLength(2);
    expect(view.affected).toBe(3);
    expect(view.clear).toBe(1);
  });

  test("a truncated page is reported as a sample of the population", () => {
    const view = nodeConditionsView(
      page([node("a", [["Ready", "False"]])], 900),
      10,
    );
    expect(view.total).toBe(900);
    expect(view.counted).toBe(1);
    expect(view.truncated).toBe(true);
  });

  test("an object with no readable name is counted as unreadable, not clear", () => {
    const view = nodeConditionsView(page([null, { metadata: {} }, 7]), 10);
    expect(view.unreadable).toBe(3);
    expect(view.clear).toBe(0);
    expect(view.affected).toBe(0);
    expect(view.nodes).toEqual([]);
  });

  test("an empty page is a readable, genuinely clean cluster", () => {
    const view = nodeConditionsView(page([]), 10);
    expect(view.readable).toBe(true);
    expect(view.nodes).toEqual([]);
    expect(view.affected).toBe(0);
    expect(view.clear).toBe(0);
    expect(view.total).toBe(0);
    expect(view.truncated).toBe(false);
  });

  // This replaces "an empty or absent page does not throw", which asserted
  // that an empty page, a null payload and an unreadable one all produce the
  // same view. Not throwing was the right half of that; treating the three as
  // one answer was the belief that let a card print a clean cluster over a
  // read that never happened. The sentence is kept here because it is the
  // mistake, not merely a test that was wrong.
  test("a null, absent or malformed payload is unreadable, not clean", () => {
    for (const p of [null, undefined, page(null), page("nodes"), page(7)]) {
      const view = nodeConditionsView(p, 10);
      expect(view.readable).toBe(false);
      // The counts are still zero -- there was nothing to count -- which is
      // exactly why `readable` has to be what the card branches on.
      expect(view.nodes).toEqual([]);
      expect(view.affected).toBe(0);
      expect(view.clear).toBe(0);
    }
  });

  test("node links are encoded", () => {
    expect(nodeHref("ip-10.0.0.1/a")).toBe("/cluster/nodes/ip-10.0.0.1%2Fa");
  });
});

// --------------------------------------------------------------------------
// Storage capacity
// --------------------------------------------------------------------------

/** `GET /v1/storage/classes` answers with the handler's `ClassInfo` list. */
function storageClass(
  name: string,
  over: Record<string, unknown> = {},
): unknown {
  return {
    name,
    provisioner: "ebs.csi.aws.com",
    reclaimPolicy: "Delete",
    volumeBindingMode: "WaitForFirstConsumer",
    allowVolumeExpansion: true,
    isDefault: false,
    ...over,
  };
}

/** The instant-vector envelope from
 * `GET /v1/monitoring/queries/cluster/storage-capacity`. The slug's template
 * divides used by capacity, and PromQL binary arithmetic keeps only the labels
 * common to both sides -- so the series carry `namespace` and
 * `persistentvolumeclaim` and NOT `pod`. */
function capacityVector(
  samples: Array<[string, string, string]>,
  warnings: unknown = null,
): unknown {
  return {
    resultType: "vector",
    result: samples.map(([namespace, claim, value]) => ({
      metric: { namespace, persistentvolumeclaim: claim },
      value: [1700000000, value],
    })),
    warnings,
  };
}

describe("storageCapacityView", () => {
  test("composes the classes read with the Prometheus slug", () => {
    const view = storageCapacityView(
      [storageClass("gp3", { isDefault: true }), storageClass("io2")],
      capacityVector([
        ["prod", "data-0", "44.5"],
        ["prod", "wal-0", "97.2"],
      ]),
      10,
    );

    expect(view.classesReadable).toBe(true);
    expect(view.classes.map((c) => c.name)).toEqual(["gp3", "io2"]);
    expect(view.defaultClass).toBe("gp3");
    expect(view.volumesReadable).toBe(true);
    expect(view.volumes.map((v) => v.claim)).toEqual(["wal-0", "data-0"]);
    expect(view.volumes[0].level).toBe("critical");
    expect(view.volumes[0].percent).toBeCloseTo(97.2, 5);
  });

  test("degrades to the classes it has when the slug fails", () => {
    // This is the scenario the card exists in the shape it does for. The
    // classes read is the required source and the slug is optional, so a
    // Prometheus that is down, absent or refused must leave the class
    // inventory on screen rather than blanking the card -- and must say the
    // volume half is missing rather than rendering no volumes, which reads as
    // a cluster where nothing is close to full.
    const view = storageCapacityView([storageClass("gp3")], null, 10);

    expect(view.classesReadable).toBe(true);
    expect(view.classes).toHaveLength(1);
    expect(view.volumesReadable).toBe(false);
    expect(view.volumes).toEqual([]);
  });

  test("a slug payload this build cannot read is unreadable, not empty", () => {
    for (const bad of [
      {},
      { resultType: "matrix", result: [] },
      { resultType: "vector", result: "nope" },
      "nope",
    ]) {
      const view = storageCapacityView([storageClass("gp3")], bad, 10);
      expect(view.volumesReadable).toBe(false);
      expect(view.volumes).toEqual([]);
    }
  });

  test("an empty vector is readable and means no volume is reporting", () => {
    const view = storageCapacityView([], capacityVector([]), 10);
    expect(view.classesReadable).toBe(true);
    expect(view.classes).toEqual([]);
    expect(view.classTotal).toBe(0);
    expect(view.defaultClass).toBeNull();
    expect(view.volumesReadable).toBe(true);
    expect(view.volumes).toEqual([]);
  });

  test("a sample with no claim label or no readable value is dropped and counted", () => {
    const view = storageCapacityView(
      [],
      {
        resultType: "vector",
        result: [
          { metric: { namespace: "prod" }, value: [1, "80"] },
          {
            metric: { namespace: "prod", persistentvolumeclaim: "a" },
            value: [1, "NaN"],
          },
          {
            metric: { namespace: "prod", persistentvolumeclaim: "b" },
            value: [1, "51"],
          },
        ],
      },
      10,
    );
    expect(view.volumes.map((v) => v.claim)).toEqual(["b"]);
    expect(view.volumesDropped).toBe(2);
  });

  test("Prometheus warnings are carried through", () => {
    const view = storageCapacityView(
      [],
      capacityVector([["prod", "a", "10"]], ["partial results", ""]),
      10,
    );
    expect(view.volumeWarnings).toEqual(["partial results"]);
  });

  test("the volume list is capped and the total is not", () => {
    const view = storageCapacityView(
      [],
      capacityVector([
        ["a", "one", "10"],
        ["a", "two", "20"],
        ["a", "three", "30"],
      ]),
      2,
    );
    expect(view.volumes.map((v) => v.claim)).toEqual(["three", "two"]);
    expect(view.volumeTotal).toBe(3);
  });

  test("a classes payload this build cannot read is unreadable, not empty", () => {
    const view = storageCapacityView(null, capacityVector([]), 10);
    expect(view.classesReadable).toBe(false);
    expect(view.classes).toEqual([]);
    expect(view.classTotal).toBe(0);
  });

  test("an unreadable class entry is skipped", () => {
    const view = storageCapacityView(
      [null, { provisioner: "x" }, storageClass("gp3")],
      null,
      10,
    );
    expect(view.classes.map((c) => c.name)).toEqual(["gp3"]);
    expect(view.classTotal).toBe(1);
  });

  test("both payloads absent does not throw", () => {
    const view = storageCapacityView(null, null, 10);
    expect(view.classesReadable).toBe(false);
    expect(view.volumesReadable).toBe(false);
    expect(view.classes).toEqual([]);
    expect(view.volumes).toEqual([]);
  });

  test("claim links are encoded on both segments", () => {
    expect(
      claimHref({
        namespace: "a b",
        claim: "c/d",
        percent: 1,
        level: "ok",
      }),
    ).toBe("/storage/pvcs/a%20b/c%2Fd");
  });
});
