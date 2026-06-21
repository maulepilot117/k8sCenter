import { selectedNamespace } from "@/lib/namespace.ts";
import { getCount, resourceCounts } from "@/lib/resource-counts.ts";
import ResourceTable from "@/islands/ResourceTable.tsx";

function resolveTab(currentPath: string): {
  kind: string;
  title: string;
  createHref?: string;
} {
  const path = currentPath.replace(/\/$/, "");

  if (path === "/config/configmaps") {
    return {
      kind: "configmaps",
      title: "ConfigMaps",
      createHref: "/config/configmaps/new",
    };
  }

  if (path === "/config/secrets") {
    return {
      kind: "secrets",
      title: "Secrets",
      createHref: "/config/secrets/new",
    };
  }

  if (path === "/config/serviceaccounts") {
    return {
      kind: "serviceaccounts",
      title: "Service Accounts",
    };
  }

  if (path === "/config/resourcequotas") {
    return {
      kind: "resourcequotas",
      title: "Resource Quotas",
    };
  }

  if (path === "/config/limitranges") {
    return {
      kind: "limitranges",
      title: "Limit Ranges",
    };
  }

  // Default (landing /config): show ConfigMaps
  return {
    kind: "configmaps",
    title: "ConfigMaps",
    createHref: "/config/configmaps/new",
  };
}

export default function ConfigDashboard(
  { currentPath }: { currentPath: string },
) {
  // Reading selectedNamespace.value here wires reactivity — the shared
  // resource-counts store re-fetches when namespace changes.
  const _ns = selectedNamespace.value;

  const { kind, title, createHref } = resolveTab(currentPath);

  // Subtitle derived from live counts — no invented data.
  const total = getCount(kind) ?? 0;
  const countsReady = resourceCounts.value !== null;

  const subtitle = countsReady
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
        <div style={{ display: "flex", gap: "8px", alignItems: "center" }}>
          <a
            href="/config/secrets/new"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: "6px",
              padding: "8px 14px",
              fontSize: "13px",
              fontWeight: 500,
              color: "var(--text-muted)",
              background: "transparent",
              borderRadius: "9px",
              textDecoration: "none",
              border: "1px solid var(--border-primary)",
              cursor: "pointer",
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
            New Secret
          </a>
          <a
            href="/config/configmaps/new"
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
            New ConfigMap
          </a>
        </div>
      </div>

      {/* Content area */}
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
