import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { esoApi } from "@/lib/eso-api.ts";
import { resourceHref } from "@/lib/k8s-links.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import type { PushSecret } from "@/lib/eso-types.ts";

interface Props {
  namespace: string;
  name: string;
}

type TabKey = "overview" | "yaml" | "events" | "history" | "chain";

const EM_DASH = "—";

function storeHref(kind: string, namespace: string, name: string): string {
  if (kind === "ClusterSecretStore") {
    return `/external-secrets/cluster-stores/${encodeURIComponent(name)}`;
  }
  return `/external-secrets/stores/${encodeURIComponent(namespace)}/${
    encodeURIComponent(name)
  }`;
}

export default function ESOPushSecretDetail({ namespace, name }: Props) {
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const data = useSignal<PushSecret | null>(null);
  const activeTab = useSignal<TabKey>("overview");

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      loading.value = true;
      error.value = null;
      try {
        const res = await esoApi.getPushSecret(namespace, name);
        if (!cancelled) data.value = res.data ?? null;
      } catch {
        if (!cancelled) error.value = "Failed to load PushSecret";
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

  const ps = data.value;
  const showMessage = ps.status === "SyncFailed" ||
    (ps.readyMessage && ps.readyMessage.length > 0);
  const sourceSecretLink = ps.sourceSecretName
    ? resourceHref("secret", ps.namespace, ps.sourceSecretName)
    : null;

  return (
    <div class="p-6 space-y-6">
      <div class="flex flex-wrap items-center gap-3">
        <h1 class="text-2xl font-bold text-text-primary">{ps.name}</h1>
        <StatusBadge status={ps.status} />
      </div>

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
          <div class="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt class="text-text-muted">Namespace</dt>
                <dd class="text-text-primary">{ps.namespace}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Name</dt>
                <dd class="text-text-primary">{ps.name}</dd>
              </div>
              <div class="sm:col-span-2">
                <dt class="text-text-muted">UID</dt>
                <dd class="text-text-primary font-mono text-xs">{ps.uid}</dd>
              </div>
              <div>
                <dt class="text-text-muted">Source Secret</dt>
                <dd>
                  {ps.sourceSecretName
                    ? (sourceSecretLink
                      ? (
                        <a
                          href={sourceSecretLink}
                          class="text-brand hover:underline"
                        >
                          {ps.sourceSecretName}
                        </a>
                      )
                      : (
                        <span class="text-text-primary">
                          {ps.sourceSecretName}
                        </span>
                      ))
                    : <span class="text-text-muted">{EM_DASH}</span>}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Refresh Interval</dt>
                <dd class="text-text-primary">
                  {ps.refreshInterval || EM_DASH}
                </dd>
              </div>
              <div>
                <dt class="text-text-muted">Last Sync</dt>
                <dd
                  class="text-text-primary"
                  title={ps.lastSyncTime ?? ""}
                >
                  {ps.lastSyncTime ? timeAgo(ps.lastSyncTime) : EM_DASH}
                </dd>
              </div>
              {ps.readyReason && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Ready Reason</dt>
                  <dd class="text-text-primary">{ps.readyReason}</dd>
                </div>
              )}
              {showMessage && (
                <div class="sm:col-span-2">
                  <dt class="text-text-muted">Message</dt>
                  <dd class="text-text-secondary">{ps.readyMessage}</dd>
                </div>
              )}
            </dl>
          </div>

          <div class="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 class="text-sm font-semibold text-text-primary mb-3">
              Push targets ({(ps.storeRefs ?? []).length})
            </h2>
            {ps.storeRefs && ps.storeRefs.length > 0
              ? (
                <ul class="text-sm space-y-1">
                  {ps.storeRefs.map((s) => (
                    <li key={`${s.kind}/${s.name}`}>
                      <a
                        href={storeHref(s.kind, ps.namespace, s.name)}
                        class="text-brand hover:underline"
                      >
                        {s.kind}/{s.name}
                      </a>
                    </li>
                  ))}
                </ul>
              )
              : <p class="text-sm text-text-muted">No store references.</p>}
          </div>
        </div>
      )}

      {activeTab.value === "yaml" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
        >
          YAML editor coming in Phase&nbsp;B.
        </div>
      )}

      {activeTab.value === "events" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
        >
          Events feed coming in Phase&nbsp;B.
        </div>
      )}

      {activeTab.value === "history" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
        >
          History timeline coming in Phase&nbsp;C.
        </div>
      )}

      {activeTab.value === "chain" && (
        <div
          role="tabpanel"
          class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
        >
          Chain visualization coming in Phase&nbsp;I.
        </div>
      )}
    </div>
  );
}
