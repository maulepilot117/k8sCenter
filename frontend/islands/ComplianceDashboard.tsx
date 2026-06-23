import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import {
  EngineBadge,
  SEVERITY_COLORS,
  SEVERITY_ORDER,
  SeverityBadge,
} from "@/components/ui/PolicyBadges.tsx";
import type {
  ComplianceScore,
  NormalizedViolation,
  SeverityCounts,
} from "@/lib/policy-types.ts";
import { scoreColor } from "@/lib/score-color.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import {
  failingPolicies,
  scopeViolations,
  worstResources,
} from "@/lib/compliance-violations.ts";
import ComplianceTrendChart from "@/islands/ComplianceTrendChart.tsx";

const TOP_POLICIES = 5;
const TOP_RESOURCES = 8;

function SeverityBar({
  label,
  counts,
}: {
  label: string;
  counts: SeverityCounts;
}) {
  const pct = counts.total > 0 ? (counts.pass / counts.total) * 100 : 100;
  const color = SEVERITY_COLORS[label] ?? "var(--text-muted)";
  return (
    <div class="flex items-center gap-3">
      <span class="text-xs font-medium w-16 text-right" style={{ color }}>
        {label}
      </span>
      <div class="flex-1 h-3 rounded-full overflow-hidden bg-bg-elevated">
        <div
          class="h-full rounded-full"
          style={{
            width: "100%",
            backgroundColor: color,
            opacity: 0.8,
            transform: `scaleX(${pct / 100})`,
            transformOrigin: "left center",
            transition: "transform 0.8s cubic-bezier(0.16, 1, 0.3, 1)",
          }}
        />
      </div>
      <span class="text-xs text-text-muted w-20">
        {counts.pass}/{counts.total} pass
      </span>
    </div>
  );
}

export default function ComplianceDashboard() {
  const scores = useSignal<ComplianceScore[]>([]);
  const violations = useSignal<NormalizedViolation[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);

  // Read the global namespace picker in the synchronous render path so the
  // island re-renders (and the effect below re-runs) when the picker changes.
  // "all" = cluster-wide; a specific namespace scopes the score server-side.
  const ns = selectedNamespace.value;

  async function fetchData() {
    try {
      // Peek the live value (not the render-time closure) so WS-triggered
      // refetches also pick up the currently selected namespace.
      const current = selectedNamespace.peek();
      const qs = current && current !== "all"
        ? `?namespace=${encodeURIComponent(current)}`
        : "";
      const res = await apiGet<ComplianceScore | ComplianceScore[]>(
        `/v1/policies/compliance${qs}`,
      );
      // Backend returns a single scoped object (cluster-wide or namespace).
      scores.value = Array.isArray(res.data)
        ? res.data
        : res.data
        ? [res.data]
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load compliance data";
    }
  }

  // Violations power the "what's in violation" preview. Fetched once (the
  // full RBAC-filtered list) and filtered client-side by the picker, so a
  // namespace change needs no refetch. Failures here are non-fatal — the
  // score is the primary content; the preview just stays empty.
  async function fetchViolations() {
    try {
      const res = await apiGet<NormalizedViolation[]>(
        "/v1/policies/violations",
      );
      violations.value = Array.isArray(res.data) ? res.data : [];
    } catch {
      // keep the page usable even if the violations list is unavailable
    }
  }

  // Refetch the score on mount and whenever the namespace picker changes.
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, [ns]);

  // Load the violations list once on mount (filtered client-side by scope).
  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchViolations();
  }, []);

  useWsRefetch(
    () => Promise.all([fetchData(), fetchViolations()]).then(() => {}),
    [
      ["compliance-policyreports", "policyreports", ""],
      ["compliance-clusterpolicyreports", "clusterpolicyreports", ""],
    ],
    5000,
  );

  async function handleRefresh() {
    refreshing.value = true;
    await Promise.all([fetchData(), fetchViolations()]);
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  // Backend echoes the requested scope ("" for cluster-wide, else the
  // namespace). Match it back, falling back to the first row for robustness.
  const activeScope = ns === "all" ? "" : ns;
  const primaryScore =
    scores.value.find((s) =>
      s.scope === activeScope || (activeScope === "" && s.scope === "cluster")
    ) ?? scores.value[0];
  const scoped = ns !== "all";

  // Derive the "what's in violation" preview from the full list, scoped the
  // same way the score is (namespace-strict), so the two always agree.
  const scopedViolations = scopeViolations(violations.value, ns);
  const topPolicies = failingPolicies(scopedViolations, TOP_POLICIES);
  const topResources = worstResources(scopedViolations, TOP_RESOURCES);
  const violationsHref = scoped
    ? `/security/violations?namespace=${encodeURIComponent(ns)}`
    : "/security/violations";

  return (
    <div
      style={{
        padding: "24px",
        display: "flex",
        flexDirection: "column",
        gap: "20px",
      }}
    >
      {/* Page header */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: "16px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            Compliance
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            {scoped
              ? `Weighted compliance for namespace "${ns}".`
              : "Weighted cluster-wide compliance based on policy pass/fail rates."}
          </p>
        </div>
        {!loading.value && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing.value}
          >
            {refreshing.value ? "Refreshing..." : "Refresh"}
          </Button>
        )}
      </div>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && (
        <p class="text-sm py-4" style={{ color: "var(--error)" }}>
          {error.value}
        </p>
      )}

      {!loading.value && !error.value && !primaryScore && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "32px 0" }}>
            <p style={{ color: "var(--text-muted)" }}>
              No compliance data available. Install a policy engine and define
              policies to see scores.
            </p>
          </div>
        </WidgetShell>
      )}

      {!loading.value && !error.value && primaryScore && (
        <>
          {/* Cluster overview */}
          <div style={{ display: "flex", flexWrap: "wrap", gap: "20px" }}>
            {/* Score ring */}
            <div style={{ flex: "1 1 280px", minWidth: "260px" }}>
              <WidgetShell
                title={scoped ? `Namespace: ${ns}` : "Cluster Score"}
              >
                <div class="flex flex-col items-center justify-center">
                  <GaugeRing
                    value={primaryScore.score}
                    size={140}
                    strokeWidth={10}
                    color={scoreColor(primaryScore.score)}
                    label={scoped ? "Namespace" : "Cluster"}
                    valueSize="28px"
                  />
                  <p class="mt-3 text-sm text-text-secondary">
                    {primaryScore.pass}/{primaryScore.total} passing
                    {primaryScore.warn > 0 && (
                      <span class="text-warning ml-2">
                        ({primaryScore.warn} warnings)
                      </span>
                    )}
                  </p>
                </div>
              </WidgetShell>
            </div>

            {/* Severity breakdown */}
            <div style={{ flex: "2 1 340px", minWidth: "300px" }}>
              <WidgetShell title="Severity Breakdown">
                <div class="space-y-3">
                  {SEVERITY_ORDER.map((sev) => {
                    const counts = primaryScore.bySeverity?.[sev] ?? {
                      pass: 0,
                      fail: 0,
                      total: 0,
                    };
                    if (counts.total === 0) return null;
                    return (
                      <SeverityBar key={sev} label={sev} counts={counts} />
                    );
                  })}
                  {!primaryScore.bySeverity && (
                    <p class="text-xs text-text-muted">
                      No severity breakdown available.
                    </p>
                  )}
                </div>
              </WidgetShell>
            </div>
          </div>

          {/* What's in violation */}
          {scopedViolations.length === 0
            ? (
              <WidgetShell title="Violations">
                <p class="text-sm text-text-muted py-2">
                  No active violations in{" "}
                  {scoped ? `namespace "${ns}"` : "the cluster"}.
                </p>
              </WidgetShell>
            )
            : (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "12px",
                }}
              >
                <div
                  style={{ display: "flex", flexWrap: "wrap", gap: "20px" }}
                >
                  {/* Failing policies — grouped by impact */}
                  <div style={{ flex: "1 1 320px", minWidth: "300px" }}>
                    <WidgetShell title="Failing Policies">
                      <div class="space-y-2">
                        {topPolicies.map((g) => (
                          <div key={g.policy} class="flex items-center gap-3">
                            <span
                              style={{
                                width: "6px",
                                height: "6px",
                                borderRadius: "50%",
                                flexShrink: 0,
                                background: SEVERITY_COLORS[g.severity] ??
                                  "var(--text-muted)",
                              }}
                            />
                            <span
                              class="text-sm font-medium flex-1 truncate"
                              style={{ color: "var(--text-primary)" }}
                              title={g.policy}
                            >
                              {g.policy}
                            </span>
                            <SeverityBadge severity={g.severity} />
                            <EngineBadge engine={g.engine} />
                            <span class="text-xs text-text-muted w-24 text-right tabular-nums">
                              {g.count}{" "}
                              {g.count === 1 ? "resource" : "resources"}
                            </span>
                          </div>
                        ))}
                      </div>
                    </WidgetShell>
                  </div>

                  {/* Worst individual violating resources */}
                  <div style={{ flex: "2 1 360px", minWidth: "320px" }}>
                    <WidgetShell title="Worst Resources">
                      <div class="space-y-2">
                        {topResources.map((vio, i) => {
                          const href = resourceHref(
                            vio.kind,
                            vio.namespace,
                            vio.name,
                          );
                          const label = (
                            <span
                              class="font-mono text-xs font-medium"
                              style={{
                                color: href
                                  ? "var(--accent)"
                                  : "var(--text-primary)",
                              }}
                            >
                              {vio.kind}/{vio.name}
                            </span>
                          );
                          return (
                            <div
                              key={`${vio.kind}-${vio.namespace}-${vio.name}-${vio.policy}-${i}`}
                              class="flex items-start gap-3"
                            >
                              <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-2 flex-wrap">
                                  {href
                                    ? (
                                      <a href={href} class="hover:underline">
                                        {label}
                                      </a>
                                    )
                                    : label}
                                  {vio.namespace && (
                                    <span class="text-xs text-text-muted">
                                      {vio.namespace}
                                    </span>
                                  )}
                                  <SeverityBadge severity={vio.severity} />
                                </div>
                                <p
                                  class="text-xs text-text-muted truncate"
                                  title={vio.message}
                                >
                                  {vio.policy}
                                  {vio.message ? ` — ${vio.message}` : ""}
                                </p>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    </WidgetShell>
                  </div>
                </div>

                <a
                  href={violationsHref}
                  class="text-sm hover:underline self-start"
                  style={{ color: "var(--accent)" }}
                >
                  View all violations →
                </a>
              </div>
            )}

          {/* Compliance trend chart */}
          <ComplianceTrendChart />
        </>
      )}
    </div>
  );
}
