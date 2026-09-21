import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The partial-answer accounting, the zero-versus-absent rule and the error-rate
// rounding live in lib/, under test (D-10, KTD8). The first of those especially:
// the route answers with a zero for every query Prometheus refused, and a card
// that printed those zeros would report an unmeasured service as a flawless one.
import type { GoldenSignalsView } from "@/lib/dashboard/networking.ts";
import {
  goldenSignalsServiceHref,
  goldenSignalsView,
} from "@/lib/dashboard/networking.ts";
import {
  PARAM_KEY_NAMESPACE,
  PARAM_KEY_SERVICE,
  sourceKeyFor,
} from "@/lib/dashboard/params.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
import type { WidgetProps } from "@/lib/dashboard/types.ts";

/**
 * The four golden signals for one mesh-managed service.
 *
 * Parameterized by TWO values, and this is the case the parameter dialog's
 * cascading branch was built for: a service is only meaningful inside a
 * namespace, the list of services is not knowable until the namespace is
 * chosen, and neither value is optional -- `/v1/mesh/golden-signals` answers
 * 400 with either missing. The namespace is stored under the key the read path
 * re-authorizes, so an operator who loses access to it stops seeing this card
 * rather than seeing it break (R5).
 *
 * Absence and silence are opposite readings of the same numbers. A service
 * nobody is calling reports zero requests per second; so does a service whose
 * Prometheus queries failed, because the backend zero-values what it could not
 * measure and names the failures separately. `goldenSignalsView` is what keeps
 * those apart, and the declared `mesh-status` family is what keeps both apart
 * from a cluster with no service mesh at all (R1, KTD1).
 */
function MeshGoldenSignals({ params }: WidgetProps) {
  const namespace = params[PARAM_KEY_NAMESPACE] ?? "";
  const service = params[PARAM_KEY_SERVICE] ?? "";
  const view = goldenSignalsView(
    dashboardData.state(sourceKeyFor("mesh-golden-signals", params)).data,
  );

  return (
    <WidgetShell
      title="Golden Signals"
      action={
        <span
          data-testid="golden-signals-target"
          title={`${namespace}/${service}`}
          class="max-w-[12rem] truncate rounded-md border border-glass-border px-1.5 py-0.5 text-[11px] text-text-muted"
        >
          {namespace}/{service}
        </span>
      }
    >
      {!view.readable ? (
        // The mesh route answered with something this build cannot read.
        // Empty numbers here would read as a silent service.
        <p
          data-testid="golden-signals-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The service mesh endpoint returned a result this card cannot read.
        </p>
      ) : !view.available ? (
        // A mesh is installed -- the family status said so -- and its metrics
        // backend could not be asked. Distinct from a quiet service, which is
        // the whole reason this branch exists rather than rendering zeros.
        <p
          data-testid="golden-signals-unmeasured"
          class="py-4 text-center text-xs text-text-muted"
        >
          {view.reason === "metrics_unavailable"
            ? "A service mesh is installed but no Prometheus is wired up, so there are no signals to read."
            : "The mesh could not measure this service."}
        </p>
      ) : (
        <>
          <div class="mb-3 grid grid-cols-2 gap-2" data-testid="golden-signals">
            <Signal
              testId="golden-signals-rps"
              label="requests/s"
              value={view.rps}
              format={(v) => (v < 10 ? v.toFixed(2) : Math.round(v).toString())}
            />
            <Signal
              testId="golden-signals-errors"
              label="errors"
              value={view.errorPercent}
              format={(v) => `${v}%`}
              // Any error rate at all is worth a colour, and the rounding
              // guarantees that 0 means zero rather than "not many".
              color={
                view.errorPercent === null || view.errorPercent === 0
                  ? "var(--success)"
                  : view.errorPercent >= 5
                    ? "var(--error)"
                    : "var(--warning)"
              }
            />
          </div>

          <div
            class="flex flex-wrap gap-1.5"
            data-testid="golden-signals-latency"
          >
            <Quantile label="p50" value={view.p50Ms} />
            <Quantile label="p95" value={view.p95Ms} />
            <Quantile label="p99" value={view.p99Ms} />
          </div>

          {view.rps === 0 && view.missing.length === 0 && (
            // A measured zero, which is a finding: the mesh is watching this
            // service and nothing is calling it.
            <p
              data-testid="golden-signals-idle"
              class="mt-3 text-[11px] leading-snug text-text-muted"
            >
              No traffic reached this service in the last window.
            </p>
          )}
        </>
      )}

      {/* The route's own partial-failure surface. A latency quantile computed
          while the error queries failed is real and incomplete, and printing
          the rest without saying so is this card's version of a zero that
          means nothing. */}
      {view.missing.length > 0 && (
        <p
          data-testid="golden-signals-partial"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          Prometheus could not answer {view.missing.join(", ")}, so{" "}
          {view.missing.length === 1 ? "that signal is" : "those signals are"}{" "}
          left blank rather than shown as zero.
        </p>
      )}

      <a
        href={goldenSignalsServiceHref(namespace, service)}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View service →
      </a>
    </WidgetShell>
  );
}

/**
 * One headline number.
 *
 * A null value prints an em dash rather than a zero, everywhere, because the
 * two mean opposite things on this card and there is no arrangement of digits
 * that says "we could not measure this".
 */
function Signal({
  testId,
  label,
  value,
  format,
  color,
}: {
  testId: string;
  label: string;
  value: number | null;
  format: (value: number) => string;
  color?: string;
}) {
  return (
    <div class="flex flex-col">
      <span
        data-testid={testId}
        class="text-lg font-semibold leading-tight"
        style={color !== undefined && value !== null ? { color } : undefined}
      >
        {value === null ? "—" : format(value)}
      </span>
      <span class="text-[11px] text-text-muted">{label}</span>
    </div>
  );
}

/** One latency quantile. Milliseconds are the route's own unit. */
function Quantile({ label, value }: { label: string; value: number | null }) {
  return (
    <span
      class="inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs"
      style={{ backgroundColor: "var(--surface-subtle)" }}
    >
      <span class="text-text-muted">{label}</span>
      <span class="font-medium text-text-secondary">
        {value === null ? "—" : `${Math.round(value)}ms`}
      </span>
    </span>
  );
}

registerWidget({
  id: "mesh-golden-signals",
  title: "Golden Signals",
  family: "networking",
  scopes: ["overview"],
  sources: ["mesh-golden-signals"],
  // A service mesh is CRD-discovered, and this route answers 200 with zeroed
  // signals when none is installed rather than refusing -- the same body a
  // healthy, idle service produces. Without this the card would report a
  // cluster with no mesh as a service with no traffic (R1, KTD1).
  familyStatus: "mesh-status",
  // Two mandatory parameters, which is what this widget contributes to the
  // catalog: the route answers 400 with either missing, and the two together
  // are the cache key its read is issued under. Both value sets are empty --
  // neither a cluster's namespaces nor a namespace's services are knowable
  // from a catalog -- but they are NOT bounded the same way: the namespace is
  // re-authorized against the live cluster on every read (R5), and the service
  // is bounded by its shape instead, because nothing re-authorizes it. See
  // PARAM_VALUE_SHAPE in lib/dashboard/params.ts.
  params: { [PARAM_KEY_NAMESPACE]: [], [PARAM_KEY_SERVICE]: [] },
  // Pinned on five sides (KTD7). Three columns is enough: the card carries two
  // numbers over three latency chips, not a list of names.
  minW: 3,
  minH: 3,
  defaultW: 3,
  defaultH: 4,
  modes: ["normal"],
  render: (props) => <MeshGoldenSignals {...props} />,
});
