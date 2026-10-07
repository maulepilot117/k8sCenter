import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  captureEvidence,
  createIncident,
  isPersistenceUnavailable,
  listIncidents,
} from "@/lib/incident-api.ts";
import {
  DEFAULT_INCIDENT_WINDOW_MS,
  INCIDENT_MAX_TITLE_CHARS,
  type IncidentView,
  isCaptureKind,
} from "@/lib/incident-types.ts";
import { captureErrorText } from "@/src/components/incidents/CapturePanel.tsx";
import { keyedCreateErrorText } from "@/src/components/incidents/errors.ts";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";

/**
 * The diagnosis-to-incident entry point (Release D, U25b): a button that
 * opens a small dialog offering to capture the diagnosed object into a new
 * incident, or into one of the caller's open incidents, and then opens that
 * incident.
 *
 * What it carries through unchanged is the diagnosis's target (cluster,
 * namespace, kind, name) and its time window. `windowStart` is the earliest
 * time the diagnosis observed its sources (`earliestObservedAt`); when the
 * diagnosis did not report one, the window starts an hour before the click,
 * the same default the "New incident" form uses. Nothing else is invented.
 *
 * Capture is owner-only, so the "existing incident" list shows only open
 * incidents the caller owns. Capture is also local-cluster-only in Release D:
 * with a remote cluster selected the button is inactive and says why, and no
 * request is sent. A deployment without a database is detected once on mount
 * (`incident_persistence_unavailable`) and the button is inactive with that
 * explanation rather than failing on click.
 *
 * **No duplicate incidents.** The guarantee comes from the server's create
 * idempotency key (U25c, `clientRequestId`): each "capture into a new
 * incident" intent gets one request id, and every retry of that intent,
 * including one after a lost or unknown response, resends the same id, so
 * the server answers with the incident the first attempt made instead of
 * creating another. The intent is recorded BEFORE the create is sent, in a
 * pending record keyed by the target ("" and the local id normalised to one
 * cluster): sessionStorage, or a module-scoped Map when storage is
 * unavailable. A Re-scan remounts the island, and a remount (even one taken
 * while the create is still in flight) therefore retries with the same id.
 * The record holds the request id, the title, summary and window it was sent
 * with (a retry must resend the same payload), and, once known, the incident
 * id, which later attempts capture into directly. It is dropped when a
 * capture into that incident succeeds, when the incident refuses the capture
 * for good (gone, not the caller's, closed, at a limit), when the target is
 * captured into an existing incident instead, when the create is refused
 * outright (4xx), and on `client_request_id_conflict`, after which the next
 * attempt uses a fresh id. A diagnosis whose window start changed is a new
 * intent and gets a new id. Every write a flow makes after it starts is
 * conditional on the record still being that flow's, so a reply that lands
 * after a newer flow took over cannot overwrite it. Every action is inactive
 * while one is in flight, so a double click sends one create. Capture itself
 * is deduplicated server-side by capture key, which is what makes retrying an
 * outcome-unknown capture safe.
 *
 * Inactive controls use `aria-disabled` rather than `disabled`, so they stay
 * focusable and their explanatory tooltip stays reachable.
 */

const LIST_PAGE_SIZE = 50;
const PENDING_STORAGE_PREFIX = "kubecenter.capture-pending:";
const DIALOG_TITLE_ID = "capture-to-incident-title";
const REASON_ID = "capture-to-incident-reason";

const ROOT_CLASS = "inline-flex items-center";
const BUTTON_TRIGGER =
  "inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-surface px-3 py-1 text-xs font-medium text-text-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const BUTTON_PRIMARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-(--bg-base) focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const BUTTON_SECONDARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-transparent px-3 py-1.5 text-sm font-medium text-text-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";

const incidentHref = (id: string) =>
  `/observability/incidents/${encodeURIComponent(id)}`;

// --- Pending create intent ---------------------------------------------------------

/**
 * One "capture into a new incident" intent for a target: the request id the
 * create is (re)sent with, the payload it was first sent with, and the
 * incident it made once that is known.
 */
interface PendingRecord {
  requestId: string;
  title: string;
  summary: string;
  windowStart: string;
  id?: string;
}

/**
 * Overrides sessionStorage when storage is unavailable (SSR, blocked
 * storage, a private window that throws, a full quota). A record here is
 * newer than whatever storage holds; `null` is a tombstone for a clear that
 * storage refused. Module-scoped, so it survives a remount within the page.
 */
const pendingFallback = new Map<string, PendingRecord | null>();

/** The pending key; "" and the local id are the same (local) cluster. */
const pendingKey = (
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
) => `${clusterId || LOCAL_CLUSTER_ID}|${namespace}|${kind}|${name}`;

function parsePending(raw: string | null): PendingRecord | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as unknown;
    if (!v || typeof v !== "object") return null;
    const { requestId, title, summary, windowStart, id } = v as Record<
      string,
      unknown
    >;
    if (
      typeof requestId !== "string" ||
      !requestId ||
      typeof title !== "string" ||
      typeof summary !== "string" ||
      typeof windowStart !== "string"
    ) {
      return null;
    }
    const rec: PendingRecord = { requestId, title, summary, windowStart };
    if (typeof id === "string" && id) rec.id = id;
    return rec;
  } catch {
    return null;
  }
}

/** What sessionStorage holds for the key; null when empty or unreadable. */
function storedPending(key: string): PendingRecord | null {
  try {
    return parsePending(
      globalThis.sessionStorage.getItem(PENDING_STORAGE_PREFIX + key),
    );
  } catch {
    return null;
  }
}

function readPending(key: string): PendingRecord | null {
  if (pendingFallback.has(key)) {
    const rec = pendingFallback.get(key) ?? null;
    // A tombstone is only needed while storage still holds a value.
    if (rec === null && storedPending(key) === null) {
      pendingFallback.delete(key);
    }
    return rec;
  }
  return storedPending(key);
}

function writePending(key: string, rec: PendingRecord): void {
  try {
    globalThis.sessionStorage.setItem(
      PENDING_STORAGE_PREFIX + key,
      JSON.stringify(rec),
    );
    pendingFallback.delete(key);
  } catch {
    // Storage unavailable or full: the fallback holds it, and shadows any
    // older value storage still has.
    pendingFallback.set(key, rec);
  }
}

function clearPending(key: string): void {
  try {
    globalThis.sessionStorage.removeItem(PENDING_STORAGE_PREFIX + key);
    pendingFallback.delete(key);
  } catch {
    // Storage refused the removal: a tombstone shadows what it still holds,
    // or, when it holds nothing readable, there is nothing to shadow.
    if (storedPending(key) === null) pendingFallback.delete(key);
    else pendingFallback.set(key, null);
  }
}

/**
 * A new create request id: a v4 UUID in its 36-character hyphenated form,
 * the only spelling the server accepts. `crypto.randomUUID` exists only in
 * secure contexts, and a homelab install may be served over plain HTTP, so
 * the id is built from `crypto.getRandomValues` when it is missing.
 */
function newRequestId(): string {
  const c = globalThis.crypto;
  if (typeof c.randomUUID === "function") return c.randomUUID();
  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/** A failed create, kept apart from a failed capture for its wording. */
class CreateFailed extends Error {
  constructor(readonly failure: unknown) {
    super("create failed");
  }
}

/**
 * True when a failed create certainly made nothing, so its request id can be
 * dropped: a 4xx (including `client_request_id_conflict`) and the no-database
 * 503. A network error or any other 5xx may have committed, and the intent
 * is kept so the retry resends the same id.
 */
function createRefusedForGood(err: unknown): boolean {
  if (isPersistenceUnavailable(err)) return true;
  return err instanceof ApiError && err.status >= 400 && err.status < 500;
}

/**
 * True when a capture into a pending incident failed for a reason a retry
 * cannot fix: the incident is gone or not the caller's (404, 403), closed, or
 * at an evidence or scope limit. A network error, a 5xx, `incident_busy` and
 * an outcome-unknown capture are transient and keep it as the retry target.
 */
function captureRefusedForGood(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  if (err.status === 404 || err.status === 403) return true;
  return (
    err.reason === "incident_closed" ||
    err.reason === "evidence_limit_exceeded" ||
    err.reason === "scope_limit_exceeded"
  );
}

/**
 * The earliest valid `observedAt` among a diagnosis's checks, as an RFC 3339
 * instant, or undefined when no check reports one. Unparseable values are
 * ignored, never coerced.
 *
 * `observedAt` is the normalized check contract's field
 * (diagnostics.CheckResult). The legacy `GET /v1/diagnostics` response does
 * not carry it yet, so the checks are typed loosely and the field is read only
 * when it is present as a string; until the response carries it, every
 * diagnosis takes the one-hour fallback in `resolveWindowStart`.
 */
export function earliestObservedAt(
  checks: ReadonlyArray<object>,
): string | undefined {
  let earliest: number | undefined;
  for (const check of checks) {
    const observedAt = (check as { observedAt?: unknown }).observedAt;
    if (typeof observedAt !== "string" || !observedAt) continue;
    const t = Date.parse(observedAt);
    if (Number.isNaN(t)) continue;
    if (earliest === undefined || t < earliest) earliest = t;
  }
  return earliest === undefined ? undefined : new Date(earliest).toISOString();
}

/** The window start to record: the diagnosis's own, else `now` minus an hour. */
export function resolveWindowStart(
  windowStart: string | undefined,
  now: number,
): string {
  return (
    windowStart ?? new Date(now - DEFAULT_INCIDENT_WINDOW_MS).toISOString()
  );
}

export interface CaptureToIncidentButtonProps {
  /** The cluster the diagnosed object was observed on. */
  clusterId: string;
  namespace: string;
  kind: string;
  name: string;
  /** The earliest time the diagnosis observed its sources, when it reported one. */
  windowStart?: string;
  /** Extra utilities for the root, for placement in the caller's layout. */
  class?: string;
}

export default function CaptureToIncidentButton({
  clusterId,
  namespace,
  kind,
  name,
  windowStart,
  class: className,
}: CaptureToIncidentButtonProps) {
  const isLocal = clusterId === "" || clusterId === LOCAL_CLUSTER_ID;
  const key = pendingKey(clusterId, namespace, kind, name);
  /** Set when the deployment has no database; fixed for the page's life. */
  const noDatabase = useSignal(false);
  const open = useSignal(false);
  /** A create or capture is in flight; every action is inactive meanwhile. */
  const busy = useSignal(false);
  const error = useSignal<string | null>(null);
  /**
   * Bumped whenever the pending record is written or cleared, so the label
   * and link, which read it from storage, re-render.
   */
  const pendingVersion = useSignal(0);

  const owned = useSignal<IncidentView[]>([]);
  const listCursor = useSignal<string | undefined>(undefined);
  const listLoading = useSignal(false);
  const listError = useSignal<string | null>(null);

  const triggerRef = useRef<HTMLButtonElement>(null);
  const newButtonRef = useRef<HTMLButtonElement>(null);
  /**
   * Aborted only on unmount: a late reply then neither navigates nor writes
   * component state. A cluster switch does not abort it, so a flow or list
   * load in flight across a switch still settles its own flags.
   */
  const lifetime = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    return () => controller.abort();
  }, []);

  // The no-database probe, re-run whenever the target becomes local again.
  // It has its own controller, so a switch away cancels only the probe.
  useEffect(() => {
    if (!isLocal) {
      open.value = false;
      return;
    }
    const controller = new AbortController();
    listIncidents({ limit: 1 }, controller.signal).catch((err) => {
      if (!controller.signal.aborted && isPersistenceUnavailable(err)) {
        noDatabase.value = true;
      }
    });
    return () => controller.abort();
  }, [isLocal]);

  const isOpen = open.value && isLocal;
  useEffect(() => {
    if (isOpen) newButtonRef.current?.focus();
  }, [isOpen]);

  const inactiveReason = !isLocal
    ? "Capture is local-cluster only. Switch to the local cluster to capture this resource into an incident."
    : noDatabase.value
      ? "This deployment has no database, so incidents cannot be recorded."
      : !isCaptureKind(kind)
        ? `${kind || "This kind"} cannot be captured into an incident.`
        : null;

  const loadOwned = async (cursor?: string) => {
    if (listLoading.peek()) return;
    listLoading.value = true;
    listError.value = null;
    const signal = lifetime.current?.signal;
    try {
      const page = await listIncidents(
        { limit: LIST_PAGE_SIZE, continue: cursor },
        signal,
      );
      if (signal?.aborted) return;
      const mine = page.items.filter(
        (i) => i.role === "owner" && i.status === "open",
      );
      owned.value = cursor ? [...owned.peek(), ...mine] : mine;
      listCursor.value = page.continue;
    } catch (err) {
      if (signal?.aborted) return;
      if (isPersistenceUnavailable(err)) {
        noDatabase.value = true;
        open.value = false;
        return;
      }
      listError.value = "Could not load your open incidents.";
    } finally {
      if (!signal?.aborted) listLoading.value = false;
    }
  };

  const openDialog = () => {
    if (inactiveReason || open.peek()) return;
    error.value = null;
    open.value = true;
    void loadOwned();
  };

  const closeDialog = () => {
    if (busy.peek()) return;
    open.value = false;
    triggerRef.current?.focus();
  };

  /**
   * Runs one action; a second activation while one is in flight is a no-op.
   * The action returns the incident to open, or null to stay put.
   */
  const run = async (
    action: (signal?: AbortSignal) => Promise<string | null>,
  ) => {
    if (busy.peek()) return;
    busy.value = true;
    error.value = null;
    const signal = lifetime.current?.signal;
    try {
      const id = await action(signal);
      if (signal?.aborted || !id) return;
      globalThis.location.assign(incidentHref(id));
    } catch (err) {
      if (signal?.aborted) return;
      // A failed create has its own wording; a failed capture uses the
      // workspace's.
      error.value =
        err instanceof CreateFailed
          ? keyedCreateErrorText(err.failure)
          : captureErrorText(err);
      busy.value = false;
    }
  };

  const capture = (id: string, signal?: AbortSignal) => {
    if (!isCaptureKind(kind)) {
      throw new Error(`unsupported capture kind ${kind}`);
    }
    return captureEvidence(id, clusterId, { namespace, kind, name }, signal);
  };

  /**
   * Writes or clears the pending record, re-rendering while mounted. With
   * `owns`, the write happens only while the stored record still belongs to
   * the calling flow, so a reply that lands after a newer flow took over (a
   * create answering after a remount) cannot overwrite what it recorded.
   */
  const setPending = (
    rec: PendingRecord | null,
    signal?: AbortSignal,
    owns?: (current: PendingRecord | null) => boolean,
  ) => {
    if (owns && !owns(readPending(key))) return;
    if (rec) writePending(key, rec);
    else clearPending(key);
    if (!signal?.aborted) pendingVersion.value++;
  };

  // The new incident's title and summary. The summary carries the full,
  // untruncated target.
  const incidentTitle = `${kind}/${name} in ${namespace}`.slice(
    0,
    INCIDENT_MAX_TITLE_CHARS,
  );
  const incidentSummary = `Opened from the diagnosis of ${kind} ${namespace}/${name}.`;

  /**
   * The create intent to (re)send: the stored one when its payload still
   * matches this diagnosis (a retry must resend the same payload with the
   * same id), else a new one with a fresh request id. Without a diagnosis
   * window the stored window, the one-hour fallback computed at the first
   * click, is reused rather than recomputed.
   */
  const createIntent = (stored: PendingRecord | null): PendingRecord => {
    if (
      stored &&
      stored.title === incidentTitle &&
      stored.summary === incidentSummary &&
      (windowStart === undefined || windowStart === stored.windowStart)
    ) {
      return stored;
    }
    return {
      requestId: newRequestId(),
      title: incidentTitle,
      summary: incidentSummary,
      windowStart: resolveWindowStart(windowStart, Date.now()),
    };
  };

  const captureNew = () =>
    run(async (signal) => {
      const stored = readPending(key);
      let id = stored?.id ?? null;
      if (!id) {
        const intent = createIntent(stored);
        const ownsIntent = (current: PendingRecord | null) =>
          current?.requestId === intent.requestId;
        // Recorded before the request, so a retry (after a lost response,
        // or from a remounted island while this create is still in flight)
        // resends the same request id and gets the same incident back.
        setPending(intent, signal);
        // Not tied to the island's lifetime: a create that reached the
        // server must be recorded even when the island has unmounted.
        try {
          const created = await createIncident({
            title: intent.title,
            summary: intent.summary,
            windowStart: intent.windowStart,
            clientRequestId: intent.requestId,
          });
          id = created.incident.id;
        } catch (err) {
          // A refused create made nothing: drop the intent, so the next
          // attempt (after a request id conflict, too) starts afresh.
          if (createRefusedForGood(err)) setPending(null, signal, ownsIntent);
          throw new CreateFailed(err);
        }
        setPending({ ...intent, id }, signal, ownsIntent);
      }
      if (signal?.aborted) return null;
      const target = id;
      const ownsId = (current: PendingRecord | null) => current?.id === target;
      try {
        await capture(target, signal);
      } catch (err) {
        // The incident cannot take this capture (gone, not ours, closed,
        // full): forget it, so the next attempt creates a fresh one. A
        // transient failure keeps it as the retry target.
        if (captureRefusedForGood(err)) setPending(null, signal, ownsId);
        throw err;
      }
      setPending(null, signal, ownsId);
      return target;
    });

  const captureExisting = (id: string) =>
    run(async (signal) => {
      await capture(id, signal);
      // The target is now captured: no pending create intent is owed.
      setPending(null, signal);
      return id;
    });

  const inFlight = busy.value;
  // Read after the version so a write or clear re-renders the label and link.
  void pendingVersion.value;
  const created = readPending(key)?.id ?? null;

  return (
    <div class={className ? `${ROOT_CLASS} ${className}` : ROOT_CLASS}>
      <button
        ref={triggerRef}
        type="button"
        aria-haspopup="dialog"
        aria-disabled={inactiveReason !== null}
        aria-describedby={inactiveReason ? REASON_ID : undefined}
        title={inactiveReason ?? undefined}
        data-testid="capture-to-incident"
        onClick={openDialog}
        class={BUTTON_TRIGGER}
      >
        Capture to incident
      </button>
      {inactiveReason && (
        <span id={REASON_ID} class="sr-only">
          {inactiveReason}
        </span>
      )}
      {isOpen && (
        <ModalDialogShell
          titleId={DIALOG_TITLE_ID}
          title="Capture to incident"
          testId="capture-to-incident-dialog"
          closeTestId="capture-to-incident-close"
          onClose={closeDialog}
        >
          <div class="flex flex-col gap-4 overflow-y-auto p-4">
            <p class="m-0 text-sm text-text-secondary">
              Captures the diagnosis, object and events of{" "}
              <span class="font-medium text-text-primary">
                {kind}/{name}
              </span>{" "}
              in <span class="font-medium text-text-primary">{namespace}</span>.
            </p>
            {error.value && (
              <div role="alert" class="flex flex-col gap-2">
                <Alert variant="error">{error.value}</Alert>
                {created && (
                  <a
                    href={incidentHref(created)}
                    class="text-sm font-medium text-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
                  >
                    Open the incident that was created
                  </a>
                )}
              </div>
            )}
            {inFlight && (
              <p role="status" class="m-0 text-sm text-text-muted">
                Capturing…
              </p>
            )}
            <button
              ref={newButtonRef}
              type="button"
              aria-disabled={inFlight}
              data-testid="capture-to-new-incident"
              onClick={captureNew}
              class={BUTTON_PRIMARY}
            >
              {created
                ? "Retry capture into the new incident"
                : "Capture into a new incident"}
            </button>
            <section
              aria-labelledby="capture-existing-heading"
              class="flex flex-col gap-2"
            >
              <h3
                id="capture-existing-heading"
                class="m-0 text-xs font-semibold uppercase tracking-wide text-text-muted"
              >
                Or capture into an open incident you own
              </h3>
              {listError.value && (
                <div class="flex items-center gap-3 text-sm">
                  <span role="alert" class="text-error">
                    {listError.value}
                  </span>
                  <button
                    type="button"
                    aria-disabled={listLoading.value}
                    onClick={() => void loadOwned()}
                    class={BUTTON_SECONDARY}
                  >
                    Retry
                  </button>
                </div>
              )}
              {!listError.value &&
                !listLoading.value &&
                !listCursor.value &&
                owned.value.length === 0 && (
                  <p class="m-0 text-sm text-text-muted">
                    You own no open incidents.
                  </p>
                )}
              {owned.value.length > 0 && (
                <ul class="m-0 flex list-none flex-col gap-1 p-0">
                  {owned.value.map((incident) => (
                    <li key={incident.id}>
                      <button
                        type="button"
                        aria-disabled={inFlight}
                        data-testid="capture-to-existing-incident"
                        onClick={() => captureExisting(incident.id)}
                        class="flex w-full cursor-pointer items-center justify-between gap-3 rounded-md border border-border-subtle bg-transparent px-3 py-2 text-left text-sm text-text-primary hover:bg-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50"
                      >
                        <span class="truncate">{incident.title}</span>
                        <span class="shrink-0 text-xs text-text-muted">
                          Capture here
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {listLoading.value && (
                <p role="status" class="m-0 text-sm text-text-muted">
                  Loading your incidents…
                </p>
              )}
              {listCursor.value && !listLoading.value && !listError.value && (
                <button
                  type="button"
                  onClick={() => void loadOwned(listCursor.peek())}
                  class={BUTTON_SECONDARY}
                >
                  Load more
                </button>
              )}
            </section>
          </div>
        </ModalDialogShell>
      )}
    </div>
  );
}
