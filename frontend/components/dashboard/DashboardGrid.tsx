import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { CellFillContext } from "@/components/ui/cell-fill.ts";
import type { Cell } from "@/lib/dashboard/grid.ts";
import {
  cellFromPoint,
  compact,
  dragTarget,
  layoutHeight,
  metricsFrom,
  moveItem,
  resizeItemBy,
  resizeItemToCell,
  resolveRenderable,
  stepItem,
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
 * mobile app was held to that bar in M5 PR-5h. So the pointer target is 24 and
 * the grip is painted in its bottom-right 16.
 *
 * 24 here and 40 for the drag handle also have to fit one above the other
 * without touching, which 2.5.8 requires of adjacent targets. That holds as
 * long as no widget declares `minH` below 2: a 2-row card is 96px, leaving the
 * two 32px apart, while a 1-row card is 40px and would put the grip inside the
 * title row. `registry_test.ts` enforces that floor, since the types cannot.
 */
const RESIZE_HANDLE_SIZE = 24;
const RESIZE_GRIP_SIZE = 16;

/**
 * The remove control's pointer target, held to the same WCAG 2.2 AA Target
 * Size (Minimum) bar as the resize grip above.
 *
 * It sits inside the 40px title row rather than beside it, so the two targets
 * in that row -- this one and the drag handle underneath it -- overlap rather
 * than adjoin. 2.5.8 measures spacing between targets that do not overlap, and
 * an enclosed target is the documented exception; the drag handle is the
 * larger surface and this is the smaller one cut out of it.
 */
const REMOVE_HANDLE_SIZE = 24;

/**
 * The focus ring for a grid item. Utilities rather than an inline style,
 * because `:focus-visible` has no inline form -- and an arrangeable widget with
 * no visible focus indicator is a keyboard path nobody can follow.
 */
const ITEM_FOCUS_RING =
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand";

/**
 * The remove control's surface, ring and hover wash.
 *
 * Utilities rather than an inline style, for the same reason `ITEM_FOCUS_RING`
 * is one -- neither `:focus-visible` nor `:hover` has an inline form -- and
 * the same glass pill the edit toolbar's own buttons wear, because this is
 * chrome sitting on a data surface. It carries that surface at rest rather
 * than only on hover: the title row it covers already holds a title, and four
 * widgets put a "View all" link exactly where this sits, so a bare glyph over
 * them would read as part of the card rather than as a control on top of it.
 */
const REMOVE_BUTTON_CLASS =
  "cursor-pointer rounded-md border border-glass-border bg-glass-surface hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand";

/** What a pointer session is doing. Only one runs at a time. */
type SessionKind = "drag" | "resize";

/**
 * A placed widget's accessible name: what it is, and where it sits.
 *
 * Position belongs in the name because on this surface it is the only thing
 * the user is changing. It is also announced through the grid's live region
 * after a key press, since a name that changes under an already-focused
 * element is not reliably re-read.
 */
function describePlacement(item: LayoutItem, def: WidgetDef): string {
  return (
    `${def.title}, column ${item.x + 1} of ${DASHBOARD_COLUMNS}, ` +
    `row ${item.y + 1}, ${item.w} wide, ${item.h} tall`
  );
}

interface GridItemProps {
  item: LayoutItem;
  def: WidgetDef;
  /** One-column mode: keep the height, discard x and w. */
  narrow: boolean;
  /**
   * Whether this item can be arranged: the handles are rendered, the item is a
   * tab stop, and it takes the arrow keys. Narrower than the grid's own
   * `editable`: one column has nothing to arrange, so the grid gates this on
   * being wide as well.
   */
  arrangeable: boolean;
  /**
   * Whether this item can be taken off the layout: the remove button is
   * rendered.
   *
   * Deliberately NOT folded into `arrangeable`. Arranging is a wide-grid
   * surface -- one column has no columns to move between and no width to size
   * -- but removing a widget is neither of those things, and gating it on the
   * same flag left edit mode reachable below the narrow breakpoint with
   * nothing in it to do. A user on a split laptop screen or a phone could open
   * the editor, see no way to take a widget off, and Save an unchanged layout.
   */
  removable: boolean;
  /** True while this item is the one being dragged. */
  dragging: boolean;
  /** True while this item is the one being resized. */
  resizing: boolean;
  onDragStart: (event: JSX.TargetedPointerEvent<HTMLElement>) => void;
  onResizeStart: (event: JSX.TargetedPointerEvent<HTMLElement>) => void;
  onKeyDown: (event: JSX.TargetedKeyboardEvent<HTMLElement>) => void;
  /** Takes this widget off the working copy. */
  onRemove: () => void;
}

/**
 * One positioned cell and, while editing, the ways to move and size it: two
 * pointer handles, and the item itself as a keyboard target.
 *
 * It provides CellFillContext, so the widget's card fills the cell and scrolls
 * its body instead of sizing to its content.
 */
export function GridItem({
  item,
  def,
  narrow,
  arrangeable,
  removable,
  dragging,
  resizing,
  onDragStart,
  onResizeStart,
  onKeyDown,
  onRemove,
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
      // The item is the widget's tab stop for arranging it while editing. The
      // two pointer handles below deliberately are not (see the note on the
      // drag handle), and the widget's own content is inert -- so a Tab
      // through an editable grid stops on the item and then on its remove
      // button, rather than on every link inside every card. The remove
      // button is a stop because it is the one control here with no keyboard
      // equivalent on the item itself; see its own note.
      //
      // No tabindex at all outside edit mode, rather than -1: a -1 element is
      // still focused by a click, which would ring a widget nobody can move.
      tabIndex={arrangeable ? 0 : undefined}
      // Arrow keys mean "move this widget" here, not "read the next line", so
      // the item asks assistive technology for the raw keys instead of letting
      // browse mode consume them. Only while editing -- the rest of the time
      // the widget's content is meant to be browsed, and role="application"
      // would take that away.
      role={arrangeable ? "application" : undefined}
      aria-roledescription={arrangeable ? "dashboard widget" : undefined}
      aria-label={arrangeable ? describePlacement(item, def) : undefined}
      onKeyDown={arrangeable ? onKeyDown : undefined}
      class={ITEM_FOCUS_RING}
      style={{
        ...style,
        minWidth: 0,
        minHeight: 0,
        position: "relative",
        // The widget under the pointer passes over the ones it displaces.
        zIndex: dragging || resizing ? 2 : undefined,
      }}
    >
      {arrangeable && (
        // The handle is the card's title row, not the whole card: several
        // widgets have links in their body, and a card-wide drag target would
        // swallow those clicks.
        //
        // It does cover the title row's own action slot, so the four widgets
        // with a header link ("View all", the utilization legend) cannot be
        // clicked while editing. That is the intended trade (decided
        // 2026-09-17): edit mode is for arranging, the whole row is one
        // predictable grab target, and leaving edit mode restores the links.
        //
        // A plain element, not a button. D8 made it one so that D10 could give
        // it keys; D10 gave the keys to the item instead, because one tab stop
        // that announces its geometry and moves is worth more than three that
        // do nothing on Enter -- and a button that ignores Enter and Space is
        // a promise to assistive technology this could not keep. What is left
        // is a pointer affordance, which has no role and no name to expose.
        <div
          data-testid="drag-handle"
          onPointerDown={onDragStart}
          style={{
            position: "absolute",
            insetInline: 0,
            top: 0,
            height: `${DRAG_HANDLE_HEIGHT}px`,
            zIndex: 1,
            background: "transparent",
            cursor: dragging ? "grabbing" : "grab",
            // Pointer events only; the browser's own touch scrolling would
            // otherwise cancel the drag on the first vertical movement.
            touchAction: "none",
          }}
        />
      )}
      {removable && (
        // The title row's right end, painted over the drag handle rather than
        // beside it -- the handle spans the whole row, so there is no "beside".
        // zIndex 2 puts it above the handle, which is what makes a press land
        // here instead of starting a drag; the two guards below are the
        // belt to that braces, and are what `PinnedResources.tsx:167-186`
        // does for the same reason (its row is an anchor).
        //
        // A real tab stop, unlike the drag handle and the resize grip. Those
        // two have keyboard equivalents on the item itself -- the arrows move,
        // Shift and an arrow sizes -- so they can be pointer affordances with
        // no name and no role. Removal has no such equivalent, and a visible
        // control that only a pointer can reach is a control a keyboard user
        // does not have. So editing a widget is two tab stops: the item, then
        // its remove button.
        //
        // In one-column mode it is the only one. The item withholds `tabIndex`
        // there because there is nothing to arrange, which leaves this button
        // as the whole keyboard surface of a narrow edit session -- and the
        // reason `removable` is a separate flag rather than `arrangeable`.
        // Nothing sits under it there either, because the wrapper below is
        // inert for the whole session and not just the arrangeable part of it
        // -- without that, the header link of the four widgets that have one
        // would still be live in one column, under a button painted on top of
        // it at the same end of the same row.
        //
        // No confirmation. Removal is one Cancel away from being undone and
        // writes nothing until Save, and a modal between the user and every
        // widget they want gone would make arranging a dashboard tedious --
        // which is the thing that actually loses layouts.
        <button
          type="button"
          data-testid="remove-widget"
          aria-label={`Remove ${def.title}`}
          title={`Remove ${def.title}`}
          onPointerDown={(e) => {
            // Stops a press on this button from being read as a grab. The
            // stacking above already keeps the event off the handle; this is
            // what keeps that true if the two ever stop overlapping.
            e.stopPropagation();
          }}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            onRemove();
          }}
          class={REMOVE_BUTTON_CLASS}
          style={{
            position: "absolute",
            right: "4px",
            top: `${(DRAG_HANDLE_HEIGHT - REMOVE_HANDLE_SIZE) / 2}px`,
            width: `${REMOVE_HANDLE_SIZE}px`,
            height: `${REMOVE_HANDLE_SIZE}px`,
            zIndex: 2,
            display: "grid",
            placeItems: "center",
            padding: 0,
            // 1.4.11 asks 3:1 of a control against what is behind it, and
            // --text-muted does not have it on these cards; --text-secondary
            // is the same token the resize grip settled on for the same test.
            color: "var(--text-secondary)",
            fontSize: "14px",
            lineHeight: 1,
          }}
        >
          {/* The glyph is decoration: the button's accessible name already
              says what it removes, and a multiplication sign read aloud
              beside that name is noise. */}
          <span aria-hidden="true">✕</span>
        </button>
      )}
      {arrangeable && (
        // The bottom-right corner, the one convention every resizable surface
        // shares. Drawn as two edges rather than a filled square so it reads
        // against whatever widget body it sits on, and kept small: it overlays
        // the card's own content, and the title row is the larger target.
        //
        // Pointer-only, like the drag handle: Shift and an arrow key on the
        // item is the keyboard's resize.
        <div
          data-testid="resize-handle"
          onPointerDown={onResizeStart}
          style={{
            position: "absolute",
            right: 0,
            bottom: 0,
            width: `${RESIZE_HANDLE_SIZE}px`,
            height: `${RESIZE_HANDLE_SIZE}px`,
            zIndex: 1,
            background: "transparent",
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
        </div>
      )}
      <div
        // Edit mode arranges widgets; it does not use them. Four of the ten
        // default widgets are a link *around* the whole card, so without this
        // a click anywhere below the title row navigates away from the layout
        // being edited, and a Tab through the grid stops on every one of them.
        // `inert` takes the subtree out of hit testing, the tab order and the
        // accessibility tree in one attribute, which leaves the item itself as
        // the widget's single tab stop. Leaving edit mode gives the links back.
        //
        // Keyed on the session rather than on `arrangeable`, so one column is
        // inert too. It was not before D17's remove control existed there,
        // because a narrow edit session had no affordance of its own and the
        // links were the only thing in the card worth reaching. Now the remove
        // button is painted over the same end of the same title row that four
        // of the ten default widgets put a header link in, and a live link
        // under a button is a click that navigates away from the layout being
        // edited.
        inert={removable}
        // The cell's height has to reach the card through this wrapper, or the
        // card's own fill has nothing to fill.
        style={{ height: "100%", minWidth: 0, minHeight: 0 }}
      >
        <CellFillContext.Provider value={true}>
          <WidgetHost def={def} params={item.params ?? {}} />
        </CellFillContext.Provider>
      </div>
    </div>
  );
}

/**
 * What one arrow key asks for, as a whole-cell delta. Shift turns the same
 * delta into a resize, which is why they are deltas rather than four handlers.
 */
const KEY_STEPS: Record<string, { dx: number; dy: number }> = {
  ArrowLeft: { dx: -1, dy: 0 },
  ArrowRight: { dx: 1, dy: 0 },
  ArrowUp: { dx: 0, dy: -1 },
  ArrowDown: { dx: 0, dy: 1 },
};

interface DashboardGridProps {
  initial: DashboardLayoutConfig;
  /**
   * Leaves edit mode. Escape on a focused widget is the keyboard's way out of
   * it, and the mode is the caller's state, so the grid asks rather than sets.
   * A caller should also put focus back where editing started: the item the
   * user was on stops being a tab stop the moment this returns.
   */
  onExitEdit?: () => void;
  /**
   * Edit mode. Handles exist only while this is true: a monitoring dashboard
   * gets clicked through fast, and always-live handles over every title row
   * and card corner would swallow those clicks.
   */
  editable?: boolean;
  /**
   * Reports the working copy whenever it changes, so the caller's edit session
   * can decide whether there is anything to save.
   *
   * The grid stays the owner of the arrangement -- this is a notification, not
   * a controlled value -- because the pointer sessions need a copy they can
   * reshape at pointer speed without a round trip through the caller's state.
   *
   * Fired for every committed change including a cancelled session's restore,
   * and NOT on mount: what the grid starts from is what the caller just handed
   * it, so announcing it would report a change nobody made.
   */
  onChange?: (items: LayoutItem[]) => void;
  /**
   * A widget is about to be taken off, at this reading-order position.
   *
   * Fired immediately before the layout write, so the caller can arm whatever
   * it uses to put the keyboard somewhere once the re-compacted grid has
   * rendered. The grid does not do that itself: the widget that went may have
   * been the last one, and where focus goes then is a toolbar the grid cannot
   * reach. `useDashboardFocus` owns both halves -- see `focusAfterRemoval`.
   */
  onRemoved?: (vacatedIndex: number) => void;
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
  onExitEdit,
  onChange,
  onRemoved,
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
  /** What the live region is saying. Keyboard changes only: a pointer user is
   * watching the thing they just moved. */
  const announcement = useSignal("");
  const gridRef = useRef<HTMLDivElement | null>(null);
  /** Ends the session in flight, if there is one. Set for the grid's life. */
  const endSession = useRef<((restore: boolean) => void) | null>(null);
  /**
   * The one way the working copy changes, so that nothing can reshape the
   * layout without the caller hearing about it.
   *
   * Every assignment goes through here -- the two pointer sessions, the
   * keyboard, and a cancelled session's restore -- because a Save button armed
   * by three of those four paths is worse than no Save button at all.
   */
  function setItems(next: LayoutItem[]) {
    items.value = next;
    onChange?.(next);
  }

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
      if (restore) setItems(before);
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

  /**
   * Takes a widget off the working copy.
   *
   * Compacted afterwards, like every other write path here: a layout with a
   * hole in it is not the canonical form of itself, and leaving one would mean
   * the arrangement the user sees is not the one `isDirty` compares or Save
   * writes -- the grid re-compacts on its next mount either way.
   *
   * Refused mid-session. A drag holds the pre-session layout for its own
   * Escape to restore, so removing underneath one would either resurrect the
   * widget on cancel or restore a layout that no longer describes the grid.
   * Unreachable by pointer -- a press cannot be in two places -- but a click
   * on one widget's remove button while a touch drag runs on another is not.
   */
  function removeItem(instanceId: string, def: WidgetDef) {
    if (session.value !== null) return;
    const before = items.value;
    const index = before.findIndex((i) => i.instanceId === instanceId);
    if (index === -1) return;
    // Before the layout write, and up to the caller rather than handled here:
    // the widget that just went may have been the last one, and an empty grid
    // has no cell to move to. Where focus goes then is a toolbar control this
    // component cannot reach, so both halves live with the caller's focus
    // hook -- which also gets the ordering for free, because a parent's layout
    // effects run after its children have patched.
    onRemoved?.(index);
    setItems(compact(before.filter((i) => i.instanceId !== instanceId)));
    // Said rather than left to the moved focus: the cell focus lands on
    // announces its own placement, which tells the user where they now are but
    // never that something went. The two read together as "removed X" then
    // "you are on Y".
    announcement.value = `Removed ${def.title}`;
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
        setItems(moveItem(items.value, instanceId, to.x, to.y));
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
        setItems(
          resizeItemToCell(items.value, instanceId, cell, {
            minW: def.minW,
            minH: def.minH,
          }),
        );
      };
    });
  }

  /**
   * The keyboard's whole vocabulary on a focused widget: an arrow moves it one
   * cell, Shift and an arrow sizes it by one, Escape leaves edit mode.
   *
   * It goes through the same two engines the pointer does, for the same reason
   * D-10 gives: the island translates an input into a request and never
   * positions anything itself. `stepItem` rather than `moveItem` because a
   * one-cell downward request is absorbed by design -- see its own note.
   */
  function handleItemKey(
    item: LayoutItem,
    def: WidgetDef,
    event: JSX.TargetedKeyboardEvent<HTMLElement>,
  ) {
    // Ctrl, Alt and Meta with an arrow belong to the browser and the window
    // manager -- back, forward, workspace switching. Shift is ours. Checked
    // before the session guard so a modified key is never swallowed there
    // either.
    if (event.ctrlKey || event.altKey || event.metaKey) return;

    // A pointer session owns the keyboard while it runs: its Escape is a
    // cancel, and an arrow would ask the engine for a second position while
    // the pointer is still asking for the first.
    //
    // Still consumed, not just ignored. An arrow that keeps its default
    // scrolls the box the grid sits in, and the session re-measures the grid
    // from its bounding rect on every pointermove -- so the widget lands in a
    // different cell with the pointer standing still. Escape is consumed for
    // the same reason the branch below does it: to keep the global shortcut
    // handler from blurring the widget that still has focus. Neither call
    // stops propagation, so the session's own window listener still sees the
    // Escape it cancels on.
    if (session.value !== null) {
      if (KEY_STEPS[event.key] || event.key === "Escape") {
        event.preventDefault();
      }
      return;
    }

    if (event.key === "Escape") {
      event.preventDefault();
      onExitEdit?.();
      return;
    }

    const step = KEY_STEPS[event.key];
    if (!step) return;
    // Without this the page scrolls out from under the widget being moved,
    // which is the one thing the user is watching.
    event.preventDefault();

    setItems(
      event.shiftKey
        ? resizeItemBy(items.value, item.instanceId, step.dx, step.dy, {
            minW: def.minW,
            minH: def.minH,
          })
        : stepItem(items.value, item.instanceId, step.dx, step.dy),
    );

    // Read back rather than predicted: the engine clamps, and a step that hit
    // the grid edge or a declared minimum must not be announced as one that
    // landed. An unchanged position produces the same string as last time,
    // which a live region correctly says nothing about.
    const moved = items.value.find((i) => i.instanceId === item.instanceId);
    announcement.value = moved ? describePlacement(moved, def) : "";
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
    <>
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
            // One column has no columns to move between and no width to size,
            // and its order is the layout's own reading order rather than
            // anything a user placed -- so there is nothing to arrange there
            // by pointer or by key. Editing is a wide-grid surface; P3's
            // stored layout is what a narrow screen renders.
            arrangeable={editable && !narrow.value}
            // Not narrowed the way `arrangeable` is. Removing a widget is not
            // a spatial gesture, so the reason one column cannot be arranged
            // is not a reason it cannot be edited -- and gating both on the
            // same flag made narrow edit mode a session with nothing in it.
            removable={editable}
            dragging={
              active?.kind === "drag" && active.instanceId === item.instanceId
            }
            resizing={
              active?.kind === "resize" && active.instanceId === item.instanceId
            }
            onDragStart={(e) => startDrag(item.instanceId, e)}
            onResizeStart={(e) => startResize(item.instanceId, def, e)}
            onKeyDown={(e) => handleItemKey(item, def, e)}
            onRemove={() => removeItem(item.instanceId, def)}
          />
        ))}
      </div>
      {/* A widget's own name carries its position too, but a name that changes
          under an already-focused element is not reliably re-announced; this
          is.

          Hidden by clipping a one-pixel box, NOT by Tailwind's `sr-only`.
          That utility positions absolutely, and the page's scroll container
          (`main`) is not itself positioned -- so the region's containing block
          would be the initial one, parking it at the grid's bottom in document
          coordinates and extending the *document's* scrollable area by the
          grid's full height. On the default layout that is 539px of empty
          scroll below a dashboard that should not scroll at all, and a pointer
          drag near the viewport edge auto-scrolls the page into it. Staying in
          flow costs one pixel and nothing else. */}
      <div
        data-testid="grid-announcement"
        role="status"
        aria-live="polite"
        style={{
          width: "1px",
          height: "1px",
          overflow: "hidden",
          clipPath: "inset(50%)",
          whiteSpace: "nowrap",
        }}
      >
        {announcement.value}
      </div>
    </>
  );
}
