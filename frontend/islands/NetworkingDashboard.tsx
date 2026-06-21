import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";
import FlowViewer from "@/islands/FlowViewer.tsx";
import NetworkOverview from "@/islands/NetworkOverview.tsx";

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
  createLabel?: string;
  isFlows: boolean;
  isCni: boolean;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/networking/services") {
    return {
      kind: "services",
      title: "Services",
      createHref: "/networking/services/new",
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
      createHref: "/networking/ingresses/new",
      createLabel: "New Ingress",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/networkpolicies") {
    return {
      kind: "networkpolicies",
      title: "Network Policies",
      createHref: "/networking/networkpolicies/new",
      createLabel: "New Network Policy",
      isFlows: false,
      isCni: false,
    };
  }

  if (path === "/networking/cilium-policies") {
    return {
      kind: "ciliumnetworkpolicies",
      title: "Cilium Network Policies",
      createHref: "/networking/cilium-policies/new",
      createLabel: "New Cilium Policy",
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
  // Reading selectedNamespace.value here wires reactivity — the shared
  // resource-counts store re-fetches when namespace changes.
  const _ns = selectedNamespace.value;

  const { kind, title, createHref, createLabel, isFlows, isCni } = resolveTab(
    currentPath,
  );

  // Subtitle derived from live counts — no invented data.
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
        {createHref && (
          <a
            href={createHref}
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
              flexShrink: 0,
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
          </a>
        )}
      </div>

      {/* Content area */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        {isFlows ? <FlowViewer /> : isCni ? <NetworkOverview /> : (
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
