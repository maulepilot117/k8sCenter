import { useSignal } from "@preact/signals";
import { useLayoutEffect, useRef } from "preact/hooks";
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

  // Layout, not plain, effect: useEffect runs after the browser paints, so the
  // widget would paint once at 0x0 (that is, "compact") and then flip to its
  // real mode. For a widget whose compact and normal renderings differ, that
  // is a visible flash on every mount.
  useLayoutEffect(() => {
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

  const optional = def.optionalSources ?? [];
  const states = def.sources.map((k) => dashboardData.state(k));
  const requiredStates = def.sources
    .filter((k) => !optional.includes(k))
    .map((k) => dashboardData.state(k));

  // One rule, four properties:
  //
  //   1. A widget is rendered once every source it DEPENDS on has data, so
  //      `render` never receives a null required source. Half the catalog
  //      declares two sources from separate endpoints with different
  //      latencies, so "the first one landed" is the common case, not an edge
  //      case, and a widget author writing `t.cpu.length` would get a crash
  //      rather than a type error.
  //   2. A source the widget declared optional does not gate it. Gating on
  //      every declared source made each one another way for the widget to
  //      disappear: a slow or failed trend request blanked a percentage the
  //      summary endpoint had already returned, where the pre-registry island
  //      rendered it with no sparkline. An optional source may be null at
  //      render, and the widget is written to tolerate that.
  //   3. A failure is shown as soon as the widget cannot render, even while a
  //      sibling source is still in flight. Gating on loading first would hide
  //      a known error behind the slower sibling's skeleton -- and `api()`
  //      applies no request timeout, so a stalled sibling makes that durable.
  //      Only a REQUIRED source's failure blocks; an optional one shows the
  //      inline notice beside a rendered widget.
  //   4. Stale data outranks an error. The cache keeps the last good value
  //      across a failed refresh, so a transient 500 leaves the widget
  //      rendered with an inline error instead of replacing it with an error
  //      page. That is the view an operator needs most while a cluster is
  //      degrading.
  //
  // Idle (never requested -- the consumer calls `ensure`, not this component)
  // and in-flight both land on the skeleton, so they never need distinguishing.
  const failure = states.find((s) => s.error !== null);
  const blockingFailure = requiredStates.find((s) => s.error !== null);
  const renderable = requiredStates.every((s) => s.data !== null);

  const mode = pickMode(def.modes, width.value, height.value);

  return (
    <div
      ref={box}
      data-testid="widget-host"
      data-widget-id={def.id}
      data-widget-mode={mode}
      style={{ height: "100%", minWidth: 0, minHeight: 0 }}
    >
      {renderable ? (
        <>
          {failure && (
            <div
              data-testid="widget-stale"
              title={failure.error ?? undefined}
              style={{
                padding: "4px 8px",
                fontSize: "11px",
                lineHeight: 1.4,
                color: "var(--warning)",
              }}
            >
              Showing last known data — refresh failed.
            </div>
          )}
          {def.render({ mode, params })}
        </>
      ) : blockingFailure ? (
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
          <div style={{ opacity: 0.75, marginTop: "4px" }}>
            {blockingFailure.error}
          </div>
        </div>
      ) : (
        // Sized to the widget box: a bare <Skeleton /> carries no height class
        // and would render an invisible zero-height div.
        <Skeleton class="h-full w-full rounded-lg" />
      )}
    </div>
  );
}
