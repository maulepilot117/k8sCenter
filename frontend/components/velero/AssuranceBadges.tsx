/** Backup assurance — shared chips and time display (Release F U36).
 *
 * Small presentational primitives the BackupAssurance island and its panels
 * share. Theme colours come from the token utilities only.
 */

import type {
  AssuranceCondition,
  AssuranceSeverity,
  SurfaceState,
} from "@/lib/backup-assurance-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

export function absolute(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

/** A relative time with the absolute one as a tooltip. */
export function When({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={absolute(iso)}>
      {timeAgo(iso)}
    </time>
  );
}

export const SEVERITY_CLASS: Record<AssuranceSeverity, string> = {
  critical: "bg-error-dim text-error",
  warning: "bg-warning-dim text-warning",
  info: "bg-accent-dim text-accent",
};

export const CONDITION_CLASS: Record<AssuranceCondition, string> = {
  overdue: "bg-warning-dim text-warning",
  failed: "bg-error-dim text-error",
  partially_failed: "bg-warning-dim text-warning",
  paused: "bg-elevated text-text-secondary",
  never_run: "bg-warning-dim text-warning",
  location_unavailable: "bg-error-dim text-error",
  collection_unknown: "bg-elevated text-text-muted",
};

export const STATE_CLASS: Record<SurfaceState, string> = {
  ok: "bg-success-dim text-success",
  stale: "bg-warning-dim text-warning",
  empty: "bg-elevated text-text-secondary",
  unknown: "bg-elevated text-text-muted",
  forbidden: "bg-error-dim text-error",
  unavailable: "bg-elevated text-text-muted",
};

export function Chip({
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
