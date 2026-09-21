import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The severity mapping and the counting live in lib/, under test, because
// this repo has no component test harness (D-10) and the direction the
// mapping errs in is a judgement an operator acts on. Do not inline them.
import { rollUpDiagnostics } from "@/lib/dashboard/diagnostics-summary.ts";
import { PARAM_KEY_NAMESPACE, sourceKeyFor } from "@/lib/dashboard/params.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";
import type { DiagnosticsSummary } from "@/lib/dashboard/wire-types.ts";

/**
 * Namespace diagnostics: how many pods in one namespace the backend considers
 * failing, and why.
 *
 * The first parameterized widget. Everything about it that is not the
 * parameter is ordinary -- it reads one source and renders counts -- and the
 * parameter changes exactly two things: the cache key its read is issued
 * under (`sourceKeyFor`, so diagnostics-for-prod and diagnostics-for-staging
 * are two entries rather than one that flickers between them), and the fact
 * that it can appear on a dashboard twice.
 *
 * The namespace is named ON the card, in the title. Two instances of this
 * widget are otherwise identical rectangles of numbers, and a roll-up whose
 * scope the reader has to remember is a roll-up they will eventually read
 * against the wrong namespace.
 *
 * What it does NOT do is report absence as health. The handler lists pods out
 * of the informer cache, so a namespace that has been deleted -- or one that
 * simply never had anything in it -- answers 200 with an empty list and a
 * zero total rather than 404. "0 failing" over that is a green card about a
 * namespace that is gone, so the empty case gets its own line that says what
 * it actually knows. The other two failure directions are resolved before
 * this renders at all: a 403 from the read is the permission state, and a
 * namespace the caller has lost access to is withheld from the stored layout
 * server-side (R5).
 */
function DiagnosticsSummaryCard({ params }: WidgetProps) {
  const namespace = params[PARAM_KEY_NAMESPACE] ?? "";
  const state = dashboardData.state<DiagnosticsSummary>(
    sourceKeyFor("diagnostics-summary", params),
  );
  const roll = rollUpDiagnostics(state.data);

  return (
    <WidgetShell
      title="Namespace Diagnostics"
      action={
        <span
          data-testid="diagnostics-namespace"
          title={namespace}
          class="max-w-[10rem] truncate rounded-md border border-glass-border px-1.5 py-0.5 text-[11px] text-text-muted"
        >
          {namespace}
        </span>
      }
    >
      {roll.empty ? (
        // Not "all healthy". See the widget docstring: this is also what a
        // deleted namespace looks like, and the two are indistinguishable
        // from here -- so the card says the one thing it does know.
        <p
          data-testid="diagnostics-empty"
          class="py-4 text-center text-xs text-text-muted"
        >
          No workloads found in {namespace}. The namespace may be empty, or it
          may no longer exist.
        </p>
      ) : (
        <div class="flex flex-col gap-3">
          <div class="flex items-baseline gap-4">
            <div>
              <div
                data-testid="diagnostics-critical"
                class="font-mono text-2xl font-bold leading-none"
                style={{
                  color:
                    roll.critical > 0 ? "var(--error)" : "var(--text-muted)",
                }}
              >
                {roll.critical}
              </div>
              <div class="mt-1 text-[11px] text-text-muted">critical</div>
            </div>
            <div>
              <div
                data-testid="diagnostics-warning"
                class="font-mono text-2xl font-bold leading-none"
                style={{
                  color:
                    roll.warning > 0 ? "var(--warning)" : "var(--text-muted)",
                }}
              >
                {roll.warning}
              </div>
              <div class="mt-1 text-[11px] text-text-muted">pending</div>
            </div>
            <div class="ml-auto text-right">
              <div
                data-testid="diagnostics-healthy"
                class="font-mono text-2xl font-bold leading-none text-text-primary"
              >
                {roll.healthy}
              </div>
              <div class="mt-1 text-[11px] text-text-muted">
                of {roll.total} healthy
              </div>
            </div>
          </div>

          {roll.reasons.length === 0 ? (
            <p class="text-xs text-text-secondary">
              Nothing failing in this namespace.
            </p>
          ) : (
            <ul class="flex flex-col gap-1.5" data-testid="diagnostics-reasons">
              {roll.reasons.map((r) => (
                <li
                  key={r.reason}
                  class="flex items-center justify-between gap-2 text-xs"
                >
                  <span class="flex min-w-0 items-center gap-2">
                    <span
                      aria-hidden="true"
                      class="size-2 shrink-0 rounded-full"
                      style={{
                        background:
                          r.severity === "critical"
                            ? "var(--error)"
                            : "var(--warning)",
                      }}
                    />
                    <span class="truncate text-text-secondary">{r.reason}</span>
                  </span>
                  <span class="shrink-0 font-mono font-semibold text-text-primary">
                    {r.count}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </WidgetShell>
  );
}

registerWidget({
  id: "diagnostics-summary",
  title: "Namespace Diagnostics",
  family: "reliability",
  scopes: ["overview"],
  sources: ["diagnostics-summary"],
  // No `familyStatus`. Diagnostics is not a CRD-discovered feature -- it is a
  // first-party route over the informer cache, present on every cluster this
  // server runs against -- so there is no discovery route to ask and nothing
  // for the "not installed" state to be true of.
  //
  // The namespace declares an EMPTY value set, which is not the same as
  // declaring nothing: the legal values are the cluster's namespaces and are
  // not knowable from a catalog, so only the generic bounds apply. Spelled
  // exactly as the server's `paramKeyNamespace`, which is what makes a stored
  // value re-authorized on every read (R5); `registerWidget` refuses any other
  // spelling.
  params: { [PARAM_KEY_NAMESPACE]: [] },
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 3,
  minH: 3,
  defaultW: 4,
  defaultH: 4,
  modes: ["normal"],
  render: (props) => <DiagnosticsSummaryCard {...props} />,
});
