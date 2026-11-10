/** @jsxImportSource react */
import { afterEach, describe, expect, test } from "bun:test";
import { dashboardData } from "@/lib/dashboard/data.ts";
import {
  PARAM_KEY_NAMESPACE,
  PARAM_KEY_SERVICE,
  sourceKeyFor,
} from "@/lib/dashboard/params.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import { act, body, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

// Registers the widgets under test; a lookup below would return undefined
// without these. Only these files, so nothing else in the widget set loads a
// signals integration that would mask a missing subscription.
await Promise.all(
  [
    "VeleroBackups",
    "ClusterHealth",
    "HPAStatus",
    "MeshGoldenSignals",
    "EsoHealth",
    "ClusterStatus",
    "GatewayRoutes",
    "GitOpsAppHealth",
  ].map((name) => import(`./${name}Widget.tsx`)),
);

/** Every key a test seeded, so afterEach can put it back. */
const seeded = new Set<string>();

/** Writes a source's state the way a landed fetch would. */
function seed(key: string, data: unknown): void {
  seeded.add(key);
  dashboardData.signalFor(key).value = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  };
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
function show(id: string, params: Record<string, string> = {}): void {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  render(def.render({ mode: "normal", params }));
}

function text(): string {
  return document.body.textContent ?? "";
}

function testId(id: string): string {
  return body().getByTestId(id).textContent ?? "";
}

describe("velero-backups", () => {
  const backups = [
    {
      name: "daily-a",
      namespace: "velero",
      phase: "Failed",
      startTime: "2026-01-01T00:00:00Z",
      errors: 2,
    },
    {
      name: "daily-b",
      namespace: "velero",
      phase: "Completed",
      startTime: "2026-01-02T00:00:00Z",
    },
  ];

  test("shows the total, the failing backup and its error count", () => {
    seed("velero-backups-list", backups);
    show("velero-backups");
    expect(testId("velero-backups-summary")).toBe("2 backups");
    const list = testId("velero-backups-list");
    expect(list).toContain("daily-a");
    expect(list).toContain("2 errors");
    expect(list).not.toContain("daily-b");
  });

  test("an empty readable list says Velero has no backups", () => {
    seed("velero-backups-list", []);
    show("velero-backups");
    expect(testId("velero-backups-none")).toContain("no backup you can see");
  });

  test("re-renders when the source changes", () => {
    seed("velero-backups-list", backups);
    show("velero-backups");
    expect(text()).toContain("daily-a");
    act(() => {
      seed("velero-backups-list", [
        { name: "weekly-x", phase: "PartiallyFailed", warnings: 1 },
        { name: "weekly-y", phase: "Failed" },
        { name: "weekly-z", phase: "Completed" },
      ]);
    });
    expect(testId("velero-backups-summary")).toBe("3 backups");
    expect(text()).toContain("weekly-y");
    expect(text()).not.toContain("daily-a");
  });
});

describe("cluster-health", () => {
  const summary = (
    nodes: { total: number; ready: number },
    failed: number,
    critical: number,
    health: unknown,
  ) => ({
    nodes,
    pods: { total: 40, running: 36, pending: 0, failed },
    alerts: { active: critical, critical },
    health,
  });

  test("shows the score, its label and the three checklist values", () => {
    seed(
      "dashboard-summary",
      summary({ total: 3, ready: 2 }, 4, 1, { score: 87, status: "healthy" }),
    );
    show("cluster-health");
    const t = text();
    expect(t).toContain("87");
    expect(t).toContain("HEALTHY");
    expect(t).toContain("2 / 3");
    expect(t).toContain("Failed pods");
    expect(t).toContain("4");
    expect(t).toContain("Critical alerts");
  });

  test("a null score renders the not-scored state, not a zero gauge", () => {
    seed(
      "dashboard-summary",
      summary({ total: 3, ready: 3 }, 0, 0, { score: null, status: "unknown" }),
    );
    show("cluster-health");
    expect(testId("health-unscored")).toContain("Not scored");
  });

  test("re-renders when the summary changes", () => {
    seed(
      "dashboard-summary",
      summary({ total: 3, ready: 2 }, 4, 1, { score: 87, status: "healthy" }),
    );
    show("cluster-health");
    expect(text()).toContain("2 / 3");
    act(() => {
      seed(
        "dashboard-summary",
        summary({ total: 5, ready: 1 }, 9, 0, {
          score: 41,
          status: "critical",
        }),
      );
    });
    const t = text();
    expect(t).toContain("1 / 5");
    expect(t).toContain("41");
    expect(t).toContain("CRITICAL");
    expect(t).not.toContain("2 / 3");
  });
});

describe("hpa-status", () => {
  function hpa(
    name: string,
    current: number,
    max: number,
    utilization: number,
  ) {
    return {
      metadata: { name, namespace: "prod" },
      spec: {
        minReplicas: 2,
        maxReplicas: max,
        scaleTargetRef: { kind: "Deployment", name },
        metrics: [
          {
            type: "Resource",
            resource: { name: "cpu", target: { averageUtilization: 50 } },
          },
        ],
      },
      status: {
        currentReplicas: current,
        desiredReplicas: current,
        currentMetrics: [
          {
            type: "Resource",
            resource: {
              name: "cpu",
              current: { averageUtilization: utilization },
            },
          },
        ],
      },
    };
  }

  test("an autoscaler at its ceiling over target reads out of room", () => {
    seed("hpas-list", { items: [hpa("web", 10, 10, 90)], total: 1 });
    show("hpa-status");
    expect(testId("hpa-status-summary")).toBe("1 out of room");
    const list = testId("hpa-status-list");
    expect(list).toContain("web");
    expect(list).toContain("10/10");
    expect(list).toContain("90%/50%");
  });

  test("a readable empty page says there are no autoscalers", () => {
    seed("hpas-list", { items: [], total: 0 });
    show("hpa-status");
    expect(testId("hpa-status-empty")).toContain("No HorizontalPodAutoscalers");
  });

  test("re-renders when the source changes", () => {
    seed("hpas-list", { items: [hpa("web", 10, 10, 90)], total: 1 });
    show("hpa-status");
    expect(text()).toContain("out of room");
    act(() => {
      seed("hpas-list", { items: [hpa("worker", 3, 10, 20)], total: 1 });
    });
    expect(testId("hpa-status-list")).toContain("worker");
    expect(testId("hpa-status-list")).toContain("3/10");
    expect(text()).not.toContain("out of room");
  });
});

describe("mesh-golden-signals", () => {
  const params = {
    [PARAM_KEY_NAMESPACE]: "prod",
    [PARAM_KEY_SERVICE]: "api",
  };
  const key = sourceKeyFor("mesh-golden-signals", params);

  const signals = (rps: number, p95Ms: number) => ({
    signals: {
      available: true,
      namespace: "prod",
      service: "api",
      mesh: "istio",
      rps,
      errorRate: 0,
      p50Ms: 20,
      p95Ms,
      p99Ms: 400,
      missingQueries: [],
    },
  });

  test("reads the parameterised key and shows the target and numbers", () => {
    seed(key, signals(3.5, 250));
    show("mesh-golden-signals", params);
    expect(testId("golden-signals-target")).toBe("prod/api");
    expect(testId("golden-signals-rps")).toBe("3.50");
    expect(testId("golden-signals-latency")).toContain("250ms");
  });

  test("an unavailable backend says so instead of showing zeros", () => {
    seed(key, { signals: { available: false, reason: "unreachable" } });
    show("mesh-golden-signals", params);
    expect(testId("golden-signals-unmeasured")).toContain(
      "Prometheus could not be read",
    );
  });

  test("re-renders when the source changes", () => {
    seed(key, signals(3.5, 250));
    show("mesh-golden-signals", params);
    act(() => {
      seed(key, signals(42, 900));
    });
    expect(testId("golden-signals-rps")).toBe("42");
    expect(testId("golden-signals-latency")).toContain("900ms");
    expect(testId("golden-signals-latency")).not.toContain("250ms");
  });
});

describe("eso-health", () => {
  const list = [
    {
      name: "db-creds",
      namespace: "apps",
      status: "SyncFailed",
      storeRef: { name: "vault" },
    },
    { name: "api-key", namespace: "apps", status: "Synced" },
  ];

  test("shows the synced ratio and the failing secret with its store", () => {
    seed("external-secrets-list", list);
    show("eso-health");
    expect(testId("eso-health-summary")).toBe("1 of 2 synced");
    const rows = testId("eso-health-list");
    expect(rows).toContain("db-creds");
    expect(rows).toContain("apps · vault");
    expect(rows).not.toContain("api-key");
  });

  test("an empty readable list says none exist yet", () => {
    seed("external-secrets-list", []);
    show("eso-health");
    expect(testId("eso-health-none")).toContain("no ExternalSecret");
  });

  test("re-renders when the source changes", () => {
    seed("external-secrets-list", list);
    show("eso-health");
    expect(text()).toContain("db-creds");
    act(() => {
      seed("external-secrets-list", [
        { name: "one", namespace: "apps", status: "Synced" },
      ]);
    });
    expect(testId("eso-health-summary")).toBe("1 of 1 synced");
    expect(testId("eso-health-clear")).toContain("is synced");
    expect(text()).not.toContain("db-creds");
  });
});

describe("cluster-status", () => {
  const registry = [
    {
      id: "local",
      name: "home",
      isLocal: true,
      status: "connected",
      k8sVersion: "v1.30.1",
      nodeCount: 3,
    },
    {
      id: "c2",
      name: "edge",
      status: "error",
      statusMessage: "dial tcp refused",
    },
  ];

  test("lists the worst cluster first with its probe message", () => {
    seed("clusters-list", registry);
    show("cluster-status");
    expect(testId("cluster-status-summary")).toBe("2 clusters");
    const rows = testId("cluster-status-list");
    expect(rows.indexOf("edge")).toBeLessThan(rows.indexOf("home"));
    expect(rows).toContain("dial tcp refused");
    expect(rows).toContain("v1.30.1 · 3 nodes");
  });

  test("an emptied registry says no clusters are registered", () => {
    seed("clusters-list", []);
    show("cluster-status");
    expect(testId("cluster-status-none")).toContain("No clusters");
  });

  test("re-renders when the source changes", () => {
    seed("clusters-list", registry);
    show("cluster-status");
    act(() => {
      seed("clusters-list", [{ id: "x", name: "solo", status: "connected" }]);
    });
    expect(testId("cluster-status-summary")).toBe("1 cluster");
    expect(text()).toContain("solo");
    expect(text()).not.toContain("edge");
  });
});

describe("gateway-routes", () => {
  const gateway = (name: string) => ({
    name,
    namespace: "infra",
    gatewayClassName: "istio",
    listeners: [{}, {}],
    attachedRouteCount: 3,
    conditions: [{ type: "Programmed", status: "True" }],
  });
  const route = {
    name: "r1",
    namespace: "apps",
    parentRefs: [{ name: "gw1", namespace: "infra" }],
  };

  test("shows the gateway count, its row and the route caption", () => {
    seed("gateway-gateways", [gateway("gw1")]);
    seed("gateway-httproutes", [route]);
    show("gateway-routes");
    expect(testId("gateway-routes-total")).toBe("1 gateway");
    expect(testId("gateway-routes-list")).toContain(
      "infra · istio · 2 listeners · 3 routes",
    );
    expect(testId("gateway-routes-list")).toContain("programmed");
    expect(testId("gateway-routes-caption")).toContain(
      "1 HTTP route you can see, all attached",
    );
  });

  test("no gateways says Gateway API is installed but unused", () => {
    seed("gateway-gateways", []);
    seed("gateway-httproutes", []);
    show("gateway-routes");
    expect(testId("gateway-routes-none")).toContain("no Gateway you can see");
  });

  test("re-renders when either source changes", () => {
    seed("gateway-gateways", [gateway("gw1")]);
    seed("gateway-httproutes", [route]);
    show("gateway-routes");
    act(() => {
      seed("gateway-gateways", [gateway("gw1"), gateway("gw2")]);
    });
    expect(testId("gateway-routes-total")).toBe("2 gateways");
    act(() => {
      seed("gateway-httproutes", [route, { name: "r2", namespace: "apps" }]);
    });
    expect(testId("gateway-routes-caption")).toContain(
      "2 HTTP routes you can see, 1 of them attached to no gateway",
    );
  });
});

describe("gitops-app-health", () => {
  const apps = {
    applications: [
      {
        id: "argocd:argocd:shop",
        name: "shop",
        namespace: "argocd",
        tool: "argocd",
        syncStatus: "outofsync",
        healthStatus: "healthy",
        message: "drift detected",
      },
      {
        id: "argocd:argocd:blog",
        name: "blog",
        namespace: "argocd",
        tool: "argocd",
        syncStatus: "synced",
        healthStatus: "healthy",
      },
    ],
  };

  test("shows the synced ratio and the application needing attention", () => {
    seed("gitops-applications", apps);
    show("gitops-app-health");
    expect(testId("gitops-app-health-summary")).toBe("1 of 2 synced");
    const rows = testId("gitops-app-health-list");
    expect(rows).toContain("shop");
    expect(rows).toContain("drift detected");
    expect(rows).not.toContain("blog");
  });

  test("an empty readable list says nothing is managed yet", () => {
    seed("gitops-applications", { applications: [] });
    show("gitops-app-health");
    expect(testId("gitops-app-health-none")).toContain("no application");
  });

  test("re-renders when the source changes", () => {
    seed("gitops-applications", apps);
    show("gitops-app-health");
    act(() => {
      seed("gitops-applications", {
        applications: [
          {
            id: "fluxcd:flux:one",
            name: "one",
            namespace: "flux",
            tool: "fluxcd",
            syncStatus: "synced",
            healthStatus: "healthy",
          },
        ],
      });
    });
    expect(testId("gitops-app-health-summary")).toBe("1 of 1 synced");
    expect(testId("gitops-app-health-clear")).toContain("synced and healthy");
    expect(text()).not.toContain("shop");
  });
});
