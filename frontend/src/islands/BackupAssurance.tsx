/** Backup assurance — recovery readiness page (Release F U36).
 *
 * Reads the U35 API under /v1/velero/assurance: the collector's status, the
 * caller's visible exceptions, and (admins only) the freshness policies.
 *
 * Honesty rule (plan §7): backup freshness is not demonstrated
 * recoverability. The page therefore
 *   - opens with a persistent, non-dismissible banner saying so;
 *   - shows no aggregate score, percentage, shield or "protected" badge —
 *     counts are per condition and always labelled with the condition;
 *   - uses the R3 six-state vocabulary verbatim and never renders an
 *     `unknown` collection as zero or as "no backups";
 *   - offers no restore action of any kind. It observes; it never mutates a
 *     cluster. The only writes are admin policy edits, which touch
 *     PostgreSQL alone.
 *
 * Local cluster only: a remote selection is answered 501
 * remote_assurance_unsupported and the page says so instead of rendering an
 * empty list that would read as "no exceptions".
 *
 * Glass is chrome-only: the banner and page header use `.glass-bar`; every
 * data surface (counts, exception cards, policy table) is solid.
 */

import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Button } from "@/components/ui/Button.tsx";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, api, apiGet, apiPost, apiPut } from "@/lib/api.ts";
import {
  ASSURANCE_CONDITIONS,
  ASSURANCE_REASONS,
  type AssuranceCondition,
  type AssuranceException,
  type AssuranceExceptionState,
  type AssuranceFieldError,
  type AssurancePolicy,
  type AssurancePolicyCreate,
  type AssurancePolicyDeleted,
  type AssurancePolicyUpdate,
  type AssuranceRuntime,
  type AssuranceScopeKind,
  type AssuranceSeverity,
  type AssuranceStatus,
  collectionSourceLabel,
  conditionExplanation,
  conditionLabel,
  countsAreCurrent,
  DEFAULT_GRACE_SECONDS,
  EXCEPTION_PAGE_SIZE,
  formatSeconds,
  groupByCondition,
  HONESTY_TEXT,
  MIN_MAX_AGE_SECONDS,
  outcomeLabel,
  type SurfaceState,
  scopeLabel,
  surfaceStateCopy,
  surfaceStateFor,
  type TreatPartialAs,
} from "@/lib/backup-assurance-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import type { VeleroStatus } from "@/lib/velero-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

const BASE = "/v1/velero/assurance";

const ROOT_CLASS = "flex flex-col gap-5 p-6";
const ROOT_TEST_ID = "backup-assurance";

/** A failed request, reduced to what the page branches on. */
interface RequestFailure {
  status: number;
  reason?: string;
  message: string;
}

function toFailure(err: unknown): RequestFailure {
  if (err instanceof ApiError) {
    return {
      status: err.status,
      reason: err.reason,
      message: err.body?.error?.message ?? err.detail ?? "Request failed",
    };
  }
  return { status: 0, message: "The request could not be completed." };
}

function fieldErrorsOf(err: unknown): AssuranceFieldError[] {
  if (!(err instanceof ApiError)) return [];
  const raw = err.body?.error?.extra?.fieldErrors;
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (e): e is AssuranceFieldError =>
      typeof e === "object" &&
      e !== null &&
      typeof (e as AssuranceFieldError).field === "string" &&
      typeof (e as AssuranceFieldError).message === "string",
  );
}

function numberExtra(err: unknown, key: string): number | null {
  if (!(err instanceof ApiError)) return null;
  const v = err.body?.error?.extra?.[key];
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

function absolute(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** A relative time with the absolute one as a tooltip. */
function When({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={absolute(iso)}>
      {timeAgo(iso)}
    </time>
  );
}

// ---------------------------------------------------------------------------
// Chips
// ---------------------------------------------------------------------------

const SEVERITY_CLASS: Record<AssuranceSeverity, string> = {
  critical: "bg-error-dim text-error",
  warning: "bg-warning-dim text-warning",
  info: "bg-accent-dim text-accent",
};

const CONDITION_CLASS: Record<AssuranceCondition, string> = {
  overdue: "bg-warning-dim text-warning",
  failed: "bg-error-dim text-error",
  partially_failed: "bg-warning-dim text-warning",
  paused: "bg-elevated text-text-secondary",
  never_run: "bg-warning-dim text-warning",
  location_unavailable: "bg-error-dim text-error",
  collection_unknown: "bg-elevated text-text-muted",
};

const STATE_CLASS: Record<SurfaceState, string> = {
  ok: "bg-success-dim text-success",
  stale: "bg-warning-dim text-warning",
  empty: "bg-elevated text-text-secondary",
  unknown: "bg-elevated text-text-muted",
  forbidden: "bg-error-dim text-error",
  unavailable: "bg-elevated text-text-muted",
};

function Chip({
  class: cls,
  children,
  testId,
}: {
  class: string;
  children: string;
  testId?: string;
}) {
  return (
    <span
      class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}
      data-testid={testId}
    >
      {children}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Freshness row + per-condition counts
// ---------------------------------------------------------------------------

function FreshnessRow({
  state,
  status,
  veleroDetected,
}: {
  state: SurfaceState;
  status: AssuranceStatus | null;
  veleroDetected: boolean | null;
}) {
  const copy = surfaceStateCopy(state);
  const lastRunAt = status?.runtime?.lastRunAt ?? null;
  return (
    <section
      class="rounded-lg border border-border-subtle bg-surface p-4"
      aria-label="Collection freshness"
      data-testid="assurance-surface-state"
      data-state={state}
    >
      <div class="flex flex-wrap items-center gap-3">
        <span class="text-sm font-semibold text-text-primary">Collection</span>
        <Chip class={STATE_CLASS[state]} testId="assurance-state-word">
          {copy.word}
        </Chip>
        {status && (
          <span class="text-xs text-text-muted">
            {collectionSourceLabel(status.collectionSource)}
          </span>
        )}
        {lastRunAt && (
          <span class="text-xs text-text-muted">
            Last evaluated <When iso={lastRunAt} />
          </span>
        )}
      </div>
      <p class="mt-2 text-sm text-text-secondary">{copy.sentence}</p>
      {veleroDetected === false && (
        <p
          class="mt-2 text-sm text-text-secondary"
          data-testid="assurance-velero-not-detected"
        >
          Velero was not detected on this cluster, so backup state is unknown:
          whether any backups exist cannot be observed from here.
        </p>
      )}
    </section>
  );
}

function ConditionCounts({
  status,
  state,
}: {
  status: AssuranceStatus;
  state: SurfaceState;
}) {
  const current = countsAreCurrent(state);
  return (
    <section aria-label="Open exceptions by condition">
      <h2 class="mb-2 text-sm font-semibold text-text-primary">
        Open exceptions by condition
      </h2>
      {!current && (
        <p class="mb-2 text-xs text-text-muted">
          Collection is {state}: a zero below cannot be read as "none", so it is
          shown as unknown.
        </p>
      )}
      <ul class="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
        {ASSURANCE_CONDITIONS.map((c) => {
          const n = status.open.byCondition[c] ?? 0;
          const unknown = !current && n === 0;
          return (
            <li
              key={c}
              class={`rounded-lg border border-border-subtle bg-surface p-3 ${
                current ? "" : "opacity-70"
              }`}
              data-testid={`assurance-count-${c}`}
              data-unknown={unknown ? "true" : "false"}
            >
              <span class="block text-xs font-medium uppercase tracking-wide text-text-muted">
                {conditionLabel(c)}
              </span>
              {unknown ? (
                <span class="mt-1 inline-block">
                  <Chip class={STATE_CLASS.unknown}>unknown</Chip>
                </span>
              ) : (
                <span class="mt-1 block font-mono text-xl font-bold tabular-nums text-text-primary">
                  {n}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function RuntimePanel({ runtime }: { runtime: AssuranceRuntime }) {
  const backlog = runtime.deliveryBacklog;
  return (
    <section
      class="rounded-lg border border-border-subtle bg-surface p-4"
      aria-label="Collector (admin)"
      data-testid="assurance-runtime"
    >
      <h2 class="text-sm font-semibold text-text-primary">Collector</h2>
      <dl class="mt-2 grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-2 lg:grid-cols-3">
        <div class="flex gap-2">
          <dt class="text-text-muted">This replica</dt>
          <dd class="break-all font-mono text-text-secondary">
            {runtime.holder}
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Last run here</dt>
          <dd class="text-text-secondary">
            {runtime.lastRunAt ? <When iso={runtime.lastRunAt} /> : "never"}
            {runtime.lastCollection && ` (${runtime.lastCollection})`}
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Lease</dt>
          <dd class="break-all text-text-secondary">
            {runtime.lease
              ? `${runtime.lease.holder}${runtime.lease.expired ? " (expired)" : ""}`
              : "never held"}
            {runtime.leaseHeld && " — held by this replica"}
          </dd>
        </div>
        <div class="flex gap-2">
          <dt class="text-text-muted">Notification backlog</dt>
          <dd class="text-text-secondary">
            {backlog.pending} pending, {backlog.failed} failed
          </dd>
        </div>
        {runtime.lastError && (
          <div class="flex gap-2">
            <dt class="text-text-muted">Last error</dt>
            <dd class="text-error">
              {runtime.lastError}
              {runtime.lastErrorAt && (
                <>
                  {" "}
                  (<When iso={runtime.lastErrorAt} />)
                </>
              )}
            </dd>
          </div>
        )}
      </dl>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Exceptions
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Policy form
// ---------------------------------------------------------------------------

interface PolicyForm {
  scopeKind: AssuranceScopeKind;
  scopeNamespace: string;
  scopeName: string;
  /** Minutes, as typed. */
  maxAgeMinutes: string;
  graceMinutes: string;
  treatPartialAs: TreatPartialAs;
  alertOnPaused: boolean;
  enabled: boolean;
}

function emptyForm(): PolicyForm {
  return {
    scopeKind: "schedule",
    scopeNamespace: "",
    scopeName: "",
    maxAgeMinutes: String(24 * 60),
    graceMinutes: String(DEFAULT_GRACE_SECONDS / 60),
    treatPartialAs: "failure",
    alertOnPaused: true,
    enabled: true,
  };
}

function formFromPolicy(p: AssurancePolicy): PolicyForm {
  return {
    scopeKind: p.scopeKind,
    scopeNamespace: p.scopeNamespace,
    scopeName: p.scopeName,
    maxAgeMinutes: String(p.maxAgeSeconds / 60),
    graceMinutes: String(p.graceSeconds / 60),
    treatPartialAs: p.treatPartialAs,
    alertOnPaused: p.alertOnPaused,
    enabled: p.enabled,
  };
}

/** Minutes as typed → whole seconds, or null when not a number. */
function minutesToSeconds(raw: string): number | null {
  const n = Number(raw.trim());
  if (raw.trim() === "" || !Number.isFinite(n)) return null;
  return Math.round(n * 60);
}

/** Client-side checks that mirror the handler's; the server stays authoritative. */
function localFieldErrors(
  f: PolicyForm,
  creating: boolean,
): Record<string, string> {
  const errs: Record<string, string> = {};
  if (creating) {
    if (f.scopeKind !== "cluster" && f.scopeNamespace.trim() === "") {
      errs.scopeNamespace = "is required";
    }
    if (f.scopeKind === "schedule" && f.scopeName.trim() === "") {
      errs.scopeName = "is required for schedule scope";
    }
  }
  const maxAge = minutesToSeconds(f.maxAgeMinutes);
  if (maxAge === null) errs.maxAgeSeconds = "must be a number of minutes";
  else if (maxAge < MIN_MAX_AGE_SECONDS) {
    errs.maxAgeSeconds = `must be at least ${MIN_MAX_AGE_SECONDS / 60} minutes`;
  }
  const grace = minutesToSeconds(f.graceMinutes);
  if (grace === null) errs.graceSeconds = "must be a number of minutes";
  else if (grace < 0) errs.graceSeconds = "must not be negative";
  return errs;
}

const FIELD_LABELS: Record<string, string> = {
  scopeKind: "Scope",
  scopeNamespace: "Namespace",
  scopeName: "Schedule",
  maxAgeSeconds: "Maximum age",
  graceSeconds: "Grace",
  treatPartialAs: "Treat PartiallyFailed as",
  alertOnPaused: "Alert on paused",
  enabled: "Enabled",
  revision: "Revision",
};

const INPUT_CLASS =
  "w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary disabled:opacity-60";

function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null;
  return (
    <p id={id} class="mt-1 text-xs text-error" role="alert">
      {message}
    </p>
  );
}

function PolicyEditor({
  editing,
  form,
  fieldErrors,
  formError,
  conflict,
  saving,
  onChange,
  onSubmit,
  onCancel,
  onReload,
}: {
  editing: AssurancePolicy | null;
  form: PolicyForm;
  fieldErrors: Record<string, string>;
  formError: string | null;
  conflict: boolean;
  saving: boolean;
  onChange: (patch: Partial<PolicyForm>) => void;
  onSubmit: () => void;
  onCancel: () => void;
  onReload: () => void;
}) {
  const creating = editing === null;
  const ids = {
    kind: "assurance-policy-scope-kind",
    ns: "assurance-policy-namespace",
    name: "assurance-policy-schedule",
    maxAge: "assurance-policy-max-age",
    grace: "assurance-policy-grace",
    partial: "assurance-policy-partial",
  };
  const maxAgeSec = minutesToSeconds(form.maxAgeMinutes);
  const graceSec = minutesToSeconds(form.graceMinutes);
  return (
    <form
      class="rounded-lg border border-border-subtle bg-surface p-4"
      data-testid="assurance-policy-form"
      aria-label={creating ? "New freshness policy" : "Edit freshness policy"}
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <h3 class="text-sm font-semibold text-text-primary">
        {creating
          ? "New freshness policy"
          : `Edit policy for ${scopeLabel(
              editing.scopeKind,
              editing.scopeNamespace,
              editing.scopeName,
            )}`}
      </h3>
      {!creating && (
        <p class="mt-1 text-xs text-text-muted">
          A policy's scope cannot be changed. To watch a different scope, create
          a new policy.
        </p>
      )}
      <div class="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div>
          <label
            for={ids.kind}
            class="block text-xs font-medium text-text-secondary"
          >
            Scope
          </label>
          <select
            id={ids.kind}
            class={INPUT_CLASS}
            value={form.scopeKind}
            disabled={!creating}
            onChange={(e) =>
              onChange({
                scopeKind: (e.target as HTMLSelectElement)
                  .value as AssuranceScopeKind,
              })
            }
          >
            <option value="schedule">Schedule</option>
            <option value="namespace">Namespace</option>
            <option value="cluster">Cluster</option>
          </select>
          <FieldError
            id={`${ids.kind}-error`}
            message={fieldErrors.scopeKind}
          />
        </div>
        {form.scopeKind !== "cluster" && (
          <div>
            <label
              for={ids.ns}
              class="block text-xs font-medium text-text-secondary"
            >
              Namespace
            </label>
            <input
              id={ids.ns}
              class={INPUT_CLASS}
              value={form.scopeNamespace}
              disabled={!creating}
              aria-invalid={fieldErrors.scopeNamespace ? "true" : undefined}
              aria-describedby={
                fieldErrors.scopeNamespace ? `${ids.ns}-error` : undefined
              }
              onInput={(e) =>
                onChange({
                  scopeNamespace: (e.target as HTMLInputElement).value,
                })
              }
            />
            <FieldError
              id={`${ids.ns}-error`}
              message={fieldErrors.scopeNamespace}
            />
          </div>
        )}
        {form.scopeKind === "schedule" && (
          <div>
            <label
              for={ids.name}
              class="block text-xs font-medium text-text-secondary"
            >
              Schedule
            </label>
            <input
              id={ids.name}
              class={INPUT_CLASS}
              value={form.scopeName}
              disabled={!creating}
              aria-invalid={fieldErrors.scopeName ? "true" : undefined}
              aria-describedby={
                fieldErrors.scopeName ? `${ids.name}-error` : undefined
              }
              onInput={(e) =>
                onChange({ scopeName: (e.target as HTMLInputElement).value })
              }
            />
            <FieldError
              id={`${ids.name}-error`}
              message={fieldErrors.scopeName}
            />
          </div>
        )}
      </div>
      <div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div>
          <label
            for={ids.maxAge}
            class="block text-xs font-medium text-text-secondary"
          >
            Maximum age (minutes)
          </label>
          <input
            id={ids.maxAge}
            type="number"
            min={MIN_MAX_AGE_SECONDS / 60}
            step="any"
            class={INPUT_CLASS}
            value={form.maxAgeMinutes}
            aria-invalid={fieldErrors.maxAgeSeconds ? "true" : undefined}
            aria-describedby={`${ids.maxAge}-hint${
              fieldErrors.maxAgeSeconds ? ` ${ids.maxAge}-error` : ""
            }`}
            onInput={(e) =>
              onChange({ maxAgeMinutes: (e.target as HTMLInputElement).value })
            }
          />
          <p id={`${ids.maxAge}-hint`} class="mt-1 text-xs text-text-muted">
            {maxAgeSec !== null && maxAgeSec > 0
              ? `${formatSeconds(maxAgeSec)} without a successful backup opens an exception (after grace).`
              : `At least ${MIN_MAX_AGE_SECONDS / 60} minutes.`}
          </p>
          <FieldError
            id={`${ids.maxAge}-error`}
            message={fieldErrors.maxAgeSeconds}
          />
        </div>
        <div>
          <label
            for={ids.grace}
            class="block text-xs font-medium text-text-secondary"
          >
            Grace (minutes)
          </label>
          <input
            id={ids.grace}
            type="number"
            min={0}
            step="any"
            class={INPUT_CLASS}
            value={form.graceMinutes}
            aria-invalid={fieldErrors.graceSeconds ? "true" : undefined}
            aria-describedby={`${ids.grace}-hint${
              fieldErrors.graceSeconds ? ` ${ids.grace}-error` : ""
            }`}
            onInput={(e) =>
              onChange({ graceMinutes: (e.target as HTMLInputElement).value })
            }
          />
          <p id={`${ids.grace}-hint`} class="mt-1 text-xs text-text-muted">
            {graceSec !== null && graceSec >= 0
              ? `${formatSeconds(graceSec)} of slack for late runs and clock changes.`
              : "Slack for late runs and clock changes."}
          </p>
          <FieldError
            id={`${ids.grace}-error`}
            message={fieldErrors.graceSeconds}
          />
        </div>
        <div>
          <label
            for={ids.partial}
            class="block text-xs font-medium text-text-secondary"
          >
            Treat PartiallyFailed as
          </label>
          <select
            id={ids.partial}
            class={INPUT_CLASS}
            value={form.treatPartialAs}
            onChange={(e) =>
              onChange({
                treatPartialAs: (e.target as HTMLSelectElement)
                  .value as TreatPartialAs,
              })
            }
          >
            <option value="failure">Failure (opens an exception)</option>
            <option value="success">
              Success (still shown as partial, never as a plain success)
            </option>
          </select>
          <FieldError
            id={`${ids.partial}-error`}
            message={fieldErrors.treatPartialAs}
          />
        </div>
      </div>
      <div class="mt-4 flex flex-wrap gap-6 text-sm text-text-secondary">
        <label class="inline-flex items-center gap-2">
          <input
            type="checkbox"
            checked={form.alertOnPaused}
            onChange={(e) =>
              onChange({
                alertOnPaused: (e.target as HTMLInputElement).checked,
              })
            }
          />
          Open an exception when the schedule is paused
        </label>
        <label class="inline-flex items-center gap-2">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) =>
              onChange({ enabled: (e.target as HTMLInputElement).checked })
            }
          />
          Enabled
        </label>
      </div>
      {formError && (
        <div
          class="mt-4 rounded-md bg-error-dim px-3 py-2 text-sm text-error"
          role="alert"
          data-testid="assurance-policy-form-error"
        >
          {formError}
          {conflict && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              class="ml-3"
              onClick={onReload}
            >
              Reload policy
            </Button>
          )}
        </div>
      )}
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" loading={saving} disabled={conflict}>
          {creating ? "Create policy" : "Save changes"}
        </Button>
      </div>
    </form>
  );
}

function scheduleStatusNote(p: AssurancePolicy): string | null {
  switch (p.scheduleStatus) {
    case "not_found":
      return `${p.scheduleNote ?? "schedule not found"} — this policy currently watches nothing`;
    case "unknown":
      return "schedule existence unknown — Velero could not be read";
    default:
      return null;
  }
}

function PolicyTable({
  policies,
  busy,
  onEdit,
  onDelete,
}: {
  policies: AssurancePolicy[];
  busy: boolean;
  onEdit: (p: AssurancePolicy) => void;
  onDelete: (p: AssurancePolicy) => void;
}) {
  return (
    <div class="overflow-x-auto rounded-lg border border-border-subtle bg-surface">
      <table class="w-full text-left text-sm">
        <thead class="border-b border-border-subtle text-xs uppercase tracking-wide text-text-muted">
          <tr>
            <th scope="col" class="px-4 py-2 font-medium">
              Scope
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Max age
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Grace
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              PartiallyFailed
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              Paused
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              State
            </th>
            <th scope="col" class="px-4 py-2 font-medium">
              <span class="sr-only">Actions</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {policies.map((p) => {
            const note = scheduleStatusNote(p);
            return (
              <tr
                key={p.id}
                class="border-b border-border-subtle last:border-b-0"
                data-testid="assurance-policy-row"
              >
                <td class="px-4 py-2">
                  <span class="break-all font-mono text-text-primary">
                    {scopeLabel(p.scopeKind, p.scopeNamespace, p.scopeName)}
                  </span>
                  {note && (
                    <span class="block text-xs text-warning">{note}</span>
                  )}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {formatSeconds(p.maxAgeSeconds)}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {formatSeconds(p.graceSeconds)}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  as {p.treatPartialAs}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {p.alertOnPaused ? "alert" : "ignore"}
                </td>
                <td class="px-4 py-2 text-text-secondary">
                  {p.enabled ? "enabled" : "disabled"}
                </td>
                <td class="whitespace-nowrap px-4 py-2 text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => onEdit(p)}
                  >
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    class="text-error"
                    disabled={busy}
                    onClick={() => onDelete(p)}
                  >
                    Delete
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

interface DeleteTarget {
  policy: AssurancePolicy;
  openExceptions: number;
}

export default function BackupAssurance() {
  const loading = useSignal(true);
  const status = useSignal<AssuranceStatus | null>(null);
  const statusFailure = useSignal<RequestFailure | null>(null);
  const veleroDetected = useSignal<boolean | null>(null);

  const exState = useSignal<AssuranceExceptionState>("open");
  const exOffset = useSignal(0);
  const exceptions = useSignal<AssuranceException[]>([]);
  const exTotal = useSignal(0);
  const exFailure = useSignal<RequestFailure | null>(null);
  const exLoading = useSignal(false);

  const policies = useSignal<AssurancePolicy[] | null>(null);
  const policiesFailure = useSignal<RequestFailure | null>(null);

  const editorOpen = useSignal(false);
  const editing = useSignal<AssurancePolicy | null>(null);
  const form = useSignal<PolicyForm>(emptyForm());
  const fieldErrors = useSignal<Record<string, string>>({});
  const formError = useSignal<string | null>(null);
  const conflict = useSignal(false);
  const saving = useSignal(false);

  const deleteTarget = useSignal<DeleteTarget | null>(null);
  const deleting = useSignal(false);
  const deleteError = useSignal<string | null>(null);
  const notice = useSignal<string | null>(null);

  /** Bumped per load so a late reply cannot overwrite a newer one. */
  const generation = useRef(0);

  async function loadExceptions(gen: number) {
    exLoading.value = true;
    try {
      const qs = new URLSearchParams({
        state: exState.value,
        limit: String(EXCEPTION_PAGE_SIZE),
        offset: String(exOffset.value),
      });
      const res = await apiGet<AssuranceException[]>(
        `${BASE}/exceptions?${qs.toString()}`,
      );
      if (gen !== generation.current) return;
      exceptions.value = Array.isArray(res.data) ? res.data : [];
      exTotal.value = res.metadata?.total ?? exceptions.value.length;
      exFailure.value = null;
    } catch (err) {
      if (gen !== generation.current) return;
      exceptions.value = [];
      exTotal.value = 0;
      exFailure.value = toFailure(err);
    } finally {
      if (gen === generation.current) exLoading.value = false;
    }
  }

  async function loadPolicies(gen: number) {
    try {
      const res = await apiGet<AssurancePolicy[]>(`${BASE}/policies`);
      if (gen !== generation.current) return;
      policies.value = Array.isArray(res.data) ? res.data : [];
      policiesFailure.value = null;
    } catch (err) {
      if (gen !== generation.current) return;
      policies.value = null;
      policiesFailure.value = toFailure(err);
    }
  }

  async function loadAll() {
    const gen = ++generation.current;
    const [statusRes, veleroRes] = await Promise.allSettled([
      apiGet<AssuranceStatus>(`${BASE}/status`),
      apiGet<VeleroStatus>("/v1/velero/status"),
    ]);
    if (gen !== generation.current) return;

    veleroDetected.value =
      veleroRes.status === "fulfilled" &&
      typeof veleroRes.value.data?.detected === "boolean"
        ? veleroRes.value.data.detected
        : null;

    if (statusRes.status === "rejected") {
      status.value = null;
      statusFailure.value = toFailure(statusRes.reason);
      exceptions.value = [];
      policies.value = null;
      loading.value = false;
      return;
    }
    status.value = statusRes.value.data;
    statusFailure.value = null;

    // `runtime` is projected for admins only, so its presence is the
    // server's own answer to "may this caller manage policies".
    const admin = status.value?.runtime !== undefined;
    await Promise.all([
      loadExceptions(gen),
      admin ? loadPolicies(gen) : Promise.resolve(),
    ]);
    if (gen === generation.current) loading.value = false;
  }

  useEffect(() => {
    if (!IS_BROWSER) return;
    loadAll();
  }, []);

  // The SSR placeholder and the hydrated root share their attributes: Preact
  // hydration never re-applies the root's own props (see
  // server/check-placeholder-root-parity.ts).
  if (!IS_BROWSER) return <div class={ROOT_CLASS} data-testid={ROOT_TEST_ID} />;

  const failure = statusFailure.value;
  const isAdmin = status.value?.runtime !== undefined;
  const state = surfaceStateFor(status.value, failure?.status);

  function changePage(delta: number) {
    const next = Math.max(0, exOffset.value + delta * EXCEPTION_PAGE_SIZE);
    exOffset.value = next;
    loadExceptions(generation.current);
  }

  function switchExState(next: AssuranceExceptionState) {
    if (exState.value === next) return;
    exState.value = next;
    exOffset.value = 0;
    loadExceptions(generation.current);
  }

  function openCreate() {
    editing.value = null;
    form.value = emptyForm();
    fieldErrors.value = {};
    formError.value = null;
    conflict.value = false;
    editorOpen.value = true;
    notice.value = null;
  }

  function openEdit(p: AssurancePolicy) {
    editing.value = p;
    form.value = formFromPolicy(p);
    fieldErrors.value = {};
    formError.value = null;
    conflict.value = false;
    editorOpen.value = true;
    notice.value = null;
  }

  function closeEditor() {
    editorOpen.value = false;
    editing.value = null;
  }

  async function reloadEditedPolicy() {
    const current = editing.value;
    await loadPolicies(generation.current);
    const fresh = policies.value?.find((p) => p.id === current?.id);
    if (fresh) {
      openEdit(fresh);
      notice.value =
        "Reloaded the current values. Re-apply your changes and save again.";
    } else {
      closeEditor();
      notice.value = "That policy no longer exists.";
    }
  }

  async function submitPolicy() {
    const f = form.value;
    const creating = editing.value === null;
    const local = localFieldErrors(f, creating);
    fieldErrors.value = local;
    formError.value = null;
    if (Object.keys(local).length > 0) return;

    const maxAgeSeconds = minutesToSeconds(f.maxAgeMinutes) ?? 0;
    const graceSeconds = minutesToSeconds(f.graceMinutes) ?? 0;
    saving.value = true;
    try {
      if (creating) {
        const body: AssurancePolicyCreate = {
          scopeKind: f.scopeKind,
          scopeNamespace:
            f.scopeKind === "cluster" ? "" : f.scopeNamespace.trim(),
          scopeName: f.scopeKind === "schedule" ? f.scopeName.trim() : "",
          maxAgeSeconds,
          graceSeconds,
          treatPartialAs: f.treatPartialAs,
          alertOnPaused: f.alertOnPaused,
          enabled: f.enabled,
        };
        await apiPost<AssurancePolicy>(`${BASE}/policies`, body);
        notice.value =
          "Policy created. It is evaluated on the collector's next pass, within about a minute.";
      } else {
        const p = editing.value as AssurancePolicy;
        const body: AssurancePolicyUpdate = {
          revision: p.revision,
          maxAgeSeconds,
          graceSeconds,
          treatPartialAs: f.treatPartialAs,
          alertOnPaused: f.alertOnPaused,
          enabled: f.enabled,
        };
        await apiPut<AssurancePolicy>(`${BASE}/policies/${p.id}`, body);
        notice.value = "Policy saved.";
      }
      closeEditor();
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      switch (fail.reason) {
        case ASSURANCE_REASONS.invalidPolicy: {
          const errs: Record<string, string> = {};
          const unplaced: string[] = [];
          for (const fe of fieldErrorsOf(err)) {
            if (fe.field in FIELD_LABELS && fe.field !== "revision") {
              errs[fe.field] = fe.message;
            } else {
              unplaced.push(
                `${FIELD_LABELS[fe.field] ?? fe.field} ${fe.message}`,
              );
            }
          }
          fieldErrors.value = errs;
          formError.value =
            unplaced.length > 0
              ? unplaced.join("; ")
              : Object.keys(errs).length > 0
                ? "Correct the highlighted fields."
                : fail.message;
          break;
        }
        case ASSURANCE_REASONS.revisionConflict:
          conflict.value = true;
          formError.value =
            "Someone else changed this policy since you opened it. Reload it to see the current values; your edits were not saved.";
          break;
        case ASSURANCE_REASONS.policyExists:
          formError.value =
            "A policy already exists for this scope. Edit that policy instead.";
          break;
        case ASSURANCE_REASONS.policyNotFound:
          formError.value = "This policy no longer exists.";
          await loadPolicies(generation.current);
          break;
        case ASSURANCE_REASONS.scopeImmutable:
          formError.value =
            "A policy's scope cannot be changed. Create a new policy for a different scope.";
          break;
        default:
          formError.value =
            fail.status === 403
              ? "Only administrators can change freshness policies."
              : fail.message;
      }
    } finally {
      saving.value = false;
    }
  }

  /**
   * Step one of a delete: ask the server what it would discard. Without
   * ?confirm=true the handler deletes nothing and answers 400
   * confirmation_required with extra.openExceptions.
   */
  async function requestDelete(p: AssurancePolicy) {
    deleteError.value = null;
    notice.value = null;
    try {
      await api<AssurancePolicyDeleted>(`${BASE}/policies/${p.id}`, {
        method: "DELETE",
      });
      // A server that deleted without confirmation would be a contract
      // break; refresh so the page shows the truth either way.
      notice.value = "Policy deleted.";
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.confirmationRequired) {
        deleteTarget.value = {
          policy: p,
          openExceptions: numberExtra(err, "openExceptions") ?? 0,
        };
      } else if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        notice.value = "That policy no longer exists.";
        await loadPolicies(generation.current);
      } else {
        notice.value = fail.message;
      }
    }
  }

  async function confirmDelete() {
    const target = deleteTarget.value;
    if (!target) return;
    deleting.value = true;
    deleteError.value = null;
    try {
      const res = await api<AssurancePolicyDeleted>(
        `${BASE}/policies/${target.policy.id}?confirm=true`,
        { method: "DELETE" },
      );
      const n = res.data?.discardedOpenExceptions ?? target.openExceptions;
      deleteTarget.value = null;
      notice.value = `Policy deleted. ${n} open ${
        n === 1 ? "exception was" : "exceptions were"
      } discarded with its history.`;
      await loadAll();
    } catch (err) {
      const fail = toFailure(err);
      if (fail.reason === ASSURANCE_REASONS.policyNotFound) {
        deleteTarget.value = null;
        notice.value = "That policy no longer exists.";
        await loadPolicies(generation.current);
      } else {
        deleteError.value = fail.message;
      }
    } finally {
      deleting.value = false;
    }
  }

  const st = status.value;
  const exFail = exFailure.value;
  const rangeStart = exTotal.value === 0 ? 0 : exOffset.value + 1;
  const rangeEnd = exOffset.value + exceptions.value.length;

  return (
    <div class={ROOT_CLASS} data-testid={ROOT_TEST_ID}>
      {/* Honesty banner — first element, persistent, non-dismissible. */}
      <div
        class="glass-bar rounded-2xl px-5 py-3 text-sm text-text-primary"
        role="note"
        data-testid="assurance-honesty-banner"
      >
        {HONESTY_TEXT}
      </div>

      <header class="glass-bar flex flex-wrap items-start justify-between gap-4 rounded-2xl px-5 py-4">
        <div>
          <h1 class="text-2xl font-bold tracking-tight text-text-primary">
            Backup Assurance
          </h1>
          <p class="mt-1 text-sm text-text-muted">
            Freshness of what Velero reported for the local cluster, judged
            against the policies below. Freshness is not demonstrated
            recoverability and is not a recovery-point or recovery-time
            objective.
          </p>
        </div>
        <Button
          variant="secondary"
          onClick={() => loadAll()}
          loading={loading.value}
        >
          Refresh
        </Button>
      </header>

      {notice.value && (
        <div
          class="rounded-md border border-border-subtle bg-surface px-4 py-2 text-sm text-text-secondary"
          role="status"
          data-testid="assurance-notice"
        >
          {notice.value}
        </div>
      )}

      {loading.value && !st && !failure ? (
        <div class="flex justify-center py-12">
          <Spinner />
        </div>
      ) : failure?.reason === ASSURANCE_REASONS.remote ? (
        <section
          class="rounded-lg border border-border-subtle bg-surface p-6"
          data-testid="assurance-remote-unsupported"
        >
          <h2 class="text-base font-semibold text-text-primary">
            Local cluster only
          </h2>
          <p class="mt-2 text-sm text-text-secondary">
            Backup assurance is collected by the k8sCenter instance running in
            each cluster, for that cluster. The selected cluster is a remote
            cluster, so nothing is shown here — this does not mean it has no
            backup exceptions. Switch to the local cluster to view its backup
            assurance.
          </p>
        </section>
      ) : failure ? (
        <>
          <FreshnessRow
            state={state}
            status={null}
            veleroDetected={veleroDetected.value}
          />
          {state !== "forbidden" && state !== "unavailable" && (
            <div
              class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
              role="alert"
              data-testid="assurance-error"
            >
              Backup assurance status could not be loaded: {failure.message}.
              Backup state is unknown.
            </div>
          )}
        </>
      ) : st ? (
        <>
          <FreshnessRow
            state={state}
            status={st}
            veleroDetected={veleroDetected.value}
          />

          {st.policyCount === 0 && (
            <section
              class="rounded-lg border border-border-subtle bg-surface p-6"
              data-testid="assurance-empty-policies"
            >
              <p class="text-sm text-text-primary">
                {isAdmin
                  ? "No freshness policies configured. Backup assurance evaluates nothing until you add one."
                  : "No freshness policies apply to namespaces you can see. Backup assurance evaluates nothing for you until an administrator adds one."}
              </p>
            </section>
          )}

          {st.policyCount > 0 && <ConditionCounts status={st} state={state} />}

          {st.runtime && <RuntimePanel runtime={st.runtime} />}

          <section aria-label="Backup exceptions" class="flex flex-col gap-3">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-text-primary">
                {exState.value === "open"
                  ? "Open exceptions"
                  : "Resolved exceptions"}
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
                    aria-pressed={exState.value === s}
                    class={`px-3 py-1 text-xs font-medium first:rounded-l-md last:rounded-r-md ${
                      exState.value === s
                        ? "bg-elevated text-text-primary"
                        : "text-text-secondary hover:bg-hover"
                    }`}
                    onClick={() => switchExState(s)}
                  >
                    {s === "open" ? "Open" : "Resolved"}
                  </button>
                ))}
              </div>
            </div>

            {exFail?.status === 403 ? (
              <p
                class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
                data-testid="assurance-forbidden"
              >
                You do not have permission to view backup exceptions.
              </p>
            ) : exFail ? (
              <p
                class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
                role="alert"
              >
                Backup exceptions could not be loaded: {exFail.message}. Their
                state is unknown.
              </p>
            ) : exLoading.value && exceptions.value.length === 0 ? (
              <div class="flex justify-center py-6">
                <Spinner />
              </div>
            ) : exceptions.value.length === 0 ? (
              exState.value === "open" &&
              state === "ok" &&
              st.policyCount > 0 ? (
                <p
                  class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
                  data-testid="assurance-no-open"
                >
                  No open backup exceptions. This is not a recoverability
                  guarantee.
                </p>
              ) : exState.value === "open" && st.policyCount > 0 ? (
                <p
                  class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
                  data-testid="assurance-no-open-unknown"
                >
                  No open exceptions are recorded, but collection is {state}, so
                  backup state is unknown.
                </p>
              ) : exState.value === "resolved" ? (
                <p class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary">
                  No resolved exceptions are retained.
                </p>
              ) : null
            ) : (
              <>
                {groupByCondition(exceptions.value).map(([c, rows]) => (
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
                {exTotal.value > EXCEPTION_PAGE_SIZE && (
                  <div class="flex items-center justify-between text-xs text-text-muted">
                    <span>
                      {rangeStart}–{rangeEnd} of {exTotal.value}
                    </span>
                    <div class="flex gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={exOffset.value === 0 || exLoading.value}
                        onClick={() => changePage(-1)}
                      >
                        Previous
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={rangeEnd >= exTotal.value || exLoading.value}
                        onClick={() => changePage(1)}
                      >
                        Next
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
          </section>

          <section
            aria-label="Freshness policies"
            class="flex flex-col gap-3"
            data-testid="assurance-policies"
          >
            <div class="flex flex-wrap items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-text-primary">
                Freshness policies
              </h2>
              {isAdmin && !editorOpen.value && (
                <Button size="sm" onClick={openCreate}>
                  Add policy
                </Button>
              )}
            </div>
            {!isAdmin ? (
              <p
                class="rounded-lg border border-border-subtle bg-surface p-4 text-sm text-text-secondary"
                data-testid="assurance-policies-readonly"
              >
                Freshness policies are managed by administrators.{" "}
                {st.policyCount === 1
                  ? "1 policy applies"
                  : `${st.policyCount} policies apply`}{" "}
                to namespaces you can see.
              </p>
            ) : (
              <>
                {editorOpen.value && (
                  <PolicyEditor
                    editing={editing.value}
                    form={form.value}
                    fieldErrors={fieldErrors.value}
                    formError={formError.value}
                    conflict={conflict.value}
                    saving={saving.value}
                    onChange={(patch) => {
                      form.value = { ...form.value, ...patch };
                    }}
                    onSubmit={submitPolicy}
                    onCancel={closeEditor}
                    onReload={reloadEditedPolicy}
                  />
                )}
                {policiesFailure.value ? (
                  <p
                    class="rounded-md bg-error-dim px-4 py-3 text-sm text-error"
                    role="alert"
                  >
                    Policies could not be loaded:{" "}
                    {policiesFailure.value.message}
                  </p>
                ) : policies.value && policies.value.length > 0 ? (
                  <PolicyTable
                    policies={policies.value}
                    busy={saving.value || deleting.value}
                    onEdit={openEdit}
                    onDelete={requestDelete}
                  />
                ) : null}
              </>
            )}
          </section>
        </>
      ) : null}

      {deleteTarget.value && (
        <ConfirmDialog
          title="Delete freshness policy"
          danger
          confirmLabel="Delete policy"
          loading={deleting.value}
          typeToConfirm={
            deleteTarget.value.policy.scopeName ||
            deleteTarget.value.policy.scopeNamespace ||
            "cluster"
          }
          message={
            <>
              Deleting the policy for{" "}
              {scopeLabel(
                deleteTarget.value.policy.scopeKind,
                deleteTarget.value.policy.scopeNamespace,
                deleteTarget.value.policy.scopeName,
              )}{" "}
              discards every exception it opened, open and resolved, with their
              history.{" "}
              {deleteTarget.value.openExceptions === 1
                ? "1 open exception will be discarded."
                : `${deleteTarget.value.openExceptions} open exceptions will be discarded.`}
              {deleteError.value && (
                <span class="mt-2 block text-error" role="alert">
                  {deleteError.value}
                </span>
              )}
            </>
          }
          onConfirm={confirmDelete}
          onCancel={() => {
            deleteTarget.value = null;
            deleteError.value = null;
          }}
        />
      )}
    </div>
  );
}
