import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS } from "@/lib/constants.ts";
import SubNav from "@/islands/SubNav.tsx";
import ResourceTable from "@/islands/ResourceTable.tsx";
import FlowViewer from "@/islands/FlowViewer.tsx";
import CniStatus from "@/islands/CniStatus.tsx";
import { SummaryRing } from "@/components/ui/SummaryRing.tsx";

interface SummaryData {
  totalServices: number;
  totalIngresses: number;
  networkPolicies: number;
  ciliumPolicies: number;
  endpoints: number;
  endpointSlices: number;
}

const EMPTY_SUMMARY: SummaryData = {
  totalServices: 0,
  totalIngresses: 0,
  networkPolicies: 0,
  ciliumPolicies: 0,
  endpoints: 0,
  endpointSlices: 0,
};

const networkSection = DOMAIN_SECTIONS.find((s) => s.id === "network")!;

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
  isFlows: boolean;
  isCni: boolean;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/networking/services") {
    return {
      kind: "services",
      title: "Services",
      createHref: "/networking/services/new",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/flows") {
    return { kind: "", title: "Flows", isFlows: true, isCni: false };
  }

  if (path === "/networking/ingresses") {
    return {
      kind: "ingresses",
      title: "Ingresses",
      createHref: "/networking/ingresses/new",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/networkpolicies") {
    return {
      kind: "networkpolicies",
      title: "Network Policies",
      createHref: "/networking/networkpolicies/new",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/cilium-policies") {
    return {
      kind: "ciliumnetworkpolicies",
      title: "Cilium Network Policies",
      createHref: "/networking/cilium-policies/new",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/endpoints") {
    return {
      kind: "endpoints",
      title: "Endpoints",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/endpointslices") {
    return {
      kind: "endpointslices",
      title: "EndpointSlices",
      isFlows: false,
      isCni: false,
    };
  }

  // Default (landing /networking): show CNI overview as the landing page
  return {
    kind: "",
    title: "Overview",
    isFlows: false,
    isCni: true,
  };
}

export default function NetworkingDashboard(
  { currentPath }: { currentPath: string },
) {
  const summary = useSignal<SummaryData>(EMPTY_SUMMARY);
  const loading = useSignal(true);
  const namespace = selectedNamespace.value;

  useEffect(() => {
    if (!IS_BROWSER) return;

    loading.value = true;

    const fetchSummary = async () => {
      try {
        const nsParam = namespace && namespace !== "all"
          ? `?namespace=${encodeURIComponent(namespace)}`
          : "";

        const countsRes = await apiGet<Record<string, number>>(
          `/v1/resources/counts${nsParam}`,
        );

        const countsData = countsRes.data ?? {};

        summary.value = {
          totalServices: countsData["services"] ?? 0,
          totalIngresses: countsData["ingresses"] ?? 0,
          networkPolicies: countsData["networkpolicies"] ?? 0,
          ciliumPolicies: countsData["ciliumnetworkpolicies"] ?? 0,
          endpoints: countsData["endpoints"] ?? 0,
          endpointSlices: countsData["endpointslices"] ?? 0,
        };
      } catch {
        summary.value = EMPTY_SUMMARY;
      } finally {
        loading.value = false;
      }
    };

    fetchSummary();
  }, [namespace]);

  const { kind, title, createHref, isFlows, isCni } = resolveTab(currentPath);
  const s = summary.value;

  const summaryCards = [
    {
      label: "Total Services",
      value: s.totalServices,
      displayValue: String(s.totalServices),
      max: Math.max(s.totalServices, 1),
      ringValue: s.totalServices,
      color: "var(--accent)",
    },
    {
      label: "Total Ingresses",
      value: s.totalIngresses,
      displayValue: String(s.totalIngresses),
      max: Math.max(s.totalIngresses, 1),
      ringValue: s.totalIngresses,
      color: "var(--accent-secondary)",
    },
    {
      label: "Network Policies",
      value: s.networkPolicies,
      displayValue: String(s.networkPolicies),
      max: Math.max(s.networkPolicies, 1),
      ringValue: s.networkPolicies,
      color: "var(--success)",
    },
    {
      label: "Cilium Policies",
      value: s.ciliumPolicies,
      displayValue: String(s.ciliumPolicies),
      max: Math.max(s.ciliumPolicies, 1),
      ringValue: s.ciliumPolicies,
      color: "var(--success)",
    },
    {
      label: "Endpoints",
      value: s.endpoints,
      displayValue: String(s.endpoints),
      max: Math.max(s.endpoints, 1),
      ringValue: s.endpoints,
      color: "var(--accent)",
    },
    {
      label: "EndpointSlices",
      value: s.endpointSlices,
      displayValue: String(s.endpointSlices),
      max: Math.max(s.endpointSlices, 1),
      ringValue: s.endpointSlices,
      color: "var(--accent)",
    },
  ];

  return (
    <div class="flex flex-col h-full">
      {/* Page header */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-text-primary">
            Network
          </h1>
          <p class="text-xs text-text-muted mt-0.5">
            Manage services, ingresses, network policies, and endpoints
          </p>
        </div>
        <div class="flex gap-2">
          <a
            href="/networking/ingresses/new"
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
            New Ingress
          </a>
          <a
            href="/networking/services/new"
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
            New Service
          </a>
        </div>
      </div>

      {/* Sub-navigation */}
      <SubNav tabs={networkSection.tabs ?? []} currentPath={currentPath} />

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
        {isFlows ? <FlowViewer /> : isCni ? <CniStatus /> : (
          <ResourceTable
            kind={kind}
            title={title}
            createHref={createHref}
            hideHeader
          />
        )}
      </div>
    </div>
  );
}
