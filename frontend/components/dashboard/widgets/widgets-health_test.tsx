/** @jsxImportSource react */
import { afterEach, describe, expect, test } from "bun:test";
import type { SourceState } from "@/lib/dashboard/data.ts";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { getWidget } = await import("@/lib/dashboard/registry.ts");
const { PARAM_KEY_NAMESPACE, sourceKeyFor } = await import(
  "@/lib/dashboard/params.ts"
);
for (const widget of [
  "QuotaPressure",
  "WorkloadHealth",
  "NodeConditions",
  "NotificationsFeed",
  "PolicyCompliance",
  "VulnerabilitySeverity",
  "AuditActivity",
  "SnapshotHealth",
]) {
  await import(`./${widget}Widget.tsx`);
}

const seeded = new Set<string>();

/** Seeds one source key the way a landed fetch would. */
function seed(key: string, data: unknown): void {
  seeded.add(key);
  const state: SourceState = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
  dashboardData.signalFor(key).value = state;
}

afterEach(() => {
  for (const key of seeded) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
  seeded.clear();
});

/** Renders a registered widget the way the host does. */
function show(
  id: string,
  params: Record<string, string> = {},
  mode: "normal" | "compact" = "normal",
) {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  return render(<>{def.render({ mode, params })}</>);
}

const text = (c: Element) => c.textContent ?? "";

describe("quota-pressure", () => {
  const ns = (
    namespace: string,
    highestUtilization: number,
    over: Record<string, unknown> = {},
  ) => ({
    namespace,
    hasQuota: true,
    highestUtilization,
    status: "ok",
    quotaCount: 1,
    cpuUsedPercent: null,
    memoryUsedPercent: null,
    ...over,
  });

  test("ranks namespaces with a quota and counts the ones at risk", () => {
    seed("limits-namespaces", [
      ns("team-b", 30),
      ns("team-a", 92, { status: "critical", cpuUsedPercent: 80 }),
      { namespace: "bare", hasQuota: false },
    ]);
    const { container } = show("quota-pressure");
    const t = text(container);
    expect(t).toContain("team-a");
    expect(t).toContain("92%");
    expect(t).toContain("CPU 80%");
    expect(t).toContain("1 near limit");
    expect(t).toContain("1 namespace here has no quota");
    expect(t.indexOf("team-a")).toBeLessThan(t.indexOf("team-b"));
  });

  test("a cluster with no quotas says so", () => {
    seed("limits-namespaces", []);
    const { container } = show("quota-pressure");
    expect(text(container)).toContain(
      "No namespace on this cluster has a ResourceQuota.",
    );
  });

  test("live update", () => {
    seed("limits-namespaces", [ns("team-a", 40)]);
    const { container } = show("quota-pressure");
    expect(text(container)).toContain("team-a");
    act(() => {
      seed("limits-namespaces", [ns("team-z", 77)]);
    });
    expect(text(container)).toContain("team-z");
    expect(text(container)).toContain("77%");
    expect(text(container)).not.toContain("team-a");
  });
});

describe("workload-health", () => {
  const dep = (replicas: number, readyReplicas: number) => ({
    spec: { replicas },
    status: { readyReplicas },
  });

  function seedAll(deployments: unknown[]) {
    seed("resource-counts", {
      deployments: deployments.length,
      statefulsets: 0,
      daemonsets: 1,
    });
    seed("deployments-list", { items: deployments });
    seed("statefulsets-list", { items: [] });
    seed("daemonsets-list", {
      items: [{ status: { desiredNumberScheduled: 2, numberReady: 2 } }],
    });
  }

  test("shows ready over total per kind and the degraded count", () => {
    seedAll([dep(1, 1), dep(1, 1), dep(2, 1)]);
    const { container } = show("workload-health");
    const t = text(container);
    expect(t).toContain("2/3");
    expect(t).toContain("1/1");
    expect(t).toContain("3 ready · 1 degraded");
  });

  test("a cluster with none of the kinds says so", () => {
    seed("resource-counts", { deployments: 0, statefulsets: 0, daemonsets: 0 });
    seed("deployments-list", { items: [] });
    seed("statefulsets-list", { items: [] });
    seed("daemonsets-list", { items: [] });
    const { container } = show("workload-health");
    expect(text(container)).toContain(
      "No Deployments, StatefulSets or DaemonSets on this cluster.",
    );
  });

  test("live update", () => {
    seedAll([dep(1, 0)]);
    const { container } = show("workload-health");
    expect(text(container)).toContain("0/1");
    act(() => {
      seedAll([dep(1, 1), dep(1, 1)]);
    });
    expect(text(container)).toContain("2/2");
    expect(text(container)).not.toContain("0/1");
  });
});

describe("node-conditions", () => {
  const node = (name: string, ready: "True" | "False") => ({
    metadata: { name },
    status: { conditions: [{ type: "Ready", status: ready }] },
  });
  const page = (items: unknown[]) => ({ items, total: items.length });

  test("lists the affected node and the affected count", () => {
    seed(
      "nodes-list",
      page([node("worker-1", "False"), node("worker-2", "True")]),
    );
    const { container } = show("node-conditions");
    const t = text(container);
    expect(t).toContain("worker-1");
    expect(t).not.toContain("worker-2");
    expect(t).toContain("1 of 2 affected");
  });

  test("all-clear state when every node is ready", () => {
    seed("nodes-list", page([node("a", "True"), node("b", "True")]));
    const { container } = show("node-conditions");
    expect(text(container)).toContain(
      "All 2 nodes are ready and reporting no pressure.",
    );
  });

  test("live update", () => {
    seed("nodes-list", page([node("old-node", "False")]));
    const { container } = show("node-conditions");
    expect(text(container)).toContain("old-node");
    act(() => {
      seed("nodes-list", page([node("new-node", "False")]));
    });
    expect(text(container)).toContain("new-node");
    expect(text(container)).not.toContain("old-node");
  });
});

describe("notifications-feed", () => {
  const item = (id: string, title: string, severity = "warning") => ({
    id,
    title,
    severity,
    source: "alert",
    createdAt: "",
  });

  test("lists unread notifications with the server count", () => {
    seed("unread-notifications", {
      items: [item("1", "Disk almost full", "critical")],
      total: 1,
    });
    const { container } = show("notifications-feed");
    expect(text(container)).toContain("Disk almost full");
    expect(text(container)).toContain("1 unread");
  });

  test("compact mode is the unread number alone", () => {
    seed("unread-notifications", {
      items: [item("1", "a"), item("2", "b")],
      total: 7,
    });
    const { container } = show("notifications-feed", {}, "compact");
    expect(text(container)).toContain("7");
    expect(text(container)).toContain("unread");
    expect(text(container)).not.toContain("View all notifications");
  });

  test("nothing unread", () => {
    seed("unread-notifications", { items: [], total: 0 });
    const { container } = show("notifications-feed");
    expect(text(container)).toContain("Nothing unread.");
  });

  test("live update", () => {
    seed("unread-notifications", { items: [item("1", "First")], total: 1 });
    const { container } = show("notifications-feed");
    expect(text(container)).toContain("First");
    act(() => {
      seed("unread-notifications", {
        items: [item("2", "Second"), item("3", "Third")],
        total: 2,
      });
    });
    expect(text(container)).toContain("Second");
    expect(text(container)).toContain("2 unread");
    expect(text(container)).not.toContain("First");
  });
});

describe("policy-compliance", () => {
  const score = (total: number, over: Record<string, unknown> = {}) => ({
    score: 80,
    pass: 8,
    fail: 2,
    warn: 1,
    total,
    bySeverity: { high: { fail: 2 } },
    ...over,
  });

  test("shows the counts and the trend delta", () => {
    seed("policy-compliance-score", score(11));
    seed("policy-compliance-history", [{ score: 70 }, { score: 80 }]);
    const { container } = show("policy-compliance");
    const t = text(container);
    expect(t).toContain("11 policies");
    expect(t).toContain("passing");
    expect(t).toContain("blocked");
    expect(t).toContain("Up 10 points");
  });

  test("an engine with no policies is not scored", () => {
    seed("policy-compliance-score", score(0, { score: 100, pass: 0, fail: 0 }));
    seed("policy-compliance-history", []);
    const { container } = show("policy-compliance");
    expect(text(container)).toContain("no policies are defined");
  });

  test("live update", () => {
    seed("policy-compliance-score", score(4));
    seed("policy-compliance-history", []);
    const { container } = show("policy-compliance");
    expect(text(container)).toContain("4 policies");
    act(() => {
      seed("policy-compliance-score", score(9));
    });
    expect(text(container)).toContain("9 policies");
  });
});

describe("vulnerability-severity", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod" };
  const key = sourceKeyFor("vulnerability-reports", params);
  const report = (
    name: string,
    total: Partial<Record<"critical" | "high" | "medium" | "low", number>>,
  ) => ({
    namespace: "prod",
    kind: "Deployment",
    name,
    total: { critical: 0, high: 0, medium: 0, low: 0, ...total },
    scanner: "trivy",
  });

  test("lists the worst workload and the coverage line", () => {
    seed(key, {
      vulnerabilities: [report("api", { critical: 2 }), report("web", {})],
    });
    const { container } = show("vulnerability-severity", params);
    const t = text(container);
    expect(t).toContain("prod");
    expect(t).toContain("api");
    expect(t).toContain("1 of 2 scanned workloads affected.");
  });

  test("no reports is not a clean namespace", () => {
    seed(key, { vulnerabilities: [] });
    const { container } = show("vulnerability-severity", params);
    expect(text(container)).toContain("No scan reports for workloads in prod");
  });

  test("scanned and clean", () => {
    seed(key, { vulnerabilities: [report("api", {})] });
    const { container } = show("vulnerability-severity", params);
    expect(text(container)).toContain(
      "No vulnerabilities across 1 scanned workload in prod.",
    );
  });

  test("live update", () => {
    seed(key, { vulnerabilities: [report("api", { high: 1 })] });
    const { container } = show("vulnerability-severity", params);
    expect(text(container)).toContain("api");
    act(() => {
      seed(key, { vulnerabilities: [report("billing", { low: 3 })] });
    });
    expect(text(container)).toContain("billing");
    expect(text(container)).not.toContain("api");
  });
});

describe("audit-activity", () => {
  const entry = (user: string, action: string, result = "success") => ({
    timestamp: "",
    user,
    action,
    result,
    resourceKind: "Deployment",
    resourceNamespace: "prod",
    resourceName: "web",
  });

  test("shows who did what to which object", () => {
    seed("audit-log", [entry("alice", "delete", "denied")]);
    const { container } = show("audit-activity");
    const t = text(container);
    expect(t).toContain("alice");
    expect(t).toContain("delete");
    expect(t).toContain("Deployment/prod/web");
    expect(t).toContain("last 1");
  });

  test("two identical entries both render", () => {
    seed("audit-log", [entry("bob", "scale"), entry("bob", "scale")]);
    const { container } = show("audit-activity");
    expect(container.querySelectorAll("li").length).toBe(2);
  });

  test("an empty log says nothing was recorded", () => {
    seed("audit-log", []);
    const { container } = show("audit-activity");
    expect(text(container)).toContain(
      "Audit logging is on and has recorded nothing yet.",
    );
  });

  test("live update", () => {
    seed("audit-log", [entry("alice", "delete")]);
    const { container } = show("audit-activity");
    expect(text(container)).toContain("alice");
    act(() => {
      seed("audit-log", [entry("carol", "patch")]);
    });
    expect(text(container)).toContain("carol");
    expect(text(container)).not.toContain("alice");
  });
});

describe("snapshot-health", () => {
  const snap = (name: string, readyToUse: boolean, errorMessage = "") => ({
    name,
    namespace: "db",
    readyToUse,
    sourcePVC: "data",
    errorMessage,
  });

  test("lists the unhealthy snapshot with its reason", () => {
    seed("snapshots-list", [
      snap("snap-bad", false, "quota exceeded"),
      snap("snap-ok", true),
    ]);
    const { container } = show("snapshot-health");
    const t = text(container);
    expect(t).toContain("snap-bad");
    expect(t).toContain("quota exceeded");
    expect(t).toContain("2 snapshots");
    expect(t).not.toContain("snap-ok");
  });

  test("all ready", () => {
    seed("snapshots-list", [snap("only", true)]);
    const { container } = show("snapshot-health");
    expect(text(container)).toContain(
      "All 1 snapshot is ready to restore from.",
    );
  });

  test("none exist", () => {
    seed("snapshots-list", []);
    const { container } = show("snapshot-health");
    expect(text(container)).toContain("none you can see exists");
  });

  test("live update", () => {
    seed("snapshots-list", [snap("first", false)]);
    const { container } = show("snapshot-health");
    expect(text(container)).toContain("first");
    act(() => {
      seed("snapshots-list", [snap("second", false), snap("third", false)]);
    });
    expect(text(container)).toContain("second");
    expect(text(container)).toContain("2 snapshots");
    expect(text(container)).not.toContain("first");
  });
});
