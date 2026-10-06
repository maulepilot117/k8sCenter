import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { Alert } from "@/components/ui/Alert.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError, errorExtra, getAccessToken } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import { LOCAL_CLUSTER_ID } from "@/lib/cluster.ts";
import {
  addGrant,
  captureEvidence,
  exportUrl,
  getIncident,
  incidentErrorNumber,
  isPersistenceUnavailable,
  isRemoteCaptureRefusal,
  listEvidence,
  listGrants,
  removeGrant,
  updateIncident,
} from "@/lib/incident-api.ts";
import {
  CAPTURE_KINDS,
  CAPTURE_SOURCES,
  type CaptureKind,
  type CaptureResponse,
  type CaptureSourceId,
  captureSourceLabel,
  completenessLabel,
  type EvidenceCounts,
  type EvidenceItem,
  type ExportFormat,
  type GrantView,
  type IncidentStatus,
  type IncidentView,
  pageItems,
  sortTimeline,
} from "@/lib/incident-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import IncidentEvidenceTimeline, {
  CompletenessBadge,
} from "./IncidentEvidenceTimeline.tsx";
import IncidentNotes from "./IncidentNotes.tsx";

/**
 * One incident: header, capture, sharing, export, the evidence timeline and
 * the notes (GET /v1/incidents/{id} and the endpoints under it).
 *
 * Owner-only controls (status, capture, grants) render only for the owner;
 * the server enforces the same rule. Capture always targets the local
 * cluster: Release D captures nowhere else, and pinning it means the
 * operator's cluster selection can never send a capture somewhere it would
 * be refused.
 *
 * Evidence is paged ("Load more"). Every Load more or Retry is a new request
 * with its own sequence number, so asking again after a failure always
 * fetches. A capture reloads the first page, because new evidence sorts first.
 *
 * Export downloads a file. The access token lives in memory, so a plain link
 * cannot carry it: the file is fetched with the Authorization header and
 * saved from a Blob.
 *
 * The root element is the same during SSR and after hydration (the loading
 * state renders on the server).
 */

const ROOT_CLASS = "flex flex-col gap-6";
const PAGE_SIZE = 50;
/** The server's fixed Retry-After for `incident_busy`, in seconds. */
const BUSY_RETRY_AFTER_SECONDS = 1;

const BUTTON_PRIMARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-(--bg-base) focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const BUTTON_SECONDARY =
  "inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-transparent px-3 py-1.5 text-sm font-medium text-text-secondary focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50 aria-disabled:cursor-not-allowed aria-disabled:opacity-50";
const PANEL =
  "flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4";
const CHROME = "glass flex flex-col gap-3 rounded-lg p-4";

const busyText = (what: string) =>
  `${what} The server asked to retry in about ${BUSY_RETRY_AFTER_SECONDS} second${BUSY_RETRY_AFTER_SECONDS === 1 ? "" : "s"}.`;

type LoadError = {
  kind: "not_found" | "invalid" | "retryable";
  message: string;
};

function loadErrorFor(err: unknown): LoadError {
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
    if (err.reason === "incident_busy") {
      return {
        kind: "retryable",
        message: busyText("The incident store is busy."),
      };
    }
  }
  return {
    kind: "retryable",
    message: "Could not load the incident's evidence.",
  };
}

/** The message for a failed capture; every reason the endpoint sends. */
export function captureErrorText(err: unknown): string {
  if (isRemoteCaptureRefusal(err, LOCAL_CLUSTER_ID)) {
    return "Evidence capture is supported on the local cluster only. Nothing was recorded.";
  }
  if (!(err instanceof ApiError))
    return "Capture failed. Nothing was recorded.";
  switch (err.reason) {
    case "incident_busy":
      return busyText(
        `Capture did not run: ${err.detail ?? "the incident is busy"}.`,
      );
    case "incident_capture_outcome_unknown":
      return "The capture may or may not have been recorded. Retrying is safe: evidence that was already recorded is not duplicated.";
    case "incident_closed":
      return "This incident is closed. Reopen it to capture evidence.";
    case "incident_capture_unavailable":
      return "Evidence capture is not available on this deployment.";
    case "evidence_limit_exceeded": {
      const max = incidentErrorNumber(err, "max");
      const which = errorExtra(err, "limit");
      return `This capture would exceed the incident's evidence limit${which ? ` (${which}` : ""}${which && max !== undefined ? `, max ${max})` : which ? ")" : ""}. Nothing was recorded.`;
    }
    case "scope_limit_exceeded": {
      const max = incidentErrorNumber(err, "max");
      return `This incident already holds evidence from the most distinct scopes it can${max !== undefined ? ` (${max})` : ""}. Nothing was recorded.`;
    }
  }
  if (err.status === 400) {
    return `The capture target is invalid: ${err.body?.error?.detail || err.detail || "check the namespace, kind and name"}.`;
  }
  if (err.status === 403)
    return "Only the incident owner may capture evidence.";
  if (err.status === 503 && err.detail) return `${err.detail}.`;
  return "Capture failed. Nothing was recorded.";
}

function simpleErrorText(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.reason === "incident_busy") {
      return busyText("The incident is busy.");
    }
    if (err.reason === "grant_limit_reached") {
      const max = incidentErrorNumber(err, "max");
      return `This incident is already shared with the maximum number of people${max !== undefined ? ` (${max})` : ""}.`;
    }
    if (err.status === 400 || err.status === 403 || err.status === 404) {
      return err.body?.error?.detail || err.detail || fallback;
    }
  }
  return fallback;
}

/**
 * Fetches an export with the Authorization header and returns the file. On a
 * 401 the token has expired: one cheap read through api.ts refreshes it (or
 * sends the user to log in), then the export is fetched once more.
 */
export async function fetchExport(
  id: string,
  format: ExportFormat,
  signal?: AbortSignal,
): Promise<{ blob: Blob; filename: string }> {
  const url = exportUrl(id, format);
  const attempt = () => {
    const headers = new Headers({ "X-Cluster-ID": LOCAL_CLUSTER_ID });
    const token = getAccessToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return fetch(url, { headers, credentials: "include", signal });
  };
  let res = await attempt();
  if (res.status === 401) {
    await getIncident(id, { limit: 1 }, signal);
    res = await attempt();
  }
  if (!res.ok) {
    let body: { error?: Record<string, unknown> } | undefined;
    try {
      body = await res.json();
    } catch {
      // not JSON
    }
    const e = body?.error as { code?: number; message?: string } | undefined;
    throw new ApiError(
      res.status,
      e?.code ?? res.status,
      e?.message ?? res.statusText,
      body as ConstructorParameters<typeof ApiError>[3],
    );
  }
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const match = /filename="([^"]+)"/.exec(disposition);
  const filename =
    match?.[1] ?? `incident-${id}.${format === "markdown" ? "md" : "json"}`;
  return { blob: await res.blob(), filename };
}

function saveBlob(blob: Blob, filename: string) {
  const href = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 0);
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

function CapturePanel({
  incidentId,
  closed,
  onCaptured,
}: {
  incidentId: string;
  closed: boolean;
  /** Called after any capture that may have written evidence. */
  onCaptured: () => void;
}) {
  const namespace = useSignal("");
  const kind = useSignal<CaptureKind>("Deployment");
  const name = useSignal("");
  const sources = useSignal<CaptureSourceId[]>(
    CAPTURE_SOURCES.map((s) => s.id),
  );
  const running = useSignal(false);
  const result = useSignal<CaptureResponse | null>(null);
  const error = useSignal<string | null>(null);

  const submit = async (e: Event) => {
    e.preventDefault();
    if (running.value) return;
    if (!namespace.value.trim() || !name.value.trim()) {
      error.value = "Enter the namespace and name of the object to capture.";
      result.value = null;
      return;
    }
    if (sources.value.length === 0) {
      error.value = "Choose at least one source.";
      result.value = null;
      return;
    }
    running.value = true;
    error.value = null;
    result.value = null;
    try {
      result.value = await captureEvidence(incidentId, LOCAL_CLUSTER_ID, {
        namespace: namespace.value.trim(),
        kind: kind.value,
        name: name.value.trim(),
        sources: sources.value,
      });
      onCaptured();
    } catch (err) {
      error.value = captureErrorText(err);
      if (
        err instanceof ApiError &&
        (err.reason === "incident_capture_outcome_unknown" ||
          err.reason === "incident_closed")
      ) {
        onCaptured();
      }
    } finally {
      running.value = false;
    }
  };

  const toggle = (id: CaptureSourceId, on: boolean) => {
    sources.value = on
      ? CAPTURE_SOURCES.map((s) => s.id).filter(
          (s) => s === id || sources.value.includes(s),
        )
      : sources.value.filter((s) => s !== id);
  };

  const r = result.value;
  return (
    <section aria-labelledby="incident-capture-heading" class={PANEL}>
      <h2
        id="incident-capture-heading"
        class="m-0 text-base font-semibold text-text-primary"
      >
        Capture evidence
      </h2>
      {closed ? (
        <p class="m-0 text-sm text-text-secondary">
          This incident is closed. Reopen it to capture more evidence.
        </p>
      ) : (
        <form onSubmit={submit} class="flex flex-col gap-3">
          <p class="m-0 text-xs text-text-muted">
            Captures from the local cluster, under your own access.
          </p>
          <div class="grid gap-3 sm:grid-cols-3">
            <Input
              id="capture-namespace"
              label="Namespace"
              required
              value={namespace.value}
              onInput={(ev) => {
                namespace.value = ev.currentTarget.value;
              }}
            />
            <div class="space-y-1">
              <label
                for="capture-kind"
                class="block text-sm font-medium text-text-secondary"
              >
                Kind
              </label>
              <select
                id="capture-kind"
                value={kind.value}
                onChange={(ev) => {
                  kind.value = ev.currentTarget.value as CaptureKind;
                }}
                class="block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/50"
              >
                {CAPTURE_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {k}
                  </option>
                ))}
              </select>
            </div>
            <Input
              id="capture-name"
              label="Name"
              required
              value={name.value}
              onInput={(ev) => {
                name.value = ev.currentTarget.value;
              }}
            />
          </div>
          <fieldset class="m-0 flex flex-wrap gap-4 border-0 p-0">
            <legend class="mb-1 p-0 text-sm font-medium text-text-secondary">
              Sources
            </legend>
            {CAPTURE_SOURCES.map((s) => (
              <label
                key={s.id}
                class="inline-flex items-center gap-2 text-sm text-text-primary"
              >
                <input
                  type="checkbox"
                  checked={sources.value.includes(s.id)}
                  onChange={(ev) => toggle(s.id, ev.currentTarget.checked)}
                  class="focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
                />
                {s.label}
              </label>
            ))}
          </fieldset>
          <div>
            <button
              type="submit"
              aria-disabled={running.value}
              class={BUTTON_PRIMARY}
            >
              {running.value ? "Capturing…" : "Capture"}
            </button>
          </div>
        </form>
      )}
      <div role="status" aria-live="polite" class="flex flex-col gap-2">
        {running.value && (
          <p class="m-0 text-sm text-text-muted">Capturing evidence…</p>
        )}
        {error.value && <Alert variant="error">{error.value}</Alert>}
        {r && (
          <div class="flex flex-col gap-2" data-capture-result="true">
            <p class="m-0 text-sm text-text-primary">
              Capture {completenessLabel(r.completeness).toLowerCase()}:{" "}
              {r.inserted} new item{r.inserted === 1 ? "" : "s"} recorded
              {r.deduplicated > 0 ? `, ${r.deduplicated} already recorded` : ""}
              {r.dropped > 0 ? `, ${r.dropped} dropped as invalid` : ""}.
            </p>
            <ul class="m-0 flex list-none flex-col gap-1 p-0">
              {r.sources.map((s) => (
                <li
                  key={s.id}
                  class="flex flex-wrap items-center gap-2 text-sm text-text-secondary"
                >
                  <span class="font-medium text-text-primary">
                    {captureSourceLabel(s.id)}
                  </span>
                  <CompletenessBadge value={s.completeness} />
                  <span>
                    {s.items} item{s.items === 1 ? "" : "s"}
                  </span>
                  {s.detail && <span class="text-text-muted">{s.detail}</span>}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </section>
  );
}

function GrantsPanel({ incidentId }: { incidentId: string }) {
  const grants = useSignal<GrantView[] | null>(null);
  const loadError = useSignal<string | null>(null);
  const granteeId = useSignal("");
  const canAnnotate = useSignal(false);
  const working = useSignal(false);
  const message = useSignal<string | null>(null);
  const error = useSignal<string | null>(null);
  const reloadSeq = useSignal(0);

  const seq = reloadSeq.value;
  useEffect(() => {
    const controller = new AbortController();
    loadError.value = null;
    listGrants(incidentId, controller.signal)
      .then((g) => {
        if (!controller.signal.aborted) grants.value = g;
      })
      .catch((err) => {
        if (controller.signal.aborted) return;
        loadError.value = simpleErrorText(
          err,
          "Could not load who this incident is shared with.",
        );
      });
    return () => controller.abort();
  }, [incidentId, seq]);

  const run = async (action: () => Promise<string | null>) => {
    if (working.value) return;
    working.value = true;
    error.value = null;
    message.value = null;
    try {
      message.value = await action();
    } catch (err) {
      error.value = simpleErrorText(err, "The change could not be saved.");
      if (err instanceof ApiError && err.status === 404) {
        reloadSeq.value = reloadSeq.peek() + 1;
      }
    } finally {
      working.value = false;
    }
  };

  const upsert = (g: GrantView) => {
    const rest = (grants.value ?? []).filter(
      (x) => x.granteeId !== g.granteeId,
    );
    grants.value = [...rest, g];
  };

  const share = (e: Event) => {
    e.preventDefault();
    const who = granteeId.value.trim();
    if (!who) {
      error.value = "Enter the user id to share with.";
      return;
    }
    void run(async () => {
      const g = await addGrant(incidentId, {
        granteeId: who,
        canAnnotate: canAnnotate.value,
      });
      granteeId.value = "";
      if (!g) {
        return "That is you. As the owner you already hold every right, so nothing changed.";
      }
      upsert(g);
      return `Shared with ${g.granteeId}.`;
    });
  };

  const list = grants.value;
  return (
    <section aria-labelledby="incident-grants-heading" class={PANEL}>
      <h2
        id="incident-grants-heading"
        class="m-0 text-base font-semibold text-text-primary"
      >
        Sharing
      </h2>
      <p class="m-0 text-xs text-text-muted">
        People you share with see the evidence their own access allows; the rest
        appears to them as withheld.
      </p>
      {loadError.value && (
        <div role="alert">
          <Alert variant="error">{loadError.value}</Alert>
        </div>
      )}
      {list && list.length === 0 && (
        <p class="m-0 text-sm text-text-secondary">Not shared with anyone.</p>
      )}
      {list && list.length > 0 && (
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-left text-sm">
            <caption class="sr-only">
              People this incident is shared with
            </caption>
            <thead>
              <tr class="text-xs uppercase tracking-wide text-text-muted">
                <th scope="col" class="px-2 py-1 font-semibold">
                  User
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  Can add notes
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  Since
                </th>
                <th scope="col" class="px-2 py-1 font-semibold">
                  <span class="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((g) => (
                <tr key={g.granteeId} class="border-t border-border-subtle">
                  <td class="px-2 py-1 text-text-primary">{g.granteeId}</td>
                  <td class="px-2 py-1">
                    <input
                      type="checkbox"
                      aria-label={`${g.granteeId} can add notes`}
                      checked={g.canAnnotate}
                      aria-disabled={working.value}
                      onChange={(ev) => {
                        const want = ev.currentTarget.checked;
                        ev.currentTarget.checked = g.canAnnotate;
                        void run(async () => {
                          const updated = await addGrant(incidentId, {
                            granteeId: g.granteeId,
                            canAnnotate: want,
                          });
                          if (updated) upsert(updated);
                          return null;
                        });
                      }}
                      class="focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
                    />
                  </td>
                  <td class="px-2 py-1 text-text-secondary">
                    {timeAgo(g.createdAt)}
                  </td>
                  <td class="px-2 py-1 text-right">
                    <button
                      type="button"
                      aria-disabled={working.value}
                      onClick={() =>
                        void run(async () => {
                          await removeGrant(incidentId, g.granteeId);
                          grants.value = (grants.value ?? []).filter(
                            (x) => x.granteeId !== g.granteeId,
                          );
                          return `Stopped sharing with ${g.granteeId}.`;
                        })
                      }
                      class={BUTTON_SECONDARY}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <form onSubmit={share} class="flex flex-wrap items-end gap-3">
        <Input
          id="grant-grantee"
          label="User id"
          value={granteeId.value}
          onInput={(ev) => {
            granteeId.value = ev.currentTarget.value;
          }}
        />
        <label class="inline-flex items-center gap-2 pb-2 text-sm text-text-primary">
          <input
            type="checkbox"
            checked={canAnnotate.value}
            onChange={(ev) => {
              canAnnotate.value = ev.currentTarget.checked;
            }}
            class="focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
          />
          Can add notes
        </label>
        <button
          type="submit"
          aria-disabled={working.value}
          class={BUTTON_SECONDARY}
        >
          Share
        </button>
      </form>
      <div role="status" aria-live="polite">
        {message.value && (
          <p class="m-0 text-sm text-text-secondary">{message.value}</p>
        )}
        {error.value && <Alert variant="error">{error.value}</Alert>}
      </div>
    </section>
  );
}

function ExportMenu({ incidentId }: { incidentId: string }) {
  const pending = useSignal<ExportFormat | null>(null);
  const error = useSignal<string | null>(null);

  const download = async (format: ExportFormat) => {
    if (pending.value) return;
    pending.value = format;
    error.value = null;
    try {
      const { blob, filename } = await fetchExport(incidentId, format);
      saveBlob(blob, filename);
    } catch (err) {
      error.value = simpleErrorText(err, "The export could not be downloaded.");
    } finally {
      pending.value = null;
    }
  };

  return (
    <div
      class="flex flex-wrap items-center gap-2"
      role="group"
      aria-label="Export"
    >
      <span class="text-sm text-text-secondary">Export</span>
      <button
        type="button"
        aria-disabled={pending.value !== null}
        onClick={() => download("json")}
        class={BUTTON_SECONDARY}
      >
        {pending.value === "json" ? "Exporting…" : "JSON"}
      </button>
      <button
        type="button"
        aria-disabled={pending.value !== null}
        onClick={() => download("markdown")}
        class={BUTTON_SECONDARY}
      >
        {pending.value === "markdown" ? "Exporting…" : "Markdown"}
      </button>
      {error.value && (
        <div role="alert" class="w-full">
          <Alert variant="error">{error.value}</Alert>
        </div>
      )}
    </div>
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

  const { cursor, seq } = request.value;
  useEffect(() => {
    const controller = new AbortController();
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
        if (r.incident) incident.value = r.incident;
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
          loadError.value = loadErrorFor(err);
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
        <a
          href="/observability/incidents"
          class="rounded-sm text-accent focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
        >
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
            <h2
              id="incident-evidence-heading"
              class="m-0 text-base font-semibold text-text-primary"
            >
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
            {err && !request.value.cursor && (
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
