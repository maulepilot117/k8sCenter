/** Golden Signals card for the Service detail page (Phase D3).
 *
 *  Lazy-fetches RPS, error rate, and p50/p95/p99 latency from the mesh
 *  golden-signals endpoint. The card hides itself when:
 *    - no service mesh is installed in the cluster
 *    - the request 4xx's (e.g. service not in mesh, or both meshes
 *      installed and the auto-detect is ambiguous in v1)
 *    - the response carries available=true but every metric is zero —
 *      this matches the "unmeshed service" silent-absence contract;
 *      genuinely silent meshed services also hide, which is acceptable
 *      for v1 (the card adds no signal at zero traffic).
 *
 *  The card RENDERS (with a "Metrics unavailable" sub-message) when the
 *  backend reports available=false, so an offline Prometheus is visible
 *  rather than indistinguishable from "no data".
 *
 *  Refresh cadence: 30s, matching the monitoring-dashboard convention.
 *  Component is rendered from inside the ResourceDetail island, so its
 *  hooks hydrate without needing a separate islands/ entry.
 */

import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { meshApi } from "@/lib/mesh-api.ts";
import type { GoldenSignals, MeshType } from "@/lib/mesh-types.ts";

const REFRESH_INTERVAL_MS = 30_000;

interface Props {
  namespace: string;
  service: string;
}

export function MeshGoldenSignals({ namespace, service }: Props) {
  // null = not yet decided / hidden; populated only when we have something
  // worth rendering. We never set this to a zero-valued meshed signal
  // (see header comment) — those services hide instead.
  const data = useSignal<GoldenSignals | null>(null);
  const offline = useSignal(false);
  const offlineReason = useSignal<string>("");

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    // inFlight protects against re-entry: if a load() is still awaiting
    // when the 30s tick fires, we skip rather than stack concurrent
    // fetches. On slow networks or during a Prometheus restart the
    // setInterval cadence can outrun the round-trip; skipping keeps
    // request count bounded to one per cycle.
    let inFlight = false;

    async function load() {
      if (inFlight) return;
      inFlight = true;
      try {
        const status = await meshApi.status();
        if (cancelled) return;
        const detected: MeshType = status.data.status.detected;
        if (!detected) {
          // No mesh installed — hide.
          data.value = null;
          offline.value = false;
          return;
        }

        // When both meshes are installed the backend requires an
        // explicit ?mesh= disambiguator. In v1 we can't infer which
        // mesh manages this specific service from the frontend, so we
        // hide rather than guess. (Future enhancement: pass workload
        // context from ServiceOverview.)
        if (detected === "both") {
          data.value = null;
          offline.value = false;
          return;
        }

        const res = await meshApi.goldenSignals({ namespace, service });
        if (cancelled) return;

        const signals = res.data.signals;

        if (!signals.available) {
          // Prometheus offline (or full PromQL fan-out failure).
          // Render the card with the unavailable banner — operators
          // need to see this rather than silent absence.
          data.value = null;
          offline.value = true;
          offlineReason.value = signals.reason || "metrics_unavailable";
          return;
        }

        // available=true. Distinguish "unmeshed (or genuinely silent)
        // service" from "meshed with traffic" by checking whether ANY
        // metric is non-zero. A meshed service with traffic always has
        // at least RPS > 0; a meshed-but-silent service produces no
        // useful card. Hide both cases.
        //
        // Known limitation: the backend reports available=true even
        // when 5 of 6 PromQL queries failed (partial-success contract,
        // see internal/servicemesh/metrics.go). A heavily degraded
        // Prometheus that only answers one query with zeros looks the
        // same as a silent meshed service here. Surfacing that
        // distinction requires a backend signal we don't have today.
        const hasTraffic = signals.rps > 0 ||
          signals.errorRate > 0 ||
          signals.p50Ms > 0 ||
          signals.p95Ms > 0 ||
          signals.p99Ms > 0;
        if (!hasTraffic) {
          data.value = null;
          offline.value = false;
          return;
        }

        data.value = signals;
        offline.value = false;
      } catch (_err) {
        // 4xx (no mesh detected, validation, RBAC denial) — hide
        // silently. The Service detail page must not surface a toast
        // for an optional enrichment that didn't apply.
        if (!cancelled) {
          data.value = null;
          offline.value = false;
        }
      } finally {
        inFlight = false;
      }
    }

    load();
    const id = setInterval(load, REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [namespace, service]);

  if (offline.value) {
    return (
      <section>
        <h3 class="mb-2 text-sm font-semibold uppercase tracking-wide text-text-muted">
          Service Mesh — Golden Signals
        </h3>
        <div class="rounded-md border border-border-primary bg-bg-surface p-4 text-sm text-text-secondary">
          Metrics unavailable
          <span class="ml-2 text-xs text-text-muted">
            ({offlineReason.value})
          </span>
        </div>
      </section>
    );
  }

  const s = data.value;
  if (!s) return null;

  return (
    <section>
      <h3 class="mb-2 text-sm font-semibold uppercase tracking-wide text-text-muted">
        Service Mesh — Golden Signals
        <span class="ml-2 rounded bg-bg-elevated px-1.5 py-0.5 text-xs font-normal text-text-muted">
          {s.mesh}
        </span>
      </h3>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <Metric label="RPS" value={formatRps(s.rps)} />
        <Metric
          label="Error rate"
          value={formatErrorRate(s.errorRate)}
          tone={s.errorRate >= 0.05
            ? "error"
            : s.errorRate >= 0.01
            ? "warning"
            : "default"}
        />
        <Metric label="p50" value={formatMs(s.p50Ms)} />
        <Metric label="p95" value={formatMs(s.p95Ms)} />
        <Metric label="p99" value={formatMs(s.p99Ms)} />
      </div>
    </section>
  );
}

function Metric(
  { label, value, tone = "default" }: {
    label: string;
    value: string;
    tone?: "default" | "warning" | "error";
  },
) {
  const valueColor = tone === "error"
    ? "var(--status-error)"
    : tone === "warning"
    ? "var(--status-warning)"
    : "var(--text-primary)";
  return (
    <div class="rounded-md border border-border-primary bg-bg-surface p-3">
      <div class="text-xs text-text-muted">{label}</div>
      <div
        class="mt-1 font-mono text-lg font-semibold"
        style={{ color: valueColor }}
      >
        {value}
      </div>
    </div>
  );
}

// --- Formatters ---

function formatRps(rps: number): string {
  if (rps >= 1000) return `${(rps / 1000).toFixed(1)}k req/s`;
  if (rps >= 10) return `${rps.toFixed(0)} req/s`;
  return `${rps.toFixed(2)} req/s`;
}

function formatErrorRate(rate: number): string {
  // Backend convention: fraction in [0, 1]. Render as percentage.
  return `${(rate * 100).toFixed(2)}%`;
}

function formatMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  if (ms >= 100) return `${ms.toFixed(0)} ms`;
  return `${ms.toFixed(1)} ms`;
}
