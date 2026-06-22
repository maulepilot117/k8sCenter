import type { ComponentChildren } from "preact";
import YamlView from "@/components/ui/YamlView.tsx";
import { wizardBusy } from "@/lib/wizard-busy.ts";

export interface WizardStep {
  label: string;
  sub?: string;
}

interface WizardShellProps {
  title: string;
  icon?: ComponentChildren;
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
  children: ComponentChildren;
}

/**
 * Floating wizard chrome shared by ALL wizards (Deployment, Service,
 * StorageClass, NetworkPolicy, ExternalSecret, …). Provides: scrim, glass
 * sheet, left step rail with checkmarks, a form column, an optional live-YAML
 * pane, and the footer. Each wizard owns its form state and renders the active
 * step's fields as children; pass the assembled manifest as `yaml`.
 */
export default function WizardShell(
  {
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
  }: WizardShellProps,
) {
  // While a server-side apply is in flight, suppress every close affordance
  // (scrim, X, Cancel) so the user cannot abandon the request with no feedback.
  const busy = wizardBusy.value;
  const guardedCancel = () => {
    if (!busy) onCancel();
  };
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
        class="glass-elevated"
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "980px",
          maxWidth: "96vw",
          maxHeight: "90vh",
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
              stroke-width="1.7"
              stroke-linecap="round"
            >
              <path d="M5 5l10 10M15 5L5 15" />
            </svg>
          </button>
        </div>

        {/* body */}
        <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
          {/* step rail */}
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
                  onClick={() => onStep(i)}
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
                        status === "active"
                          ? "var(--accent)"
                          : "var(--border-subtle)"
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

          {/* form + live yaml */}
          <div style={{ flex: 1, display: "flex", minWidth: 0 }}>
            <div
              style={{
                flex: 1,
                minWidth: 0,
                padding: "24px 26px",
                overflowY: "auto",
              }}
            >
              {children}
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
            <button
              type="button"
              onClick={onNext}
              style={{
                display: "flex",
                alignItems: "center",
                gap: "8px",
                padding: "9px 20px",
                borderRadius: "9px",
                border: "none",
                cursor: "pointer",
                color: "var(--text-on-accent)",
                fontSize: "13px",
                fontWeight: 650,
                fontFamily: "inherit",
                background:
                  "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
                boxShadow:
                  "0 8px 20px -8px color-mix(in srgb, var(--accent) 60%, transparent)",
              }}
            >
              {nextLabel}
              <svg
                width="14"
                height="14"
                viewBox="0 0 20 20"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
              >
                <path d="M7 5l5 5-5 5" />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
