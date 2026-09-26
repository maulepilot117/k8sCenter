import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { ESODriftIndicator } from "@/components/eso/ESODriftIndicator.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError } from "@/lib/api.ts";
import { clusterEpoch } from "@/lib/cluster.ts";
import { esoApi } from "@/lib/eso-api.ts";
import { type EvidenceTabKey, evidenceTabsFor } from "@/lib/eso-evidence.ts";
import {
  createRefreshPoller,
  describeObserver,
  INITIAL_OBSERVER_STATE,
  isTerminal,
  type ObserverEvent,
  type ObserverState,
  type ObserverTone,
  type RefreshPoller,
  reduceObserver,
} from "@/lib/eso-refresh-observer.ts";
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

const OBSERVER_TONE: Record<ObserverTone, string> = {
  info: "text-text-primary bg-base border-border-subtle",
  success: "text-success bg-success/10 border-success",
  danger: "text-danger border-danger",
  muted: "text-text-muted bg-base border-border-subtle",
  warning: "text-warning bg-warning/10 border-warning/30",
};

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
  // The request's own failure (409, 403, …). The outcome of an accepted
  // request is the observer's to report.
  const forceSyncMsg = useSignal<string | null>(null);
  // Bumped when the observer sees the outcome, so the kept-mounted evidence
  // panel remounts and History shows the attempt the request produced.
  const evidenceEpoch = useSignal(0);
  const observer = useSignal<ObserverState>(INITIAL_OBSERVER_STATE);
  const dispatch = (e: ObserverEvent): ObserverState => {
    const next = reduceObserver(observer.value, e);
    if (next === observer.value) return next;
    observer.value = next;
    if (isTerminal(next.phase)) {
      poller.stop();
      if (
        next.phase === "observedSuccess" ||
        next.phase === "observedFailure"
      ) {
        evidenceEpoch.value++;
      }
    }
    return next;
  };

  // The poll schedule, the 90 s deadline and per-poll aborts live in the
  // poller; this island only supplies the real clock and the ES read.
  const pollerRef = useRef<RefreshPoller | null>(null);
  pollerRef.current ??= createRefreshPoller({
    fetchSample: async (signal) => {
      const res = await esoApi.getExternalSecret(namespace, name, signal);
      const sample = res.data;
      // Keep the overview live, but never swap in a replaced object.
      if (sample && sample.uid === observer.value.baseline?.uid) {
        data.value = sample;
      }
      return sample;
    },
    statusOf: (err) => (err instanceof ApiError ? err.status : undefined),
    dispatch: (e) => dispatch(e),
    now: Date.now,
    setTimer: (fn, ms) => setTimeout(fn, ms),
    clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
  });
  const poller = pollerRef.current;

  // Navigation away: stop timers and in-flight polls without writing state.
  useEffect(() => poller.stop, [poller]);

  // A cluster switch makes the pending observation meaningless. The switcher
  // reloads the page today, so this is the guard for any switch that doesn't;
  // the epoch catches A → B → A, which an id comparison would miss.
  const epoch = clusterEpoch.value;
  const observedEpoch = useRef(epoch);
  useEffect(() => {
    if (epoch === observedEpoch.current) return;
    observedEpoch.current = epoch;
    poller.stop();
    dispatch({ type: "clusterChanged" });
  }, [epoch]);

  const onForceSync = async () => {
    // Synchronous guard: a double click lands before the disabled attribute
    // renders, and a second request would restart an observation mid-wait.
    if (forceSyncing.value || poller.running) return;
    forceSyncing.value = true;
    forceSyncMsg.value = null;
    dispatch({ type: "request" });
    try {
      const res = await esoApi.forceSyncExternalSecret(namespace, name);
      const accepted = res.data;
      if (accepted?.baseline && accepted.correlation) {
        dispatch({
          type: "accepted",
          baseline: accepted.baseline,
          correlation: accepted.correlation,
          priorStatus: data.value?.status,
          nowMs: Date.now(),
        });
        poller.begin(observer.value);
      } else {
        // A backend without the U19a baseline: acceptance is all we know.
        dispatch({ type: "requestFailed" });
        forceSyncMsg.value = "Force-sync requested.";
      }
    } catch (err) {
      dispatch({ type: "requestFailed" });
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
  const observation = describeObserver(observer.value);
  const observing =
    observer.value.phase === "requested" ||
    observer.value.phase === "awaitingObservation";
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
          disabled={forceSyncing.value || observing}
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

      {observation && (
        <p
          role="status"
          data-observer-phase={observer.value.phase}
          class={`text-sm border rounded px-3 py-2 ${OBSERVER_TONE[observation.tone]}`}
        >
          {observation.text}
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
