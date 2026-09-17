import type { JSX } from "preact";
import { createContext } from "preact";
import { useContext } from "preact/hooks";

/**
 * True inside a dashboard grid cell, where the cell -- not the content -- sets
 * the widget's height.
 *
 * Cards are content-height everywhere else in the app, and WidgetShell is used
 * on dozens of pages, so filling is opt-in through context rather than a
 * global style change: only a grid cell provides `true`. Inside one, the card
 * stretches to the cell and scrolls its own content when that is taller than
 * the space the layout gave it.
 */
export const CellFillContext = createContext(false);

/**
 * Height for an element that wraps a widget card from outside it, such as a
 * tile's link. In a grid cell the wrapper must pass the cell's height through,
 * or the card's own fill has nothing to fill; elsewhere it adds nothing.
 */
export function useCellFillHeight(): JSX.CSSProperties {
  return useContext(CellFillContext) ? { height: "100%" } : {};
}
