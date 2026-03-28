import type { ComponentChildren } from "preact";
import { useEffect } from "preact/hooks";
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { SparklineChart } from "@/components/ui/SparklineChart.tsx";

interface MetricCardProps {
  value: number | string;
  label: string;
  status: "success" | "warning" | "error" | "info";
  statusText: string;
  sparklineData?: number[];
  sparklineColor?: string;
  href?: string;
  icon?: ComponentChildren;
}

const STATUS_STYLES: Record<
  MetricCardProps["status"],
  { iconBg: string; iconColor: string; pillBg: string; pillColor: string }
> = {
  success: {
    iconBg: "var(--success-dim)",
    iconColor: "var(--success)",
    pillBg: "var(--success-dim)",
    pillColor: "var(--success)",
  },
  warning: {
    iconBg: "var(--warning-dim)",
    iconColor: "var(--warning)",
    pillBg: "var(--warning-dim)",
    pillColor: "var(--warning)",
  },
  error: {
    iconBg: "var(--error-dim)",
    iconColor: "var(--error)",
    pillBg: "var(--error-dim)",
    pillColor: "var(--error)",
  },
  info: {
    iconBg: "var(--accent-dim)",
    iconColor: "var(--accent)",
    pillBg: "var(--accent-dim)",
    pillColor: "var(--accent)",
  },
};

function MetricCardInner(
  { value, label, status, statusText, sparklineData, sparklineColor, icon }:
    MetricCardProps,
) {
  const styles = STATUS_STYLES[status];

  // Animated value display
  const animatedValue = useSignal(0);
  const prevValue = useSignal(0);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const target = typeof value === "number"
      ? value
      : parseInt(String(value), 10);
    if (isNaN(target)) return;

    const start = prevValue.value;
    const startTime = performance.now();
    const duration = 800;

    function easeOutExpo(t: number): number {
      return t === 1 ? 1 : 1 - Math.pow(2, -10 * t);
    }

    let frame: number;
    function tick(now: number) {
      const elapsed = now - startTime;
      const progress = Math.min(elapsed / duration, 1);
      animatedValue.value = Math.round(
        start + (target - start) * easeOutExpo(progress),
      );
      if (progress < 1) {
        frame = requestAnimationFrame(tick);
      } else {
        prevValue.value = target;
      }
    }
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [value]);

  return (
    <div
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "var(--radius)",
        padding: "16px",
        transition: "border-color 0.2s ease",
        overflow: "hidden",
        position: "relative",
        display: "flex",
        flexDirection: "column",
        height: "100%",
      }}
    >
      {/* Header: icon square + status pill */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "8px",
        }}
      >
        {/* 32x32 icon square */}
        <div
          style={{
            width: "32px",
            height: "32px",
            borderRadius: "var(--radius-sm)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: styles.iconBg,
            color: styles.iconColor,
          }}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
          >
            {icon ?? <circle cx="8" cy="8" r="5" />}
          </svg>
        </div>
        {/* Status pill */}
        <span
          style={{
            fontSize: "10px",
            fontWeight: 500,
            textTransform: "uppercase",
            letterSpacing: "0.05em",
            padding: "3px 8px",
            borderRadius: "10px",
            background: styles.pillBg,
            color: styles.pillColor,
          }}
        >
          {statusText}
        </span>
      </div>

      {/* Value */}
      <div
        style={{
          fontSize: "28px",
          fontFamily: "var(--font-mono, monospace)",
          fontWeight: 700,
          color: "var(--text-primary)",
          lineHeight: 1.1,
          letterSpacing: "-0.02em",
        }}
      >
        {animatedValue.value}
      </div>

      {/* Label */}
      <div
        style={{
          fontSize: "12px",
          color: "var(--text-muted)",
          marginTop: "2px",
        }}
      >
        {label}
      </div>

      {/* Sparkline — pushed to bottom of card */}
      {sparklineData && sparklineData.length >= 2 && (
        <div style={{ marginTop: "auto", paddingTop: "12px" }}>
          <SparklineChart
            data={sparklineData}
            color={sparklineColor ?? "var(--accent)"}
            width={120}
            height={32}
          />
        </div>
      )}
    </div>
  );
}

export default function MetricCard(props: MetricCardProps) {
  if (props.href) {
    return (
      <a
        href={props.href}
        style={{
          textDecoration: "none",
          color: "inherit",
          display: "block",
          height: "100%",
        }}
      >
        <MetricCardInner {...props} />
      </a>
    );
  }
  return <MetricCardInner {...props} />;
}
