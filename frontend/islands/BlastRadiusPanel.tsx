import type { Signal } from "@preact/signals";
import { KIND_ROUTE_MAP } from "@/lib/types/diagnostics.ts";

export interface AffectedResource {
  kind: string;
  name: string;
  health: string;
  impact: string;
}

interface BlastRadiusPanelProps {
  directlyAffected: Signal<AffectedResource[]>;
  potentiallyAffected: Signal<AffectedResource[]>;
  namespace: string;
}

function healthColor(health: string): string {
  switch (health) {
    case "healthy":
      return "var(--success)";
    case "degraded":
      return "var(--warning)";
    case "failing":
      return "var(--error)";
    default:
      return "var(--text-muted)";
  }
}

function healthBg(health: string): string {
  switch (health) {
    case "healthy":
      return "var(--success-dim)";
    case "degraded":
      return "var(--warning-dim)";
    case "failing":
      return "var(--error-dim)";
    default:
      return "var(--bg-elevated)";
  }
}

function detailHref(kind: string, name: string, namespace: string): string {
  const route = KIND_ROUTE_MAP[kind] ?? kind.toLowerCase() + "s";
  // Most resources live under /workloads; services under /networking
  const section =
    kind === "Service" || kind === "Ingress" || kind === "Endpoints"
      ? "networking"
      : kind === "ConfigMap" || kind === "Secret"
      ? "config"
      : "workloads";
  return `/${section}/${route}/${namespace}/${name}`;
}

function ResourceRow(
  { resource, namespace }: { resource: AffectedResource; namespace: string },
) {
  return (
    <a
      href={detailHref(resource.kind, resource.name, namespace)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: "10px",
        padding: "10px 14px",
        borderBottom: "1px solid var(--border-primary)",
        textDecoration: "none",
        color: "inherit",
      }}
    >
      <span
        style={{
          fontSize: "11px",
          fontWeight: 600,
          padding: "2px 6px",
          borderRadius: "var(--radius-sm)",
          background: "var(--bg-elevated)",
          color: "var(--text-secondary)",
          minWidth: "60px",
          textAlign: "center",
        }}
      >
        {resource.kind}
      </span>
      <span
        style={{
          fontSize: "13px",
          fontWeight: 500,
          color: "var(--accent)",
          flex: 1,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {resource.name}
      </span>
      <span
        style={{
          fontSize: "11px",
          fontWeight: 500,
          padding: "2px 8px",
          borderRadius: "var(--radius-sm)",
          background: healthBg(resource.health),
          color: healthColor(resource.health),
        }}
      >
        {resource.health}
      </span>
      <span
        style={{
          fontSize: "12px",
          color: "var(--text-muted)",
          maxWidth: "140px",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {resource.impact}
      </span>
    </a>
  );
}

function Section(
  { title, borderColor, resources, namespace }: {
    title: string;
    borderColor: string;
    resources: AffectedResource[];
    namespace: string;
  },
) {
  return (
    <div
      style={{
        border: "1px solid var(--border-primary)",
        borderLeft: `3px solid ${borderColor}`,
        borderRadius: "var(--radius)",
        background: "var(--bg-surface)",
        overflow: "hidden",
      }}
    >
      <div
        style={{
          padding: "10px 14px",
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-secondary)",
          textTransform: "uppercase",
          letterSpacing: "0.05em",
          borderBottom: "1px solid var(--border-primary)",
        }}
      >
        {title} ({resources.length})
      </div>
      {resources.length === 0
        ? (
          <div
            style={{
              padding: "16px 14px",
              fontSize: "13px",
              color: "var(--text-muted)",
            }}
          >
            No affected resources
          </div>
        )
        : resources.map((r) => (
          <ResourceRow
            key={`${r.kind}-${r.name}`}
            resource={r}
            namespace={namespace}
          />
        ))}
    </div>
  );
}

export default function BlastRadiusPanel(
  { directlyAffected, potentiallyAffected, namespace }: BlastRadiusPanelProps,
) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
      <Section
        title="Directly Affected"
        borderColor="var(--error)"
        resources={directlyAffected.value}
        namespace={namespace}
      />
      <Section
        title="Potentially Affected"
        borderColor="var(--warning)"
        resources={potentiallyAffected.value}
        namespace={namespace}
      />
    </div>
  );
}
