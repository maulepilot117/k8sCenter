import {
  type Completeness,
  completenessDescription,
  completenessLabel,
} from "@/lib/incident-types.ts";

/**
 * Class strings and small presentational pieces shared by the incident
 * islands (IncidentList, IncidentWorkspace, IncidentEvidenceTimeline,
 * IncidentNotes) and the workspace panels beside this file. One definition, so
 * a focus-ring or disabled-state change lands everywhere at once.
 *
 * Buttons use `aria-disabled`, not `disabled`, so they stay focusable while a
 * request is in flight; every handler checks its own busy flag.
 */

export const FOCUS_RING =
  "focus:outline-none focus-visible:ring-2 focus-visible:ring-brand/50";

export const BUTTON_PRIMARY = `inline-flex cursor-pointer items-center justify-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-(--bg-base) ${FOCUS_RING} aria-disabled:cursor-not-allowed aria-disabled:opacity-50`;

export const BUTTON_SECONDARY = `inline-flex cursor-pointer items-center justify-center rounded-md border border-border-primary bg-transparent px-3 py-1.5 text-sm font-medium text-text-secondary ${FOCUS_RING} aria-disabled:cursor-not-allowed aria-disabled:opacity-50`;

export const FIELD =
  "block w-full rounded-md border border-border-primary bg-surface px-3 py-2 text-sm text-text-primary focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand/50 disabled:opacity-60";

export const LINK = `rounded-sm text-accent ${FOCUS_RING}`;

/** A solid data panel (Liquid Glass: data surfaces stay solid). */
export const PANEL =
  "flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4";

/** Page chrome (the incident header): glass is for chrome only. */
export const CHROME = "glass flex flex-col gap-3 rounded-lg p-4";

export const HEADING = "m-0 text-base font-semibold text-text-primary";

const BADGE = "inline-flex rounded-full px-2 py-0.5 text-xs font-medium";

export const BADGE_CLASS = {
  base: BADGE,
  accent: `${BADGE} bg-accent-dim text-accent`,
  accentOutline: `${BADGE} border border-accent text-accent`,
  muted: `${BADGE} border border-border-primary text-text-muted`,
} as const;

const COMPLETENESS_CLASS: Record<Completeness, string> = {
  complete: "bg-success-dim text-success",
  partial: "bg-warning-dim text-warning",
  failed: "bg-danger-dim text-danger",
  forbidden: "border border-danger text-danger",
  timed_out: "border border-warning text-warning",
};

/** One of the five completeness states, each visually distinct. */
export function CompletenessBadge({ value }: { value: Completeness }) {
  return (
    <span
      class={`${BADGE} ${COMPLETENESS_CLASS[value] ?? "border border-border-subtle text-text-muted"}`}
      title={completenessDescription(value)}
    >
      {completenessLabel(value)}
    </span>
  );
}
