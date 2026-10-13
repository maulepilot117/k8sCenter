import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { ApiError, apiGet } from "@/lib/api.ts";
import { LOCAL_CLUSTER_ID } from "@/lib/cluster.ts";
import {
  type Evidence,
  type EvidenceItem,
  type EvidenceMode,
  evidenceKindLabel,
  evidenceTimestamps,
  isWithheld,
  liveLinkTarget,
  OBSERVATION_TIME_UNKNOWN,
  redactionNotes,
  type SourceRef,
  type WithheldEvidence,
  withheldReasonText,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import {
  BADGE_CLASS,
  CompletenessBadge,
  FOCUS_RING,
  LINK,
} from "@/src/components/incidents/ui.tsx";

/**
 * An incident's evidence as a timeline, newest capture first. The caller
 * passes the items already ordered (`sortTimeline`); this component renders
 * them and resolves live links.
 *
 * What it must make visible, per row:
 *   - both timestamps, labelled: when the cluster observed the fact and when
 *     k8sCenter captured it. A source that recorded no observation time says
 *     so; the capture time is never shown in its place.
 *   - whether the row is a retained snapshot or a live link. A live link is
 *     re-read now, under the reader's current access: a 404 is "no longer
 *     present" and a 403 "no longer authorized", both ordinary states rather
 *     than errors.
 *   - the five completeness states, each distinct.
 *   - what redaction removed.
 *   - a withheld item as an explicit placeholder row: it says something is
 *     there and why it is withheld, and nothing about what it is about.
 */

function ModeBadge({ mode }: { mode: EvidenceMode }) {
  return mode === "live_link" ? (
    <span class={BADGE_CLASS.accentOutline}>Live link</span>
  ) : (
    <span class={BADGE_CLASS.accent}>Snapshot</span>
  );
}

function Stamp({ label, at }: { label: string; at: string | null }) {
  return (
    <span class="text-xs text-text-muted">
      <span class="font-medium text-text-secondary">{label}</span>{" "}
      {at ? (
        <time dateTime={at} title={at}>
          {new Date(at).toLocaleString()} ({timeAgo(at)})
        </time>
      ) : (
        <span>{OBSERVATION_TIME_UNKNOWN}</span>
      )}
    </span>
  );
}

function sourceLabel(s: SourceRef): string {
  return s.namespace
    ? `${s.kind} ${s.namespace}/${s.name}`
    : `${s.kind} ${s.name}`;
}

type LiveState =
  | "checking"
  | "present"
  | "replaced"
  | "gone"
  | "unauthorized"
  | "error"
  | "unchecked";

/** At most this many live-link reads run at once; the rest wait their turn. */
const LIVE_LINK_CONCURRENCY = 6;
/** A live-link read is abandoned after this long, freeing its slot. */
const LIVE_LINK_TIMEOUT_MS = 10_000;
let liveActive = 0;
const liveWaiting: Array<() => void> = [];

/**
 * Runs `read` when one of the LIVE_LINK_CONCURRENCY slots is free, so a long
 * timeline does not fire one request per live link at once. A read aborted
 * while still waiting leaves the queue without ever running.
 */
function withLiveSlot<T>(
  signal: AbortSignal,
  read: () => Promise<T>,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => {
      const i = liveWaiting.indexOf(start);
      if (i >= 0) liveWaiting.splice(i, 1);
      reject(new DOMException("Aborted", "AbortError"));
    };
    function start() {
      signal.removeEventListener("abort", onAbort);
      liveActive++;
      read()
        .then(resolve, reject)
        .finally(() => {
          liveActive--;
          liveWaiting.shift()?.();
        });
    }
    if (liveActive < LIVE_LINK_CONCURRENCY) {
      start();
    } else {
      liveWaiting.push(start);
      signal.addEventListener("abort", onAbort, { once: true });
    }
  });
}

/**
 * Re-reads a live link's object under the reader's current access. Only the
 * local cluster is resolved: Release D captures there only, and a non-admin
 * reading a remote cluster would be refused by the cluster gate with a 403
 * that says nothing about the object.
 */
function LiveLink({ source }: { source: SourceRef }) {
  const target = liveLinkTarget(source);
  const local = source.clusterId === LOCAL_CLUSTER_ID;
  const state = useSignal<LiveState>(
    target && local ? "checking" : "unchecked",
  );

  useEffect(() => {
    if (!target || !local) return;
    const controller = new AbortController();
    withLiveSlot(controller.signal, () => {
      // A read that hangs would hold its slot for good; give up after
      // LIVE_LINK_TIMEOUT_MS and report "could not be checked".
      const read = new AbortController();
      const stop = () => read.abort();
      controller.signal.addEventListener("abort", stop, { once: true });
      const timer = setTimeout(stop, LIVE_LINK_TIMEOUT_MS);
      return apiGet<{ metadata?: { uid?: string } }>(target.apiPath, {
        clusterId: source.clusterId,
        signal: read.signal,
      }).finally(() => {
        clearTimeout(timer);
        controller.signal.removeEventListener("abort", stop);
      });
    })
      .then((res) => {
        if (controller.signal.aborted) return;
        const uid = res.data?.metadata?.uid;
        // Same name, different object: the captured one is gone.
        state.value =
          source.uid && uid && uid !== source.uid ? "replaced" : "present";
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (err instanceof ApiError && err.status === 404) {
          state.value = "gone";
        } else if (err instanceof ApiError && err.status === 403) {
          state.value = "unauthorized";
        } else {
          state.value = "error";
        }
      });
    return () => controller.abort();
  }, [target?.apiPath, source.clusterId, source.uid]);

  const link = target && (
    <a href={target.href} class={LINK}>
      Open the live object
    </a>
  );
  let text: string;
  switch (state.value) {
    case "checking":
      text = "Checking the live object…";
      break;
    case "present":
      text = "The object still exists.";
      break;
    case "replaced":
      text =
        "No longer present: a different object now has this name (its UID changed).";
      break;
    case "gone":
      text = "No longer present: the object was deleted after capture.";
      break;
    case "unauthorized":
      text = "No longer authorized: you cannot read this object now.";
      break;
    case "error":
      text = "The live object could not be checked right now.";
      break;
    default:
      text = local
        ? "This kind has no detail page to link to."
        : "Live objects are checked on the local cluster only.";
  }
  const showLink =
    state.value === "present" || state.value === "error" ? link : null;
  return (
    <p class="m-0 flex flex-wrap items-center gap-2 text-sm text-text-secondary">
      <span data-live-state={state.value}>{text}</span>
      {showLink}
    </p>
  );
}

function payloadText(payload: unknown): string {
  try {
    return JSON.stringify(payload, null, 2) ?? "";
  } catch {
    return String(payload);
  }
}

function EvidenceRow({ item }: { item: Evidence }) {
  const { observed, captured } = evidenceTimestamps(item);
  const notes = redactionNotes(item.redaction);
  return (
    <li class="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-sm font-semibold text-text-primary">
          {evidenceKindLabel(item.evidenceKind)}
        </span>
        <ModeBadge mode={item.mode} />
        <CompletenessBadge value={item.completeness} />
        <span class="text-sm text-text-secondary">
          {sourceLabel(item.source)}
        </span>
      </div>
      <div class="flex flex-wrap gap-x-4 gap-y-1">
        <Stamp label="Observed" at={observed} />
        <Stamp label="Captured" at={captured} />
      </div>
      {item.completenessDetail && (
        <p class="m-0 text-xs text-text-muted">{item.completenessDetail}</p>
      )}
      {item.source.identityWeak && (
        <p class="m-0 text-xs text-text-muted">
          Identified by name only: no UID was observed, so this may not be the
          same object that exists under this name now.
        </p>
      )}
      {notes.length > 0 && (
        <ul
          class="m-0 flex list-none flex-wrap gap-2 p-0"
          aria-label="Redaction"
        >
          {notes.map((n) => (
            <li
              key={n}
              class="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-text-secondary"
            >
              {n}
            </li>
          ))}
        </ul>
      )}
      {item.mode === "live_link" ? (
        <LiveLink source={item.source} />
      ) : (
        item.payload !== undefined && (
          <details class="text-sm">
            <summary
              class={`cursor-pointer rounded-sm text-text-secondary ${FOCUS_RING}`}
            >
              Captured data ({item.payloadBytes} bytes)
            </summary>
            <pre class="mt-2 max-h-80 overflow-auto rounded-md border border-border-subtle bg-base p-3 text-xs text-text-primary">
              {payloadText(item.payload)}
            </pre>
          </details>
        )
      )}
    </li>
  );
}

function WithheldRow({ item }: { item: WithheldEvidence }) {
  return (
    <li
      data-withheld="true"
      class="flex flex-col gap-1 rounded-lg border border-dashed border-border-primary bg-surface p-3"
    >
      <div class="flex flex-wrap items-center gap-2">
        <span class={BADGE_CLASS.muted}>Withheld</span>
        <span class="text-sm text-text-secondary">
          {evidenceKindLabel(item.evidenceKind)}
        </span>
      </div>
      <Stamp label="Captured" at={item.collectedAt} />
      <p class="m-0 text-sm text-text-secondary">
        {withheldReasonText(item.withheldReason)}
      </p>
    </li>
  );
}

export default function IncidentEvidenceTimeline({
  items,
}: {
  items: EvidenceItem[];
}) {
  return (
    <ol
      aria-label="Evidence timeline"
      class="m-0 flex list-none flex-col gap-3 p-0"
    >
      {items.map((item) =>
        isWithheld(item) ? (
          <WithheldRow key={item.id} item={item} />
        ) : (
          <EvidenceRow key={item.id} item={item} />
        ),
      )}
    </ol>
  );
}
