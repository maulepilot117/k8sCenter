/** @jsxImportSource react */
import { afterEach, describe, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { getWidget } = await import("@/lib/dashboard/registry.ts");
const { selectedCluster } = await import("@/src/lib/cluster.ts");
// Single widget files rather than the index, so the test loads just the
// widgets it checks.
for (const file of [
  "CpuTileWidget",
  "MemoryTileWidget",
  "NetworkTileWidget",
  "PodsTileWidget",
  "ResourceUtilizationWidget",
  "PodStatusWidget",
  "ActiveAlertsWidget",
  "PendingPodsWidget",
  "PodRestartsWidget",
]) {
  await import(`@/components/dashboard/widgets/${file}.tsx`);
}

const KEYS = ["dashboard-summary", "dashboard-trends", "pods-list"];

/** Writes one source key's state, the way a landed fetch would. */
function seed(key: string, data: unknown, range = "1h"): void {
  dashboardData.signalFor(key).value = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range,
  };
}

function mount(id: string) {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  return render(<>{def.render({ mode: "normal", params: {} })}</>);
}

function summary(over: Record<string, unknown> = {}) {
  return {
    nodes: { total: 3, ready: 3 },
    pods: { total: 42, running: 37, pending: 3, failed: 2 },
    alerts: { active: 5, critical: 2 },
    cpu: {
      percentage: 63.4,
      used: "5",
      total: "8",
      requests: "3",
      limits: "4",
    },
    memory: {
      percentage: 41.6,
      used: "10Gi",
      total: "24Gi",
      requests: "6Gi",
      limits: "8Gi",
    },
    ...over,
  };
}

afterEach(() => {
  for (const key of KEYS) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
  selectedCluster.value = "local";
});

const podItem = (
  name: string,
  status: Record<string, unknown>,
  namespace = "prod",
) => ({
  metadata: { name, namespace },
  status,
});

const pendingPod = (name: string, unschedulable: boolean) =>
  podItem(name, {
    phase: "Pending",
    conditions: unschedulable
      ? [{ type: "PodScheduled", status: "False", reason: "Unschedulable" }]
      : [],
  });

const loopingPod = (name: string, restarts: number) =>
  podItem(name, {
    phase: "Running",
    containerStatuses: [
      {
        restartCount: restarts,
        state: { waiting: { reason: "CrashLoopBackOff" } },
      },
    ],
  });

const restartedPod = (name: string, restarts: number) =>
  podItem(name, {
    phase: "Running",
    containerStatuses: [{ restartCount: restarts, state: { running: {} } }],
  });

describe("cpu-tile", () => {
  test("shows the rounded percentage", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("cpu-tile");
    expect(container.textContent).toContain("CPU");
    expect(container.textContent).toContain("63%");
  });

  test("shows a dash, not 0%, when usage was not observed", () => {
    seed(
      "dashboard-summary",
      summary({
        cpu: {
          percentage: 0,
          used: "N/A",
          total: "8",
          requests: "",
          limits: "",
        },
      }),
    );
    const { container } = mount("cpu-tile");
    expect(container.textContent).toContain("—");
    expect(container.textContent).not.toContain("0%");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("cpu-tile");
    expect(container.textContent).toContain("63%");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ cpu: { percentage: 12, used: "1" } }),
      );
    });
    expect(container.textContent).toContain("12%");
    expect(container.textContent).not.toContain("63%");
  });
});

describe("memory-tile", () => {
  test("shows the rounded percentage", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("memory-tile");
    expect(container.textContent).toContain("Memory");
    expect(container.textContent).toContain("42%");
  });

  test("shows a dash when there is no allocatable capacity", () => {
    seed("dashboard-summary", summary({ memory: null }));
    const { container } = mount("memory-tile");
    expect(container.textContent).toContain("—");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("memory-tile");
    expect(container.textContent).toContain("42%");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ memory: { percentage: 88, used: "20Gi" } }),
      );
    });
    expect(container.textContent).toContain("88%");
  });

  test("shows the trend delta once trends land", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("memory-tile");
    expect(container.textContent).not.toContain("▲");
    act(() => {
      seed("dashboard-trends", { memory: [50, 60], pods: [], cpu: [] });
    });
    expect(container.textContent).toContain("▲20%");
  });
});

describe("network-tile", () => {
  const trends = (rx: number[], tx: number[]) => ({
    pods: null,
    cpu: null,
    memory: null,
    networkRx: rx,
    networkTx: tx,
  });

  test("shows RX and TX p95 and the period of the landed series", () => {
    seed("dashboard-trends", trends([1, 2, 3, 4, 5], [10, 10, 10]), "6h");
    const { container } = mount("network-tile");
    expect(container.textContent).toContain("RX");
    expect(container.textContent).toContain("TX");
    expect(container.textContent).toContain("4.8");
    expect(container.textContent).toContain("10");
    expect(container.textContent).toContain("6h");
  });

  test("shows zero throughput for empty series", () => {
    seed("dashboard-trends", trends([], []));
    const { container } = mount("network-tile");
    expect(container.textContent).toContain("0");
  });

  test("re-renders when the trends change", () => {
    seed("dashboard-trends", trends([1, 1], [1, 1]), "1h");
    const { container } = mount("network-tile");
    expect(container.textContent).toContain("1h");
    act(() => {
      seed("dashboard-trends", trends([50, 50], [70, 70]), "24h");
    });
    expect(container.textContent).toContain("24h");
    expect(container.textContent).toContain("70");
  });
});

describe("pods-tile", () => {
  test("shows the pod total and running count", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("pods-tile");
    expect(container.textContent).toContain("42");
    expect(container.textContent).toContain("/37 running");
  });

  test("shows no running line while the summary is absent", () => {
    const { container } = mount("pods-tile");
    expect(container.textContent).not.toContain("running");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("pods-tile");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ pods: { total: 7, running: 5, pending: 1, failed: 1 } }),
      );
    });
    expect(container.textContent).toContain("/5 running");
    expect(container.textContent).not.toContain("/37 running");
  });
});

describe("resource-utilization", () => {
  test("renders the card title and legend", () => {
    seed("dashboard-trends", { cpu: [10, 20, 30], memory: [30, 40, 50] });
    const { container } = mount("resource-utilization");
    expect(container.textContent).toContain("Resource Utilization");
    expect(container.textContent).toContain("CPU");
    expect(container.textContent).toContain("Memory");
  });

  test("still renders its card without any trends", () => {
    const { container } = mount("resource-utilization");
    expect(container.textContent).toContain("Resource Utilization");
  });

  test("redraws the chart when the trends change", () => {
    seed("dashboard-trends", { cpu: null, memory: null });
    const { container } = mount("resource-utilization");
    const before = container.innerHTML;
    act(() => {
      seed("dashboard-trends", {
        cpu: [10, 90, 20, 80],
        memory: [30, 40, 50, 60],
      });
    });
    expect(container.innerHTML).not.toBe(before);
  });
});

describe("pod-status", () => {
  test("shows the total and the three phase counts", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("pod-status");
    const text = container.textContent ?? "";
    expect(text).toContain("42pods");
    expect(text).toContain("Running37");
    expect(text).toContain("Pending3");
    expect(text).toContain("Failed2");
  });

  test("shows zero counts for an empty cluster", () => {
    seed(
      "dashboard-summary",
      summary({ pods: { total: 0, running: 0, pending: 0, failed: 0 } }),
    );
    const { container } = mount("pod-status");
    const text = container.textContent ?? "";
    expect(text).toContain("0pods");
    expect(text).toContain("Running0");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("pod-status");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ pods: { total: 9, running: 4, pending: 0, failed: 5 } }),
      );
    });
    const text = container.textContent ?? "";
    expect(text).toContain("9pods");
    expect(text).toContain("Failed5");
  });
});

describe("active-alerts", () => {
  test("shows the firing, critical and warning counts and the link", () => {
    seed("dashboard-summary", summary());
    const { container } = mount("active-alerts");
    const text = container.textContent ?? "";
    expect(text).toContain("5 firing");
    expect(text).toContain("2 critical");
    expect(text).toContain("+3 warnings");
    expect(container.querySelector('a[href="/alerting"]')).not.toBeNull();
  });

  test("hides the alerts link on a remote cluster", () => {
    seed("dashboard-summary", summary());
    selectedCluster.value = "remote-1";
    const { container } = mount("active-alerts");
    expect(container.querySelector('a[href="/alerting"]')).toBeNull();
  });

  test("shows All clear when nothing is firing", () => {
    seed("dashboard-summary", summary({ alerts: { active: 0, critical: 0 } }));
    const { container } = mount("active-alerts");
    expect(container.textContent).toContain("All clear");
    expect(container.textContent).not.toContain("firing");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary({ alerts: { active: 0, critical: 0 } }));
    const { container } = mount("active-alerts");
    expect(container.textContent).toContain("All clear");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ alerts: { active: 1, critical: 1 } }),
      );
    });
    expect(container.textContent).toContain("1 firing");
    expect(container.textContent).toContain("1 critical");
    expect(container.textContent).not.toContain("All clear");
  });
});

describe("pending-pods", () => {
  test("lists waiting pods with the unschedulable count in the header", () => {
    seed("pods-list", {
      items: [
        pendingPod("starting-a", false),
        pendingPod("stuck-b", true),
        restartedPod("fine-c", 0),
      ],
      total: 3,
    });
    const { container } = mount("pending-pods");
    const text = container.textContent ?? "";
    expect(text).toContain("1 unschedulable");
    expect(text).toContain("stuck-b");
    expect(text).toContain("starting-a");
    expect(text).not.toContain("fine-c");
    expect(text).toContain("Unschedulable");
  });

  test("shows the empty state when every pod started", () => {
    seed("pods-list", { items: [restartedPod("ok", 0)], total: 1 });
    const { container } = mount("pending-pods");
    expect(container.textContent).toContain(
      "Every pod has been scheduled and started.",
    );
  });

  test("does not claim a clean cluster over an unreadable page", () => {
    seed("pods-list", { items: null, total: 0 });
    const { container } = mount("pending-pods");
    expect(container.textContent).toContain("cannot read");
    expect(container.textContent).not.toContain("Every pod has been");
  });

  test("re-renders when the pod list changes", () => {
    seed("pods-list", { items: [pendingPod("first-pod", false)], total: 1 });
    const { container } = mount("pending-pods");
    expect(container.textContent).toContain("first-pod");
    act(() => {
      seed("pods-list", { items: [pendingPod("second-pod", true)], total: 1 });
    });
    expect(container.textContent).toContain("second-pod");
    expect(container.textContent).not.toContain("first-pod");
  });
});

describe("pod-restarts", () => {
  test("ranks a crash loop first and summarises the loops", () => {
    seed("pods-list", {
      items: [
        restartedPod("calm-many", 40),
        loopingPod("loopy", 3),
        restartedPod("quiet", 0),
      ],
      total: 3,
    });
    const { container } = mount("pod-restarts");
    const text = container.textContent ?? "";
    expect(text).toContain("1 crash-looping");
    expect(text).toContain("looping");
    expect(text).not.toContain("quiet");
    expect(text.indexOf("loopy")).toBeLessThan(text.indexOf("calm-many"));
  });

  test("summarises restarts when nothing is looping", () => {
    seed("pods-list", { items: [restartedPod("a", 1)], total: 1 });
    const { container } = mount("pod-restarts");
    expect(container.textContent).toContain("1 restart");
    expect(container.textContent).not.toContain("crash-looping");
  });

  test("shows the empty state when no pod restarted", () => {
    seed("pods-list", { items: [restartedPod("a", 0)], total: 1 });
    const { container } = mount("pod-restarts");
    expect(container.textContent).toContain("No pod has restarted.");
  });

  test("does not claim a clean cluster over an unreadable page", () => {
    seed("pods-list", { items: "nope", total: 0 });
    const { container } = mount("pod-restarts");
    expect(container.textContent).toContain("cannot read");
    expect(container.textContent).not.toContain("No pod has restarted.");
  });

  test("re-renders when the pod list changes", () => {
    seed("pods-list", { items: [restartedPod("old-pod", 2)], total: 1 });
    const { container } = mount("pod-restarts");
    expect(container.textContent).toContain("old-pod");
    act(() => {
      seed("pods-list", { items: [restartedPod("new-pod", 5)], total: 1 });
    });
    expect(container.textContent).toContain("new-pod");
    expect(container.textContent).not.toContain("old-pod");
  });
});
