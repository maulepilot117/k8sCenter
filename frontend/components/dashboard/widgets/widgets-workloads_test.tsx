/** @jsxImportSource react */
import { afterEach, describe, expect, test } from "bun:test";
import { act, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { dashboardData } = await import("@/lib/dashboard/data.ts");
const { PARAM_KEY_NAMESPACE, sourceKeyFor } = await import(
  "@/lib/dashboard/params.ts"
);
const { getWidget } = await import("@/lib/dashboard/registry.ts");
const { selectedCluster } = await import("@/src/lib/cluster.ts");
await import("@/components/dashboard/widgets/NodesWidget.tsx");
await import("@/components/dashboard/widgets/DiagnosticsSummaryWidget.tsx");
await import("@/components/dashboard/widgets/RecentEventsWidget.tsx");
await import("@/components/dashboard/widgets/SavedViewsWidget.tsx");
await import("@/components/dashboard/widgets/PinnedResourcesWidget.tsx");
await import("@/components/dashboard/widgets/PolicyViolationsWidget.tsx");
await import("@/components/dashboard/widgets/GitOpsRecentSyncsWidget.tsx");
await import("@/components/dashboard/widgets/PDBRiskWidget.tsx");

const SEEDED = new Set<string>();

function seed(key: string, data: unknown): void {
  SEEDED.add(key);
  dashboardData.signalFor(key).value = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
}

afterEach(() => {
  for (const key of SEEDED) {
    dashboardData.signalFor(key).value = {
      data: null,
      error: null,
      errorKind: null,
      loading: false,
      range: "1h",
    };
  }
  SEEDED.clear();
  selectedCluster.value = "local";
});

function show(id: string, params: Record<string, string> = {}) {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  return render(<>{def.render({ mode: "normal", params })}</>);
}

function text(): string {
  return (document.body.textContent ?? "").replace(/\s+/g, " ");
}

function byTestId(id: string): string {
  return (
    document.body.querySelector(`[data-testid="${id}"]`)?.textContent ?? ""
  );
}

describe("nodes", () => {
  const summary = (total: number, ready: number, pods: number) => ({
    nodes: { total, ready },
    pods: { total: pods, running: pods, pending: 0, failed: 0 },
    alerts: { active: 0, critical: 0 },
    cpu: {
      percentage: 41.6,
      used: "4",
      total: "8",
      requests: "1",
      limits: "2",
    },
    memory: {
      percentage: 63,
      used: "10Gi",
      total: "16Gi",
      requests: "1",
      limits: "2",
    },
  });

  test("shows readiness, percentages and pod count from the payload", () => {
    seed("dashboard-summary", summary(3, 2, 57));
    show("nodes");
    expect(text()).toContain("2 ready · 1 under pressure");
    expect(text()).toContain("42%");
    expect(text()).toContain("63%");
    expect(text()).toContain("57");
    expect(text()).toContain("2/3");
  });

  test("says there is no node data when the cluster reports no nodes", () => {
    seed("dashboard-summary", summary(0, 0, 0));
    show("nodes");
    expect(text()).toContain("No node data available");
  });

  test("re-renders when the summary changes", () => {
    seed("dashboard-summary", summary(3, 2, 57));
    show("nodes");
    expect(text()).toContain("2/3");
    act(() => {
      seed("dashboard-summary", summary(5, 5, 90));
    });
    expect(text()).toContain("5/5");
    expect(text()).toContain("5 ready");
    expect(text()).not.toContain("under pressure");
  });
});

describe("diagnostics-summary", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod" };
  const key = sourceKeyFor("diagnostics-summary", params);
  const payload = (failing: string[], total: number) => ({
    failing: failing.map((reason, i) => ({
      kind: "Pod",
      name: `p${i}`,
      reason,
    })),
    total,
  });

  test("shows critical, pending and healthy counts for the namespace", () => {
    seed(key, payload(["CrashLoopBackOff", "CrashLoopBackOff", "Pending"], 10));
    show("diagnostics-summary", params);
    expect(byTestId("diagnostics-namespace")).toBe("prod");
    expect(byTestId("diagnostics-critical")).toBe("2");
    expect(byTestId("diagnostics-warning")).toBe("1");
    expect(byTestId("diagnostics-healthy")).toBe("7");
    expect(text()).toContain("of 10 healthy");
    expect(byTestId("diagnostics-reasons")).toContain("CrashLoopBackOff");
  });

  test("an empty namespace is not reported as healthy", () => {
    seed(key, payload([], 0));
    show("diagnostics-summary", params);
    expect(byTestId("diagnostics-empty")).toContain(
      "No workloads found in prod",
    );
  });

  test("re-renders when the payload changes", () => {
    seed(key, payload(["Pending"], 4));
    show("diagnostics-summary", params);
    expect(byTestId("diagnostics-warning")).toBe("1");
    act(() => {
      seed(key, payload(["ImagePullBackOff", "ImagePullBackOff", "x"], 8));
    });
    expect(byTestId("diagnostics-critical")).toBe("3");
    expect(byTestId("diagnostics-warning")).toBe("0");
    expect(byTestId("diagnostics-healthy")).toBe("5");
  });
});

describe("recent-events", () => {
  const evt = (uid: string, kind: string, name: string, message: string) => ({
    type: "Warning",
    message,
    metadata: { uid, creationTimestamp: new Date().toISOString() },
    involvedObject: { kind, name, namespace: "shop" },
    source: { component: "kubelet" },
  });

  test("lists events with an abbreviated resource label", () => {
    seed("recent-events", [
      evt("1", "Deployment", "checkout", "Scaled up to 3"),
      evt("2", "Pod", "web-1", "Back-off restarting"),
    ]);
    show("recent-events");
    expect(text()).toContain("deploy/checkout");
    expect(text()).toContain("Scaled up to 3");
    expect(text()).toContain("pod/web-1");
    expect(text()).toContain("kubelet · shop");
    expect(text()).toContain("View all");
  });

  test("shows the quiet state when there are no events", () => {
    seed("recent-events", []);
    show("recent-events");
    expect(text()).toContain("All quiet — no recent events");
    expect(text()).not.toContain("View all");
  });

  test("re-renders when events change", () => {
    seed("recent-events", [evt("1", "Pod", "web-1", "first message")]);
    show("recent-events");
    expect(text()).toContain("first message");
    act(() => {
      seed("recent-events", [evt("2", "Service", "api", "second message")]);
    });
    expect(text()).toContain("second message");
    expect(text()).toContain("svc/api");
    expect(text()).not.toContain("first message");
  });
});

describe("saved-views", () => {
  const view = (id: string, name: string, kind: string, ns = "") => ({
    id,
    name,
    config: { resourceKind: kind, namespace: ns },
  });

  test("lists saved views with kind, namespace and a list-page link", () => {
    seed("preference-views", [
      view("a", "Failing pods", "pods", "shop"),
      view("b", "All deployments", "deployments"),
    ]);
    show("saved-views");
    expect(byTestId("saved-views-summary")).toBe("2 saved");
    expect(byTestId("saved-views-list")).toContain("Failing pods");
    expect(byTestId("saved-views-list")).toContain("pods · shop");
    expect(byTestId("saved-views-list")).toContain("deployments");
    const link = document.body.querySelector(
      '[data-testid="saved-views-list"] a',
    );
    expect(link?.getAttribute("href")).toBe("/workloads/pods");
  });

  test("tells the user how to save a first view", () => {
    seed("preference-views", []);
    show("saved-views");
    expect(byTestId("saved-views-none")).toContain(
      "You have not saved a view yet",
    );
  });

  test("re-renders when views change", () => {
    seed("preference-views", [view("a", "First view", "pods")]);
    show("saved-views");
    expect(text()).toContain("First view");
    act(() => {
      seed("preference-views", [
        view("b", "Second view", "jobs"),
        view("c", "Third view", "jobs"),
      ]);
    });
    expect(byTestId("saved-views-summary")).toBe("2 saved");
    expect(text()).toContain("Second view");
    expect(text()).not.toContain("First view");
  });
});

describe("pinned-resources", () => {
  const pin = (id: string, name: string, kind: string, cluster: string) => ({
    id,
    clusterId: cluster,
    config: { name, resourceKind: kind, namespace: "shop" },
  });

  test("lists pins on the selected cluster and counts the others", () => {
    selectedCluster.value = "local";
    seed("preference-pins", [
      pin("1", "checkout", "deployments", "local"),
      pin("2", "web-1", "pods", "local"),
      pin("3", "elsewhere", "pods", "remote-9"),
    ]);
    show("pinned-resources");
    expect(byTestId("pinned-resources-summary")).toBe("2 pinned");
    expect(byTestId("pinned-resources-list")).toContain("checkout");
    expect(byTestId("pinned-resources-list")).not.toContain("elsewhere");
    expect(byTestId("pinned-resources-other-clusters")).toContain(
      "1 more pin is held on other clusters",
    );
  });

  test("says nothing is pinned when the cluster has no pins", () => {
    seed("preference-pins", []);
    show("pinned-resources");
    expect(byTestId("pinned-resources-none")).toContain(
      "Nothing is pinned on this cluster",
    );
  });

  test("re-renders when pins change", () => {
    seed("preference-pins", [pin("1", "first-pin", "pods", "local")]);
    show("pinned-resources");
    expect(text()).toContain("first-pin");
    act(() => {
      seed("preference-pins", [pin("2", "second-pin", "pods", "local")]);
    });
    expect(text()).toContain("second-pin");
    expect(text()).not.toContain("first-pin");
  });

  test("re-renders when the selected cluster changes", () => {
    selectedCluster.value = "local";
    seed("preference-pins", [
      pin("1", "local-pin", "pods", "local"),
      pin("2", "remote-pin", "pods", "remote-9"),
    ]);
    show("pinned-resources");
    expect(text()).toContain("local-pin");
    act(() => {
      selectedCluster.value = "remote-9";
    });
    expect(text()).toContain("remote-pin");
    expect(text()).not.toContain("local-pin");
  });
});

describe("policy-violations", () => {
  const v = (
    policy: string,
    severity: string,
    name: string,
    blocking = false,
  ) => ({
    policy,
    severity,
    namespace: "shop",
    kind: "Pod",
    name,
    engine: "kyverno",
    blocking,
  });

  test("summarises the total and blocking count and lists rows", () => {
    seed("policy-violations-list", [
      v("require-labels", "medium", "a"),
      v("no-root", "critical", "b", true),
      v("no-latest", "high", "c"),
    ]);
    show("policy-violations");
    expect(byTestId("policy-violations-summary")).toBe("3 total · 1 blocking");
    const list = byTestId("policy-violations-list");
    expect(list).toContain("no-root");
    expect(list).toContain("require-labels");
    expect(list.indexOf("no-root")).toBeLessThan(list.indexOf("no-latest"));
  });

  test("an empty list reads as clear", () => {
    seed("policy-violations-list", []);
    show("policy-violations");
    expect(byTestId("policy-violations-clear")).toContain(
      "No policy violations",
    );
  });

  test("re-renders when violations change", () => {
    seed("policy-violations-list", [v("first-policy", "low", "a")]);
    show("policy-violations");
    expect(text()).toContain("first-policy");
    act(() => {
      seed("policy-violations-list", [
        v("second-policy", "high", "x"),
        v("third-policy", "high", "y"),
      ]);
    });
    expect(byTestId("policy-violations-summary")).toBe("2 total");
    expect(text()).toContain("second-policy");
    expect(text()).not.toContain("first-policy");
  });
});

describe("gitops-recent-syncs", () => {
  const app = (name: string, lastSyncTime: string | undefined, rev = "") => ({
    id: `argo:argocd:${name}`,
    name,
    namespace: "argocd",
    tool: "argocd",
    syncStatus: "synced",
    healthStatus: "healthy",
    lastSyncTime,
    currentRevision: rev,
  });

  test("ranks applications by most recent sync and shows the revision", () => {
    seed("gitops-applications", {
      applications: [
        app("older", "2026-01-01T00:00:00Z"),
        app("newest", "2026-03-01T00:00:00Z", "abcdef1234567890abcdef"),
        app("never", undefined),
      ],
    });
    show("gitops-recent-syncs");
    const list = byTestId("gitops-recent-syncs-list");
    expect(list.indexOf("newest")).toBeLessThan(list.indexOf("older"));
    expect(list).toContain("abcdef1");
    expect(list).not.toContain("never");
    expect(byTestId("gitops-recent-syncs-summary")).toBe("2 of 2");
    expect(byTestId("gitops-recent-syncs-untimed")).toContain(
      "1 application reports no sync time",
    );
  });

  test("says no application is managed when the fleet is empty", () => {
    seed("gitops-applications", { applications: [] });
    show("gitops-recent-syncs");
    expect(byTestId("gitops-recent-syncs-none")).toContain("no application");
  });

  test("re-renders when applications change", () => {
    seed("gitops-applications", {
      applications: [app("first-app", "2026-01-01T00:00:00Z")],
    });
    show("gitops-recent-syncs");
    expect(text()).toContain("first-app");
    act(() => {
      seed("gitops-applications", {
        applications: [app("second-app", "2026-02-01T00:00:00Z")],
      });
    });
    expect(text()).toContain("second-app");
    expect(text()).not.toContain("first-app");
  });
});

describe("pdb-risk", () => {
  const pdb = (name: string, allowed: number, current = 1, desired = 2) => ({
    metadata: { name, namespace: "shop" },
    status: {
      disruptionsAllowed: allowed,
      currentHealthy: current,
      desiredHealthy: desired,
      expectedPods: 2,
    },
  });
  const page = (items: unknown[]) => ({ items, total: items.length });

  test("lists budgets that block a drain with their healthy ratio", () => {
    seed("pdbs-list", page([pdb("api-pdb", 0, 1, 2), pdb("ok-pdb", 1, 3, 2)]));
    show("pdb-risk");
    expect(byTestId("pdb-risk-summary")).toBe("1 blocking");
    const list = byTestId("pdb-risk-list");
    expect(list).toContain("api-pdb");
    expect(list).toContain("blocks drain");
    expect(list).toContain("1/2");
    expect(list).not.toContain("ok-pdb");
  });

  test("states that all budgets allow disruption when none block", () => {
    seed("pdbs-list", page([pdb("ok-pdb", 1), pdb("fine-pdb", 2)]));
    show("pdb-risk");
    expect(byTestId("pdb-risk-summary")).toBe("2 allow disruption");
    expect(byTestId("pdb-risk-empty")).toContain(
      "All 2 budgets allow at least one disruption",
    );
  });

  test("says there are no budgets on an empty page", () => {
    seed("pdbs-list", page([]));
    show("pdb-risk");
    expect(byTestId("pdb-risk-empty")).toContain(
      "No PodDisruptionBudgets on this cluster",
    );
  });

  test("re-renders when the page changes", () => {
    seed("pdbs-list", page([pdb("first-pdb", 0)]));
    show("pdb-risk");
    expect(text()).toContain("first-pdb");
    act(() => {
      seed("pdbs-list", page([pdb("second-pdb", 0), pdb("third-pdb", 0)]));
    });
    expect(byTestId("pdb-risk-summary")).toBe("2 blocking");
    expect(text()).toContain("second-pdb");
    expect(text()).not.toContain("first-pdb");
  });
});
