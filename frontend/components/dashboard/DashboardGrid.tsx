import { useSignal } from "@preact/signals";
import type { JSX } from "preact";
import { useLayoutEffect, useRef } from "preact/hooks";
import { CellFillContext } from "@/components/dashboard/cell-fill.ts";
import WidgetHost from "@/components/dashboard/WidgetHost.tsx";
import { byReadingOrder, layoutHeight } from "@/lib/dashboard/grid.ts";
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
 */
export const NARROW_GRID_WIDTH = 768;

interface GridItemProps {
  item: LayoutItem;
  def: WidgetDef;
  /** One-column mode: keep the height, discard x and w. */
  narrow: boolean;
}

/**
 * One positioned cell. D8 and D9 add the drag and resize handles here.
 *
 * It provides CellFillContext, so the widget's card fills the cell and scrolls
 * its body instead of sizing to its content.
 */
export function GridItem({ item, def, narrow }: GridItemProps) {
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
      style={{ ...style, minWidth: 0, minHeight: 0 }}
    >
      <CellFillContext.Provider value={true}>
        <WidgetHost def={def} params={item.params ?? {}} />
      </CellFillContext.Provider>
    </div>
  );
}

interface DashboardGridProps {
  initial: DashboardLayoutConfig;
}

/**
 * Renders a layout on the snapping grid and, in P2, owns the in-memory working
 * copy of it.
 *
 * Persistence arrives in P3. Until then the layout resets on reload, which is
 * deliberate: interaction is the hard part, and proving it without a stored
 * contract to migrate is much cheaper than proving both at once.
 */
export default function DashboardGrid({ initial }: DashboardGridProps) {
  const items = useSignal<LayoutItem[]>(initial.items);
  const narrow = useSignal(false);
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

  // One column follows reading order, which is also the keyboard and
  // screen-reader order. The wide grid places items by coordinates, but DOM
  // order still matters for tab order, so it is reading order there too.
  const ordered = [...items.value].sort(byReadingOrder);

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
        gridTemplateRows: `repeat(${Math.max(1, layoutHeight(items.value))}, ${DASHBOARD_ROW_HEIGHT}px)`,
        gap: `${DASHBOARD_GRID_GAP}px`,
      };

  return (
    <div
      ref={gridRef}
      data-testid="dashboard-grid"
      data-grid-mode={narrow.value ? "narrow" : "wide"}
      style={style}
    >
      {ordered.map((item) => {
        const def = getWidget(item.id);
        // An unknown id is skipped rather than rendered (spec D-7). Telling the
        // user a widget was dropped is P4's job, on the editor surface.
        if (!def) return null;
        return (
          <GridItem
            key={item.instanceId}
            item={item}
            def={def}
            narrow={narrow.value}
          />
        );
      })}
    </div>
  );
}
