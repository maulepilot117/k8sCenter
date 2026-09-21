import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The readiness reading, the parentRef resolution and the worst-first ranking
// live in lib/, under test (D-10, KTD8). The first of those especially: a
// Gateway the controller has not touched carries no Programmed condition at
// all, and a check that only looks for a False reads that as health.
import type { GatewayRow } from "@/lib/dashboard/networking.ts";
import {
  GATEWAY_API_PAGE_HREF,
  gatewayRoutesView,
} from "@/lib/dashboard/networking.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many gateways the card lists. The counts above them cover all of
 * them, not just these. */
const ROW_LIMIT = 5;

/**
 * The Gateway API inventory: what is declared, what is programmed, and what
 * is routed nowhere.
 *
 * Reads the Gateway API prefix (`/v1/gateway/...`), which is its own
 * top-level group rather than part of the networking one. Two reads, because
 * they answer two questions: a Gateway's own status carries the controller's
 * attached-route count across every route kind, and the HTTPRoute list is the
 * only way to see a route whose parent names a Gateway that is not there. A
 * route like that looks perfectly well-formed on its own page and its traffic
 * goes nowhere.
 *
 * Parameterless: both routes are cluster-wide and already RBAC-filtered, so
 * there is no scope for an operator to choose and nothing to re-authorize.
 *
 * Absence and emptiness are opposite readings of the same response -- every
 * Gateway API list route answers 200 with an empty array when the CRDs are
 * absent. The declared `gateway-status` family is what tells them apart,
 * resolved by the shell before `render` is called (R1, KTD1), which is what
 * lets an installed-but-unused Gateway API safely say so here.
 */
function GatewayRoutes() {
  const view = gatewayRoutesView(
    dashboardData.state("gateway-gateways").data,
    dashboardData.state("gateway-httproutes").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Gateway API"
      action={
        view.readable ? (
          <span
            data-testid="gateway-routes-total"
            class="text-xs font-semibold"
            style={{
              color:
                view.notReady > 0
                  ? "var(--error)"
                  : view.unattached > 0
                    ? "var(--warning)"
                    : "var(--success)",
            }}
          >
            {view.gatewayCount} gateway{view.gatewayCount === 1 ? "" : "s"}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // One or both routes answered with something this build cannot read.
        // An empty inventory here would read as a cluster with nothing
        // misrouted.
        <p
          data-testid="gateway-routes-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The Gateway API endpoints returned a result this card cannot read.
        </p>
      ) : view.gatewayCount === 0 ? (
        // Gateway API is installed -- the family status said so -- and there
        // are no Gateways, or none this account can see. Distinct from "no
        // Gateway API", which is the shell's unavailable state.
        <p
          data-testid="gateway-routes-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          Gateway API is installed but no Gateway you can see is declared yet.
        </p>
      ) : (
        <>
          <p
            class="mb-3 text-[11px] leading-snug text-text-muted"
            data-testid="gateway-routes-caption"
          >
            {view.routeCount} HTTP route{view.routeCount === 1 ? "" : "s"} you
            can see
            {view.unattached === 0
              ? ", all attached to a gateway."
              : `, ${view.unattached} of them attached to no gateway you can see.`}
          </p>

          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="gateway-routes-counts"
          >
            <SeverityCount
              label="not programmed"
              count={view.notReady}
              color="var(--error)"
            />
            <SeverityCount
              label="unattached routes"
              count={view.unattached}
              color="var(--warning)"
            />
            <SeverityCount
              label="programmed"
              count={view.gatewayCount - view.unreadableRows - view.notReady}
              color="var(--success)"
            />
          </div>

          {view.rows.length === 0 ? (
            <p
              data-testid="gateway-routes-unnamed"
              class="py-2 text-center text-xs text-text-muted"
            >
              No gateway in this inventory named itself.
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="gateway-routes-list">
              {view.rows.map((row) => (
                <GatewayItem key={`${row.namespace}/${row.name}`} row={row} />
              ))}
            </ul>
          )}

          {view.gatewayCount - view.unreadableRows > view.rows.length && (
            <p
              data-testid="gateway-routes-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.rows.length} of{" "}
              {view.gatewayCount - view.unreadableRows} gateways.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="gateway-routes-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no gateway this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={GATEWAY_API_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View Gateway API →
      </a>
    </WidgetShell>
  );
}

/**
 * One Gateway.
 *
 * The route count is the Gateway's OWN status total, which the controller
 * writes across every route kind -- so it can legitimately exceed the HTTP
 * route count in the caption above, on a cluster using gRPC or TCP routes.
 * The two are labelled differently for that reason rather than reconciled
 * into one number that would be wrong for somebody.
 *
 * An unknown readiness is marked apart from a failed one: a Gateway with no
 * conditions has not been reached by a controller, which is a different
 * problem from one a controller refused.
 */
function GatewayItem({ row }: { row: GatewayRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          title={`${row.namespace}/${row.name}`}
          class="min-w-0 truncate text-xs text-text-secondary"
        >
          {row.name}
        </span>
        <span
          class="shrink-0 text-[10px] font-medium"
          style={{
            color:
              row.ready === true
                ? "var(--success)"
                : row.ready === false
                  ? "var(--error)"
                  : "var(--text-muted)",
          }}
        >
          {row.ready === true
            ? "programmed"
            : row.ready === false
              ? "not programmed"
              : "no status yet"}
        </span>
      </span>
      <span class="flex items-center gap-1.5 truncate text-[10px] text-text-muted">
        {row.namespace}
        {row.className === "" ? "" : ` · ${row.className}`}
        {` · ${row.listeners} listener${row.listeners === 1 ? "" : "s"}`}
        {` · ${row.attachedRoutes} route${row.attachedRoutes === 1 ? "" : "s"}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "gateway-routes",
  title: "Gateway API",
  family: "networking",
  scopes: ["overview"],
  // Gateways and routes together, which is two reads rather than one: the
  // per-gateway count comes from each Gateway's own status and the unattached
  // count can only come from the route list. See the two source comments in
  // lib/dashboard/types.ts.
  sources: ["gateway-gateways", "gateway-httproutes"],
  // Gateway API is CRD-discovered, and every one of its list routes returns a
  // literal empty array when the CRDs are absent -- byte-identical to a
  // cluster that has Gateway API and no Gateways. Without this the card would
  // report the first as the second (R1, KTD1).
  familyStatus: "gateway-status",
  // Parameterless, and that is a consequence rather than a decision: both
  // routes are cluster-wide and already RBAC-filtered, so there is no scope to
  // collect and nothing for the server to re-authorize.
  //
  // Pinned on five sides (KTD7). Four columns: a row carries a gateway name
  // beside a readiness word and a namespace/class/listener/route line.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <GatewayRoutes />,
});
