/**
 * ESO bulk-refresh dialog (Phase E Unit 15).
 *
 * Three phases live in one island:
 *   1. scope-loading — initial GET refresh-scope call
 *   2. confirm       — render visible target count + per-namespace breakdown
 *   3. progress      — poll bulk-refresh-jobs/{jobId} every 2s until completedAt
 *
 * The 409 scope_changed branch re-resolves the scope and re-prompts. The
 * 409 active_job_exists branch jumps straight to the progress phase, attaching
 * to the existing job — operators get immediate progress visibility instead of
 * being confused by "another refresh is already running."
 *
 * This island does NOT use a global modal portal; the parent renders it
 * conditionally, and the close handler (`onClose`) is the parent's signal.
 */

import { useEffect, useState } from "react";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, errorExtra } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import type {
  BulkRefreshAction,
  BulkRefreshJob,
  BulkScopeResponse,
} from "@/lib/eso-types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { useStateRef } from "@/src/lib/use-state-ref.ts";

interface Props {
  action: BulkRefreshAction;
  /** Scope identifier — for action=refresh_store: { namespace, name }. */
  target:
    | { namespace: string; name: string }
    | { name: string }
    | {
        namespace: string;
      };
  onClose: () => void;
}

type Phase = "scope-loading" | "confirm" | "submitting" | "progress" | "error";

const NAMESPACE_PREVIEW_LIMIT = 10;

export default function ESOBulkRefreshDialog({
  action,
  target,
  onClose,
}: Props) {
  // The phase is also read by the submit guard, which a second click can reach
  // before the render that follows the first.
  const [phase, setPhase, phaseRef] = useStateRef<Phase>("scope-loading");
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [scope, setScope] = useState<BulkScopeResponse | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const [job, setJob] = useState<BulkRefreshJob | null>(null);
  const targetKey = JSON.stringify(target);

  // --- scope load ---------------------------------------------------------
  // #355 item 10: Esc closes the modal. Destructive bulk-write dialog
  // without keyboard escape is hostile.
  useEffect(() => {
    if (!IS_BROWSER) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        onClose();
      }
    };
    globalThis.document.addEventListener("keydown", handler);
    return () => {
      globalThis.document.removeEventListener("keydown", handler);
    };
  }, [onClose]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    let cancelled = false;
    (async () => {
      setPhase("scope-loading");
      setErrorMsg(null);
      try {
        const res = await resolveScope(
          action,
          JSON.parse(targetKey) as Props["target"],
        );
        if (!cancelled) {
          setScope(res.data ?? null);
          setPhase("confirm");
        }
      } catch (err) {
        if (!cancelled) {
          setErrorMsg((err as Error).message ?? "Failed to load scope");
          setPhase("error");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [action, targetKey]);

  // --- progress polling ---------------------------------------------------
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (phase !== "progress" || !jobId) return;
    let cancelled = false;
    let timer: number | undefined;

    const poll = async () => {
      try {
        const res = await esoApi.getBulkRefreshJob(jobId);
        if (cancelled) return;
        const next = res.data ?? null;
        setJob(next);
        if (next?.completedAt) {
          // Done — no further polls.
          return;
        }
      } catch (err) {
        if (cancelled) return;
        setErrorMsg((err as Error).message ?? "Failed to load job");
      }
      if (!cancelled) {
        timer = setTimeout(poll, 2000) as unknown as number;
      }
    };
    void poll();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [phase, jobId]);

  const submit = async () => {
    // #355 item 9: in-flight guard. Fast double-click would otherwise fire
    // two POSTs; the server's 409 active_job_exists is recovery, not a
    // safety net the UI should rely on.
    if (!scope || phaseRef.current !== "confirm") return;
    setPhase("submitting");
    setErrorMsg(null);
    const targetUIDs = scope.targets.map((t) => t.uid);
    try {
      const res = await esoApi.bulkRefresh(action, target as never, targetUIDs);
      setJobId(res.data?.jobId ?? null);
      setPhase("progress");
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.reason === "scope_changed") {
          // Re-resolve the scope; show updated counts and re-prompt.
          setPhase("scope-loading");
          try {
            const res = await resolveScope(action, target);
            setScope(res.data ?? null);
            setPhase("confirm");
          } catch (e) {
            setErrorMsg((e as Error).message ?? "Failed to re-resolve scope");
            setPhase("error");
          }
          return;
        }
        if (err.reason === "active_job_exists") {
          // Attach to the existing job and start polling — saves the operator
          // from confusion when two tabs both hit Confirm.
          const existing = errorExtra(err, "jobId");
          if (existing) {
            setJobId(existing);
            setPhase("progress");
            return;
          }
        }
      }
      setErrorMsg((err as Error).message ?? "Failed to start refresh");
      setPhase("error");
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 glass-scrim"
    >
      <div className="w-full max-w-xl glass-elevated rounded-2xl">
        <div className="flex items-center justify-between border-b border-border-primary px-5 py-3">
          <h2 className="text-base font-semibold text-text-primary">
            Refresh ExternalSecrets
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="text-text-muted hover:text-text-primary"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <div className="px-5 py-4 min-h-[160px]">
          {phase === "scope-loading" && (
            <div className="flex items-center gap-3 text-text-muted">
              <Spinner className="text-brand" />
              <span>Resolving scope…</span>
            </div>
          )}

          {phase === "error" && (
            <p className="text-sm text-danger">{errorMsg}</p>
          )}

          {phase === "confirm" && scope && (
            <ConfirmBody
              scope={scope}
              expanded={expanded}
              onToggleExpanded={() => setExpanded(!expanded)}
            />
          )}

          {phase === "submitting" && (
            <div className="flex items-center gap-3 text-text-muted">
              <Spinner className="text-brand" />
              <span>Starting refresh…</span>
            </div>
          )}

          {phase === "progress" && (
            <ProgressBody job={job} jobId={jobId ?? undefined} />
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border-primary px-5 py-3">
          {phase === "confirm" && scope && scope.visibleCount > 0 && (
            <>
              <button
                type="button"
                onClick={onClose}
                className="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={submit}
                disabled={phase !== "confirm"}
                className="px-3 py-1.5 text-sm rounded bg-brand hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed"
                style={{ color: "var(--bg-base)" }}
              >
                Refresh {scope.visibleCount}
              </button>
            </>
          )}

          {phase === "confirm" && scope && scope.visibleCount === 0 && (
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary"
            >
              Close
            </button>
          )}

          {(phase === "progress" || phase === "error") && (
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
            >
              {phase === "progress" && job?.completedAt
                ? "Close"
                : "Run in background"}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function ConfirmBody({
  scope,
  expanded,
  onToggleExpanded,
}: {
  scope: BulkScopeResponse;
  expanded: boolean;
  onToggleExpanded: () => void;
}) {
  if (scope.visibleCount === 0) {
    return (
      <p className="text-sm text-text-muted">
        No ExternalSecrets are in scope, or you don't have permission to refresh
        them.
      </p>
    );
  }
  const showAll =
    expanded || scope.byNamespace.length <= NAMESPACE_PREVIEW_LIMIT;
  const visibleByNs = showAll
    ? scope.byNamespace
    : scope.byNamespace.slice(0, NAMESPACE_PREVIEW_LIMIT);
  const remaining = scope.byNamespace.length - NAMESPACE_PREVIEW_LIMIT;

  return (
    <div className="space-y-3 text-sm">
      <p className="text-text-primary">
        <span className="font-semibold">{scope.visibleCount}</span>{" "}
        ExternalSecret
        {scope.visibleCount === 1 ? "" : "s"} across{" "}
        <span className="font-semibold">{scope.byNamespace.length}</span>{" "}
        namespace
        {scope.byNamespace.length === 1 ? "" : "s"} will receive a force-sync.
      </p>

      {scope.restricted && (
        <p className="text-xs text-text-muted bg-base border border-border-subtle rounded px-3 py-2">
          Showing only resources you can refresh.{" "}
          {scope.totalCount - scope.visibleCount} additional ExternalSecret
          {scope.totalCount - scope.visibleCount === 1 ? "" : "s"}{" "}
          {scope.totalCount - scope.visibleCount === 1 ? "is" : "are"} out of
          your visibility.
        </p>
      )}

      <ul className="border border-border-primary rounded text-xs divide-y divide-border-primary">
        {visibleByNs.map((row) => (
          <li
            key={row.namespace}
            className="flex items-center justify-between px-3 py-1.5"
          >
            <span className="text-text-primary font-mono">{row.namespace}</span>
            <span className="text-text-muted">{row.count}</span>
          </li>
        ))}
      </ul>

      {!showAll && remaining > 0 && (
        <button
          type="button"
          onClick={onToggleExpanded}
          className="text-xs text-text-muted hover:text-text-primary"
        >
          Show {remaining} more namespace{remaining === 1 ? "" : "s"}
        </button>
      )}
    </div>
  );
}

function ProgressBody({
  job,
  jobId,
}: {
  job: BulkRefreshJob | null;
  jobId?: string;
}) {
  if (!job) {
    return (
      <div className="flex items-center gap-3 text-text-muted">
        <Spinner className="text-brand" />
        <span>Tracking job {jobId ?? "…"}</span>
      </div>
    );
  }
  const done = job.completedAt != null;
  const processed =
    job.succeeded.length + job.failed.length + job.skipped.length;
  const pct =
    job.targetCount > 0 ? Math.round((processed / job.targetCount) * 100) : 0;

  return (
    <div className="space-y-3 text-sm">
      <div className="flex items-center justify-between">
        <p className="text-text-primary font-semibold">
          {done ? "Refresh complete" : "Refreshing…"}
        </p>
        <p className="text-xs text-text-muted">
          {processed} / {job.targetCount}
        </p>
      </div>

      <div className="w-full h-2 bg-base rounded overflow-hidden border border-border-subtle">
        <div
          className="h-full bg-brand transition-all duration-200"
          style={{ width: `${pct}%` }}
        />
      </div>

      <dl className="grid grid-cols-3 gap-3 text-xs">
        <div>
          <dt className="text-text-muted">Succeeded</dt>
          <dd className="text-success font-semibold">{job.succeeded.length}</dd>
        </div>
        <div>
          <dt className="text-text-muted">Failed</dt>
          <dd className="text-danger font-semibold">{job.failed.length}</dd>
        </div>
        <div>
          <dt className="text-text-muted">Skipped</dt>
          <dd className="text-warning font-semibold">{job.skipped.length}</dd>
        </div>
      </dl>

      {done && job.failed.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-text-muted hover:text-text-primary">
            Show failed ({job.failed.length})
          </summary>
          <ul className="mt-2 border border-border-primary rounded divide-y divide-border-primary">
            {job.failed.map((f) => (
              <li
                key={f.uid}
                className="px-3 py-1.5 flex items-center justify-between font-mono"
              >
                <span className="text-text-primary truncate">{f.uid}</span>
                <span className="text-danger">{f.reason}</span>
              </li>
            ))}
          </ul>
        </details>
      )}

      {done && job.skipped.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-text-muted hover:text-text-primary">
            Show skipped ({job.skipped.length})
          </summary>
          <ul className="mt-2 border border-border-primary rounded divide-y divide-border-primary">
            {job.skipped.map((s) => (
              <li
                key={s.uid}
                className="px-3 py-1.5 flex items-center justify-between font-mono"
              >
                <span className="text-text-primary truncate">{s.uid}</span>
                <span className="text-warning">{s.reason}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function resolveScope(action: BulkRefreshAction, target: Props["target"]) {
  if (action === "refresh_store") {
    const t = target as { namespace: string; name: string };
    return esoApi.resolveStoreScope(t.namespace, t.name);
  }
  if (action === "refresh_cluster_store") {
    const t = target as { name: string };
    return esoApi.resolveClusterStoreScope(t.name);
  }
  const t = target as { namespace: string };
  return esoApi.resolveNamespaceScope(t.namespace);
}
