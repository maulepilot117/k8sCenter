import { MeshBadge, MTLSStateBadge } from "@/components/ui/MeshBadges.tsx";
import { SeverityCount } from "@/components/ui/ScanBadges.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The coverage fraction, the meshed/unmeshed split and the percent rounding
// live in lib/, under test (D-10, KTD8). The rounding especially: plain
// Math.round prints 0% for a cluster with one strict workload in three hundred
// and 100% for one with a permissive workload left in it.
import type { MtlsRow } from "@/lib/dashboard/sync-state.ts";
import {
  MESH_MTLS_PAGE_HREF,
  mtlsCoverageView,
} from "@/lib/dashboard/sync-state.ts";
import type { MeshType, MTLSState } from "@/lib/mesh-types.ts";

/** How many non-strict workloads the card lists. */
const ROW_LIMIT = 5;

/**
 * How much of the mesh actually encrypts.
 *
 * One read, `/v1/mesh/mtls`, issued with NO namespace -- which that route
 * treats as a cluster-scoped read. That is why this card takes no parameters
 * (KTD4): the cluster-wide posture is the more useful default for an overview,
 * and it is the one an operator cannot reconstruct without opening every
 * namespace's mesh page in turn.
 *
 * The fraction's denominator is MESHED workloads, not every workload.
 * `unmeshed` is an opt-out rather than a failure -- a workload outside the
 * mesh has no mTLS posture to enforce -- so counting it against coverage would
 * make a cluster look worse the more deliberately it excluded, and counting it
 * toward coverage would be worse still. It is reported beside the fraction
 * instead.
 *
 * Absence and a fully strict mesh are opposite readings of the same response:
 * this route answers 200 with an empty workload list when no mesh is
 * installed. The declared `mesh-status` family is what tells them apart,
 * resolved by the shell before `render` is called (R1, KTD1), which is why a
 * mesh that IS installed and has no workloads can safely say so here.
 */
function MtlsCoverage() {
  const view = mtlsCoverageView(
    dashboardData.state("mesh-mtls").data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="mTLS Coverage"
      action={
        view.readable && view.percent !== null ? (
          <span
            data-testid="mtls-coverage-percent"
            class="text-xs font-semibold"
            style={{
              color:
                view.percent === 100
                  ? "var(--success)"
                  : view.percent === 0
                    ? "var(--error)"
                    : "var(--warning)",
            }}
          >
            {view.percent}%
          </span>
        ) : undefined
      }
    >
      {!view.readable ? (
        // The mTLS route answered with something this build cannot read. An
        // empty posture here would read as a mesh with nothing unencrypted.
        <p
          data-testid="mtls-coverage-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The service mesh endpoint returned a result this card cannot read.
        </p>
      ) : view.total === 0 ? (
        // A mesh is installed -- the family status said so -- and there is
        // nothing in it. Distinct from both "no mesh" and "everything strict".
        <p
          data-testid="mtls-coverage-none"
          class="py-4 text-center text-xs text-text-muted"
        >
          A service mesh is installed but no workload you can see is running in
          it yet.
        </p>
      ) : (
        <>
          {/* The proportion, drawn as well as printed. `meshed` is the
              denominator, so a cluster whose workloads are mostly unmeshed
              does not read as one that is mostly unencrypted. */}
          <div
            class="mb-1 h-2 w-full overflow-hidden rounded-full"
            style={{ backgroundColor: "var(--surface-subtle)" }}
            data-testid="mtls-coverage-bar"
          >
            <div
              class="h-full rounded-full"
              style={{
                width: `${view.percent ?? 0}%`,
                backgroundColor:
                  view.percent === 100
                    ? "var(--success)"
                    : view.percent === 0
                      ? "var(--error)"
                      : "var(--warning)",
              }}
            />
          </div>
          <p
            class="mb-3 text-[11px] leading-snug text-text-muted"
            data-testid="mtls-coverage-caption"
          >
            {view.meshed === 0
              ? // Meshed is the denominator, so this is not zero coverage --
                // it is no coverage to compute. The two are different clusters
                // and the card says different things about them.
                `${view.counts.unmeshed} workload${
                  view.counts.unmeshed === 1 ? "" : "s"
                } you can see, none of them in the mesh.`
              : `${view.counts.active} of ${view.meshed} meshed workload${
                  view.meshed === 1 ? "" : "s"
                } enforce strict mTLS.`}
          </p>

          <div
            class="mb-3 flex flex-wrap gap-1.5"
            data-testid="mtls-coverage-counts"
          >
            <SeverityCount
              label="inactive"
              count={view.counts.inactive}
              color="var(--error)"
            />
            <SeverityCount
              label="mixed"
              count={view.counts.mixed}
              color="var(--warning)"
            />
            <SeverityCount
              label="strict"
              count={view.counts.active}
              color="var(--success)"
            />
            <SeverityCount
              label="unmeshed"
              count={view.counts.unmeshed}
              color="var(--text-muted)"
            />
            {/* Counted apart from all four rather than rounded into one: a
                posture this build does not recognise is neither enforced nor
                broken. */}
            <SeverityCount
              label="unrecognised"
              count={view.counts.unknown}
              color="var(--accent-secondary)"
            />
          </div>

          {view.rows.length === 0 ? (
            <p
              data-testid="mtls-coverage-clear"
              class="py-2 text-center text-xs text-text-muted"
            >
              {view.meshed === 0
                ? "No meshed workload to report on."
                : "Every meshed workload you can see enforces strict mTLS."}
            </p>
          ) : (
            <ul class="flex flex-col gap-2" data-testid="mtls-coverage-list">
              {view.rows.map((row) => (
                <WorkloadItem
                  key={`${row.namespace}/${row.workload}`}
                  row={row}
                />
              ))}
            </ul>
          )}

          {view.counts.inactive + view.counts.mixed > view.rows.length && (
            <p
              data-testid="mtls-coverage-more"
              class="mt-2 text-[11px] leading-snug text-text-muted"
            >
              Showing {view.rows.length} of{" "}
              {view.counts.inactive + view.counts.mixed} not enforcing strict
              mTLS.
            </p>
          )}
        </>
      )}

      {/* The route's own partial-failure keys. A percentage computed over a
          truncated pod list, or with the Prometheus cross-check down, is real
          but incomplete, and printing it without the caveat is this card's own
          version of absence-as-good-news. */}
      {view.partial.length > 0 && (
        <p
          data-testid="mtls-coverage-partial"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          This posture is incomplete ({view.partial.join(", ")}).
        </p>
      )}

      {view.unreadableRows > 0 && (
        <p
          data-testid="mtls-coverage-unreadable-rows"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.unreadableRows} entr
          {view.unreadableRows === 1 ? "y" : "ies"} named no workload this card
          could render and {view.unreadableRows === 1 ? "was" : "were"} left
          out.
        </p>
      )}

      <a
        href={MESH_MTLS_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View mTLS posture →
      </a>
    </WidgetShell>
  );
}

/**
 * One workload that is not enforcing strict mTLS.
 *
 * `MTLSStateBadge` is the mesh surface's own rather than a chip styled here:
 * the same posture in two colours on two pages is a bug in the colour. The
 * kind is marked when it was inferred from a ReplicaSet name rather than read
 * off an owner reference, exactly as the mesh pages mark it -- a row an
 * operator cannot locate is worse than one that admits it is a guess.
 */
function WorkloadItem({ row }: { row: MtlsRow }) {
  return (
    <li class="flex flex-col gap-1">
      <span class="flex items-center justify-between gap-2">
        <span
          title={`${row.namespace}/${row.workload}`}
          class="min-w-0 truncate text-xs text-text-secondary"
        >
          {row.workload}
        </span>
        <span class="shrink-0">
          <MTLSStateBadge state={row.state as MTLSState} />
        </span>
      </span>
      <span class="flex items-center gap-1.5 truncate text-[10px] text-text-muted">
        <MeshBadge mesh={row.mesh as MeshType} />
        {row.namespace}
        {row.workloadKind === ""
          ? ""
          : ` · ${row.workloadKind}${row.confident ? "" : "?"}`}
      </span>
    </li>
  );
}

registerWidget({
  id: "mtls-coverage",
  title: "mTLS Coverage",
  family: "networking",
  scopes: ["overview"],
  // One read, and a cluster-scoped one: the route reads an absent namespace as
  // cluster-wide, which is what keeps this widget parameterless.
  sources: ["mesh-mtls"],
  // A service mesh is CRD-discovered, and this route answers 200 with an empty
  // workload list when none is installed -- the same response a mesh with
  // nothing in it sends. Without this the card would report a cluster with no
  // mesh at all as one with nothing unencrypted (R1, KTD1).
  familyStatus: "mesh-status",
  // Pinned on five sides (KTD7). Four columns: a row carries a workload name
  // beside a posture badge and a mesh/namespace/kind attribution line.
  minW: 4,
  minH: 3,
  defaultW: 4,
  defaultH: 5,
  modes: ["normal"],
  render: () => <MtlsCoverage />,
});
