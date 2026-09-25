import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
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
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<SecretStore | null>(null);
  const activeTab = useSignal<TabKey>("overview");
  // The last evidence tab opened; the panel stays mounted once opened.
  const evidenceTab = useSignal<EvidenceTabKey | null>(null);
  const showRefreshDialog = useSignal(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      loading.value = true;
      error.value = null;
      try {
        const res = await esoApi.getStore(namespace, name);
        if (!cancelled) data.value = res.data ?? null;
      } catch {
        if (!cancelled) error.value = "Failed to load SecretStore";
      } finally {
        if (!cancelled) loading.value = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, name]);

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-12">
        <Spinner class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <p class="text-sm text-danger p-6">{error.value}</p>;
  }

  if (!data.value) return null;

  const store = data.value;

  return (
    <div class="p-6 space-y-6">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{store.name}</h1>
        <StatusBadge status={store.status} />
        <ProviderBadge provider={store.provider} />
        <button
          type="button"
          onClick={() => (showRefreshDialog.value = true)}
          class="ml-auto px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
        >
          Refresh dependent ExternalSecrets
        </button>
      </div>

      {showRefreshDialog.value && (
        <ESOBulkRefreshDialog
          action="refresh_store"
          target={{ namespace, name }}
          onClose={() => (showRefreshDialog.value = false)}
        />
      )}

      <div role="tablist" class="flex gap-1 border-b border-border-primary">
        {TABS.map(({ key, label }) => {
          const active = activeTab.value === key;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => {
                activeTab.value = key;
                if (isEvidenceTab(key)) evidenceTab.value = key;
              }}
              class={`px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
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

      {activeTab.value === "overview" && (
        <div role="tabpanel" class="space-y-4">
          <div class="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt class="text-text-muted">Namespace</dt>
                <dd class="text-text-primary">{store.namespace || EM_DASH}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Name</dt>
                <dd class="text-text-primary">{store.name}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="text-text-muted">UID</dt>
                <dd class="text-text-primary font-mono text-xs">{store.uid}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Status</dt>
                <dd>
                  <StatusBadge status={store.status} />
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Ready</dt>
                <dd class="text-text-primary">{store.ready ? "Yes" : "No"}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Provider</dt>
                <dd>
                  <ProviderBadge provider={store.provider} />
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Scope</dt>
                <dd class="text-text-primary">{store.scope}</dd>
              </div>
              {store.readyReason && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Ready Reason</dt>
                  <dd class="text-text-primary">{store.readyReason}</dd>
                </div>
              )}
              {store.readyMessage && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Ready Message</dt>
                  <dd class="text-text-secondary">{store.readyMessage}</dd>
                </div>
              )}
            </dl>
            <p class="mt-4 text-sm text-text-muted">
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
            <div class="rounded-lg border border-border-primary bg-elevated p-5">
              <h2 class="text-sm font-semibold text-text-primary mb-1">
                Provider spec
              </h2>
              <p class="text-xs text-text-muted mb-3">
                Read-only infrastructure addressing — never credentials.
              </p>
              <pre class="text-xs font-mono text-text-primary bg-base border border-border-subtle rounded p-3 overflow-x-auto">
                {JSON.stringify(store.providerSpec, null, 2)}
              </pre>
            </div>
          )}
        </div>
      )}

      {evidenceTab.value && (
        <div hidden={!isEvidenceTab(activeTab.value)}>
          <ESOEvidencePanel
            kind="secretstores"
            namespace={namespace}
            name={name}
            uid={store.uid}
            activeTab={evidenceTab.value}
          />
        </div>
      )}

      {activeTab.value === "chain" && (
        <ESOChainPanel namespace={store.namespace} focusedNodeId={store.uid} />
      )}
    </div>
  );
}
