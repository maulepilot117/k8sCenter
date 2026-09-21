import { GaugeRing } from "@/components/ui/GaugeRing.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { SEVERITY_COLORS } from "@/lib/badge-colors.ts";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The score band, the ungoverned case and the trend delta live in lib/, under
// test, because none of them is a field read (D-10, KTD8). Do not inline them.
import {
  COMPLIANCE_PAGE_HREF,
  policyComplianceView,
} from "@/lib/dashboard/severity.ts";
import { scoreColor } from "@/lib/score-color.ts";

/**
 * How much of the cluster's policy set is passing.
 *
 * The first card whose entire meaning depends on the availability state
 * underneath it. A cluster with no Kyverno and no Gatekeeper scores 100 on
 * `/v1/policies/compliance` -- `computeCompliance` divides a zero weighted
 * fail by a zero weighted total and returns the identity -- so a card that
 * rendered the payload would show a full green ring to an operator who has no
 * policy engine at all. That is the failure R1 exists to remove, and it is
 * removed one level up: `familyStatus` sends the widget to the "not installed"
 * state before `render` is ever called, so the gauge below is only ever drawn
 * on a cluster that actually runs an engine (KTD1).
 *
 * One level down, the same shape repeats inside the payload and has to be
 * handled here: an engine that IS installed with no policies defined also
 * scores 100. `view.governed` is what tells the two apart, and the card says
 * "no policies defined" rather than drawing a ring that claims perfection.
 *
 * The 30-day trend is a second, optional read. It is admin-gated and answers
 * 503 on a deployment with no database, and the current score is present and
 * worth rendering in both cases -- so the source is declared optional and the
 * card degrades to a line of copy instead of the shell's error state.
 */
function PolicyCompliance() {
  const score = dashboardData.state("policy-compliance-score");
  const history = dashboardData.state("policy-compliance-history");
  const view = policyComplianceView(score.data, history.data);

  return (
    <WidgetShell
      title="Policy Compliance"
      action={
        view.readable && view.governed ? (
          <span
            data-testid="policy-compliance-total"
            class="text-xs text-text-muted"
          >
            {view.total} polic{view.total === 1 ? "y" : "ies"}
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The compliance route answered with something this build cannot read.
        // Saying so is the honest rendering; a zero here would read as total
        // non-compliance rather than as an unreadable response.
        <p
          data-testid="policy-compliance-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The compliance endpoint returned a result this card cannot read.
        </p>
      ) : !view.governed ? (
        <p
          data-testid="policy-compliance-ungoverned"
          class="py-4 text-center text-xs text-text-muted"
        >
          A policy engine is running, but no policies are defined. There is
          nothing to score yet.
        </p>
      ) : (
        <div class="flex flex-wrap items-center gap-4">
          <div class="shrink-0">
            <GaugeRing
              value={view.score}
              size={96}
              strokeWidth={8}
              color={scoreColor(view.score)}
              valueSize="22px"
            />
          </div>

          <div class="flex min-w-[9rem] flex-1 flex-col gap-2">
            <div class="flex items-baseline gap-3 text-xs">
              <span data-testid="policy-compliance-pass">
                <span class="font-mono font-semibold text-text-primary">
                  {view.pass}
                </span>
                <span class="ml-1 text-text-muted">passing</span>
              </span>
              <span data-testid="policy-compliance-fail">
                <span
                  class="font-mono font-semibold"
                  style={{
                    color:
                      view.fail > 0 ? "var(--error)" : "var(--text-primary)",
                  }}
                >
                  {view.fail}
                </span>
                <span class="ml-1 text-text-muted">blocked</span>
              </span>
              <span data-testid="policy-compliance-warn">
                <span
                  class="font-mono font-semibold"
                  style={{
                    color:
                      view.warn > 0 ? "var(--warning)" : "var(--text-primary)",
                  }}
                >
                  {view.warn}
                </span>
                <span class="ml-1 text-text-muted">audited</span>
              </span>
            </div>

            {view.fail === 0 && view.warn === 0 ? (
              <p
                data-testid="policy-compliance-clear"
                class="text-xs text-text-secondary"
              >
                Every policy is passing.
              </p>
            ) : (
              <div
                data-testid="policy-compliance-severities"
                class="flex flex-wrap gap-1.5"
              >
                {view.failuresBySeverity.map((bucket) => (
                  <SeverityCount
                    key={bucket.severity}
                    label={bucket.severity}
                    count={bucket.count}
                    color={
                      SEVERITY_COLORS[bucket.severity] ?? "var(--text-muted)"
                    }
                  />
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {view.readable && view.governed && (
        <p
          data-testid="policy-compliance-trend"
          class="mt-3 text-[11px] leading-snug text-text-muted"
        >
          {view.history.delta !== null ? (
            <>
              {view.history.delta === 0
                ? "No change"
                : `${view.history.delta > 0 ? "Up" : "Down"} ${Math.abs(
                    Math.round(view.history.delta),
                  )} points`}{" "}
              over {view.history.days} days.
            </>
          ) : history.errorKind === "permission" ? (
            // The endpoint is admin-gated, and the refusal is a standing fact
            // about the account rather than something a retry fixes.
            <>
              The {view.history.days}-day trend requires an administrator
              account.
            </>
          ) : view.history.available ? (
            <>Not enough history yet for a {view.history.days}-day trend.</>
          ) : (
            // 503 from the handler when the deployment has no database, or any
            // other failed read. Either way the score above is current.
            <>The {view.history.days}-day trend is unavailable.</>
          )}
        </p>
      )}

      <a
        href={COMPLIANCE_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View compliance report →
      </a>
    </WidgetShell>
  );
}

registerWidget({
  id: "policy-compliance",
  title: "Policy Compliance",
  family: "security",
  scopes: ["overview"],
  sources: ["policy-compliance-score", "policy-compliance-history"],
  // The history read is admin-gated and answers 503 without a database, so on
  // most accounts it never lands. Gating the card on it would blank a gauge
  // the compliance endpoint already answered, permanently.
  optionalSources: ["policy-compliance-history"],
  // Kyverno and Gatekeeper are CRD-discovered, so an absent engine is a
  // question only `/v1/policies/status` can answer: the compliance route
  // returns 200 with a score of 100 whether the cluster is fully compliant or
  // has no policy engine at all. Declaring the family is what buys the
  // explicit "not installed" state and keeps that 100 off an unpoliced
  // cluster's dashboard (R1, KTD1).
  familyStatus: "policies-status",
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 4,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <PolicyCompliance />,
});
