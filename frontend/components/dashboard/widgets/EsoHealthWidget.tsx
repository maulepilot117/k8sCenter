import { StatusBadge } from "@/components/eso/ESOBadges.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The status ranking, the four-state drift reading and the unreadable-row
// accounting live in lib/, under test, because none of them is a field read
// (D-10, KTD8). Do not inline them.
import type { EsoRow } from "@/lib/dashboard/expiry.ts";
import {
  EXTERNAL_SECRETS_PAGE_HREF,
  esoHealthView,
  externalSecretHref,
} from "@/lib/dashboard/expiry.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { Status } from "@/lib/eso-types.ts";

/** How many ExternalSecrets the card lists. The counts above them cover every
 * one the endpoint returned, not just these. */
const ROW_LIMIT = 5;

/**
 * Whether the cluster's secrets are actually arriving from their providers.
 *
 * Both halves come off one read. `/v1/externalsecrets/externalsecrets` carries
 * the lifecycle status the server derived AND the poller's last-observed
 * drift inline on every item, so there is no second call to make and nothing
 * for the browser to re-derive -- which is just as well, since resolving drift
 * needs an impersonated read of the target Secret that the browser cannot
 * perform.
 *
 * Drift is four readings, not a boolean. `InSync` and `Drifted` are
 * observations; `Unknown` is ESO saying it could not determine drift for this
 * one -- no `syncedResourceVersion` from the provider, a deleted target
 * Secret, an account without `get secret` -- and an absent field is the poller
 * not having reached it yet. The wire contract states outright that absence
 * must be read as neither InSync nor Drifted, so the card counts the two
 * non-answers separately rather than rounding either toward "clean". See
 * `ESO_DRIFT_STATES` in lib/dashboard/expiry.ts.
 *
 * Absence and a healthy fleet are opposite readings of the same response. ESO
 * is CRD-discovered and the list route answers 200 with an empty array whether
 * the operator is absent or no ExternalSecret exists; the declared
 * `external-secrets-status` family is what tells them apart, resolved by the
 * shell before `render` is called (R1, KTD1).
 */
function EsoHealth() {
  const view = esoHealthView(
    dashboardData.state("external-secrets-list").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Secret Sync"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="eso-health-summary"
            class="text-xs"
            style={{
              color:
                view.failing > 0
                  ? "var(--error)"
                  : view.unhealthy > 0
                    ? "var(--warning)"
                    : "var(--success)",
            }}
          >
            {view.synced} of {view.total} synced
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The external-secrets route answered with something this build cannot
        // read. An empty list here would read as a fleet with nothing wrong.
        <p
          data-testid="eso-health-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The external-secrets endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // The operator is installed -- the family status said so -- and has
        // nothing to sync. Distinct from both "no ESO" and "all healthy".
        <p
          data-testid="eso-health-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          External Secrets is installed but no ExternalSecret you can see exists
          yet.
        </p>
      ) : (
        <>
          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="eso-health-counts"
          >
            <SeverityCount
              label="failing"
              count={view.failing}
              color="var(--error)"
            />
            <SeverityCount
              label="stale"
              count={view.stale}
              color="var(--warning)"
            />
            <SeverityCount
              label="drifted"
              count={view.drift.Drifted}
              color="var(--accent-secondary)"
            />
            <SeverityCount
              label="synced"
              count={view.synced}
              color="var(--success)"
            />
          </div>

          {view.unhealthy === 0 ? (
            <p
              data-testid="eso-health-clear"
              class="py-2 text-center text-xs text-text-muted"
            >
              Every ExternalSecret you can see is synced.
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="eso-health-list">
              {view.rows.map((row) => (
                <ExternalSecretItem
                  key={`${row.namespace}/${row.name}`}
                  row={row}
                />
              ))}
            </ul>
          )}

          {view.unhealthy > view.rows.length && (
            <p
              data-testid="eso-health-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} worst of {view.unhealthy} not
              synced.
            </p>
          )}

          {/* The two non-answers, reported as non-answers. Folding either into
              the synced count is the defect this card's drift handling exists
              to avoid. */}
          {view.drift.Unknown + view.drift.unobserved > 0 && (
            <p
              data-testid="eso-health-drift-unknown"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Drift is undetermined for {view.drift.Unknown} and unchecked for{" "}
              {view.drift.unobserved} of them.
            </p>
          )}
        </>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="eso-health-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no ExternalSecret this
          card could render and {view.unreadableRows === 1 ? "was" : "were"}{" "}
          left out.
        </p>
      )}

      <a
        href={EXTERNAL_SECRETS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all external secrets →
      </a>
    </WidgetShell>
  );
}

/**
 * One ExternalSecret's row.
 *
 * `StatusBadge` is the ESO surface's own badge rather than a chip styled here:
 * the same word in two colours on two pages is a bug in the colour. The second
 * line names the store, because "SyncFailed" against a store that is down is
 * one incident rather than a dozen.
 */
function ExternalSecretItem({ row }: { row: EsoRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <a
          href={externalSecretHref(row.namespace, row.name)}
          title={`${row.namespace}/${row.name}`}
          class="min-w-0 truncate text-xs text-text-secondary no-underline hover:text-accent"
        >
          {row.name}
        </a>
        <span class="shrink-0">
          <StatusBadge status={row.status as Status} />
        </span>
      </span>
      <span class="truncate text-[10px] text-text-muted">
        {row.namespace}
        {row.store === "" ? "" : ` · ${row.store}`}
        {row.drift === "Unknown" ? " · drift undetermined" : ""}
        {row.drift === "unobserved" ? " · drift not yet checked" : ""}
      </span>
    </li>
  );
}

registerWidget({
  id: "eso-health",
  title: "Secret Sync",
  family: "security",
  scopes: ["overview"],
  // One read. Drift and sync state ride inline on each item, so the card needs
  // no second call and the browser re-derives neither.
  sources: ["external-secrets-list"],
  // ESO is CRD-discovered, so an absent operator and a cluster with no
  // ExternalSecrets produce the same 200 with the same empty array. Without
  // this the card would report a cluster with no secret management at all as
  // one where every secret is in order (R1, KTD1).
  familyStatus: "external-secrets-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Wider than
  // its minimum neighbours: a row carries a name beside a status badge and a
  // namespace/store attribution line.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <EsoHealth />,
});
