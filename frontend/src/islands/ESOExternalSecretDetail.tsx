import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESODriftIndicator } from "@/components/eso/ESODriftIndicator.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import { type EvidenceTabKey, evidenceTabsFor } from "@/lib/eso-evidence.ts";
import type { ExternalSecret } from "@/lib/eso-types.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import ESOChainPanel from "@/src/islands/ESOChainPanel.tsx";
import ESOEvidencePanel from "@/src/islands/ESOEvidencePanel.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface Props {
  namespace: string;
  name: string;
}

type TabKey = "overview" | EvidenceTabKey | "chain";

// The evidence tabs come from the D5 matrix, so this strip cannot offer a tab
// the panel would not render.
const TABS: Array<{ key: TabKey; label: string }> = [
  { key: "overview", label: "Overview" },
  ...evidenceTabsFor("externalsecrets"),
  { key: "chain", label: "Chain" },
];

function isEvidenceTab(tab: TabKey): tab is EvidenceTabKey {
  return tab !== "overview" && tab !== "chain";
}

const EM_DASH = "—";

function storeHref(kind: string, namespace: string, name: string): string {
  if (kind === "ClusterSecretStore") {
    return `/external-secrets/cluster-stores/${encodeURIComponent(name)}`;
  }
  return `/external-secrets/stores/${encodeURIComponent(namespace)}/${encodeURIComponent(
    name,
  )}`;
}

export default function ESOExternalSecretDetail({ namespace, name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<ExternalSecret | null>(null);
  const activeTab = useSignal<TabKey>("overview");
  // The last evidence tab opened. The panel stays mounted (hidden) once one
  // has been opened, so returning from Overview or Chain keeps what it loaded.
  const evidenceTab = useSignal<EvidenceTabKey | null>(null);
  const forceSyncing = useSignal(false);
  const forceSyncMsg = useSignal<string | null>(null);
  // Bumped on a successful force-sync so the kept-mounted evidence panel
  // remounts and reloads the open tab instead of showing pre-sync evidence.
  const evidenceEpoch = useSignal(0);

  const onForceSync = async () => {
    forceSyncing.value = true;
    forceSyncMsg.value = null;
    try {
      await esoApi.forceSyncExternalSecret(namespace, name);
      forceSyncMsg.value = "Force-sync requested.";
      evidenceEpoch.value++;
    } catch (err) {
      if (err instanceof ApiError) {
        const reason = err.body?.error?.reason as string | undefined;
        if (err.status === 409 && reason === "already_refreshing") {
          forceSyncMsg.value = "Already refreshing — try again in a minute.";
        } else if (err.status === 403) {
          forceSyncMsg.value = "Access denied.";
        } else {
          forceSyncMsg.value = err.detail ?? "Force-sync failed.";
        }
      } else {
        forceSyncMsg.value = "Force-sync failed.";
      }
    } finally {
      forceSyncing.value = false;
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      loading.value = true;
      error.value = null;
      try {
        const res = await esoApi.getExternalSecret(namespace, name);
        if (!cancelled) data.value = res.data ?? null;
      } catch {
        if (!cancelled) error.value = "Failed to load ExternalSecret";
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

  const es = data.value;
  const showMessage =
    es.status === "SyncFailed" ||
    (es.readyMessage && es.readyMessage.length > 0);
  const targetSecretLink = es.targetSecretName
    ? resourceHref("secret", es.namespace, es.targetSecretName)
    : null;

  return (
    <div class="p-6 space-y-6">
      {/* Header */}
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{es.name}</h1>
        <StatusBadge status={es.status} />
        {es.driftStatus && (
          <ESODriftIndicator
            status={es.driftStatus}
            reason={es.driftUnknownReason}
          />
        )}
        <button
          type="button"
          onClick={onForceSync}
          disabled={forceSyncing.value}
          class="ml-auto px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base disabled:opacity-50"
        >
          {forceSyncing.value ? "Force-syncing…" : "Force sync"}
        </button>
      </div>

      {forceSyncMsg.value && (
        <p class="text-sm text-text-muted bg-base border border-border-subtle rounded px-3 py-2">
          {forceSyncMsg.value}
        </p>
      )}

      {/* Tab strip */}
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

      {/* Tab panels */}
      {activeTab.value === "overview" && (
        <div role="tabpanel" class="space-y-4">
          <div class="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt class="text-text-muted">Namespace</dt>
                <dd class="text-text-primary">{es.namespace}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Name</dt>
                <dd class="text-text-primary">{es.name}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="text-text-muted">UID</dt>
                <dd class="text-text-primary font-mono text-xs">{es.uid}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Store</dt>
                <dd>
                  <a
                    href={storeHref(
                      es.storeRef.kind,
                      es.namespace,
                      es.storeRef.name,
                    )}
                    class="text-brand hover:underline"
                  >
                    {es.storeRef.kind}/{es.storeRef.name}
                  </a>
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Target Secret</dt>
                <dd>
                  {es.targetSecretName ? (
                    targetSecretLink ? (
                      <a
                        href={targetSecretLink}
                        class="text-brand hover:underline"
                      >
                        {es.targetSecretName}
                      </a>
                    ) : (
                      <span class="text-text-primary">
                        {es.targetSecretName}
                      </span>
                    )
                  ) : (
                    <span class="text-text-muted">{EM_DASH}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Refresh Interval</dt>
                <dd class="text-text-primary">
                  {es.refreshInterval || EM_DASH}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Last Sync</dt>
                <dd class="text-text-primary" title={es.lastSyncTime ?? ""}>
                  {es.lastSyncTime ? timeAgo(es.lastSyncTime) : EM_DASH}
                </dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="text-text-muted">Synced ResourceVersion</dt>
                <dd class="text-text-primary font-mono text-xs">
                  {es.syncedResourceVersion || EM_DASH}
                </dd>
              </div>
              {es.readyReason && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Ready Reason</dt>
                  <dd class="text-text-primary">{es.readyReason}</dd>
                </div>
              )}
              {showMessage && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Message</dt>
                  <dd class="text-text-secondary">{es.readyMessage}</dd>
                </div>
              )}
            </dl>
          </div>
        </div>
      )}

      {evidenceTab.value && (
        <div hidden={!isEvidenceTab(activeTab.value)}>
          <ESOEvidencePanel
            key={evidenceEpoch.value}
            kind="externalsecrets"
            namespace={es.namespace}
            name={es.name}
            uid={es.uid}
            activeTab={evidenceTab.value}
          />
        </div>
      )}

      {activeTab.value === "chain" && (
        <ESOChainPanel namespace={es.namespace} focusedNodeId={es.uid} />
      )}
    </div>
  );
}
