/** Per-store rate + cost-tier panel — Phase F Unit 16.
 *
 * Embedded in ESOStoreDetail / ESOClusterStoreDetail. Hits
 * /externalsecrets/{stores,clusterstores}/.../metrics on mount. The endpoint
 * always returns HTTP 200; degraded responses surface via the `error` field
 * so we branch on payload shape rather than status code.
 *
 * Theme tokens only.
 */

import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { StoreMetrics } from "@/lib/eso-types.ts";

interface Props {
  /** Empty for ClusterSecretStore. */
  namespace: string;
  name: string;
  scope: "Namespaced" | "Cluster";
}

const EM_DASH = "—";

function formatRate(rpm: number | null): string {
  if (rpm === null) return EM_DASH;
  if (rpm < 0.01) return "< 0.01";
  if (rpm < 10) return rpm.toFixed(2);
  return rpm.toFixed(1);
}

function formatCount(n: number | null): string {
  if (n === null) return EM_DASH;
  if (n < 1_000) return n.toFixed(0);
  if (n < 1_000_000) return `${(n / 1_000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(2)}M`;
}

function formatUSD(n: number | undefined): string {
  if (n === undefined) return EM_DASH;
  if (n < 0.01) return "< $0.01";
  return `$${n.toFixed(2)}`;
}

function formatSnapshotDate(iso: string | undefined): string {
  if (!iso) return EM_DASH;
  // Backend emits RFC3339 at 00:00 UTC; render as YYYY-MM-DD.
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return EM_DASH;
  return d.toISOString().slice(0, 10);
}

export default function ESOStoreMetricsPanel(
  { namespace, name, scope }: Props,
) {
  const loading = useSignal(true);
  const data = useSignal<StoreMetrics | null>(null);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      loading.value = true;
      try {
        const res = scope === "Cluster"
          ? await esoApi.getClusterStoreMetrics(name)
          : await esoApi.getStoreMetrics(namespace, name);
        if (!cancelled) data.value = res.data ?? null;
      } catch {
        if (!cancelled) {
          data.value = {
            ratePerMin: null,
            last24h: null,
            error: "rate metrics offline",
          };
        }
      } finally {
        if (!cancelled) loading.value = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, name, scope]);

  if (!IS_BROWSER) return null;

  return (
    <div
      class="rounded-lg border border-border-primary bg-elevated p-5"
      aria-label="Store request rate and cost"
    >
      <div class="flex items-baseline justify-between mb-4">
        <h2 class="text-sm font-semibold text-text-primary">
          Request rate
        </h2>
        {loading.value && <Spinner class="text-brand" />}
      </div>

      {!loading.value && data.value?.error
        ? (
          <p class="text-sm text-text-muted">
            {data.value.error === "rate metrics offline"
              ? "Rate metrics offline — Prometheus is unavailable."
              : data.value.error}
          </p>
        )
        : !loading.value && data.value
        ? (
          <>
            <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
              <div>
                <dt class="text-text-muted text-xs uppercase tracking-wide">
                  Per minute (last 5m)
                </dt>
                <dd class="text-text-primary font-mono mt-1 text-lg">
                  {formatRate(data.value.ratePerMin)}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted text-xs uppercase tracking-wide">
                  Last 24h
                </dt>
                <dd class="text-text-primary font-mono mt-1 text-lg">
                  {formatCount(data.value.last24h)}
                </dd>
              </div>
            </dl>

            {data.value.cost && (
              <div class="mt-4 pt-4 border-t border-border-subtle">
                <div class="flex items-baseline justify-between mb-1">
                  <span class="text-text-muted text-xs uppercase tracking-wide">
                    Estimated 24h spend
                  </span>
                  <span class="text-text-primary font-mono text-base">
                    {formatUSD(data.value.cost.estimated24h)}{" "}
                    <span class="text-xs text-text-muted">
                      {data.value.cost.currency ?? ""}
                    </span>
                  </span>
                </div>
                <p class="text-xs text-text-muted leading-snug">
                  Rates as of {formatSnapshotDate(data.value.cost.lastUpdated)}
                  {" "}
                  for{" "}
                  {data.value.cost.billingProvider}; not connected to live
                  billing.
                </p>
              </div>
            )}
            {!data.value.cost && data.value.ratePerMin !== null && (
              <p class="text-xs text-text-muted mt-3 leading-snug">
                Self-hosted provider — no cost estimate.
              </p>
            )}
          </>
        )
        : null}
    </div>
  );
}
