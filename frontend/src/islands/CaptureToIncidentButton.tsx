import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  captureEvidence,
  createIncident,
  isPersistenceUnavailable,
  listIncidents,
} from "@/lib/incident-api.ts";
import {
  clearPendingCapture,
  isPendingConflict,
  isStaleIntent,
  newClientRequestId,
  type PendingCapture,
  type PendingCreate,
  readPendingCapture,
  writePendingCapture,
} from "@/lib/incident-create.ts";
import {
  DEFAULT_INCIDENT_WINDOW_MS,
  INCIDENT_MAX_TITLE_CHARS,
  type IncidentView,
  isCaptureKind,
} from "@/lib/incident-types.ts";
import { captureErrorText } from "@/src/components/incidents/CapturePanel.tsx";
import {
  createConflictText,
  createRefusedForGood,
  isCreateConflict,
  keyedCreateErrorText,
} from "@/src/components/incidents/errors.ts";
import { LOCAL_CLUSTER_ID } from "@/src/lib/cluster.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";

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
 * including one after a lost or unknown response, resends the same id with
 * the same payload, so the server answers with the incident the first
 * attempt made instead of creating another. The intent is recorded BEFORE
 * the create is sent, in a pending record keyed by the signed-in user and
 * the target ("" and the local id normalised to one cluster; storage lives
 * in `lib/incident-create.ts`), so another identity on the same tab never
 * reads it, even after a session that ended without logging out. A Re-scan
 * remounts the island, and a remount (even one taken while the create is
 * still in flight) therefore retries with the same id. The button stays
 * inactive until the signed-in user is known; when nothing is loading the
 * user (the top bar's load failed), selecting it retries the load once and
 * a second failure says to reload the page.
 *
 * While the record has no incident id, every retry resends the stored id
 * and the stored title, summary and window byte for byte, whatever the
 * diagnosis now reports; a new id is minted only when no intent is stored.
 * Once the incident id is known, later attempts capture into it directly.
 * The record is dropped when a capture into that incident succeeds, when
 * the incident refuses the capture for good (gone, not the caller's, closed,
 * at a limit), when the target is captured into an existing incident while
 * nothing is owed (no record, a conflict, or a known incident), when the
 * create is refused in a way that proves nothing was made
 * (400, 413, no database), and on logout. A 401, 403, 408, 429 or any other
 * refusal keeps it: an earlier attempt may have committed, and resending the
 * key cannot create a second incident.
 *
 * `client_request_id_conflict` on a retry means an earlier attempt made an
 * incident that was edited since. The key is dropped, a notice says an
 * incident for this capture may already exist and links to the incident
 * list, and nothing is created on the next click: only the explicit "Create
 * a new incident anyway" mints a new id. The conflict state is recorded, so
 * it survives a remount. Two other states read the same way: an intent
 * whose outcome is still unknown `PENDING_INTENT_MAX_AGE_MS` (15 minutes,
 * far longer than any create can stay in flight) after it was last sent is
 * no longer resent silently, and capturing into an existing incident while an
 * intent's outcome is unknown leaves the notice behind instead of dropping
 * the intent.
 *
 * Every write a flow makes after it starts is conditional on the record
 * still being that flow's, so a reply that lands after a newer flow took
 * over cannot overwrite it. Every action is inactive while one is in
 * flight, so a double click sends one create. Capture itself is deduplicated
 * server-side by capture key, which is what makes retrying an
 * outcome-unknown capture safe.
 *
 * Inactive controls use `aria-disabled` rather than `disabled`, so they stay
 * focusable and their explanatory tooltip stays reachable.
 */

const LIST_PAGE_SIZE = 50;
const DIALOG_TITLE_ID = "capture-to-incident-title";
const REASON_ID = "capture-to-incident-reason";

const ROOT_CLASS = "inline-flex items-center";
const BUTTON_TRIGGER =
  "inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-surface px-3 py-1 text-xs font-medium text-text-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const BUTTON_PRIMARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-(--bg-base) focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const BUTTON_SECONDARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-transparent px-3 py-1.5 text-sm font-medium text-text-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";

const INCIDENTS_HREF = "/observability/incidents";
const LINK_CLASS =
  "text-sm font-medium text-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50";

const incidentHref = (id: string) =>
  `/observability/incidents/${encodeURIComponent(id)}`;

// --- Pending create intent ---------------------------------------------------------

/**
 * The pending key: the signed-in user, then the target. "" and the local id
 * are the same (local) cluster.
 */
const pendingKey = (
  userId: string,
  clusterId: string,
  namespace: string,
  kind: string,
  name: string,
) => `${userId}|${clusterId || LOCAL_CLUSTER_ID}|${namespace}|${kind}|${name}`;

/** Same record: same key, same incident, or both a conflict, or both none. */
function sameRecord(a: PendingCapture | null, b: PendingCapture | null) {
  if (a === null || b === null) return a === b;
  if (isPendingConflict(a) || isPendingConflict(b)) {
    return isPendingConflict(a) && isPendingConflict(b);
  }
  return a.requestId === b.requestId && a.id === b.id;
}

/** A failed create, kept apart from a failed capture for its wording. */
class CreateFailed extends Error {
  constructor(readonly failure: unknown) {
    super("create failed");
  }
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
  const {
    user,
    loading: userLoading,
    loadAttempted: userLoadAttempted,
    fetchCurrentUser,
  } = useAuth();
  /** The signed-in user; the button is inactive until it is known. */
  const userId = user.value?.id ?? null;
  /** This island's own retry of the user load ended without a user. */
  const userLoadFailed = useSignal(false);
  /**
   * A create met `client_request_id_conflict` in this mount. Normally the
   * stored conflict record shows the notice; this keeps it showing when that
   * write was refused because a newer flow owns the record.
   */
  const conflictSeen = useSignal(false);
  const key = pendingKey(userId ?? "", clusterId, namespace, kind, name);
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
        : userId
          ? null
          : userLoading.value || !userLoadAttempted.value
            ? "Your sign-in details are still loading."
            : userLoadFailed.value
              ? "Your sign-in details could not be loaded. Reload the page."
              : "Your sign-in details are not loaded. Select to try again.";

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

  /**
   * Retries the user load once per click when nothing is loading it (the
   * top bar's load failed or never ran). Failing again says so truthfully.
   */
  const retryUserLoad = async () => {
    if (userId || userLoading.peek()) return;
    userLoadFailed.value = false;
    // Scoped like the top bar's load, so the RBAC summary it stores keeps
    // matching the selected namespace.
    const ns = selectedNamespace.peek();
    const loaded = await fetchCurrentUser(ns !== "all" ? ns : undefined);
    if (!lifetime.current?.signal.aborted) userLoadFailed.value = !loaded;
  };

  const openDialog = () => {
    // Only the missing user stands in the way: try loading it again.
    if (isLocal && !noDatabase.peek() && isCaptureKind(kind) && !userId) {
      void retryUserLoad();
      return;
    }
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
      if (signal?.aborted) return;
      if (!id) {
        // Nothing to open (a conflict stopped the create): stay, re-read.
        busy.value = false;
        pendingVersion.value++;
        return;
      }
      globalThis.location.assign(incidentHref(id));
    } catch (err) {
      if (signal?.aborted) return;
      // A failed create has its own wording; a failed capture uses the
      // workspace's.
      // A key conflict is shown by the "may already exist" notice, not twice.
      if (err instanceof CreateFailed && isCreateConflict(err.failure)) {
        conflictSeen.value = true;
      }
      error.value =
        err instanceof CreateFailed
          ? isCreateConflict(err.failure)
            ? null
            : keyedCreateErrorText(err.failure)
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
    rec: PendingCapture | null,
    signal?: AbortSignal,
    owns?: (current: PendingCapture | null) => boolean,
  ) => {
    if (owns && !owns(readPendingCapture(key))) return;
    if (rec) writePendingCapture(key, rec);
    else clearPendingCapture(key);
    if (!signal?.aborted) pendingVersion.value++;
  };

  // The new incident's title and summary. The summary carries the full,
  // untruncated target.
  const incidentTitle = `${kind}/${name} in ${namespace}`.slice(
    0,
    INCIDENT_MAX_TITLE_CHARS,
  );
  const incidentSummary = `Opened from the diagnosis of ${kind} ${namespace}/${name}.`;

  /** A fresh create intent for this diagnosis, with a new request id. */
  const freshIntent = (): PendingCreate => {
    const now = Date.now();
    return {
      requestId: newClientRequestId(),
      title: incidentTitle,
      summary: incidentSummary,
      windowStart: resolveWindowStart(windowStart, now),
      createdAt: now,
    };
  };

  /**
   * Creates the incident for `intent` (recorded first), then captures into
   * it. Returns the incident to open.
   */
  const createAndCapture = async (
    intent: PendingCreate,
    signal?: AbortSignal,
  ): Promise<string | null> => {
    const ownsIntent = (current: PendingCapture | null) =>
      !isPendingConflict(current) && current?.requestId === intent.requestId;
    // Recorded before the request, so a retry (after a lost response, or
    // from a remounted island while this create is still in flight) resends
    // the same request id and payload and gets the same incident back. The
    // send time is restamped on every send: the age bound runs from it.
    setPending({ ...intent, sentAt: Date.now() }, signal);
    let id: string;
    // Not tied to the island's lifetime: a create that reached the server
    // must be recorded even when the island has unmounted.
    try {
      const created = await createIncident({
        title: intent.title,
        summary: intent.summary,
        windowStart: intent.windowStart,
        clientRequestId: intent.requestId,
      });
      id = created.incident.id;
    } catch (err) {
      if (isCreateConflict(err)) {
        // An earlier attempt with this key made an incident that was edited
        // since. Drop the key and create nothing until the operator says so.
        setPending({ conflict: true }, signal, ownsIntent);
      } else if (createRefusedForGood(err)) {
        // Nothing was ever made with this key: the next attempt starts afresh.
        setPending(null, signal, ownsIntent);
      }
      throw new CreateFailed(err);
    }
    setPending({ ...intent, id }, signal, ownsIntent);
    if (signal?.aborted) return null;
    return captureInto(id, signal);
  };

  /**
   * Captures into the pending incident `id`, clearing the record once the
   * capture lands or the incident refuses it for good.
   */
  const captureInto = async (id: string, signal?: AbortSignal) => {
    const ownsId = (current: PendingCapture | null) =>
      !isPendingConflict(current) && current?.id === id;
    try {
      await capture(id, signal);
    } catch (err) {
      // The incident cannot take this capture (gone, not ours, closed,
      // full): forget it, so the next attempt creates a fresh one. A
      // transient failure keeps it as the retry target.
      if (captureRefusedForGood(err)) setPending(null, signal, ownsId);
      throw err;
    }
    setPending(null, signal, ownsId);
    return id;
  };

  /**
   * "Capture into a new incident": a stored intent is resent exactly as
   * stored, whatever the current window, until its outcome is known; a new
   * request id is minted only when there is no stored intent. After a
   * conflict, or once an unresolved intent is stale, it does nothing: only
   * "Create a new incident anyway" creates.
   */
  const captureNew = () =>
    run(async (signal) => {
      const stored = readPendingCapture(key);
      if (
        conflictSeen.peek() ||
        isPendingConflict(stored) ||
        isStaleIntent(stored, Date.now())
      ) {
        return null;
      }
      if (stored?.id) return captureInto(stored.id, signal);
      return createAndCapture(stored ?? freshIntent(), signal);
    });

  /** After a conflict, the operator's explicit choice to create anyway. */
  const createAnyway = () =>
    run((signal) => {
      conflictSeen.value = false;
      return createAndCapture(freshIntent(), signal);
    });

  const captureExisting = (id: string) =>
    run(async (signal) => {
      const before = readPendingCapture(key);
      await capture(id, signal);
      // The target is now captured: no pending create intent is owed. Every
      // write here is guarded: when a create from an earlier mount finished
      // in the meantime, its incident id stays recorded, so the next visit
      // offers to capture into it. An intent whose create outcome is still
      // unknown (no incident id) is not dropped silently, because that
      // create may have committed: it becomes the "may already exist"
      // notice, with the link to the incident list, so the next visit says
      // so. A late create from an earlier mount then finds that notice, not
      // its intent, and records nothing more.
      const unresolved =
        before !== null && !isPendingConflict(before) && !before.id;
      setPending(unresolved ? { conflict: true } : null, signal, (current) =>
        sameRecord(current, before),
      );
      return id;
    });

  const inFlight = busy.value;
  // Read after the version so a write or clear re-renders the label and link.
  void pendingVersion.value;
  const pending = userId ? readPendingCapture(key) : null;
  // A conflict, or an unresolved intent too old to resend silently, both
  // read as "an incident for this capture may already exist".
  const conflict =
    conflictSeen.value ||
    isPendingConflict(pending) ||
    isStaleIntent(pending, Date.now());
  const created = conflict ? null : (pending?.id ?? null);

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
            {conflict && (
              <div
                role="alert"
                data-testid="capture-create-conflict"
                class="flex flex-col gap-2"
              >
                <Alert variant="warning">
                  {createConflictText("this capture")}
                </Alert>
                <a href={INCIDENTS_HREF} class={LINK_CLASS}>
                  Check your incidents
                </a>
              </div>
            )}
            {error.value && (
              <div role="alert" class="flex flex-col gap-2">
                <Alert variant="error">{error.value}</Alert>
                {created && !conflict && (
                  <a href={incidentHref(created)} class={LINK_CLASS}>
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
            {conflict ? (
              <button
                ref={newButtonRef}
                type="button"
                aria-disabled={inFlight}
                data-testid="capture-create-anyway"
                onClick={createAnyway}
                class={BUTTON_PRIMARY}
              >
                Create a new incident anyway
              </button>
            ) : (
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
            )}
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
