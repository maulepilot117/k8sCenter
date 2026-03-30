import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import SnapshotList from "@/islands/SnapshotList.tsx";
import { SummaryRing } from "@/components/ui/SummaryRing.tsx";

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
          apiGet<{
            data: Array<{
              status?: { phase?: string };
            }>;
          }>(`/v1/resources/pvcs${nsPath}?limit=500`),
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

  return (
    <div class="flex flex-col h-full">
      {/* Page header */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-text-primary">
            Storage
          </h1>
          <p class="text-xs text-text-muted mt-0.5">
            Manage persistent volumes, claims, storage classes, and snapshots
          </p>
        </div>
        <div class="flex gap-2">
          <a
            href="/storage/pvcs/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "7px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--bg-base)",
              background: "var(--accent)",
              borderRadius: "6px",
              textDecoration: "none",
              border: "none",
              cursor: "pointer",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
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
              padding: "7px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-secondary)",
              background: "transparent",
              borderRadius: "6px",
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
            }}
          >
            <svg
              width="15"
              height="15"
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
      <SubNav tabs={storageSection.tabs ?? []} currentPath={currentPath} />

      {/* Summary strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
          gap: "var(--grid-gap, 12px)",
          marginBottom: "20px",
        }}
      >
        {summaryCards.map((card) => (
          <div
            key={card.label}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "12px",
              padding: "14px 16px",
              borderRadius: "10px",
              background: "var(--bg-surface)",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
              transition: "border-color 0.2s ease",
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
                  fontSize: "12px",
                  color: "var(--text-secondary)",
                }}
              >
                {card.label}
              </div>
              <div
                style={{
                  fontSize: "16px",
                  fontWeight: 600,
                  fontFamily: "var(--font-mono)",
                  color: card.color,
                }}
              >
                {loading.value ? "\u2014" : card.displayValue}
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {isOverview
          ? (
            <ResourceTable
              kind="pvcs"
              title="Persistent Volume Claims"
              createHref="/storage/pvcs/new"
              hideHeader
            />
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
