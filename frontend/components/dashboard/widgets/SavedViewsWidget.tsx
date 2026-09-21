import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The readable/empty distinction, the kind-to-page routing and the
// unreadable-row accounting live in lib/, under test (D-10, KTD8).
import type { SavedViewRow } from "@/lib/dashboard/platform.ts";
import {
  PREFERENCES_START_HREF,
  savedViewsView,
} from "@/lib/dashboard/platform.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many views the card lists. The count beside them covers every view the
 * route returned. */
const ROW_LIMIT = 6;

/**
 * The table views this account has saved, as a launcher.
 *
 * A launcher rather than a manager: creating, renaming and deleting a view all
 * happen in the dropdown above the table the view belongs to, where the state
 * being captured actually lives. What an overview card can add is the one
 * thing that surface cannot -- seeing all of them at once, from anywhere.
 *
 * Views from EVERY cluster are listed, unlike the pins beside them. A saved
 * view describes a scope -- a kind, a namespace, a filter -- which another
 * cluster could in principle satisfy, where a pin names one object in one
 * cluster. That distinction is `pinsForActiveCluster`'s in
 * src/lib/pin-store.ts and it is honoured rather than re-decided.
 *
 * Preferences are not CRD-discovered, so there is no family to declare. What
 * can be missing is the deployment's database, which `/v1/preferences/views`
 * reports as a 503 -- mapped onto the unavailable state by `ABSENT_STATUSES`,
 * so a deployment without PostgreSQL is told saved views are not kept rather
 * than shown an empty shelf that invites saving one.
 */
function SavedViews() {
  const view = savedViewsView(
    dashboardData.state("preference-views").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Saved Views"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="saved-views-summary"
            class="text-xs text-text-muted"
          >
            {view.total} saved
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        <p
          data-testid="saved-views-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The preferences endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // The empty state has to say where a view comes from: there is no
        // "saved views" page to send someone to, because a view is captured
        // from the table it describes.
        <div
          data-testid="saved-views-none"
          class="flex flex-col items-center gap-1.5 py-4 text-center"
        >
          <p class="text-xs text-text-muted">You have not saved a view yet.</p>
          <a
            href={PREFERENCES_START_HREF}
            class="text-xs text-accent no-underline"
          >
            Open a resource list and save one →
          </a>
        </div>
      ) : (
        <>
          <ul class="flex flex-col gap-1.5" data-testid="saved-views-list">
            {view.rows.map((row) => (
              <SavedViewItem key={row.id} row={row} />
            ))}
          </ul>

          {view.total > view.rows.length && (
            <p
              data-testid="saved-views-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.rows.length} of {view.total}.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="saved-views-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} record
          {view.unreadableRows === 1 ? "" : "s"} carried no name this card could
          render and {view.unreadableRows === 1 ? "was" : "were"} left out.
        </p>
      )}
    </WidgetShell>
  );
}

/**
 * One saved view, linking to the list page of the kind it scopes.
 *
 * The link opens that page rather than applying the view: applying is the
 * table's own affordance and needs the view's id in the table's state, which
 * an anchor cannot carry today. Opening the right page is the useful half and
 * it never lies about what it does.
 *
 * A view naming a kind this build routes no page for renders as text. The row
 * still says what the view scopes, which is more than a link that 404s.
 */
function SavedViewItem({ row }: { row: SavedViewRow }) {
  const label = (
    <>
      <span
        class="min-w-0 truncate text-xs text-text-secondary"
        title={row.name}
      >
        {row.name}
      </span>
      <span class="shrink-0 font-mono text-[10px] text-text-muted">
        {row.kind === "" ? "unknown kind" : row.kind}
        {row.namespace === "" ? "" : ` · ${row.namespace}`}
      </span>
    </>
  );

  return (
    <li class="flex items-center justify-between gap-2">
      {row.href === null ? (
        label
      ) : (
        <a
          href={row.href}
          class="flex min-w-0 flex-1 items-center justify-between gap-2 no-underline"
        >
          {label}
        </a>
      )}
    </li>
  );
}

registerWidget({
  id: "saved-views",
  title: "Saved Views",
  family: "platform",
  scopes: ["overview"],
  sources: ["preference-views"],
  // No `familyStatus` and no `adminOnly`: preferences are per-user and open to
  // every authenticated account, and their absence is the deployment's missing
  // database rather than a missing operator -- see `ABSENT_STATUSES` in
  // lib/dashboard/types.ts.
  //
  // Pinned on five sides (KTD7). Three columns is enough here where the
  // data-protection cards need four: a row is a user-chosen name beside a
  // resource kind, and both are short by construction -- the name is capped at
  // MAX_RECORD_NAME_LEN and the kind is an adapter slug.
  minW: 3,
  minH: 3,
  defaultW: 3,
  defaultH: 5,
  modes: ["normal"],
  render: () => <SavedViews />,
});
