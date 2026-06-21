import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";
import FlowViewer from "@/islands/FlowViewer.tsx";
import NetworkOverview from "@/islands/NetworkOverview.tsx";
import ServiceWizard from "@/islands/ServiceWizard.tsx";
import IngressWizard from "@/islands/IngressWizard.tsx";
import NetworkPolicyWizard from "@/islands/NetworkPolicyWizard.tsx";

type WizardComponent =
  | (({ onClose }: { onClose: () => void }) => preact.JSX.Element)
  | null;

const WIZARD_MAP: Record<string, WizardComponent> = {
  services: ServiceWizard,
  ingresses: IngressWizard,
  networkpolicies: NetworkPolicyWizard,
};

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createLabel?: string;
  isFlows: boolean;
  isCni: boolean;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/networking/services") {
    return {
      kind: "services",
      title: "Services",
      createLabel: "New Service",
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
      createLabel: "New Ingress",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/networkpolicies") {
    return {
      kind: "networkpolicies",
      title: "Network Policies",
      createLabel: "New Network Policy",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/cilium-policies") {
    return {
      kind: "ciliumnetworkpolicies",
      title: "Cilium Network Policies",
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

  // Default (landing /networking): show CNI overview
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
  const _ns = selectedNamespace.value;

  const { kind, title, createLabel, isFlows, isCni } = resolveTab(currentPath);
  const WizardComponent = WIZARD_MAP[kind] ?? null;

  // Wizard modal signal — open=true shows the floating WizardShell
  const wizardOpen = useSignal(false);

  // Auto-open wizard when navigated with ?action=create (e.g. from CommandPalette)
  useEffect(() => {
    if (!IS_BROWSER) return;
    if (
      new URL(globalThis.location.href).searchParams.get("action") ===
        "create" && WizardComponent
    ) {
      wizardOpen.value = true;
      const url = new URL(globalThis.location.href);
      url.searchParams.delete("action");
      globalThis.history.replaceState({}, "", url.toString());
    }
  }, []);

  const total = kind ? (getCount(kind) ?? 0) : 0;
  const countsReady = resourceCounts.value !== null;
  const pageTitle = isCni ? "Network" : title;

  const subtitle = isCni
    ? "Manage services, ingresses, network policies, and endpoints"
    : isFlows
    ? "Live network flow visualization"
    : countsReady
    ? `${total} ${title.toLowerCase()}`
    : `Loading ${title.toLowerCase()}…`;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      {/* Floating wizard modal — rendered above everything when open */}
      {wizardOpen.value && WizardComponent && (
        <WizardComponent onClose={() => (wizardOpen.value = false)} />
      )}

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
        {WizardComponent
          ? (
            <button
              type="button"
              onClick={() => (wizardOpen.value = true)}
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
                flexShrink: 0,
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
              {createLabel ?? `New ${title.replace(/s$/, "")}`}
            </button>
          )
          : null}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {isFlows ? <FlowViewer /> : isCni ? <NetworkOverview /> : (
          <ResourceTable
            kind={kind}
            title={title}
            hideHeader
          />
        )}
      </div>
    </div>
  );
}
