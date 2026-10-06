import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  createIncident,
  isPersistenceUnavailable,
  listIncidents,
} from "@/lib/incident-api.ts";
import {
  INCIDENT_MAX_SUMMARY_CHARS,
  INCIDENT_MAX_TITLE_CHARS,
  type IncidentStatus,
  type IncidentView,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import {
  BUTTON_PRIMARY,
  BUTTON_SECONDARY,
  FIELD,
  HEADING,
  LINK,
} from "@/src/components/incidents/ui.tsx";

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
/** The default investigation window starts this long before "now". */
const DEFAULT_WINDOW_MS = 60 * 60 * 1000;
const TITLE_ID = "incident-title";

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

function createErrorText(err: unknown): string {
  if (isPersistenceUnavailable(err)) {
    return "This deployment has no database, so incidents cannot be recorded.";
  }
  if (err instanceof ApiError) {
    if (err.reason === "incident_busy") {
      return "The incident store is busy. Try again in a moment.";
    }
    if (err.status === 400) {
      return (
        err.body?.error?.detail || err.detail || "The incident is invalid."
      );
    }
  }
  return "Could not create the incident.";
}

function StatusBadge({ status }: { status: IncidentStatus }) {
  switch (status) {
    case "open":
      return (
        <span class="inline-flex rounded-full bg-warning-dim px-2 py-0.5 text-xs font-medium text-warning">
          Open
        </span>
      );
    case "closed":
      return (
        <span class="inline-flex rounded-full bg-success-dim px-2 py-0.5 text-xs font-medium text-success">
          Closed
        </span>
      );
    default:
      return (
        <span class="inline-flex rounded-full border border-border-subtle px-2 py-0.5 text-xs font-medium text-text-muted">
          {String(status)}
        </span>
      );
  }
}

function RoleBadge({ incident }: { incident: IncidentView }) {
  if (incident.role === "owner") {
    return (
      <span class="inline-flex rounded-full bg-accent-dim px-2 py-0.5 text-xs font-medium text-accent">
        Owner
      </span>
    );
  }
  return (
    <span class="inline-flex flex-wrap items-center gap-1.5">
      <span class="inline-flex rounded-full border border-border-primary px-2 py-0.5 text-xs font-medium text-text-secondary">
        Collaborator
      </span>
      {!incident.canAnnotate && (
        <span class="text-xs text-text-muted">read-only</span>
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

/** The "New incident" form. Creates the incident, then opens it. */
function NewIncidentForm({ onCancel }: { onCancel: () => void }) {
  const title = useSignal("");
  const summary = useSignal("");
  const windowStart = useSignal(
    toLocalInput(new Date(Date.now() - DEFAULT_WINDOW_MS)),
  );
  const windowEnd = useSignal("");
  const submitting = useSignal(false);
  const error = useSignal<string | null>(null);
  /**
   * Aborted when the form unmounts. A create still in flight then neither
   * navigates nor writes state; the server may already have committed it,
   * which is why Cancel itself is inactive while a create is pending.
   */
  const lifetime = useRef<AbortController | null>(null);
  // Input does not forward refs, so the field is reached by its id.
  useEffect(() => {
    lifetime.current = new AbortController();
    document.getElementById(TITLE_ID)?.focus();
    return () => lifetime.current?.abort();
  }, []);

  const submit = async (e: Event) => {
    e.preventDefault();
    if (submitting.value) return;
    const start = fromLocalInput(windowStart.value);
    if (!title.value.trim()) {
      error.value = "Give the incident a title.";
      return;
    }
    if (!start) {
      error.value = "Enter when the incident window starts.";
      return;
    }
    let end: string | undefined;
    if (windowEnd.value) {
      const parsed = fromLocalInput(windowEnd.value);
      if (!parsed) {
        error.value = "The window end is not a valid time.";
        return;
      }
      if (parsed < start) {
        error.value = "The window end must not be before its start.";
        return;
      }
      end = parsed;
    }
    error.value = null;
    submitting.value = true;
    const signal = lifetime.current?.signal;
    try {
      const res = await createIncident(
        {
          title: title.value.trim(),
          summary: summary.value,
          windowStart: start,
          ...(end ? { windowEnd: end } : {}),
        },
        signal,
      );
      if (signal?.aborted) return;
      globalThis.location.assign(incidentHref(res.incident.id));
    } catch (err) {
      if (signal?.aborted) return;
      error.value = createErrorText(err);
      submitting.value = false;
    }
  };

  return (
    <form
      aria-labelledby="new-incident-heading"
      onSubmit={submit}
      class="flex flex-col gap-4 rounded-lg border border-border-subtle bg-surface p-4"
    >
      <h2 id="new-incident-heading" class={HEADING}>
        New incident
      </h2>
      {error.value && (
        <div role="alert">
          <Alert variant="error">{error.value}</Alert>
        </div>
      )}
      <Input
        id={TITLE_ID}
        label="Title"
        required
        maxLength={INCIDENT_MAX_TITLE_CHARS}
        value={title.value}
        onInput={(e) => {
          title.value = e.currentTarget.value;
        }}
      />
      <div class="space-y-1">
        <label
          for="incident-summary"
          class="block text-sm font-medium text-text-secondary"
        >
          Summary
        </label>
        <textarea
          id="incident-summary"
          rows={3}
          maxLength={INCIDENT_MAX_SUMMARY_CHARS}
          value={summary.value}
          onInput={(e) => {
            summary.value = e.currentTarget.value;
          }}
          class={FIELD}
        />
      </div>
      <div class="grid gap-4 sm:grid-cols-2">
        <Input
          id="incident-window-start"
          type="datetime-local"
          label="Window start"
          required
          description="Evidence is read against this window. Defaults to an hour ago."
          value={windowStart.value}
          onInput={(e) => {
            windowStart.value = e.currentTarget.value;
          }}
        />
        <Input
          id="incident-window-end"
          type="datetime-local"
          label="Window end"
          description="Leave empty while the incident is ongoing."
          value={windowEnd.value}
          onInput={(e) => {
            windowEnd.value = e.currentTarget.value;
          }}
        />
      </div>
      <div class="flex items-center gap-3">
        <button
          type="submit"
          aria-disabled={submitting.value}
          class={BUTTON_PRIMARY}
        >
          {submitting.value ? "Creating…" : "Create incident"}
        </button>
        <button
          type="button"
          aria-disabled={submitting.value}
          onClick={() => {
            if (!submitting.value) onCancel();
          }}
          class={BUTTON_SECONDARY}
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

export default function IncidentList() {
  /** Rows loaded so far, across pages. */
  const items = useSignal<IncidentView[]>([]);
  /** The cursor of the next page; undefined once the last page loaded. */
  const nextCursor = useSignal<string | undefined>(undefined);
  /** Whether the first page has loaded at least once. */
  const loaded = useSignal(false);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  /** The last request failed; keeps Retry mounted while it is re-attempted. */
  const failed = useSignal(false);
  const noDatabase = useSignal(false);
  const formOpen = useSignal(false);
  /**
   * The request to make: the cursor to continue from ("" for the first page)
   * and a sequence number every activation increments, so asking again for
   * the same cursor still re-runs the fetch effect.
   */
  const request = useSignal<{ cursor: string; seq: number }>({
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

  const { cursor, seq } = request.value;
  useEffect(() => {
    const controller = new AbortController();
    loading.value = true;
    // The previous failure is stale once a new attempt starts; Retry stays
    // mounted through `failed` until this attempt settles.
    error.value = null;
    listIncidents(
      { limit: PAGE_SIZE, continue: cursor || undefined },
      controller.signal,
    )
      .then((page) => {
        if (controller.signal.aborted) return;
        items.value = cursor ? [...items.value, ...page.items] : page.items;
        nextCursor.value = page.continue;
        loaded.value = true;
        failed.value = false;
        loading.value = false;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (isPersistenceUnavailable(err)) {
          // Not retryable: a retry that lands here must not leave Retry behind.
          noDatabase.value = true;
          failed.value = false;
        } else {
          error.value = listErrorText(err);
          failed.value = true;
        }
        loading.value = false;
      });
    return () => controller.abort();
  }, [cursor, seq]);

  const issue = (to: string) => {
    // Busy from the click itself, not from the effect a frame later, so a
    // second activation before the effect runs is already a no-op.
    loading.value = true;
    request.value = { cursor: to, seq: request.peek().seq + 1 };
  };
  const retry = () => {
    if (loading.value) return;
    issue(request.peek().cursor);
  };
  const loadMore = () => {
    if (loading.value || !nextCursor.value) return;
    issue(nextCursor.value);
  };
  const closeForm = () => {
    restoreFocus.current = true;
    formOpen.value = false;
  };
  // The form's controls are gone once it closes; return focus to the control
  // that opened it after that control has rendered again.
  const formIsOpen = formOpen.value;
  useEffect(() => {
    if (!formIsOpen && restoreFocus.current) {
      restoreFocus.current = false;
      newButtonRef.current?.focus();
    }
  }, [formIsOpen]);

  const busy = loading.value;
  const rows = items.value;
  const canCreate = !noDatabase.value;

  return (
    <div class={ROOT_CLASS}>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex flex-col gap-1">
          <h1 class="m-0 text-2xl font-bold tracking-tight text-text-primary">
            Incidents
          </h1>
          <p class="m-0 text-sm text-text-muted">
            Investigations you own or were invited to. Each one collects
            evidence from the cluster and notes from the people working it.
          </p>
        </div>
        {canCreate && !formOpen.value && (
          <button
            type="button"
            ref={newButtonRef}
            onClick={() => {
              formOpen.value = true;
            }}
            class={BUTTON_PRIMARY}
          >
            New incident
          </button>
        )}
      </div>

      {canCreate && formOpen.value && <NewIncidentForm onCancel={closeForm} />}

      {noDatabase.value && (
        <div
          role="status"
          class="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-4"
        >
          <p class="m-0 text-sm font-semibold text-text-primary">
            Incidents are unavailable: this deployment has no database
          </p>
          <p class="m-0 text-sm text-text-secondary">
            Incidents are stored in PostgreSQL. Configure a database for
            k8sCenter to record incidents here.
          </p>
        </div>
      )}

      {busy && !loaded.value && !noDatabase.value && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading incidents…
        </p>
      )}

      {failed.value && !noDatabase.value && (
        <div class="flex flex-col items-start gap-2">
          {error.value && (
            <div role="alert" class="w-full">
              <Alert variant="error">{error.value}</Alert>
            </div>
          )}
          <div class="flex items-center gap-3 text-sm">
            <button
              type="button"
              aria-disabled={busy}
              onClick={retry}
              class={BUTTON_SECONDARY}
            >
              Retry
            </button>
            {busy && (
              <span role="status" class="text-text-muted">
                Retrying…
              </span>
            )}
          </div>
        </div>
      )}

      {loaded.value && rows.length === 0 && !failed.value && (
        <p class="m-0 text-sm text-text-secondary">
          No incidents yet. Open one with “New incident” when you start
          investigating a problem.
        </p>
      )}

      {rows.length > 0 && (
        <div
          aria-busy={busy}
          class="overflow-x-auto rounded-lg border border-border-subtle bg-surface"
        >
          <table class="w-full border-collapse text-left text-sm">
            <caption class="sr-only">Your incidents</caption>
            <thead>
              <tr class="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" class="px-3 py-2 font-semibold">
                  Incident
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Status
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Your role
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Created
                </th>
                <th scope="col" class="px-3 py-2 font-semibold">
                  Updated
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((incident) => (
                <tr key={incident.id} class="border-t border-border-subtle">
                  <td class="px-3 py-2 align-top">
                    <a
                      href={incidentHref(incident.id)}
                      class={`font-medium ${LINK}`}
                    >
                      {incident.title}
                    </a>
                  </td>
                  <td class="px-3 py-2 align-top">
                    <StatusBadge status={incident.status} />
                  </td>
                  <td class="px-3 py-2 align-top">
                    <RoleBadge incident={incident} />
                  </td>
                  <td class="px-3 py-2 align-top text-text-secondary">
                    <When at={incident.createdAt} />
                  </td>
                  <td class="px-3 py-2 align-top text-text-secondary">
                    <When at={incident.updatedAt} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {nextCursor.value && rows.length > 0 && (
        <div class="flex items-center gap-3 text-sm">
          <button
            type="button"
            aria-disabled={busy}
            onClick={loadMore}
            class={BUTTON_SECONDARY}
          >
            Load more
          </button>
          {busy && !failed.value && (
            <span role="status" class="text-text-muted">
              Loading more incidents…
            </span>
          )}
        </div>
      )}
    </div>
  );
}
