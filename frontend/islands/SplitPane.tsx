import type { ComponentChildren } from "preact";
import { useSplitPane } from "@/lib/hooks/use-split-pane.ts";

interface SplitPaneProps {
  left: ComponentChildren;
  right: ComponentChildren;
  defaultRatio?: number;
}

export default function SplitPane(
  { left, right, defaultRatio }: SplitPaneProps,
) {
  const { ratio, containerRef, startDrag, dragging } = useSplitPane(
    defaultRatio,
  );

  return (
    <div
      ref={containerRef}
      style={{
        display: "flex",
        height: "100%",
        overflow: "hidden",
      }}
    >
      {/* Left pane */}
      <div
        style={{
          width: `${ratio.value * 100}%`,
          overflowY: "auto",
          padding: "20px",
          flexShrink: 0,
        }}
      >
        {left}
      </div>

      {/* Divider */}
      <div
        onMouseDown={(e) => {
          e.preventDefault();
          startDrag();
        }}
        style={{
          width: "1px",
          cursor: "col-resize",
          background: dragging.value
            ? "var(--accent)"
            : "var(--border-primary)",
          flexShrink: 0,
          transition: dragging.value ? "none" : "background 0.15s ease",
        }}
      />

      {/* Right pane */}
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "20px",
          background: "var(--bg-base)",
        }}
      >
        {right}
      </div>
    </div>
  );
}
