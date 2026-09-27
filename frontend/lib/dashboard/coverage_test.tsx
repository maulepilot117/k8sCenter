/** @jsxImportSource preact */
import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { render } from "preact-render-to-string";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
// Registers every widget, so the drift guard below sees the real catalog.
import "@/components/dashboard/widgets/index.ts";
import { ApiError } from "@/lib/api.ts";
import { selectedCluster } from "@/src/lib/cluster.ts";
import type { CoveredSummary, SectionCoverage } from "./coverage.ts";
import {
  countsUnavailableReason,
  coverageFor,
  coverageMessage,
  formatHealthScore,
  isRenderable,
  observedAgo,
  sectionTone,
  shouldRenderHealth,
  WIDGET_SECTIONS,
  withheldReason,
} from "./coverage.ts";
import type { SourceState } from "./data.ts";
import { DASHBOARD_FETCHERS, dashboardData } from "./data.ts";
import { allWidgets, getWidget } from "./registry.ts";
import { sourcesOf } from "./widget-state.ts";

/**
 * The summary a remote cluster answers with: real node and pod counts, and
 * the backend's placeholder zeroes for everything it could not observe.
 * Those zeroes are the whole reason this module exists -- every assertion
 * that says "no 0" below is asserting that one of them stayed off screen.
 */
function remoteSummary(
  coverage: SectionCoverage[],
  over: Partial<CoveredSummary> = {},
): CoveredSummary {
  return {
    nodes: { total: 3, ready: 3 },
    pods: { total: 42, running: 40, pending: 1, failed: 1 },
    alerts: { active: 0, critical: 0 },
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
    ...over,
  };
}

function row(
  section: string,
  status: string,
  over: Partial<SectionCoverage> = {},
): SectionCoverage {
  return {
    section,
    status,
    reasonCode: status === "ok" ? "ok" : "unsupported_platform",
    ...over,
  };
}

/** The coverage block `handleRemoteDashboardSummary` writes in v1. */
const V1_REMOTE: SectionCoverage[] = [
  row("nodes", "ok", { observedAt: "2026-09-27T12:00:00Z" }),
  row("pods", "ok", { observedAt: "2026-09-27T12:00:00Z" }),
  row("services", "ok", { observedAt: "2026-09-27T12:00:00Z" }),
  row("cpu", "unavailable", {
    detail:
      "remote CPU/memory usage requires the remote metrics binding (deferred)",
  }),
  row("memory", "unavailable", {
    detail:
      "remote CPU/memory usage requires the remote metrics binding (deferred)",
  }),
  row("alerts", "unavailable", {
    detail: "alert counts are bound to the local Alertmanager",
  }),
  row("health", "unavailable", {
    detail:
      "remote health scoring requires the remote metrics binding (deferred)",
  }),
];

describe("health (AE3)", () => {
  test("shouldRenderHealth is false for null health", () => {
    expect(shouldRenderHealth(remoteSummary(V1_REMOTE))).toBe(false);
    expect(shouldRenderHealth(null)).toBe(false);
  });

  test("shouldRenderHealth is false when the health coverage row is not ok", () => {
    // Even with a score somehow present: a composite over incomplete inputs
    // is the synthesised reading AE3 forbids.
    for (const status of ["partial", "stale", "unavailable", "forbidden"]) {
      const s = remoteSummary([row("health", status)], {
        health: { score: 88, status: "healthy" } as CoveredSummary["health"],
      });
      expect(shouldRenderHealth(s)).toBe(false);
    }
  });

  test("shouldRenderHealth is true for a local score with no coverage block", () => {
    const s = remoteSummary([], {
      coverage: undefined,
      health: { score: 88, status: "healthy" } as CoveredSummary["health"],
    });
    expect(shouldRenderHealth(s)).toBe(true);
    expect(formatHealthScore(s)).toBe("88");
  });

  test("null health never renders as zero", () => {
    // The local defect: `computeClusterHealth` answers `score: null` when no
    // weighted signal resolved, and the widget used to print `?? 0`.
    const local = remoteSummary([], {
      coverage: undefined,
      health: { score: null, status: "unknown" } as CoveredSummary["health"],
    });
    expect(formatHealthScore(local)).toBe("—");
    expect(formatHealthScore(remoteSummary(V1_REMOTE))).toBe("—");
  });
});

describe("tones (R3)", () => {
  test("all five statuses map to five distinct tones", () => {
    const tones = ["ok", "partial", "unavailable", "forbidden", "stale"].map(
      (s) => sectionTone(row("pods", s)),
    );
    expect(new Set(tones).size).toBe(5);
  });

  test("partial renders differently from unavailable", () => {
    expect(sectionTone(row("pods", "partial"))).toBe("partial");
    expect(sectionTone(row("pods", "unavailable"))).toBe("unavailable");
    expect(isRenderable("partial")).toBe(true);
    expect(isRenderable("unavailable")).toBe(false);
  });

  test("forbidden renders differently from unavailable", () => {
    expect(sectionTone(row("pods", "forbidden"))).toBe("forbidden");
    expect(isRenderable("forbidden")).toBe(false);
    expect(coverageMessage(row("pods", "forbidden"))).not.toBe(
      coverageMessage(row("pods", "unavailable")),
    );
  });

  test("stale carries and formats observedAt", () => {
    const now = Date.parse("2026-09-27T12:05:00Z");
    const cov = row("nodes", "stale", { observedAt: "2026-09-27T12:00:00Z" });
    expect(observedAgo(cov, now)).toBe("5m ago");
    expect(coverageMessage(cov, now)).toContain("5m ago");
    expect(isRenderable(sectionTone(cov))).toBe(true);
  });

  test("a stale row with no usable timestamp does not claim to be new", () => {
    expect(observedAgo(row("nodes", "stale"))).toBeNull();
    expect(
      observedAgo(row("nodes", "stale", { observedAt: "not a time" })),
    ).toBeNull();
    expect(coverageMessage(row("nodes", "stale"))).not.toContain("0s");
  });

  test("unknown status falls back safely", () => {
    const cov = row("pods", "exploded");
    expect(() => sectionTone(cov)).not.toThrow();
    expect(sectionTone(cov)).toBe("unavailable");
    expect(coverageMessage({ ...cov, detail: undefined })).toBe(
      "Not available on this cluster.",
    );
  });

  test("a missing or malformed coverage block renders normally", () => {
    expect(coverageFor(null, "pods")).toBeNull();
    expect(
      coverageFor({ coverage: "nope" } as unknown as CoveredSummary, "pods"),
    ).toBeNull();
    expect(sectionTone(null)).toBe("ok");
  });

  test("the backend detail wins over generic copy", () => {
    expect(
      coverageMessage(
        row("pods", "forbidden", {
          detail:
            "you do not have permission to list pods across all namespaces on this cluster",
        }),
      ),
    ).toBe(
      "You do not have permission to list pods across all namespaces on this cluster.",
    );
  });

  test("withheldReason gives the reason for an unobservable section only", () => {
    const s = remoteSummary(V1_REMOTE);
    expect(withheldReason(s, "cpu")).toContain("remote metrics binding");
    expect(withheldReason(s, "nodes")).toBeNull();
    expect(
      withheldReason(remoteSummary([], { coverage: undefined }), "cpu"),
    ).toBeNull();
  });
});

describe("resource counts", () => {
  test("counts 400 on remote maps to unavailable", () => {
    const err = new ApiError(
      400,
      400,
      "resource counts are only available for the local cluster",
    );
    expect(countsUnavailableReason(err, "abc123")).toBe(
      "Resource counts are only available for the local cluster.",
    );
  });

  test("a 400 on the local cluster is still an error", () => {
    const err = new ApiError(400, 400, "bad namespace");
    expect(countsUnavailableReason(err, "local")).toBeNull();
  });

  test("a remote 500 is still an error", () => {
    expect(
      countsUnavailableReason(new ApiError(500, 500, "boom"), "abc123"),
    ).toBeNull();
    expect(countsUnavailableReason(new Error("offline"), "abc123")).toBeNull();
  });
});

describe("drift guard", () => {
  test("every summary-backed widget declares the section it headlines", () => {
    const summaryWidgets = allWidgets()
      .filter((d) => sourcesOf(d).includes("dashboard-summary"))
      .map((d) => d.id)
      .sort();
    expect(summaryWidgets.length).toBeGreaterThan(0);
    expect(Object.keys(WIDGET_SECTIONS).sort()).toEqual(summaryWidgets);
  });
});

describe("summary fetcher", () => {
  let calls: Array<{ url: string; cluster: string | null }> = [];
  const original = globalThis.fetch;
  const originalCluster = selectedCluster.peek();

  beforeEach(() => {
    calls = [];
    globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
      calls.push({
        url: String(input),
        cluster: new Headers(init?.headers).get("X-Cluster-ID"),
      });
      return Promise.resolve(
        new Response(JSON.stringify({ data: {} }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }) as typeof globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = original;
    selectedCluster.value = originalCluster;
  });

  test("opts in to coverage for a remote cluster, pinned to that cluster", async () => {
    selectedCluster.value = "abc123";
    await DASHBOARD_FETCHERS["dashboard-summary"](
      new AbortController().signal,
      "1h",
      {},
    );
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toContain("/v1/cluster/dashboard-summary?coverage=1");
    expect(calls[0].cluster).toBe("abc123");
  });

  test("leaves the local request exactly as it was", async () => {
    selectedCluster.value = "local";
    await DASHBOARD_FETCHERS["dashboard-summary"](
      new AbortController().signal,
      "1h",
      {},
    );
    expect(calls[0].url).toMatch(/\/v1\/cluster\/dashboard-summary$/);
    expect(calls[0].cluster).toBe("local");
  });
});

/** Seeds the summary source the way a landed fetch would. */
function seedSummary(data: CoveredSummary | null): void {
  dashboardData.signalFor("dashboard-summary").value = {
    data,
    error: null,
    errorKind: null,
    loading: false,
    range: "1h",
  } satisfies SourceState;
}

function registered(id: string) {
  const def = getWidget(id);
  if (def === undefined) throw new Error(`${id} is not registered`);
  return def;
}

/** Renders a widget through the host, the way the grid does. */
function host(id: string): string {
  return render(<WidgetHost def={registered(id)} />);
}

/** Renders a widget's body alone, bypassing the host's gate. */
function body(id: string): string {
  return render(registered(id).render({ mode: "normal", params: {} }));
}

describe("WidgetHost gating (AE3)", () => {
  afterEach(() => seedSummary(null));

  test("an unavailable section replaces the card with its reason, not a zero", () => {
    seedSummary(remoteSummary(V1_REMOTE));
    const html = host("cpu-tile");
    expect(html).toContain('data-widget-state="coverage-unavailable"');
    expect(html).toContain("remote metrics binding");
    expect(html).not.toContain(">0<");
  });

  test("alerts on a remote cluster never read as zero active alerts", () => {
    seedSummary(remoteSummary(V1_REMOTE));
    const html = host("active-alerts");
    expect(html).toContain('data-widget-state="coverage-unavailable"');
    expect(html).toContain("local Alertmanager");
  });

  test("the health card shows no score on a remote cluster", () => {
    seedSummary(remoteSummary(V1_REMOTE));
    const html = host("cluster-health");
    expect(html).toContain('data-widget-state="coverage-unavailable"');
    expect(html).not.toContain("UNKNOWN");
  });

  test("forbidden renders a different card from unavailable", () => {
    seedSummary(
      remoteSummary([
        row("pods", "forbidden", {
          reasonCode: "authz_namespace_scoped",
          detail:
            "you do not have permission to list pods across all namespaces on this cluster",
        }),
      ]),
    );
    const html = host("pods-tile");
    expect(html).toContain('data-widget-state="coverage-forbidden"');
    expect(html).toContain("across all namespaces");
  });

  test("partial renders the value under a lower-bound notice", () => {
    seedSummary(
      remoteSummary([
        row("pods", "partial", { detail: "list truncated after 5000 items" }),
      ]),
    );
    const html = host("pods-tile");
    expect(html).toContain('data-widget-state="ready"');
    expect(html).toContain('data-testid="widget-coverage-partial"');
    expect(html).toContain("42");
  });

  test("stale renders the value with its observation age", () => {
    seedSummary(
      remoteSummary([
        row("pods", "stale", { observedAt: "2020-01-01T00:00:00Z" }),
      ]),
    );
    const html = host("pods-tile");
    expect(html).toContain('data-testid="widget-coverage-stale"');
    expect(html).toContain("ago");
  });

  test("a local summary renders exactly as before", () => {
    seedSummary(remoteSummary([], { coverage: undefined }));
    const html = host("pods-tile");
    expect(html).toContain('data-widget-state="ready"');
    expect(html).not.toContain("widget-coverage");
  });
});

describe("widget bodies", () => {
  afterEach(() => seedSummary(null));

  test("a local null health score renders as unknown, not a zero gauge", () => {
    seedSummary(
      remoteSummary([], {
        coverage: undefined,
        health: { score: null, status: "unknown" } as CoveredSummary["health"],
      }),
    );
    const html = body("cluster-health");
    // No ring at all -- an empty ring reads as a score of zero -- and the
    // em-dash where the number would be.
    expect(html).toContain('data-testid="health-unscored"');
    expect(html).not.toContain("stroke-dashoffset");
    expect(html).toContain(">—<");
  });

  test("a local health score still draws its gauge", () => {
    seedSummary(
      remoteSummary([], {
        coverage: undefined,
        health: { score: 92, status: "healthy" } as CoveredSummary["health"],
      }),
    );
    const html = body("cluster-health");
    expect(html).toContain("stroke-dashoffset");
    expect(html).toContain(">92<");
    expect(html).not.toContain("health-unscored");
  });

  test("the nodes card on a local cluster shows its percentages", () => {
    seedSummary(
      remoteSummary([], {
        coverage: undefined,
        cpu: {
          percentage: 37,
          used: "4",
          total: "12",
          requests: "2",
          limits: "4",
        },
      }),
    );
    expect(body("nodes")).toContain(">37%<");
  });

  test("the nodes card withholds CPU and memory it could not observe", () => {
    seedSummary(remoteSummary(V1_REMOTE));
    const html = body("nodes");
    expect(html).not.toContain(">0%<");
    expect(html).toContain(">—<");
    expect(html).toContain("remote metrics binding");
    expect(html).toContain("3/3");
  });
});
