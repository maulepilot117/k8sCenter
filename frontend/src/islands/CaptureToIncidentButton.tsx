import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
import { Alert } from "@/components/ui/Alert.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  captureEvidence,
  createIncident,
  incidentErrorNumber,
  isPersistenceUnavailable,
  isRemoteCaptureRefusal,
  listIncidents,
} from "@/lib/incident-api.ts";
import {
  type CaptureKind,
  INCIDENT_MAX_TITLE_CHARS,
  type IncidentView,
} from "@/lib/incident-types.ts";
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
 * A repeated click cannot duplicate anything. Every action is inactive while
 * one is in flight, and an incident this dialog created stays the target of a
 * retried capture, so a capture that failed after its incident was created
 * never creates a second incident. Capture itself is deduplicated server-side
 * by capture key, which is what makes retrying an outcome-unknown capture safe.
 *
 * Inactive controls use `aria-disabled` rather than `disabled`, so they stay
 * focusable and their explanatory tooltip stays reachable.
 */

/** The window start used when the diagnosis reported no observation time. */
const DEFAULT_WINDOW_MS = 60 * 60 * 1000;
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

const CAPTURE_KINDS: ReadonlySet<string> = new Set<CaptureKind>([
  "Deployment",
  "StatefulSet",
  "DaemonSet",
  "Pod",
  "Service",
  "PersistentVolumeClaim",
]);

const incidentHref = (id: string) =>
  `/observability/incidents/${encodeURIComponent(id)}`;

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
  return windowStart ?? new Date(now - DEFAULT_WINDOW_MS).toISOString();
}

/** The message for a failed create or capture. */
function actionErrorText(err: unknown, clusterId: string): string {
  if (isRemoteCaptureRefusal(err, clusterId)) {
    return "Capture is local-cluster only. Nothing was recorded.";
  }
  if (isPersistenceUnavailable(err)) {
    return "This deployment has no database, so incidents cannot be recorded.";
  }
  if (!(err instanceof ApiError)) return "Capture failed. Try again.";
  switch (err.reason) {
    case "incident_busy":
      return "The incident is busy. Try again in a moment.";
    case "incident_capture_outcome_unknown":
      return "The capture may or may not have been recorded. Retrying is safe: evidence that was already recorded is not duplicated.";
    case "incident_closed":
      return "That incident is closed. Reopen it to capture evidence.";
    case "incident_capture_unavailable":
      return "Evidence capture is not available on this deployment.";
    case "evidence_limit_exceeded":
      return "This capture would exceed the incident's evidence limit. Nothing was recorded.";
    case "scope_limit_exceeded": {
      const max = incidentErrorNumber(err, "max");
      return `That incident already holds evidence from the most distinct scopes it can${max !== undefined ? ` (${max})` : ""}. Nothing was recorded.`;
    }
  }
  if (err.status === 403) {
    return "Only the incident owner may capture evidence.";
  }
  if (err.status === 400) {
    return `The capture target is invalid: ${err.body?.error?.detail || err.detail || "check the namespace, kind and name"}.`;
  }
  return "Capture failed. Try again.";
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
  /** Set when the deployment has no database; fixed for the page's life. */
  const noDatabase = useSignal(false);
  const open = useSignal(false);
  /** A create or capture is in flight; every action is inactive meanwhile. */
  const busy = useSignal(false);
  const error = useSignal<string | null>(null);
  /** An incident this dialog created whose capture has not yet succeeded. */
  const createdId = useSignal<string | null>(null);

  const owned = useSignal<IncidentView[]>([]);
  const listCursor = useSignal<string | undefined>(undefined);
  const listLoading = useSignal(false);
  const listError = useSignal<string | null>(null);

  const triggerRef = useRef<HTMLButtonElement>(null);
  const newButtonRef = useRef<HTMLButtonElement>(null);
  /** Aborted on unmount: a late reply then neither navigates nor writes state. */
  const lifetime = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    lifetime.current = controller;
    if (isLocal) {
      listIncidents({ limit: 1 }, controller.signal).catch((err) => {
        if (!controller.signal.aborted && isPersistenceUnavailable(err)) {
          noDatabase.value = true;
        }
      });
    }
    return () => controller.abort();
  }, [isLocal]);

  const isOpen = open.value;
  useEffect(() => {
    if (isOpen) newButtonRef.current?.focus();
  }, [isOpen]);

  const supportedKind = CAPTURE_KINDS.has(kind);
  const inactiveReason = !isLocal
    ? "Capture is local-cluster only. Switch to the local cluster to capture this resource into an incident."
    : noDatabase.value
      ? "This deployment has no database, so incidents cannot be recorded."
      : !supportedKind
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

  const target = () => ({
    namespace,
    kind: kind as CaptureKind,
    name,
  });

  /** Runs one action; a second activation while one is in flight is a no-op. */
  const run = async (action: (signal?: AbortSignal) => Promise<string>) => {
    if (busy.peek()) return;
    busy.value = true;
    error.value = null;
    const signal = lifetime.current?.signal;
    try {
      const id = await action(signal);
      if (signal?.aborted) return;
      globalThis.location.assign(incidentHref(id));
    } catch (err) {
      if (signal?.aborted) return;
      error.value = actionErrorText(err, clusterId);
      busy.value = false;
    }
  };

  const captureNew = () =>
    run(async (signal) => {
      let id = createdId.peek();
      if (!id) {
        const title = `${kind}/${name} in ${namespace}`.slice(
          0,
          INCIDENT_MAX_TITLE_CHARS,
        );
        const created = await createIncident(
          {
            title,
            summary: `Opened from the diagnosis of ${kind} ${namespace}/${name}.`,
            windowStart: resolveWindowStart(windowStart, Date.now()),
          },
          signal,
        );
        id = created.incident.id;
        createdId.value = id;
      }
      await captureEvidence(id, clusterId, target(), signal);
      return id;
    });

  const captureExisting = (id: string) =>
    run(async (signal) => {
      await captureEvidence(id, clusterId, target(), signal);
      return id;
    });

  const inFlight = busy.value;
  const created = createdId.value;

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
