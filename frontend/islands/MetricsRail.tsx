/**
 * MetricsRail — 300px glass right-rail showing up to 4 live sparkline KPI
 * tiles for the current resource.  Rendered by ResourceDetail when metrics
 * are available for the kind (deployments, statefulsets, daemonsets, pods).
 *
 * Behaviour:
 * - SSR-safe: renders nothing on the server (IS_BROWSER guard).
 * - Backend 502 / monitoring unavailable → renders a graceful empty state
 *   ("Monitoring unavailable") so the rail never breaks the layout.
 * - Auto-refreshes every 30 s; clears the interval on unmount.
 * - Uses the same RBAC-gated slug API as PerformancePanel — no raw PromQL.
 */
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import Sparkline from "@/components/charts/Sparkline.tsx";
import GlassCard from "@/components/ui/GlassCard.tsx";

// ── types ─────────────────────────────────────────────────────────────────────

interface MetricsRailProps {
  kind: string;
  name: string;
  namespace?: string;
}

interface QueryRangeResult {
  result: { values: [number, string][] }[];
}

interface KpiDef {
  label: string;
  slug: string;
  format: (v: number) => string;
}

interface KpiState {
  label: string;
  value: string;
  series: number[];
  loading: boolean;
  error: boolean;
}

// ── KPI definitions per kind ─────────────────────────────────────────────────

const fmt = {
  cores: (v: number) => v < 1 ? `${Math.round(v * 1000)}m` : `${v.toFixed(2)}`,
  mb: (v: number) =>
    v >= 1024 ? `${(v / 1024).toFixed(1)}Gi` : `${Math.round(v)}Mi`,
  reqps: (v: number) => v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(1),
  count: (v: number) => String(Math.round(v)),
};

const RAIL_KPIS: Record<string, KpiDef[]> = {
  deployments: [
    { label: "CPU", slug: "deployments/cpu", format: fmt.cores },
    { label: "Memory", slug: "deployments/memory", format: fmt.mb },
    { label: "Net Rx", slug: "deployments/network-rx", format: fmt.reqps },
    { label: "Restarts", slug: "pods/restarts", format: fmt.count },
  ],
  statefulsets: [
    { label: "CPU", slug: "statefulsets/cpu", format: fmt.cores },
    { label: "Memory", slug: "statefulsets/memory", format: fmt.mb },
    { label: "Net Rx", slug: "statefulsets/network-rx", format: fmt.reqps },
    {
      label: "Ready",
      slug: "statefulsets/replicas-ready",
      format: fmt.count,
    },
  ],
  daemonsets: [
    { label: "CPU", slug: "daemonsets/cpu", format: fmt.cores },
    { label: "Memory", slug: "daemonsets/memory", format: fmt.mb },
    { label: "Net Rx", slug: "daemonsets/network-rx", format: fmt.reqps },
    { label: "Ready", slug: "daemonsets/ready", format: fmt.count },
  ],
  pods: [
    { label: "CPU", slug: "pods/cpu", format: fmt.cores },
    { label: "Memory", slug: "pods/memory", format: fmt.mb },
    { label: "Net Rx", slug: "pods/network-rx", format: fmt.reqps },
    { label: "Restarts", slug: "pods/restarts", format: fmt.count },
  ],
};

// ── component ─────────────────────────────────────────────────────────────────

export default function MetricsRail(
  { kind, name, namespace }: MetricsRailProps,
) {
  // ── all hooks must run unconditionally before any early return ───────────
  const available = useSignal<boolean | null>(null);
  // Start with skeleton tiles for known kinds, empty for unknown
  const kpis = useSignal<KpiState[]>(
    (RAIL_KPIS[kind] ?? []).map((k) => ({
      label: k.label,
      value: "–",
      series: [],
      loading: true,
      error: false,
    })),
  );

  useEffect(() => {
    const kpiDefs = RAIL_KPIS[kind];
    if (!kpiDefs) return;

    async function load() {
      const now = new Date();
      const end = now.toISOString();
      const start = new Date(now.getTime() - 30 * 60 * 1000).toISOString();
      const step = "60s";

      const results = await Promise.allSettled(
        kpiDefs.map(async (def) => {
          const params = new URLSearchParams({ start, end, step, name });
          if (namespace) params.set("namespace", namespace);
          const res = await apiGet<QueryRangeResult>(
            `/v1/monitoring/queries/${def.slug}?${params}`,
          );
          return res.data;
        }),
      );

      kpis.value = kpiDefs.map((def, i) => {
        const r = results[i];
        if (r.status === "rejected") {
          return {
            label: def.label,
            value: "–",
            series: [],
            loading: false,
            error: true,
          };
        }
        const series = (r.value.result?.[0]?.values ?? []).map(
          ([, v]: [number, string]) => parseFloat(v),
        );
        const last = series.length > 0 ? series[series.length - 1] : NaN;
        return {
          label: def.label,
          value: Number.isFinite(last) ? def.format(last) : "–",
          series,
          loading: false,
          error: false,
        };
      });
    }

    apiGet<{ prometheus: { available: boolean } }>("/v1/monitoring/status")
      .then((res) => {
        available.value = res.data.prometheus.available;
        if (res.data.prometheus.available) load();
      })
      .catch(() => {
        available.value = false;
      });

    const id = setInterval(() => {
      if (available.value) load();
    }, 30_000);
    return () => clearInterval(id);
  }, [kind, name, namespace]);

  // ── early returns after all hooks ────────────────────────────────────────
  if (!IS_BROWSER) return null;

  const kpiDefs = RAIL_KPIS[kind];
  if (!kpiDefs) return null;

  // If monitoring unavailable (502 etc.), render a minimalist empty rail
  if (available.value === false) {
    return (
      <GlassCard padding={16} style={{ position: "sticky", top: "20px" }}>
        <div
          style={{
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.07em",
            color: "var(--text-muted)",
            marginBottom: "12px",
          }}
        >
          Live Metrics
        </div>
        <p
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            textAlign: "center",
            paddingTop: "12px",
          }}
        >
          Monitoring unavailable
        </p>
      </GlassCard>
    );
  }

  // Still checking or loading: render the rail with skeleton tiles
  return (
    <GlassCard padding={16} style={{ position: "sticky", top: "20px" }}>
      {/* Rail header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "6px",
          marginBottom: "16px",
        }}
      >
        <span
          style={{
            width: "6px",
            height: "6px",
            borderRadius: "50%",
            background: "var(--success)",
            flexShrink: 0,
          }}
        />
        <span
          style={{
            fontSize: "11px",
            fontWeight: 600,
            textTransform: "uppercase",
            letterSpacing: "0.07em",
            color: "var(--text-muted)",
          }}
        >
          Live
        </span>
      </div>

      {/* KPI tiles */}
      {kpis.value.map((kpi) => <KpiTile key={kpi.label} kpi={kpi} />)}
    </GlassCard>
  );
}

// ── KPI tile ──────────────────────────────────────────────────────────────────

function KpiTile({ kpi }: { kpi: KpiState }) {
  return (
    <div
      style={{
        marginBottom: "20px",
      }}
    >
      <div
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.07em",
          color: "var(--text-muted)",
          marginBottom: "4px",
        }}
      >
        {kpi.label}
      </div>
      <div
        style={{
          fontSize: "17px",
          fontWeight: 650,
          color: kpi.error ? "var(--text-muted)" : "var(--text-primary)",
          fontVariantNumeric: "tabular-nums",
          lineHeight: 1.2,
          marginBottom: "6px",
        }}
      >
        {kpi.loading
          ? (
            <span
              style={{
                display: "inline-block",
                width: "48px",
                height: "17px",
                borderRadius: "4px",
                background: "var(--bg-elevated)",
              }}
            />
          )
          : kpi.value}
      </div>
      {kpi.series.length > 1 && (
        <Sparkline
          data={kpi.series}
          height={32}
          stroke="var(--accent)"
          fill="color-mix(in srgb, var(--accent) 14%, transparent)"
        />
      )}
      {kpi.series.length <= 1 && !kpi.loading && (
        <div
          style={{
            height: "32px",
            borderRadius: "4px",
            background: "var(--bg-elevated)",
            opacity: 0.4,
          }}
        />
      )}
    </div>
  );
}
