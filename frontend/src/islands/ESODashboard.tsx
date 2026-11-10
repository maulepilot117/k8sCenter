/** @jsxImportSource react */
/** External Secrets Operator dashboard — Liquid Glass re-skin (Phase 8).
 *
 * Surfaces sync health at a glance:
 *   - Hero gauge ring: synced / total ExternalSecrets
 *   - Secondary cards: SyncFailed / Stale / Drifted / Unknown counts
 *   - Tertiary row: stores by provider + cost-tier stub
 *   - Failure table: ExternalSecrets in non-Synced/non-Refreshing state
 *
 * Glass = chrome/widget cards. Solid = failure data table (data surface rule).
 * Tokens only — no hardcoded colors. All error states use var(--error).
 */

import { useCallback, useEffect, useState } from "react";
import RadialGauge from "@/components/charts/RadialGauge.tsx";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESONotDetected } from "@/components/eso/ESONotDetected.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Card } from "@/components/ui/Card.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type {
  ESOStatus,
  ExternalSecret,
  SecretStore,
  Status,
} from "@/lib/eso-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { filterByNamespace, selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

const FAILURE_TABLE_LIMIT = 50;

const FAILURE_SEVERITY: Record<Status, number> = {
  SyncFailed: 0,
  Stale: 1,
  Drifted: 2,
  Unknown: 3,
  Refreshing: 4,
  Synced: 5,
};

/** Map ESO status → StatusDot tone for the failure table name column. */
function esoStatusToDot(
  status: Status,
): "error" | "warning" | "info" | "neutral" {
  switch (status) {
    case "SyncFailed":
      return "error";
    case "Stale":
      return "warning";
    case "Drifted":
      return "info";
    default:
      return "neutral";
  }
}

/** Hero gauge — synced / total. Uses var(--success) for the synced arc and
 * var(--bg-elevated) for the track. */
function SyncHealthRing({ synced, total }: { synced: number; total: number }) {
  return (
    <RadialGauge
      percent={total > 0 ? (synced / total) * 100 : 0}
      size={200}
      thickness={12}
      color="var(--success)"
      track="var(--bg-elevated)"
      label={`${synced} of ${total} synced`}
    >
      <span
        className="font-bold text-text-primary tabular-nums"
        style={{
          fontSize: "32px",
          fontFamily: "var(--font-mono, monospace)",
        }}
      >
        {synced} / {total}
      </span>
      <span
        className="mt-1 text-xs uppercase tracking-wide"
        style={{
          fontSize: "11px",
          fontWeight: 600,
          color: "var(--text-muted)",
        }}
      >
        ExternalSecrets synced
      </span>
    </RadialGauge>
  );
}

interface StatCardProps {
  label: string;
  count: number;
  color: string;
  subText: string;
  href?: string;
}

function StatCard({ label, count, color, subText, href }: StatCardProps) {
  const inner = (
    <Card
      glass
      className="h-full flex flex-col"
      aria-label={`${label}: ${count}`}
    >
      <span
        style={{
          fontSize: "11px",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.05em",
          color: "var(--text-muted)",
        }}
      >
        {label}
      </span>
      <span
        className="mt-2 tabular-nums"
        style={{
          color,
          fontSize: "28px",
          fontWeight: 700,
          fontFamily: "var(--font-mono, monospace)",
          lineHeight: 1.1,
        }}
      >
        {count}
      </span>
      <p
        className="mt-2 leading-snug"
        style={{ fontSize: "12px", color: "var(--text-muted)" }}
      >
        {subText}
      </p>
    </Card>
  );
  if (href && count > 0) {
    return (
      <a href={href} className="block hover:opacity-90 transition-opacity">
        {inner}
      </a>
    );
  }
  return inner;
}

function aggregateProviders(stores: SecretStore[]): Array<[string, number]> {
  const counts = new Map<string, number>();
  for (const s of stores) {
    const key = s.provider || "(none)";
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1]);
}

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
    <Card glass aria-label="Cost estimate">
      <h2
        className="mb-3"
        style={{
          fontSize: "14px",
          fontWeight: 650,
          color: "var(--text-primary)",
        }}
      >
        Per-provider cost estimate
      </h2>
      {billingProviders.length === 0 ? (
        <p style={{ fontSize: "12px", color: "var(--text-muted)" }}>
          No paid-tier stores visible — self-hosted providers (Vault,
          Kubernetes) carry no per-request charge.
        </p>
      ) : (
        <ul className="space-y-2">
          {billingProviders.map(([key, n]) => (
            <li key={key} className="flex items-center justify-between gap-3">
              <span
                style={{
                  fontSize: "12px",
                  fontFamily: "var(--font-mono, monospace)",
                  color: "var(--text-primary)",
                }}
              >
                {key}
              </span>
              <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>
                {n} {n === 1 ? "store" : "stores"}
              </span>
            </li>
          ))}
        </ul>
      )}
      <p
        className="mt-3 leading-snug"
        style={{ fontSize: "12px", color: "var(--text-muted)" }}
      >
        Per-store dollar estimates render on each store's detail page. Rates as
        of {RATE_CARD_SNAPSHOT_DATE}; not connected to live billing.
      </p>
    </Card>
  );
}

export default function ESODashboard() {
  const isClient = useIsClient();
  const ns = useSignalValue(selectedNamespace);
  const [status, setStatus] = useState<ESOStatus | null>(null);
  const [externalSecrets, setExternalSecrets] = useState<ExternalSecret[]>([]);
  const [stores, setStores] = useState<SecretStore[]>([]);
  const [clusterStores, setClusterStores] = useState<SecretStore[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const [statusRes, esRes, storesRes, clusterStoresRes] = await Promise.all(
        [
          esoApi.status(),
          esoApi.listExternalSecrets(),
          esoApi.listStores(),
          esoApi.listClusterStores(),
        ],
      );
      setStatus(statusRes.data);
      setExternalSecrets(Array.isArray(esRes.data) ? esRes.data : []);
      setStores(Array.isArray(storesRes.data) ? storesRes.data : []);
      setClusterStores(
        Array.isArray(clusterStoresRes.data) ? clusterStoresRes.data : [],
      );
      setError(null);
    } catch {
      setError("Failed to load ESO dashboard data");
    }
  }, []);

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchData().then(() => {
      setLoading(false);
    });
  }, [fetchData]);

  async function handleRefresh() {
    setRefreshing(true);
    await fetchData();
    setRefreshing(false);
  }

  if (!isClient) return null;

  const allExternalSecrets = externalSecrets;
  const items = filterByNamespace(allExternalSecrets, ns);
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

  const filteredStores = filterByNamespace(stores, ns);
  const allStores = [...filteredStores, ...clusterStores];
  const providerCounts = aggregateProviders(allStores);

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

  const detected = status?.detected === true;

  return (
    <div className="p-6">
      {/* Page header */}
      <div className="flex items-center justify-between mb-1">
        <h1
          style={{
            fontSize: "24px",
            fontWeight: 700,
            letterSpacing: "-0.02em",
            color: "var(--text-primary)",
          }}
        >
          External Secrets
        </h1>
        {!loading && (
          <Button
            type="button"
            variant="ghost"
            onClick={handleRefresh}
            disabled={refreshing}
          >
            {refreshing ? "Refreshing…" : "Refresh"}
          </Button>
        )}
      </div>
      <p
        className="mb-6"
        style={{ fontSize: "13px", color: "var(--text-muted)" }}
      >
        Sync health across ExternalSecret resources, source stores by provider,
        and currently broken syncs.
      </p>

      {loading && (
        <div className="flex justify-center py-12">
          <Spinner className="text-brand" />
        </div>
      )}

      {error && (
        <p style={{ fontSize: "13px", color: "var(--error)" }} className="py-4">
          {error}
        </p>
      )}

      {!loading && !error && !detected && <ESONotDetected />}

      {!loading && !error && detected && (
        <>
          {/* Hero — sync health ring in a glass card */}
          <Card glass className="mb-6 flex flex-col items-center">
            <SyncHealthRing synced={counts.Synced} total={total} />
            {total === 0 ? (
              <p
                className="mt-3"
                style={{ fontSize: "12px", color: "var(--text-muted)" }}
              >
                {ns !== "all" && ns
                  ? `No ExternalSecrets in namespace "${ns}".`
                  : "No ExternalSecrets visible yet."}
              </p>
            ) : ns !== "all" && ns ? (
              <p
                className="mt-1"
                style={{ fontSize: "12px", color: "var(--text-muted)" }}
              >
                {total} of {allExternalSecrets.length} total (namespace: {ns})
              </p>
            ) : null}
          </Card>

          {/* Secondary stat cards row */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
            <StatCard
              label="Sync Failed"
              count={counts.SyncFailed}
              color="var(--error)"
              subText="ExternalSecrets the controller could not reconcile."
              href="/external-secrets/external-secrets?status=SyncFailed"
            />
            <StatCard
              label="Stale"
              count={counts.Stale}
              color="var(--warning)"
              subText="Synced, but not refreshed within the threshold set by kubecenter.io/eso-stale-after-minutes."
            />
            <StatCard
              label="Drifted"
              count={counts.Drifted}
              color="var(--accent-2)"
              subText="Cached hint refreshed every ~90s; detail page is source of truth."
            />
            <StatCard
              label="Unknown"
              count={counts.Unknown}
              color="var(--text-muted)"
              subText="Brand-new ExternalSecrets the controller hasn't reconciled yet."
            />
          </div>

          {/* Tertiary row — providers + cost stub */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
            <Card glass aria-label="Stores by provider">
              <h2
                className="mb-3"
                style={{
                  fontSize: "14px",
                  fontWeight: 650,
                  color: "var(--text-primary)",
                }}
              >
                Stores by provider
              </h2>
              {providerCounts.length === 0 ? (
                <p style={{ fontSize: "12px", color: "var(--text-muted)" }}>
                  No SecretStores or ClusterSecretStores visible.
                </p>
              ) : (
                <ul className="space-y-2">
                  {providerCounts.map(([provider, n]) => (
                    <li
                      key={provider}
                      className="flex items-center justify-between gap-3"
                    >
                      <ProviderBadge provider={provider} />
                      <span
                        style={{
                          fontSize: "12px",
                          fontFamily: "var(--font-mono, monospace)",
                          color: "var(--text-muted)",
                        }}
                      >
                        {n} {n === 1 ? "store" : "stores"}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </Card>

            <ProviderCostCard stores={allStores} />
          </div>

          {/* Failure table — solid data surface */}
          <div className="mb-3">
            <h2
              style={{
                fontSize: "17px",
                fontWeight: 650,
                color: "var(--text-primary)",
              }}
            >
              Broken ExternalSecrets right now
            </h2>
            <p
              className="mt-1"
              style={{ fontSize: "13px", color: "var(--text-muted)" }}
            >
              ExternalSecrets currently in SyncFailed, Stale, Drifted, or
              Unknown state.
            </p>
          </div>

          {broken.length === 0 ? (
            <div
              className="text-center py-8 rounded-2xl"
              style={{
                background: "var(--bg-surface)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>
                No broken ExternalSecrets — everything synced!
              </p>
            </div>
          ) : (
            <div
              className="overflow-x-auto rounded-lg"
              style={{
                background: "var(--bg-surface)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <table className="w-full text-sm">
                <thead>
                  <tr
                    style={{ borderBottom: "1px solid var(--border-subtle)" }}
                  >
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      Namespace
                    </th>
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      Name
                    </th>
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      Status
                    </th>
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      Reason
                    </th>
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                    >
                      Last Sync
                    </th>
                    <th
                      scope="col"
                      className="px-3 py-2 text-left"
                      style={{
                        fontSize: "11px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: "0.05em",
                        color: "var(--text-muted)",
                      }}
                      title="Affected workload counts ship in Phase I (chain visualization)."
                    >
                      Affected workloads
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {brokenDisplayed.map((es) => {
                    const href = `/external-secrets/external-secrets/${encodeURIComponent(
                      es.namespace,
                    )}/${encodeURIComponent(es.name)}`;
                    return (
                      <tr
                        key={es.uid}
                        style={{
                          borderTop: "1px solid var(--border-subtle)",
                        }}
                        className="hover:bg-hover/30"
                      >
                        <td
                          className="px-3 py-2"
                          style={{
                            fontSize: "13px",
                            color: "var(--text-muted)",
                          }}
                        >
                          {es.namespace}
                        </td>
                        <td className="px-3 py-2">
                          <a
                            href={href}
                            className="inline-flex items-center gap-2 hover:underline"
                            style={{ color: "var(--accent)" }}
                          >
                            <StatusDot status={esoStatusToDot(es.status)} />
                            <span
                              style={{
                                fontSize: "13px",
                                fontWeight: 500,
                                fontFamily: "var(--font-mono, monospace)",
                              }}
                            >
                              {es.name}
                            </span>
                          </a>
                        </td>
                        <td className="px-3 py-2">
                          <StatusBadge status={es.status} />
                        </td>
                        <td
                          className="px-3 py-2"
                          style={{
                            fontSize: "12px",
                            color: "var(--text-muted)",
                          }}
                        >
                          {es.readyReason ?? "—"}
                        </td>
                        <td
                          className="px-3 py-2 tabular-nums"
                          style={{
                            fontSize: "12px",
                            color: "var(--text-muted)",
                          }}
                        >
                          {es.lastSyncTime ? timeAgo(es.lastSyncTime) : "—"}
                        </td>
                        <td
                          className="px-3 py-2"
                          style={{
                            fontSize: "12px",
                            color: "var(--text-muted)",
                          }}
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
                <div
                  className="px-3 py-2 text-center"
                  style={{
                    borderTop: "1px solid var(--border-subtle)",
                    background: "var(--bg-surface)",
                    fontSize: "12px",
                    color: "var(--text-muted)",
                  }}
                >
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
