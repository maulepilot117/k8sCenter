/** Backup assurance — exception cards and the grouped, paged exception list
 * (Release F U36).
 *
 * An empty list is keyed on the server's total, never on the rows of the
 * current page, and is only called "no open exceptions" while collection is
 * ok.
 */

import { Button } from "@/components/ui/Button.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import {
  absolute,
  Chip,
  CONDITION_CLASS,
  SEVERITY_CLASS,
  STATE_CLASS,
  When,
} from "@/components/velero/AssuranceBadges.tsx";
import {
  type AssuranceException,
  type AssuranceExceptionState,
  conditionExplanation,
  conditionLabel,
  EXCEPTION_PAGE_SIZE,
  groupByCondition,
  outcomeLabel,
  type SurfaceState,
  scopeLabel,
} from "@/lib/backup-assurance-types.ts";

/**
 * A failed request, reduced to what the page branches on. The island builds
 * these for every request stream; the exception list renders one.
 */
export interface RequestFailure {
  status: number;
  reason?: string;
  message: string;
}

function ExceptionCard({ ex }: { ex: AssuranceException }) {
  const d = ex.detail;
  const subject = scopeLabel(
    ex.subject.kind,
    ex.subject.namespace,
    ex.subject.name,
  );
  // Under treat_partial_as = success a PartiallyFailed run is accepted as
  // the last success, but it is still never shown as a plain success.
  const partialAccepted =
    d.lastOutcome === "partial" && ex.condition !== "partially_failed";
  return (
    <li
      class="rounded-lg border border-border-subtle bg-surface p-4"
      data-testid="assurance-exception"
      data-condition={ex.condition}
      data-exception-id={ex.id}
      data-policy-id={ex.policyId}
    >
      <div class="flex flex-wrap items-center gap-2">
        <Chip
          class={CONDITION_CLASS[ex.condition] ?? STATE_CLASS.unknown}
          testId="assurance-condition-chip"
        >
          {conditionLabel(ex.condition)}
        </Chip>
        <Chip class={SEVERITY_CLASS[ex.severity] ?? STATE_CLASS.unknown}>
          {ex.severity}
        </Chip>
        {partialAccepted && (
          <Chip
            class="bg-warning-dim text-warning"
            testId="assurance-partial-success-chip"
          >
            last success was partial
          </Chip>
        )}
        <span class="break-all font-mono text-sm text-text-primary">
          {subject}
        </span>
      </div>
      <p class="mt-2 text-sm text-text-secondary">
        {conditionExplanation(ex.condition)}
      </p>
      <dl class="mt-3 grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-2">
        <div class="flex gap-2">
          <dt class="text-text-muted">Opened</dt>
          <dd class="text-text-secondary">
            <When iso={ex.openedAt} />
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Last observed</dt>
          <dd class="text-text-secondary">
            <When iso={ex.lastObservedAt} /> ({ex.observationCount}{" "}
            {ex.observationCount === 1 ? "observation" : "observations"})
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Last successful backup</dt>
          <dd class="text-text-secondary">
            {ex.lastSuccessAt ? (
              <When iso={ex.lastSuccessAt} />
            ) : (
              "no successful backup observed"
            )}
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Last run outcome</dt>
          <dd class="text-text-secondary">{outcomeLabel(d.lastOutcome)}</dd>
        </div>
        {d.expectedRunKnown && d.expectedRunAt && (
          <div class="flex gap-2">
            <dt class="text-text-muted">Expected run</dt>
            <dd class="text-text-secondary">{absolute(d.expectedRunAt)}</dd>
          </div>
        )}
        {ex.state === "resolved" && ex.resolvedAt && (
          <div class="flex gap-2">
            <dt class="text-text-muted">Resolved</dt>
            <dd class="text-text-secondary">
              <When iso={ex.resolvedAt} />
              {d.resolutionReason === "subject_absent" &&
                " — the subject no longer exists"}
              {d.resolutionReason === "condition_cleared" &&
                " — the condition cleared"}
            </dd>
          </div>
        )}
      </dl>
      {ex.expectedRunNote && (
        <p
          class="mt-2 text-xs text-text-muted"
          data-testid="assurance-expected-run-note"
        >
          {ex.subject.kind === "schedule"
            ? "Next expected run is not computable for this schedule expression."
            : "Next expected run is not computable for this scope; overdue is judged from maximum age plus grace alone."}
          {d.cronParseError && ` (${d.cronParseError})`}
        </p>
      )}
      {d.suppressedBy === "paused" && (
        <p class="mt-2 text-xs text-text-muted">
          Held open, not re-evaluated, while the schedule is paused.
        </p>
      )}
      {ex.subjectStatus === "not_found" && (
        <p
          class="mt-2 text-xs text-warning"
          data-testid="assurance-subject-note"
        >
          {ex.subjectNote ?? "schedule not found"}: no schedule with this name
          and UID exists now; it may have been deleted or recreated.
        </p>
      )}
      {ex.subjectStatus === "unknown" && (
        <p class="mt-2 text-xs text-text-muted">
          Whether this schedule still exists is unknown: Velero could not be
          read.
        </p>
      )}
      {(d.storageLocation || d.bslMessage || d.failureReason) && (
        <dl class="mt-3 space-y-1 border-t border-border-subtle pt-2 text-xs">
          {d.storageLocation && (
            <div class="flex gap-2">
              <dt class="text-text-muted">Storage location</dt>
              <dd class="break-all font-mono text-text-secondary">
                {d.storageLocation}
              </dd>
            </div>
          )}
          {d.bslMessage && (
            <div class="flex gap-2">
              <dt class="text-text-muted">Location message</dt>
              <dd class="break-all text-text-secondary">{d.bslMessage}</dd>
            </div>
          )}
          {d.failureReason && (
            <div class="flex gap-2">
              <dt class="text-text-muted">Failure reason</dt>
              <dd class="break-all text-text-secondary">{d.failureReason}</dd>
            </div>
          )}
        </dl>
      )}
    </li>
  );
}

/**
 * The Open/Resolved toggle and the exception list below it. Presentational:
 * the island owns the request stream and passes its current reply in.
 */
export function ExceptionList({
  exState,
  exceptions,
  total,
  offset,
  loading,
  failure,
  state,
  policyCount,
  onSwitchState,
  onChangePage,
}: {
  exState: AssuranceExceptionState;
  exceptions: AssuranceException[];
  total: number;
  offset: number;
  loading: boolean;
  failure: RequestFailure | null;
  state: SurfaceState;
  policyCount: number;
  onSwitchState: (next: AssuranceExceptionState) => void;
  onChangePage: (delta: number) => void;
}) {
  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = offset + exceptions.length;
  return (
    <section aria-label="Backup exceptions" class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <h2 class="text-sm font-semibold text-text-primary">
          {exState === "open" ? "Open exceptions" : "Resolved exceptions"}
        </h2>
        <div
          class="inline-flex rounded-md border border-border-primary"
          role="group"
          aria-label="Exception state"
        >
          {(["open", "resolved"] as const).map((s) => (
            <button
              key={s}
              type="button"
              aria-pressed={exState === s}
              class={`px-3 py-1 text-xs font-medium first:rounded-l-md last:rounded-r-md ${
                exState === s
                  ? "bg-elevated text-text-primary"
                  : "text-text-secondary hover:bg-hover"
              }`}
              onClick={() => onSwitchState(s)}
            >
              {s === "open" ? "Open" : "Resolved"}
            </button>
          ))}
        </div>
      </div>

      {failure?.status === 403 ? (
        <p
          class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
          data-testid="assurance-forbidden"
        >
          You do not have permission to view backup exceptions.
        </p>
      ) : failure ? (
        <p
          class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
          role="alert"
        >
          Backup exceptions could not be loaded: {failure.message}. Their state
          is unknown.
        </p>
      ) : loading && exceptions.length === 0 ? (
        <div class="flex justify-center py-6">
          <Spinner />
        </div>
      ) : total === 0 ? (
        // Keyed on the server's total, not the rows on this page: an
        // empty page of a non-empty list is never "no exceptions".
        exState === "open" && state === "ok" && policyCount > 0 ? (
          <p
            class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
            data-testid="assurance-no-open"
          >
            No open backup exceptions. This is not a recoverability guarantee.
          </p>
        ) : exState === "open" && policyCount > 0 ? (
          <p
            class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
            data-testid="assurance-no-open-unknown"
          >
            No open exceptions are recorded, but collection is {state}, so
            backup state is unknown.
          </p>
        ) : exState === "resolved" ? (
          <p class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary">
            No resolved exceptions are retained.
          </p>
        ) : null
      ) : (
        <>
          {groupByCondition(exceptions).map(([c, rows]) => (
            <div key={c} data-testid={`assurance-group-${c}`}>
              <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-text-muted">
                {conditionLabel(c)} ({rows.length})
              </h3>
              <ul class="flex flex-col gap-2">
                {rows.map((ex) => (
                  <ExceptionCard key={ex.id} ex={ex} />
                ))}
              </ul>
            </div>
          ))}
          {total > EXCEPTION_PAGE_SIZE && (
            <div class="flex items-center justify-between text-xs text-text-muted">
              <span>
                {rangeStart}–{rangeEnd} of {total}
              </span>
              <div class="flex gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={offset === 0 || loading}
                  onClick={() => onChangePage(-1)}
                >
                  Previous
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={rangeEnd >= total || loading}
                  onClick={() => onChangePage(1)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}
