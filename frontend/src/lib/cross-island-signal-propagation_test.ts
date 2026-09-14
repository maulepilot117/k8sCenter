import { expect, test } from "bun:test";
import { effect } from "@preact/signals";
import {
  clusterEpoch,
  selectedCluster,
  selectedClusterGeneration,
  switchCluster,
} from "./cluster.ts";
import { selectedNamespace } from "./namespace.ts";

/**
 * U13 test scenarios: "switching cluster ... updates every resource table
 * and detail panel on the page" and "the batched cluster write is observed
 * atomically -- no subscriber sees the new cluster with the old
 * generation." lib/cluster-targeting_test.ts already proves the single-
 * subscriber atomicity property for the pre-port module; this file adds
 * the genuinely cross-island shape -- multiple independent subscribers,
 * standing in for separate mounted islands (a resource table, a detail
 * panel) that each only import the signals, the way real islands do -- and
 * asserts after a flush rather than synchronously, per the unit brief's own
 * warning about false failures from asserting too early.
 */

/** Waits one tick past whatever preact-signals' own scheduling needs. */
function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

test("a cluster switch is observed by every independent subscriber, and each sees the full new triple together", async () => {
  switchCluster("cluster-a", "gen-a");

  const tableSeen: Array<{ id: string; generation: string; epoch: number }> =
    [];
  const detailSeen: Array<{ id: string; generation: string; epoch: number }> =
    [];

  // Two independently-registered effects, standing in for a resource table
  // island and a detail-panel island that each subscribe on their own —
  // neither knows the other exists, which is the real shape of two
  // separately mounted islands sharing only the module.
  const disposeTable = effect(() => {
    tableSeen.push({
      id: selectedCluster.value,
      generation: selectedClusterGeneration.value,
      epoch: clusterEpoch.value,
    });
  });
  const disposeDetail = effect(() => {
    detailSeen.push({
      id: selectedCluster.value,
      generation: selectedClusterGeneration.value,
      epoch: clusterEpoch.value,
    });
  });

  const tableCountBefore = tableSeen.length;
  const detailCountBefore = detailSeen.length;

  switchCluster("cluster-b", "gen-b");

  // Do not assert synchronously (KTD5 / this unit's brief): give the write
  // a tick to flush before either subscriber is inspected.
  await flush();

  try {
    expect(tableSeen.length).toBe(tableCountBefore + 1);
    expect(detailSeen.length).toBe(detailCountBefore + 1);

    const tableLast = tableSeen[tableSeen.length - 1]!;
    const detailLast = detailSeen[detailSeen.length - 1]!;

    // Atomicity: both subscribers see the SAME triple, and it is the fully
    // new one -- never the new id paired with cluster-a's stale generation
    // or epoch (the torn read the batch() in switchCluster exists to
    // prevent).
    for (const seen of [tableLast, detailLast]) {
      expect(seen.id).toBe("cluster-b");
      expect(seen.generation).toBe("gen-b");
      expect(seen.epoch).toBe(clusterEpoch.value);
    }
    expect(tableLast).toEqual(detailLast);
  } finally {
    disposeTable();
    disposeDetail();
  }
});

test("switching namespace propagates to every island reading the namespace signal", async () => {
  selectedNamespace.value = "default";

  const seenByTable: string[] = [];
  const seenByFilterBar: string[] = [];
  const disposeTable = effect(() => {
    seenByTable.push(selectedNamespace.value);
  });
  const disposeFilterBar = effect(() => {
    seenByFilterBar.push(selectedNamespace.value);
  });

  selectedNamespace.value = "kube-system";
  await flush();

  try {
    expect(seenByTable[seenByTable.length - 1]).toBe("kube-system");
    expect(seenByFilterBar[seenByFilterBar.length - 1]).toBe("kube-system");
  } finally {
    disposeTable();
    disposeFilterBar();
  }
});
