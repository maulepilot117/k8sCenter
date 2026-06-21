import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { useWsRefetch } from "@/lib/useWsRefetch.ts";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import {
  SEVERITY_COLORS,
  SEVERITY_ORDER,
} from "@/components/ui/PolicyBadges.tsx";
import type { ComplianceScore, SeverityCounts } from "@/lib/policy-types.ts";
import { scoreColor } from "@/lib/score-color.ts";
import ComplianceTrendChart from "@/islands/ComplianceTrendChart.tsx";

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
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const res = await apiGet<ComplianceScore | ComplianceScore[]>(
        "/v1/policies/compliance",
      );
      // Backend returns a single object (cluster-wide) or an array
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

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  useWsRefetch(fetchData, [
    ["compliance-policyreports", "policyreports", ""],
    ["compliance-clusterpolicyreports", "clusterpolicyreports", ""],
  ], 5000);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  const clusterScore = scores.value.find((s) =>
    s.scope === "" || s.scope === "cluster"
  );
  const nsScores = scores.value
    .filter((s) => s.scope !== "" && s.scope !== "cluster")
    .sort((a, b) => a.score - b.score);

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
            Weighted compliance scores based on policy pass/fail rates.
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

      {!loading.value && !error.value && !clusterScore && (
        <WidgetShell>
          <div style={{ textAlign: "center", padding: "32px 0" }}>
            <p style={{ color: "var(--text-muted)" }}>
              No compliance data available. Install a policy engine and define
              policies to see scores.
            </p>
          </div>
        </WidgetShell>
      )}

      {!loading.value && !error.value && clusterScore && (
        <>
          {/* Cluster overview */}
          <div style={{ display: "flex", flexWrap: "wrap", gap: "20px" }}>
            {/* Score ring */}
            <div style={{ flex: "1 1 280px", minWidth: "260px" }}>
              <WidgetShell title="Cluster Score">
                <div class="flex flex-col items-center justify-center">
                  <GaugeRing
                    value={clusterScore.score}
                    size={140}
                    strokeWidth={10}
                    color={scoreColor(clusterScore.score)}
                    label="Cluster Score"
                    valueSize="28px"
                  />
                  <p class="mt-3 text-sm text-text-secondary">
                    {clusterScore.pass}/{clusterScore.total} passing
                    {clusterScore.warn > 0 && (
                      <span class="text-warning ml-2">
                        ({clusterScore.warn} warnings)
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
                    const counts = clusterScore.bySeverity?.[sev] ?? {
                      pass: 0,
                      fail: 0,
                      total: 0,
                    };
                    if (counts.total === 0) return null;
                    return (
                      <SeverityBar key={sev} label={sev} counts={counts} />
                    );
                  })}
                  {!clusterScore.bySeverity && (
                    <p class="text-xs text-text-muted">
                      No severity breakdown available.
                    </p>
                  )}
                </div>
              </WidgetShell>
            </div>
          </div>

          {/* Compliance trend chart */}
          <ComplianceTrendChart />

          {/* Per-namespace table */}
          {nsScores.length > 0 && (
            <>
              <h2
                style={{
                  margin: 0,
                  fontSize: "17px",
                  fontWeight: 650,
                  color: "var(--text-primary)",
                }}
              >
                Per-Namespace Compliance
              </h2>
              <div class="overflow-x-auto rounded-lg border border-border-primary">
                <table class="w-full text-sm">
                  <thead>
                    <tr class="border-b border-border-primary bg-surface">
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Namespace
                      </th>
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Score
                      </th>
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Pass
                      </th>
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Fail
                      </th>
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Warn
                      </th>
                      <th class="px-3 py-2 text-left text-xs font-medium text-text-muted">
                        Total
                      </th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-border-subtle">
                    {nsScores.map((ns) => (
                      <tr key={ns.scope} class="hover:bg-hover/30">
                        <td class="px-3 py-2">
                          <a
                            href={`/security/violations?namespace=${
                              encodeURIComponent(ns.scope)
                            }`}
                            style={{ color: "var(--accent)" }}
                            class="hover:underline font-medium"
                          >
                            {ns.scope}
                          </a>
                        </td>
                        <td class="px-3 py-2">
                          <span
                            class="font-mono font-medium"
                            style={{ color: scoreColor(ns.score) }}
                          >
                            {Math.round(ns.score)}%
                          </span>
                        </td>
                        <td class="px-3 py-2 text-text-secondary">
                          {ns.pass}
                        </td>
                        <td class="px-3 py-2">
                          {ns.fail > 0
                            ? (
                              <span
                                style={{ color: "var(--error)" }}
                                class="font-medium"
                              >
                                {ns.fail}
                              </span>
                            )
                            : <span class="text-text-muted">0</span>}
                        </td>
                        <td class="px-3 py-2">
                          {ns.warn > 0
                            ? <span class="text-warning">{ns.warn}</span>
                            : <span class="text-text-muted">0</span>}
                        </td>
                        <td class="px-3 py-2 text-text-secondary">
                          {ns.total}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}
