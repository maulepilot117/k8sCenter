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
    containers?: { name: string }[];
    initContainers?: { name: string }[];
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

interface K8sWorkload {
  metadata: K8sMetadata;
  kind: string; // "Deployment", "StatefulSet", "DaemonSet"
  spec?: {
    selector?: { matchLabels?: Record<string, string> };
  };
}

interface TopoNode {
  id: string;
  label: string;
  abbr: string;
  kind: "node" | "service" | "workload" | "pod" | "pvc";
  namespace?: string;
  x: number;
  y: number;
  size: number;
  health: "healthy" | "warning" | "error";
  /** Number of containers (pods only) */
  containerCount?: number;
  /** Container names for tooltip (pods only) */
  containerNames?: string[];
}

interface TopoEdge {
  from: string;
  to: string;
  color: string;
}

// No artificial limits — SVG viewBox handles scaling for any cluster size.
// The backend API default limit is 100 per kind (configurable via ?limit=500).

function matchesSelector(
  selector: Record<string, string> | undefined,
  labels: Record<string, string> | undefined,
): boolean {
  if (!selector || !labels) return false;
  return Object.entries(selector).every(([k, v]) => labels[k] === v);
}

function getHealthStatus(
  kind: string,
  resource: K8sNode | K8sPod | K8sPVC | K8sService | K8sWorkload,
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
  return "healthy"; // services and workloads always healthy
}

interface RawData {
  k8sNodes: K8sNode[];
  k8sSvcs: K8sService[];
  k8sWorkloads: K8sWorkload[];
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
  workload: {
    stroke: "rgba(97,175,239,0.5)",
    fill: "rgba(97,175,239,0.12)",
    text: "#61AFEF",
    isRect: true,
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

function getWorkloadHref(label: string, namespace: string): string {
  // Label format: "kind/name" e.g. "Deployment/nginx"
  const slashIdx = label.indexOf("/");
  if (slashIdx === -1) {
    return `/workloads/deployments/${namespace}/${label}`;
  }
  const kind = label.substring(0, slashIdx).toLowerCase();
  const name = label.substring(slashIdx + 1);
  const kindPath: Record<string, string> = {
    deployment: "deployments",
    statefulset: "statefulsets",
    daemonset: "daemonsets",
  };
  return `/workloads/${kindPath[kind] ?? "deployments"}/${namespace}/${name}`;
}

function getNodeHref(node: TopoNode): string {
  switch (node.kind) {
    case "node":
      return `/cluster/nodes/${node.label}`;
    case "service":
      return `/networking/services/${
        node.namespace ?? "default"
      }/${node.label}`;
    case "workload":
      return getWorkloadHref(node.label, node.namespace ?? "default");
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
  let tooltip = `${node.kind}: ${node.label}${ns} - ${status}`;
  if (
    node.kind === "pod" && node.containerNames &&
    node.containerNames.length > 1
  ) {
    tooltip += `\nContainers: ${node.containerNames.join(", ")}`;
  }
  return tooltip;
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

      const [nodesRes, svcsRes, podsRes, pvcsRes, depsRes, stsRes, dsRes] =
        await Promise.allSettled([
          apiGet<K8sNode[]>("/v1/resources/nodes?limit=500"),
          apiGet<K8sService[]>("/v1/resources/services?limit=500"),
          apiGet<K8sPod[]>("/v1/resources/pods?limit=500"),
          apiGet<K8sPVC[]>("/v1/resources/pvcs?limit=500"),
          apiGet<K8sWorkload[]>("/v1/resources/deployments?limit=500"),
          apiGet<K8sWorkload[]>("/v1/resources/statefulsets?limit=500"),
          apiGet<K8sWorkload[]>("/v1/resources/daemonsets?limit=500"),
        ]);

      const extractArr = <T,>(
        res: PromiseSettledResult<{ data: T[] }>,
      ): T[] =>
        res.status === "fulfilled" && Array.isArray(res.data)
          ? res.data
          : res.status === "fulfilled" && Array.isArray(res.value?.data)
          ? res.value.data
          : [];

      const deployments = extractArr<K8sWorkload>(depsRes).map((w) => ({
        ...w,
        kind: "Deployment",
      }));
      const statefulSets = extractArr<K8sWorkload>(stsRes).map((w) => ({
        ...w,
        kind: "StatefulSet",
      }));
      const daemonSets = extractArr<K8sWorkload>(dsRes).map((w) => ({
        ...w,
        kind: "DaemonSet",
      }));

      rawData.value = {
        k8sNodes: extractArr<K8sNode>(nodesRes),
        k8sSvcs: extractArr<K8sService>(svcsRes),
        k8sWorkloads: [...deployments, ...statefulSets, ...daemonSets],
        k8sPods: extractArr<K8sPod>(podsRes),
        k8sPVCs: extractArr<K8sPVC>(pvcsRes),
      };

      loading.value = false;
    }

    load();
  }, []);

  // Compute layout from raw data (no container measurement needed)
  useEffect(() => {
    if (!rawData.value) return;

    const { k8sNodes, k8sSvcs, k8sWorkloads, k8sPods, k8sPVCs } = rawData.value;

    // Dynamic node sizes based on item count
    const nodeSize = k8sNodes.length > 6 ? 40 : 52;
    const svcSize = k8sSvcs.length > 8 ? 32 : 44;
    const wlSize = k8sWorkloads.length > 10 ? 28 : 36;
    const podSize = k8sPods.length > 10 ? 28 : 36;
    const pvcSize = k8sPVCs.length > 6 ? 28 : 36;

    // Compute virtual canvas based on largest row
    const ITEM_SPACING_H = 62;
    const maxItemsInRow = Math.max(
      k8sNodes.length,
      k8sSvcs.length,
      k8sWorkloads.length,
      k8sPods.length,
      k8sPVCs.length,
    );
    const virtualWidth = Math.max(400, (maxItemsInRow + 1) * ITEM_SPACING_H);
    const virtualHeight = Math.max(220, virtualWidth * 0.6);
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
        workload: "WL",
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

    const workloadAbbrMap: Record<string, string> = {
      Deployment: "DEP",
      StatefulSet: "STS",
      DaemonSet: "DS",
    };

    placeRow(
      k8sNodes.map((n) => ({
        id: `node-${n.metadata.name}`,
        label: n.metadata.name,
        kind: "node" as const,
        health: getHealthStatus("node", n),
      })),
      0.08,
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
      0.25,
      svcSize,
    );

    // Workloads row — use workload-specific abbreviations
    const wlItems = k8sWorkloads.map((w) => ({
      id: `wl-${w.kind}-${w.metadata.namespace}-${w.metadata.name}`,
      label: `${w.kind}/${w.metadata.name}`,
      namespace: w.metadata.namespace,
      kind: "workload" as const,
      health: getHealthStatus("workload", w),
    }));
    // Place workloads, then override abbr with kind-specific label
    placeRow(wlItems, 0.45, wlSize);
    for (let i = 0; i < k8sWorkloads.length; i++) {
      const wl = k8sWorkloads[i];
      const placed = topoNodes.find(
        (n) =>
          n.id ===
            `wl-${wl.kind}-${wl.metadata.namespace}-${wl.metadata.name}`,
      );
      if (placed) {
        placed.abbr = workloadAbbrMap[wl.kind] ?? wl.kind.substring(0, 3);
      }
    }

    // Pods row — include container info
    const podItems = k8sPods.map((p) => ({
      id: `pod-${p.metadata.namespace}-${p.metadata.name}`,
      label: p.metadata.name,
      namespace: p.metadata.namespace,
      kind: "pod" as const,
      health: getHealthStatus("pod", p),
      containerCount: p.spec?.containers?.length ?? 1,
      containerNames: (p.spec?.containers ?? []).map((c) => c.name),
    }));
    const podCount = podItems.length;
    if (podCount > 0) {
      const spacing = virtualWidth / (podCount + 1);
      podItems.forEach((item, i) => {
        topoNodes.push({
          ...item,
          abbr: "P",
          x: spacing * (i + 1),
          y: virtualHeight * 0.65,
          size: podSize,
        });
      });
    }

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

    // Build a map of pod ID → workload IDs (via label selector matching)
    const podToWorkloads = new Map<string, Set<string>>();
    for (const wl of k8sWorkloads) {
      const wlId = `wl-${wl.kind}-${wl.metadata.namespace}-${wl.metadata.name}`;
      const sel = wl.spec?.selector?.matchLabels;
      if (!sel) continue;
      for (const pod of k8sPods) {
        if (
          wl.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(sel, pod.metadata.labels)
        ) {
          const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
          if (!podToWorkloads.has(podId)) {
            podToWorkloads.set(podId, new Set());
          }
          podToWorkloads.get(podId)!.add(wlId);
          // Workload → Pod edge
          topoEdges.push({
            from: wlId,
            to: podId,
            color: "#61AFEF",
          });
        }
      }
    }

    // Build service-to-workload edges (service selects pods that belong to a workload)
    for (const svc of k8sSvcs) {
      const svcId = `svc-${svc.metadata.namespace}-${svc.metadata.name}`;
      const connectedWorkloads = new Set<string>();
      for (const pod of k8sPods) {
        if (
          svc.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(svc.spec?.selector, pod.metadata.labels)
        ) {
          const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
          const wlIds = podToWorkloads.get(podId);
          if (wlIds) {
            for (const wlId of wlIds) {
              connectedWorkloads.add(wlId);
            }
          } else {
            // No workload found — draw direct service→pod edge
            topoEdges.push({
              from: svcId,
              to: podId,
              color: "var(--accent-secondary)",
            });
          }
        }
      }
      for (const wlId of connectedWorkloads) {
        topoEdges.push({
          from: svcId,
          to: wlId,
          color: "var(--accent-secondary)",
        });
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
                {node.kind === "workload"
                  ? (
                    <rect
                      x={-r}
                      y={-r}
                      width={node.size}
                      height={node.size}
                      rx={8}
                      ry={8}
                      fill={style.fill}
                      stroke={style.stroke}
                      stroke-width="2"
                    />
                  )
                  : style.isRect
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
                {node.containerCount && node.containerCount > 1 && (
                  <>
                    <circle
                      cx={r - 4}
                      cy={-r + 4}
                      r="7"
                      fill="var(--accent)"
                    />
                    <text
                      x={r - 4}
                      y={-r + 4}
                      text-anchor="middle"
                      dy="0.35em"
                      font-size="8"
                      fill="white"
                      font-weight="700"
                      style={{ pointerEvents: "none", userSelect: "none" }}
                    >
                      {node.containerCount}
                    </text>
                  </>
                )}
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
