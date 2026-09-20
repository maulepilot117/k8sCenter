import { useSignal } from "@preact/signals";
import { useLayoutEffect, useMemo, useRef } from "preact/hooks";
// The disabled-reason decision lives in lib/, not here, because this repo has
// no component test harness (D-10): logic that needs a unit test has to sit
// somewhere a test can import it. Do not move it back into this file.
import { disabledReasonFor } from "@/lib/dashboard/catalog.ts";
import { widgetsForScope } from "@/lib/dashboard/registry.ts";
import type {
  DashboardScope,
  LayoutItem,
  WidgetDef,
  WidgetFamily,
} from "@/lib/dashboard/types.ts";
import { WIDGET_FAMILIES } from "@/lib/dashboard/types.ts";
import type { Searchable } from "@/lib/fuzzy-search.ts";
import { fuzzySearch } from "@/lib/fuzzy-search.ts";

/**
 * The catalog of widgets that can be added to the dashboard being edited.
 *
 * A modal dialog, not a menu: it takes the screen, it closes on Escape and on
 * the scrim, and focus goes into it and comes back out to the control that
 * opened it. `SavedViews.tsx` models the other shape -- `role="menu"` with no
 * Escape handling, no outside click and no focus management -- which is why
 * this copies `CommandPalette.tsx` instead: combobox input, listbox of
 * options, arrow keys moving an `aria-activedescendant` while focus stays in
 * the input.
 *
 * It renders inside DashboardV2 rather than being its own island. The list it
 * shows depends on the layout being edited, and the entry it returns has to
 * reach the open edit session, so a separate hydration root would have to
 * synchronise both across islands to render one dialog.
 */

/**
 * Elements the Tab trap treats as stops, chosen by what actually makes an
 * element focusable rather than by which tags this dialog happens to use
 * today (input and button). A tag list goes stale the moment a control that
 * is not one of those two tags joins the dialog -- a select, an anchor, a
 * textarea, anything carrying an explicit tabindex -- and a stop the trap
 * does not recognise falls into the untracked-focus backstop below, which
 * yanks focus to the Close button on every Tab instead of letting it join
 * the cycle. D17's Remove control lands in this same dialog family, so this
 * has to be right before that control exists, not fixed after it breaks.
 * `[tabindex="-1"]` is excluded because that is how this dialog itself opts
 * an element (the option rows) out of the Tab order without removing it from
 * the DOM, and `:disabled` is excluded because a disabled control is not a
 * tab stop -- treating it as one would make Tab appear to stick on it.
 */
const FOCUSABLE_SELECTOR = [
  "a[href]:not([tabindex='-1'])",
  "button:not([tabindex='-1']):not(:disabled)",
  "input:not([tabindex='-1']):not(:disabled)",
  "select:not([tabindex='-1']):not(:disabled)",
  "textarea:not([tabindex='-1']):not(:disabled)",
  "[tabindex]:not([tabindex='-1'])",
].join(", ");

/** The catalog is closed and small; every entry stays reachable. Passing the
 * catalog's own size defeats `fuzzySearch`'s command-palette caps, which are a
 * screenful rather than a relevance judgement. */
const NO_LIMIT = Number.MAX_SAFE_INTEGER;

/** Section headers. `WidgetFamily` is a kebab-case key; these are the words. */
const FAMILY_LABELS: Record<WidgetFamily, string> = {
  cluster: "Cluster",
  workloads: "Workloads",
  reliability: "Reliability",
  security: "Security",
  delivery: "Delivery",
  "data-protection": "Data protection",
  networking: "Networking",
  platform: "Platform",
};

/** One catalog row: the widget, plus the fields `fuzzySearch` ranks on. */
interface Entry extends Searchable {
  def: WidgetDef;
  disabledReason: string | null;
}

export interface WidgetPaletteProps {
  /** Which dashboard is being edited; only widgets declaring it are offered. */
  scope: DashboardScope;
  /** The layout as it stands, which decides what is already on it. */
  placed: readonly LayoutItem[];
  /**
   * The grid width of the layout being edited.
   *
   * Taken from the caller rather than from `DASHBOARD_COLUMNS`, because the
   * entry this dialog offers is placed by the same scan on the other side of
   * `onAdd` -- and that one uses the layout's own column count. A palette that
   * answered "there is room" against a different grid than the one the widget
   * lands on would be offering a row whose Add then does nothing.
   */
  columns: number;
  /** Adds the widget. The caller places it and closes this dialog. */
  onAdd: (def: WidgetDef) => void;
  onClose: () => void;
}

export default function WidgetPalette({
  scope,
  placed,
  columns,
  onAdd,
  onClose,
}: WidgetPaletteProps) {
  const query = useSignal("");
  /** Index into the flattened list below; -1 while nothing is selectable. */
  const selectedIndex = useSignal(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Depends only on the scope, the placed items and the column count -- never
  // on the query or the selection -- so this is the one list here worth
  // memoizing. Without it, every keystroke, arrow press and hover re-ran
  // `widgetsForScope` and a bounded grid scan per entry for no reason: none
  // of those interactions can change what is addable. The filtered and
  // grouped lists below are deliberately left unmemoized -- they genuinely
  // depend on the query, which changes on nearly every render anyway.
  const entries: Entry[] = useMemo(
    () =>
      widgetsForScope(scope).map((def) => ({
        id: `widget-option-${def.id}`,
        label: def.title,
        detail: FAMILY_LABELS[def.family],
        def,
        disabledReason: disabledReasonFor(def, placed, columns),
      })),
    [scope, placed, columns],
  );

  // What the query shows, grouped for display in the families' declared order,
  // and flattened again for the keyboard: the index the arrows move through has
  // to be the order the eye reads, not the order the scorer returned.
  const matches = fuzzySearch(entries, query.value, NO_LIMIT);
  const groups = WIDGET_FAMILIES.map((family) => ({
    family,
    items: matches.filter((e) => e.def.family === family),
  })).filter((g) => g.items.length > 0);
  const flat = groups.flatMap((g) => g.items);
  // Position in the flattened order, by entry, so a row does not rescan the
  // list to find out which index it is.
  const positions = new Map(flat.map((e, i) => [e, i]));
  const selected = flat[selectedIndex.value];

  /**
   * The first entry that can actually be added, or -1 when none can.
   *
   * The selection skips the entries that are already on the dashboard, here
   * and in `move` below, so Enter always does something. Landing on one of
   * them instead would make the palette open on a highlighted row that ignores
   * the key the row is highlighted for; the reason those entries carry is on
   * screen for everyone reading the list either way.
   *
   * -1 rather than 0 for a catalog with nothing left to add -- the shipped
   * default layout, which holds every widget -- because highlighting the first
   * row there would promise an Enter that does nothing.
   */
  function firstAddable(items: readonly Entry[]): number {
    return items.findIndex((e) => e.disabledReason === null);
  }

  // Focus moves into the dialog on open and back to the opener on close; the
  // caller owns the return leg, because the button it goes back to is its own.
  //
  // A layout effect, not an effect: the dialog is mounted from the click on
  // the button that opens it, and a passive effect runs after the browser has
  // finished that click -- late enough that the button takes focus back and
  // the dialog opens with the keyboard still outside it, Escape included.
  // Measured, not assumed; `CommandPalette.tsx` papers over the same ordering
  // with a `setTimeout(..., 10)`.
  useLayoutEffect(() => {
    inputRef.current?.focus();
  }, []);

  // The selection follows the query, and only the query: it opens on the first
  // addable entry and returns there whenever the list is refiltered, while the
  // arrows and the pointer move it freely within one list.
  //
  // Before paint, so a keystroke never shows the previous query's selection on
  // the new list for a frame. `flat` is this render's list, which is what makes
  // this the single place that decides where the selection starts -- the input
  // handler used to re-derive the filtered list itself to answer the same
  // question one render early.
  useLayoutEffect(() => {
    selectedIndex.value = firstAddable(flat);
  }, [query.value]);

  function choose(entry: Entry | undefined) {
    if (entry === undefined || entry.disabledReason !== null) return;
    onAdd(entry.def);
  }

  /** Moves the selection `delta` steps, over anything already placed, and
   * stays put rather than stopping on one when there is nothing further. */
  function move(delta: number) {
    for (
      let i = selectedIndex.value + delta;
      i >= 0 && i < flat.length;
      i += delta
    ) {
      if (flat[i].disabledReason === null) {
        selectedIndex.value = i;
        return;
      }
    }
  }

  function handleInputKeyDown(e: KeyboardEvent) {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        move(1);
        break;
      case "ArrowUp":
        e.preventDefault();
        move(-1);
        break;
      case "Enter":
        e.preventDefault();
        choose(selected);
        break;
      case "Escape":
        e.preventDefault();
        onClose();
        break;
    }
  }

  /**
   * Ctrl/Cmd+K, Escape from anywhere in the dialog, and Tab held inside it.
   *
   * A modal that lets Tab walk out into the page behind it is modal only to
   * the mouse. The options are not tab stops -- the arrow keys move through
   * them, which is what `aria-activedescendant` on the input describes -- so
   * today the ring is just the input and the close button. `FOCUSABLE_SELECTOR`
   * finds that ring by capability (what the browser would actually let a user
   * Tab onto) rather than by naming those two controls, so a future control in
   * this dialog joins the cycle automatically instead of falling into the
   * untracked-focus backstop below.
   *
   * This is also the one place that swallows Ctrl/Cmd+K. `CommandPalette.tsx`
   * binds that combination on `window` to open the global command palette,
   * and its handler calls `preventDefault()` but never `stopPropagation()` --
   * so with nothing stopping it here, pressing it while this dialog is open
   * stacks a second `aria-modal` dialog on top of this one, with two Tab
   * traps live at once and this dialog's own Escape no longer reachable.
   * Attached to the dialog container rather than duplicated on the input:
   * Preact's onKeyDown is a real DOM listener, so a keydown fired on the
   * input still bubbles up through this container before it would reach
   * `window`, and this branch runs first and stops it there. Every branch
   * below that already calls `preventDefault` for a key this dialog owns
   * gets `stopPropagation` alongside it for the same reason -- nothing this
   * dialog handles should be visible to a listener outside it.
   */
  function handleDialogKeyDown(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      onClose();
      return;
    }
    if (e.key !== "Tab") return;
    const stops = Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ??
        [],
    );
    if (stops.length === 0) return;
    const first = stops[0];
    const last = stops[stops.length - 1];
    const active = document.activeElement;
    if (!stops.some((stop) => stop === active)) {
      // Focus is inside the dialog but on an element the trap does not
      // recognise as a stop -- an option row focused by a pointer press is
      // the only way that happens today, guarded separately by the
      // mousedown handler below, but this is the backstop the comment above
      // warns about: neither wrap branch matches an activeElement outside
      // `stops`, so both would be skipped and the next Tab would walk out of
      // an `aria-modal="true"` dialog. Pull focus back onto the first stop
      // instead of trusting the browser to keep it inside.
      e.preventDefault();
      e.stopPropagation();
      first.focus();
      return;
    }
    if (e.shiftKey && active === first) {
      e.preventDefault();
      e.stopPropagation();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      e.stopPropagation();
      first.focus();
    }
  }

  return (
    // The scrim's click is the pointer-only way out; every keyboard path out
    // of this dialog is handled by the key handler on the panel below.
    <div
      class="glass-scrim fixed inset-0 z-50 flex items-start justify-center pt-[min(15vh,96px)] px-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="widget-palette-title"
        data-testid="widget-palette"
        class="glass-elevated flex max-h-[70vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl"
        onKeyDown={handleDialogKeyDown}
      >
        <div class="flex items-center justify-between gap-3 border-b border-border-subtle px-4 py-3">
          <h2
            id="widget-palette-title"
            class="text-sm font-semibold text-text-primary"
          >
            Add a widget
          </h2>
          <button
            type="button"
            data-testid="close-palette"
            onClick={onClose}
            class="rounded-lg px-2 py-1 text-xs font-medium text-text-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand"
          >
            Close
          </button>
        </div>

        <div class="border-b border-border-subtle px-4 py-2">
          <input
            ref={inputRef}
            type="text"
            data-testid="widget-search"
            placeholder="Search widgets..."
            value={query.value}
            role="combobox"
            aria-expanded="true"
            aria-haspopup="listbox"
            aria-controls="widget-palette-results"
            aria-activedescendant={selected?.id}
            aria-label="Search widgets"
            onInput={(e) => {
              // The selection follows from the query; the effect above owns it.
              query.value = (e.target as HTMLInputElement).value;
            }}
            onKeyDown={handleInputKeyDown}
            class="w-full bg-transparent py-1 text-sm text-text-primary outline-none placeholder:text-text-muted"
          />
        </div>

        <div
          id="widget-palette-results"
          role="listbox"
          aria-label="Widgets"
          class="flex-1 overflow-y-auto py-2"
        >
          {flat.length === 0 && (
            <p class="px-4 py-6 text-center text-xs text-text-muted">
              No widget matches that search.
            </p>
          )}
          {groups.map((group) => (
            <div
              key={group.family}
              role="group"
              aria-label={FAMILY_LABELS[group.family]}
            >
              <div
                // Presentational: the group's name reaches assistive
                // technology through the wrapper's aria-label, and repeating
                // it as a listbox child would announce a row that cannot be
                // selected.
                aria-hidden="true"
                class="px-4 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wider text-text-muted"
              >
                {FAMILY_LABELS[group.family]}
              </div>
              {group.items.map((entry) => {
                const index = positions.get(entry) ?? -1;
                const isSelected = index === selectedIndex.value;
                const blocked = entry.disabledReason !== null;
                return (
                  <button
                    key={entry.def.id}
                    id={entry.id}
                    type="button"
                    role="option"
                    // Not a tab stop: the arrows move the selection and the
                    // input keeps focus, which is what aria-activedescendant
                    // on it describes.
                    tabIndex={-1}
                    data-testid={`widget-option-${entry.def.id}`}
                    aria-selected={isSelected}
                    // aria-disabled rather than the attribute: a disabled
                    // button is skipped by some screen readers entirely, and
                    // the entry exists precisely to say why it cannot be
                    // added.
                    aria-disabled={blocked}
                    title={entry.disabledReason ?? undefined}
                    // A pointer press focuses whatever it lands on regardless
                    // of tabIndex -- tabIndex={-1} only keeps a row out of the
                    // Tab order, it does not stop a mousedown from focusing
                    // it directly. That would move focus off the input,
                    // which is where every keyboard handler below lives, and
                    // the Tab trap's own stops selector does not recognise an
                    // option row either, so the next Tab would walk straight
                    // out of the dialog. Preventing the default here is the
                    // standard combobox-with-activedescendant guard: the
                    // click still fires and `choose` still runs.
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => choose(entry)}
                    // The pointer moves the selection too, but only onto rows
                    // that can be added -- the same invariant the arrows keep,
                    // and the reason Enter can be relied on after a hover.
                    onMouseEnter={() => {
                      if (!blocked) selectedIndex.value = index;
                    }}
                    // The selected row carries a bar and brighter text as well
                    // as the tint: `--accent-dim` is a 12% wash and on the
                    // glass panel it is not, on its own, a state a user can
                    // see -- and this row is the one Enter acts on. Every row
                    // reserves the bar's width so the selection does not
                    // shuffle the list sideways.
                    class={`flex w-full items-center gap-3 border-l-2 px-4 py-2 text-left text-sm ${
                      isSelected
                        ? "cursor-pointer border-accent bg-accent-dim text-text-primary"
                        : blocked
                          ? "cursor-not-allowed border-transparent text-text-muted"
                          : "cursor-pointer border-transparent text-text-secondary"
                    }`}
                  >
                    <span class="flex-1 truncate">{entry.def.title}</span>
                    {blocked && (
                      <span class="shrink-0 rounded-md border border-glass-border px-1.5 py-0.5 text-[11px] text-text-muted">
                        {entry.disabledReason}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
