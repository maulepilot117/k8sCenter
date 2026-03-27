import type { Deployment, K8sResource } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";
import { InfoGrid } from "./InfoGrid.tsx";
import type { InfoGridItem } from "./InfoGrid.tsx";
import { ConditionsGrid, SectionTitle } from "./ConditionsGrid.tsx";

function parseCpuMillis(val: string | undefined): number {
  if (!val || val === "-") return 0;
  if (val.endsWith("m")) return parseInt(val);
  return parseFloat(val) * 1000;
}

function parseMemMi(val: string | undefined): number {
  if (!val || val === "-") return 0;
  if (val.endsWith("Mi")) return parseInt(val);
  if (val.endsWith("Gi")) return parseFloat(val) * 1024;
  if (val.endsWith("Ki")) return parseFloat(val) / 1024;
  return parseInt(val) / (1024 * 1024);
}

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

            const hasCpuBoth = c.resources?.requests?.cpu &&
              c.resources?.limits?.cpu;
            const hasMemBoth = c.resources?.requests?.memory &&
              c.resources?.limits?.memory;
            const showBars = hasCpuBoth || hasMemBoth;

            const cpuReqMillis = parseCpuMillis(c.resources?.requests?.cpu);
            const cpuLimMillis = parseCpuMillis(c.resources?.limits?.cpu);
            const cpuPercent = cpuLimMillis > 0
              ? Math.min(100, (cpuReqMillis / cpuLimMillis) * 100)
              : 0;

            const memReqMi = parseMemMi(c.resources?.requests?.memory);
            const memLimMi = parseMemMi(c.resources?.limits?.memory);
            const memPercent = memLimMi > 0
              ? Math.min(100, (memReqMi / memLimMi) * 100)
              : 0;

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
                {/* Usage bars */}
                {showBars && (
                  <div
                    style={{
                      display: "grid",
                      gridTemplateColumns: "1fr 1fr",
                      gap: "12px",
                      marginTop: "12px",
                    }}
                  >
                    {hasCpuBoth && (
                      <div>
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            fontSize: "11px",
                            marginBottom: "4px",
                          }}
                        >
                          <span style={{ color: "var(--text-muted)" }}>
                            CPU
                          </span>
                          <span
                            style={{
                              fontFamily: "var(--font-mono)",
                              color: "var(--text-secondary)",
                            }}
                          >
                            {cpuReq} / {cpuLim}
                          </span>
                        </div>
                        <div
                          style={{
                            height: "4px",
                            background: "var(--border-primary)",
                            borderRadius: "2px",
                            overflow: "hidden",
                          }}
                        >
                          <div
                            style={{
                              height: "100%",
                              borderRadius: "2px",
                              width: `${cpuPercent}%`,
                              background:
                                "linear-gradient(90deg, var(--accent), var(--success))",
                            }}
                          />
                        </div>
                      </div>
                    )}
                    {hasMemBoth && (
                      <div>
                        <div
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            fontSize: "11px",
                            marginBottom: "4px",
                          }}
                        >
                          <span style={{ color: "var(--text-muted)" }}>
                            Memory
                          </span>
                          <span
                            style={{
                              fontFamily: "var(--font-mono)",
                              color: "var(--text-secondary)",
                            }}
                          >
                            {memReq} / {memLim}
                          </span>
                        </div>
                        <div
                          style={{
                            height: "4px",
                            background: "var(--border-primary)",
                            borderRadius: "2px",
                            overflow: "hidden",
                          }}
                        >
                          <div
                            style={{
                              height: "100%",
                              borderRadius: "2px",
                              width: `${memPercent}%`,
                              background:
                                "linear-gradient(90deg, var(--accent-secondary), #FF79C6)",
                            }}
                          />
                        </div>
                      </div>
                    )}
                  </div>
                )}
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
