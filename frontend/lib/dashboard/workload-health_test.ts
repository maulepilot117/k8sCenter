import { describe, expect, test } from "bun:test";
import {
  rollUpWorkloadHealth,
  WORKLOAD_KINDS,
  workloadReady,
} from "./workload-health.ts";

// The controller roll-up behind the workload-health widget.
//
// Two payloads meet here and they answer different questions.
// `/v1/resources/counts` says which of the three controller kinds this account
// may list at all -- a kind it omits is one the caller cannot see, NOT a kind
// with zero workloads -- and how many of each exist. The three list reads say
// how many of those are ready. Folding the two together is the part an
// operator acts on, so it is pure and lives here rather than inside the
// component (D-10).
//
// The direction this module errs in is the whole point: an unanswerable kind
// reports "unknown", never "0 of N ready". A card that renders a kind it
// cannot see as fully degraded is a false alarm; one that renders it as fully
// ready is the "absence as good news" the release exists to prevent. Neither
// is acceptable, so the third state is explicit.

function dep(ready: number | undefined, replicas?: number) {
  return {
    metadata: { name: "d" },
    spec: replicas === undefined ? {} : { replicas },
    status: ready === undefined ? {} : { readyReplicas: ready },
  };
}

function ds(ready: number, desired: number) {
  return {
    metadata: { name: "x" },
    status: { numberReady: ready, desiredNumberScheduled: desired },
  };
}

describe("workloadReady", () => {
  test("a Deployment is ready when readyReplicas reaches spec.replicas", () => {
    expect(workloadReady("deployments", dep(3, 3))).toBe(true);
    expect(workloadReady("deployments", dep(2, 3))).toBe(false);
  });

  test("an omitted spec.replicas means one, as Kubernetes defaults it", () => {
    // Not zero. Treating the omission as zero would make every
    // default-sized Deployment read as ready before it has a pod.
    expect(workloadReady("deployments", dep(0))).toBe(false);
    expect(workloadReady("deployments", dep(1))).toBe(true);
  });

  test("a workload scaled to zero is ready, not degraded", () => {
    // Deliberately scaled down. Zero of zero is the state the operator asked
    // for, and reporting it as degraded would put a permanent red count on
    // every suspended workload.
    expect(workloadReady("deployments", dep(0, 0))).toBe(true);
  });

  test("a workload with no status at all is degraded", () => {
    // Just created, or a status this build cannot read. Either way nothing
    // says its replicas are up, and "we could not tell" must not render as
    // ready for a kind the caller CAN list.
    expect(workloadReady("deployments", { metadata: { name: "d" } })).toBe(
      false,
    );
    expect(workloadReady("statefulsets", null)).toBe(false);
    expect(workloadReady("deployments", "not an object")).toBe(false);
  });

  test("a StatefulSet reads the same two fields as a Deployment", () => {
    expect(workloadReady("statefulsets", dep(2, 2))).toBe(true);
    expect(workloadReady("statefulsets", dep(1, 2))).toBe(false);
  });

  test("a DaemonSet reads numberReady against desiredNumberScheduled", () => {
    // A DaemonSet has no spec.replicas -- its size is however many nodes
    // match -- so reading spec.replicas would make every DaemonSet on the
    // cluster look like a one-replica workload with nothing ready.
    expect(workloadReady("daemonsets", ds(4, 4))).toBe(true);
    expect(workloadReady("daemonsets", ds(3, 4))).toBe(false);
  });

  test("a DaemonSet no node is eligible for is ready, not degraded", () => {
    // desiredNumberScheduled 0 means the node selector matches nothing. The
    // cluster is doing exactly what was asked.
    expect(workloadReady("daemonsets", ds(0, 0))).toBe(true);
  });
});

describe("rollUpWorkloadHealth", () => {
  test("an empty payload does not throw and claims nothing", () => {
    const out = rollUpWorkloadHealth(null, {});
    expect(out.kinds.map((k) => k.visibility)).toEqual([
      "unknown",
      "unknown",
      "unknown",
    ]);
    expect(out.ready).toBe(0);
    expect(out.degraded).toBe(0);
    expect(out.total).toBe(0);
    expect(out.unknownKinds).toEqual([...WORKLOAD_KINDS]);
    // Nothing is known, so nothing is empty either. "Empty" is a claim about
    // the cluster and this payload supports no claims at all.
    expect(out.empty).toBe(false);
  });

  test("a kind the counts payload omits is unknown, not zero-ready", () => {
    // The route omits a kind the caller cannot list rather than zeroing it.
    // Rendering the omission as 0 of 0 ready would be a green card about
    // workloads this account cannot see; rendering it as degraded would be a
    // false alarm. It is neither -- it is unknown.
    const out = rollUpWorkloadHealth(
      { deployments: 2, statefulsets: 1 },
      { deployments: [dep(1, 1), dep(0, 1)], statefulsets: [dep(1, 1)] },
    );
    const daemonsets = out.kinds.find((k) => k.kind === "daemonsets");
    expect(daemonsets?.visibility).toBe("unknown");
    expect(daemonsets?.total).toBe(0);
    expect(daemonsets?.ready).toBe(0);
    expect(daemonsets?.degraded).toBe(0);
    expect(out.unknownKinds).toEqual(["daemonsets"]);
    // The omitted kind contributes to neither side of the split.
    expect(out.ready).toBe(2);
    expect(out.degraded).toBe(1);
    expect(out.total).toBe(3);
  });

  test("a kind counts names but whose list has not landed is pending", () => {
    // Distinguished from unknown on purpose: the total is known and the
    // readiness is merely not in yet, so the card can show "3 workloads"
    // rather than claiming the kind is invisible.
    const out = rollUpWorkloadHealth(
      { deployments: 3, statefulsets: 0, daemonsets: 0 },
      { deployments: null, statefulsets: [], daemonsets: [] },
    );
    const deployments = out.kinds.find((k) => k.kind === "deployments");
    expect(deployments?.visibility).toBe("pending");
    expect(deployments?.total).toBe(3);
    expect(deployments?.ready).toBe(0);
    expect(deployments?.degraded).toBe(0);
    expect(out.unknownKinds).toEqual([]);
    // A pending kind does not contribute to the split either -- its numbers
    // are not in.
    expect(out.ready).toBe(0);
    expect(out.degraded).toBe(0);
  });

  test("every workload ready splits entirely to ready", () => {
    const out = rollUpWorkloadHealth(
      { deployments: 2, statefulsets: 1, daemonsets: 1 },
      {
        deployments: [dep(1, 1), dep(2, 2)],
        statefulsets: [dep(3, 3)],
        daemonsets: [ds(5, 5)],
      },
    );
    expect(out.ready).toBe(4);
    expect(out.degraded).toBe(0);
    expect(out.total).toBe(4);
    expect(out.empty).toBe(false);
  });

  test("no workload ready splits entirely to degraded", () => {
    const out = rollUpWorkloadHealth(
      { deployments: 2, statefulsets: 0, daemonsets: 1 },
      {
        deployments: [dep(0, 1), dep(1, 2)],
        statefulsets: [],
        daemonsets: [ds(0, 3)],
      },
    );
    expect(out.ready).toBe(0);
    expect(out.degraded).toBe(3);
    expect(out.total).toBe(3);
  });

  test("a visible cluster with no workloads is empty, not all-ready", () => {
    // Zero of zero ready reads as a clean bill of health. It is not one --
    // there is simply nothing here, which is the distinction the card has to
    // draw before it prints a reassuring number.
    const out = rollUpWorkloadHealth(
      { deployments: 0, statefulsets: 0, daemonsets: 0 },
      { deployments: [], statefulsets: [], daemonsets: [] },
    );
    expect(out.empty).toBe(true);
    expect(out.total).toBe(0);
    expect(out.ready).toBe(0);
    expect(out.degraded).toBe(0);
  });

  test("one visible kind holding nothing is not empty when another holds something", () => {
    const out = rollUpWorkloadHealth(
      { deployments: 1, statefulsets: 0, daemonsets: 0 },
      { deployments: [dep(1, 1)], statefulsets: [], daemonsets: [] },
    );
    expect(out.empty).toBe(false);
  });

  test("a list shorter than the count it belongs to is reported as truncated", () => {
    // The list route caps at 500 items while counts reports the whole
    // population, so a large cluster's readiness covers only part of it.
    // Saying "12 degraded" over a sample and calling it the cluster is a
    // number an operator would act on.
    const out = rollUpWorkloadHealth(
      { deployments: 900, statefulsets: 0, daemonsets: 0 },
      { deployments: [dep(1, 1), dep(0, 1)], statefulsets: [], daemonsets: [] },
    );
    const deployments = out.kinds.find((k) => k.kind === "deployments");
    expect(deployments?.visibility).toBe("counted");
    expect(deployments?.total).toBe(900);
    expect(deployments?.counted).toBe(2);
    expect(deployments?.truncated).toBe(true);
    expect(out.truncated).toBe(true);
  });

  test("a list matching its count is not truncated", () => {
    const out = rollUpWorkloadHealth(
      { deployments: 1, statefulsets: 0, daemonsets: 0 },
      { deployments: [dep(1, 1)], statefulsets: [], daemonsets: [] },
    );
    expect(out.truncated).toBe(false);
  });

  test("kinds come back in the declared order, whatever the payload's", () => {
    // The card renders three fixed rows. An order that followed object key
    // order would reshuffle them between refreshes.
    const out = rollUpWorkloadHealth(
      { daemonsets: 0, deployments: 0, statefulsets: 0 },
      {},
    );
    expect(out.kinds.map((k) => k.kind)).toEqual([...WORKLOAD_KINDS]);
  });

  test("a counts payload that is not a map of numbers claims nothing", () => {
    // A body this build cannot read is not evidence of an empty cluster.
    const out = rollUpWorkloadHealth(
      { deployments: "12" } as unknown as Record<string, number>,
      {},
    );
    expect(out.kinds.every((k) => k.visibility === "unknown")).toBe(true);
    expect(out.empty).toBe(false);
  });
});
