import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import type { ClusterExternalSecret } from "@/lib/eso-types.ts";

interface Props {
  name: string;
}

type TabKey = "overview" | "yaml" | "events" | "history" | "chain";

const EM_DASH = "—";

function storeHref(kind: string, name: string): string {
  if (kind === "ClusterSecretStore") {
    return `/external-secrets/cluster-stores/${encodeURIComponent(name)}`;
  }
  // Namespaced store referenced from a cluster-scoped resource — link to
  // the list view since we don't know the namespace at this point.
  return "/external-secrets/stores";
}

export default function ESOClusterExternalSecretDetail({ name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<ClusterExternalSecret | null>(null);
  const activeTab = useSignal<TabKey>("overview");

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      loading.value = true;
      error.value = null;
      try {
        const res = await esoApi.getClusterExternalSecret(name);
        if (!cancelled) data.value = res.data ?? null;
      } catch {
        if (!cancelled) error.value = "Failed to load ClusterExternalSecret";
      } finally {
        if (!cancelled) loading.value = false;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [name]);

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

  const ces = data.value;
  const showMessage = ces.status === "SyncFailed" ||
    (ces.readyMessage && ces.readyMessage.length > 0);

  return (
    <div class="p-6 space-y-6">
      {/* Header */}
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{ces.name}</h1>
        <StatusBadge status={ces.status} />
        <span class="text-xs text-text-muted">Cluster-scoped</span>
      </div>

      {/* Tab strip */}
      <div role="tablist" class="flex gap-1 border-b border-border-primary">
        {(
          [
            ["overview", "Overview"],
            ["yaml", "YAML"],
            ["events", "Events"],
            ["history", "History"],
            ["chain", "Chain"],
          ] as Array<[TabKey, string]>
        ).map(([key, label]) => {
          const active = activeTab.value === key;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => (activeTab.value = key)}
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
          <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt class="text-text-muted">Name</dt>
                <dd class="text-text-primary">{ces.name}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="text-text-muted">UID</dt>
                <dd class="text-text-primary font-mono text-xs">{ces.uid}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Store</dt>
                <dd>
                  <a
                    href={storeHref(ces.storeRef.kind, ces.storeRef.name)}
                    class="text-brand hover:underline"
                  >
                    {ces.storeRef.kind}/{ces.storeRef.name}
                  </a>
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Target Secret</dt>
                <dd class="text-text-primary">
                  {ces.targetSecretName || EM_DASH}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Refresh Interval</dt>
                <dd class="text-text-primary">
                  {ces.refreshInterval || EM_DASH}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">External Secret Base Name</dt>
                <dd class="text-text-primary">
                  {ces.externalSecretBaseName || EM_DASH}
                </dd>
              </div>
              {ces.readyReason && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Ready Reason</dt>
                  <dd class="text-text-primary">{ces.readyReason}</dd>
                </div>
              )}
              {showMessage && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Message</dt>
                  <dd class="text-text-secondary">{ces.readyMessage}</dd>
                </div>
              )}
            </dl>
          </div>

          {/* Namespace selectors */}
          <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-3">
              Namespace selectors
            </h2>
            {ces.namespaceSelectors && ces.namespaceSelectors.length > 0
              ? (
                <div class="flex flex-wrap gap-1.5">
                  {ces.namespaceSelectors.map((s) => (
                    <span
                      key={s}
                      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-mono text-text-primary bg-bg-base border border-border-subtle"
                    >
                      {s}
                    </span>
                  ))}
                </div>
              )
              : (
                <p class="text-sm text-text-muted">
                  No selectors set — using static namespace list.
                </p>
              )}
            {ces.namespaces && ces.namespaces.length > 0 && (
              <div class="mt-4">
                <h3 class="text-xs font-semibold text-text-muted mb-2">
                  Static namespace list
                </h3>
                <ul class="text-sm text-text-primary list-disc list-inside space-y-0.5">
                  {ces.namespaces.map((ns) => <li key={ns}>{ns}</li>)}
                </ul>
              </div>
            )}
          </div>

          {/* Provisioned / failed namespaces */}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
              <h2 class="text-sm font-semibold text-text-primary mb-3">
                Provisioned namespaces ({(ces.provisionedNamespaces ?? [])
                  .length})
              </h2>
              {ces.provisionedNamespaces && ces.provisionedNamespaces.length > 0
                ? (
                  <ul class="text-sm text-text-primary list-disc list-inside space-y-0.5">
                    {ces.provisionedNamespaces.map((ns) => (
                      <li key={ns}>{ns}</li>
                    ))}
                  </ul>
                )
                : <p class="text-sm text-text-muted">None.</p>}
            </div>
            <div class="rounded-lg border border-border-primary bg-bg-elevated p-5">
              <h2 class="text-sm font-semibold text-text-primary mb-3">
                Failed namespaces ({(ces.failedNamespaces ?? []).length})
              </h2>
              {ces.failedNamespaces && ces.failedNamespaces.length > 0
                ? (
                  <ul class="text-sm text-danger list-disc list-inside space-y-0.5">
                    {ces.failedNamespaces.map((ns) => <li key={ns}>{ns}</li>)}
                  </ul>
                )
                : <p class="text-sm text-text-muted">None.</p>}
            </div>
          </div>
        </div>
      )}

      {activeTab.value === "yaml" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-bg-elevated p-5 text-sm text-text-muted"
        >
          YAML editor coming in Phase&nbsp;B.
        </div>
      )}

      {activeTab.value === "events" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-bg-elevated p-5 text-sm text-text-muted"
        >
          Events feed coming in Phase&nbsp;B.
        </div>
      )}

      {activeTab.value === "history" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-bg-elevated p-5 text-sm text-text-muted"
        >
          History timeline coming in Phase&nbsp;C.
        </div>
      )}

      {activeTab.value === "chain" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-bg-elevated p-5 text-sm text-text-muted"
        >
          Chain visualization coming in Phase&nbsp;I.
        </div>
      )}
    </div>
  );
}
