import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { CellFillContext } from "@/components/ui/cell-fill.ts";
import {
  cellFromPoint,
  dragTarget,
  layoutHeight,
  metricsFrom,
  moveItem,
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

/** Height of the drag handle: the widget card's title row. */
const DRAG_HANDLE_HEIGHT = 40;

interface GridItemProps {
  item: LayoutItem;
  def: WidgetDef;
  /** One-column mode: keep the height, discard x and w. */
  narrow: boolean;
  /** Whether this item can be picked up. D9 adds the resize handle. */
  draggable: boolean;
  /** True while this item is the one being dragged. */
  dragging: boolean;
  onDragStart: (
    instanceId: string,
    event: JSX.TargetedPointerEvent<HTMLElement>,
  ) => void;
}

/**
 * One positioned cell. D9 adds the resize handle here.
 *
 * It provides CellFillContext, so the widget's card fills the cell and scrolls
 * its body instead of sizing to its content.
 */
export function GridItem({
  item,
  def,
  narrow,
  draggable,
  dragging,
  onDragStart,
}: GridItemProps) {
  const style: JSX.CSSProperties = narrow
    ? { gridColumn: "1 / -1", gridRow: `span ${item.h}` }
    : {
        gridColumn: `${item.x + 1} / span ${item.w}`,
        gridRow: `${item.y + 1} / span ${item.h}`,
      };

  return (
    <div
      data-testid="grid-item"
      data-instance-id={item.instanceId}
      data-dragging={dragging ? "true" : undefined}
      style={{
        ...style,
        minWidth: 0,
        minHeight: 0,
        position: "relative",
        // The dragged widget passes over the ones it displaces.
        zIndex: dragging ? 2 : undefined,
      }}
    >
      {draggable && (
        // The handle is the card's title row, not the whole card: several
        // widgets have links in their body, and a card-wide drag target would
        // swallow those clicks. A button, not a bare div, so the handle is
        // focusable -- D10 gives it arrow keys.
        <button
          type="button"
          data-testid="drag-handle"
          aria-label={`Move ${def.title}`}
          onPointerDown={(e) => onDragStart(item.instanceId, e)}
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
      <CellFillContext.Provider value={true}>
        <WidgetHost def={def} params={item.params ?? {}} />
      </CellFillContext.Provider>
    </div>
  );
}

interface DashboardGridProps {
  initial: DashboardLayoutConfig;
  /**
   * Edit mode. Drag handles exist only while this is true: a monitoring
   * dashboard gets clicked through fast, and an always-live handle over every
   * title row would swallow those clicks.
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
  const items = useSignal<LayoutItem[]>(initial.items);
  const narrow = useSignal(false);
  const dragging = useSignal<string | null>(null);
  const gridRef = useRef<HTMLDivElement | null>(null);

  // Layout effect, so the first paint already uses the right mode rather than
  // flashing twelve squeezed columns on a narrow screen.
  useLayoutEffect(() => {
    if (!IS_BROWSER) return;
    const el = gridRef.current;
    if (!el) return;
    const measure = () => {
      narrow.value = el.clientWidth < NARROW_GRID_WIDTH;
    };
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    measure();
    return () => ro.disconnect();
  }, []);

  /**
   * Runs one drag from pointerdown to release.
   *
   * The session lives in this closure rather than in signals: nothing renders
   * from the grab origin, and a mid-drag re-render must not be able to lose it.
   * Every position comes back through `moveItem`, so the engine's rules -- the
   * dragged item wins, others are displaced downward, then gravity -- decide
   * the layout, and the island never positions anything itself.
   */
  function startDrag(
    instanceId: string,
    event: JSX.TargetedPointerEvent<HTMLElement>,
  ) {
    const el = gridRef.current;
    const handle = event.currentTarget;
    const start = items.value.find((i) => i.instanceId === instanceId);
    // Not editable, one-column mode (where x and y carry no meaning), a
    // secondary button, or an item that is no longer there: not a drag.
    if (!editable || narrow.value || event.button !== 0 || !el || !start) {
      return;
    }

    // Suppress the browser's own text selection and image dragging, which
    // otherwise fight the pointer session.
    event.preventDefault();
    // Pointer capture is what keeps the drag alive when the cursor outruns
    // the handle; without it a fast drag drops the widget mid-flight.
    handle.setPointerCapture(event.pointerId);
    dragging.value = instanceId;

    const before = items.value;
    const origin = cellFromPoint(
      event.clientX,
      event.clientY,
      metricsFrom(el.getBoundingClientRect()),
    );

    const onMove = (ev: PointerEvent) => {
      const cell = cellFromPoint(
        ev.clientX,
        ev.clientY,
        metricsFrom(el.getBoundingClientRect()),
      );
      const to = dragTarget(start, origin, cell);
      items.value = moveItem(items.value, instanceId, to.x, to.y);
    };

    const end = (restore: boolean) => {
      if (restore) items.value = before;
      dragging.value = null;
      handle.releasePointerCapture(event.pointerId);
      handle.removeEventListener("pointermove", onMove);
      handle.removeEventListener("pointerup", onUp);
      handle.removeEventListener("pointercancel", onCancel);
      globalThis.removeEventListener("keydown", onKeyDown);
    };
    const onUp = () => end(false);
    // pointercancel is an interruption, not a drop: the OS took the pointer
    // (a system gesture, a touch turned into a scroll), so the layout the
    // user never released goes back to where it was.
    const onCancel = () => end(true);
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") end(true);
    };

    handle.addEventListener("pointermove", onMove);
    handle.addEventListener("pointerup", onUp);
    handle.addEventListener("pointercancel", onCancel);
    globalThis.addEventListener("keydown", onKeyDown);
  }

  // Unknown ids are skipped and their rows reclaimed, and what is left comes
  // back in reading order -- which is also the keyboard and screen-reader
  // order. The wide grid places items by coordinates, but DOM order still
  // decides tab order, so it renders in that order too.
  const ordered = resolveRenderable(items.value, getWidget);

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
          // One column has no columns to drag between, so edit mode offers no
          // handles there; D10's keyboard moves are the narrow-screen path.
          draggable={editable && !narrow.value}
          dragging={dragging.value === item.instanceId}
          onDragStart={startDrag}
        />
      ))}
    </div>
  );
}
