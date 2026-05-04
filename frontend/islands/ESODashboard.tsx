/** External Secrets Operator dashboard — Phase B Unit 8b.
 *
 * Surfaces sync health at a glance:
 *   - Hero gauge ring: synced / total ExternalSecrets
 *   - Secondary cards: SyncFailed / Stale / Drifted / Unknown counts
 *   - Tertiary row: stores by provider + cost-tier stub (Phase F)
 *   - Failure table: ExternalSecrets in non-Synced/non-Refreshing state
 *
 * Data sources (loaded concurrently via Promise.all):
 *   - esoApi.status() — ESO discovery
 *   - esoApi.listExternalSecrets() — primary inventory
 *   - esoApi.listStores() / listClusterStores() — store provider breakdown
 *
 * Theme tokens only — no hardcoded color classes.
 */

import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import type {
  ESOStatus,
  ExternalSecret,
  SecretStore,
  Status,
} from "@/lib/eso-types.ts";

const FAILURE_TABLE_LIMIT = 50;

/** Severity ordering for the broken-ES table. Synced/Refreshing rows are
 * filtered out before sort. Drifted falls below Stale because Drifted is
 * "config out of band" while Stale is "controller hasn't synced in too long". */
const FAILURE_SEVERITY: Record<Status, number> = {
  SyncFailed: 0,
  Stale: 1,
  Drifted: 2,
  Unknown: 3,
  Refreshing: 4,
  Synced: 5,
};

/** Hero gauge — synced / total. Renders the SVG ring inline (no external
 * chart lib) using `var(--success)` for the synced arc and the standard
 * GaugeRing primitive isn't reused here because we want a fraction display
 * (`X / Y`) rather than the percentage label GaugeRing emits. */
function SyncHealthRing(
  { synced, total }: { synced: number; total: number },
) {
  const size = 200;
  const strokeWidth = 12;
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const fraction = total > 0 ? synced / total : 0;
  const offset = circumference - fraction * circumference;
  const center = size / 2;

  return (
    <div
      class="relative inline-flex items-center justify-center"
      role="img"
      aria-label={`${synced} of ${total} synced`}
      style={{ width: `${size}px`, height: `${size}px` }}
    >
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke="var(--bg-elevated)"
          stroke-width={strokeWidth}
        />
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          stroke="var(--success)"
          stroke-width={strokeWidth}
          stroke-linecap="round"
          stroke-dasharray={circumference}
          stroke-dashoffset={offset}
          transform={`rotate(-90 ${center} ${center})`}
          style={{
            transition: "stroke-dashoffset 1s cubic-bezier(0.16, 1, 0.3, 1)",
          }}
        />
      </svg>
      <div class="absolute inset-0 flex flex-col items-center justify-center">
        <span
          class="font-bold text-text-primary"
          style={{
            fontSize: "32px",
            fontFamily: "var(--font-mono, monospace)",
          }}
        >
          {synced} / {total}
        </span>
        <span class="text-xs text-text-muted mt-1">
          ExternalSecrets synced
        </span>
      </div>
    </div>
  );
}

interface SecondaryCardProps {
  label: string;
  count: number;
  color: string;
  subText: string;
  href?: string;
}

function SecondaryCard(
  { label, count, color, subText, href }: SecondaryCardProps,
) {
  const inner = (
    <div
      class="rounded-lg border border-border-primary p-4 bg-elevated h-full flex flex-col"
      aria-label={`${label}: ${count}`}
    >
      <div class="flex items-center justify-between mb-2">
        <span class="text-xs font-medium uppercase tracking-wide text-text-muted">
          {label}
        </span>
      </div>
      <span
        class="font-bold"
        style={{
          color,
          fontSize: "28px",
          fontFamily: "var(--font-mono, monospace)",
          lineHeight: 1.1,
        }}
      >
        {count}
      </span>
      <p class="text-xs text-text-muted mt-2 leading-snug">{subText}</p>
    </div>
  );
  if (href && count > 0) {
    return (
      <a href={href} class="block hover:opacity-90 transition-opacity">
        {inner}
      </a>
    );
  }
  return inner;
}

/** Aggregate provider counts across Namespaced + Cluster stores.
 * Empty/unset providers fold into the "(none)" bucket. */
function aggregateProviders(stores: SecretStore[]): Array<[string, number]> {
  const counts = new Map<string, number>();
  for (const s of stores) {
    const key = s.provider || "(none)";
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1]);
}

/** Mirror of backend's ResolveBillingProvider — Phase F Unit 16. Returns
 * an empty string for self-hosted / unknown providers so callers can filter
 * the cost card to paid-tier stores only. AWS disambiguation reads
 * spec.service the same way the backend does. */
function resolveBillingProvider(s: SecretStore): string {
  switch (s.provider) {
    case "aws": {
      const svc = s.providerSpec?.service;
      if (typeof svc === "string" && svc.toLowerCase() === "parameterstore") {
        return "aws-parameter-store-advanced";
      }
      return "aws-secrets-manager";
    }
    case "gcpsm":
      return "gcp-secret-manager";
    case "azurekv":
      return "azure-key-vault";
    default:
      return "";
  }
}

/** Static rate-card snapshot date — kept in sync with backend's cost_tier.go.
 * Surfaced on the dashboard caveat so operators know how fresh the estimate
 * is without drilling into a single store. */
const RATE_CARD_SNAPSHOT_DATE = "2026-04-30";

function ProviderCostCard({ stores }: { stores: SecretStore[] }) {
  const counts = new Map<string, number>();
  for (const s of stores) {
    const key = resolveBillingProvider(s);
    if (!key) continue;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  const billingProviders = [...counts.entries()].sort((a, b) => b[1] - a[1]);

  return (
    <div
      class="rounded-lg border border-border-primary p-4 bg-elevated"
      aria-label="Cost estimate"
    >
      <h2 class="text-sm font-medium text-text-primary mb-3">
        Per-provider cost estimate
      </h2>
      {billingProviders.length === 0
        ? (
          <p class="text-xs text-text-muted">
            No paid-tier stores visible — self-hosted providers (Vault,
            Kubernetes) carry no per-request charge.
          </p>
        )
        : (
          <ul class="space-y-2">
            {billingProviders.map(([key, n]) => (
              <li
                key={key}
                class="flex items-center justify-between gap-3 text-sm"
              >
                <span class="text-text-primary font-mono text-xs">{key}</span>
                <span class="text-xs text-text-muted">
                  {n} {n === 1 ? "store" : "stores"}
                </span>
              </li>
            ))}
          </ul>
        )}
      <p class="text-xs text-text-muted mt-3 leading-snug">
        Per-store dollar estimates render on each store's detail page. Rates as
        of {RATE_CARD_SNAPSHOT_DATE}; not connected to live billing.
      </p>
    </div>
  );
}

export default function ESODashboard() {
  const status = useSignal<ESOStatus | null>(null);
  const externalSecrets = useSignal<ExternalSecret[]>([]);
  const stores = useSignal<SecretStore[]>([]);
  const clusterStores = useSignal<SecretStore[]>([]);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const refreshing = useSignal(false);

  async function fetchData() {
    try {
      const [statusRes, esRes, storesRes, clusterStoresRes] = await Promise
        .all([
          esoApi.status(),
          esoApi.listExternalSecrets(),
          esoApi.listStores(),
          esoApi.listClusterStores(),
        ]);
      status.value = statusRes.data;
      externalSecrets.value = Array.isArray(esRes.data) ? esRes.data : [];
      stores.value = Array.isArray(storesRes.data) ? storesRes.data : [];
      clusterStores.value = Array.isArray(clusterStoresRes.data)
        ? clusterStoresRes.data
        : [];
      error.value = null;
    } catch {
      error.value = "Failed to load ESO dashboard data";
    }
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      loading.value = false;
    });
  }, []);

  async function handleRefresh() {
    refreshing.value = true;
    await fetchData();
    refreshing.value = false;
  }

  if (!IS_BROWSER) return null;

  // Aggregate counts client-side. Backend already RBAC-filters.
  const items = externalSecrets.value;
  const total = items.length;
  const counts = {
    Synced: 0,
    SyncFailed: 0,
    Refreshing: 0,
    Stale: 0,
    Drifted: 0,
    Unknown: 0,
  } satisfies Record<Status, number>;
  for (const it of items) counts[it.status]++;

  const allStores = [...stores.value, ...clusterStores.value];
  const providerCounts = aggregateProviders(allStores);

  // Failure table — non-Synced / non-Refreshing only.
  const broken = items
    .filter((i) => i.status !== "Synced" && i.status !== "Refreshing")
    .sort((a, b) => {
      const sev = FAILURE_SEVERITY[a.status] - FAILURE_SEVERITY[b.status];
      if (sev !== 0) return sev;
      return `${a.namespace}/${a.name}`.localeCompare(
        `${b.namespace}/${b.name}`,
      );
    });
  const brokenDisplayed = broken.slice(0, FAILURE_TABLE_LIMIT);
  const brokenOverflow = broken.length - brokenDisplayed.length;

  const detected = status.value?.detected === true;

  return (
    <div class="p-6">
      <div class="flex items-center justify-between mb-1">
        <h1 class="text-2xl font-bold text-text-primary">External Secrets</h1>
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
      <p class="text-sm text-text-muted mb-6">
        Sync health across ExternalSecret resources, source stores by provider,
        and currently broken syncs.
      </p>

      {loading.value && (
        <div class="flex justify-center py-12">
          <Spinner class="text-brand" />
        </div>
      )}

      {error.value && <p class="text-sm text-danger py-4">{error.value}</p>}

      {!loading.value && !error.value && !detected && <ESONotDetected />}

      {!loading.value && !error.value && detected && (
        <>
          {
            /* Hero — sync health ring. Renders 0/0 when no ExternalSecrets are
           * visible so the dashboard's vertical rhythm doesn't shift the moment
           * the first ES appears. */
          }
          <div class="rounded-lg border border-border-primary p-6 bg-elevated mb-6 flex flex-col items-center">
            <SyncHealthRing synced={counts.Synced} total={total} />
            {total === 0 && (
              <p class="text-xs text-text-muted mt-3">
                No ExternalSecrets visible yet.
              </p>
            )}
          </div>

          {/* Secondary cards row */}
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <SecondaryCard
              label="Sync Failed"
              count={counts.SyncFailed}
              color="var(--danger)"
              subText="ExternalSecrets the controller could not reconcile."
              href="/external-secrets/external-secrets?status=SyncFailed"
            />
            <SecondaryCard
              label="Stale"
              count={counts.Stale}
              color="var(--warning)"
              subText="Phase D resolves stale thresholds — count is 0 until that ships."
            />
            <SecondaryCard
              label="Drifted"
              count={counts.Drifted}
              color="var(--accent-secondary)"
              subText="Cached hint refreshed every ~90s; detail page is source of truth."
            />
            <SecondaryCard
              label="Unknown"
              count={counts.Unknown}
              color="var(--text-muted)"
              subText="Brand-new ExternalSecrets the controller hasn't reconciled yet."
            />
          </div>

          {/* Tertiary row — providers + cost stub */}
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
            <div
              class="rounded-lg border border-border-primary p-4 bg-elevated"
              aria-label="Stores by provider"
            >
              <h2 class="text-sm font-medium text-text-primary mb-3">
                Stores by provider
              </h2>
              {providerCounts.length === 0
                ? (
                  <p class="text-xs text-text-muted">
                    No SecretStores or ClusterSecretStores visible.
                  </p>
                )
                : (
                  <ul class="space-y-2">
                    {providerCounts.map(([provider, n]) => (
                      <li
                        key={provider}
                        class="flex items-center justify-between gap-3"
                      >
                        <ProviderBadge provider={provider} />
                        <span class="text-xs font-mono text-text-secondary">
                          {n} {n === 1 ? "store" : "stores"}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
            </div>

            <ProviderCostCard stores={allStores} />
          </div>

          {/* Failure table */}
          <div class="mb-1">
            <h2 class="text-lg font-semibold text-text-primary">
              Broken ExternalSecrets right now
            </h2>
            <p class="text-sm text-text-muted mb-3">
              ExternalSecrets currently in SyncFailed, Stale, Drifted, or
              Unknown state.
            </p>
          </div>
          {broken.length === 0
            ? (
              <div class="text-center py-8 rounded-lg border border-border-primary bg-elevated">
                <p class="text-text-muted">
                  No broken ExternalSecrets — everything synced!
                </p>
              </div>
            )
            : (
              <div class="overflow-x-auto rounded-lg border border-border-primary">
                <table class="w-full text-sm">
                  <thead>
                    <tr class="border-b border-border-primary bg-surface">
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                      >
                        Namespace
                      </th>
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                      >
                        Name
                      </th>
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                      >
                        Status
                      </th>
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                      >
                        Reason
                      </th>
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                      >
                        Last Sync
                      </th>
                      <th
                        scope="col"
                        class="px-3 py-2 text-left text-xs font-medium text-text-muted"
                        title="Affected workload counts ship in Phase I (chain visualization)."
                      >
                        Affected workloads
                      </th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-border-subtle">
                    {brokenDisplayed.map((es) => {
                      const href = `/external-secrets/external-secrets/${
                        encodeURIComponent(es.namespace)
                      }/${encodeURIComponent(es.name)}`;
                      return (
                        <tr key={es.uid} class="hover:bg-hover/30">
                          <td class="px-3 py-2 text-text-secondary">
                            {es.namespace}
                          </td>
                          <td class="px-3 py-2">
                            <a
                              href={href}
                              class="text-brand hover:underline font-medium"
                            >
                              {es.name}
                            </a>
                          </td>
                          <td class="px-3 py-2">
                            <StatusBadge status={es.status} />
                          </td>
                          <td class="px-3 py-2 text-text-secondary text-xs">
                            {es.readyReason ?? "—"}
                          </td>
                          <td class="px-3 py-2 text-text-secondary text-xs">
                            {es.lastSyncTime ? timeAgo(es.lastSyncTime) : "—"}
                          </td>
                          <td
                            class="px-3 py-2 text-text-muted text-xs"
                            title="Affected workload counts ship in Phase I (chain visualization)."
                          >
                            —
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                {brokenOverflow > 0 && (
                  <div class="px-3 py-2 border-t border-border-primary bg-surface text-xs text-text-muted text-center">
                    +{brokenOverflow} more
                  </div>
                )}
              </div>
            )}
        </>
      )}
    </div>
  );
}
