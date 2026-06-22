export interface StepRailStep {
  label: string;
  /** optional secondary line (vertical rail only) */
  sub?: string;
}

interface StepRailProps {
  steps: StepRailStep[];
  current: number;
  onStep?: (i: number) => void;
  /**
   * "vertical" — the floating WizardShell rail (numbered circles + sublabels,
   * any step clickable; the parent's onStep decides what's allowed).
   * "horizontal" — the full-page setup stepper (connector pills, only completed
   * steps clickable).
   */
  orientation?: "vertical" | "horizontal";
}

/**
 * Shared wizard step indicator. Extracted so the floating `WizardShell` modal
 * and the full-page `SetupWizard` flow share one step-rail implementation
 * (replaces the former standalone `WizardStepper`). The two orientations keep
 * their distinct, pre-existing visuals — this is dedup, not a redesign.
 */
export default function StepRail(
  { steps, current, onStep, orientation = "vertical" }: StepRailProps,
) {
  if (orientation === "horizontal") {
    return (
      <nav class="flex items-center justify-center mb-8">
        <ol class="flex items-center gap-2">
          {steps.map((step, index) => {
            const isCompleted = index < current;
            const isCurrent = index === current;
            const isClickable = isCompleted && !!onStep;

            return (
              <li key={index} class="flex items-center">
                {index > 0 && (
                  <div
                    class={`w-8 h-0.5 mx-1 ${
                      isCompleted ? "bg-brand" : "bg-elevated"
                    }`}
                  />
                )}
                <button
                  type="button"
                  onClick={() => isClickable && onStep(index)}
                  disabled={!isClickable}
                  class={`flex items-center gap-2 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                    isCurrent
                      ? "bg-brand/10 text-brand border border-brand/30"
                      : isCompleted
                      ? "text-brand hover:bg-brand/5 cursor-pointer"
                      : "text-text-muted cursor-default"
                  }`}
                >
                  <span
                    class={`flex items-center justify-center w-6 h-6 rounded-full text-xs font-bold ${
                      isCurrent
                        ? "bg-brand"
                        : isCompleted
                        ? "bg-brand"
                        : "bg-elevated text-text-muted"
                    }`}
                    style={(isCurrent || isCompleted)
                      ? { color: "var(--bg-base)" }
                      : undefined}
                  >
                    {isCompleted
                      ? (
                        <svg
                          class="w-3.5 h-3.5"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                          stroke-width="3"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M5 13l4 4L19 7"
                          />
                        </svg>
                      )
                      : index + 1}
                  </span>
                  <span class="hidden sm:inline">{step.label}</span>
                </button>
              </li>
            );
          })}
        </ol>
      </nav>
    );
  }

  // vertical (WizardShell)
  return (
    <div
      style={{
        width: "218px",
        flexShrink: 0,
        padding: "20px 16px",
        borderRight: "1px solid var(--border-subtle)",
        display: "flex",
        flexDirection: "column",
        gap: "4px",
      }}
    >
      {steps.map((s, i) => {
        const status = i < current
          ? "done"
          : i === current
          ? "active"
          : "upcoming";
        return (
          <button
            key={s.label}
            type="button"
            onClick={() => onStep?.(i)}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "12px",
              padding: "10px",
              borderRadius: "10px",
              border: "none",
              cursor: "pointer",
              background: "transparent",
              textAlign: "left",
            }}
          >
            <span
              style={{
                width: "26px",
                height: "26px",
                borderRadius: "50%",
                flexShrink: 0,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                fontSize: "12px",
                fontWeight: 700,
                background: status === "done"
                  ? "var(--accent)"
                  : status === "active"
                  ? "var(--accent-dim)"
                  : "var(--bg-elevated)",
                color: status === "done"
                  ? "var(--text-on-accent)"
                  : status === "active"
                  ? "var(--accent)"
                  : "var(--text-muted)",
                border: `1.5px solid ${
                  status === "active" ? "var(--accent)" : "var(--border-subtle)"
                }`,
              }}
            >
              {status === "done"
                ? (
                  <svg
                    width="13"
                    height="13"
                    viewBox="0 0 20 20"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="2.4"
                    stroke-linecap="round"
                    stroke-linejoin="round"
                  >
                    <path d="M4 10l4 4 8-8" />
                  </svg>
                )
                : i + 1}
            </span>
            <span
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "1px",
              }}
            >
              <span
                style={{
                  fontSize: "13px",
                  fontWeight: 600,
                  color: status === "upcoming"
                    ? "var(--text-muted)"
                    : "var(--text-primary)",
                }}
              >
                {s.label}
              </span>
              {s.sub && (
                <span
                  style={{ fontSize: "11px", color: "var(--text-muted)" }}
                >
                  {s.sub}
                </span>
              )}
            </span>
          </button>
        );
      })}
    </div>
  );
}
