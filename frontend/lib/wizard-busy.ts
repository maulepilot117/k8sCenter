import { signal } from "@preact/signals";

/**
 * True while a wizard's server-side apply (`/v1/yaml/apply`) is in flight.
 *
 * A single wizard modal is open at a time, so a module-level signal is the
 * simplest correct channel between `WizardReviewStep` (which owns the apply
 * call) and `WizardShell` (which owns the scrim / Cancel / close controls).
 * While busy, `WizardShell` suppresses scrim-click, the X button, and Cancel
 * so the user cannot abandon an in-flight apply with no feedback.
 *
 * `WizardReviewStep` sets this around `handleApply` and resets it on unmount.
 */
export const wizardBusy = signal(false);
