import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { pickMode } from "@/lib/dashboard/display-mode.ts";
import type { WidgetDef } from "@/lib/dashboard/types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

interface WidgetHostProps {
  def: WidgetDef;
  params?: Record<string, string>;
}

/**
 * Measures a widget's box, chooses its display mode, and renders it -- or the
 * loading or error state instead.
 *
 * The pre-registry dashboard had neither: the skeleton was an all-or-nothing
 * page gate and every fetch failure was swallowed. A dashboard of thirty-nine
 * independent widgets cannot share one gate, so each widget owns its own.
 *
 * Deliberately thin. Everything here that could be wrong lives in pickMode,
 * which is a pure function with unit tests, because this repo has no component
 * test harness.
 */
export default function WidgetHost({ def, params = {} }: WidgetHostProps) {
  const box = useRef<HTMLDivElement | null>(null);
  const width = useSignal(0);
  const height = useSignal(0);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const el = box.current;
    if (!el) return;

    // Debounced, mirroring PodTerminal.tsx -- the only prior art for
    // ResizeObserver in this codebase. The timer is declared before the
    // closure so the closure captures the binding, not a stale value, and is
    // typed as the platform's handle rather than a number.
    let resizeTimer: ReturnType<typeof setTimeout> | undefined;
    const measure = () => {
      width.value = el.clientWidth;
      height.value = el.clientHeight;
    };
    const ro = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = globalThis.setTimeout(measure, 100);
    });
    ro.observe(el);
    measure();

    return () => {
      clearTimeout(resizeTimer);
      ro.disconnect();
    };
  }, []);

  const states = def.sources.map((k) => dashboardData.state(k));
  // A widget is loading only while it has nothing to show. Once any source has
  // landed, a background refresh must not blank the widget out.
  const loading =
    states.some((s) => s.loading) && states.every((s) => s.data === null);
  const failure = states.find((s) => s.error !== null);

  const mode = pickMode(def.modes, width.value, height.value);

  return (
    <div
      ref={box}
      data-testid="widget-host"
      data-widget-id={def.id}
      data-widget-mode={mode}
      style={{ height: "100%", minWidth: 0, minHeight: 0 }}
    >
      {loading ? (
        // Sized to the widget box: a bare <Skeleton /> carries no height class
        // and would render an invisible zero-height div.
        <Skeleton class="h-full w-full rounded-lg" />
      ) : failure ? (
        <div
          data-testid="widget-error"
          style={{
            padding: "16px",
            fontSize: "12px",
            lineHeight: 1.5,
            color: "var(--warning)",
          }}
        >
          {def.title} could not be loaded.
          <div style={{ opacity: 0.75, marginTop: "4px" }}>{failure.error}</div>
        </div>
      ) : (
        def.render({ mode, params })
      )}
    </div>
  );
}
