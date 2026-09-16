/**
 * Size-to-mode selection: the entire size-responsive contract, in one pure
 * function.
 *
 * Breakpoints are pixel measurements of the rendered box, not grid units,
 * because the same grid span is a different physical size on a phone and a
 * 4K monitor -- and it is the physical size that decides whether a detail
 * table is legible.
 */
import type { DisplayMode } from "./types.ts";
import { DISPLAY_MODES } from "./types.ts";

/** Minimum rendered box, in CSS pixels, for each mode. Both dimensions must
 * qualify: a wide, short box cannot host a detail table. Inclusive on the way
 * up, so a widget sized exactly to a breakpoint does not flicker. */
const THRESHOLDS: Record<DisplayMode, { w: number; h: number }> = {
  compact: { w: 0, h: 0 },
  normal: { w: 280, h: 160 },
  expanded: { w: 520, h: 320 },
};

/**
 * Picks the largest mode that both fits the box and is implemented by the
 * widget. Falls back downward first (a widget with no `expanded` stays at
 * `normal` in a huge box), then upward (a widget that only implements
 * `normal` renders `normal` in a tiny box). Blank is never an answer.
 */
export function pickMode(
  modes: readonly DisplayMode[],
  width: number,
  height: number,
): DisplayMode {
  if (modes.length === 0) return "normal";

  const fits = (m: DisplayMode) =>
    width >= THRESHOLDS[m].w && height >= THRESHOLDS[m].h;

  // DISPLAY_MODES is ordered smallest to largest. The initialiser also covers
  // a negative box, where even compact's 0x0 threshold does not fit.
  let wanted: DisplayMode = "compact";
  for (const m of DISPLAY_MODES) {
    if (fits(m)) wanted = m;
  }

  const wantedIndex = DISPLAY_MODES.indexOf(wanted);
  for (let i = wantedIndex; i >= 0; i--) {
    const m = DISPLAY_MODES[i];
    if (modes.includes(m)) return m;
  }
  for (let i = wantedIndex + 1; i < DISPLAY_MODES.length; i++) {
    const m = DISPLAY_MODES[i];
    if (modes.includes(m)) return m;
  }
  return "normal";
}
