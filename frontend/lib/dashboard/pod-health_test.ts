import { describe, expect, test } from "bun:test";
import {
  classifyPod,
  classifyPods,
  pendingPodsView,
  podRestartsView,
  rankByRestarts,
  rankPending,
} from "./pod-health.ts";

// One pod classification, two selectors over it.
//
// `pending-pods` and `pod-restarts` read the same list from the same route and
// both have to decide what state a pod is in. That decision written twice is
// two chances to disagree about the same pod on the same dashboard -- one card
// calling it pending while the other calls it crash-looping -- so it is
// written once here and each widget selects from the result (D-10, KTD8).
//
// The classification is deliberately defensive about the payload. It reads raw
// Kubernetes objects off the generic list route, so a field this build does
// not expect must degrade to "unknown" rather than throw: one malformed pod
// would otherwise blank a card describing several hundred healthy ones.

const NOW = Date.parse("2026-09-20T12:00:00.000Z");

interface PodFixture {
  name: string;
  namespace?: string;
  phase?: string;
  created?: string;
  scheduled?: "True" | "False";
  scheduledReason?: string;
  containers?: Array<{
    name?: string;
    ready?: boolean;
    restartCount?: number;
    waiting?: string;
  }>;
  initContainers?: Array<{ restartCount?: number }>;
}

function pod(f: PodFixture): unknown {
  return {
    metadata: {
      name: f.name,
      namespace: f.namespace ?? "default",
      creationTimestamp: f.created ?? "2026-09-20T11:00:00.000Z",
    },
    status: {
      phase: f.phase ?? "Running",
      conditions:
        f.scheduled === undefined
          ? undefined
          : [
              {
                type: "PodScheduled",
                status: f.scheduled,
                reason: f.scheduledReason,
              },
            ],
      containerStatuses: f.containers?.map((c) => ({
        name: c.name ?? "app",
        ready: c.ready ?? true,
        restartCount: c.restartCount ?? 0,
        state: c.waiting ? { waiting: { reason: c.waiting } } : { running: {} },
      })),
      initContainerStatuses: f.initContainers,
    },
  };
}

function page(items: unknown[], total?: number) {
  return { items, total: total ?? items.length };
}

describe("classifyPod", () => {
  test("a running pod is running", () => {
    expect(classifyPod(pod({ name: "a" }), NOW).state).toBe("running");
  });

  test("a terminal phase keeps its own name", () => {
    expect(classifyPod(pod({ name: "a", phase: "Succeeded" }), NOW).state).toBe(
      "succeeded",
    );
    expect(classifyPod(pod({ name: "a", phase: "Failed" }), NOW).state).toBe(
      "failed",
    );
  });

  test("Pending with PodScheduled=False is unschedulable, not merely pending", () => {
    // The two need different actions. A pending pod is waiting on an image
    // pull or a container start and will usually come up on its own; an
    // unschedulable one is waiting on capacity, a taint or a node selector
    // and will wait forever until somebody changes something.
    const p = classifyPod(
      pod({
        name: "a",
        phase: "Pending",
        scheduled: "False",
        scheduledReason: "Unschedulable",
      }),
      NOW,
    );
    expect(p.state).toBe("unschedulable");
    expect(p.reason).toBe("Unschedulable");
  });

  test("Pending with PodScheduled=True is merely pending", () => {
    expect(
      classifyPod(pod({ name: "a", phase: "Pending", scheduled: "True" }), NOW)
        .state,
    ).toBe("pending");
  });

  test("Pending with no scheduling condition at all is merely pending", () => {
    // A pod the scheduler has not looked at yet. Claiming it is
    // unschedulable would be an alarm about a decision nobody has made.
    expect(classifyPod(pod({ name: "a", phase: "Pending" }), NOW).state).toBe(
      "pending",
    );
  });

  test("a container in CrashLoopBackOff is crash-looping whatever the phase", () => {
    // A crash-looping pod reports phase Running, because the kubelet keeps
    // restarting it. Reading the phase alone would file the single most
    // actionable state on the cluster under "running".
    const p = classifyPod(
      pod({
        name: "a",
        phase: "Running",
        containers: [
          { ready: false, restartCount: 9, waiting: "CrashLoopBackOff" },
        ],
      }),
      NOW,
    );
    expect(p.state).toBe("crash-looping");
    expect(p.restarts).toBe(9);
  });

  test("an image-pull failure stays pending and carries its reason", () => {
    // Not a restart loop -- the container has never started, so there is
    // nothing to rank by restart count. The pending card is where it belongs,
    // and the reason is what tells it apart from a pod waiting on capacity.
    const p = classifyPod(
      pod({
        name: "a",
        phase: "Pending",
        scheduled: "True",
        containers: [{ ready: false, waiting: "ImagePullBackOff" }],
      }),
      NOW,
    );
    expect(p.state).toBe("pending");
    expect(p.reason).toBe("ImagePullBackOff");
  });

  test("restarts sum across every container, init containers included", () => {
    const p = classifyPod(
      pod({
        name: "a",
        containers: [{ restartCount: 2 }, { name: "sidecar", restartCount: 3 }],
        initContainers: [{ restartCount: 1 }],
      }),
      NOW,
    );
    expect(p.restarts).toBe(6);
  });

  test("a pod with no container statuses has no restart history and does not throw", () => {
    const p = classifyPod(pod({ name: "a" }), NOW);
    expect(p.restarts).toBe(0);
  });

  test("age is measured from creationTimestamp and is null when unreadable", () => {
    expect(
      classifyPod(pod({ name: "a", created: "2026-09-20T11:00:00.000Z" }), NOW)
        .ageMs,
    ).toBe(60 * 60 * 1000);
    expect(
      classifyPod(pod({ name: "a", created: "not a date" }), NOW).ageMs,
    ).toBeNull();
  });

  test("an object this build cannot read is unknown rather than a throw", () => {
    // One malformed item must not blank a card describing several hundred
    // readable ones.
    expect(classifyPod(null, NOW).state).toBe("unknown");
    expect(classifyPod("pod", NOW).state).toBe("unknown");
    expect(classifyPod({}, NOW).state).toBe("unknown");
    expect(classifyPod({ metadata: { name: "a" } }, NOW).state).toBe("unknown");
  });
});

describe("classifyPods", () => {
  test("an empty payload yields no pods and does not throw", () => {
    expect(classifyPods(null, NOW)).toEqual([]);
    expect(classifyPods(undefined, NOW)).toEqual([]);
    expect(classifyPods(page([]), NOW)).toEqual([]);
  });

  test("a payload whose items are not an array yields no pods", () => {
    expect(classifyPods({ items: "nope", total: 3 } as never, NOW)).toEqual([]);
  });
});

describe("rankByRestarts", () => {
  test("a crash-looping pod outranks a higher-count pod that is running", () => {
    // The loop is the actionable one. A pod that restarted forty times last
    // week and has been up since is history; one looping right now is an
    // outage in progress, and sorting purely by count would bury it.
    const ranked = rankByRestarts(
      classifyPods(
        page([
          pod({ name: "settled", containers: [{ restartCount: 40 }] }),
          pod({
            name: "looping",
            containers: [
              { ready: false, restartCount: 3, waiting: "CrashLoopBackOff" },
            ],
          }),
        ]),
        NOW,
      ),
    );
    expect(ranked.map((p) => p.name)).toEqual(["looping", "settled"]);
  });

  test("two crash-looping pods order by restart count", () => {
    const ranked = rankByRestarts(
      classifyPods(
        page([
          pod({
            name: "few",
            containers: [
              { ready: false, restartCount: 3, waiting: "CrashLoopBackOff" },
            ],
          }),
          pod({
            name: "many",
            containers: [
              { ready: false, restartCount: 30, waiting: "CrashLoopBackOff" },
            ],
          }),
        ]),
        NOW,
      ),
    );
    expect(ranked.map((p) => p.name)).toEqual(["many", "few"]);
  });

  test("a pod with no restart history sorts last rather than throwing", () => {
    const ranked = rankByRestarts(
      classifyPods(
        page([
          pod({ name: "quiet" }),
          {},
          pod({ name: "noisy", containers: [{ restartCount: 1 }] }),
        ]),
        NOW,
      ),
    );
    expect(ranked[0].name).toBe("noisy");
    expect(ranked).toHaveLength(3);
  });

  test("ties break on namespace and name so two refreshes do not reshuffle", () => {
    const ranked = rankByRestarts(
      classifyPods(
        page([
          pod({ name: "b", namespace: "x", containers: [{ restartCount: 2 }] }),
          pod({ name: "a", namespace: "x", containers: [{ restartCount: 2 }] }),
          pod({ name: "a", namespace: "a", containers: [{ restartCount: 2 }] }),
        ]),
        NOW,
      ),
    );
    expect(ranked.map((p) => `${p.namespace}/${p.name}`)).toEqual([
      "a/a",
      "x/a",
      "x/b",
    ]);
  });
});

describe("podRestartsView", () => {
  test("an empty payload does not throw and reports nothing", () => {
    const out = podRestartsView(null, 5);
    expect(out.pods).toEqual([]);
    expect(out.crashLooping).toBe(0);
    expect(out.total).toBe(0);
    expect(out.truncated).toBe(false);
  });

  test("pods with no restarts and no loop are left off the list entirely", () => {
    // The card is a ranking of things worth looking at. A healthy cluster's
    // card is empty, and an empty list is the finding.
    const out = podRestartsView(page([pod({ name: "quiet" })]), 5);
    expect(out.pods).toEqual([]);
    expect(out.counted).toBe(1);
  });

  test("the list is capped and the crash-looping count covers the whole page", () => {
    const items = [
      pod({
        name: "loop-a",
        containers: [
          { ready: false, restartCount: 5, waiting: "CrashLoopBackOff" },
        ],
      }),
      pod({
        name: "loop-b",
        containers: [
          { ready: false, restartCount: 4, waiting: "CrashLoopBackOff" },
        ],
      }),
      pod({ name: "restarted", containers: [{ restartCount: 2 }] }),
    ];
    const out = podRestartsView(page(items), 2);
    expect(out.pods.map((p) => p.name)).toEqual(["loop-a", "loop-b"]);
    expect(out.crashLooping).toBe(2);
    expect(out.restarting).toBe(1);
  });

  test("a page smaller than the reported total is truncated", () => {
    // The list route caps at 500. A ranking over the first 500 of 3000 pods
    // is not "the worst pods on the cluster", and the card has to say so.
    const out = podRestartsView(
      page([pod({ name: "a", containers: [{ restartCount: 1 }] })], 3000),
      5,
    );
    expect(out.total).toBe(3000);
    expect(out.counted).toBe(1);
    expect(out.truncated).toBe(true);
  });
});

describe("rankPending and pendingPodsView", () => {
  test("an empty payload does not throw and reports nothing", () => {
    const out = pendingPodsView(null, 5, NOW);
    expect(out.pods).toEqual([]);
    expect(out.pending).toBe(0);
    expect(out.unschedulable).toBe(0);
    expect(out.truncated).toBe(false);
  });

  test("only pending and unschedulable pods appear", () => {
    const out = pendingPodsView(
      page([
        pod({ name: "running" }),
        pod({ name: "done", phase: "Succeeded" }),
        pod({ name: "waiting", phase: "Pending", scheduled: "True" }),
      ]),
      5,
      NOW,
    );
    expect(out.pods.map((p) => p.name)).toEqual(["waiting"]);
    expect(out.pending).toBe(1);
    expect(out.unschedulable).toBe(0);
  });

  test("unschedulable pods lead, then the longest-waiting", () => {
    // Unschedulable first because it is the one that will not resolve on its
    // own. Within a state, oldest first: a pod pending for an hour is a
    // finding, one pending for ten seconds is a deploy in progress.
    const ranked = rankPending(
      classifyPods(
        page([
          pod({
            name: "young-pending",
            phase: "Pending",
            created: "2026-09-20T11:59:00.000Z",
          }),
          pod({
            name: "old-pending",
            phase: "Pending",
            created: "2026-09-20T09:00:00.000Z",
          }),
          pod({
            name: "unschedulable",
            phase: "Pending",
            scheduled: "False",
            created: "2026-09-20T11:59:30.000Z",
          }),
        ]),
        NOW,
      ),
    );
    expect(ranked.map((p) => p.name)).toEqual([
      "unschedulable",
      "old-pending",
      "young-pending",
    ]);
  });

  test("a pod whose age cannot be read sorts after ones whose age can", () => {
    const ranked = rankPending(
      classifyPods(
        page([
          pod({ name: "ageless", phase: "Pending", created: "not a date" }),
          pod({
            name: "dated",
            phase: "Pending",
            created: "2026-09-20T11:59:00.000Z",
          }),
        ]),
        NOW,
      ),
    );
    expect(ranked.map((p) => p.name)).toEqual(["dated", "ageless"]);
  });

  test("counts cover the whole page while the list is capped", () => {
    const items = [
      pod({ name: "u1", phase: "Pending", scheduled: "False" }),
      pod({ name: "u2", phase: "Pending", scheduled: "False" }),
      pod({ name: "p1", phase: "Pending" }),
    ];
    const out = pendingPodsView(page(items, 900), 1, NOW);
    expect(out.pods).toHaveLength(1);
    expect(out.unschedulable).toBe(2);
    expect(out.pending).toBe(1);
    expect(out.total).toBe(900);
    expect(out.truncated).toBe(true);
  });
});
