import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { CellFillContext } from "@/components/ui/cell-fill.ts";
import type { Cell } from "@/lib/dashboard/grid.ts";
import {
  cellFromPoint,
  dragTarget,
  layoutHeight,
  metricsFrom,
  moveItem,
  resizeItemToCell,
  resolveRenderable,
} from "@/lib/dashboard/grid.ts";
import { getWidget } from "@/lib/dashboard/registry.ts";
import type {
  DashboardLayoutConfig,
  LayoutItem,
  WidgetDef,
} from "@/lib/dashboard/types.ts";
import {
  DASHBOARD_COLUMNS,
  DASHBOARD_GRID_GAP,
  DASHBOARD_ROW_HEIGHT,
} from "@/lib/dashboard/types.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * Below this grid width the layout renders as one column. It is the grid's own
 * width, not the viewport's: the sidebar takes a large share of a narrow
 * viewport, and twelve columns in the ~440px left at a 768px window would be
 * about 22px each.
 *
 * 900, not 768: the narrowest widget in the default layout is a three-column
 * metric tile, and the Network I/O tile's value, unit and sparkline need about
 * 170px of content. Three columns only give that from a grid of about 885px.
 */
export const NARROW_GRID_WIDTH = 900;

/**
 * Once collapsed, the grid needs this much width to expand again.
 *
 * The gap is a scrollbar wide on purpose. Collapsing to one column makes the
 * page taller, which can add the scroll bar, which takes ~15px off the grid --
 * and a single threshold would then flip the mode straight back, and again,
 * and again. Mid-drag that is worse than cosmetic: a mode flip cancels the
 * drag the user is still holding.
 */
export const WIDE_GRID_WIDTH = 916;

/** Height of the drag handle: the widget card's title row. */
const DRAG_HANDLE_HEIGHT = 40;

/**
 * The resize handle's pointer target, and the corner it draws inside it.
 *
 * The drawn grip is 16px because anything larger reads as a widget of its own
 * in a card corner, but WCAG 2.2 AA Target Size (Minimum) wants 24px and the
 * mobile app was held to that bar in M5 PR-5h. So the button is 24 and the
 * grip is painted in its bottom-right 16.
 *
 * 24 here and 40 for the drag handle also have to fit one above the other
 * without touching, which 2.5.8 requires of adjacent targets. That holds as
 * long as no widget declares `minH` below 2: a 2-row card is 96px, leaving the
 * two 32px apart, while a 1-row card is 40px and would put the grip inside the
 * title row. `registry_test.ts` enforces that floor, since the types cannot.
 */
const RESIZE_HANDLE_SIZE = 24;
const RESIZE_GRIP_SIZE = 16;

/** What a pointer session is doing. Only one runs at a time. */
type SessionKind = "drag" | "resize";

interface GridItemProps {
  item: LayoutItem;
  def: WidgetDef;
  /** One-column mode: keep the height, discard x and w. */
  narrow: boolean;
  /**
   * Whether this item offers its drag and resize handles. Narrower than the
   * grid's own `editable`: one column has nothing to arrange, so the grid
   * gates this on being wide as well.
   */
  handlesVisible: boolean;
  /** True while this item is the one being dragged. */
  dragging: boolean;
  /** True while this item is the one being resized. */
  resizing: boolean;
  onDragStart: (event: JSX.TargetedPointerEvent<HTMLElement>) => void;
  onResizeStart: (event: JSX.TargetedPointerEvent<HTMLElement>) => void;
}

/**
 * One positioned cell and, while editing, the two handles that move and size
 * it.
 *
 * It provides CellFillContext, so the widget's card fills the cell and scrolls
 * its body instead of sizing to its content.
 */
export function GridItem({
  item,
  def,
  narrow,
  handlesVisible,
  dragging,
  resizing,
  onDragStart,
  onResizeStart,
}: GridItemProps) {
  const style: JSX.CSSProperties = narrow
    ? { gridColumn: "1 / -1", gridRow: `span ${item.h}` }
    : {
        gridColumn: `${item.x + 1} / span ${item.w}`,
        gridRow: `${item.y + 1} / span ${item.h}`,
      };

  // A grip is a UI component, so WCAG 1.4.11 asks 3:1 of it against the card
  // behind it. --text-muted does not have it: 2.1:1 on a dark card, 1.9:1 on a
  // light one at a dimmed opacity, and still 2.96:1 against a light glass card
  // at full strength. Both of these clear 3:1 on all four card backgrounds.
  const gripColor = resizing ? "var(--accent)" : "var(--text-secondary)";

  return (
    <div
      data-testid="grid-item"
      data-instance-id={item.instanceId}
      data-dragging={dragging ? "true" : undefined}
      data-resizing={resizing ? "true" : undefined}
      style={{
        ...style,
        minWidth: 0,
        minHeight: 0,
        position: "relative",
        // The widget under the pointer passes over the ones it displaces.
        zIndex: dragging || resizing ? 2 : undefined,
      }}
    >
      {handlesVisible && (
        // The handle is the card's title row, not the whole card: several
        // widgets have links in their body, and a card-wide drag target would
        // swallow those clicks. A button, not a bare div, so the handle is
        // focusable -- D10 gives it arrow keys.
        //
        // It does cover the title row's own action slot, so the four widgets
        // with a header link ("View all", the utilization legend) cannot be
        // clicked while editing. That is the intended trade (decided
        // 2026-09-17): edit mode is for arranging, the whole row is one
        // predictable grab target, and leaving edit mode restores the links.
        <button
          type="button"
          data-testid="drag-handle"
          aria-label={`Move ${def.title}`}
          onPointerDown={onDragStart}
          style={{
            position: "absolute",
            insetInline: 0,
            top: 0,
            height: `${DRAG_HANDLE_HEIGHT}px`,
            zIndex: 1,
            padding: 0,
            border: "none",
            background: "transparent",
            cursor: dragging ? "grabbing" : "grab",
            // Pointer events only; the browser's own touch scrolling would
            // otherwise cancel the drag on the first vertical movement.
            touchAction: "none",
          }}
        />
      )}
      {handlesVisible && (
        // The bottom-right corner, the one convention every resizable surface
        // shares. Drawn as two edges rather than a filled square so it reads
        // against whatever widget body it sits on, and kept small: it overlays
        // the card's own content, and the title row is the larger target.
        <button
          type="button"
          data-testid="resize-handle"
          aria-label={`Resize ${def.title}`}
          onPointerDown={onResizeStart}
          style={{
            position: "absolute",
            right: 0,
            bottom: 0,
            width: `${RESIZE_HANDLE_SIZE}px`,
            height: `${RESIZE_HANDLE_SIZE}px`,
            zIndex: 1,
            padding: 0,
            background: "transparent",
            border: "none",
            display: "grid",
            placeItems: "end",
            cursor: "se-resize",
            // Pointer events only, for the same reason as the drag handle.
            touchAction: "none",
          }}
        >
          <span
            style={{
              width: `${RESIZE_GRIP_SIZE}px`,
              height: `${RESIZE_GRIP_SIZE}px`,
              borderRight: `2px solid ${gripColor}`,
              borderBottom: `2px solid ${gripColor}`,
              borderBottomRightRadius: "4px",
            }}
          />
        </button>
      )}
      <CellFillContext.Provider value={true}>
        <WidgetHost def={def} params={item.params ?? {}} />
      </CellFillContext.Provider>
    </div>
  );
}

interface DashboardGridProps {
  initial: DashboardLayoutConfig;
  /**
   * Edit mode. Handles exist only while this is true: a monitoring dashboard
   * gets clicked through fast, and always-live handles over every title row
   * and card corner would swallow those clicks.
   */
  editable?: boolean;
}

/**
 * Renders a layout on the snapping grid and, in P2, owns the in-memory working
 * copy of it.
 *
 * Persistence arrives in P3. Until then the layout resets on reload, which is
 * deliberate: interaction is the hard part, and proving it without a stored
 * contract to migrate is much cheaper than proving both at once.
 */
export default function DashboardGrid({
  initial,
  editable = false,
}: DashboardGridProps) {
  // Resolved once, on the way in, so the working copy is exactly what the grid
  // renders. The pointer sessions below read this signal while the DOM is laid
  // out from `resolveRenderable` of it, and the two have to share a coordinate
  // frame: that helper drops items whose widget id this build does not have and
  // re-compacts the survivors, so an unresolved copy would put every item below
  // a skipped one a row away from where the user is pointing. Normalizing here
  // rather than per render also means the drop happens once, which is the
  // display half of the unknown-id contract (telling the user belongs to the
  // editor). Today's only caller passes the default layout, where this is a
  // no-op; P3's stored layouts are what make it matter.
  const items = useSignal<LayoutItem[]>(
    resolveRenderable(initial.items, getWidget).map((r) => r.item),
  );
  const narrow = useSignal(false);
  const session = useSignal<{ kind: SessionKind; instanceId: string } | null>(
    null,
  );
  const gridRef = useRef<HTMLDivElement | null>(null);
  /** Ends the session in flight, if there is one. Set for the grid's life. */
  const endSession = useRef<((restore: boolean) => void) | null>(null);

  // Layout effect, so the first paint already uses the right mode rather than
  // flashing twelve squeezed columns on a narrow screen.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    const el = gridRef.current;
    if (!el) return;
    const measure = () => {
      // Which threshold applies depends on which mode is showing, so the
      // width has to move a scrollbar's worth to change the answer.
      narrow.value =
        el.clientWidth < (narrow.value ? WIDE_GRID_WIDTH : NARROW_GRID_WIDTH);
    };
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    measure();
    return () => ro.disconnect();
  }, []);

  // A session cannot outlive the grid either. Nothing unmounts DashboardGrid
  // today short of a navigation, which tears down the listeners anyway, but a
  // session that outlived its grid would go on reshaping a layout nobody
  // renders.
  useLayoutEffect(() => () => endSession.current?.(true), []);

  // A session cannot outlive the mode that offered the handle. Leaving edit
  // mode or collapsing to one column unmounts the handle under the pointer,
  // and a session nobody can finish would keep the widget lifted and hold the
  // pre-session layout for the next Escape to apply. The user did not drop it,
  // so the layout goes back.
  useLayoutEffect(() => {
    if (!editable || narrow.value) endSession.current?.(true);
  }, [editable, narrow.value]);

  /**
   * Runs one pointer session -- a drag or a resize -- from pointerdown to
   * release.
   *
   * Everything except what a pointermove *means* is shared between the two:
   * the guards, the capture, the window listeners, and the four ways a session
   * ends without a drop (Escape, pointercancel, a handle that disappeared, a
   * window blur). `begin` is called once with the item as it stood at
   * pointerdown and the cell the pointer started in, and returns the handler
   * that turns every later cell into a call on the geometry engine.
   *
   * The session lives in this closure rather than in signals: nothing renders
   * from the grab origin, and a mid-session re-render must not be able to lose
   * it. Every position and size comes back through `moveItem` or `resizeItem`,
   * so the engine's rules -- the item under the pointer wins, others are
   * displaced downward, then gravity -- decide the layout, and the island
   * never positions anything itself.
   */
  function startSession(
    kind: SessionKind,
    instanceId: string,
    event: JSX.TargetedPointerEvent<HTMLElement>,
    begin: (start: LayoutItem, origin: Cell) => (cell: Cell) => void,
  ) {
    const el = gridRef.current;
    const handle = event.currentTarget;
    const start = items.value.find((i) => i.instanceId === instanceId);
    // Not editable, one-column mode (where x and w carry no meaning), a
    // secondary button, an item that is no longer there, or a session already
    // in flight: not a session. Two concurrent ones -- two fingers on two
    // handles -- would each hold their own pre-session layout, and whichever
    // one was cancelled would restore over the other's work.
    if (
      !editable ||
      narrow.value ||
      event.button !== 0 ||
      !el ||
      !start ||
      session.value !== null
    ) {
      return;
    }

    // Suppress the browser's own text selection and image dragging, which
    // otherwise fight the pointer session.
    event.preventDefault();
    // Capture aims the events at the handle for as long as the browser keeps
    // it. It is an enhancement, not the session: the listeners below are on
    // the window, so a pointer the user agent will not let us capture -- or
    // takes back mid-session -- still works. Capturing an already-released
    // pointer throws, and losing the session over that would be worse than
    // losing the capture.
    try {
      handle.setPointerCapture(event.pointerId);
    } catch {
      // Nothing to do: the session runs uncaptured.
    }
    session.value = { kind, instanceId };

    const before = items.value;
    // Measured per call, not once: the page can scroll under a captured
    // pointer, which moves the grid's top without any resize.
    const cellAt = (ev: { clientX: number; clientY: number }) =>
      cellFromPoint(
        ev.clientX,
        ev.clientY,
        metricsFrom(el.getBoundingClientRect()),
      );

    const onMove = begin(start, cellAt(event));

    // One controller for the whole session: aborting it drops every listener
    // below, so there is no teardown list to keep in step with the setup.
    const controller = new AbortController();
    const end = (restore: boolean) => {
      // Abort first. Releasing a pointer the user agent has already
      // deactivated throws, and a throw after this point would leave the
      // session's listeners attached -- including the keydown one, which
      // holds the pre-session layout and would revert the dashboard on some
      // unrelated Escape minutes later.
      controller.abort();
      endSession.current = null;
      if (restore) items.value = before;
      session.value = null;
      if (handle.hasPointerCapture(event.pointerId)) {
        handle.releasePointerCapture(event.pointerId);
      }
    };
    endSession.current = end;
    const listen = { signal: controller.signal };
    /** Runs `fn` only for this session's pointer: another finger is not this
     * session. The window's listener map is untyped, so the narrowing lives
     * here rather than at four call sites. */
    const forThisPointer =
      (fn: (ev: PointerEvent) => void) =>
      (ev: Event): void => {
        const pointer = ev as PointerEvent;
        if (pointer.pointerId === event.pointerId) fn(pointer);
      };

    // The session belongs to the pointer, not to the handle: the handle is
    // rendered only while the grid is editable and wide, so a mid-session flip
    // of either -- Space on the still-focused "Edit layout" button, or a zoom
    // that crosses the breakpoint -- unmounts it. Listening on the window
    // means the session still ends when the element that started it is gone.
    globalThis.addEventListener(
      "pointermove",
      forThisPointer((ev) => onMove(cellAt(ev))),
      listen,
    );
    globalThis.addEventListener(
      "pointerup",
      forThisPointer(() => end(false)),
      listen,
    );
    // pointercancel is an interruption, not a drop: the OS took the pointer
    // (a system gesture, a touch turned into a scroll), so the layout the
    // user never released goes back to where it was.
    globalThis.addEventListener(
      "pointercancel",
      forThisPointer(() => end(true)),
      listen,
    );
    // Losing capture is only an interruption when the handle is gone. Chrome
    // releases capture whenever the captured node moves in the DOM, and the
    // first effective drag move does exactly that: the grid renders in
    // reading order, so the dragged widget changes places among its siblings.
    // Treating that as a cancel ended the drag a few milliseconds after it
    // started. The session does not need capture to survive -- it listens on
    // the window -- so capture is worth keeping only while it lasts.
    handle.addEventListener(
      "lostpointercapture",
      () => {
        if (!handle.isConnected) end(true);
      },
      listen,
    );
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") end(true);
    };
    globalThis.addEventListener("keydown", onKeyDown, listen);
    // Switching windows mid-session can take the pointer with neither a
    // pointerup nor a pointercancel: the button comes up over something else
    // entirely. Without this the widget stays lifted and follows the cursor
    // with no button held, and only Escape gets out of it.
    globalThis.addEventListener("blur", () => end(true), listen);
  }

  /** Moves a widget: the pointer's travel in cells applied to where it began. */
  function startDrag(
    instanceId: string,
    event: JSX.TargetedPointerEvent<HTMLElement>,
  ) {
    startSession("drag", instanceId, event, (start, origin) => {
      let last = origin;
      return (cell) => {
        // A cell is tens of pixels wide, so most moves land where the last one
        // did. Re-resolving the layout for those would re-render every widget
        // to produce the layout it already has.
        if (cell.x === last.x && cell.y === last.y) return;
        last = cell;
        const to = dragTarget(start, origin, cell);
        items.value = moveItem(items.value, instanceId, to.x, to.y);
      };
    });
  }

  /** Sizes a widget: the pointer names the cell its bottom-right corner covers. */
  function startResize(
    instanceId: string,
    def: WidgetDef,
    event: JSX.TargetedPointerEvent<HTMLElement>,
  ) {
    startSession("resize", instanceId, event, (_start, origin) => {
      let last = origin;
      return (cell) => {
        // Same reason the drag dedupes: a cell is tens of pixels wide, so most
        // moves ask for the layout that is already on screen.
        if (cell.x === last.x && cell.y === last.y) return;
        last = cell;
        // The whole size calculation -- measuring from the item's top-left,
        // the inclusive corner cell, the clamp, and re-aiming when gravity
        // lifts the item mid-resize -- belongs to the engine, which is where
        // it can be unit tested. The bounds come from the registry rather than
        // a constant here: a widget is the only thing that knows how small it
        // still renders.
        items.value = resizeItemToCell(items.value, instanceId, cell, {
          minW: def.minW,
          minH: def.minH,
        });
      };
    });
  }

  // Pairs each item with its definition and returns them in reading order --
  // which is also the keyboard and screen-reader order. The wide grid places
  // items by coordinates, but DOM order still decides tab order, so it renders
  // in that order too.
  //
  // The skip-and-recompact this also does has already happened, on the way into
  // `items` above, so here it is idempotent and the layout the sessions read is
  // the layout on screen.
  const ordered = resolveRenderable(items.value, getWidget);
  const active = session.value;

  const style: JSX.CSSProperties = narrow.value
    ? {
        display: "grid",
        gridTemplateColumns: "minmax(0, 1fr)",
        gridAutoRows: `${DASHBOARD_ROW_HEIGHT}px`,
        gap: `${DASHBOARD_GRID_GAP}px`,
      }
    : {
        display: "grid",
        gridTemplateColumns: `repeat(${DASHBOARD_COLUMNS}, minmax(0, 1fr))`,
        // An explicit row count keeps the grid tall enough for the lowest
        // widget. Implicit rows would make a drop below the last row clamp.
        gridTemplateRows: `repeat(${Math.max(1, layoutHeight(ordered.map((r) => r.item)))}, ${DASHBOARD_ROW_HEIGHT}px)`,
        gap: `${DASHBOARD_GRID_GAP}px`,
      };

  return (
    <div
      ref={gridRef}
      data-testid="dashboard-grid"
      data-grid-mode={narrow.value ? "narrow" : "wide"}
      data-grid-editable={editable ? "true" : "false"}
      style={style}
    >
      {ordered.map(({ item, def }) => (
        <GridItem
          key={item.instanceId}
          item={item}
          def={def}
          narrow={narrow.value}
          // One column has no columns to drag between and no width to size, so
          // edit mode offers no handles there; D10's keyboard is the
          // narrow-screen path.
          handlesVisible={editable && !narrow.value}
          dragging={
            active?.kind === "drag" && active.instanceId === item.instanceId
          }
          resizing={
            active?.kind === "resize" && active.instanceId === item.instanceId
          }
          onDragStart={(e) => startDrag(item.instanceId, e)}
          onResizeStart={(e) => startResize(item.instanceId, def, e)}
        />
      ))}
    </div>
  );
}
