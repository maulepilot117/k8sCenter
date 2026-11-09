/** @jsxImportSource react */
import { useEffect, useState } from "react";
import { ProviderBadge, StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import { type EvidenceTabKey, evidenceTabsFor } from "@/lib/eso-evidence.ts";
import type { SecretStore } from "@/lib/eso-types.ts";
import ESOBulkRefreshDialog from "@/src/islands/ESOBulkRefreshDialog.tsx";
import ESOChainPanel from "@/src/islands/ESOChainPanel.tsx";
import ESOEvidencePanel from "@/src/islands/ESOEvidencePanel.tsx";
import ESOStoreMetricsPanel from "@/src/islands/ESOStoreMetricsPanel.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";

interface Props {
  namespace: string;
  name: string;
}

type TabKey = "overview" | EvidenceTabKey | "chain";

// No History tab: history is collected per ExternalSecret, and the D5 matrix
// gives a store none of its own (R12). Deriving the strip from it keeps an
// ExternalSecret's attempts from ever being offered as the store's.
const TABS: Array<{ key: TabKey; label: string }> = [
  { key: "overview", label: "Overview" },
  ...evidenceTabsFor("secretstores"),
  { key: "chain", label: "Chain" },
];

function isEvidenceTab(tab: TabKey): tab is EvidenceTabKey {
  return tab !== "overview" && tab !== "chain";
}

const EM_DASH = "—";

export default function ESOStoreDetail({ namespace, name }: Props) {
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<SecretStore | null>(null);
  const [activeTab, setActiveTab] = useState<TabKey>("overview");
  // The last evidence tab opened; the panel stays mounted once opened.
  const [evidenceTab, setEvidenceTab] = useState<EvidenceTabKey | null>(null);
  const [showRefreshDialog, setShowRefreshDialog] = useState(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await esoApi.getStore(namespace, name);
        if (!cancelled) setData(res.data ?? null);
      } catch {
        if (!cancelled) setError("Failed to load SecretStore");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, name]);

  if (!isClient) return null;

  if (loading) {
    return (
      <div className="flex justify-center py-12">
        <Spinner className="text-brand" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-danger p-6">{error}</p>;
  }

  if (!data) return null;

  const store = data;

  return (
    <div className="p-6 space-y-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold text-text-primary">{store.name}</h1>
        <StatusBadge status={store.status} />
        <ProviderBadge provider={store.provider} />
        <button
          type="button"
          onClick={() => setShowRefreshDialog(true)}
          className="ml-auto px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
        >
          Refresh dependent ExternalSecrets
        </button>
      </div>

      {showRefreshDialog && (
        <ESOBulkRefreshDialog
          action="refresh_store"
          target={{ namespace, name }}
          onClose={() => setShowRefreshDialog(false)}
        />
      )}

      <div role="tablist" className="flex gap-1 border-b border-border-primary">
        {TABS.map(({ key, label }) => {
          const active = activeTab === key;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => {
                setActiveTab(key);
                if (isEvidenceTab(key)) setEvidenceTab(key);
              }}
              className={`px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
                active
                  ? "border-brand text-text-primary"
                  : "border-transparent text-text-muted hover:text-text-primary"
              }`}
            >
              {label}
            </button>
          );
        })}
      </div>

      {activeTab === "overview" && (
        <div role="tabpanel" className="space-y-4">
          <div className="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 className="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt className="text-text-muted">Namespace</dt>
                <dd className="text-text-primary">
                  {store.namespace || EM_DASH}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Name</dt>
                <dd className="text-text-primary">{store.name}</dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="text-text-muted">UID</dt>
                <dd className="text-text-primary font-mono text-xs">
                  {store.uid}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Status</dt>
                <dd>
                  <StatusBadge status={store.status} />
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Ready</dt>
                <dd className="text-text-primary">
                  {store.ready ? "Yes" : "No"}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Provider</dt>
                <dd>
                  <ProviderBadge provider={store.provider} />
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Scope</dt>
                <dd className="text-text-primary">{store.scope}</dd>
              </div>
              {store.readyReason && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Ready Reason</dt>
                  <dd className="text-text-primary">{store.readyReason}</dd>
                </div>
              )}
              {store.readyMessage && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Ready Message</dt>
                  <dd className="text-text-secondary">{store.readyMessage}</dd>
                </div>
              )}
            </dl>
            <p className="mt-4 text-sm text-text-muted">
              Reconciliation history is collected per ExternalSecret. Open an
              ExternalSecret that references this store to see its sync
              attempts.
            </p>
          </div>

          <ESOStoreMetricsPanel
            namespace={namespace}
            name={name}
            scope="Namespaced"
          />

          {store.providerSpec && Object.keys(store.providerSpec).length > 0 && (
            <div className="rounded-lg border border-border-primary bg-elevated p-5">
              <h2 className="text-sm font-semibold text-text-primary mb-1">
                Provider spec
              </h2>
              <p className="text-xs text-text-muted mb-3">
                Read-only infrastructure addressing — never credentials.
              </p>
              <pre className="text-xs font-mono text-text-primary bg-base border border-border-subtle rounded p-3 overflow-x-auto">
                {JSON.stringify(store.providerSpec, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}

      {evidenceTab && (
        <div hidden={!isEvidenceTab(activeTab)}>
          {/* Route params, not store.namespace: that field is optional on the
              shared SecretStore type, and this page is always namespaced. */}
          <ESOEvidencePanel
            kind="secretstores"
            namespace={namespace}
            name={name}
            uid={store.uid}
            activeTab={evidenceTab}
          />
        </div>
      )}

      {activeTab === "chain" && (
        <ESOChainPanel namespace={store.namespace} focusedNodeId={store.uid} />
      )}
    </div>
  );
}
