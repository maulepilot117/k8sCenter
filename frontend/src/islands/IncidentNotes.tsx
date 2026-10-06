import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
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
  conflict: { current?: number; reloaded: boolean } | null;
  saving: boolean;
  error: string | null;
}

function NoteBody({ body }: { body: string }) {
  return (
    <p class="m-0 whitespace-pre-wrap break-words text-sm text-text-primary">
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
    <div class="flex flex-col gap-2" data-note-editor={edit.noteId}>
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
        <div class="flex flex-col gap-1">
          <span class="text-xs text-text-muted">
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
      <label class="sr-only" for={fieldId}>
        Edit note
      </label>
      <textarea
        id={fieldId}
        rows={4}
        maxLength={INCIDENT_MAX_NOTE_BODY_CHARS}
        value={edit.draft}
        disabled={edit.saving}
        onInput={(ev) => onDraft(ev.currentTarget.value)}
        class={FIELD}
      />
      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-disabled={edit.saving}
          onClick={onSave}
          class={BUTTON_PRIMARY}
        >
          {edit.saving ? "Saving…" : "Save"}
        </button>
        {edit.conflict && (
          <button
            type="button"
            aria-disabled={busy}
            onClick={onReload}
            class={BUTTON_SECONDARY}
          >
            Reload notes
          </button>
        )}
        <button
          type="button"
          aria-disabled={edit.saving}
          onClick={onCancel}
          class={BUTTON_SECONDARY}
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
  const notes = useSignal<NoteView[]>([]);
  const nextCursor = useSignal<string | undefined>(undefined);
  const loaded = useSignal(false);
  const loading = useSignal(true);
  const loadError = useSignal<string | null>(null);
  const request = useSignal<{ cursor: string; seq: number }>({
    cursor: "",
    seq: 0,
  });
  const draft = useSignal("");
  const posting = useSignal(false);
  const postError = useSignal<string | null>(null);
  const postNotice = useSignal<string | null>(null);
  const editing = useSignal<Editing | null>(null);
  const confirmDelete = useSignal<string | null>(null);
  const deleting = useSignal<string | null>(null);
  const deleteError = useSignal<string | null>(null);

  const { cursor, seq } = request.value;
  useEffect(() => {
    const controller = new AbortController();
    loading.value = true;
    loadError.value = null;
    listNotes(
      incidentId,
      { limit: PAGE_SIZE, continue: cursor || undefined },
      controller.signal,
    )
      .then((page) => {
        if (controller.signal.aborted) return;
        if (cursor) {
          const seen = new Set(notes.value.map((n) => n.id));
          notes.value = [
            ...notes.value,
            ...page.items.filter((n) => !seen.has(n.id)),
          ];
        } else {
          notes.value = page.items;
        }
        // After a conflict the user asked to see the current text: bind the
        // editor to what was just loaded, so the next save carries that
        // revision. Without a conflict the editor keeps the revision it was
        // opened on, so someone else's later save still surfaces as a 409
        // instead of being overwritten silently.
        const e = editing.peek();
        const fresh = e?.conflict && page.items.find((n) => n.id === e.noteId);
        if (e?.conflict && fresh && !e.saving) {
          editing.value = {
            ...e,
            revision: fresh.revision,
            loadedBody: fresh.body,
            conflict: { ...e.conflict, reloaded: true },
          };
        }
        nextCursor.value = page.continue;
        loaded.value = true;
        loading.value = false;
      })
      .catch(() => {
        if (controller.signal.aborted) return;
        loadError.value = "Could not load notes.";
        loading.value = false;
      });
    return () => controller.abort();
  }, [incidentId, cursor, seq]);

  const issue = (to: string) => {
    loading.value = true;
    request.value = { cursor: to, seq: request.peek().seq + 1 };
  };
  const reload = () => {
    if (!loading.value) issue("");
  };
  const loadMore = () => {
    if (loading.value || !nextCursor.value) return;
    issue(nextCursor.value);
  };

  const post = async (e: Event) => {
    e.preventDefault();
    if (posting.value) return;
    const body = draft.value;
    if (!body.trim()) {
      postError.value = "Write something first.";
      return;
    }
    posting.value = true;
    postError.value = null;
    postNotice.value = null;
    try {
      const note = await createNote(incidentId, body);
      draft.value = "";
      if (nextCursor.peek() === undefined) {
        // The whole thread is loaded, so the newest note goes last.
        notes.value = [...notes.value.filter((n) => n.id !== note.id), note];
      } else {
        postNotice.value =
          "Note added. It appears at the end of the thread; load more notes to see it.";
      }
    } catch (err) {
      postError.value = noteErrorText(err, "added");
    } finally {
      posting.value = false;
    }
  };

  const startEdit = (note: NoteView) => {
    editing.value = {
      noteId: note.id,
      draft: note.body,
      revision: note.revision,
      loadedBody: note.body,
      conflict: null,
      saving: false,
      error: null,
    };
  };

  const setDraft = (value: string) => {
    const cur = editing.peek();
    if (cur && !cur.saving) editing.value = { ...cur, draft: value };
  };

  const saveEdit = async () => {
    const e = editing.value;
    if (!e || e.saving) return;
    if (!e.draft.trim()) {
      editing.value = { ...e, error: "A note cannot be empty." };
      return;
    }
    editing.value = { ...e, saving: true, error: null };
    try {
      const saved = await updateNote(incidentId, e.noteId, e.draft, e.revision);
      notes.value = notes.value.map((n) => (n.id === saved.id ? saved : n));
      editing.value = null;
    } catch (err) {
      const current = editing.value ?? e;
      if (err instanceof ApiError && err.reason === "note_revision_conflict") {
        editing.value = {
          ...current,
          saving: false,
          conflict: {
            current: incidentErrorNumber(err, "currentRevision"),
            reloaded: false,
          },
        };
      } else {
        editing.value = {
          ...current,
          saving: false,
          error: noteErrorText(err, "saved"),
        };
      }
    }
  };

  const cancelEdit = () => {
    if (!editing.peek()?.saving) editing.value = null;
  };

  const remove = async (noteId: string) => {
    if (deleting.value) return;
    deleting.value = noteId;
    deleteError.value = null;
    try {
      await deleteNote(incidentId, noteId);
      notes.value = notes.value.filter((n) => n.id !== noteId);
      confirmDelete.value = null;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        // Already gone (another tab, or a retried click): the outcome the
        // user asked for.
        notes.value = notes.value.filter((n) => n.id !== noteId);
        confirmDelete.value = null;
      } else {
        deleteError.value = noteErrorText(err, "deleted");
      }
    } finally {
      deleting.value = null;
    }
  };

  const busy = loading.value;
  const edit = editing.value;
  const editMissing =
    edit !== null && !notes.value.some((n) => n.id === edit.noteId);
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
      class="flex flex-col gap-3"
    >
      <h2 id="incident-notes-heading" class={HEADING}>
        Notes
      </h2>

      {busy && !loaded.value && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading notes…
        </p>
      )}
      {loadError.value && (
        <div role="alert">
          <Alert variant="error">{loadError.value}</Alert>
        </div>
      )}
      {loaded.value && notes.value.length === 0 && !editMissing && (
        <p class="m-0 text-sm text-text-secondary">No notes yet.</p>
      )}
      {deleteError.value && (
        <div role="alert">
          <Alert variant="error">{deleteError.value}</Alert>
        </div>
      )}

      {editorProps && editMissing && (
        <div class="rounded-lg border border-border-subtle bg-surface p-3">
          <NoteEditor {...editorProps} missing />
        </div>
      )}

      {notes.value.length > 0 && (
        <ol aria-label="Notes" class="m-0 flex list-none flex-col gap-2 p-0">
          {notes.value.map((note) => {
            const mine =
              canAnnotate &&
              currentUserId !== null &&
              note.authorId === currentUserId;
            const isEditing = edit?.noteId === note.id;
            const removing = deleting.value === note.id;
            return (
              <li
                key={note.id}
                class="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3"
              >
                <div class="flex flex-wrap items-center gap-2 text-xs text-text-muted">
                  <span class="font-medium text-text-secondary">
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
                  <div class="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => startEdit(note)}
                      class={BUTTON_SECONDARY}
                    >
                      Edit
                    </button>
                    {confirmDelete.value === note.id ? (
                      <>
                        <button
                          type="button"
                          aria-disabled={removing}
                          onClick={() => remove(note.id)}
                          class={BUTTON_SECONDARY}
                        >
                          {removing ? "Deleting…" : "Confirm delete"}
                        </button>
                        <button
                          type="button"
                          aria-disabled={removing}
                          onClick={() => {
                            if (!removing) confirmDelete.value = null;
                          }}
                          class={BUTTON_SECONDARY}
                        >
                          Keep note
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        onClick={() => {
                          deleteError.value = null;
                          confirmDelete.value = note.id;
                        }}
                        class={BUTTON_SECONDARY}
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

      {nextCursor.value && (
        <div class="flex items-center gap-3 text-sm">
          <button
            type="button"
            aria-disabled={busy}
            onClick={loadMore}
            class={BUTTON_SECONDARY}
          >
            Load more notes
          </button>
          {busy && (
            <span role="status" class="text-text-muted">
              Loading notes…
            </span>
          )}
        </div>
      )}

      {canAnnotate ? (
        <form onSubmit={post} class="flex flex-col gap-2">
          <label
            for={`new-note-${incidentId}`}
            class="text-sm font-medium text-text-secondary"
          >
            Add a note
          </label>
          {postError.value && (
            <div role="alert">
              <Alert variant="error">{postError.value}</Alert>
            </div>
          )}
          {postNotice.value && (
            <p role="status" class="m-0 text-sm text-text-secondary">
              {postNotice.value}
            </p>
          )}
          <textarea
            id={`new-note-${incidentId}`}
            rows={3}
            maxLength={INCIDENT_MAX_NOTE_BODY_CHARS}
            value={draft.value}
            onInput={(ev) => {
              draft.value = ev.currentTarget.value;
            }}
            class={FIELD}
          />
          <div>
            <button
              type="submit"
              aria-disabled={posting.value}
              class={BUTTON_PRIMARY}
            >
              {posting.value ? "Adding…" : "Add note"}
            </button>
          </div>
        </form>
      ) : (
        <p class="m-0 text-xs text-text-muted">
          Your access to this incident is read-only, so you cannot add notes.
        </p>
      )}
    </section>
  );
}
