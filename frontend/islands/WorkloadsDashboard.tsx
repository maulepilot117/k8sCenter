import { selectedNamespace } from "@/lib/namespace.ts";
import { DOMAIN_SECTIONS, flattenGroups } from "@/lib/constants.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

const workloadsSection = DOMAIN_SECTIONS.find((s) => s.id === "workloads")!;

function resolveKind(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
} {
  const tabs = flattenGroups(workloadsSection);
  for (const tab of tabs) {
    if (
      tab.href === currentPath ||
      (currentPath.startsWith(tab.href) &&
        currentPath[tab.href.length] === "/")
    ) {
      return {
        kind: tab.kind!,
        title: tab.label,
        createHref: `${tab.href}/new`,
      };
    }
  }
  // Default: Deployments
  return {
    kind: "deployments",
    title: "Deployments",
    createHref: "/workloads/deployments/new",
  };
}

export default function WorkloadsDashboard(
  { currentPath }: { currentPath: string },
) {
  // Trigger the shared counts fetch on mount/namespace change.
  // The actual fetch lives in resource-counts.ts (reactive effect);
  // reading selectedNamespace.value here is enough to wire the dependency.
  const _ns = selectedNamespace.value;

  const { kind, title, createHref } = resolveKind(currentPath);

  // Derive subtitle from live counts in the shared store.
  const total = getCount(kind) ?? 0;
  const countsReady = resourceCounts.value !== null;

  // Build subtitle: "N deployments" — omit degraded clause until we have
  // real degraded data (not invented). The resource table itself shows status.
  const subtitle = countsReady
    ? `${total} ${title.toLowerCase()}`
    : `Loading ${title.toLowerCase()}…`;

  return (
    <div class="flex flex-col h-full">
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
            {title}
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
            New {title.replace(/s$/, "")}
          </a>
        )}
      </div>

      {/* Resource table — solid surface, no backdrop-filter */}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
        <ResourceTable
          kind={kind}
          title={title}
          createHref={createHref}
          hideHeader
        />
      </div>
    </div>
  );
}
