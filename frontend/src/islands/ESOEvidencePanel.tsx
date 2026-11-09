/** @jsxImportSource react */
import { type ReactNode, useEffect, useRef } from "react";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { Tabs } from "@/components/ui/Tabs.tsx";
import { ApiError } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import {
  classifyEvidenceError,
  describeProjection,
  type EvidenceTabKey,
  evidenceTabsFor,
  generatedExternalSecrets,
  isStaleResponse,
  mergeHistoryPages,
  redactEntriesTo,
  unavailableMessage,
} from "@/lib/eso-evidence.ts";
import type {
  ClusterExternalSecret,
  EvidenceEventsResponse,
  EvidenceKind,
  EvidenceProjection,
  EvidenceUnavailableReason,
  HistoryEntry,
} from "@/lib/eso-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

interface Props {
  kind: EvidenceKind;
  /** null for the two cluster-scoped kinds. */
  namespace: string | null;
  name: string;
  /** The live object's UID; a response for any other UID is discarded. */
  uid: string;
  /** Uncontrolled: the tab the panel's own strip opens on. */
  initialTab?: EvidenceTabKey;
  /**
   * Controlled: a host page that owns its own tab strip (a detail page with
   * Overview and Chain beside the evidence tabs) passes the selected evidence
   * tab here. The panel then renders only that tab's body, with no strip of
   * its own, and loads it on first selection. A tab the kind does not
   * support renders nothing.
   */
  activeTab?: EvidenceTabKey;
  /** ClusterExternalSecret only: the source of its "Generated ExternalSecrets". */
  clusterExternalSecret?: Pick<
    ClusterExternalSecret,
    | "name"
    | "externalSecretBaseName"
    | "provisionedNamespaces"
    | "failedNamespaces"
  >;
}

/** One tab's load state. "unavailable" is never rendered as empty. */
type Load<T> =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; data: T }
  | {
      state: "unavailable";
      reason: EvidenceUnavailableReason;
      detail?: string;
    };

interface HistoryState {
  projection: EvidenceProjection;
  entries: HistoryEntry[];
  next?: string;
  loadingMore: boolean;
  moreError?: EvidenceUnavailableReason;
}

const HISTORY_PAGE_SIZE = 50;

function unavailable(err: unknown): Load<never> {
  const reason = classifyEvidenceError(err);
  // The 403 for events names the grant that is missing; that server-authored
  // detail tells the operator what to ask for.
  const detail =
    err instanceof ApiError && err.reason === "events_forbidden"
      ? err.detail
      : undefined;
  return { state: "unavailable", reason, detail };
}

/**
 * The YAML / Events / History evidence for one ESO object. One island serves
 * all five detail pages: it renders only the tabs `evidenceTabsFor(kind)`
 * returns, loads each tab on first view, and keeps every failure distinct
 * from an empty result.
 */
export default function ESOEvidencePanel(props: Props) {
  const { kind, namespace, name, uid } = props;
  const tabs = evidenceTabsFor(kind);
  const firstTab = tabs.some((t) => t.key === props.initialTab)
    ? (props.initialTab as EvidenceTabKey)
    : tabs[0].key;

  // The loaders and the effects below read these between renders (the reset
  // effect and the load effect run in one flush, and a response lands later),
  // so they are state with a ref that is current at once.
  const [active, setActive, activeRef] = useStateRef<EvidenceTabKey>(firstTab);
  const [yaml, setYaml, yamlRef] = useStateRef<Load<string>>({
    state: "idle",
  });
  const [events, setEvents, eventsRef] = useStateRef<
    Load<EvidenceEventsResponse>
  >({ state: "idle" });
  const [history, setHistory, historyRef] = useStateRef<Load<HistoryState>>({
    state: "idle",
  });

  // Which object the panel currently shows. A response started for an earlier
  // target compares unequal and is dropped even if it resolves last.
  const targetKey = `${kind}/${namespace ?? "_"}/${name}/${uid}`;
  const currentTarget = useRef(targetKey);
  const controllers = useRef(new Map<EvidenceTabKey, AbortController>());

  const supported = (tab: EvidenceTabKey | undefined) =>
    tab !== undefined && tabs.some((t) => t.key === tab);
  const controlled = props.activeTab !== undefined;
  const shown: EvidenceTabKey | null = controlled
    ? supported(props.activeTab)
      ? (props.activeTab as EvidenceTabKey)
      : null
    : active;

  function begin(tab: EvidenceTabKey): { signal: AbortSignal; target: string } {
    controllers.current.get(tab)?.abort();
    const c = new AbortController();
    controllers.current.set(tab, c);
    return { signal: c.signal, target: currentTarget.current };
  }

  function stillCurrent(signal: AbortSignal, target: string): boolean {
    return !signal.aborted && target === currentTarget.current;
  }

  function loadYaml() {
    const { signal, target } = begin("yaml");
    setYaml({ state: "loading" });
    esoApi
      .exportEvidenceYaml(kind, namespace, name, uid, signal)
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        setYaml({ state: "ready", data: String(res.data ?? "") });
      })
      .catch((err) => {
        if (stillCurrent(signal, target)) setYaml(unavailable(err));
      });
  }

  function loadEvents() {
    const { signal, target } = begin("events");
    setEvents({ state: "loading" });
    esoApi
      .getEvidenceEvents(kind, namespace, name, signal)
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        // The server resolved this name to a different object: it was
        // deleted and recreated. Say so rather than leave the tab loading.
        setEvents(
          isStaleResponse(uid, res.data?.uid)
            ? { state: "unavailable", reason: "replaced" }
            : { state: "ready", data: res.data },
        );
      })
      .catch((err) => {
        if (stillCurrent(signal, target)) setEvents(unavailable(err));
      });
  }

  function loadHistory(more: boolean) {
    const current = historyRef.current;
    const prev = current.state === "ready" ? current.data : null;
    if (more && !prev?.next) return;
    const { signal, target } = begin("history");
    if (more && prev) {
      setHistory({
        state: "ready",
        data: { ...prev, loadingMore: true, moreError: undefined },
      });
    } else {
      setHistory({ state: "loading" });
    }
    esoApi
      .getExternalSecretHistory(namespace ?? "", name, {
        limit: HISTORY_PAGE_SIZE,
        cursor: more ? prev?.next : undefined,
        signal,
      })
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        if (isStaleResponse(uid, res.data?.uid)) {
          setHistory(
            more && prev
              ? {
                  state: "ready",
                  data: { ...prev, loadingMore: false, moreError: "replaced" },
                }
              : { state: "unavailable", reason: "replaced" },
          );
          return;
        }
        const page = res.data;
        setHistory({
          state: "ready",
          data: {
            projection: page.projection,
            // Rows from earlier pages are shown under this page's projection,
            // so they are reduced to it if Secret read was revoked meanwhile.
            entries: redactEntriesTo(
              more && prev
                ? mergeHistoryPages(prev.entries, page.entries)
                : mergeHistoryPages([], page.entries),
              page.projection,
            ),
            next: res.metadata?.continue || undefined,
            loadingMore: false,
          },
        });
      })
      .catch((err) => {
        if (!stillCurrent(signal, target)) return;
        if (more && prev) {
          // Rows already shown stay; the failed page is reported beside them.
          setHistory({
            state: "ready",
            data: {
              ...prev,
              loadingMore: false,
              moreError: classifyEvidenceError(err),
            },
          });
        } else {
          setHistory(unavailable(err));
        }
      });
  }

  function ensureLoaded(tab: EvidenceTabKey) {
    if (tab === "yaml" && yamlRef.current.state === "idle") loadYaml();
    if (tab === "events" && eventsRef.current.state === "idle") loadEvents();
    if (tab === "history" && historyRef.current.state === "idle") {
      loadHistory(false);
    }
  }

  // A new target discards everything shown for the old one and aborts its
  // in-flight requests; unmount aborts them too.
  const latest = useLatest({ supported, tabs, ensureLoaded });
  useEffect(() => {
    currentTarget.current = targetKey;
    setYaml({ state: "idle" });
    setEvents({ state: "idle" });
    setHistory({ state: "idle" });
    if (!latest.current.supported(activeRef.current)) {
      setActive(latest.current.tabs[0].key);
    }
    const inFlight = controllers.current;
    return () => {
      for (const c of inFlight.values()) c.abort();
      inFlight.clear();
    };
  }, [targetKey]);

  // Load whatever tab is shown, on first view and after a target change. It
  // runs after the reset above, which is declared first.
  // biome-ignore lint/correctness/useExhaustiveDependencies: targetKey is the trigger: a new target must reload the shown tab after the reset above
  useEffect(() => {
    if (shown) latest.current.ensureLoaded(shown);
  }, [targetKey, shown, latest]);

  function renderTab(tab: EvidenceTabKey) {
    switch (tab) {
      case "yaml":
        return (
          <TabBody load={yaml} retry={loadYaml}>
            {(text) => (
              <pre className="max-h-[32rem] overflow-auto rounded-lg border border-border-primary bg-base p-4 font-mono text-xs text-text-primary">
                {text}
              </pre>
            )}
          </TabBody>
        );
      case "events":
        return (
          <TabBody load={events} retry={loadEvents}>
            {(data) => <EventsView data={data} />}
          </TabBody>
        );
      case "history":
        return (
          <TabBody load={history} retry={() => loadHistory(false)}>
            {(data) => (
              <HistoryView data={data} loadMore={() => loadHistory(true)} />
            )}
          </TabBody>
        );
      case "generated":
        return <GeneratedView ces={props.clusterExternalSecret} />;
    }
  }

  if (controlled) {
    return shown ? <div role="tabpanel">{renderTab(shown)}</div> : null;
  }

  return (
    <Tabs
      tabs={tabs.map(({ key, label }) => ({
        id: key,
        label,
        content: () => <div className="pt-4">{renderTab(key)}</div>,
      }))}
      activeTab={active}
      onTabChange={(id) => {
        setActive(id as EvidenceTabKey);
      }}
    />
  );
}

function TabBody<T>(p: {
  load: Load<T>;
  retry: () => void;
  children: (data: T) => ReactNode;
}) {
  const l = p.load;
  if (l.state === "idle" || l.state === "loading") {
    return (
      <div
        className="flex justify-center py-8"
        role="status"
        aria-label="Loading"
      >
        <Spinner />
      </div>
    );
  }
  if (l.state === "unavailable") {
    return <Unavailable reason={l.reason} detail={l.detail} retry={p.retry} />;
  }
  return <>{p.children(l.data)}</>;
}

function Unavailable(p: {
  reason: EvidenceUnavailableReason;
  detail?: string;
  retry?: () => void;
}) {
  const transient =
    p.reason === "history_unavailable" ||
    p.reason === "discovery_unavailable" ||
    p.reason === "error";
  return (
    <div
      role="status"
      data-evidence-state={p.reason}
      className="space-y-2 rounded-lg border border-border-primary bg-elevated p-5 text-sm"
    >
      <p
        className={
          p.reason === "forbidden" ? "text-warning" : "text-text-primary"
        }
      >
        {unavailableMessage(p.reason)}
      </p>
      {p.detail && <p className="text-text-muted">{p.detail}</p>}
      {transient && p.retry && (
        <button
          type="button"
          onClick={p.retry}
          className="rounded border border-border-primary px-3 py-1 text-xs text-text-primary hover:bg-base"
        >
          Try again
        </button>
      )}
    </div>
  );
}

function ProjectionNote({ projection }: { projection: EvidenceProjection }) {
  const note = describeProjection(projection);
  if (!note) return null;
  return (
    <p
      data-evidence-state="redacted"
      className="rounded border border-border-subtle bg-surface px-3 py-2 text-xs text-text-muted"
    >
      {note}
    </p>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return (
    <div
      data-evidence-state="empty"
      className="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
    >
      {children}
    </div>
  );
}

function When({ at }: { at?: string }) {
  if (!at) return <span className="text-text-muted">—</span>;
  return (
    <time dateTime={at} title={at}>
      {timeAgo(at)}
    </time>
  );
}

function EventsView({ data }: { data: EvidenceEventsResponse }) {
  return (
    <div className="space-y-3">
      <ProjectionNote projection={data.projection} />
      {data.events.length === 0 ? (
        <Empty>
          No events are recorded for this object. Kubernetes keeps events for
          about an hour, so an object that has been quiet shows none.
        </Empty>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border-primary">
          <table className="w-full text-left text-sm">
            <thead className="bg-surface text-xs text-text-muted">
              <tr>
                <th className="px-3 py-2">Type</th>
                <th className="px-3 py-2">Reason</th>
                <th className="px-3 py-2">Count</th>
                <th className="px-3 py-2">Last seen</th>
                <th className="px-3 py-2">Details</th>
              </tr>
            </thead>
            <tbody>
              {data.events.map((ev, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: events carry no id and the list is replaced whole
                <tr key={i} className="border-t border-border-subtle align-top">
                  <td
                    className={`px-3 py-2 ${ev.type === "Warning" ? "text-warning" : "text-text-primary"}`}
                  >
                    {ev.type}
                  </td>
                  <td className="px-3 py-2 text-text-primary">{ev.reason}</td>
                  <td className="px-3 py-2 text-text-muted">{ev.count}</td>
                  <td className="px-3 py-2 text-text-muted">
                    <When at={ev.lastTimestamp} />
                  </td>
                  <td className="px-3 py-2 text-text-secondary">
                    {ev.message !== undefined ? (
                      <span className="whitespace-pre-wrap break-words">
                        {ev.message}
                        {ev.messageTruncated && " …(truncated)"}
                      </span>
                    ) : (
                      <span className="text-text-muted">Hidden</span>
                    )}
                    {ev.source && (
                      <span className="block text-xs text-text-muted">
                        {ev.source}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data.truncated && (
        <p className="text-xs text-text-muted">
          Showing the newest {data.events.length} events; older events for this
          object are not shown.
        </p>
      )}
    </div>
  );
}

function outcomeClass(outcome: string): string {
  if (outcome === "success") return "text-success";
  if (outcome === "failure") return "text-danger";
  return "text-text-muted";
}

function KeyChips({ label, keys }: { label: string; keys?: string[] }) {
  if (!keys || keys.length === 0) return null;
  return (
    <span className="flex flex-wrap items-center gap-1">
      <span className="text-text-muted">{label}</span>
      {keys.map((k) => (
        <code
          key={k}
          className="rounded bg-base px-1.5 py-0.5 text-text-primary"
        >
          {k}
        </code>
      ))}
    </span>
  );
}

function HistoryView(p: { data: HistoryState; loadMore: () => void }) {
  const { projection, entries, next, loadingMore, moreError } = p.data;
  return (
    <div className="space-y-3">
      <ProjectionNote projection={projection} />
      {entries.length === 0 ? (
        <Empty>
          No sync attempts have been recorded for this ExternalSecret yet.
        </Empty>
      ) : (
        <ol className="divide-y divide-border-subtle rounded-lg border border-border-primary">
          {entries.map((e) => {
            const c = e.diffKeyCounts;
            return (
              <li key={e.id} className="space-y-1 px-4 py-3 text-sm">
                <div className="flex flex-wrap items-center gap-3">
                  <span className={`font-medium ${outcomeClass(e.outcome)}`}>
                    {e.outcome}
                  </span>
                  <span className="text-text-primary">{e.reason}</span>
                  <span className="text-xs text-text-muted">
                    +{c.added} −{c.removed} ~{c.changed} keys
                  </span>
                  <span className="ml-auto text-xs text-text-muted">
                    <When at={e.attemptAt} />
                  </span>
                </div>
                {e.message && (
                  <p className="whitespace-pre-wrap break-words text-text-secondary">
                    {e.message}
                    {e.messageTruncated && " …(truncated)"}
                  </p>
                )}
                <div className="flex flex-wrap gap-3 text-xs">
                  <KeyChips label="Added" keys={e.diffKeysAdded} />
                  <KeyChips label="Removed" keys={e.diffKeysRemoved} />
                  <KeyChips label="Changed" keys={e.diffKeysChanged} />
                </div>
              </li>
            );
          })}
        </ol>
      )}
      {moreError && <Unavailable reason={moreError} />}
      {next && (
        <button
          type="button"
          disabled={loadingMore}
          onClick={p.loadMore}
          className="rounded border border-border-primary px-3 py-1.5 text-sm text-text-primary hover:bg-base disabled:opacity-50"
        >
          {loadingMore ? "Loading…" : "Load more"}
        </button>
      )}
    </div>
  );
}

function GeneratedView({ ces }: { ces?: Props["clusterExternalSecret"] }) {
  if (!ces) return <Unavailable reason="error" />;
  const children = generatedExternalSecrets(ces);
  return (
    <div className="space-y-3">
      <p className="text-sm text-text-muted">
        Sync history is recorded per ExternalSecret. Open a generated
        ExternalSecret to see its own attempts.
      </p>
      {children.length === 0 ? (
        <Empty>
          This ClusterExternalSecret has not generated any ExternalSecrets.
        </Empty>
      ) : (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border-primary">
          {children.map((c) => (
            <li
              key={c.namespace}
              className="flex items-center gap-3 px-4 py-2 text-sm"
            >
              <a href={c.href} className="text-brand hover:underline">
                {c.namespace}/{c.name}
              </a>
              {!c.provisioned && (
                <span className="text-xs text-danger">failed</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
