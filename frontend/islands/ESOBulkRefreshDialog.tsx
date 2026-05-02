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

import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";

import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, errorExtra } from "@/lib/api.ts";
import { esoApi } from "@/lib/eso-api.ts";
import type {
  BulkRefreshAction,
  BulkRefreshJob,
  BulkScopeResponse,
} from "@/lib/eso-types.ts";

interface Props {
  action: BulkRefreshAction;
  /** Scope identifier — for action=refresh_store: { namespace, name }. */
  target: { namespace: string; name: string } | { name: string } | {
    namespace: string;
  };
  onClose: () => void;
}

type Phase = "scope-loading" | "confirm" | "submitting" | "progress" | "error";

const NAMESPACE_PREVIEW_LIMIT = 10;

export default function ESOBulkRefreshDialog(
  { action, target, onClose }: Props,
) {
  const phase = useSignal<Phase>("scope-loading");
  const errorMsg = useSignal<string | null>(null);
  const scope = useSignal<BulkScopeResponse | null>(null);
  const expanded = useSignal(false);
  const jobId = useSignal<string | null>(null);
  const job = useSignal<BulkRefreshJob | null>(null);

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
      phase.value = "scope-loading";
      errorMsg.value = null;
      try {
        const res = await resolveScope(action, target);
        if (!cancelled) {
          scope.value = res.data ?? null;
          phase.value = "confirm";
        }
      } catch (err) {
        if (!cancelled) {
          errorMsg.value = (err as Error).message ?? "Failed to load scope";
          phase.value = "error";
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [action, JSON.stringify(target)]);

  // --- progress polling ---------------------------------------------------
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (phase.value !== "progress" || !jobId.value) return;
    let cancelled = false;
    let timer: number | undefined;

    const poll = async () => {
      try {
        const res = await esoApi.getBulkRefreshJob(jobId.value!);
        if (cancelled) return;
        job.value = res.data ?? null;
        if (job.value?.completedAt) {
          // Done — no further polls.
          return;
        }
      } catch (err) {
        if (cancelled) return;
        errorMsg.value = (err as Error).message ?? "Failed to load job";
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
  }, [phase.value, jobId.value]);

  const submit = async () => {
    // #355 item 9: in-flight guard. Fast double-click would otherwise fire
    // two POSTs; the server's 409 active_job_exists is recovery, not a
    // safety net the UI should rely on.
    if (!scope.value || phase.value !== "confirm") return;
    phase.value = "submitting";
    errorMsg.value = null;
    const targetUIDs = scope.value.targets.map((t) => t.uid);
    try {
      const res = await esoApi.bulkRefresh(action, target as never, targetUIDs);
      jobId.value = res.data?.jobId ?? null;
      phase.value = "progress";
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.reason === "scope_changed") {
          // Re-resolve the scope; show updated counts and re-prompt.
          phase.value = "scope-loading";
          try {
            const res = await resolveScope(action, target);
            scope.value = res.data ?? null;
            phase.value = "confirm";
          } catch (e) {
            errorMsg.value = (e as Error).message ??
              "Failed to re-resolve scope";
            phase.value = "error";
          }
          return;
        }
        if (err.reason === "active_job_exists") {
          // Attach to the existing job and start polling — saves the operator
          // from confusion when two tabs both hit Confirm.
          const existing = errorExtra(err, "jobId");
          if (existing) {
            jobId.value = existing;
            phase.value = "progress";
            return;
          }
        }
      }
      errorMsg.value = (err as Error).message ?? "Failed to start refresh";
      phase.value = "error";
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50"
    >
      <div class="w-full max-w-xl rounded-lg border border-border-primary bg-elevated shadow-xl">
        <div class="flex items-center justify-between border-b border-border-primary px-5 py-3">
          <h2 class="text-base font-semibold text-text-primary">
            Refresh ExternalSecrets
          </h2>
          <button
            type="button"
            onClick={onClose}
            class="text-text-muted hover:text-text-primary"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <div class="px-5 py-4 min-h-[160px]">
          {phase.value === "scope-loading" && (
            <div class="flex items-center gap-3 text-text-muted">
              <Spinner class="text-brand" />
              <span>Resolving scope…</span>
            </div>
          )}

          {phase.value === "error" && (
            <p class="text-sm text-danger">{errorMsg.value}</p>
          )}

          {phase.value === "confirm" && scope.value && (
            <ConfirmBody
              scope={scope.value}
              expanded={expanded.value}
              onToggleExpanded={() => (expanded.value = !expanded.value)}
            />
          )}

          {phase.value === "submitting" && (
            <div class="flex items-center gap-3 text-text-muted">
              <Spinner class="text-brand" />
              <span>Starting refresh…</span>
            </div>
          )}

          {phase.value === "progress" && (
            <ProgressBody job={job.value} jobId={jobId.value ?? undefined} />
          )}
        </div>

        <div class="flex items-center justify-end gap-2 border-t border-border-primary px-5 py-3">
          {phase.value === "confirm" && scope.value &&
            scope.value.visibleCount > 0 && (
            <>
              <button
                type="button"
                onClick={onClose}
                class="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={submit}
                disabled={phase.value !== "confirm"}
                class="px-3 py-1.5 text-sm rounded bg-brand text-white hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed"
              >
                Refresh {scope.value.visibleCount}
              </button>
            </>
          )}

          {phase.value === "confirm" && scope.value &&
            scope.value.visibleCount === 0 && (
            <button
              type="button"
              onClick={onClose}
              class="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary"
            >
              Close
            </button>
          )}

          {(phase.value === "progress" || phase.value === "error") && (
            <button
              type="button"
              onClick={onClose}
              class="px-3 py-1.5 text-sm rounded border border-border-primary text-text-primary hover:bg-base"
            >
              {phase.value === "progress" && job.value?.completedAt
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
      <p class="text-sm text-text-muted">
        No ExternalSecrets are in scope, or you don't have permission to refresh
        them.
      </p>
    );
  }
  const showAll = expanded ||
    scope.byNamespace.length <= NAMESPACE_PREVIEW_LIMIT;
  const visibleByNs = showAll
    ? scope.byNamespace
    : scope.byNamespace.slice(0, NAMESPACE_PREVIEW_LIMIT);
  const remaining = scope.byNamespace.length - NAMESPACE_PREVIEW_LIMIT;

  return (
    <div class="space-y-3 text-sm">
      <p class="text-text-primary">
        <span class="font-semibold">{scope.visibleCount}</span> ExternalSecret
        {scope.visibleCount === 1 ? "" : "s"} across{" "}
        <span class="font-semibold">{scope.byNamespace.length}</span> namespace
        {scope.byNamespace.length === 1 ? "" : "s"} will receive a force-sync.
      </p>

      {scope.restricted && (
        <p class="text-xs text-text-muted bg-base border border-border-subtle rounded px-3 py-2">
          Showing only resources you can refresh.{" "}
          {scope.totalCount - scope.visibleCount} additional ExternalSecret
          {scope.totalCount - scope.visibleCount === 1 ? "" : "s"}{" "}
          {scope.totalCount - scope.visibleCount === 1 ? "is" : "are"}{" "}
          out of your visibility.
        </p>
      )}

      <ul class="border border-border-primary rounded text-xs divide-y divide-border-primary">
        {visibleByNs.map((row) => (
          <li
            key={row.namespace}
            class="flex items-center justify-between px-3 py-1.5"
          >
            <span class="text-text-primary font-mono">{row.namespace}</span>
            <span class="text-text-muted">{row.count}</span>
          </li>
        ))}
      </ul>

      {!showAll && remaining > 0 && (
        <button
          type="button"
          onClick={onToggleExpanded}
          class="text-xs text-text-muted hover:text-text-primary"
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
      <div class="flex items-center gap-3 text-text-muted">
        <Spinner class="text-brand" />
        <span>Tracking job {jobId ?? "…"}</span>
      </div>
    );
  }
  const done = job.completedAt != null;
  const processed = job.succeeded.length + job.failed.length +
    job.skipped.length;
  const pct = job.targetCount > 0
    ? Math.round((processed / job.targetCount) * 100)
    : 0;

  return (
    <div class="space-y-3 text-sm">
      <div class="flex items-center justify-between">
        <p class="text-text-primary font-semibold">
          {done ? "Refresh complete" : "Refreshing…"}
        </p>
        <p class="text-xs text-text-muted">
          {processed} / {job.targetCount}
        </p>
      </div>

      <div class="w-full h-2 bg-base rounded overflow-hidden border border-border-subtle">
        <div
          class="h-full bg-brand transition-all duration-200"
          style={{ width: `${pct}%` }}
        />
      </div>

      <dl class="grid grid-cols-3 gap-3 text-xs">
        <div>
          <dt class="text-text-muted">Succeeded</dt>
          <dd class="text-success font-semibold">{job.succeeded.length}</dd>
        </div>
        <div>
          <dt class="text-text-muted">Failed</dt>
          <dd class="text-danger font-semibold">{job.failed.length}</dd>
        </div>
        <div>
          <dt class="text-text-muted">Skipped</dt>
          <dd class="text-warning font-semibold">{job.skipped.length}</dd>
        </div>
      </dl>

      {done && job.failed.length > 0 && (
        <details class="text-xs">
          <summary class="cursor-pointer text-text-muted hover:text-text-primary">
            Show failed ({job.failed.length})
          </summary>
          <ul class="mt-2 border border-border-primary rounded divide-y divide-border-primary">
            {job.failed.map((f) => (
              <li
                key={f.uid}
                class="px-3 py-1.5 flex items-center justify-between font-mono"
              >
                <span class="text-text-primary truncate">{f.uid}</span>
                <span class="text-danger">{f.reason}</span>
              </li>
            ))}
          </ul>
        </details>
      )}

      {done && job.skipped.length > 0 && (
        <details class="text-xs">
          <summary class="cursor-pointer text-text-muted hover:text-text-primary">
            Show skipped ({job.skipped.length})
          </summary>
          <ul class="mt-2 border border-border-primary rounded divide-y divide-border-primary">
            {job.skipped.map((s) => (
              <li
                key={s.uid}
                class="px-3 py-1.5 flex items-center justify-between font-mono"
              >
                <span class="text-text-primary truncate">{s.uid}</span>
                <span class="text-warning">{s.reason}</span>
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
