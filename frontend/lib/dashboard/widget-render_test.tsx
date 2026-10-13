/** @jsxImportSource preact */
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { render } from "preact-render-to-string";
// Registers every widget. Without it the registry is empty and each lookup
// below would return undefined, so these tests would pass vacuously.
import "@/components/dashboard/widgets/index.ts";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { selectedCluster } from "@/src/lib/cluster.ts";
import type { CoveredSummary } from "./coverage.ts";
import type { SourceState } from "./data.ts";
import { dashboardData, METRICS_NOT_CONFIGURED_MESSAGE } from "./data.ts";
import {
  PARAM_KEY_NAMESPACE,
  PARAM_KEY_SERVICE,
  sourceKeyFor,
} from "./params.ts";
import { getWidget } from "./registry.ts";

/**
 * Render coverage for the widget bodies.
 *
 * Everything else in lib/dashboard is a pure function with its own tests, and
 * those are where the arithmetic lives. What had no coverage at all was the
 * JSX wiring on top of them: which source key a widget reads, and which of
 * its branches that produces. A widget can pass every pure-module test in the
 * repo and still render the wrong branch, or read a key nothing fetches and
 * sit in a skeleton for good, and nothing would catch it -- the catalog's
 * e2e specs cover placement mechanics and the ten default widgets, not a
 * card's rendered content against known data.
 *
 * These are a representative set rather than one per widget. All 38 bodies
 * are built to one shape, so a defect in the shape is a defect 38 times and a
 * per-widget file would be 38 near-identical copies of the same three
 * assertions. The ones chosen below span the shapes that actually differ:
 * a list page with a readable/unreadable distinction, an envelope-backed
 * card, and a card whose absence arrives as a status code rather than a
 * discovery route.
 *
 * `preact-render-to-string` rather than a DOM harness: these branches are
 * static output, no events and no effects, so a DOM adds a dependency and a
 * global registrator for nothing. It was already in the tree via
 * @astrojs/preact; this file is why it is now declared directly.
 */

/** Seeds one source key's state, the way a landed fetch would. */
function seed(key: string, over: Partial<SourceState>): void {
  dashboardData.signalFor(key).value = {
    data: null,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
    ...over,
  };
}

/** Renders a registered widget by id, exactly as the host would. */
function renderWidget(id: string, params: Record<string, string> = {}): string {
  const def = getWidget(id);
  if (def === undefined) {
    throw new Error(
      `${id} is not registered. The manifest import at the top of this file ` +
        `is what registers widgets; a lookup failing here means the widget ` +
        `was renamed or dropped, not that the test is wrong.`,
    );
  }
  return render(def.render({ mode: "normal", params }));
}

/** A ResourceListPage payload, including the malformed shapes. */
function page(items: unknown, total?: number): unknown {
  return { items, total: total ?? (Array.isArray(items) ? items.length : 0) };
}

function node(name: string, conditions: Array<[string, string]>) {
  return {
    metadata: { name },
    status: {
      conditions: conditions.map(([type, status]) => ({ type, status })),
    },
  };
}

describe("node-conditions", () => {
  beforeEach(() => {
    seed("nodes-list", { data: null });
  });

  test("a readable page with a problem renders the list, not the empty state", () => {
    seed("nodes-list", {
      data: page([
        node("n1", [["Ready", "False"]]),
        node("n2", [["Ready", "True"]]),
      ]),
    });
    const html = renderWidget("node-conditions");
    expect(html).toContain("node-conditions-list");
    expect(html).not.toContain("node-conditions-empty");
    expect(html).not.toContain("node-conditions-unreadable-page");
  });

  test("a readable empty page renders the clean state", () => {
    seed("nodes-list", { data: page([node("n1", [["Ready", "True"]])]) });
    const html = renderWidget("node-conditions");
    expect(html).toContain("node-conditions-empty");
    expect(html).not.toContain("node-conditions-unreadable-page");
  });

  // The defect this release exists to prevent, at the point it reaches a
  // screen. Every count is zero in both cases; only `readable` tells them
  // apart, and only if the card actually branches on it.
  test("an unreadable page does NOT render the clean state", () => {
    for (const bad of [page(null), page("nodes"), page(7)]) {
      seed("nodes-list", { data: bad });
      const html = renderWidget("node-conditions");
      expect(html).toContain("node-conditions-unreadable-page");
      expect(html).not.toContain("node-conditions-empty");
      expect(html).not.toContain("node-conditions-list");
    }
  });
});

// A PARAMETERIZED widget, which is the shape most likely to read a key
// nothing ever fetched: its source is cached under a key carrying the
// parameter values, so a body that looked up the bare source name would find
// an idle entry and render a skeleton for good.
describe("vulnerability-severity", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod" };
  const key = sourceKeyFor("vulnerability-reports", params);

  test("reads the key its parameters resolve to, not the bare source name", () => {
    // Seeded ONLY under the parameterized key. If the body read
    // "vulnerability-reports" directly it would see idle data here.
    seed(key, { data: { vulnerabilities: [] } });
    const html = renderWidget("vulnerability-severity", params);
    // An empty list is a READ that found nothing scanned -- distinct from
    // both the unreadable state and the skeleton a wrong key would leave.
    // The namespace chip proves the params reached the body too.
    expect(html).toContain("vulnerability-unscanned");
    expect(html).toContain("prod");
    expect(html).not.toContain("vulnerability-unreadable");
  });

  // The finding #476 shipped to fix, at the point it reaches a screen: the
  // scanning route answers 200 with a null list when both scanner fetches
  // failed, or when the viewer holds one scanner's grant and every report
  // came from the other. Neither is a clean scan.
  test("a null list is not a clean scan", () => {
    seed(key, { data: { vulnerabilities: null } });
    const html = renderWidget("vulnerability-severity", params);
    expect(html).toContain("vulnerability-unreadable");
    expect(html).not.toContain("vulnerability-clear");
  });
});

// A PLATFORM widget, whose absence is a status code rather than a discovery
// route, and whose empty state is the common case -- an account with nothing
// unread. That combination is what made its null-instead-of-empty defect the
// worst of the release: the card would sit in its skeleton forever on exactly
// the account it has the best news for.
describe("notifications-feed", () => {
  test("an empty feed renders the none state, not a skeleton", () => {
    seed("unread-notifications", { data: { items: [] } });
    const html = renderWidget("notifications-feed");
    expect(html).toContain("notifications-feed-none");
    expect(html).not.toContain("notifications-feed-unreadable");
  });

  test("a null body is unreadable, not empty", () => {
    seed("unread-notifications", { data: { items: null } });
    const html = renderWidget("notifications-feed");
    expect(html).toContain("notifications-feed-unreadable");
    expect(html).not.toContain("notifications-feed-none");
  });
});

// The widget whose central promise is a sentence rather than a number: a row
// the map could not draw, or drew from a capped page, is named in words, so
// an incomplete picture never reads as a smaller cluster. The words come from
// per-source state the pure graph module never sees, so only a render can
// check them.
describe("cluster-topology", () => {
  const SOURCES = [
    "nodes-list",
    "services-list",
    "deployments-list",
    "statefulsets-list",
    "daemonsets-list",
    "pods-list",
    "pvcs-list",
  ];

  const pod = (name: string) => ({
    metadata: { name, namespace: "ns" },
    spec: { containers: [{ name: "app" }] },
    status: { phase: "Running" },
  });

  /** Every read landed and empty, then the overrides on top. */
  function seedAll(over: Record<string, Partial<SourceState>> = {}): void {
    for (const key of SOURCES) seed(key, { data: page([]), ...over[key] });
  }

  test("a refused row is named as not permitted", () => {
    seedAll({
      "pods-list": { data: page([pod("p")]) },
      "nodes-list": { data: null, errorKind: "permission" },
    });
    const html = renderWidget("cluster-topology");
    expect(html).toContain(
      "Not drawn: Nodes (not permitted for this account).",
    );
  });

  test("a row still loading is named as loading", () => {
    seedAll({
      "pods-list": { data: page([pod("p")]) },
      "nodes-list": { data: null, loading: true },
    });
    expect(renderWidget("cluster-topology")).toContain("Nodes (loading)");
  });

  // The partial workload row: Deployments and StatefulSets listed, DaemonSets
  // refused. Naming the whole row would hide that two kinds DID draw; saying
  // nothing would pass the partial count off as every workload.
  test("a partial workload row names the missing kind, not the row", () => {
    seedAll({
      "pods-list": { data: page([pod("p")]) },
      "daemonsets-list": { data: null, errorKind: "permission" },
    });
    const html = renderWidget("cluster-topology");
    expect(html).toContain(
      "Not drawn: DaemonSets (not permitted for this account).",
    );
    expect(html).not.toContain("Workloads (");
  });

  test("a capped page says how much of it was drawn", () => {
    seedAll({ "pods-list": { data: page([pod("a"), pod("b")], 900) } });
    expect(renderWidget("cluster-topology")).toContain(
      "Showing 2 of 900 pods.",
    );
  });

  test("a complete read has no coverage notes", () => {
    seedAll({ "pods-list": { data: page([pod("p")]) } });
    const html = renderWidget("cluster-topology");
    expect(html).toContain("cluster-topology-canvas");
    expect(html).not.toContain("cluster-topology-coverage");
  });

  test("an empty cluster and an unreadable pod list are told apart", () => {
    seedAll();
    let html = renderWidget("cluster-topology");
    expect(html).toContain("cluster-topology-empty");
    expect(html).not.toContain("cluster-topology-unreadable");

    seedAll({ "pods-list": { data: page(null) } });
    html = renderWidget("cluster-topology");
    expect(html).toContain("cluster-topology-unreadable");
    expect(html).not.toContain("cluster-topology-empty");
  });
});

describe("mesh-golden-signals on a remote cluster (R9)", () => {
  const params = { [PARAM_KEY_NAMESPACE]: "prod", [PARAM_KEY_SERVICE]: "api" };
  const key = sourceKeyFor("mesh-golden-signals", params);

  function signalsReason(reason: string): void {
    seed(key, { data: { signals: { available: false, reason } } });
  }

  test("metrics_not_configured says to add a Prometheus binding", () => {
    signalsReason("metrics_not_configured");
    const html = renderWidget("mesh-golden-signals", params);
    expect(html).toContain('data-testid="golden-signals-unmeasured"');
    expect(html).toContain(METRICS_NOT_CONFIGURED_MESSAGE);
    expect(html).not.toContain("metrics_not_configured");
  });

  test("unreachable says the cluster's Prometheus could not be read", () => {
    signalsReason("unreachable");
    const html = renderWidget("mesh-golden-signals", params);
    expect(html).toContain("Prometheus could not be read");
    expect(html).not.toContain(METRICS_NOT_CONFIGURED_MESSAGE);
    expect(html).not.toMatch(/>unreachable</);
  });
});

describe("active-alerts on a remote cluster (R15, AE1, AE5)", () => {
  const originalCluster = selectedCluster.peek();

  function summary(
    alerts: CoveredSummary["alerts"],
    coverage: CoveredSummary["coverage"],
  ): CoveredSummary {
    return {
      nodes: { total: 3, ready: 3 },
      pods: { total: 42, running: 42, pending: 0, failed: 0 },
      alerts,
      cpu: {
        percentage: 0,
        used: "N/A",
        total: "12",
        requests: "2",
        limits: "4",
      },
      memory: {
        percentage: 0,
        used: "N/A",
        total: "48Gi",
        requests: "8Gi",
        limits: "16Gi",
      },
      health: undefined,
      coverage,
    };
  }

  function seedSummary(data: CoveredSummary): void {
    seed("dashboard-summary", { data });
  }

  function host(): string {
    const def = getWidget("active-alerts");
    if (def === undefined) throw new Error("active-alerts is not registered");
    return render(<WidgetHost def={def} />);
  }

  function alertsRow(reasonCode: string, detail: string) {
    return [{ section: "alerts", status: "unavailable", reasonCode, detail }];
  }

  afterEach(() => {
    selectedCluster.value = originalCluster;
    seed("dashboard-summary", { data: null });
  });

  test("a remote selection has no link to the Alerts page", () => {
    selectedCluster.value = "abc123";
    seedSummary(
      summary({ active: 3, critical: 1 }, [
        { section: "alerts", status: "ok", reasonCode: "ok" },
      ]),
    );
    const html = host();
    expect(html).toContain("1 critical");
    expect(html).not.toContain("/alerting");
  });

  test("the local cluster keeps the link", () => {
    selectedCluster.value = "local";
    seedSummary(summary({ active: 3, critical: 1 }, undefined));
    expect(host()).toContain('href="/alerting"');
  });

  test("alerts_not_configured shows the Alertmanager advice and no count", () => {
    selectedCluster.value = "abc123";
    seedSummary(
      summary(
        { active: 0, critical: 0 },
        alertsRow(
          "alerts_not_configured",
          "no Alertmanager is configured for this cluster",
        ),
      ),
    );
    const html = host();
    expect(html).toContain("Add an Alertmanager URL to its metrics binding.");
    expect(html).not.toContain("All clear");
    expect(html).not.toContain("firing");
    expect(html).not.toContain(">0<");
  });

  test("the token re-entry detail shows the re-enter advice and no count", () => {
    selectedCluster.value = "abc123";
    seedSummary(
      summary(
        { active: 0, critical: 0 },
        alertsRow(
          "unreachable",
          "the Alertmanager token must be entered again for the Alertmanager's current address",
        ),
      ),
    );
    const html = host();
    expect(html).toContain("token must be entered again");
    expect(html).not.toContain("All clear");
    expect(html).not.toContain(">0<");
  });

  test("an unreachable row shows its fixed detail and no count", () => {
    selectedCluster.value = "abc123";
    seedSummary(
      summary(
        { active: 0, critical: 0 },
        alertsRow(
          "unreachable",
          "the cluster's Alertmanager could not be reached",
        ),
      ),
    );
    const html = host();
    expect(html).toContain("Alertmanager could not be reached");
    expect(html).not.toContain("All clear");
    expect(html).not.toContain(">0<");
  });
});
