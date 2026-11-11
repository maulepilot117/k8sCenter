/** Backup assurance — collection freshness, per-condition counts and the
 * admin-only collector panel (Release F U36).
 *
 * Counts are always labelled with their condition; a zero under a collection
 * that is not current is shown as unknown, never as "none".
 */

import {
  Chip,
  STATE_CLASS,
  When,
} from "@/components/velero/AssuranceBadges.tsx";
import {
  ASSURANCE_CONDITIONS,
  type AssuranceRuntime,
  type AssuranceStatus,
  collectionSourceLabel,
  conditionLabel,
  countsAreCurrent,
  type SurfaceState,
  surfaceStateCopy,
} from "@/lib/backup-assurance-types.ts";

export function FreshnessRow({
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
      className="rounded-lg border border-border-subtle bg-surface p-4"
      aria-label="Collection freshness"
      data-testid="assurance-surface-state"
      data-state={state}
    >
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm font-semibold text-text-primary">
          Collection
        </span>
        <Chip className={STATE_CLASS[state]} testId="assurance-state-word">
          {copy.word}
        </Chip>
        {status && (
          <span className="text-xs text-text-muted">
            {collectionSourceLabel(status.collectionSource)}
          </span>
        )}
        {lastRunAt && (
          <span className="text-xs text-text-muted">
            Last evaluated <When iso={lastRunAt} />
          </span>
        )}
      </div>
      <p className="mt-2 text-sm text-text-secondary">{copy.sentence}</p>
      {veleroDetected === false && (
        <p
          className="mt-2 text-sm text-text-secondary"
          data-testid="assurance-velero-not-detected"
        >
          Velero was not detected on this cluster, so backup state is unknown:
          whether any backups exist cannot be observed from here.
        </p>
      )}
    </section>
  );
}

export function ConditionCounts({
  status,
  state,
}: {
  status: AssuranceStatus;
  state: SurfaceState;
}) {
  const current = countsAreCurrent(state);
  return (
    <section aria-label="Open exceptions by condition">
      <h2 className="mb-2 text-sm font-semibold text-text-primary">
        Open exceptions by condition
      </h2>
      {!current && (
        <p className="mb-2 text-xs text-text-muted">
          Collection is {state}: a zero below cannot be read as "none", so it is
          shown as unknown.
        </p>
      )}
      <ul className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
        {ASSURANCE_CONDITIONS.map((c) => {
          const n = status.open.byCondition[c] ?? 0;
          const unknown = !current && n === 0;
          return (
            <li
              key={c}
              className={`rounded-lg border border-border-subtle bg-surface p-3 ${
                current ? "" : "opacity-70"
              }`}
              data-testid={`assurance-count-${c}`}
              data-unknown={unknown ? "true" : "false"}
            >
              <span className="block text-xs font-medium uppercase tracking-wide text-text-muted">
                {conditionLabel(c)}
              </span>
              {unknown ? (
                <span className="mt-1 inline-block">
                  <Chip className={STATE_CLASS.unknown}>unknown</Chip>
                </span>
              ) : (
                <span className="mt-1 block font-mono text-xl font-bold tabular-nums text-text-primary">
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

export function RuntimePanel({ runtime }: { runtime: AssuranceRuntime }) {
  const backlog = runtime.deliveryBacklog;
  return (
    <section
      className="rounded-lg border border-border-subtle bg-surface p-4"
      aria-label="Collector (admin)"
      data-testid="assurance-runtime"
    >
      <h2 className="text-sm font-semibold text-text-primary">Collector</h2>
      <dl className="mt-2 grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-2 lg:grid-cols-3">
        <div className="flex gap-2">
          <dt className="text-text-muted">This replica</dt>
          <dd className="break-all font-mono text-text-secondary">
            {runtime.holder}
          </dd>
        </div>
        <div className="flex gap-2">
          <dt className="text-text-muted">Last run here</dt>
          <dd className="text-text-secondary">
            {runtime.lastRunAt ? <When iso={runtime.lastRunAt} /> : "never"}
            {runtime.lastCollection && ` (${runtime.lastCollection})`}
          </dd>
        </div>
        <div className="flex gap-2">
          <dt className="text-text-muted">Lease</dt>
          <dd className="break-all text-text-secondary">
            {runtime.lease
              ? `${runtime.lease.holder}${runtime.lease.expired ? " (expired)" : ""}`
              : "never held"}
            {runtime.leaseHeld && " — held by this replica"}
          </dd>
        </div>
        <div className="flex gap-2">
          <dt className="text-text-muted">Notification backlog</dt>
          <dd className="text-text-secondary">
            {backlog.pending} pending, {backlog.failed} failed
          </dd>
        </div>
        {runtime.lastError && (
          <div className="flex gap-2">
            <dt className="text-text-muted">Last error</dt>
            <dd className="text-error">
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
