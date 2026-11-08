import type { Signal } from "@preact/signals-core";
import { createContext } from "react";

/**
 * The footer's primary button, as registered by the wizard step that owns it.
 *
 * `WizardShell` renders the footer, but on the review step the action the
 * user is there to take (Apply, then "View resource") belongs to
 * `WizardReviewStep`. The shell provides this slot to its children; a step
 * that registers an action replaces the wizard's own `nextLabel` / `onNext`
 * button, and clearing it (null, or unmounting) restores that button.
 */
export interface WizardPrimaryAction {
  label: string;
  /** Runs on click. Omitted, the click falls through to the wizard's onNext. */
  onClick?: () => void;
  /** Renders the action as a link instead of a button. */
  href?: string;
  disabled?: boolean;
  loading?: boolean;
}

export const WizardPrimaryActionContext =
  createContext<Signal<WizardPrimaryAction | null> | null>(null);
