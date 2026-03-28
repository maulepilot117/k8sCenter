import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { age } from "@/lib/format.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";

interface RelatedPodsProps {
  namespace: string;
  /** Label selector to find pods (e.g.,"app=nginx") */
  labelSelector: string;
  /** Parent resource name for display */
  parentName: string;
}

interface PodInfo {
  metadata: {
    name: string;
    namespace: string;
    creationTimestamp: string;
  };
  spec: {
    nodeName: string;
    containers: {
      name: string;
      resources?: {
        requests?: Record<string, string>;
        limits?: Record<string, string>;
      };
    }[];
  };
  status: {
    phase: string;
    podIP?: string;
    containerStatuses?: {
      name: string;
      ready: boolean;
      restartCount: number;
    }[];
  };
}

function podStatusInfo(
  pod: PodInfo,
): { text: string; color: string } {
  const phase = pod.status?.phase ?? "Unknown";
  if (phase === "Running") {
    const allReady = pod.status?.containerStatuses?.every((c) => c.ready);
    return allReady
      ? { text: "Running", color: "var(--success)" }
      : { text: "Not Ready", color: "var(--warning)" };
  }
  if (phase === "Succeeded") return { text: "Completed", color: "var(--info)" };
  if (phase === "Failed") return { text: "Failed", color: "var(--error)" };
  if (phase === "Pending") return { text: "Pending", color: "var(--warning)" };
  return { text: phase, color: "var(--text-muted)" };
}

function parseCpuMillis(val: string | undefined): number {
  if (!val) return 0;
  if (val.endsWith("m")) return parseInt(val);
  return parseFloat(val) * 1000;
}

function parseMemMi(val: string | undefined): number {
  if (!val) return 0;
  if (val.endsWith("Mi")) return parseInt(val);
  if (val.endsWith("Gi")) return parseFloat(val) * 1024;
  if (val.endsWith("Ki")) return parseFloat(val) / 1024;
  return parseInt(val) / (1024 * 1024);
}

function formatCpu(millis: number): string {
  if (millis === 0) return "-";
  if (millis >= 1000 && millis % 1000 === 0) return `${millis / 1000}`;
  return `${Math.round(millis)}m`;
}

function formatMem(mi: number): string {
  if (mi === 0) return "-";
  if (mi >= 1024 && mi % 1024 === 0) return `${mi / 1024}Gi`;
  return `${Math.round(mi)}Mi`;
}

function aggregateResources(
  containers: PodInfo["spec"]["containers"],
): {
  cpuRequest: string;
  cpuLimit: string;
  memRequest: string;
  memLimit: string;
  cpuPercent: number;
  memPercent: number;
  hasResources: boolean;
} {
  let totalCpuReq = 0,
    totalCpuLim = 0,
    totalMemReq = 0,
    totalMemLim = 0;
  let hasResources = false;

  for (const c of containers) {
    if (c.resources) {
      totalCpuReq += parseCpuMillis(c.resources.requests?.cpu);
      totalCpuLim += parseCpuMillis(c.resources.limits?.cpu);
      totalMemReq += parseMemMi(c.resources.requests?.memory);
      totalMemLim += parseMemMi(c.resources.limits?.memory);
      if (
        c.resources.requests?.cpu || c.resources.limits?.cpu ||
        c.resources.requests?.memory || c.resources.limits?.memory
      ) {
        hasResources = true;
      }
    }
  }

  return {
    cpuRequest: formatCpu(totalCpuReq),
    cpuLimit: formatCpu(totalCpuLim),
    memRequest: formatMem(totalMemReq),
    memLimit: formatMem(totalMemLim),
    cpuPercent: totalCpuLim > 0
      ? Math.min(100, (totalCpuReq / totalCpuLim) * 100)
      : 0,
    memPercent: totalMemLim > 0
      ? Math.min(100, (totalMemReq / totalMemLim) * 100)
      : 0,
    hasResources,
  };
}

export default function RelatedPods(
  { namespace, labelSelector, parentName }: RelatedPodsProps,
) {
  const pods = useSignal<PodInfo[]>([]);
  const loading = useSignal(true);
  const error = useSignal("");

  useEffect(() => {
    if (!IS_BROWSER) return;
    fetchPods();
  }, [namespace, labelSelector]);

  async function fetchPods() {
    loading.value = true;
    error.value = "";
    try {
      const res = await apiGet<PodInfo[]>(
        `/v1/resources/pods/${namespace}?labelSelector=${
          encodeURIComponent(labelSelector)
        }`,
      );
      pods.value = Array.isArray(res.data) ? res.data : [];
    } catch (err) {
      error.value = err instanceof Error ? err.message : "Failed to load pods";
    } finally {
      loading.value = false;
    }
  }

  if (!IS_BROWSER) return null;

  if (loading.value) {
    return (
      <div class="flex justify-center py-8">
        <Spinner size="sm" class="text-brand" />
      </div>
    );
  }

  if (error.value) {
    return <ErrorBanner message={error.value} />;
  }

  if (pods.value.length === 0) {
    return (
      <div class="py-8 text-center text-sm text-text-muted">
        No pods found for {parentName}
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: "16px",
        }}
      >
        <div style={{ fontSize: "16px", fontWeight: 600 }}>Related Pods</div>
        <span
          style={{
            fontSize: "12px",
            color: "var(--text-muted)",
            fontFamily: "var(--font-mono)",
            padding: "3px 8px",
            background: "var(--bg-elevated)",
            borderRadius: "10px",
          }}
        >
          {pods.value.length} pods
        </span>
      </div>

      {/* Pod cards */}
      {pods.value.map((pod) => {
        const restarts = pod.status?.containerStatuses?.reduce(
          (sum, c) => sum + (c.restartCount || 0),
          0,
        ) ?? 0;
        const { text: statusText, color: statusColor } = podStatusInfo(pod);
        const {
          cpuRequest,
          cpuLimit,
          memRequest,
          memLimit,
          cpuPercent,
          memPercent,
          hasResources,
        } = aggregateResources(pod.spec?.containers ?? []);

        return (
          <a
            key={pod.metadata.name}
            href={`/workloads/pods/${pod.metadata.namespace}/${pod.metadata.name}`}
            style={{
              textDecoration: "none",
              color: "inherit",
              display: "block",
            }}
          >
            <div
              style={{
                background: "var(--bg-surface)",
                border: "1px solid var(--border-primary)",
                borderRadius: "var(--radius)",
                padding: "14px",
                marginBottom: "8px",
                cursor: "pointer",
                transition: "all 0.15s ease",
              }}
              onMouseOver={(e) => {
                (e.currentTarget as HTMLElement).style.borderColor =
                  "var(--accent)";
              }}
              onMouseOut={(e) => {
                (e.currentTarget as HTMLElement).style.borderColor =
                  "var(--border-primary)";
              }}
            >
              {/* Header: name + status */}
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  marginBottom: "8px",
                }}
              >
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: "13px",
                    fontWeight: 500,
                    color: "var(--accent)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    maxWidth: "70%",
                  }}
                >
                  {pod.metadata.name}
                </span>
                <span
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: "5px",
                    fontSize: "11px",
                    fontWeight: 500,
                    color: statusColor,
                    flexShrink: 0,
                  }}
                >
                  <span
                    style={{
                      width: "6px",
                      height: "6px",
                      borderRadius: "50%",
                      background: statusColor,
                    }}
                  />
                  {statusText}
                </span>
              </div>

              {/* Meta row: Node, Age, Restarts */}
              <div
                style={{
                  display: "flex",
                  gap: "16px",
                  fontSize: "11px",
                  color: "var(--text-muted)",
                  flexWrap: "wrap",
                }}
              >
                <span>
                  <span style={{ color: "var(--text-muted)" }}>Node:</span>{" "}
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-secondary)",
                    }}
                  >
                    {pod.spec?.nodeName ?? "-"}
                  </span>
                </span>
                <span>
                  <span>Age:</span>{" "}
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-secondary)",
                    }}
                  >
                    {age(pod.metadata.creationTimestamp)}
                  </span>
                </span>
                <span>
                  <span>Restarts:</span>{" "}
                  <span
                    style={{
                      fontFamily: "var(--font-mono)",
                      color: "var(--text-secondary)",
                    }}
                  >
                    {restarts}
                  </span>
                </span>
              </div>

              {/* Resource requests/limits */}
              {hasResources && (
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: "8px",
                    marginTop: "10px",
                  }}
                >
                  <div>
                    <div
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        fontSize: "10px",
                        marginBottom: "3px",
                      }}
                    >
                      <span style={{ color: "var(--text-muted)" }}>CPU</span>
                      <span
                        style={{
                          fontFamily: "var(--font-mono)",
                          color: "var(--text-secondary)",
                          fontSize: "11px",
                        }}
                      >
                        {cpuRequest} / {cpuLimit}
                      </span>
                    </div>
                    {cpuPercent > 0 && (
                      <div
                        style={{
                          height: "3px",
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
                            background: "var(--accent)",
                          }}
                        />
                      </div>
                    )}
                  </div>
                  <div>
                    <div
                      style={{
                        display: "flex",
                        justifyContent: "space-between",
                        fontSize: "10px",
                        marginBottom: "3px",
                      }}
                    >
                      <span style={{ color: "var(--text-muted)" }}>Memory</span>
                      <span
                        style={{
                          fontFamily: "var(--font-mono)",
                          color: "var(--text-secondary)",
                          fontSize: "11px",
                        }}
                      >
                        {memRequest} / {memLimit}
                      </span>
                    </div>
                    {memPercent > 0 && (
                      <div
                        style={{
                          height: "3px",
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
                            background: "var(--accent-secondary)",
                          }}
                        />
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          </a>
        );
      })}
    </div>
  );
}
