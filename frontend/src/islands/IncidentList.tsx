/** @jsxImportSource react */
import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  createIncident,
  isPersistenceUnavailable,
  listIncidents,
} from "@/lib/incident-api.ts";
import {
  clearPendingCapture,
  isPendingConflict,
  isStaleConflict,
  isStaleIntent,
  newClientRequestId,
  newIncidentFormKey,
  type PendingCapture,
  readPendingCapture,
  writePendingCapture,
} from "@/lib/incident-create.ts";
import {
  DEFAULT_INCIDENT_WINDOW_MS,
  INCIDENT_MAX_SUMMARY_CHARS,
  INCIDENT_MAX_TITLE_CHARS,
  type IncidentView,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import {
  createConflictText,
  createRefusedForGood,
  isCreateConflict,
  keyedCreateErrorText,
} from "@/src/components/incidents/errors.ts";
import {
  BUTTON_PRIMARY,
  BUTTON_SECONDARY,
  FIELD,
  HEADING,
  LINK,
  StatusBadge,
} from "@/src/components/incidents/ui.tsx";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

/**
 * The incidents the caller owns or was granted (GET /v1/incidents), newest
 * first, with a minimal "New incident" form that creates one and opens it.
 *
 * Three non-list states are kept distinct on purpose:
 *   - no database (`incident_persistence_unavailable`): a property of the
 *     installation. It never reads as "no incidents", and there is nothing to
 *     create into or retry.
 *   - a failed read: retryable, and the rows already shown stay.
 *   - an empty list: offers the form.
 *
 * Paging is cursor-based: "Load more" appends the next page. While it loads
 * the button stays mounted and focused (aria-disabled, not disabled). Every
 * Load more or Retry activation is a new request with its own sequence
 * number, so asking again for a page that failed always issues a fetch; the
 * previous failure's message is cleared as the new attempt starts, and the
 * Retry control stays mounted (inactive) until that attempt settles.
 *
 * The root element is identical during SSR and after hydration (the loading
 * state renders on the server), so there is no placeholder root to diverge.
 */

const ROOT_CLASS = "flex flex-col gap-5";
const PAGE_SIZE = 50;
const TITLE_ID = "incident-title";
const NOT_READY_ID = "new-incident-not-ready";

const incidentHref = (id: string) =>
  `/observability/incidents/${encodeURIComponent(id)}`;

/** A Date as the value of an `<input type="datetime-local">` (local wall time, minutes). */
function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(
    d.getHours(),
  )}:${pad(d.getMinutes())}`;
}

/** A datetime-local value as an RFC 3339 instant, or null when unparseable. */
function fromLocalInput(value: string): string | null {
  const t = new Date(value);
  return Number.isNaN(t.getTime()) ? null : t.toISOString();
}

function listErrorText(err: unknown): string {
  if (err instanceof ApiError && err.reason === "incident_busy") {
    return "The incident store is busy. Try again in a moment.";
  }
  if (err instanceof ApiError && err.status === 503) {
    return "The incident store could not be reached. Try again.";
  }
  return "Could not load incidents.";
}

function RoleBadge({ incident }: { incident: IncidentView }) {
  if (incident.role === "owner") {
    return (
      <span className="inline-flex rounded-full bg-accent-dim px-2 py-0.5 text-xs font-medium text-accent">
        Owner
      </span>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <span className="inline-flex rounded-full border border-border-primary px-2 py-0.5 text-xs font-medium text-text-secondary">
        Collaborator
      </span>
      {!incident.canAnnotate && (
        <span className="text-xs text-text-muted">read-only</span>
      )}
    </span>
  );
}

function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={at}>
      {timeAgo(at)}
    </time>
  );
}

/**
 * A create whose outcome is not known yet: its request id, the inputs it was
 * last sent with, whether any attempt with it ended with an unknown outcome,
 * and when it was minted and last sent (epoch ms; the stored record's age
 * bound runs from the last send). Kept by the list island, not the form, so
 * closing and reopening the form neither forgets it nor mints a new id, and
 * recorded for the signed-in user so a reload does not either.
 */
interface Outstanding {
  requestId: string;
  payload: CreatePayload;
  uncertain: boolean;
  createdAt: number;
  sentAt?: number;
}

interface CreatePayload {
  title: string;
  summary: string;
  windowStart: string;
  windowEnd?: string;
}

/**
 * What the form must not forget when it closes: the outstanding create, and
 * whether a create met `client_request_id_conflict` (so a reopened form
 * still shows the notice and creates only through "anyway") and when the
 * create that met it was sent (the conflict's age bound runs from it).
 */
interface CreateState {
  intent: Outstanding | null;
  conflict: boolean;
  conflictAt?: number;
}

/**
 * Records `state` for `userId`. The form sends nothing until the user is
 * known and their record restored, so a null `userId` (logged out) writes
 * nothing. With `owner`, the write happens only while the stored record is
 * still that create's, so an answer that lands after a logout cleared every
 * record does not bring this one back.
 */
function recordCreateState(
  userId: string | null,
  state: CreateState,
  owner?: string,
) {
  if (!userId) return;
  const key = newIncidentFormKey(userId);
  if (owner !== undefined) {
    const current = readPendingCapture(key);
    if (isPendingConflict(current) || current?.requestId !== owner) return;
  }
  const { intent } = state;
  const rec: PendingCapture | null = state.conflict
    ? {
        conflict: true,
        ...(state.conflictAt !== undefined ? { sentAt: state.conflictAt } : {}),
      }
    : intent && {
        requestId: intent.requestId,
        ...intent.payload,
        createdAt: intent.createdAt,
        ...(intent.sentAt !== undefined ? { sentAt: intent.sentAt } : {}),
      };
  if (rec) writePendingCapture(key, rec);
  else clearPendingCapture(key);
}

/**
 * What `userId` recorded, as form state. A create or a conflict whose
 * create was sent more than `PENDING_INTENT_MAX_AGE_MS` ago is dropped: no
 * create can still be in flight by then, so the list this page shows is the
 * answer to whether one committed. A restored create is uncertain: the page
 * that sent it never saw its outcome, so a later 400 or 413 keeps its id.
 */
function restoreCreateState(userId: string, now: number): CreateState {
  const key = newIncidentFormKey(userId);
  const rec = readPendingCapture(key);
  if (rec === null || isStaleIntent(rec, now) || isStaleConflict(rec, now)) {
    if (rec !== null) clearPendingCapture(key);
    return { intent: null, conflict: false };
  }
  if (isPendingConflict(rec)) {
    return { intent: null, conflict: true, conflictAt: rec.sentAt };
  }
  const { requestId, title, summary, windowStart, windowEnd } = rec;
  return {
    intent: {
      requestId,
      payload: {
        title,
        summary,
        windowStart,
        ...(windowEnd ? { windowEnd } : {}),
      },
      uncertain: true,
      createdAt: rec.createdAt,
      sentAt: rec.sentAt,
    },
    conflict: false,
  };
}

/**
 * The "New incident" form. Creates the incident, then opens it.
 *
 * Each create carries a `clientRequestId` (U25c). While the last attempt's
 * outcome is unknown (a network error, a 5xx, `incident_busy`, or any
 * refusal that does not prove nothing was made), every later submit sends
 * the same id with the CURRENT inputs: the server creates if nothing
 * committed, returns the incident the first attempt made if the inputs are
 * unchanged, or answers `client_request_id_conflict` if different inputs
 * committed. So editing a field after a lost answer can never make a second
 * incident silently. On a conflict the id is dropped, a notice links to the
 * incident list, and only "Create a new incident anyway" creates, with a new
 * id. A new id is otherwise minted only when no attempt is outstanding.
 *
 * A 400 or 413 proves nothing was made only while no earlier attempt with
 * the id had an unknown outcome (the server validates before it writes, so
 * the same inputs never committed); after one, the id is kept, because the
 * earlier attempt may have committed different inputs. No database drops it
 * either way.
 *
 * The outstanding create and the conflict state live in the list island
 * (`state`), so a Cancel and reopen restores them: the form opens prefilled
 * with the inputs the outstanding create was last sent with and says why
 * until the next submit, and submitting resends its id; after a conflict it
 * opens with the notice, and only "Create a new incident anyway" creates.
 *
 * They are also recorded for the signed-in user (`recordCreateState`,
 * written before each create is sent), so a reload, or the notice's link
 * back to this page, restores them the same way the next time the form
 * opens. The form does not open by itself. A confirmed success, a refusal
 * that drops the id, and logout clear the record; one whose create was sent
 * more than `PENDING_INTENT_MAX_AGE_MS` ago is ignored.
 *
 * Nothing is sent until the signed-in user is known and their record has
 * been restored into `state` (`ready`): a create sent earlier would mint a
 * new id while an outcome-unknown one is still recorded. Until then the
 * create controls are inactive and say why (`notReadyReason`). A record
 * restored while the form is already open is used by the next submit, and a
 * restored conflict shows its notice at once.
 */
function NewIncidentForm({
  onCancel,
  state,
  userId,
  ready,
  notReadyReason,
}: {
  onCancel: () => void;
  state: CreateState;
  /** Whose record the form writes. */
  userId: string | null;
  /** The user is known and their record restored: a create may be sent. */
  ready: boolean;
  /** Why nothing can be sent yet, while `ready` is false. */
  notReadyReason: string;
}) {
  // Snapshotted once: the form prefills from, and announces, only what was
  // outstanding when it opened, never a create this form starts itself.
  const restored = useRef(state.intent?.payload).current;
  const [showRestored, setShowRestored] = useState(restored !== undefined);
  const [title, setTitle] = useState(restored?.title ?? "");
  const [summary, setSummary] = useState(restored?.summary ?? "");
  const [windowStart, setWindowStart] = useState(() =>
    toLocalInput(
      restored
        ? new Date(restored.windowStart)
        : new Date(Date.now() - DEFAULT_INCIDENT_WINDOW_MS),
    ),
  );
  const [windowEnd, setWindowEnd] = useState(
    restored?.windowEnd ? toLocalInput(new Date(restored.windowEnd)) : "",
  );
  // Written before the request is sent, so a second activation before the
  // next render is already a no-op.
  const [submitting, setSubmitting, submittingRef] = useStateRef(false);
  const [error, setError] = useState<string | null>(null);
  /** A create met `client_request_id_conflict` (kept in `state` too). */
  const [conflict, setConflict] = useState(state.conflict);
  /**
   * The notice shows for a conflict this form met, or one restored into
   * `state` after it opened (the island re-renders when it restores).
   */
  const inConflict = conflict || state.conflict;
  /**
   * Aborted when the form unmounts. A create still in flight then neither
   * navigates nor sets this form's state, but it still settles the
   * island's state and the stored record, which outlive the form: the abort
   * itself leaves the outcome unknown, so the id is kept as uncertain. The
   * server may already have committed it, which is also why Cancel is
   * inactive while a create is pending.
   */
  const lifetime = useRef<AbortController | null>(null);
  // Input does not forward refs, so the field is reached by its id.
  useEffect(() => {
    lifetime.current = new AbortController();
    document.getElementById(TITLE_ID)?.focus();
    return () => lifetime.current?.abort();
  }, []);

  /** Validates and creates; `fresh` forces a new request id. */
  const create = async (fresh: boolean) => {
    if (submittingRef.current || !ready) return;
    const start = fromLocalInput(windowStart);
    if (!title.trim()) {
      setError("Give the incident a title.");
      return;
    }
    if (!start) {
      setError("Enter when the incident window starts.");
      return;
    }
    let end: string | undefined;
    if (windowEnd) {
      const parsed = fromLocalInput(windowEnd);
      if (!parsed) {
        setError("The window end is not a valid time.");
        return;
      }
      if (parsed < start) {
        setError("The window end must not be before its start.");
        return;
      }
      end = parsed;
    }
    const payload: CreatePayload = {
      title: title.trim(),
      summary,
      windowStart: start,
      ...(end ? { windowEnd: end } : {}),
    };
    // An outstanding id is resent even with edited inputs (see the doc
    // above); only "anyway" or nothing outstanding mints a new one.
    const now = Date.now();
    if (fresh || !state.intent) {
      state.intent = {
        requestId: newClientRequestId(),
        payload,
        uncertain: false,
        createdAt: now,
      };
    }
    const sent = state.intent;
    sent.payload = payload;
    sent.sentAt = now;
    state.conflict = false;
    state.conflictAt = undefined;
    // Recorded before the request, so a reload while it is in flight, or
    // after its answer was lost, resends this id.
    recordCreateState(userId, state);
    setError(null);
    setConflict(false);
    setShowRestored(false);
    setSubmitting(true);
    const signal = lifetime.current?.signal;
    try {
      const res = await createIncident(
        { ...payload, clientRequestId: sent.requestId },
        signal,
      );
      // Settled, so nothing is outstanding. The state and the record outlive
      // this form, so they are settled even when it has unmounted.
      if (state.intent === sent) {
        state.intent = null;
        recordCreateState(userId, state, sent.requestId);
      }
      if (signal?.aborted) return;
      globalThis.location.assign(incidentHref(res.incident.id));
    } catch (err) {
      // Attempts are serialized (submitting, and Cancel is inactive while
      // one is pending), so `sent` is still the outstanding intent here.
      if (state.intent === sent) {
        if (
          isCreateConflict(err) ||
          isPersistenceUnavailable(err) ||
          (createRefusedForGood(err) && !sent.uncertain)
        ) {
          state.intent = null;
        } else if (!createRefusedForGood(err)) {
          sent.uncertain = true;
        }
        if (isCreateConflict(err)) {
          state.conflict = true;
          state.conflictAt = sent.sentAt;
        }
        recordCreateState(userId, state, sent.requestId);
      }
      if (signal?.aborted) return;
      if (isCreateConflict(err)) setConflict(true);
      else setError(keyedCreateErrorText(err, "this request"));
      setSubmitting(false);
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    // After a conflict only the explicit "anyway" action creates, including
    // one restored into `state` after the form opened.
    if (conflict || state.conflict) {
      setConflict(true);
      return;
    }
    void create(false);
  };

  return (
    <form
      aria-labelledby="new-incident-heading"
      onSubmit={submit}
      className="flex flex-col gap-4 rounded-lg border border-border-subtle bg-surface p-4"
    >
      <h2 id="new-incident-heading" className={HEADING}>
        New incident
      </h2>
      {error && (
        <div role="alert">
          <Alert variant="error">{error}</Alert>
        </div>
      )}
      {showRestored && !inConflict && !error && (
        <p
          data-testid="new-incident-restored"
          className="m-0 text-sm text-text-secondary"
        >
          Restored from a create whose outcome is not known. Submitting again
          cannot make a second incident.
        </p>
      )}
      {inConflict && (
        <div
          role="alert"
          data-testid="new-incident-conflict"
          className="flex flex-col gap-2"
        >
          <Alert variant="warning">{createConflictText("this request")}</Alert>
          <a href="/observability/incidents" className={`text-sm ${LINK}`}>
            Check your incidents
          </a>
        </div>
      )}
      <Input
        id={TITLE_ID}
        label="Title"
        required
        maxLength={INCIDENT_MAX_TITLE_CHARS}
        value={title}
        onChange={(e) => setTitle(e.currentTarget.value)}
      />
      <div className="space-y-1">
        <label
          htmlFor="incident-summary"
          className="block text-sm font-medium text-text-secondary"
        >
          Summary
        </label>
        <textarea
          id="incident-summary"
          rows={3}
          maxLength={INCIDENT_MAX_SUMMARY_CHARS}
          value={summary}
          onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
            setSummary(e.currentTarget.value)
          }
          className={FIELD}
        />
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Input
          id="incident-window-start"
          type="datetime-local"
          label="Window start"
          required
          description="Evidence is read against this window. Defaults to an hour ago."
          value={windowStart}
          onChange={(e) => setWindowStart(e.currentTarget.value)}
        />
        <Input
          id="incident-window-end"
          type="datetime-local"
          label="Window end"
          description="Leave empty while the incident is ongoing."
          value={windowEnd}
          onChange={(e) => setWindowEnd(e.currentTarget.value)}
        />
      </div>
      {!ready && (
        <p
          id={NOT_READY_ID}
          role="status"
          data-testid="new-incident-not-ready"
          className="m-0 text-sm text-text-muted"
        >
          {notReadyReason}
        </p>
      )}
      <div className="flex items-center gap-3">
        {inConflict ? (
          <button
            type="button"
            aria-disabled={submitting || !ready}
            aria-describedby={ready ? undefined : NOT_READY_ID}
            data-testid="new-incident-create-anyway"
            onClick={() => void create(true)}
            className={BUTTON_PRIMARY}
          >
            {submitting ? "Creating…" : "Create a new incident anyway"}
          </button>
        ) : (
          <button
            type="submit"
            aria-disabled={submitting || !ready}
            aria-describedby={ready ? undefined : NOT_READY_ID}
            data-testid="new-incident-create"
            className={BUTTON_PRIMARY}
          >
            {submitting ? "Creating…" : "Create incident"}
          </button>
        )}
        <button
          type="button"
          aria-disabled={submitting}
          onClick={() => {
            if (!submittingRef.current) onCancel();
          }}
          className={BUTTON_SECONDARY}
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

export default function IncidentList() {
  /** Rows loaded so far, across pages. */
  const [items, setItems, itemsRef] = useStateRef<IncidentView[]>([]);
  /** The cursor of the next page; undefined once the last page loaded. */
  const [nextCursor, setNextCursor, nextCursorRef] = useStateRef<
    string | undefined
  >(undefined);
  /** Whether the first page has loaded at least once. */
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading, loadingRef] = useStateRef(true);
  const [error, setError] = useState<string | null>(null);
  /** The last request failed; keeps Retry mounted while it is re-attempted. */
  const [failed, setFailed] = useState(false);
  const [noDatabase, setNoDatabase] = useState(false);
  const [formOpen, setFormOpen] = useState(false);
  /** The outstanding create and conflict state, across Cancel and reopen. */
  const createState = useRef<CreateState>({ intent: null, conflict: false });
  /** The signed-in user; the top bar loads it, possibly after this mounts. */
  const auth = useAuth();
  const user = useSignalValue(auth.user);
  const authLoading = useSignalValue(auth.loading);
  const authLoadAttempted = useSignalValue(auth.loadAttempted);
  const userId = user?.id ?? null;
  /**
   * The user whose record `createState` was restored from. The form sends
   * nothing until it is the signed-in user, and setting it re-renders an
   * open form so a restored conflict shows at once.
   */
  const [restoredFor, setRestoredFor, restoredForRef] = useStateRef<
    string | null
  >(null);

  // Restores what this user recorded before a reload, once the user is
  // known. The state is mutated in place: an open form holds this object.
  useEffect(() => {
    if (restoredForRef.current === userId) return;
    const state = createState.current;
    // Signed out, or another user signed in on this page: nothing of the
    // previous user's create carries over in memory (logout cleared their
    // record, and another user's key never reads it). The form closes too:
    // it was filled from, and may say it restored, the previous user's
    // create, so the next user opens it afresh.
    if (restoredForRef.current !== null) {
      state.intent = null;
      state.conflict = false;
      state.conflictAt = undefined;
      setFormOpen(false);
    }
    if (!userId) {
      setRestoredFor(null);
      return;
    }
    // The state is empty here: only a create sets it, a create waits for
    // this restore, and the reset above empties it whenever the user changes.
    Object.assign(state, restoreCreateState(userId, Date.now()));
    setRestoredFor(userId);
  }, [userId]);
  const createReady = userId !== null && restoredFor === userId;
  /**
   * The signed-in user is no longer the one whose create the island holds.
   * For the render before the effect above runs, the form is not shown, so
   * the previous user's draft is never on screen for the next one.
   */
  const userChanged = restoredFor !== null && restoredFor !== userId;
  // "Could not be loaded" only once the load finished without a user; while
  // it runs, or between the user arriving and their record being restored,
  // the form is still loading.
  const notReadyReason =
    userId !== null || authLoading || !authLoadAttempted
      ? "Your sign-in details are still loading. You can create the incident once they have."
      : "Your sign-in details could not be loaded. Reload the page to create an incident.";
  /**
   * The request to make: the cursor to continue from ("" for the first page)
   * and a sequence number every activation increments, so asking again for
   * the same cursor still re-runs the fetch effect.
   */
  const [request, setRequest, requestRef] = useStateRef<{
    cursor: string;
    seq: number;
  }>({
    cursor: "",
    seq: 0,
  });
  const newButtonRef = useRef<HTMLButtonElement>(null);
  /** Set when the form closes, so focus returns to its trigger once it renders. */
  const restoreFocus = useRef(false);

  useEffect(() => {
    document.title = "Incidents - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, []);

  const { cursor, seq } = request;
  // biome-ignore lint/correctness/useExhaustiveDependencies: `seq` is not read, but a new value re-runs the fetch when the same cursor is asked for again
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    // The previous failure is stale once a new attempt starts; Retry stays
    // mounted through `failed` until this attempt settles.
    setError(null);
    listIncidents(
      { limit: PAGE_SIZE, continue: cursor || undefined },
      controller.signal,
    )
      .then((page) => {
        if (controller.signal.aborted) return;
        setItems(cursor ? [...itemsRef.current, ...page.items] : page.items);
        setNextCursor(page.continue);
        setLoaded(true);
        setFailed(false);
        setLoading(false);
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (isPersistenceUnavailable(err)) {
          // Not retryable: a retry that lands here must not leave Retry behind.
          setNoDatabase(true);
          setFailed(false);
        } else {
          setError(listErrorText(err));
          setFailed(true);
        }
        setLoading(false);
      });
    return () => controller.abort();
  }, [cursor, seq]);

  const issue = (to: string) => {
    // Busy from the click itself, not from the effect a frame later, so a
    // second activation before the effect runs is already a no-op.
    setLoading(true);
    setRequest({ cursor: to, seq: requestRef.current.seq + 1 });
  };
  const retry = () => {
    if (loadingRef.current) return;
    issue(requestRef.current.cursor);
  };
  const loadMore = () => {
    const next = nextCursorRef.current;
    if (loadingRef.current || !next) return;
    issue(next);
  };
  const closeForm = () => {
    restoreFocus.current = true;
    setFormOpen(false);
  };
  // The form's controls are gone once it closes; return focus to the control
  // that opened it after that control has rendered again.
  useEffect(() => {
    if (!formOpen && restoreFocus.current) {
      restoreFocus.current = false;
      newButtonRef.current?.focus();
    }
  }, [formOpen]);

  const busy = loading;
  const rows = items;
  const canCreate = !noDatabase;

  return (
    <div className={ROOT_CLASS}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="m-0 text-2xl font-bold tracking-tight text-text-primary">
            Incidents
          </h1>
          <p className="m-0 text-sm text-text-muted">
            Investigations you own or were invited to. Each one collects
            evidence from the cluster and notes from the people working it.
          </p>
        </div>
        {canCreate && !formOpen && (
          <button
            type="button"
            ref={newButtonRef}
            onClick={() => {
              setFormOpen(true);
            }}
            className={BUTTON_PRIMARY}
          >
            New incident
          </button>
        )}
      </div>

      {canCreate && formOpen && !userChanged && (
        <NewIncidentForm
          onCancel={closeForm}
          state={createState.current}
          userId={userId}
          ready={createReady}
          notReadyReason={notReadyReason}
        />
      )}

      {noDatabase && (
        <div
          role="status"
          className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-4"
        >
          <p className="m-0 text-sm font-semibold text-text-primary">
            Incidents are unavailable: this deployment has no database
          </p>
          <p className="m-0 text-sm text-text-secondary">
            Incidents are stored in PostgreSQL. Configure a database for
            k8sCenter to record incidents here.
          </p>
        </div>
      )}

      {busy && !loaded && !noDatabase && (
        <p role="status" className="m-0 text-sm text-text-muted">
          Loading incidents…
        </p>
      )}

      {failed && !noDatabase && (
        <div className="flex flex-col items-start gap-2">
          {error && (
            <div role="alert" className="w-full">
              <Alert variant="error">{error}</Alert>
            </div>
          )}
          <div className="flex items-center gap-3 text-sm">
            <button
              type="button"
              aria-disabled={busy}
              onClick={retry}
              className={BUTTON_SECONDARY}
            >
              Retry
            </button>
            {busy && (
              <span role="status" className="text-text-muted">
                Retrying…
              </span>
            )}
          </div>
        </div>
      )}

      {loaded && rows.length === 0 && !failed && (
        <p className="m-0 text-sm text-text-secondary">
          No incidents yet. Open one with “New incident” when you start
          investigating a problem.
        </p>
      )}

      {rows.length > 0 && (
        <div
          aria-busy={busy}
          className="overflow-x-auto rounded-lg border border-border-subtle bg-surface"
        >
          <table className="w-full border-collapse text-left text-sm">
            <caption className="sr-only">Your incidents</caption>
            <thead>
              <tr className="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" className="px-3 py-2 font-semibold">
                  Incident
                </th>
                <th scope="col" className="px-3 py-2 font-semibold">
                  Status
                </th>
                <th scope="col" className="px-3 py-2 font-semibold">
                  Your role
                </th>
                <th scope="col" className="px-3 py-2 font-semibold">
                  Created
                </th>
                <th scope="col" className="px-3 py-2 font-semibold">
                  Updated
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((incident) => (
                <tr key={incident.id} className="border-t border-border-subtle">
                  <td className="px-3 py-2 align-top">
                    <a
                      href={incidentHref(incident.id)}
                      className={`font-medium ${LINK}`}
                    >
                      {incident.title}
                    </a>
                  </td>
                  <td className="px-3 py-2 align-top">
                    <StatusBadge status={incident.status} />
                  </td>
                  <td className="px-3 py-2 align-top">
                    <RoleBadge incident={incident} />
                  </td>
                  <td className="px-3 py-2 align-top text-text-secondary">
                    <When at={incident.createdAt} />
                  </td>
                  <td className="px-3 py-2 align-top text-text-secondary">
                    <When at={incident.updatedAt} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {nextCursor && rows.length > 0 && (
        <div className="flex items-center gap-3 text-sm">
          <button
            type="button"
            aria-disabled={busy}
            onClick={loadMore}
            className={BUTTON_SECONDARY}
          >
            Load more
          </button>
          {busy && !failed && (
            <span role="status" className="text-text-muted">
              Loading more incidents…
            </span>
          )}
        </div>
      )}
    </div>
  );
}
