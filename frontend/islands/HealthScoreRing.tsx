import { useSignal } from "@preact/signals";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { healthStatusColor, scoreColor } from "@/lib/score-color.ts";
import type { ClusterHealth, HealthSignal } from "@/lib/score-color.ts";

interface HealthScoreRingProps {
  health?: ClusterHealth;
}

// The four weighted signals surfaced as chips (certificates/storage/controlPlane
// surface through the reasons list only).
const CHIP_SIGNAL_NAMES = ["nodes", "workloads", "pods", "alerts"] as const;

// Chip display labels keyed by signal name.
const CHIP_LABELS: Record<string, string> = {
  nodes: "Nodes",
  workloads: "Workloads",
  pods: "Pods",
  alerts: "Alerts",
};

export default function HealthScoreRing({ health }: HealthScoreRingProps) {
  const showAllReasons = useSignal(false);

  // --- Stale-backend state: health field absent (R11) -----------------------
  if (health === undefined) {
    return (
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: "16px",
          padding: "20px",
          background: "var(--bg-surface)",
          borderRadius: "12px",
          border: "1px solid var(--border-subtle)",
        }}
      >
        <span
          style={{
            fontSize: "13px",
            color: "var(--text-muted)",
            textAlign: "center",
            padding: "40px 0",
          }}
        >
          Health score unavailable — backend update pending
        </span>
      </div>
    );
  }

  const { status, score, signals, reasons } = health;

  // Build a quick lookup from signal name → HealthSignal.
  const signalMap = new Map<string, HealthSignal>(
    signals.map((sig) => [sig.name, sig]),
  );

  // --- Determine ring props based on status ---------------------------------
  const isUnknown = status === "unknown";
  const ringColor = healthStatusColor(status);
  const ringValue = score ?? 0;
  const ringDisplayValue = score !== null ? String(score) : "—";

  // --- Reasons list (first 3 visible, rest toggled) -------------------------
  const MAX_VISIBLE = 3;
  const visibleReasons = showAllReasons.value
    ? reasons
    : reasons.slice(0, MAX_VISIBLE);
  const hiddenCount = reasons.length - MAX_VISIBLE;

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: "16px",
        padding: "20px",
        background: "var(--bg-surface)",
        borderRadius: "12px",
        border: "1px solid var(--border-subtle)",
      }}
    >
      {/* Main gauge */}
      <GaugeRing
        value={ringValue}
        size={160}
        strokeWidth={8}
        color={ringColor}
        label="Health"
        displayValue={ringDisplayValue}
        valueSize="42px"
        indeterminate={isUnknown}
      />

      {/* Sub-score chips — exactly 4: Nodes, Workloads, Pods, Alerts */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(4, 1fr)",
          gap: "8px",
          width: "100%",
        }}
      >
        {CHIP_SIGNAL_NAMES.map((name) => {
          const sig = signalMap.get(name);
          const isOk = sig?.status === "ok";
          const chipScore = isOk && sig?.score !== null ? sig.score! : null;
          const chipColor = chipScore !== null
            ? scoreColor(chipScore, name)
            : "var(--text-muted)";
          const reasonText = sig?.reason;
          const chipId = `chip-${name}-reason`;

          return (
            <div
              key={name}
              aria-describedby={!isOk && reasonText ? chipId : undefined}
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                padding: "8px 6px",
                borderRadius: "8px",
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              {/* Visually-hidden reason span for screen readers */}
              {!isOk && reasonText && (
                <span
                  id={chipId}
                  style={{
                    position: "absolute",
                    width: "1px",
                    height: "1px",
                    padding: "0",
                    margin: "-1px",
                    overflow: "hidden",
                    clip: "rect(0,0,0,0)",
                    whiteSpace: "nowrap",
                    border: "0",
                  }}
                >
                  {reasonText}
                </span>
              )}
              <span
                style={{
                  fontSize: "16px",
                  fontWeight: 600,
                  fontFamily: "var(--font-mono, monospace)",
                  color: chipColor,
                }}
              >
                {chipScore !== null ? chipScore : "—"}
              </span>
              <span
                style={{
                  fontSize: "9px",
                  color: "var(--text-muted)",
                  marginTop: "2px",
                  textAlign: "center",
                }}
              >
                {CHIP_LABELS[name]}
              </span>
            </div>
          );
        })}
      </div>

      {/* Reasons list — shown only when there are reasons */}
      {reasons.length > 0 && (
        <div
          style={{
            width: "100%",
            display: "flex",
            flexDirection: "column",
            gap: "4px",
          }}
        >
          {visibleReasons.map((reason, i) => (
            <div
              key={i}
              style={{
                fontSize: "11px",
                color: "var(--text-secondary)",
                lineHeight: 1.4,
              }}
            >
              {reason}
            </div>
          ))}
          {reasons.length > MAX_VISIBLE && (
            <button
              type="button"
              onClick={() => {
                showAllReasons.value = !showAllReasons.value;
              }}
              style={{
                alignSelf: "flex-start",
                background: "none",
                border: "none",
                padding: "0",
                cursor: "pointer",
                fontSize: "11px",
                color: "var(--text-muted)",
                textDecoration: "underline",
                marginTop: "2px",
              }}
            >
              {showAllReasons.value ? "Show less" : `Show ${hiddenCount} more`}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
