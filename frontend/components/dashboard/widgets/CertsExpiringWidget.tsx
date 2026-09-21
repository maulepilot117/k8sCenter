import { ColorBadge } from "@/components/ui/ColorBadge.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The classification, the ranking and the unreadable-row accounting live in
// lib/, under test, because none of them is a field read (D-10, KTD8). Do not
// inline them -- and in particular do not inline a threshold.
import type { CertExpiryRow, ExpiryClass } from "@/lib/dashboard/expiry.ts";
import {
  CERTIFICATES_PAGE_HREF,
  certificateHref,
  certsExpiringView,
} from "@/lib/dashboard/expiry.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many certificates the card lists. The counts above them cover every
 * certificate the endpoint returned, not just these. */
const ROW_LIMIT = 5;

/** One colour per class, exhaustive over `ExpiryClass` so a class added to the
 * pure module cannot silently render as `undefined` here. `healthy` never
 * reaches a row -- healthy certificates are not listed -- but it does reach the
 * count chip above them. */
const CLASS_COLORS: Record<ExpiryClass, string> = {
  critical: "var(--error)",
  warning: "var(--warning)",
  healthy: "var(--success)",
};

/**
 * Which certificates are running out, against the thresholds their own
 * operator set.
 *
 * The card reads a classification; it does not compute one. cert-manager's
 * warning and critical day thresholds are operator-settable per certificate
 * and resolve up a chain -- certificate, then issuer, then cluster-issuer --
 * and when the resolved critical threshold is not stricter than the warning
 * one the server falls back to its own defaults and flags the conflict. All of
 * that happens in `backend/internal/certmanager/thresholds.go`, and the payload
 * carries the outcome: `warningThresholdDays`, `criticalThresholdDays` and
 * `thresholdConflict` per certificate. A browser-side copy of "30 and 7" would
 * quietly contradict every cluster that annotates its issuers, in the
 * direction of "this certificate is fine".
 *
 * Absence and an all-healthy fleet are opposite readings of the same response.
 * cert-manager is CRD-discovered, and `/v1/certificates/certificates` answers
 * 200 with an empty array whether the operator is absent or the cluster simply
 * manages no certificates. The declared `certificates-status` family is what
 * tells them apart, resolved by the shell before `render` is called (R1,
 * KTD1) -- so an empty list here is known to mean cert-manager is installed
 * and has nothing to manage, and the card says exactly that.
 */
function CertsExpiring() {
  const view = certsExpiringView(
    dashboardData.state("certificates-list").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Certificate Expiry"
      action={
        view.readable && view.total > 0 ? (
          <span
            data-testid="certs-expiring-summary"
            class="text-xs"
            style={{
              color:
                view.critical > 0
                  ? "var(--error)"
                  : view.expiring > 0
                    ? "var(--warning)"
                    : "var(--text-muted)",
            }}
          >
            {view.expiring} of {view.total} expiring
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The certificates route answered with something this build cannot
        // read. Zero expiring here would read as a fleet in good order.
        <p
          data-testid="certs-expiring-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The certificates endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // cert-manager is installed -- the family status said so -- and
        // manages nothing. That is not the same as no cert-manager, and it is
        // not the same as everything being healthy either.
        <p
          data-testid="certs-expiring-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          cert-manager is installed but manages no certificates you can see.
        </p>
      ) : view.expiring === 0 ? (
        <p
          data-testid="certs-expiring-clear"
          class="py-4 text-center text-xs text-text-muted"
        >
          None of the {view.total} certificate{view.total === 1 ? "" : "s"} you
          can see is inside its renewal threshold.
        </p>
      ) : (
        <>
          <div
            data-testid="certs-expiring-counts"
            class="mb-3 flex flex-wrap gap-1.5"
          >
            <SeverityCount
              label="critical"
              count={view.critical}
              color={CLASS_COLORS.critical}
            />
            <SeverityCount
              label="warning"
              count={view.warning}
              color={CLASS_COLORS.warning}
            />
            <SeverityCount
              label="healthy"
              count={view.healthy}
              color={CLASS_COLORS.healthy}
            />
          </div>

          <ul class="flex flex-col gap-2" data-testid="certs-expiring-list">
            {view.rows.map((row) => (
              <CertificateItem key={`${row.namespace}/${row.name}`} row={row} />
            ))}
          </ul>

          {view.expiring > view.rows.length && (
            <p
              data-testid="certs-expiring-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing the {view.rows.length} soonest of {view.expiring}.
            </p>
          )}
        </>
      )}

      {view.conflicted > 0 && (
        // Named rather than hidden. An operator who annotated an issuer with a
        // critical threshold no stricter than its warning one gets the
        // defaults instead, and a card that showed the resulting
        // classification without explaining it looks like it ignored them.
        <p
          data-testid="certs-expiring-conflicts"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.conflicted === 1
            ? "1 certificate carries conflicting threshold annotations and is"
            : `${view.conflicted} certificates carry conflicting threshold annotations and are`}{" "}
          classified against cert-manager&rsquo;s defaults.
        </p>
      )}

      {view.unclassified > 0 && (
        // Not healthy, and not hidden either. A certificate whose expiry the
        // payload did not carry is one nobody is watching.
        <p
          data-testid="certs-expiring-unclassified"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unclassified} certificate
          {view.unclassified === 1 ? " has" : "s have"} no readable expiry and{" "}
          {view.unclassified === 1 ? "was" : "were"} left out of the counts
          above.
        </p>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="certs-expiring-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no certificate this
          card could render and {view.unreadableRows === 1 ? "was" : "were"}{" "}
          left out.
        </p>
      )}

      <a
        href={CERTIFICATES_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View all certificates →
      </a>
    </WidgetShell>
  );
}

/**
 * One certificate's row: how long it has, and which threshold says that is a
 * problem.
 *
 * The thresholds are on the row rather than in the header because they are
 * per-certificate facts. A header claiming "warning below 30 days" would be a
 * cluster-wide rule that may not exist on a cluster whose issuers each set
 * their own.
 */
function CertificateItem({ row }: { row: CertExpiryRow }) {
  const scope = row.namespace === "" ? "cluster-scoped" : row.namespace;
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <a
          href={certificateHref(row.namespace, row.name)}
          title={`${scope}/${row.name}`}
          class="min-w-0 truncate text-xs text-text-secondary no-underline hover:text-accent"
        >
          {row.name}
        </a>
        <span class="shrink-0">
          <ColorBadge
            label={
              row.daysRemaining < 0 ? "expired" : `${row.daysRemaining}d left`
            }
            color={CLASS_COLORS[row.class]}
          />
        </span>
      </span>
      <span
        class="truncate text-[10px] text-text-muted"
        title={`critical at ${row.criticalDays}d, warning at ${row.warnDays}d`}
      >
        {scope} · {row.class} below{" "}
        {row.class === "critical" ? row.criticalDays : row.warnDays}d
        {row.thresholdConflict ? " (defaults)" : ""}
      </span>
    </li>
  );
}

registerWidget({
  id: "certs-expiring",
  title: "Certificate Expiry",
  family: "security",
  scopes: ["overview"],
  // The full inventory rather than `/v1/certificates/expiring`: the expiring
  // route carries a pre-computed severity string instead of the thresholds
  // behind it, and returns nothing at all for a healthy fleet -- so a card
  // built on it could tell neither "every certificate is healthy" from
  // "cert-manager manages nothing", nor which threshold a classification came
  // from. See `certificates-list` in lib/dashboard/types.ts.
  sources: ["certificates-list"],
  // cert-manager is CRD-discovered, so an absent operator and a cluster with
  // no certificates produce the same 200 with the same empty array. Without
  // this the card would report an unprotected cluster as a well-managed one
  // (R1, KTD1).
  familyStatus: "certificates-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once. Wider than
  // its minimum neighbours: a row carries a certificate name beside a
  // days-left badge and a threshold attribution line.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <CertsExpiring />,
});
