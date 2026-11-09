/** @jsxImportSource react */
import type { ReactNode } from "react";
import YamlView from "@/components/ui/YamlView.tsx";
import StepRail from "@/components/wizard/StepRail.tsx";
import { wizardBusy } from "@/lib/wizard-busy.ts";
import {
  type WizardPrimaryAction,
  WizardPrimaryActionContext,
} from "@/lib/wizard-primary-action.ts";
import { useSignal } from "@/src/lib/signal-hooks.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

export interface WizardStep {
  label: string;
  sub?: string;
}

interface WizardShellProps {
  title: string;
  icon?: ReactNode;
  subtitle?: string;
  steps: WizardStep[];
  current: number;
  onStep: (i: number) => void;
  onCancel: () => void;
  onBack: () => void;
  onNext: () => void;
  nextLabel: string;
  /** live manifest text — pass undefined to hide the right pane */
  yaml?: string;
  /** the active step's form */
  children: ReactNode;
}

/**
 * Floating wizard chrome shared by ALL wizards (Deployment, Service,
 * StorageClass, NetworkPolicy, ExternalSecret, …). Provides: scrim, glass
 * sheet, left step rail with checkmarks, a form column, an optional live-YAML
 * pane, and the footer. Each wizard owns its form state and renders the active
 * step's fields as children; pass the assembled manifest as `yaml`.
 *
 * The footer's primary button is `nextLabel` / `onNext` unless the active step
 * registers its own action through `WizardPrimaryActionContext` (the review
 * step does, for Apply).
 */
export default function WizardShell({
  title,
  icon,
  subtitle,
  steps,
  current,
  onStep,
  onCancel,
  onBack,
  onNext,
  nextLabel,
  yaml,
  children,
}: WizardShellProps) {
  // While a server-side apply is in flight, suppress every close affordance
  // (scrim, X, Cancel) so the user cannot abandon the request with no feedback.
  const busy = useSignalValue(wizardBusy);
  const guardedCancel = () => {
    if (!busy) onCancel();
  };
  const registered = useSignal<WizardPrimaryAction | null>(null);
  const action = useSignalValue(registered);
  const primaryLabel = action?.label ?? nextLabel;
  const primaryDisabled = action?.disabled ?? false;
  const primaryStyle = {
    display: "flex",
    alignItems: "center",
    gap: "8px",
    padding: "9px 20px",
    borderRadius: "9px",
    border: "none",
    cursor: primaryDisabled ? "not-allowed" : "pointer",
    opacity: primaryDisabled ? 0.5 : 1,
    color: "var(--text-on-accent)",
    fontSize: "13px",
    fontWeight: 650,
    fontFamily: "inherit",
    textDecoration: "none",
    background:
      "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
    boxShadow:
      "0 8px 20px -8px color-mix(in srgb, var(--accent) 60%, transparent)",
  };
  const primaryContent = (
    <>
      {primaryLabel}
      <svg
        width="14"
        height="14"
        viewBox="0 0 20 20"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        className={action?.loading ? "animate-spin" : undefined}
      >
        {action?.loading ? (
          <path d="M10 3a7 7 0 1 0 7 7" />
        ) : (
          <path d="M7 5l5 5-5 5" />
        )}
      </svg>
    </>
  );
  return (
    <div
      onClick={guardedCancel}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 220,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "24px",
        background: "var(--glass-scrim)",
        backdropFilter: "blur(10px)",
        WebkitBackdropFilter: "blur(10px)",
      }}
    >
      <div
        className="glass-elevated"
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "980px",
          maxWidth: "96vw",
          // A fixed height, not a max: the sheet keeps its size from step to
          // step so the footer stays put, and the review step's editor can
          // fill the space left instead of a hard-coded height.
          height: "min(860px, 90vh)",
          display: "flex",
          flexDirection: "column",
          borderRadius: "20px",
          overflow: "hidden",
        }}
      >
        {/* header */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "16px",
            padding: "18px 22px",
            borderBottom: "1px solid var(--border-subtle)",
            flexShrink: 0,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: "13px" }}>
            {icon && (
              <span
                style={{
                  width: "40px",
                  height: "40px",
                  borderRadius: "11px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  background: "var(--accent-dim)",
                  color: "var(--accent)",
                  flexShrink: 0,
                }}
              >
                {icon}
              </span>
            )}
            <div>
              <div
                style={{
                  fontSize: "17px",
                  fontWeight: 700,
                  letterSpacing: "-0.01em",
                }}
              >
                {title}
              </div>
              {subtitle && (
                <div style={{ fontSize: "12.5px", color: "var(--text-muted)" }}>
                  {subtitle}
                </div>
              )}
            </div>
          </div>
          <button
            type="button"
            onClick={guardedCancel}
            disabled={busy}
            aria-label="Close"
            style={{
              width: "32px",
              height: "32px",
              borderRadius: "9px",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              border: "none",
              cursor: busy ? "not-allowed" : "pointer",
              background: "transparent",
              color: "var(--text-muted)",
              opacity: busy ? 0.4 : 1,
            }}
          >
            <svg
              width="17"
              height="17"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.7"
              strokeLinecap="round"
            >
              <path d="M5 5l10 10M15 5L5 15" />
            </svg>
          </button>
        </div>

        {/* body */}
        <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
          {/* step rail */}
          <StepRail
            steps={steps}
            current={current}
            onStep={onStep}
            orientation="vertical"
          />

          {/* form + live yaml */}
          <div style={{ flex: 1, display: "flex", minWidth: 0 }}>
            <div
              style={{
                flex: 1,
                minWidth: 0,
                padding: "24px 26px",
                overflowY: "auto",
                display: "flex",
                flexDirection: "column",
              }}
            >
              <WizardPrimaryActionContext.Provider value={registered}>
                {children}
              </WizardPrimaryActionContext.Provider>
            </div>

            {yaml !== undefined && (
              <div
                style={{
                  width: "368px",
                  flexShrink: 0,
                  display: "flex",
                  flexDirection: "column",
                  borderLeft: "1px solid var(--border-subtle)",
                  background: "var(--bg-surface)",
                }}
              >
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    padding: "12px 16px",
                    borderBottom: "1px solid var(--border-subtle)",
                    flexShrink: 0,
                  }}
                >
                  <span
                    style={{
                      width: "7px",
                      height: "7px",
                      borderRadius: "50%",
                      background: "var(--success)",
                    }}
                  />
                  <span
                    style={{
                      fontSize: "12px",
                      fontWeight: 600,
                      color: "var(--text-secondary)",
                    }}
                  >
                    Live manifest
                  </span>
                </div>
                <div style={{ flex: 1, overflow: "auto" }}>
                  <YamlView text={yaml} fontSize={12} maxHeight={9999} />
                </div>
              </div>
            )}
          </div>
        </div>

        {/* footer */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "16px",
            padding: "15px 22px",
            borderTop: "1px solid var(--border-subtle)",
            flexShrink: 0,
          }}
        >
          <button
            type="button"
            onClick={guardedCancel}
            disabled={busy}
            style={{
              padding: "9px 16px",
              borderRadius: "9px",
              border: "none",
              cursor: busy ? "not-allowed" : "pointer",
              background: "transparent",
              color: "var(--text-muted)",
              fontSize: "13px",
              fontWeight: 600,
              fontFamily: "inherit",
              opacity: busy ? 0.4 : 1,
            }}
          >
            Cancel
          </button>
          <div style={{ display: "flex", gap: "10px" }}>
            {current > 0 && (
              <button
                type="button"
                onClick={onBack}
                style={{
                  padding: "9px 18px",
                  borderRadius: "9px",
                  cursor: "pointer",
                  background: "var(--bg-elevated)",
                  border: "1px solid var(--border-subtle)",
                  color: "var(--text-secondary)",
                  fontSize: "13px",
                  fontWeight: 600,
                  fontFamily: "inherit",
                }}
              >
                Back
              </button>
            )}
            {action?.href ? (
              <a href={action.href} style={primaryStyle}>
                {primaryContent}
              </a>
            ) : (
              <button
                type="button"
                onClick={action?.onClick ?? onNext}
                disabled={primaryDisabled}
                aria-busy={action?.loading || undefined}
                style={primaryStyle}
              >
                {primaryContent}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
