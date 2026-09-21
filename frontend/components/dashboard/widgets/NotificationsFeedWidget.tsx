import {
  SeverityDot,
  SourceBadge,
} from "@/components/ui/NotifCenterBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The severity roll-up, the page-coverage arithmetic and the unreadable-row
// accounting live in lib/, under test (D-10, KTD8). Do not inline them.
import type { NotificationRow } from "@/lib/dashboard/platform.ts";
import {
  NOTIFICATIONS_PAGE_HREF,
  notificationsFeedView,
} from "@/lib/dashboard/platform.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import type { NotifSeverity, NotifSource } from "@/lib/notif-center-types.ts";
import { timeAgo } from "@/lib/timeAgo.ts";

/** How many notifications the card lists. The count beside them is the
 * server's count of the whole unread population, not this. */
const ROW_LIMIT = 5;

/**
 * What this account has not read yet.
 *
 * Unread rather than the whole feed: an overview card is worth the space only
 * for what is still owed attention, and the feed page is one click away for
 * the rest. The filter is the request's (`?read=unread`), so the count the
 * card shows is the server's count under the same filter -- see
 * `unread-notifications` in lib/dashboard/data.ts.
 *
 * The notification centre is not CRD-discovered and has no discovery route.
 * What can be missing is the deployment's database, and without one the routes
 * are never registered at all -- chi answers 404, which
 * `ABSENT_STATUSES` maps onto the unavailable state rather than onto an error
 * card offering a retry that cannot help.
 */
function NotificationsFeed({ mode }: WidgetProps) {
  const view = notificationsFeedView(
    dashboardData.state("unread-notifications").data,
    ROW_LIMIT,
  );

  // Compact is the badge alone. A card two rows tall cannot show a title and a
  // message legibly, and a truncated notification is worse than a number: the
  // number is true at any size.
  if (mode === "compact") {
    return (
      <WidgetShell title="Unread">
        <div
          data-testid="notifications-feed-compact"
          class="flex h-full flex-col items-center justify-center gap-1"
        >
          <span
            class="text-3xl font-semibold leading-none"
            style={{
              color:
                view.counts.critical > 0
                  ? "var(--error)"
                  : view.unread > 0
                    ? "var(--warning)"
                    : "var(--text-muted)",
            }}
          >
            {view.readable ? view.unread : "—"}
          </span>
          <span class="text-[11px] text-text-muted">
            {view.readable ? "unread" : "unreadable"}
          </span>
        </div>
      </WidgetShell>
    );
  }

  return (
    <WidgetShell
      title="Notifications"
      action={
        view.readable && view.unread > 0 ? (
          <span
            data-testid="notifications-feed-summary"
            class="text-xs"
            style={{
              color:
                view.counts.critical > 0 ? "var(--error)" : "var(--text-muted)",
            }}
          >
            {view.unread} unread
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The feed answered with something this build cannot read. An empty
        // list here would read as an account with nothing waiting.
        <p
          data-testid="notifications-feed-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The notifications endpoint returned a result this card cannot read.
        </p>
      ) : view.rows.length === 0 ? (
        <p
          data-testid="notifications-feed-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Nothing unread.
        </p>
      ) : (
        <>
          <ul class="flex flex-col gap-2" data-testid="notifications-feed-list">
            {view.rows.map((row) => (
              <NotificationItem key={row.id} row={row} />
            ))}
          </ul>

          {view.truncated && (
            <p
              data-testid="notifications-feed-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.rows.length} of {view.unread} unread.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="notifications-feed-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} carried no title this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={NOTIFICATIONS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all notifications →
      </a>
    </WidgetShell>
  );
}

/**
 * One notification's row. Not a link to the object it names: resolving a
 * notification to a destination is `notifActionUrl`'s job and it needs to know
 * whether the reader is an admin, which this card deliberately does not ask.
 * The feed page does both.
 *
 * `SeverityDot` and `SourceBadge` are the notification centre's own, reused
 * rather than restyled, so the dashboard and the feed page never colour the
 * same severity differently.
 */
function NotificationItem({ row }: { row: NotificationRow }) {
  return (
    <li class="flex items-start gap-2">
      <span class="mt-1 shrink-0">
        {/* The three known severities render their dot; anything else falls
            back to "info" so an unrecognised value is quiet rather than loud.
            The roll-up above has already counted it apart. */}
        <SeverityDot severity={severityOrInfo(row.severity)} />
      </span>
      <span class="min-w-0 flex-1">
        <span
          class="block truncate text-xs text-text-secondary"
          title={row.title}
        >
          {row.title}
        </span>
        <span class="mt-0.5 flex items-center gap-1.5 text-[10px] text-text-muted">
          {row.source !== "" && (
            <SourceBadge source={row.source as NotifSource} />
          )}
          {row.createdAt !== "" && <span>{timeAgo(row.createdAt)}</span>}
        </span>
      </span>
    </li>
  );
}

const KNOWN_SEVERITIES: readonly string[] = ["critical", "warning", "info"];

function severityOrInfo(value: string): NotifSeverity {
  return KNOWN_SEVERITIES.includes(value) ? (value as NotifSeverity) : "info";
}

registerWidget({
  id: "notifications-feed",
  title: "Notifications",
  family: "platform",
  scopes: ["overview"],
  sources: ["unread-notifications"],
  // No `familyStatus`: the notification centre is not CRD-discovered. Its
  // absence is the deployment's missing database, which leaves the routes
  // unregistered -- see `ABSENT_STATUSES` in lib/dashboard/types.ts.
  //
  // No `adminOnly` either: the feed routes are open to every authenticated
  // account; only the channel and rule management under them is admin-gated,
  // and this card reads neither.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 6,
  // Compact renders the unread count alone -- a real rendering rather than a
  // truncation of this one, which is what the mode is for. There is no third
  // thing to show at a larger size.
  modes: ["compact", "normal"],
  render: (props) => <NotificationsFeed {...props} />,
});
