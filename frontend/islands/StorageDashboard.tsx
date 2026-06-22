import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";
import SnapshotList from "@/islands/SnapshotList.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import Donut from "@/components/charts/Donut.tsx";
import BarRow from "@/components/charts/BarRow.tsx";
import PVCWizard from "@/islands/PVCWizard.tsx";
import StorageClassWizard from "@/islands/StorageClassWizard.tsx";

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
  clusterScoped?: boolean;
  isOverview: boolean;
  isSnapshots: boolean;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/storage" || path === "/storage/overview") {
    return {
      kind: "",
      title: "Overview",
      isOverview: true,
      isSnapshots: false,
    };
  }

  if (path === "/storage/snapshots") {
    return {
      kind: "",
      title: "Snapshots",
      isOverview: false,
      isSnapshots: true,
    };
  }

  if (path === "/storage/pvcs") {
    return {
      kind: "pvcs",
      title: "Persistent Volume Claims",
      createHref: "/storage/pvcs/new",
      isOverview: false,
      isSnapshots: false,
    };
  }

  if (path === "/cluster/pvs") {
    return {
      kind: "pvs",
      title: "Persistent Volumes",
      clusterScoped: true,
      isOverview: false,
      isSnapshots: false,
    };
  }

  if (path === "/cluster/storageclasses") {
    return {
      kind: "storageclasses",
      title: "Storage Classes",
      clusterScoped: true,
      isOverview: false,
      isSnapshots: false,
    };
  }

  // Default: overview
  return {
    kind: "",
    title: "Overview",
    isOverview: true,
    isSnapshots: false,
  };
}

export default function StorageDashboard(
  { currentPath }: { currentPath: string },
) {
  // Reading selectedNamespace.value here wires reactivity — the shared
  // resource-counts store re-fetches when namespace changes.
  const _ns = selectedNamespace.value;

  const pvcWizardOpen = useSignal(false);
  const scWizardOpen = useSignal(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    const params = new URLSearchParams(globalThis.location?.search ?? "");
    if (params.get("action") === "create") {
      if (currentPath.includes("storageclasses")) {
        scWizardOpen.value = true;
      } else {
        pvcWizardOpen.value = true;
      }
    }
  }, []);

  const { kind, title, createHref, clusterScoped, isOverview, isSnapshots } =
    resolveTab(currentPath);

  // Derive counts from shared store — no separate fetch needed.
  const counts = resourceCounts.value;
  const countsReady = counts !== null;

  const totalPVCs = counts?.["persistentvolumeclaims"] ??
    counts?.["pvcs"] ?? 0;
  const totalPVs = counts?.["persistentvolumes"] ?? counts?.["pvs"] ?? 0;
  const storageClasses = counts?.["storageclasses"] ?? 0;

  // For the PVC donut we need phase breakdown — that requires a list fetch.
  // On the Overview we keep the donut with totals only (no invented phase split).
  // The subtitle for list pages derives from the relevant count.
  const listCount = kind ? (getCount(kind) ?? 0) : 0;

  const pageTitle = isOverview ? "Storage" : title;
  const subtitle = isOverview
    ? "Manage persistent volumes, claims, storage classes, and snapshots"
    : isSnapshots
    ? "Volume snapshots"
    : countsReady
    ? `${listCount} ${title.toLowerCase()}`
    : `Loading ${title.toLowerCase()}…`;

  // Overview donut: show total PVCs as a single neutral segment
  // (no invented phase split — real phases require a list fetch that's
  // preserved in the ResourceTable below).
  const pvcDonutSegments = [
    {
      value: Math.max(totalPVCs, 1),
      color: "var(--accent)",
      label: "Total",
    },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header — 24/700 per archetype spec */}
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          justifyContent: "space-between",
          gap: "16px",
          marginBottom: "20px",
        }}
      >
        <div>
          <h1
            style={{
              margin: 0,
              fontSize: "24px",
              fontWeight: 700,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              lineHeight: 1.2,
            }}
          >
            {pageTitle}
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            {subtitle}
          </p>
        </div>
        <div style={{ display: "flex", gap: "8px", flexShrink: 0 }}>
          <button
            type="button"
            onClick={() => (pvcWizardOpen.value = true)}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 16px",
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--bg-base)",
              background: "var(--accent)",
              borderRadius: "9px",
              border: "none",
              cursor: "pointer",
              whiteSpace: "nowrap",
              fontFamily: "inherit",
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="2.5"
            >
              <path d="M4 8h8M8 4v8" />
            </svg>
            New PVC
          </button>
          <button
            type="button"
            onClick={() => (scWizardOpen.value = true)}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 16px",
              fontSize: "13px",
              fontWeight: 600,
              color: "var(--text-primary)",
              background: "transparent",
              borderRadius: "9px",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
              whiteSpace: "nowrap",
              fontFamily: "inherit",
            }}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
            >
              <path d="M4 8h8M8 4v8" />
            </svg>
            New StorageClass
          </button>
        </div>

        {pvcWizardOpen.value && (
          <PVCWizard onClose={() => (pvcWizardOpen.value = false)} />
        )}
        {scWizardOpen.value && (
          <StorageClassWizard onClose={() => (scWizardOpen.value = false)} />
        )}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {isOverview
          ? (
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                gap: "var(--grid-gap, 20px)",
              }}
            >
              {/* Overview charts row — glass widgets */}
              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  gap: "var(--grid-gap, 12px)",
                }}
              >
                {/* PVC total donut */}
                <WidgetShell
                  title="PVC Health"
                  style={{ flex: "1 1 220px", minWidth: "200px" }}
                >
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "20px",
                    }}
                  >
                    <Donut
                      segments={!countsReady
                        ? [{ value: 1, color: "var(--bg-elevated)" }]
                        : pvcDonutSegments}
                      size={96}
                      thickness={14}
                      center={
                        <div style={{ textAlign: "center" }}>
                          <div
                            style={{
                              fontSize: "20px",
                              fontWeight: 700,
                              fontFamily: "var(--font-mono)",
                              color: "var(--text-primary)",
                              fontVariantNumeric: "tabular-nums",
                            }}
                          >
                            {!countsReady ? "—" : totalPVCs}
                          </div>
                          <div
                            style={{
                              fontSize: "10px",
                              fontWeight: 600,
                              letterSpacing: "0.05em",
                              textTransform: "uppercase",
                              color: "var(--text-muted)",
                            }}
                          >
                            total
                          </div>
                        </div>
                      }
                    />
                    <div
                      style={{
                        display: "flex",
                        flexDirection: "column",
                        gap: "6px",
                      }}
                    >
                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: "7px",
                        }}
                      >
                        <span
                          style={{
                            width: "8px",
                            height: "8px",
                            borderRadius: "50%",
                            background: "var(--accent)",
                            flexShrink: 0,
                          }}
                        />
                        <span
                          style={{
                            fontSize: "12px",
                            color: "var(--text-muted)",
                            minWidth: "44px",
                          }}
                        >
                          Claims
                        </span>
                        <span
                          style={{
                            fontSize: "12px",
                            fontWeight: 600,
                            fontFamily: "var(--font-mono)",
                            color: "var(--text-primary)",
                            fontVariantNumeric: "tabular-nums",
                          }}
                        >
                          {!countsReady ? "—" : totalPVCs}
                        </span>
                      </div>
                    </div>
                  </div>
                </WidgetShell>

                {/* Storage resources bar chart */}
                <WidgetShell
                  title="Storage Resources"
                  style={{ flex: "2 1 320px", minWidth: "280px" }}
                >
                  <div style={{ paddingTop: "4px" }}>
                    <BarRow
                      label="PVCs"
                      value={!countsReady ? 0 : totalPVCs}
                      max={Math.max(totalPVCs, 1)}
                      suffix={!countsReady ? "—" : String(totalPVCs)}
                      color="var(--accent)"
                    />
                    <BarRow
                      label="Volumes"
                      value={!countsReady ? 0 : totalPVs}
                      max={Math.max(totalPVs, 1)}
                      suffix={!countsReady ? "—" : String(totalPVs)}
                      color="var(--success)"
                    />
                    <BarRow
                      label="StorageClasses"
                      value={!countsReady ? 0 : storageClasses}
                      max={Math.max(storageClasses, 1)}
                      suffix={!countsReady ? "—" : String(storageClasses)}
                      color="var(--accent-secondary, var(--info))"
                    />
                  </div>
                </WidgetShell>
              </div>

              {/* PVC list — solid surface */}
              <ResourceTable
                kind="pvcs"
                title="Persistent Volume Claims"
                createHref="/storage/pvcs?action=create"
                hideHeader
              />
            </div>
          )
          : isSnapshots
          ? <SnapshotList />
          : (
            <ResourceTable
              kind={kind}
              title={title}
              createHref={createHref}
              clusterScoped={clusterScoped}
              hideHeader
            />
          )}
      </div>
    </div>
  );
}
