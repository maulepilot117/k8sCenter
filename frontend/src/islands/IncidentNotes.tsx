import { type FormEvent, useEffect, useRef, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  createNote,
  deleteNote,
  incidentErrorNumber,
  listNotes,
  updateNote,
} from "@/lib/incident-api.ts";
import {
  INCIDENT_MAX_NOTE_BODY_CHARS,
  type NoteView,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { busyText } from "@/src/components/incidents/errors.ts";
import {
  BUTTON_PRIMARY,
  BUTTON_SECONDARY,
  FIELD,
  HEADING,
} from "@/src/components/incidents/ui.tsx";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

/**
 * An incident's notes, oldest first, paged with "Load more".
 *
 * Who may write (backend rules): anyone who may annotate (the owner, or a
 * collaborator whose grant has canAnnotate) may add a note; only the note's
 * author, while still able to annotate, may edit or delete it. The controls
 * follow those rules; the server enforces them.
 *
 * Editing never loses typing:
 *   - an edit carries the revision it is bound to. A 409
 *     `note_revision_conflict` keeps the draft, shows a banner and offers
 *     "Reload notes"; the text shown beside the draft is labelled as the
 *     caller's last loaded copy until the reload brings the current one, and
 *     the next save carries the reloaded revision.
 *   - when the note being edited is not in the loaded list (a reload returns
 *     page one and the note is further on), the editor stays on screen on its
 *     own, with the draft, and says to load more to find the note.
 *   - the textarea is read-only while a save is in flight, so nothing typed
 *     during the request can be overwritten by its result.
 *
 * A new note is appended locally only when the whole thread is loaded; with
 * more pages to come it would sit out of order, so the user is told it is at
 * the end of the thread instead.
 */

const PAGE_SIZE = 50;

function noteErrorText(err: unknown, action: string): string {
  if (err instanceof ApiError) {
    if (err.reason === "incident_busy") {
      return busyText("The incident is busy.", err);
    }
    switch (err.status) {
      case 400:
        return err.detail || `The note could not be ${action}.`;
      case 403:
        return err.detail || "You are not allowed to change this note.";
      case 404:
        return "This note no longer exists.";
    }
  }
  return `The note could not be ${action}.`;
}

/** The note being edited: its draft survives any failed save. */
interface Editing {
  noteId: string;
  draft: string;
  /** The revision a save carries: the editor's, refreshed by each reload. */
  revision: number;
  /** The body as last loaded, shown beside the draft after a conflict. */
  loadedBody: string;
  /**
   * Set by a 409. `current` is the revision the server reported; `reloaded`
   * turns true once a reload has brought the note's current text.
   */
  conflict: {
    current?: number;
    reloaded: boolean;
    /** The list request sequence at the 409; only later loads rebind. */
    afterSeq: number;
  } | null;
  saving: boolean;
  error: string | null;
}

function NoteBody({ body }: { body: string }) {
  return (
    <p className="m-0 whitespace-pre-wrap break-words text-sm text-text-primary">
      {body}
    </p>
  );
}

function NoteEditor({
  edit,
  missing,
  busy,
  onDraft,
  onSave,
  onReload,
  onCancel,
}: {
  edit: Editing;
  /** The note is not in the loaded list. */
  missing: boolean;
  busy: boolean;
  onDraft: (draft: string) => void;
  onSave: () => void;
  onReload: () => void;
  onCancel: () => void;
}) {
  const fieldId = `note-edit-${edit.noteId}`;
  return (
    <div className="flex flex-col gap-2" data-note-editor={edit.noteId}>
      {missing && (
        <div role="status">
          <Alert variant="info">
            The note you are editing is not in the loaded list. Load more notes
            to find it. Your draft is kept.
          </Alert>
        </div>
      )}
      {edit.conflict && (
        <div role="alert">
          <Alert variant="warning">
            Someone saved this note after you started editing
            {edit.conflict.current !== undefined
              ? ` (it is now at revision ${edit.conflict.current})`
              : ""}
            . Your draft is kept below.{" "}
            {edit.conflict.reloaded
              ? "The current text is shown; save again to replace it."
              : "Reload notes to see the current text, then save again."}
          </Alert>
        </div>
      )}
      {edit.conflict && (
        <div className="flex flex-col gap-1">
          <span className="text-xs text-text-muted">
            {edit.conflict.reloaded
              ? "Current saved text"
              : "Your last loaded copy"}
          </span>
          <NoteBody body={edit.loadedBody} />
        </div>
      )}
      {edit.error && (
        <div role="alert">
          <Alert variant="error">{edit.error}</Alert>
        </div>
      )}
      <label className="sr-only" htmlFor={fieldId}>
        Edit note
      </label>
      <textarea
        id={fieldId}
        rows={4}
        maxLength={INCIDENT_MAX_NOTE_BODY_CHARS}
        value={edit.draft}
        disabled={edit.saving}
        onChange={(ev) => onDraft(ev.currentTarget.value)}
        className={FIELD}
      />
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-disabled={edit.saving}
          onClick={onSave}
          className={BUTTON_PRIMARY}
        >
          {edit.saving ? "Saving…" : "Save"}
        </button>
        {edit.conflict && (
          <button
            type="button"
            aria-disabled={busy}
            onClick={onReload}
            className={BUTTON_SECONDARY}
          >
            Reload notes
          </button>
        )}
        <button
          type="button"
          aria-disabled={edit.saving}
          onClick={onCancel}
          className={BUTTON_SECONDARY}
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

export default function IncidentNotes({
  incidentId,
  canAnnotate,
  currentUserId,
}: {
  incidentId: string;
  /** The caller may add notes (owner, or a grant with canAnnotate). */
  canAnnotate: boolean;
  /** The signed-in user's id, to offer edit/delete on their own notes. */
  currentUserId: string | null;
}) {
  const [notes, setNotes, notesRef] = useStateRef<NoteView[]>([]);
  const [nextCursor, setNextCursor, nextCursorRef] = useStateRef<
    string | undefined
  >(undefined);
  const [loaded, setLoaded, loadedRef] = useStateRef(false);
  const [loading, setLoading, loadingRef] = useStateRef(true);
  const [loadError, setLoadError, loadErrorRef] = useStateRef<string | null>(
    null,
  );
  const [request, setRequest, requestRef] = useStateRef<{
    cursor: string;
    seq: number;
  }>({
    cursor: "",
    seq: 0,
  });
  const [draft, setDraftText, draftRef] = useStateRef("");
  const [posting, setPosting, postingRef] = useStateRef(false);
  const [postError, setPostError] = useState<string | null>(null);
  const [postNotice, setPostNotice] = useState<string | null>(null);
  const [editing, setEditing, editingRef] = useStateRef<Editing | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [deleting, setDeleting, deletingRef] = useStateRef<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  /**
   * A note created while a list load was in flight. That load may have read
   * the thread before the note existed, so the note is reconciled when the
   * load settles instead of being appended (and then erased) immediately.
   */
  const pendingNotes = useRef<NoteView[]>([]);
  // Within one incident no load is ever superseded while one is in flight
  // (every issuer waits for `loading`), so a held note always meets the load
  // it waited for. A different incident's thread is another matter: its
  // load must never place this incident's notes.
  /** The incident on screen now; a post compares it with the one it began on. */
  const currentIncident = useRef(incidentId);
  useEffect(() => {
    currentIncident.current = incidentId;
    return () => {
      pendingNotes.current = [];
    };
  }, [incidentId]);

  const { cursor, seq } = request;
  // biome-ignore lint/correctness/useExhaustiveDependencies: reloads only when the incident, cursor or sequence changes; the settle helpers read refs
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setLoadError(null);
    listNotes(
      incidentId,
      { limit: PAGE_SIZE, continue: cursor || undefined },
      controller.signal,
    )
      .then((page) => {
        if (controller.signal.aborted) return;
        if (cursor) {
          const seen = new Set(notesRef.current.map((n) => n.id));
          setNotes([
            ...notesRef.current,
            ...page.items.filter((n) => !seen.has(n.id)),
          ]);
        } else {
          setNotes(page.items);
        }
        // After a conflict the user asked to see the current text: bind the
        // editor to what a load issued AFTER the 409 read, so the next save
        // carries that revision. An older in-flight page may predate the
        // conflicting save and is ignored for this. Without a conflict the
        // editor keeps the revision it was opened on, so someone else's
        // later save still surfaces as a 409 instead of being overwritten.
        const e = editingRef.current;
        const fresh =
          e?.conflict &&
          seq > e.conflict.afterSeq &&
          page.items.find((n) => n.id === e.noteId);
        if (e?.conflict && fresh && !e.saving) {
          setEditing({
            ...e,
            revision: fresh.revision,
            loadedBody: fresh.body,
            conflict: { ...e.conflict, reloaded: true },
          });
        }
        setNextCursor(page.continue);
        setLoaded(true);
        setLoading(false);
        settlePendingNote();
      })
      .catch(() => {
        if (controller.signal.aborted) return;
        setLoadError("Could not load notes.");
        setLoading(false);
        settlePendingNote();
      });
    return () => controller.abort();
  }, [incidentId, cursor, seq]);

  const issue = (to: string) => {
    setLoading(true);
    setRequest({ cursor: to, seq: requestRef.current.seq + 1 });
  };
  const reload = () => {
    if (!loadingRef.current) issue("");
  };
  const loadMore = () => {
    const next = nextCursorRef.current;
    if (loadingRef.current || !next) return;
    issue(next);
  };
  const retryLoad = () => {
    if (!loadingRef.current) issue(requestRef.current.cursor);
  };

  const ADDED_ELSEWHERE =
    "Note added. It appears at the end of the thread; load more notes to see it.";

  /**
   * Places a created note: appended when the loaded list is the whole,
   * settled thread; otherwise the user is told where it went.
   */
  const placeNote = (note: NoteView) => {
    const complete =
      loadedRef.current &&
      !loadErrorRef.current &&
      nextCursorRef.current === undefined;
    if (complete) {
      setNotes([...notesRef.current.filter((n) => n.id !== note.id), note]);
    } else if (!notesRef.current.some((n) => n.id === note.id)) {
      setPostNotice(
        loadedRef.current
          ? ADDED_ELSEWHERE
          : "Note added. Load the notes to see it.",
      );
    }
  };

  /** Reconciles notes created during the load that just settled. */
  function settlePendingNote() {
    const created = pendingNotes.current;
    pendingNotes.current = [];
    for (const note of created) placeNote(note);
  }

  const post = async (e: FormEvent) => {
    e.preventDefault();
    if (postingRef.current) return;
    const body = draftRef.current;
    if (!body.trim()) {
      setPostError("Write something first.");
      return;
    }
    setPosting(true);
    setPostError(null);
    setPostNotice(null);
    const postedTo = incidentId;
    try {
      const note = await createNote(postedTo, body);
      setDraftText("");
      // The view moved to another incident while the create was in flight:
      // this note belongs to the old thread, so it is neither placed nor
      // held here.
      if (currentIncident.current !== postedTo) return;
      if (loadingRef.current) {
        // A load in flight may have read the thread before this note
        // existed; place it once that load settles.
        pendingNotes.current.push(note);
      } else {
        placeNote(note);
      }
    } catch (err) {
      // An error about the old incident's note is not shown on another one.
      if (currentIncident.current === postedTo) {
        setPostError(noteErrorText(err, "added"));
      }
    } finally {
      setPosting(false);
    }
  };

  const startEdit = (note: NoteView) => {
    // A save in flight owns the editor until it settles.
    if (editingRef.current?.saving) return;
    setEditing({
      noteId: note.id,
      draft: note.body,
      revision: note.revision,
      loadedBody: note.body,
      conflict: null,
      saving: false,
      error: null,
    });
  };

  const setDraft = (value: string) => {
    const cur = editingRef.current;
    if (cur && !cur.saving) setEditing({ ...cur, draft: value });
  };

  const saveEdit = async () => {
    const e = editingRef.current;
    if (!e || e.saving) return;
    if (!e.draft.trim()) {
      setEditing({ ...e, error: "A note cannot be empty." });
      return;
    }
    setEditing({ ...e, saving: true, error: null });
    // The result belongs to this note's editor only. Nothing can replace or
    // close the editor while `saving` is set (startEdit, cancelEdit, setDraft
    // and a conflict reload all refuse), so the editor open when the result
    // lands is still this one.
    try {
      const saved = await updateNote(incidentId, e.noteId, e.draft, e.revision);
      setNotes(notesRef.current.map((n) => (n.id === saved.id ? saved : n)));
      setEditing(null);
    } catch (err) {
      const current = editingRef.current ?? e;
      if (err instanceof ApiError && err.reason === "note_revision_conflict") {
        setEditing({
          ...current,
          saving: false,
          conflict: {
            current: incidentErrorNumber(err, "currentRevision"),
            reloaded: false,
            afterSeq: requestRef.current.seq,
          },
        });
      } else {
        setEditing({
          ...current,
          saving: false,
          error: noteErrorText(err, "saved"),
        });
      }
    }
  };

  const cancelEdit = () => {
    if (!editingRef.current?.saving) setEditing(null);
  };

  const remove = async (noteId: string) => {
    if (deletingRef.current) return;
    setDeleting(noteId);
    setDeleteError(null);
    try {
      await deleteNote(incidentId, noteId);
      setNotes(notesRef.current.filter((n) => n.id !== noteId));
      setConfirmDelete(null);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        // Already gone (another tab, or a retried click): the outcome the
        // user asked for.
        setNotes(notesRef.current.filter((n) => n.id !== noteId));
        setConfirmDelete(null);
      } else {
        setDeleteError(noteErrorText(err, "deleted"));
      }
    } finally {
      setDeleting(null);
    }
  };

  const busy = loading;
  const edit = editing;
  const editMissing = edit !== null && !notes.some((n) => n.id === edit.noteId);
  const editorProps = edit && {
    edit,
    busy,
    onDraft: setDraft,
    onSave: saveEdit,
    onReload: reload,
    onCancel: cancelEdit,
  };

  return (
    <section
      aria-labelledby="incident-notes-heading"
      className="flex flex-col gap-3"
    >
      <h2 id="incident-notes-heading" className={HEADING}>
        Notes
      </h2>

      {busy && !loaded && (
        <p role="status" className="m-0 text-sm text-text-muted">
          Loading notes…
        </p>
      )}
      {loadError && (
        <div className="flex flex-col items-start gap-2">
          <div role="alert" className="w-full">
            <Alert variant="error">{loadError}</Alert>
          </div>
          <button
            type="button"
            aria-disabled={busy}
            onClick={retryLoad}
            className={BUTTON_SECONDARY}
          >
            Retry
          </button>
        </div>
      )}
      {loaded && notes.length === 0 && !editMissing && (
        <p className="m-0 text-sm text-text-secondary">No notes yet.</p>
      )}
      {deleteError && (
        <div role="alert">
          <Alert variant="error">{deleteError}</Alert>
        </div>
      )}

      {editorProps && editMissing && (
        <div className="rounded-lg border border-border-subtle bg-surface p-3">
          <NoteEditor {...editorProps} missing />
        </div>
      )}

      {notes.length > 0 && (
        <ol
          aria-label="Notes"
          className="m-0 flex list-none flex-col gap-2 p-0"
        >
          {notes.map((note) => {
            const mine =
              canAnnotate &&
              currentUserId !== null &&
              note.authorId === currentUserId;
            const isEditing = edit?.noteId === note.id;
            const removing = deleting === note.id;
            return (
              <li
                key={note.id}
                className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3"
              >
                <div className="flex flex-wrap items-center gap-2 text-xs text-text-muted">
                  <span className="font-medium text-text-secondary">
                    {note.authorId === currentUserId ? "You" : note.authorId}
                  </span>
                  <time dateTime={note.createdAt} title={note.createdAt}>
                    {timeAgo(note.createdAt)}
                  </time>
                  {note.revision > 1 && <span>(edited)</span>}
                </div>
                {isEditing && editorProps ? (
                  <NoteEditor {...editorProps} missing={false} />
                ) : (
                  <NoteBody body={note.body} />
                )}
                {mine && !isEditing && (
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => startEdit(note)}
                      className={BUTTON_SECONDARY}
                    >
                      Edit
                    </button>
                    {confirmDelete === note.id ? (
                      <>
                        <button
                          type="button"
                          aria-disabled={removing}
                          onClick={() => remove(note.id)}
                          className={BUTTON_SECONDARY}
                        >
                          {removing ? "Deleting…" : "Confirm delete"}
                        </button>
                        <button
                          type="button"
                          aria-disabled={removing}
                          onClick={() => {
                            if (!removing) setConfirmDelete(null);
                          }}
                          className={BUTTON_SECONDARY}
                        >
                          Keep note
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        onClick={() => {
                          setDeleteError(null);
                          setConfirmDelete(note.id);
                        }}
                        className={BUTTON_SECONDARY}
                      >
                        Delete
                      </button>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ol>
      )}

      {nextCursor && (
        <div className="flex items-center gap-3 text-sm">
          <button
            type="button"
            aria-disabled={busy}
            onClick={loadMore}
            className={BUTTON_SECONDARY}
          >
            Load more notes
          </button>
          {busy && (
            <span role="status" className="text-text-muted">
              Loading notes…
            </span>
          )}
        </div>
      )}

      {canAnnotate ? (
        <form onSubmit={post} className="flex flex-col gap-2">
          <label
            htmlFor={`new-note-${incidentId}`}
            className="text-sm font-medium text-text-secondary"
          >
            Add a note
          </label>
          {postError && (
            <div role="alert">
              <Alert variant="error">{postError}</Alert>
            </div>
          )}
          {postNotice && (
            <p role="status" className="m-0 text-sm text-text-secondary">
              {postNotice}
            </p>
          )}
          <textarea
            id={`new-note-${incidentId}`}
            rows={3}
            maxLength={INCIDENT_MAX_NOTE_BODY_CHARS}
            value={draft}
            disabled={posting}
            onChange={(ev) => setDraftText(ev.currentTarget.value)}
            className={FIELD}
          />
          <div>
            <button
              type="submit"
              aria-disabled={posting}
              className={BUTTON_PRIMARY}
            >
              {posting ? "Adding…" : "Add note"}
            </button>
          </div>
        </form>
      ) : (
        <p className="m-0 text-xs text-text-muted">
          Your access to this incident is read-only, so you cannot add notes.
        </p>
      )}
    </section>
  );
}
