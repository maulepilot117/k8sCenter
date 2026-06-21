import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import SnapshotList from "@/islands/SnapshotList.tsx";
import { SummaryRing } from "@/components/ui/SummaryRing.tsx";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import Donut from "@/components/charts/Donut.tsx";
import BarRow from "@/components/charts/BarRow.tsx";

interface SummaryData {
  totalPVCs: number;
  boundPVCs: number;
  pendingPVCs: number;
  totalPVs: number;
  storageClasses: number;
}

const EMPTY_SUMMARY: SummaryData = {
  totalPVCs: 0,
  boundPVCs: 0,
  pendingPVCs: 0,
  totalPVs: 0,
  storageClasses: 0,
};

const storageSection = DOMAIN_SECTIONS.find((s) => s.id === "storage")!;

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
  const summary = useSignal<SummaryData>(EMPTY_SUMMARY);
  const loading = useSignal(true);
  const namespace = selectedNamespace.value;

  useEffect(() => {
    if (!IS_BROWSER) return;

    loading.value = true;

    const nsPath = namespace && namespace !== "all" ? `/${namespace}` : "";

    const fetchSummary = async () => {
      try {
        const nsParam = namespace && namespace !== "all"
          ? `?namespace=${encodeURIComponent(namespace)}`
          : "";

        const [countsRes, pvcsRes] = await Promise.all([
          apiGet<Record<string, number>>(
            `/v1/resources/counts${nsParam}`,
          ),
          apiGet<
            Array<{
              status?: { phase?: string };
            }>
          >(`/v1/resources/pvcs${nsPath}?limit=500`),
        ]);

        const countsData = countsRes.data ?? {};

        const pvcs = pvcsRes.data ?? [];
        const totalPVCs = countsData["persistentvolumeclaims"] ??
          countsData["pvcs"] ?? pvcs.length;
        let boundPVCs = 0;
        let pendingPVCs = 0;

        for (const pvc of pvcs) {
          const phase = pvc.status?.phase;
          if (phase === "Bound") boundPVCs++;
          else if (phase === "Pending") pendingPVCs++;
        }

        const totalPVs = countsData["persistentvolumes"] ??
          countsData["pvs"] ?? 0;
        const storageClasses = countsData["storageclasses"] ?? 0;

        summary.value = {
          totalPVCs,
          boundPVCs,
          pendingPVCs,
          totalPVs,
          storageClasses,
        };
      } catch {
        summary.value = EMPTY_SUMMARY;
      } finally {
        loading.value = false;
      }
    };

    fetchSummary();
  }, [namespace]);

  const { kind, title, createHref, clusterScoped, isOverview, isSnapshots } =
    resolveTab(currentPath);
  const s = summary.value;

  const summaryCards = [
    {
      label: "Total PVCs",
      value: s.totalPVCs,
      displayValue: String(s.totalPVCs),
      max: Math.max(s.totalPVCs, 1),
      ringValue: s.totalPVCs,
      color: "var(--accent)",
    },
    {
      label: "Bound PVCs",
      value: s.boundPVCs,
      displayValue: String(s.boundPVCs),
      max: Math.max(s.totalPVCs, 1),
      ringValue: s.boundPVCs,
      color: "var(--success)",
    },
    {
      label: "Pending PVCs",
      value: s.pendingPVCs,
      displayValue: String(s.pendingPVCs),
      max: Math.max(s.totalPVCs, 1),
      ringValue: s.pendingPVCs,
      color: "var(--warning)",
    },
    {
      label: "Total PVs",
      value: s.totalPVs,
      displayValue: String(s.totalPVs),
      max: Math.max(s.totalPVs, 1),
      ringValue: s.totalPVs,
      color: "var(--accent)",
    },
    {
      label: "Storage Classes",
      value: s.storageClasses,
      displayValue: String(s.storageClasses),
      max: Math.max(s.storageClasses, 1),
      ringValue: s.storageClasses,
      color: "var(--accent-secondary)",
    },
  ];

  // Donut segments for PVC phase breakdown (used on Overview)
  const pvcDonutSegments = [
    {
      value: s.boundPVCs,
      color: "var(--success)",
      label: "Bound",
    },
    {
      value: s.pendingPVCs,
      color: "var(--warning)",
      label: "Pending",
    },
    {
      value: Math.max(0, s.totalPVCs - s.boundPVCs - s.pendingPVCs),
      color: "var(--error)",
      label: "Lost",
    },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Page header \u2014 24/700 per archetype spec */}
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
            {isOverview ? "Storage" : title}
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            Manage persistent volumes, claims, storage classes, and snapshots
          </p>
        </div>
        <div style={{ display: "flex", gap: "8px", flexShrink: 0 }}>
          <a
            href="/storage/pvcs/new"
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
              textDecoration: "none",
              border: "none",
              cursor: "pointer",
              whiteSpace: "nowrap",
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
          </a>
          <a
            href="/tools/storageclass-wizard"
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
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
              whiteSpace: "nowrap",
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
          </a>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={flattenGroups(storageSection)} currentPath={currentPath} />

      {/* Summary strip \u2014 WidgetShell (glass) cards per dashboard archetype */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(170px, 1fr))",
          gap: "var(--grid-gap, 12px)",
          marginBottom: "20px",
        }}
      >
        {summaryCards.map((card) => (
          <WidgetShell key={card.label} padding={14}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "12px",
              }}
            >
              <SummaryRing
                value={loading.value ? 0 : card.ringValue}
                max={card.max}
                size={40}
                color={card.color}
              />
              <div style={{ minWidth: 0 }}>
                <div
                  style={{
                    fontSize: "11px",
                    fontWeight: 600,
                    letterSpacing: "0.05em",
                    textTransform: "uppercase",
                    color: "var(--text-muted)",
                    marginBottom: "2px",
                  }}
                >
                  {card.label}
                </div>
                <div
                  style={{
                    fontSize: "20px",
                    fontWeight: 700,
                    fontFamily: "var(--font-mono)",
                    color: card.color,
                    lineHeight: 1.1,
                    fontVariantNumeric: "tabular-nums",
                  }}
                >
                  {loading.value ? "\u2014" : card.displayValue}
                </div>
              </div>
            </div>
          </WidgetShell>
        ))}
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
              {/* Overview charts row \u2014 glass widgets */}
              <div
                style={{
                  display: "flex",
                  flexWrap: "wrap",
                  gap: "var(--grid-gap, 12px)",
                }}
              >
                {/* PVC Phase donut */}
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
                      segments={loading.value
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
                            {loading.value ? "\u2014" : s.totalPVCs}
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
                      {pvcDonutSegments.map((seg) => (
                        <div
                          key={seg.label}
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
                              background: seg.color,
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
                            {seg.label}
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
                            {loading.value ? "\u2014" : seg.value}
                          </span>
                        </div>
                      ))}
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
                      value={loading.value ? 0 : s.boundPVCs}
                      max={Math.max(s.totalPVCs, 1)}
                      suffix={loading.value
                        ? "\u2014"
                        : `${s.boundPVCs}/${s.totalPVCs}`}
                      color="var(--success)"
                    />
                    <BarRow
                      label="Volumes"
                      value={loading.value ? 0 : s.totalPVs}
                      max={Math.max(s.totalPVs, 1)}
                      suffix={loading.value ? "\u2014" : String(s.totalPVs)}
                      color="var(--accent)"
                    />
                    <BarRow
                      label="StorageClasses"
                      value={loading.value ? 0 : s.storageClasses}
                      max={Math.max(s.storageClasses, 1)}
                      suffix={loading.value
                        ? "\u2014"
                        : String(s.storageClasses)}
                      color="var(--accent-secondary, var(--info))"
                    />
                  </div>
                </WidgetShell>
              </div>

              {/* PVC list \u2014 solid surface */}
              <ResourceTable
                kind="pvcs"
                title="Persistent Volume Claims"
                createHref="/storage/pvcs/new"
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
