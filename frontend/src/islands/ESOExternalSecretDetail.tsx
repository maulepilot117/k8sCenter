import { useCallback, useEffect, useRef, useState } from "react";
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
  isObserving,
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
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

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
  const isClient = useIsClient();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [data, setData] = useState<ExternalSecret | null>(null);
  const dataRef = useLatest(data);
  const target = useLatest({ namespace, name });
  const [activeTab, setActiveTab] = useState<TabKey>("overview");
  // The last evidence tab opened. The panel stays mounted (hidden) once one
  // has been opened, so returning from Overview or Chain keeps what it loaded.
  const [evidenceTab, setEvidenceTab] = useState<EvidenceTabKey | null>(null);
  const [forceSyncing, setForceSyncing, forceSyncingRef] = useStateRef(false);
  // The request's own failure (409, 403, …). The outcome of an accepted
  // request is the observer's to report.
  const [forceSyncMsg, setForceSyncMsg] = useState<string | null>(null);
  // Bumped when the observer sees the outcome, so the kept-mounted evidence
  // panel remounts and History shows the attempt the request produced.
  const [evidenceEpoch, setEvidenceEpoch] = useState(0);
  const [observer, setObserver, observerRef] = useStateRef<ObserverState>(
    INITIAL_OBSERVER_STATE,
  );
  const pollerRef = useRef<RefreshPoller | null>(null);
  const dispatch = useCallback((e: ObserverEvent): ObserverState => {
    const next = reduceObserver(observerRef.current, e);
    if (next === observerRef.current) return next;
    setObserver(next);
    if (isTerminal(next.phase)) {
      pollerRef.current?.stop();
      if (
        next.phase === "observedSuccess" ||
        next.phase === "observedFailure"
      ) {
        setEvidenceEpoch((n) => n + 1);
      }
    }
    return next;
  }, []);

  // The poll schedule, the 90 s deadline and per-poll aborts live in the
  // poller; this island only supplies the real clock and the ES read.
  pollerRef.current ??= createRefreshPoller({
    fetchSample: async (signal) => {
      const res = await esoApi.getExternalSecret(
        target.current.namespace,
        target.current.name,
        signal,
      );
      const sample = res.data;
      // Keep the overview live, but never swap in a replaced object.
      if (sample && sample.uid === observerRef.current.baseline?.uid) {
        setData(sample);
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
  const epoch = useSignalValue(clusterEpoch);
  const observedEpoch = useRef(epoch);
  useEffect(() => {
    if (epoch === observedEpoch.current) return;
    observedEpoch.current = epoch;
    poller.stop();
    dispatch({ type: "clusterChanged" });
  }, [epoch, poller, dispatch]);

  const onForceSync = async () => {
    // Synchronous guard: a double click lands before the disabled attribute
    // renders, and a second request would restart an observation mid-wait.
    if (forceSyncingRef.current || poller.running) return;
    setForceSyncing(true);
    setForceSyncMsg(null);
    dispatch({ type: "request" });
    try {
      const res = await esoApi.forceSyncExternalSecret(namespace, name);
      const accepted = res.data;
      if (accepted?.baseline && accepted.correlation) {
        dispatch({
          type: "accepted",
          baseline: accepted.baseline,
          correlation: accepted.correlation,
          priorStatus: dataRef.current?.status,
          nowMs: Date.now(),
        });
        poller.begin(observerRef.current);
      } else {
        // A backend without the baseline: acceptance is all we know.
        dispatch({ type: "requestFailed" });
        setForceSyncMsg("Force-sync requested.");
      }
    } catch (err) {
      dispatch({ type: "requestFailed" });
      if (err instanceof ApiError) {
        const reason = err.body?.error?.reason as string | undefined;
        if (err.status === 409 && reason === "already_refreshing") {
          setForceSyncMsg("Already refreshing — try again in a minute.");
        } else if (err.status === 403) {
          setForceSyncMsg("Access denied.");
        } else {
          setForceSyncMsg(err.detail ?? "Force-sync failed.");
        }
      } else {
        setForceSyncMsg("Force-sync failed.");
      }
    } finally {
      setForceSyncing(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await esoApi.getExternalSecret(namespace, name);
        if (!cancelled) setData(res.data ?? null);
      } catch {
        if (!cancelled) setError("Failed to load ExternalSecret");
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

  const es = data;
  const observation = describeObserver(observer);
  const observing = isObserving(observer.phase);
  const showMessage =
    es.status === "SyncFailed" ||
    (es.readyMessage && es.readyMessage.length > 0);
  const targetSecretLink = es.targetSecretName
    ? resourceHref("secret", es.namespace, es.targetSecretName)
    : null;

  return (
    <div className="p-6 space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-bold text-text-primary">{es.name}</h1>
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
          disabled={forceSyncing || observing}
          className="ml-auto px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base disabled:opacity-50"
        >
          {forceSyncing ? "Force-syncing…" : "Force sync"}
        </button>
      </div>

      {forceSyncMsg && (
        <p className="text-sm text-text-muted bg-base border border-border-subtle rounded px-3 py-2">
          {forceSyncMsg}
        </p>
      )}

      {observation && (
        <p
          role="status"
          data-observer-phase={observer.phase}
          className={`text-sm border rounded px-3 py-2 ${OBSERVER_TONE[observation.tone]}`}
        >
          {observation.text}
        </p>
      )}

      {/* Tab strip */}
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

      {/* Tab panels */}
      {activeTab === "overview" && (
        <div role="tabpanel" className="space-y-4">
          <div className="rounded-lg border border-border-primary bg-elevated p-5">
            <h2 className="text-sm font-semibold text-text-primary mb-4">
              Details
            </h2>
            <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 text-sm">
              <div>
                <dt className="text-text-muted">Namespace</dt>
                <dd className="text-text-primary">{es.namespace}</dd>
              </div>
              <div>
                <dt className="text-text-muted">Name</dt>
                <dd className="text-text-primary">{es.name}</dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="text-text-muted">UID</dt>
                <dd className="text-text-primary font-mono text-xs">
                  {es.uid}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Store</dt>
                <dd>
                  <a
                    href={storeHref(
                      es.storeRef.kind,
                      es.namespace,
                      es.storeRef.name,
                    )}
                    className="text-brand hover:underline"
                  >
                    {es.storeRef.kind}/{es.storeRef.name}
                  </a>
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Target Secret</dt>
                <dd>
                  {es.targetSecretName ? (
                    targetSecretLink ? (
                      <a
                        href={targetSecretLink}
                        className="text-brand hover:underline"
                      >
                        {es.targetSecretName}
                      </a>
                    ) : (
                      <span className="text-text-primary">
                        {es.targetSecretName}
                      </span>
                    )
                  ) : (
                    <span className="text-text-muted">{EM_DASH}</span>
                  )}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Refresh Interval</dt>
                <dd className="text-text-primary">
                  {es.refreshInterval || EM_DASH}
                </dd>
              </div>
              <div>
                <dt className="text-text-muted">Last Sync</dt>
                <dd className="text-text-primary" title={es.lastSyncTime ?? ""}>
                  {es.lastSyncTime ? timeAgo(es.lastSyncTime) : EM_DASH}
                </dd>
              </div>
              <div className="sm:col-span-2">
                <dt className="text-text-muted">Synced ResourceVersion</dt>
                <dd className="text-text-primary font-mono text-xs">
                  {es.syncedResourceVersion || EM_DASH}
                </dd>
              </div>
              {es.readyReason && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Ready Reason</dt>
                  <dd className="text-text-primary">{es.readyReason}</dd>
                </div>
              )}
              {showMessage && (
                <div className="sm:col-span-2">
                  <dt className="text-text-muted">Message</dt>
                  <dd className="text-text-secondary">{es.readyMessage}</dd>
                </div>
              )}
            </dl>
          </div>
        </div>
      )}

      {evidenceTab && (
        <div hidden={!isEvidenceTab(activeTab)}>
          <ESOEvidencePanel
            key={evidenceEpoch}
            kind="externalsecrets"
            namespace={es.namespace}
            name={es.name}
            uid={es.uid}
            activeTab={evidenceTab}
          />
        </div>
      )}

      {activeTab === "chain" && (
        <ESOChainPanel namespace={es.namespace} focusedNodeId={es.uid} />
      )}
    </div>
  );
}
