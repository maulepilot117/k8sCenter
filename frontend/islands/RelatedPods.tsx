import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { age } from "@/lib/format.ts";

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

function aggregateResources(
  containers: PodInfo["spec"]["containers"],
): {
  cpuRequest: string;
  cpuLimit: string;
  memRequest: string;
  memLimit: string;
  hasResources: boolean;
} {
  let cpuReq = "";
  let cpuLim = "";
  let memReq = "";
  let memLim = "";
  let hasResources = false;

  for (const c of containers) {
    if (c.resources) {
      if (c.resources.requests?.cpu) {
        cpuReq = cpuReq
          ? cpuReq + " + " + c.resources.requests.cpu
          : c.resources.requests.cpu;
        hasResources = true;
      }
      if (c.resources.limits?.cpu) {
        cpuLim = cpuLim
          ? cpuLim + " + " + c.resources.limits.cpu
          : c.resources.limits.cpu;
        hasResources = true;
      }
      if (c.resources.requests?.memory) {
        memReq = memReq
          ? memReq + " + " + c.resources.requests.memory
          : c.resources.requests.memory;
        hasResources = true;
      }
      if (c.resources.limits?.memory) {
        memLim = memLim
          ? memLim + " + " + c.resources.limits.memory
          : c.resources.limits.memory;
        hasResources = true;
      }
    }
  }

  return {
    cpuRequest: cpuReq || "-",
    cpuLimit: cpuLim || "-",
    memRequest: memReq || "-",
    memLimit: memLim || "-",
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
        <div class="h-5 w-5 animate-spin rounded-full border-2 border-border-primary border-t-brand" />
      </div>
    );
  }

  if (error.value) {
    return (
      <div class="rounded-md bg-danger-dim px-4 py-3 text-sm text-danger">
        {error.value}
      </div>
    );
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
        const { cpuRequest, cpuLimit, memRequest, memLimit, hasResources } =
          aggregateResources(pod.spec?.containers ?? []);

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
                        fontSize: "10px",
                        color: "var(--text-muted)",
                        marginBottom: "2px",
                      }}
                    >
                      CPU
                    </div>
                    <div
                      style={{
                        fontSize: "11px",
                        fontFamily: "var(--font-mono)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      {cpuRequest} / {cpuLimit}
                    </div>
                  </div>
                  <div>
                    <div
                      style={{
                        fontSize: "10px",
                        color: "var(--text-muted)",
                        marginBottom: "2px",
                      }}
                    >
                      Memory
                    </div>
                    <div
                      style={{
                        fontSize: "11px",
                        fontFamily: "var(--font-mono)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      {memRequest} / {memLimit}
                    </div>
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
