import { createContext } from "preact";

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
