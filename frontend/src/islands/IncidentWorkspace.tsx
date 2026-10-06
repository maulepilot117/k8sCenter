import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  getIncident,
  isPersistenceUnavailable,
  listEvidence,
  updateIncident,
} from "@/lib/incident-api.ts";
import {
  type EvidenceCounts,
  type EvidenceItem,
  type IncidentStatus,
  type IncidentView,
  pageItems,
  sortTimeline,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { CapturePanel } from "@/src/components/incidents/CapturePanel.tsx";
import { ExportMenu } from "@/src/components/incidents/ExportMenu.tsx";
import {
  busyText,
  simpleErrorText,
} from "@/src/components/incidents/errors.ts";
import { GrantsPanel } from "@/src/components/incidents/GrantsPanel.tsx";
import {
  BUTTON_SECONDARY,
  CHROME,
  HEADING,
  LINK,
  PANEL,
} from "@/src/components/incidents/ui.tsx";
import IncidentEvidenceTimeline from "./IncidentEvidenceTimeline.tsx";
import IncidentNotes from "./IncidentNotes.tsx";

/**
 * One incident (GET /v1/incidents/{id} and the endpoints under it): this
 * island owns the record, the evidence paging and the layout; the header is
 * here, and the capture, sharing and export panels live in
 * `src/components/incidents/`.
 *
 * Owner-only controls (status, capture, grants) render only for the owner;
 * the server enforces the same rule.
 *
 * Evidence is paged ("Load more"). Every Load more or Retry is a new request
 * with its own sequence number, so asking again after a failure always
 * fetches. A capture reloads the first page, because new evidence sorts first.
 *
 * The root element is the same during SSR and after hydration (the loading
 * state renders on the server).
 */

const ROOT_CLASS = "flex flex-col gap-6";
const PAGE_SIZE = 50;

type LoadError = {
  kind: "not_found" | "invalid" | "retryable";
  message: string;
};

/**
 * What a failed load means. A failure while paging (`cursor` set) is always
 * "could not load more": the incident itself loaded, so a 400 there is a bad
 * cursor, not a bad incident id.
 */
function loadErrorFor(err: unknown, cursor: string): LoadError {
  if (err instanceof ApiError && err.reason === "incident_busy") {
    return {
      kind: "retryable",
      message: busyText("The incident store is busy.", err),
    };
  }
  if (cursor) {
    return {
      kind: "retryable",
      message: "Could not load more evidence. Retry to try that page again.",
    };
  }
  if (err instanceof ApiError) {
    if (err.status === 404) {
      return {
        kind: "not_found",
        message:
          "Incident not found. It may not exist, or it may not be shared with you.",
      };
    }
    if (err.status === 400) {
      return { kind: "invalid", message: "This is not a valid incident id." };
    }
  }
  return {
    kind: "retryable",
    message: "Could not load the incident's evidence.",
  };
}

function StatusBadge({ status }: { status: IncidentStatus }) {
  return status === "closed" ? (
    <span class="inline-flex rounded-full bg-success-dim px-2 py-0.5 text-xs font-medium text-success">
      Closed
    </span>
  ) : (
    <span class="inline-flex rounded-full bg-warning-dim px-2 py-0.5 text-xs font-medium text-warning">
      Open
    </span>
  );
}

function When({ at }: { at: string }) {
  return (
    <time dateTime={at} title={at}>
      {new Date(at).toLocaleString()} ({timeAgo(at)})
    </time>
  );
}

function Header({
  incident,
  counts,
  onStatus,
  statusBusy,
  statusError,
}: {
  incident: IncidentView;
  counts: EvidenceCounts | null;
  onStatus: (s: IncidentStatus) => void;
  statusBusy: boolean;
  statusError: string | null;
}) {
  const owner = incident.role === "owner";
  return (
    <header class={CHROME}>
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="flex flex-col gap-2">
          <h1 class="m-0 text-2xl font-bold tracking-tight text-text-primary">
            {incident.title}
          </h1>
          <div class="flex flex-wrap items-center gap-2 text-sm text-text-secondary">
            <StatusBadge status={incident.status} />
            <span>
              {owner
                ? "You own this incident"
                : incident.canAnnotate
                  ? "Shared with you (can add notes)"
                  : "Shared with you (read-only)"}
            </span>
          </div>
        </div>
        {owner && (
          <button
            type="button"
            aria-disabled={statusBusy}
            onClick={() => {
              if (!statusBusy) {
                onStatus(incident.status === "open" ? "closed" : "open");
              }
            }}
            class={BUTTON_SECONDARY}
          >
            {incident.status === "open" ? "Close incident" : "Reopen incident"}
          </button>
        )}
      </div>
      {statusError && (
        <div role="alert">
          <Alert variant="error">{statusError}</Alert>
        </div>
      )}
      {incident.summary && (
        <p class="m-0 whitespace-pre-wrap text-sm text-text-secondary">
          {incident.summary}
        </p>
      )}
      <dl class="m-0 grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[auto_1fr]">
        <dt class="text-text-muted">Window</dt>
        <dd class="m-0 text-text-primary">
          <When at={incident.windowStart} />
          {" to "}
          {incident.windowEnd ? <When at={incident.windowEnd} /> : "ongoing"}
        </dd>
        <dt class="text-text-muted">Evidence</dt>
        <dd class="m-0 text-text-primary" data-counts="true">
          {counts
            ? `${counts.visible} visible to you, ${counts.withheld} withheld`
            : "Counts unavailable"}
        </dd>
        <dt class="text-text-muted">Retention</dt>
        <dd class="m-0 text-text-primary">{incident.retentionDays} days</dd>
      </dl>
    </header>
  );
}

export default function IncidentWorkspace({ id }: { id: string }) {
  const { user } = useAuth();
  const incident = useSignal<IncidentView | null>(null);
  const counts = useSignal<EvidenceCounts | null>(null);
  const items = useSignal<EvidenceItem[]>([]);
  const nextCursor = useSignal<string | undefined>(undefined);
  const loading = useSignal(true);
  const loadError = useSignal<LoadError | null>(null);
  const noDatabase = useSignal(false);
  const request = useSignal<{ cursor: string; seq: number }>({
    cursor: "",
    seq: 0,
  });
  const statusBusy = useSignal(false);
  const statusError = useSignal<string | null>(null);

  const title = incident.value?.title;
  useEffect(() => {
    document.title = title
      ? `${title} - Incident - k8sCenter`
      : "Incident - k8sCenter";
    return () => {
      document.title = "k8sCenter";
    };
  }, [title]);

  /**
   * Counts status changes started. A page load applies the incident record
   * it read only if no status change started after the load was issued:
   * otherwise its copy may predate the change and would revert it on screen.
   */
  const statusEpoch = useRef(0);

  const { cursor, seq } = request.value;
  useEffect(() => {
    const controller = new AbortController();
    const epoch = statusEpoch.current;
    loading.value = true;
    loadError.value = null;
    const fetchPage = cursor
      ? listEvidence(
          id,
          { limit: PAGE_SIZE, continue: cursor },
          controller.signal,
        ).then((r) => ({ incident: null, page: r.page, continue: r.continue }))
      : getIncident(id, { limit: PAGE_SIZE }, controller.signal).then((r) => ({
          incident: r.detail.incident,
          page: r.detail,
          continue: r.continue,
        }));
    fetchPage
      .then((r) => {
        if (controller.signal.aborted) return;
        if (r.incident && epoch === statusEpoch.current) {
          incident.value = r.incident;
        }
        counts.value = r.page.counts ?? counts.value;
        const fresh = pageItems(r.page);
        if (cursor) {
          const seen = new Set(items.value.map((i) => i.id));
          items.value = sortTimeline([
            ...items.value,
            ...fresh.filter((i) => !seen.has(i.id)),
          ]);
        } else {
          items.value = sortTimeline(fresh);
        }
        nextCursor.value = r.continue;
        loading.value = false;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (isPersistenceUnavailable(err)) {
          noDatabase.value = true;
        } else {
          loadError.value = loadErrorFor(err, cursor);
        }
        loading.value = false;
      });
    return () => controller.abort();
  }, [id, cursor, seq]);

  const issue = (to: string) => {
    loading.value = true;
    request.value = { cursor: to, seq: request.peek().seq + 1 };
  };
  const retry = () => {
    if (!loading.value) issue(request.peek().cursor);
  };
  const loadMore = () => {
    if (loading.value || !nextCursor.value) return;
    issue(nextCursor.value);
  };
  const reloadFirstPage = () => issue("");

  const setStatus = async (status: IncidentStatus) => {
    statusEpoch.current++;
    statusBusy.value = true;
    statusError.value = null;
    try {
      const res = await updateIncident(id, { status });
      incident.value = res.incident;
      if (res.counts) counts.value = res.counts;
    } catch (err) {
      statusError.value = simpleErrorText(
        err,
        "The status could not be changed.",
      );
    } finally {
      statusBusy.value = false;
    }
  };

  const inc = incident.value;
  const busy = loading.value;
  const err = loadError.value;
  const owner = inc?.role === "owner";

  return (
    <div class={ROOT_CLASS}>
      <nav aria-label="Breadcrumb" class="text-sm">
        <a href="/observability/incidents" class={LINK}>
          Incidents
        </a>
      </nav>

      {noDatabase.value && (
        <div role="status" class={PANEL}>
          <p class="m-0 text-sm font-semibold text-text-primary">
            Incidents are unavailable: this deployment has no database
          </p>
          <p class="m-0 text-sm text-text-secondary">
            Incidents are stored in PostgreSQL. Configure a database for
            k8sCenter to record incidents here.
          </p>
        </div>
      )}

      {busy && !inc && !noDatabase.value && (
        <p role="status" class="m-0 text-sm text-text-muted">
          Loading incident…
        </p>
      )}

      {err && !inc && (
        <div role="alert">
          <Alert variant="error">{err.message}</Alert>
        </div>
      )}
      {err && !inc && err.kind === "retryable" && !request.value.cursor && (
        <div>
          <button
            type="button"
            aria-disabled={busy}
            onClick={retry}
            class={BUTTON_SECONDARY}
          >
            Retry
          </button>
        </div>
      )}

      {inc && (
        <>
          <Header
            incident={inc}
            counts={counts.value}
            onStatus={setStatus}
            statusBusy={statusBusy.value}
            statusError={statusError.value}
          />
          <ExportMenu incidentId={id} />
          {owner && (
            <CapturePanel
              incidentId={id}
              closed={inc.status === "closed"}
              onCaptured={reloadFirstPage}
            />
          )}
          <section
            aria-labelledby="incident-evidence-heading"
            class="flex flex-col gap-3"
          >
            <h2 id="incident-evidence-heading" class={HEADING}>
              Evidence
            </h2>
            {items.value.length === 0 && !busy && !err && (
              <p class="m-0 text-sm text-text-secondary">
                No evidence yet.
                {owner && inc.status === "open" ? " Capture some above." : ""}
              </p>
            )}
            {items.value.length > 0 && (
              <div aria-busy={busy}>
                <IncidentEvidenceTimeline items={items.value} />
              </div>
            )}
            {err && (
              <div role="alert">
                <Alert variant="error">{err.message}</Alert>
              </div>
            )}
            {err && err.kind === "retryable" && (
              <div>
                <button
                  type="button"
                  aria-disabled={busy}
                  onClick={retry}
                  class={BUTTON_SECONDARY}
                >
                  Retry
                </button>
              </div>
            )}
            {nextCursor.value && (
              <div class="flex items-center gap-3 text-sm">
                {/* A failed page leaves the cursor in place, so Load more
                    asks for that page again with a new sequence number. */}
                <button
                  type="button"
                  aria-disabled={busy}
                  onClick={loadMore}
                  class={BUTTON_SECONDARY}
                >
                  Load more
                </button>
                {busy && (
                  <span role="status" class="text-text-muted">
                    Loading more evidence…
                  </span>
                )}
              </div>
            )}
          </section>
          {owner && <GrantsPanel incidentId={id} />}
          <IncidentNotes
            incidentId={id}
            canAnnotate={inc.canAnnotate}
            currentUserId={user.value?.id ?? null}
          />
        </>
      )}
    </div>
  );
}
