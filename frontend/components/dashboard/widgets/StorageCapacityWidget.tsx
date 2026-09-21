import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
// The vector parsing, the ranking and the band arithmetic live in lib/, under
// test, because none of them is a field read (D-10, KTD8). The slug is named
// in data.ts, so this file holds no route knowledge at all.
import type { VolumePressureRow } from "@/lib/dashboard/pressure.ts";
import {
  claimHref,
  STORAGE_PAGE_HREF,
  storageCapacityView,
} from "@/lib/dashboard/pressure.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";

/** How many volumes and classes the card lists. The slug's `topk` already caps
 * the query at ten server-side; this is the display cap, and the smaller of
 * the two wins. */
const ROW_LIMIT = 6;

/**
 * Storage: what the cluster can provision, and what is closest to full.
 *
 * There is no single storage overview endpoint to ask. The storage family
 * mounts drivers, classes, snapshots, snapshot-classes and presets under its
 * own prefix and nothing that spans them, so the card composes the class
 * inventory with the cluster-wide `cluster/storage-capacity` slug -- a named,
 * server-owned PromQL template from the slug registry, never query text from
 * here (D-8, R15).
 *
 * The composition is deliberately lopsided. The classes read is REQUIRED and
 * the slug is OPTIONAL, so a cluster with no Prometheus, or an account without
 * the cluster-wide PVC grant the slug demands, still gets the half of the card
 * that works. The alternative -- gating the inventory on the metrics -- makes
 * a Prometheus outage blank a list of StorageClasses that the API server
 * answered for perfectly well.
 *
 * And the volume half never renders an empty list to mean "nothing is nearly
 * full". `volumesReadable` is what tells "Prometheus answered and no volume is
 * reporting stats" apart from "Prometheus did not answer", and the second
 * reads as the first unless the card says which one it is.
 */
function StorageCapacity() {
  const capacity = dashboardData.state("volume-capacity");
  const view = storageCapacityView(
    dashboardData.state("storage-classes").data,
    capacity.data,
    ROW_LIMIT,
  );

  return (
    <WidgetShell
      title="Storage"
      action={
        view.classTotal > 0 ? (
          <span
            data-testid="storage-capacity-summary"
            class="text-xs text-text-muted"
          >
            {view.classTotal} class{view.classTotal === 1 ? "" : "es"}
          </span>
        ) : undefined
      }
    >
      {!view.classesReadable ? (
        <p
          data-testid="storage-capacity-unreadable"
          class="py-4 text-center text-xs text-text-muted"
        >
          The storage class list returned a result this card cannot read.
        </p>
      ) : view.classes.length === 0 ? (
        <p
          data-testid="storage-capacity-no-classes"
          class="py-3 text-center text-xs text-text-muted"
        >
          No StorageClass on this cluster. A PersistentVolumeClaim with no class
          name will stay Pending.
        </p>
      ) : (
        <ul class="flex flex-col gap-1" data-testid="storage-capacity-classes">
          {view.classes.map((c) => (
            <li
              key={c.name}
              class="flex items-center justify-between gap-2 text-xs"
            >
              <span class="flex min-w-0 items-center gap-1.5">
                <a
                  href={`/cluster/storageclasses/${encodeURIComponent(c.name)}`}
                  title={c.name}
                  class="truncate text-text-secondary no-underline hover:text-accent"
                >
                  {c.name}
                </a>
                {c.isDefault && (
                  <span
                    class="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
                    style={{
                      background: "var(--bg-hover)",
                      color: "var(--accent)",
                    }}
                  >
                    default
                  </span>
                )}
              </span>
              <span class="shrink-0 truncate text-[10px] text-text-muted">
                {c.provisioner}
              </span>
            </li>
          ))}
        </ul>
      )}

      {view.classesReadable && view.defaultClass === null && (
        <p
          data-testid="storage-capacity-no-default"
          class="mt-2 text-[11px] leading-snug"
          style={{ color: "var(--warning)" }}
        >
          No default StorageClass.
        </p>
      )}

      <p class="mt-3 mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-text-muted">
        Fullest volumes
      </p>

      {!view.volumesReadable ? (
        // The degradation path. `errorKind` tells a refusal apart from a
        // failure, which matters because the slug's grant is a CLUSTER-scoped
        // list on persistentvolumeclaims -- a grant a namespace-scoped
        // operator does not have, and the route answers them with the same
        // opaque 404 it gives an unknown slug. Offering that person a retry
        // would be an invitation to wait for something that will never come.
        <p
          data-testid="storage-capacity-volumes-missing"
          class="py-2 text-center text-xs text-text-muted"
        >
          {capacity.errorKind === "permission"
            ? "This account may not read cluster-wide volume usage."
            : capacity.error !== null
              ? "Volume usage could not be read."
              : capacity.data === null
                ? "Loading volume usage…"
                : "Prometheus returned a result this card cannot read."}
        </p>
      ) : view.volumes.length === 0 ? (
        <p
          data-testid="storage-capacity-volumes-empty"
          class="py-2 text-center text-xs text-text-muted"
        >
          Prometheus reported no volume statistics.
        </p>
      ) : (
        <ul class="flex flex-col gap-1.5" data-testid="storage-capacity-list">
          {view.volumes.map((row) => (
            <VolumeRow key={`${row.namespace}/${row.claim}`} row={row} />
          ))}
        </ul>
      )}

      {view.volumesDropped > 0 && (
        <p
          data-testid="storage-capacity-dropped"
          class="mt-2 text-[11px] leading-snug text-text-muted"
        >
          {view.volumesDropped} series carried no readable value and{" "}
          {view.volumesDropped === 1 ? "was" : "were"} left out.
        </p>
      )}

      {view.volumeWarnings.length > 0 && (
        <p
          data-testid="storage-capacity-warnings"
          class="mt-2 text-[11px] leading-snug"
          style={{ color: "var(--warning)" }}
          title={view.volumeWarnings.join("\n")}
        >
          Prometheus reported the result as incomplete.
        </p>
      )}

      <a
        href={STORAGE_PAGE_HREF}
        class="mt-3 block text-xs text-accent no-underline"
      >
        View storage →
      </a>
    </WidgetShell>
  );
}

const LEVEL_COLOR = {
  critical: "var(--error)",
  warning: "var(--warning)",
  ok: "var(--success)",
} as const;

/** One claim's row. The percentage can exceed 100 -- a filesystem's reserved
 * blocks make `used > capacity` reportable -- so the bar clamps and the figure
 * does not. */
function VolumeRow({ row }: { row: VolumePressureRow }) {
  const color = LEVEL_COLOR[row.level];

  return (
    <li class="flex items-center gap-2 text-xs">
      <a
        href={claimHref(row)}
        title={`${row.namespace}/${row.claim}`}
        class="min-w-0 flex-1 truncate text-text-secondary no-underline hover:text-accent"
      >
        {row.claim}
        <span class="ml-1.5 text-[10px] text-text-muted">{row.namespace}</span>
      </a>
      <span
        aria-hidden="true"
        class="hidden h-1.5 w-16 shrink-0 overflow-hidden rounded-sm bg-hover sm:block"
      >
        <span
          class="block h-full rounded-sm"
          style={{
            width: `${Math.max(0, Math.min(100, row.percent))}%`,
            background: color,
          }}
        />
      </span>
      <span
        class="w-12 shrink-0 text-right font-mono font-semibold"
        style={{ color }}
      >
        {Math.round(row.percent)}%
      </span>
    </li>
  );
}

registerWidget({
  id: "storage-capacity",
  title: "Storage",
  family: "reliability",
  scopes: ["overview"],
  sources: ["storage-classes", "volume-capacity"],
  // The slug is optional so a Prometheus that is down, absent or refused
  // leaves the class inventory on screen instead of blanking the card. This
  // is the mirror of what `top-consumers` does with its memory slug: there,
  // optionality keeps the tab a viewer is looking at from being blanked by the
  // other one; here it keeps the half that the API server can answer from
  // being blanked by the half that Prometheus cannot.
  optionalSources: ["volume-capacity"],
  // No `familyStatus`. StorageClass is core `storage.k8s.io/v1`, not a CRD,
  // and Prometheus is not one of the six discovered families -- the slug route
  // answers 503 when monitoring was never discovered, which the card's own
  // degradation copy covers.
  //
  // Wider than the smallest cards: a volume row carries a claim name, its
  // namespace and a figure, and three grid columns truncate claim names to
  // uselessness.
  //
  // Pinned on five sides -- here, two literals in registry_test.ts, the Go
  // catalog and two literals in parity_test.go (KTD7). Chosen once.
  minW: 4,
  minH: 4,
  defaultW: 4,
  defaultH: 7,
  modes: ["normal"],
  render: () => <StorageCapacity />,
});
