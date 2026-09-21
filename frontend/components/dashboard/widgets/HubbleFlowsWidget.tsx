import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The verdict tally, the blocked share and the worst-first ranking live in
// lib/, under test (D-10, KTD8). The rounding in particular: one dropped flow
// in five hundred is 0.2%, which plain rounding prints as 0% -- a card
// claiming nothing is being dropped while something is.
import type { FlowRow } from "@/lib/dashboard/networking.ts";
import {
  HUBBLE_FLOW_BATCH,
  HUBBLE_FLOWS_PAGE_HREF,
  hubbleFlowsView,
} from "@/lib/dashboard/networking.ts";
import { PARAM_KEY_NAMESPACE, sourceKeyFor } from "@/lib/dashboard/params.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";

/** How many flows the card lists. The verdict counts above them cover the
 * whole batch, not just these. */
const ROW_LIMIT = 5;

/**
 * What Cilium is dropping in one namespace.
 *
 * Reads the REST flow route, not the flow socket. A dashboard card holding a
 * WebSocket open per placement would turn a layout into one live gRPC stream
 * against Hubble Relay per card for as long as the tab is open, and a feed
 * that repaints continuously is not what an overview card is for. One bounded
 * batch per refresh instead, and the percentages say they are a share of that
 * batch rather than of the namespace's traffic.
 *
 * Parameterized, and not by choice: the route REQUIRES a `?namespace=` and
 * answers 400 without one. The namespace is stored under the key the read path
 * re-authorizes, so an operator who loses access to it stops seeing the card
 * (R5) -- which matters more here than on most cards, since flow visibility is
 * pod-level observability of everything talking to everything.
 *
 * Absence and quiet are opposite readings of the same response: the route
 * answers 200 with an empty list for a namespace nothing is talking to. The
 * declared `hubble-status` family is what tells that apart from a cluster that
 * does not run Hubble at all (R1, KTD1).
 */
function HubbleFlows({ params }: WidgetProps) {
  const namespace = params[PARAM_KEY_NAMESPACE] ?? "";
  const view = hubbleFlowsView(
    dashboardData.state(sourceKeyFor("hubble-flows", params)).data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Network Flows"
      action={
        <span
          data-testid="hubble-flows-namespace"
          title={namespace}
          class="max-w-[10rem] truncate rounded-md border border-glass-border px-1.5 py-0.5 text-[11px] text-text-muted"
        >
          {namespace}
        </span>
      }
    >
      {!view.readable ? (
        // Hubble answered with something this build cannot read. An empty
        // verdict tally here would read as a namespace with nothing blocked.
        <p
          data-testid="hubble-flows-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The flow endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // Hubble is installed -- the family status said so -- and saw nothing
        // in this namespace. Distinct from both "no Hubble" and "nothing
        // blocked", and the card says which.
        <p
          data-testid="hubble-flows-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Hubble is watching this namespace and saw no traffic in the last
          batch.
        </p>
      ) : (
        <>
          <p
            class="mb-3 text-[11px] leading-snug text-text-muted"
            data-testid="hubble-flows-caption"
          >
            {view.blocked === 0
              ? `None of the last ${view.total} flows was blocked.`
              : `${view.blocked} of the last ${view.total} flows did not get through (${view.blockedPercent}%).`}
          </p>

          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="hubble-flows-counts"
          >
            <SeverityCount
              label="dropped"
              count={view.counts.dropped}
              color="var(--error)"
            />
            <SeverityCount
              label="errored"
              count={view.counts.error}
              color="var(--warning)"
            />
            {/* Counted apart from dropped rather than with it: an audit
                verdict is a policy reporting what it WOULD have dropped while
                the flow went through, and folding it in would send an
                operator looking for an outage that is a dry run. */}
            <SeverityCount
              label="audited"
              count={view.counts.audit}
              color="var(--accent-secondary)"
            />
            <SeverityCount
              label="forwarded"
              count={view.counts.forwarded}
              color="var(--success)"
            />
            {/* A verdict this build does not recognise is neither forwarded
                nor blocked, and is counted where it cannot be mistaken for
                either. */}
            <SeverityCount
              label="unrecognised"
              count={view.counts.unknown}
              color="var(--text-muted)"
            />
          </div>

          {view.rows.length === 0 ? (
            <p
              data-testid="hubble-flows-unnamed"
              class="py-2 text-center text-xs text-text-muted"
            >
              No flow in this batch named both of its endpoints.
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="hubble-flows-list">
              {view.rows.map((row, i) => (
                <FlowItem
                  key={`${row.time}-${row.source}-${row.destination}-${i}`}
                  row={row}
                />
              ))}
            </ul>
          )}

          {view.blocked > view.shown && (
            <p
              data-testid="hubble-flows-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.shown} of {view.blocked} blocked flows.
            </p>
          )}

          {view.total >= HUBBLE_FLOW_BATCH && (
            // The batch filled, so the share above is the share of a sample
            // that hit its ceiling rather than of everything Hubble saw.
            <p
              data-testid="hubble-flows-capped"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              This is the most recent {HUBBLE_FLOW_BATCH} flows, not the whole
              window.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="hubble-flows-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} flow
          {view.unreadableRows === 1 ? "" : "s"} named no endpoint this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left out
          of the list.
        </p>
      )}

      <a
        href={HUBBLE_FLOWS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View flows →
      </a>
    </WidgetShell>
  );
}

/**
 * One flow.
 *
 * The drop reason is Hubble's own enum name, printed rather than translated:
 * `POLICY_DENIED` is what the flows page shows and what an operator will type
 * into a search, and inventing friendlier prose here would make the same flow
 * read differently on two pages.
 */
function FlowItem({ row }: { row: FlowRow }) {
  const blocked = row.verdict === "dropped" || row.verdict === "error";
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          title={`${row.source} → ${row.destination}`}
          class="min-w-0 truncate text-xs text-text-secondary"
        >
          {row.source} → {row.destination}
        </span>
        <span
          class="shrink-0 text-[10px] font-medium uppercase"
          style={{
            color: blocked
              ? row.verdict === "dropped"
                ? "var(--error)"
                : "var(--warning)"
              : "var(--text-muted)",
          }}
        >
          {row.verdict}
        </span>
      </span>
      <span class="flex items-center gap-1.5 truncate text-[10px] text-text-muted">
        {row.protocol === "" ? "traffic" : row.protocol}
        {row.port === null ? "" : `:${row.port}`}
        {row.dropReason === "" ? "" : ` · ${row.dropReason}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "hubble-flows",
  title: "Network Flows",
  family: "networking",
  scopes: ["overview"],
  // The REST route. `/ws/flows` exists and is deliberately not read here --
  // see the source's comment in lib/dashboard/types.ts.
  sources: ["hubble-flows"],
  // Hubble is a Cilium feature rather than a CRD, and the flow route cannot
  // answer whether it is present: it answers 200 with an empty list for a
  // quiet namespace and 503 only when the backend never built a client at
  // all. `hubble-status` reads the CNI detector's own flag instead, which is
  // the only signal that separates "nothing was blocked" from "nothing is
  // watching" (R1, KTD1).
  familyStatus: "hubble-status",
  // Mandatory rather than a scoping choice: the handler answers 400 without
  // `?namespace=`. The key is spelled exactly as the server's
  // `paramKeyNamespace`, which is what re-authorizes a stored value on every
  // read (R5).
  params: { [PARAM_KEY_NAMESPACE]: [] },
  // Pinned on five sides (KTD7). Four columns: a row carries two endpoint
  // names separated by an arrow, and either one can be `prod/checkout-abc123`.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: (props) => <HubbleFlows {...props} />,
});
