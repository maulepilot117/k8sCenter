import { useSignal } from "@preact/signals";
import type { VNode } from "preact";
import { useContext, useLayoutEffect, useRef } from "preact/hooks";
import { CellFillContext } from "@/components/ui/cell-fill.ts";
import { Skeleton } from "@/components/ui/Skeleton.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { pickMode } from "@/lib/dashboard/display-mode.ts";
import type { WidgetDef } from "@/lib/dashboard/types.ts";
// Which of the five outcomes this box shows is a pure decision with unit
// tests, because this repo has no component test harness (D-10). Do not move
// it back inline.
import { resolveWidgetState } from "@/lib/dashboard/widget-state.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * The shape both cluster-fact states share: an outline glyph, a heading, and
 * one line naming the widget. Centred and quiet rather than filling the cell,
 * because a card reporting that there is nothing to show should not shout as
 * loudly as one carrying data. `icon` is the glyph's paths only; the <svg>
 * around them lives here so the two cannot drift in size or stroke.
 */
function StateCard({
  testId,
  heading,
  detail,
  icon,
}: {
  testId: string;
  heading: string;
  detail: string;
  icon: VNode;
}) {
  return (
    <div
      data-testid={testId}
      class="flex h-full flex-col items-center justify-center gap-1.5 p-4 text-center text-text-muted"
    >
      <svg
        width="20"
        height="20"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        {icon}
      </svg>
      <span class="text-xs font-medium">{heading}</span>
      <span class="text-[11px] leading-[1.4] opacity-80">{detail}</span>
    </div>
  );
}

interface WidgetHostProps {
  def: WidgetDef;
  params?: Record<string, string>;
}

/**
 * Measures a widget's box, chooses its display mode, and renders it -- or one
 * of the four states that stand in for it.
 *
 * The pre-registry dashboard had none of them: the skeleton was an
 * all-or-nothing page gate and every fetch failure was swallowed. A dashboard
 * of thirty-nine independent widgets cannot share one gate, so each widget
 * owns its own.
 *
 * Two of the four are about the cluster rather than the request. A feature
 * this cluster does not run, and a feature this account may not read, are
 * outcomes in their own right: a loading shimmer over the first would promise
 * something that is never coming, and an error card over the second would
 * offer a retry that cannot help. Both are resolved HERE, before `render` is
 * called, so a widget body never learns it is unavailable and never has to
 * decide whether its own empty list means "nothing to report" or "no operator
 * installed" -- which it cannot decide, because a CRD-discovered family
 * answers 200 with an empty array either way (KTD1).
 *
 * Deliberately thin. Everything here that could be wrong lives in pickMode and
 * resolveWidgetState, both pure functions with unit tests.
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

  // One rule, five outcomes -- resolveWidgetState owns the order and the
  // argument for it. In short: stale data outranks an error, so a transient
  // 500 leaves a working widget on screen with an inline notice rather than an
  // error page; a source the widget declared optional never gates it; a
  // refused read outranks any other failure, because the state it produces
  // carries no retry; and an absent feature is decided before `render`, never
  // by the widget itself.
  //
  // Idle (never requested -- the consumer calls `ensure`, not this component)
  // and in-flight both land on the skeleton, so they never need
  // distinguishing.
  //
  // The params go in as well, because a parameterized widget's sources are
  // cached under keys that carry them -- resolving against the bare source
  // name would read a key nothing ever fetched, and the card would sit in the
  // skeleton rather than failing visibly.
  const resolved = resolveWidgetState(
    def,
    (k) => dashboardData.state(k),
    params,
  );

  const mode = pickMode(def.modes, width.value, height.value);
  // In a grid cell the stale notice takes its natural height and the widget
  // takes the rest, so a card that fills its space still fits the cell.
  const fill = useContext(CellFillContext);

  return (
    <div
      ref={box}
      data-testid="widget-host"
      data-widget-id={def.id}
      data-widget-mode={mode}
      // Which of the branches below rendered. Lets a test tell a loaded widget
      // from a skeleton -- and a feature this cluster does not run from one
      // that is broken -- which the host element alone cannot.
      data-widget-state={resolved.state}
      style={{
        height: "100%",
        minWidth: 0,
        minHeight: 0,
        ...(fill ? { display: "flex", flexDirection: "column" } : {}),
      }}
    >
      {resolved.state === "ready" ? (
        <>
          {resolved.stale && (
            <div
              data-testid="widget-stale"
              title={resolved.stale.error ?? undefined}
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
          {fill ? (
            <div style={{ flex: "1 1 auto", minHeight: 0 }}>
              {def.render({ mode, params })}
            </div>
          ) : (
            def.render({ mode, params })
          )}
        </>
      ) : resolved.state === "unavailable" ? (
        // Quieter than the error card and stiller than the skeleton, because
        // nothing is wrong and nothing is coming: this cluster simply does not
        // run the feature. Muted text rather than --warning, and a distinct
        // heading and glyph, so the three non-rendering states are told apart
        // by something other than colour.
        <StateCard
          testId="widget-unavailable"
          heading="Not installed on this cluster"
          detail={`${def.title} reads a feature this cluster does not run.`}
          icon={
            // A crossed-out box: the thing is not here.
            <>
              <path d="M3 3l18 18" />
              <path d="M21 8v8a2 2 0 0 1-1 1.73l-7 4a2 2 0 0 1-2 0l-4.4-2.53" />
              <path d="M4 16.5A2 2 0 0 1 3 15V8a2 2 0 0 1 1-1.73l7-4a2 2 0 0 1 2 0l5.5 3.14" />
            </>
          }
        />
      ) : resolved.state === "permission" ? (
        // No retry affordance, deliberately: a 403 is a standing fact about
        // the account, and a "try again" on it is a lie. The raw message is
        // not shown either -- "Forbidden" tells the reader nothing the heading
        // has not already said.
        <StateCard
          testId="widget-permission"
          heading="You do not have access"
          detail={`Your account is not permitted to read ${def.title}.`}
          icon={
            // A closed padlock: the thing is there, and shut.
            <>
              <rect x="3" y="11" width="18" height="11" rx="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </>
          }
        />
      ) : resolved.state === "error" ? (
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
            {resolved.blocking?.error}
          </div>
        </div>
      ) : (
        // Sized to the widget box, which the grid cell gives a height: a bare
        // <Skeleton /> carries no height class and would render zero-height.
        <Skeleton class="h-full w-full rounded-lg" />
      )}
    </div>
  );
}
