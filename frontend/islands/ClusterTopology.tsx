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

// SVG color definitions per kind
const kindSvgColors: Record<
  TopoNode["kind"],
  { stroke: string; fill: string; text: string; isRect: boolean }
> = {
  node: {
    stroke: "rgba(0,194,255,0.5)",
    fill: "rgba(0,194,255,0.1)",
    text: "var(--accent)",
    isRect: false,
  },
  service: {
    stroke: "rgba(124,92,252,0.5)",
    fill: "rgba(124,92,252,0.1)",
    text: "var(--accent-secondary)",
    isRect: false,
  },
  pod: {
    stroke: "rgba(80,250,123,0.5)",
    fill: "rgba(80,250,123,0.1)",
    text: "var(--success)",
    isRect: false,
  },
  pvc: {
    stroke: "rgba(255,183,77,0.5)",
    fill: "rgba(255,183,77,0.1)",
    text: "var(--warning)",
    isRect: true,
  },
};

function getNodeSvgStyle(node: TopoNode) {
  const base = kindSvgColors[node.kind];
  if (node.health === "healthy") return base;
  if (node.health === "warning") {
    return {
      ...base,
      stroke: "rgba(255,179,0,0.5)",
      fill: "rgba(255,179,0,0.1)",
    };
  }
  return {
    ...base,
    stroke: "rgba(255,82,82,0.5)",
    fill: "rgba(255,82,82,0.1)",
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

export default function ClusterTopology() {
  const nodes = useSignal<TopoNode[]>([]);
  const edges = useSignal<TopoEdge[]>([]);
  const loading = useSignal(true);
  const rawData = useSignal<RawData | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);

  // Virtual canvas dimensions (viewBox space)
  const virtualDims = useSignal({ w: 800, h: 400 });

  // Zoom and pan state (viewBox manipulation)
  const zoom = useSignal(1);
  const pan = useSignal({ x: 0, y: 0 });
  const dragging = useSignal(false);
  const dragStart = useSignal({ x: 0, y: 0 });
  const panStart = useSignal({ x: 0, y: 0 });

  // Hovered node for hover effect
  const hoveredNode = useSignal<string | null>(null);

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

  // Compute layout from raw data (no container measurement needed)
  useEffect(() => {
    if (!rawData.value) return;

    const { k8sNodes, k8sSvcs, k8sPods, k8sPVCs } = rawData.value;

    // Dynamic node sizes based on item count
    const nodeSize = k8sNodes.length > 6 ? 40 : 52;
    const svcSize = k8sSvcs.length > 8 ? 32 : 44;
    const podSize = k8sPods.length > 10 ? 28 : 36;
    const pvcSize = k8sPVCs.length > 6 ? 28 : 36;

    // Compute virtual canvas based on largest row
    const ITEM_SPACING_H = 62;
    const maxItemsInRow = Math.max(
      k8sNodes.length,
      k8sSvcs.length,
      k8sPods.length,
      k8sPVCs.length,
    );
    const virtualWidth = Math.max(400, (maxItemsInRow + 1) * ITEM_SPACING_H);
    const virtualHeight = Math.max(220, virtualWidth * 0.5);
    virtualDims.value = { w: virtualWidth, h: virtualHeight };

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
  }, [rawData.value]);

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

  const nodeMap = new Map(nodes.value.map((n) => [n.id, n]));

  // ViewBox computed from zoom and pan
  const vbW = virtualDims.value.w / zoom.value;
  const vbH = virtualDims.value.h / zoom.value;
  const vbX = pan.value.x;
  const vbY = pan.value.y;

  const handleWheel = (e: WheelEvent) => {
    e.preventDefault();
    const svg = svgRef.current;
    if (!svg) return;

    const delta = e.deltaY > 0 ? -0.1 : 0.1;
    const nextZoom = Math.min(3, Math.max(0.5, zoom.value + delta));

    // Zoom toward mouse position: compute mouse position in viewBox space
    const rect = svg.getBoundingClientRect();
    const mx = (e.clientX - rect.left) / rect.width; // 0..1 fraction
    const my = (e.clientY - rect.top) / rect.height;

    const oldW = virtualDims.value.w / zoom.value;
    const oldH = virtualDims.value.h / zoom.value;
    const newW = virtualDims.value.w / nextZoom;
    const newH = virtualDims.value.h / nextZoom;

    // Adjust pan so the point under the mouse stays fixed
    pan.value = {
      x: pan.value.x + (oldW - newW) * mx,
      y: pan.value.y + (oldH - newH) * my,
    };
    zoom.value = nextZoom;
  };

  const handleMouseDown = (e: MouseEvent) => {
    if (e.button !== 0) return;
    dragging.value = true;
    dragStart.value = { x: e.clientX, y: e.clientY };
    panStart.value = { x: pan.value.x, y: pan.value.y };
  };

  const handleMouseMove = (e: MouseEvent) => {
    if (!dragging.value) return;
    const svg = svgRef.current;
    if (!svg) return;

    const rect = svg.getBoundingClientRect();
    // Convert pixel drag distance to viewBox units
    const scaleX = vbW / rect.width;
    const scaleY = vbH / rect.height;

    const dx = (e.clientX - dragStart.value.x) * scaleX;
    const dy = (e.clientY - dragStart.value.y) * scaleY;
    pan.value = { x: panStart.value.x - dx, y: panStart.value.y - dy };
  };

  const handleMouseUp = () => {
    dragging.value = false;
  };

  return (
    <div
      style={{
        position: "relative",
        width: "100%",
        height: "100%",
        minHeight: "220px",
      }}
    >
      <svg
        ref={svgRef}
        width="100%"
        height="100%"
        viewBox={`${vbX} ${vbY} ${vbW} ${vbH}`}
        preserveAspectRatio="xMidYMid meet"
        style={{ cursor: dragging.value ? "grabbing" : "grab" }}
        onWheel={handleWheel}
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
      >
        <defs>
          <style>
            {`
@keyframes topoPulse {
  0%, 100% { opacity: 0.25; }
  50% { opacity: 0.5; }
}
.topo-edge { animation: topoPulse 3s ease-in-out infinite; }
.topo-node { transition: transform 0.15s ease; cursor: pointer; }
`}
          </style>
        </defs>

        {/* Connection lines */}
        {edges.value.map((edge, i) => {
          const from = nodeMap.get(edge.from);
          const to = nodeMap.get(edge.to);
          if (!from || !to) return null;
          return (
            <line
              key={`edge-${i}`}
              class="topo-edge"
              x1={from.x}
              y1={from.y}
              x2={to.x}
              y2={to.y}
              stroke={edge.color}
              stroke-width="1.5"
            />
          );
        })}

        {/* Resource nodes */}
        {nodes.value.map((node) => {
          const style = getNodeSvgStyle(node);
          const href = getNodeHref(node);
          const truncated = node.label.length > 15
            ? node.label.slice(0, 14) + "\u2026"
            : node.label;
          const isHovered = hoveredNode.value === node.id;
          const r = node.size / 2;
          const abbrFontSize = Math.max(9, node.size * 0.22);

          return (
            <a
              key={node.id}
              href={href}
              onClick={(e: MouseEvent) => {
                // Prevent navigation if we just finished dragging
                if (
                  Math.abs(pan.value.x - panStart.value.x) > 3 ||
                  Math.abs(pan.value.y - panStart.value.y) > 3
                ) {
                  e.preventDefault();
                }
              }}
            >
              <g
                class="topo-node"
                transform={`translate(${node.x}, ${node.y})${
                  isHovered ? " scale(1.15)" : ""
                }`}
                onMouseEnter={() => {
                  hoveredNode.value = node.id;
                }}
                onMouseLeave={() => {
                  hoveredNode.value = null;
                }}
              >
                <title>{getTooltip(node)}</title>
                {style.isRect
                  ? (
                    <rect
                      x={-r}
                      y={-r}
                      width={node.size}
                      height={node.size}
                      rx={6}
                      ry={6}
                      fill={style.fill}
                      stroke={style.stroke}
                      stroke-width="2"
                    />
                  )
                  : (
                    <circle
                      r={r}
                      fill={style.fill}
                      stroke={style.stroke}
                      stroke-width="2"
                    />
                  )}
                <text
                  text-anchor="middle"
                  dy="0.35em"
                  fill={style.text}
                  font-size={abbrFontSize}
                  font-weight="700"
                  style={{ pointerEvents: "none", userSelect: "none" }}
                >
                  {node.abbr}
                </text>
                <text
                  text-anchor="middle"
                  y={r + 14}
                  fill="var(--text-muted)"
                  font-size="9"
                  style={{ pointerEvents: "none", userSelect: "none" }}
                >
                  {truncated}
                </text>
              </g>
            </a>
          );
        })}
      </svg>

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
