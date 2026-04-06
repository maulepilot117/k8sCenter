import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import {
  SEVERITY_COLORS,
  SEVERITY_ORDER,
} from "@/components/ui/PolicyBadges.tsx";
import type { ComplianceScore, SeverityCounts } from "@/lib/policy-types.ts";

function scoreColor(score: number): string {
  if (score >= 80) return "var(--success)";
  if (score >= 50) return "var(--warning)";
  return "var(--danger)";
}

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
            width: `${pct}%`,
            backgroundColor: color,
            opacity: 0.8,
            transition: "width 0.8s cubic-bezier(0.16, 1, 0.3, 1)",
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

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchData() {
      try {
        const res = await apiGet<ComplianceScore[]>("/v1/policy/compliance");
        scores.value = Array.isArray(res.data) ? res.data : [];
      } catch {
        error.value = "Failed to load compliance data";
      }
      loading.value = false;
    }

    fetchData();
  }, []);

  if (!IS_BROWSER) return null;

  const clusterScore = scores.value.find((s) => s.scope === "cluster");
  const nsScores = scores.value
    .filter((s) => s.scope !== "cluster")
    .sort((a, b) => a.score - b.score);

  return (
    <div class="p-6">
      <h1 class="text-2xl font-bold text-text-primary mb-1">Compliance</h1>
      <p class="text-sm text-text-muted mb-6">
        Weighted compliance scores based on policy pass/fail rates.
      </p>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && !clusterScore && (
        <div class="text-center py-12 rounded-lg border border-border-primary bg-bg-elevated">
          <p class="text-text-muted">
            No compliance data available. Install a policy engine and define
            policies to see scores.
          </p>
        </div>
      )}

      {!loading.value && !error.value && clusterScore && (
        <>
          {/* Cluster overview */}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mb-8">
            {/* Score ring */}
            <div class="rounded-lg border border-border-primary p-6 flex flex-col items-center justify-center bg-bg-elevated">
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

            {/* Severity breakdown */}
            <div class="rounded-lg border border-border-primary p-6 bg-bg-elevated">
              <h2 class="text-sm font-medium text-text-primary mb-4">
                Severity Breakdown
              </h2>
              <div class="space-y-3">
                {SEVERITY_ORDER.map((sev) => {
                  const counts = clusterScore.bySeverity?.[sev] ?? {
                    pass: 0,
                    fail: 0,
                    total: 0,
                  };
                  if (counts.total === 0) return null;
                  return <SeverityBar key={sev} label={sev} counts={counts} />;
                })}
                {!clusterScore.bySeverity && (
                  <p class="text-xs text-text-muted">
                    No severity breakdown available.
                  </p>
                )}
              </div>
            </div>
          </div>

          {/* Per-namespace table */}
          {nsScores.length > 0 && (
            <>
              <h2 class="text-lg font-semibold text-text-primary mb-3">
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
                            class="text-brand hover:underline font-medium"
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
                              <span class="text-danger font-medium">
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
