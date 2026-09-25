import { type Signal, useSignal } from "@preact/signals";
import type { ComponentChildren } from "preact";
import { useEffect, useRef } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
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

interface Props {
  kind: EvidenceKind;
  /** null for the two cluster-scoped kinds. */
  namespace: string | null;
  name: string;
  /** The live object's UID; a response for any other UID is discarded. */
  uid: string;
  initialTab?: EvidenceTabKey;
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
      ? err.body?.error?.message
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

  const active = useSignal<EvidenceTabKey>(firstTab);
  const yaml = useSignal<Load<string>>({ state: "idle" });
  const events = useSignal<Load<EvidenceEventsResponse>>({ state: "idle" });
  const history = useSignal<Load<HistoryState>>({ state: "idle" });

  // Which object the panel currently shows. A response started for an earlier
  // target compares unequal and is dropped even if it resolves last.
  const targetKey = `${kind}/${namespace ?? "_"}/${name}/${uid}`;
  const currentTarget = useRef(targetKey);
  const controllers = useRef(new Map<string, AbortController>());

  function begin(tab: string): { signal: AbortSignal; target: string } {
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
    yaml.value = { state: "loading" };
    esoApi
      .exportEvidenceYaml(kind, namespace, name, signal)
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        yaml.value = { state: "ready", data: String(res.data ?? "") };
      })
      .catch((err) => {
        if (stillCurrent(signal, target)) yaml.value = unavailable(err);
      });
  }

  function loadEvents() {
    const { signal, target } = begin("events");
    events.value = { state: "loading" };
    esoApi
      .getEvidenceEvents(kind, namespace, name, signal)
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        if (isStaleResponse(uid, res.data?.uid)) return;
        events.value = { state: "ready", data: res.data };
      })
      .catch((err) => {
        if (stillCurrent(signal, target)) events.value = unavailable(err);
      });
  }

  function loadHistory(more: boolean) {
    const prev = history.value.state === "ready" ? history.value.data : null;
    if (more && !prev?.next) return;
    const { signal, target } = begin("history");
    if (more && prev) {
      history.value = {
        state: "ready",
        data: { ...prev, loadingMore: true, moreError: undefined },
      };
    } else {
      history.value = { state: "loading" };
    }
    esoApi
      .getExternalSecretHistory(namespace ?? "", name, {
        limit: HISTORY_PAGE_SIZE,
        cursor: more ? prev?.next : undefined,
        signal,
      })
      .then((res) => {
        if (!stillCurrent(signal, target)) return;
        if (isStaleResponse(uid, res.data?.uid)) return;
        const page = res.data;
        history.value = {
          state: "ready",
          data: {
            projection: page.projection,
            entries:
              more && prev
                ? mergeHistoryPages(prev.entries, page.entries)
                : mergeHistoryPages([], page.entries),
            next: res.metadata?.continue || undefined,
            loadingMore: false,
          },
        };
      })
      .catch((err) => {
        if (!stillCurrent(signal, target)) return;
        if (more && prev) {
          // Rows already shown stay; the failed page is reported beside them.
          history.value = {
            state: "ready",
            data: {
              ...prev,
              loadingMore: false,
              moreError: classifyEvidenceError(err),
            },
          };
        } else {
          history.value = unavailable(err);
        }
      });
  }

  function ensureLoaded(tab: EvidenceTabKey) {
    if (tab === "yaml" && yaml.value.state === "idle") loadYaml();
    if (tab === "events" && events.value.state === "idle") loadEvents();
    if (tab === "history" && history.value.state === "idle") loadHistory(false);
  }

  // A new target discards everything shown for the old one and aborts its
  // in-flight requests; unmount aborts them too.
  useEffect(() => {
    currentTarget.current = targetKey;
    yaml.value = { state: "idle" };
    events.value = { state: "idle" };
    history.value = { state: "idle" };
    if (!tabs.some((t) => t.key === active.value)) active.value = tabs[0].key;
    ensureLoaded(active.value);
    const inFlight = controllers.current;
    return () => {
      for (const c of inFlight.values()) c.abort();
      inFlight.clear();
    };
  }, [targetKey]);

  return (
    <div class="space-y-4">
      <div role="tablist" class="flex gap-1 border-b border-border-primary">
        {tabs.map(({ key, label }) => {
          const selected = active.value === key;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={selected}
              onClick={() => {
                active.value = key;
                ensureLoaded(key);
              }}
              class={`px-3 py-2 text-sm border-b-2 -mb-px transition-colors ${
                selected
                  ? "border-brand text-text-primary"
                  : "border-transparent text-text-muted hover:text-text-primary"
              }`}
            >
              {label}
            </button>
          );
        })}
      </div>

      <div role="tabpanel">
        {active.value === "yaml" && (
          <TabBody load={yaml} retry={loadYaml}>
            {(text) => (
              <pre class="max-h-[32rem] overflow-auto rounded-lg border border-border-primary bg-base p-4 font-mono text-xs text-text-primary">
                {text}
              </pre>
            )}
          </TabBody>
        )}
        {active.value === "events" && (
          <TabBody load={events} retry={loadEvents}>
            {(data) => <EventsView data={data} />}
          </TabBody>
        )}
        {active.value === "history" && (
          <TabBody load={history} retry={() => loadHistory(false)}>
            {(data) => (
              <HistoryView data={data} loadMore={() => loadHistory(true)} />
            )}
          </TabBody>
        )}
        {active.value === "generated" && (
          <GeneratedView ces={props.clusterExternalSecret} />
        )}
      </div>
    </div>
  );
}

function TabBody<T>(p: {
  load: Signal<Load<T>>;
  retry: () => void;
  children: (data: T) => ComponentChildren;
}) {
  const l = p.load.value;
  if (l.state === "idle" || l.state === "loading") {
    return (
      <div class="flex justify-center py-8" role="status" aria-label="Loading">
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
      class="space-y-2 rounded-lg border border-border-primary bg-elevated p-5 text-sm"
    >
      <p
        class={p.reason === "forbidden" ? "text-warning" : "text-text-primary"}
      >
        {unavailableMessage(p.reason)}
      </p>
      {p.detail && <p class="text-text-muted">{p.detail}</p>}
      {transient && p.retry && (
        <button
          type="button"
          onClick={p.retry}
          class="rounded border border-border-primary px-3 py-1 text-xs text-text-primary hover:bg-base"
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
      class="rounded border border-border-subtle bg-surface px-3 py-2 text-xs text-text-muted"
    >
      {note}
    </p>
  );
}

function Empty({ children }: { children: ComponentChildren }) {
  return (
    <div
      data-evidence-state="empty"
      class="rounded-lg border border-border-primary bg-elevated p-5 text-sm text-text-muted"
    >
      {children}
    </div>
  );
}

function When({ at }: { at?: string }) {
  if (!at) return <span class="text-text-muted">—</span>;
  return (
    <time dateTime={at} title={at}>
      {timeAgo(at)}
    </time>
  );
}

function EventsView({ data }: { data: EvidenceEventsResponse }) {
  return (
    <div class="space-y-3">
      <ProjectionNote projection={data.projection} />
      {data.events.length === 0 ? (
        <Empty>
          No events are recorded for this object. Kubernetes keeps events for
          about an hour, so an object that has been quiet shows none.
        </Empty>
      ) : (
        <div class="overflow-x-auto rounded-lg border border-border-primary">
          <table class="w-full text-left text-sm">
            <thead class="bg-surface text-xs text-text-muted">
              <tr>
                <th class="px-3 py-2">Type</th>
                <th class="px-3 py-2">Reason</th>
                <th class="px-3 py-2">Count</th>
                <th class="px-3 py-2">Last seen</th>
                <th class="px-3 py-2">Details</th>
              </tr>
            </thead>
            <tbody>
              {data.events.map((ev, i) => (
                <tr key={i} class="border-t border-border-subtle align-top">
                  <td
                    class={`px-3 py-2 ${ev.type === "Warning" ? "text-warning" : "text-text-primary"}`}
                  >
                    {ev.type}
                  </td>
                  <td class="px-3 py-2 text-text-primary">{ev.reason}</td>
                  <td class="px-3 py-2 text-text-muted">{ev.count}</td>
                  <td class="px-3 py-2 text-text-muted">
                    <When at={ev.lastTimestamp} />
                  </td>
                  <td class="px-3 py-2 text-text-secondary">
                    {ev.message !== undefined ? (
                      <span class="whitespace-pre-wrap break-words">
                        {ev.message}
                        {ev.messageTruncated && " …(truncated)"}
                      </span>
                    ) : (
                      <span class="text-text-muted">Hidden</span>
                    )}
                    {ev.source && (
                      <span class="block text-xs text-text-muted">
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
        <p class="text-xs text-text-muted">
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
    <span class="flex flex-wrap items-center gap-1">
      <span class="text-text-muted">{label}</span>
      {keys.map((k) => (
        <code key={k} class="rounded bg-base px-1.5 py-0.5 text-text-primary">
          {k}
        </code>
      ))}
    </span>
  );
}

function HistoryView(p: { data: HistoryState; loadMore: () => void }) {
  const { projection, entries, next, loadingMore, moreError } = p.data;
  return (
    <div class="space-y-3">
      <ProjectionNote projection={projection} />
      {entries.length === 0 ? (
        <Empty>
          No sync attempts have been recorded for this ExternalSecret yet.
        </Empty>
      ) : (
        <ol class="divide-y divide-border-subtle rounded-lg border border-border-primary">
          {entries.map((e) => {
            const c = e.diffKeyCounts;
            return (
              <li key={e.id} class="space-y-1 px-4 py-3 text-sm">
                <div class="flex flex-wrap items-center gap-3">
                  <span class={`font-medium ${outcomeClass(e.outcome)}`}>
                    {e.outcome}
                  </span>
                  <span class="text-text-primary">{e.reason}</span>
                  <span class="text-xs text-text-muted">
                    +{c.added} −{c.removed} ~{c.changed} keys
                  </span>
                  <span class="ml-auto text-xs text-text-muted">
                    <When at={e.attemptAt} />
                  </span>
                </div>
                {e.message && (
                  <p class="whitespace-pre-wrap break-words text-text-secondary">
                    {e.message}
                    {e.messageTruncated && " …(truncated)"}
                  </p>
                )}
                <div class="flex flex-wrap gap-3 text-xs">
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
          class="rounded border border-border-primary px-3 py-1.5 text-sm text-text-primary hover:bg-base disabled:opacity-50"
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
    <div class="space-y-3">
      <p class="text-sm text-text-muted">
        Sync history is recorded per ExternalSecret. Open a generated
        ExternalSecret to see its own attempts.
      </p>
      {children.length === 0 ? (
        <Empty>
          This ClusterExternalSecret has not generated any ExternalSecrets.
        </Empty>
      ) : (
        <ul class="divide-y divide-border-subtle rounded-lg border border-border-primary">
          {children.map((c) => (
            <li
              key={c.namespace}
              class="flex items-center gap-3 px-4 py-2 text-sm"
            >
              <a href={c.href} class="text-brand hover:underline">
                {c.namespace}/{c.name}
              </a>
              {!c.provisioned && (
                <span class="text-xs text-danger">failed</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
