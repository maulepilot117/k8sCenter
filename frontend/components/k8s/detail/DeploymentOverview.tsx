import type { Deployment, K8sResource } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";
import { InfoGrid } from "./InfoGrid.tsx";
import type { InfoGridItem } from "./InfoGrid.tsx";
import { ConditionsGrid, SectionTitle } from "./ConditionsGrid.tsx";

export function DeploymentOverview({ resource }: { resource: K8sResource }) {
  const dep = resource as Deployment;
  const containers = dep.spec?.template?.spec?.containers ?? [];

  // Determine status text from conditions
  const availableCondition = dep.status?.conditions?.find(
    (c) => c.type === "Available",
  );
  const statusText = availableCondition?.status === "True"
    ? "Available"
    : availableCondition?.reason ?? "Unavailable";
  const statusColor = availableCondition?.status === "True"
    ? "var(--success)"
    : "var(--warning)";

  const items: InfoGridItem[] = [
    {
      label: "Status",
      value: (
        <span style={{ display: "flex", alignItems: "center", gap: "6px" }}>
          <span
            style={{
              width: "7px",
              height: "7px",
              borderRadius: "50%",
              background: statusColor,
              boxShadow: availableCondition?.status === "True"
                ? "0 0 6px rgba(0, 230, 118, 0.4)"
                : undefined,
            }}
          />
          {statusText}
        </span>
      ),
    },
    {
      label: "Replicas",
      value: `${dep.status?.readyReplicas ?? 0}/${
        dep.spec?.replicas ?? 0
      } ready`,
    },
    { label: "Namespace", value: dep.metadata.namespace ?? "-" },
    { label: "Strategy", value: dep.spec?.strategy?.type ?? "-" },
    {
      label: "Created",
      value: dep.metadata.creationTimestamp
        ? `${dep.metadata.creationTimestamp} (${
          age(dep.metadata.creationTimestamp)
        })`
        : "-",
    },
    {
      label: "Revision",
      value: `#${
        dep.metadata.annotations?.["deployment.kubernetes.io/revision"] ?? "-"
      }`,
    },
    {
      label: "Selector",
      value: (
        <span style={{ fontSize: "11px" }}>
          {Object.entries(dep.spec?.selector?.matchLabels ?? {})
            .map(([k, v]) => `${k}=${v}`)
            .join(", ") || "-"}
        </span>
      ),
    },
    {
      label: "Image",
      value: (
        <span style={{ fontSize: "11px" }}>
          {containers[0]?.image ?? "-"}
        </span>
      ),
    },
  ];

  return (
    <div>
      <InfoGrid items={items} />

      {dep.status?.conditions && (
        <ConditionsGrid conditions={dep.status.conditions} />
      )}

      {containers.length > 0 && (
        <div style={{ marginBottom: "20px" }}>
          <SectionTitle title="Containers" />
          {containers.map((c) => {
            // deno-lint-ignore no-explicit-any
            const container = c as any;
            const ports = container.ports as
              | { containerPort?: number; protocol?: string }[]
              | undefined;
            const portsStr = ports && ports.length > 0
              ? ports.map((p) =>
                `${p.containerPort ?? "?"}/${p.protocol ?? "TCP"}`
              ).join(", ")
              : "-";
            const cpuReq = c.resources?.requests?.cpu ?? "-";
            const cpuLim = c.resources?.limits?.cpu ?? "-";
            const memReq = c.resources?.requests?.memory ?? "-";
            const memLim = c.resources?.limits?.memory ?? "-";

            return (
              <div
                key={c.name}
                style={{
                  background: "var(--bg-surface)",
                  border: "1px solid var(--border-primary)",
                  borderRadius: "var(--radius)",
                  padding: "14px",
                  marginBottom: "10px",
                }}
              >
                {/* Header */}
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    marginBottom: "10px",
                  }}
                >
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      fontSize: "13px",
                      fontWeight: 500,
                      color: "var(--accent)",
                    }}
                  >
                    {c.name}
                  </span>
                  <span
                    style={{
                      padding: "2px 8px",
                      borderRadius: "10px",
                      fontSize: "10px",
                      fontWeight: 500,
                      textTransform: "uppercase",
                      letterSpacing: "0.05em",
                      background: "var(--success-dim)",
                      color: "var(--success)",
                    }}
                  >
                    Running
                  </span>
                </div>
                {/* 2x2 details */}
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(2, 1fr)",
                    gap: "8px",
                  }}
                >
                  <ContainerDetail label="Image" value={c.image} />
                  <ContainerDetail label="Ports" value={portsStr} />
                  <ContainerDetail
                    label="CPU Request/Limit"
                    value={`${cpuReq} / ${cpuLim}`}
                  />
                  <ContainerDetail
                    label="Memory Request/Limit"
                    value={`${memReq} / ${memLim}`}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function ContainerDetail(
  { label, value }: { label: string; value: string },
) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
      <span
        style={{
          fontSize: "10px",
          color: "var(--text-muted)",
          textTransform: "uppercase",
          letterSpacing: "0.05em",
        }}
      >
        {label}
      </span>
      <span
        style={{
          fontSize: "12px",
          fontFamily: "var(--font-mono)",
          color: "var(--text-secondary)",
        }}
      >
        {value}
      </span>
    </div>
  );
}
