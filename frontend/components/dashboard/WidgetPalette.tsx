import { useSignal } from "@preact/signals";
import { useLayoutEffect, useMemo, useRef } from "preact/hooks";
import ModalDialogShell from "@/components/dashboard/ModalDialogShell.tsx";
// The disabled-reason decision lives in lib/, not here, because this repo has
// no component test harness (D-10): logic that needs a unit test has to sit
// somewhere a test can import it. Do not move it back into this file.
import type { FamilyStatuses } from "@/lib/dashboard/catalog.ts";
import {
  disabledReasonFor,
  selectionAfterEntriesChange,
} from "@/lib/dashboard/catalog.ts";
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
 * A row is offered or refused by `disabledReasonFor`, which now answers with
 * two reasons that are not about the layout at all: a widget whose family is
 * not installed on this cluster, and one this account may not read. They reuse
 * the affordance the other three already have -- the badge, the blocked Add,
 * the skipped keyboard stop -- so the palette gained no new mechanism, only
 * new reasons (R3).
 *
 * Choosing a row does not always place a widget. A widget that declares
 * parameters needs values first, so `onAdd` for one of those opens the
 * parameter dialog instead and the placement happens on confirm -- which is
 * why such a row says so before it is chosen. Without the hint, pressing
 * Enter on it looks like an Add that produced a dialog for no stated reason;
 * with it, the dialog is the thing the row promised. The branch itself is the
 * caller's: this dialog neither places widgets nor knows what a session is.
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
  /**
   * The CRD-discovered families' discovery statuses, as the caller last read
   * them.
   *
   * Passed in rather than read here because this dialog owns no data layer --
   * and because the statuses it needs are the whole catalog's, not the placed
   * widgets'. The source cache fetches what placed widgets declare, so a
   * widget that has never been added would have no status at all to judge, and
   * the palette could not mark it before it is added (R3). The caller requests
   * all eight regardless of what is on the layout.
   */
  familyStatuses: FamilyStatuses;
  /**
   * Whether the session holds the admin role, or null before `/auth/me` has
   * answered.
   *
   * The two admin-gated widgets (`cluster-status`, `audit-activity`) sit
   * behind `middleware.RequireAdmin` rather than behind RBAC, so their
   * availability is a property of the session and not of the cluster -- there
   * is no discovery route to put in `familyStatuses` and nothing to fetch. The
   * caller passes what it already holds; null blocks nothing, exactly as an
   * unanswered family status does.
   */
  viewerIsAdmin: boolean | null;
  /** Adds the widget. The caller places it and closes this dialog. */
  onAdd: (def: WidgetDef) => void;
  onClose: () => void;
}

export default function WidgetPalette({
  scope,
  placed,
  columns,
  familyStatuses,
  viewerIsAdmin,
  onAdd,
  onClose,
}: WidgetPaletteProps) {
  const query = useSignal("");
  /** Index into the flattened list below; -1 while nothing is selectable. */
  const selectedIndex = useSignal(-1);
  const inputRef = useRef<HTMLInputElement>(null);

  // Depends only on the scope, the placed items, the column count and the
  // family statuses -- never on the query or the selection -- so this is the
  // one list here worth memoizing. Without it, every keystroke, arrow press
  // and hover re-ran `widgetsForScope` and a bounded grid scan per entry for
  // no reason: none of those interactions can change what is addable. The
  // filtered and grouped lists below are deliberately left unmemoized -- they
  // genuinely depend on the query, which changes on nearly every render
  // anyway.
  //
  // `familyStatuses` belongs in the dependency list for the same reason the
  // other three do: a discovery route answering mid-session changes which rows
  // are addable, and a row left stale here is one the user cannot add and is
  // not told why. The caller hands it over as a computed, so its identity
  // changes only when a status actually does.
  const entries: Entry[] = useMemo(
    () =>
      widgetsForScope(scope).map((def) => ({
        id: `widget-option-${def.id}`,
        label: def.title,
        detail: FAMILY_LABELS[def.family],
        def,
        disabledReason: disabledReasonFor(
          def,
          placed,
          columns,
          familyStatuses,
          viewerIsAdmin,
        ),
      })),
    [scope, placed, columns, familyStatuses, viewerIsAdmin],
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

  // The selection also has to follow a row that stops being addable underneath
  // it. `entries` recomputes when a discovery status or the admin signal
  // lands, both of which resolve on their own schedule while the dialog is
  // open, and `disabledReasonFor` can flip the selected row to blocked without
  // the query having changed -- so the effect above never re-runs.
  //
  // What the user saw was a row that still looked selected, because the row's
  // class checks `isSelected` before `blocked`, sitting next to its new
  // disabled-reason badge and silently swallowing both Enter and a click:
  // `choose` no-ops on a blocked entry, and both paths go through it. A dialog
  // that ignores input without saying why is worse than one that refuses out
  // loud, which is what moving the selection restores.
  useLayoutEffect(() => {
    selectedIndex.value = selectionAfterEntriesChange(
      flat.map((e) => e.disabledReason === null),
      selectedIndex.value,
    );
  }, [entries]);

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

  return (
    // Escape, the Tab trap and the Ctrl/Cmd+K swallow are the modal contract,
    // shared with the copy dialog and owned by the shell along with the
    // chrome around them.
    <ModalDialogShell
      titleId="widget-palette-title"
      title="Add a widget"
      testId="widget-palette"
      closeTestId="close-palette"
      onClose={onClose}
    >
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
                  //
                  // `blocked` is tested FIRST. The selection normally moves
                  // off a row that becomes unaddable, but for the frame
                  // before it does -- and for a pointer resting on a blocked
                  // row -- "selected" styling on something that refuses both
                  // Enter and a click tells the user the opposite of the
                  // truth. Unaddable outranks selected.
                  class={`flex w-full items-center gap-3 border-l-2 px-4 py-2 text-left text-sm ${
                    blocked
                      ? "cursor-not-allowed border-transparent text-text-muted"
                      : isSelected
                        ? "cursor-pointer border-accent bg-accent-dim text-text-primary"
                        : "cursor-pointer border-transparent text-text-secondary"
                  }`}
                >
                  <span class="flex-1 truncate">{entry.def.title}</span>
                  {/* Not a disabled reason: this row CAN be added, it just
                      asks a question first. So it is rendered on its own
                      rather than through `disabledReason`, which is read by
                      the keyboard as "skip this row". Hidden while the row is
                      blocked, because a reason and a hint competing for the
                      same end of the same row reads as two badges about the
                      same refusal. */}
                  {!blocked && entry.def.params !== undefined && (
                    <span
                      data-testid="widget-option-needs-values"
                      class="shrink-0 text-[11px] text-text-muted"
                    >
                      Asks for {Object.keys(entry.def.params).join(", ")}
                    </span>
                  )}
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
    </ModalDialogShell>
  );
}
