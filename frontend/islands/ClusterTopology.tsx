import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";

interface K8sMetadata {
  name: string;
  namespace?: string;
  uid?: string;
  labels?: Record<string, string>;
  ownerReferences?: { kind: string; name: string }[];
}

interface K8sNode {
  metadata: K8sMetadata;
  status?: { conditions?: { type: string; status: string }[] };
}

interface K8sService {
  metadata: K8sMetadata;
  spec?: { selector?: Record<string, string> };
}

interface K8sPod {
  metadata: K8sMetadata;
  spec?: {
    nodeName?: string;
    volumes?: {
      name: string;
      persistentVolumeClaim?: { claimName: string };
    }[];
  };
  status?: { phase?: string };
}

interface K8sPVC {
  metadata: K8sMetadata;
  spec?: { volumeName?: string };
  status?: { phase?: string };
}

interface TopoNode {
  id: string;
  label: string;
  abbr: string;
  kind: "node" | "service" | "pod" | "pvc";
  namespace?: string;
  x: number;
  y: number;
  size: number;
  health: "healthy" | "warning" | "error";
}

interface TopoEdge {
  from: string;
  to: string;
  color: string;
}

const MAX_NODES = 10;
const MAX_SERVICES = 10;
const MAX_PODS = 15;
const MAX_PVCS = 10;

function matchesSelector(
  selector: Record<string, string> | undefined,
  labels: Record<string, string> | undefined,
): boolean {
  if (!selector || !labels) return false;
  return Object.entries(selector).every(([k, v]) => labels[k] === v);
}

function getHealthStatus(
  kind: string,
  resource: K8sNode | K8sPod | K8sPVC | K8sService,
): "healthy" | "warning" | "error" {
  if (kind === "pod") {
    const phase = resource.status?.phase;
    if (phase === "Running" || phase === "Succeeded") return "healthy";
    if (phase === "Pending") return "warning";
    return "error";
  }
  if (kind === "node") {
    const nodeRes = resource as K8sNode;
    const ready = nodeRes.status?.conditions?.find(
      (c) => c.type === "Ready",
    );
    return ready?.status === "True" ? "healthy" : "error";
  }
  if (kind === "pvc") {
    const phase = resource.status?.phase;
    if (phase === "Bound") return "healthy";
    if (phase === "Pending") return "warning";
    return "error";
  }
  return "healthy"; // services always healthy
}

interface RawData {
  k8sNodes: K8sNode[];
  k8sSvcs: K8sService[];
  k8sPods: K8sPod[];
  k8sPVCs: K8sPVC[];
}

export default function ClusterTopology() {
  const nodes = useSignal<TopoNode[]>([]);
  const edges = useSignal<TopoEdge[]>([]);
  const loading = useSignal(true);
  const rawData = useSignal<RawData | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const dimensions = useSignal({ width: 600, height: 220 });

  // Zoom and pan state
  const zoom = useSignal(1);
  const pan = useSignal({ x: 0, y: 0 });
  const dragging = useSignal(false);
  const dragStart = useSignal({ x: 0, y: 0 });
  const panStart = useSignal({ x: 0, y: 0 });

  // Measure container
  useEffect(() => {
    if (!IS_BROWSER || !containerRef.current) return;

    const measure = () => {
      if (containerRef.current) {
        const rect = containerRef.current.getBoundingClientRect();
        if (rect.width > 0 && rect.height > 0) {
          dimensions.value = { width: rect.width, height: rect.height };
        }
      }
    };

    measure();

    let timeout: number;
    const onResize = () => {
      clearTimeout(timeout);
      timeout = setTimeout(measure, 150) as unknown as number;
    };

    globalThis.addEventListener("resize", onResize);
    return () => {
      globalThis.removeEventListener("resize", onResize);
      clearTimeout(timeout);
    };
  }, []);

  // Fetch data once on mount
  useEffect(() => {
    if (!IS_BROWSER) return;

    async function load() {
      loading.value = true;

      const [nodesRes, svcsRes, podsRes, pvcsRes] = await Promise.allSettled([
        apiGet<K8sNode[]>("/v1/resources/nodes"),
        apiGet<K8sService[]>("/v1/resources/services"),
        apiGet<K8sPod[]>("/v1/resources/pods"),
        apiGet<K8sPVC[]>("/v1/resources/pvcs"),
      ]);

      rawData.value = {
        k8sNodes:
          (nodesRes.status === "fulfilled" && Array.isArray(nodesRes.value.data)
            ? nodesRes.value.data
            : []).slice(0, MAX_NODES),
        k8sSvcs:
          (svcsRes.status === "fulfilled" && Array.isArray(svcsRes.value.data)
            ? svcsRes.value.data
            : []).slice(0, MAX_SERVICES),
        k8sPods:
          (podsRes.status === "fulfilled" && Array.isArray(podsRes.value.data)
            ? podsRes.value.data
            : []).slice(0, MAX_PODS),
        k8sPVCs:
          (pvcsRes.status === "fulfilled" && Array.isArray(pvcsRes.value.data)
            ? pvcsRes.value.data
            : []).slice(0, MAX_PVCS),
      };

      loading.value = false;
    }

    load();
  }, []);

  // Compute layout from raw data + dimensions (re-runs on resize without re-fetching)
  useEffect(() => {
    if (!rawData.value) return;

    const { k8sNodes, k8sSvcs, k8sPods, k8sPVCs } = rawData.value;
    const w = dimensions.value.width;
    const h = dimensions.value.height;

    // Dynamic node sizes based on item count
    const nodeSize = k8sNodes.length > 6 ? 40 : 52;
    const svcSize = k8sSvcs.length > 8 ? 32 : 44;
    const podSize = k8sPods.length > 10 ? 28 : 36;
    const pvcSize = k8sPVCs.length > 6 ? 28 : 36;

    // Compute virtual canvas width based on largest row
    const ITEM_SPACING_H = 70;
    const maxItemsInRow = Math.max(
      k8sNodes.length,
      k8sSvcs.length,
      k8sPods.length,
      k8sPVCs.length,
    );
    const virtualWidth = Math.max(w, (maxItemsInRow + 1) * ITEM_SPACING_H);
    const virtualHeight = h;

    // Set initial zoom so topology fills the container at a readable size.
    // Scale to fit the widest row within the visible container width,
    // but never below 0.5x (unreadable) or above 1.5x (too zoomed in for few items).
    const fitZoom = (w * 0.95) / virtualWidth; // 95% of container to leave padding
    zoom.value = Math.max(0.5, Math.min(1.5, fitZoom));

    const topoNodes: TopoNode[] = [];
    const topoEdges: TopoEdge[] = [];

    const placeRow = (
      items: {
        id: string;
        label: string;
        kind: TopoNode["kind"];
        namespace?: string;
        health: "healthy" | "warning" | "error";
      }[],
      yPct: number,
      size: number,
    ) => {
      const count = items.length;
      if (count === 0) return;
      const spacing = virtualWidth / (count + 1);
      const kindAbbr: Record<string, string> = {
        node: "N",
        service: "SVC",
        pod: "P",
        pvc: "PVC",
      };
      items.forEach((item, i) => {
        const base = kindAbbr[item.kind] ??
          item.kind.substring(0, 2).toUpperCase();
        const abbr = item.kind === "node" ? `${base}${i + 1}` : base;
        topoNodes.push({
          ...item,
          abbr,
          x: spacing * (i + 1),
          y: virtualHeight * yPct,
          size,
        });
      });
    };

    placeRow(
      k8sNodes.map((n) => ({
        id: `node-${n.metadata.name}`,
        label: n.metadata.name,
        kind: "node" as const,
        health: getHealthStatus("node", n),
      })),
      0.10,
      nodeSize,
    );

    placeRow(
      k8sSvcs.map((s) => ({
        id: `svc-${s.metadata.namespace}-${s.metadata.name}`,
        label: s.metadata.name,
        namespace: s.metadata.namespace,
        kind: "service" as const,
        health: getHealthStatus("service", s),
      })),
      0.35,
      svcSize,
    );

    placeRow(
      k8sPods.map((p) => ({
        id: `pod-${p.metadata.namespace}-${p.metadata.name}`,
        label: p.metadata.name,
        namespace: p.metadata.namespace,
        kind: "pod" as const,
        health: getHealthStatus("pod", p),
      })),
      0.60,
      podSize,
    );

    placeRow(
      k8sPVCs.map((pvc) => ({
        id: `pvc-${pvc.metadata.namespace}-${pvc.metadata.name}`,
        label: pvc.metadata.name,
        namespace: pvc.metadata.namespace,
        kind: "pvc" as const,
        health: getHealthStatus("pvc", pvc),
      })),
      0.85,
      pvcSize,
    );

    // Build node-to-service edges
    for (const node of k8sNodes) {
      const nodeId = `node-${node.metadata.name}`;
      const connectedSvcs = new Set<string>();
      for (const pod of k8sPods) {
        if (pod.spec?.nodeName !== node.metadata.name) continue;
        for (const svc of k8sSvcs) {
          if (
            svc.metadata.namespace === pod.metadata.namespace &&
            matchesSelector(svc.spec?.selector, pod.metadata.labels)
          ) {
            connectedSvcs.add(
              `svc-${svc.metadata.namespace}-${svc.metadata.name}`,
            );
          }
        }
      }
      for (const svcId of connectedSvcs) {
        topoEdges.push({ from: nodeId, to: svcId, color: "var(--accent)" });
      }
    }

    // Build service-to-pod edges based on selector matching
    for (const svc of k8sSvcs) {
      const svcId = `svc-${svc.metadata.namespace}-${svc.metadata.name}`;
      for (const pod of k8sPods) {
        if (
          svc.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(svc.spec?.selector, pod.metadata.labels)
        ) {
          topoEdges.push({
            from: svcId,
            to: `pod-${pod.metadata.namespace}-${pod.metadata.name}`,
            color: "var(--accent-secondary)",
          });
        }
      }
    }

    // Build pod-to-PVC edges based on volume mounts
    for (const pod of k8sPods) {
      for (const vol of pod.spec?.volumes ?? []) {
        if (vol.persistentVolumeClaim?.claimName) {
          const pvcId =
            `pvc-${pod.metadata.namespace}-${vol.persistentVolumeClaim.claimName}`;
          const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
          if (topoNodes.some((n) => n.id === pvcId)) {
            topoEdges.push({
              from: podId,
              to: pvcId,
              color: "var(--info)",
            });
          }
        }
      }
    }

    nodes.value = topoNodes;
    edges.value = topoEdges;
  }, [dimensions.value.width, dimensions.value.height, rawData.value]);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "220px" }} />;
  }

  if (loading.value) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "100%",
          minHeight: "220px",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        Loading topology...
      </div>
    );
  }

  if (nodes.value.length === 0) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "100%",
          minHeight: "220px",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        No resources found
      </div>
    );
  }

  const kindColors: Record<
    TopoNode["kind"],
    { border: string; bg: string; borderRadius: string; color: string }
  > = {
    node: {
      border: "rgba(0,194,255,0.4)",
      bg: "linear-gradient(135deg, rgba(0,194,255,0.15), rgba(0,194,255,0.05))",
      borderRadius: "50%",
      color: "var(--accent)",
    },
    service: {
      border: "rgba(124,92,252,0.4)",
      bg:
        "linear-gradient(135deg, rgba(124,92,252,0.15), rgba(124,92,252,0.05))",
      borderRadius: "50%",
      color: "var(--accent-secondary)",
    },
    pod: {
      border: "rgba(80,250,123,0.4)",
      bg:
        "linear-gradient(135deg, rgba(80,250,123,0.15), rgba(80,250,123,0.05))",
      borderRadius: "50%",
      color: "var(--success)",
    },
    pvc: {
      border: "rgba(255,183,77,0.4)",
      bg:
        "linear-gradient(135deg, rgba(255,183,77,0.15), rgba(255,183,77,0.05))",
      borderRadius: "6px",
      color: "var(--warning)",
    },
  };

  function getNodeStyle(node: TopoNode) {
    const base = kindColors[node.kind];
    if (node.health === "healthy") return base;
    if (node.health === "warning") {
      return {
        ...base,
        border: "var(--warning)",
        bg:
          "linear-gradient(135deg, rgba(255,183,77,0.2), rgba(255,183,77,0.05))",
      };
    }
    return {
      ...base,
      border: "var(--error)",
      bg: "linear-gradient(135deg, rgba(239,68,68,0.2), rgba(239,68,68,0.05))",
    };
  }

  function getNodeHref(node: TopoNode): string {
    switch (node.kind) {
      case "node":
        return `/cluster/nodes/${node.label}`;
      case "service":
        return `/networking/services/${
          node.namespace ?? "default"
        }/${node.label}`;
      case "pod":
        return `/workloads/pods/${node.namespace ?? "default"}/${node.label}`;
      case "pvc":
        return `/storage/pvcs/${node.namespace ?? "default"}/${node.label}`;
    }
  }

  function getTooltip(node: TopoNode): string {
    const ns = node.namespace ? ` (${node.namespace})` : "";
    const status = node.health === "healthy"
      ? "Healthy"
      : node.health === "warning"
      ? "Warning"
      : "Error";
    return `${node.kind}: ${node.label}${ns} - ${status}`;
  }

  const nodeMap = new Map(nodes.value.map((n) => [n.id, n]));

  const handleWheel = (e: WheelEvent) => {
    e.preventDefault();
    const delta = e.deltaY > 0 ? -0.1 : 0.1;
    const next = Math.min(3, Math.max(0.3, zoom.value + delta));
    zoom.value = next;
  };

  const handleMouseDown = (e: MouseEvent) => {
    if (e.button !== 0) return;
    dragging.value = true;
    dragStart.value = { x: e.clientX, y: e.clientY };
    panStart.value = { x: pan.value.x, y: pan.value.y };
  };

  const handleMouseMove = (e: MouseEvent) => {
    if (!dragging.value) return;
    const dx = (e.clientX - dragStart.value.x) / zoom.value;
    const dy = (e.clientY - dragStart.value.y) / zoom.value;
    pan.value = { x: panStart.value.x + dx, y: panStart.value.y + dy };
  };

  const handleMouseUp = () => {
    dragging.value = false;
  };

  return (
    <div
      ref={containerRef}
      style={{
        position: "relative",
        width: "100%",
        height: "100%",
        overflow: "hidden",
        cursor: dragging.value ? "grabbing" : "grab",
      }}
      onWheel={handleWheel}
      onMouseDown={handleMouseDown}
      onMouseMove={handleMouseMove}
      onMouseUp={handleMouseUp}
      onMouseLeave={handleMouseUp}
    >
      {/* Transformed inner container for zoom/pan */}
      <div
        style={{
          position: "absolute",
          inset: 0,
          transform:
            `scale(${zoom.value}) translate(${pan.value.x}px, ${pan.value.y}px)`,
          transformOrigin: "center center",
        }}
      >
        {/* SVG connection lines */}
        <svg
          style={{
            position: "absolute",
            inset: 0,
            width: "100%",
            height: "100%",
            pointerEvents: "none",
          }}
        >
          <defs>
            <style>
              {`
@keyframes topoPulse {
  0%, 100% { opacity: 0.25; }
  50% { opacity: 0.5; }
}
`}
            </style>
          </defs>
          {edges.value.map((edge, i) => {
            const from = nodeMap.get(edge.from);
            const to = nodeMap.get(edge.to);
            if (!from || !to) return null;
            return (
              <line
                key={`edge-${i}`}
                x1={from.x}
                y1={from.y}
                x2={to.x}
                y2={to.y}
                stroke={edge.color}
                stroke-width="1.5"
                style={{ animation: "topoPulse 3s ease-in-out infinite" }}
              />
            );
          })}
        </svg>

        {/* Resource nodes */}
        {nodes.value.map((node) => {
          const style = getNodeStyle(node);
          const href = getNodeHref(node);
          const truncated = node.label.length > 15
            ? node.label.slice(0, 14) + "\u2026"
            : node.label;
          return (
            <a
              key={node.id}
              title={getTooltip(node)}
              href={href}
              style={{
                position: "absolute",
                left: `${node.x - node.size / 2}px`,
                top: `${node.y - node.size / 2}px`,
                width: `${node.size}px`,
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                textDecoration: "none",
                zIndex: 1,
              }}
              onMouseEnter={(e) => {
                const el = e.currentTarget as HTMLAnchorElement;
                el.style.transform = "scale(1.15)";
              }}
              onMouseLeave={(e) => {
                const el = e.currentTarget as HTMLAnchorElement;
                el.style.transform = "scale(1)";
              }}
              onClick={(e) => {
                // Prevent navigation if we just finished dragging
                if (
                  Math.abs(pan.value.x - panStart.value.x) > 3 ||
                  Math.abs(pan.value.y - panStart.value.y) > 3
                ) {
                  e.preventDefault();
                }
              }}
            >
              <div
                style={{
                  width: `${node.size}px`,
                  height: `${node.size}px`,
                  borderRadius: style.borderRadius,
                  border: `2px solid ${style.border}`,
                  background: style.bg,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  fontSize: `${Math.max(9, node.size * 0.22)}px`,
                  fontWeight: 700,
                  color: style.color,
                  cursor: "pointer",
                  transition: "transform 0.15s ease",
                }}
              >
                {node.abbr}
              </div>
              {zoom.value > 0.6 && (
                <div
                  style={{
                    fontSize: "10px",
                    color: "var(--text-muted)",
                    textAlign: "center",
                    marginTop: "2px",
                    maxWidth: `${node.size + 20}px`,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {truncated}
                </div>
              )}
            </a>
          );
        })}
      </div>

      {/* Zoom indicator */}
      {zoom.value !== 1 && (
        <div
          style={{
            position: "absolute",
            bottom: "4px",
            right: "8px",
            fontSize: "10px",
            color: "var(--text-muted)",
            pointerEvents: "none",
            userSelect: "none",
          }}
        >
          {Math.round(zoom.value * 100)}%
        </div>
      )}
    </div>
  );
}
