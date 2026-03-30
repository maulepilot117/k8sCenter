import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";

export function useSplitPane(defaultRatio = 0.5) {
  const ratio = useSignal(defaultRatio);
  const dragging = useSignal(false);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const onMouseMove = (e: MouseEvent) => {
      if (!dragging.value || !containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      const x = e.clientX - rect.left;
      // Wider range on large screens (15-85%) vs default (25-75%)
      const isWide = rect.width >= 1400;
      const min = isWide ? 0.15 : 0.25;
      const max = isWide ? 0.85 : 0.75;
      const newRatio = Math.min(max, Math.max(min, x / rect.width));
      ratio.value = newRatio;
    };

    const onMouseUp = () => {
      if (!dragging.value) return;
      dragging.value = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);

    return () => {
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
  }, []);

  const startDrag = () => {
    dragging.value = true;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  };

  return { ratio, containerRef, startDrag, dragging };
}
