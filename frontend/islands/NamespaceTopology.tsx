import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { timeAgo } from "@/lib/timeAgo.ts";
// ── Backend topology API types ──

interface TopoGraph {
  nodes: TopoNode[];
  edges: TopoEdge[];
  // truncated signals nodes were dropped at maxNodes. edgesTruncated
  // signals mesh-overlay edges were dropped at maxMeshEdges. They are
  // independent so consumers can tell "graph missing nodes" from
  // "graph complete, only some mesh edges capped".
  truncated?: boolean;
  edgesTruncated?: boolean;
  // Set by the backend when an overlay query is requested.
  // "mesh" — mesh overlay applied.
  // "eso-chain" — External Secrets chain overlay applied.
  // "unavailable" — overlay was requested but couldn't be applied
  //                  (provider unwired, CRDs absent, fetch errored).
  overlay?: "mesh" | "eso-chain" | "unavailable";
  // Per-stage warnings; currently used for mesh-overlay host-resolution
  // drops so a custom-cluster-domain namespace doesn't return a silent
  // empty overlay.
  errors?: Record<string, string>;
  computedAt: string;
}

interface TopoNode {
  id: string;
  kind: string;
  name: string;
  namespace: string;
  health: "healthy" | "degraded" | "failing" | "unknown";
  summary: string;
}

interface TopoEdge {
  source: string;
  target: string;
  type: string;
}

// ── Layout types ──

interface LayoutNode extends TopoNode {
  x: number;
  y: number;
}

type TopologyOverlay = "none" | "mesh" | "eso-chain";

interface FocusedTopologyNode {
  kind: string;
  namespace?: string;
  name: string;
}

interface NamespaceTopologyProps {
  namespace?: string;
  initialOverlay?: TopologyOverlay;
  focusedNode?: FocusedTopologyNode;
  embedded?: boolean;
  minHeight?: number;
  viewTopologyHref?: string;
}

// ── Constants ──

const NODE_WIDTH = 180;
const NODE_HEIGHT = 56;
const BASE_WIDTH = 1200;
const BASE_HEIGHT = 800;
const MIN_ZOOM = 0.3;
const MAX_ZOOM = 3;
const PANEL_WIDTH = 320;

const HEALTH_COLORS: Record<TopoNode["health"], string> = {
  healthy: "var(--status-success)",
  degraded: "var(--status-warning)",
  failing: "var(--status-error)",
  unknown: "var(--text-muted)",
};

// Edge styles. The mesh entries set their own stroke color (themed); base
// edges use var(--border-primary) at the line element level. The `stroke`
// property here is consulted only when present, keeping base behavior
// untouched.
const EDGE_STYLES: Record<
  string,
  { dasharray: string; opacity: number; stroke?: string; markerId?: string }
> = {
  owner: { dasharray: "", opacity: 0.7 },
  selector: { dasharray: "6,3", opacity: 0.7 },
  mount: { dasharray: "2,2", opacity: 0.4 },
  ingress: { dasharray: "6,3", opacity: 0.7 },
  // Istio VirtualService traffic edges — primary accent.
  mesh_vs: {
    dasharray: "4,2",
    opacity: 0.85,
    stroke: "var(--accent)",
    markerId: "arrow-mesh-vs",
  },
  // Linkerd ServiceProfile traffic edges — secondary accent so the two
  // mesh types are visually distinct when both are installed.
  mesh_sp: {
    dasharray: "4,2",
    opacity: 0.85,
    stroke: "var(--accent-secondary)",
    markerId: "arrow-mesh-sp",
  },
  eso_auth: {
    dasharray: "3,2",
    opacity: 0.9,
    stroke: "var(--accent)",
    markerId: "arrow-eso-auth",
  },
  eso_sync: {
    dasharray: "",
    opacity: 0.9,
    stroke: "var(--accent-secondary)",
    markerId: "arrow-eso-sync",
  },
  eso_consumer: {
    dasharray: "2,2",
    opacity: 0.75,
    stroke: "var(--muted)",
    markerId: "arrow-eso-consumer",
  },
  EdgeESOAuth: {
    dasharray: "3,2",
    opacity: 0.9,
    stroke: "var(--accent)",
    markerId: "arrow-eso-auth",
  },
  EdgeESOSync: {
    dasharray: "",
    opacity: 0.9,
    stroke: "var(--accent-secondary)",
    markerId: "arrow-eso-sync",
  },
  EdgeESOConsumer: {
    dasharray: "2,2",
    opacity: 0.75,
    stroke: "var(--muted)",
    markerId: "arrow-eso-consumer",
  },
};

function isMeshEdge(t: TopoEdge["type"]): boolean {
  return t === "mesh_vs" || t === "mesh_sp";
}

function isESOChainEdge(t: TopoEdge["type"]): boolean {
  return t === "eso_auth" || t === "eso_sync" || t === "eso_consumer" ||
    t === "EdgeESOAuth" || t === "EdgeESOSync" || t === "EdgeESOConsumer";
}

// ── Kind abbreviations ──

const KIND_ABBREVIATIONS: Record<string, string> = {
  "HorizontalPodAutoscaler": "HPA",
  "PodDisruptionBudget": "PDB",
  "PersistentVolumeClaim": "PVC",
  "ReplicaSet": "RS",
  "DaemonSet": "DS",
  "StatefulSet": "STS",
  "ConfigMap": "CM",
  "NetworkPolicy": "NetPol",
};

function displayKind(kind: string): string {
  return KIND_ABBREVIATIONS[kind] ?? kind;
}

// ── Helpers ──

function getResourceHref(kind: string, ns: string, name: string): string {
  const eNs = encodeURIComponent(ns);
  const eName = encodeURIComponent(name);
  const kindRoutes: Record<string, string> = {
    Deployment: `/workloads/deployments/${eNs}/${eName}`,
    StatefulSet: `/workloads/statefulsets/${eNs}/${eName}`,
    DaemonSet: `/workloads/daemonsets/${eNs}/${eName}`,
    Pod: `/workloads/pods/${eNs}/${eName}`,
    Service: `/networking/services/${eNs}/${eName}`,
    Ingress: `/networking/ingresses/${eNs}/${eName}`,
    ConfigMap: `/config/configmaps/${eNs}/${eName}`,
    Secret: `/config/secrets/${eNs}/${eName}`,
    PersistentVolumeClaim: `/storage/pvcs/${eNs}/${eName}`,
    Job: `/workloads/jobs/${eNs}/${eName}`,
    CronJob: `/workloads/cronjobs/${eNs}/${eName}`,
    ReplicaSet: `/workloads/replicasets/${eNs}/${eName}`,
  };
  return kindRoutes[kind] ??
    `/resources/${encodeURIComponent(kind)}/${encodeURIComponent(ns)}/${
      encodeURIComponent(name)
    }`;
}

function overlayFromQuery(): TopologyOverlay {
  if (!IS_BROWSER) return "none";
  const overlay = new URLSearchParams(globalThis.location.search).get(
    "overlay",
  );
  return overlay === "mesh" || overlay === "eso-chain" ? overlay : "none";
}

function topologyNamespaceFromQuery(): string | null {
  if (!IS_BROWSER) return null;
  return new URLSearchParams(globalThis.location.search).get("namespace");
}

function findFocusedNode(
  nodes: TopoNode[],
  focus: FocusedTopologyNode,
): TopoNode | null {
  return nodes.find((node) =>
    node.kind === focus.kind &&
    node.name === focus.name &&
    (!focus.namespace || node.namespace === focus.namespace ||
      (focus.kind === "ClusterSecretStore" && node.namespace === ""))
  ) ?? null;
}

function filterGraphToNeighborhood(
  graph: TopoGraph,
  focus?: FocusedTopologyNode,
): TopoGraph {
  if (!focus) return graph;
  const focal = findFocusedNode(graph.nodes, focus);
  if (!focal) return { ...graph, nodes: [], edges: [] };

  const adjacent = new Map<string, Set<string>>();
  for (const node of graph.nodes) adjacent.set(node.id, new Set());
  for (const edge of graph.edges) {
    if (!adjacent.has(edge.source) || !adjacent.has(edge.target)) continue;
    adjacent.get(edge.source)!.add(edge.target);
    adjacent.get(edge.target)!.add(edge.source);
  }

  const visited = new Set<string>([focal.id]);
  const queue = [focal.id];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const next of adjacent.get(current) ?? []) {
      if (visited.has(next)) continue;
      visited.add(next);
      queue.push(next);
    }
  }

  const filteredEdges = graph.edges.filter((edge) =>
    visited.has(edge.source) && visited.has(edge.target)
  );
  if (filteredEdges.length === 0) {
    return { ...graph, nodes: [], edges: [] };
  }

  return {
    ...graph,
    nodes: graph.nodes.filter((node) => visited.has(node.id)),
    edges: filteredEdges,
  };
}

// ── Component ──

export default function NamespaceTopology({
  namespace,
  initialOverlay,
  focusedNode,
  embedded = false,
  minHeight,
  viewTopologyHref,
}: NamespaceTopologyProps = {}) {
  const graph = useSignal<TopoGraph | null>(null);
  const loading = useSignal(true);
  const error = useSignal<string | null>(null);
  const selectedNode = useSignal<string | null>(null);
  const hoveredNode = useSignal<string | null>(null);
  const zoom = useSignal(1);
  const panX = useSignal(0);
  const panY = useSignal(0);
  const dragging = useSignal(false);
  const dragStart = useSignal({ x: 0, y: 0 });
  const panStart = useSignal({ x: 0, y: 0 });

  const requestedOverlay = useSignal<TopologyOverlay>(
    initialOverlay ?? overlayFromQuery(),
  );
  // Latches the specific overlay that returned overlay=unavailable. This keeps
  // mesh and ESO-chain availability independent while still letting the user
  // switch back to the base graph or another overlay.
  const unavailableOverlay = useSignal<Exclude<TopologyOverlay, "none"> | null>(
    null,
  );

  const svgRef = useRef<SVGSVGElement>(null);
  const layoutNodes = useSignal<LayoutNode[]>([]);
  const layoutNodeMap = useSignal<Map<string, LayoutNode>>(new Map());

  // ── Data fetching ──

  const fetchGraph = async () => {
    const ns = namespace ?? selectedNamespace.value;
    if (ns === "all") return;
    loading.value = true;
    error.value = null;
    try {
      const overlay = requestedOverlay.value;
      const overlayParam = overlay === "none" ? "" : `?overlay=${overlay}`;
      const res = await apiGet<TopoGraph>(`/v1/topology/${ns}${overlayParam}`);
      graph.value = res.data;
      if (res.data.overlay === "unavailable" && overlay !== "none") {
        unavailableOverlay.value = overlay;
        requestedOverlay.value = "none";
      } else if (overlay !== "none" && res.data.overlay === overlay) {
        unavailableOverlay.value = null;
      }
    } catch (err) {
      error.value = err instanceof Error
        ? err.message
        : "Failed to fetch topology";
      graph.value = null;
    } finally {
      loading.value = false;
    }
  };

  useEffect(() => {
    if (!IS_BROWSER) return;
    const queryNamespace = namespace ? null : topologyNamespaceFromQuery();
    if (queryNamespace) selectedNamespace.value = queryNamespace;
  }, [namespace]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const activeNamespace = namespace ?? selectedNamespace.value;
    if (activeNamespace === "all") {
      loading.value = false;
      return;
    }
    // A different namespace means a different cluster slice.
    unavailableOverlay.value = null;
    fetchGraph();
  }, [namespace ?? selectedNamespace.value]);

  // Re-fetch when the user changes overlays. Separate effect so the
  // namespace-change reset above doesn't fight a same-tick overlay change.
  useEffect(() => {
    if (!IS_BROWSER) return;
    const activeNamespace = namespace ?? selectedNamespace.value;
    if (activeNamespace === "all") return;
    fetchGraph();
  }, [requestedOverlay.value]);

  // ── Layout: custom LR topological sort (no dagre — it uses Node.js builtins) ──

  useEffect(() => {
    if (!graph.value || graph.value.nodes.length === 0) {
      layoutNodes.value = [];
      return;
    }

    const focusedGraph = filterGraphToNeighborhood(graph.value, focusedNode);
    const nodes = focusedGraph.nodes;
    const edges = focusedGraph.edges;
    const nodeMap = new Map(nodes.map((n) => [n.id, n]));

    // Build adjacency: children[parentId] = [childIds], parents[childId] = [parentIds]
    const children = new Map<string, string[]>();
    const parents = new Map<string, string[]>();
    for (const n of nodes) {
      children.set(n.id, []);
      parents.set(n.id, []);
    }
    for (const e of edges) {
      if (nodeMap.has(e.source) && nodeMap.has(e.target)) {
        children.get(e.source)!.push(e.target);
        parents.get(e.target)!.push(e.source);
      }
    }

    // Assign layers via longest-path from roots (nodes with no parents)
    const layer = new Map<string, number>();
    const visited = new Set<string>();
    function assignLayer(id: string): number {
      if (layer.has(id)) return layer.get(id)!;
      // Cycle protection: if we re-enter a node during traversal, assign layer 0
      // to break the cycle. This may cause visual overlap in the rare case of cyclic
      // ownership (e.g. mutually owning resources), but prevents infinite recursion.
      if (visited.has(id)) return 0;
      visited.add(id);
      const parentLayers = (parents.get(id) ?? []).map((pid) =>
        assignLayer(pid)
      );
      const l = parentLayers.length > 0 ? Math.max(...parentLayers) + 1 : 0;
      layer.set(id, l);
      return l;
    }
    for (const n of nodes) assignLayer(n.id);

    // Group nodes by layer
    const layers = new Map<number, string[]>();
    for (const [id, l] of layer) {
      if (!layers.has(l)) layers.set(l, []);
      layers.get(l)!.push(id);
    }

    // Position: x by layer, y by index within layer
    const LAYER_GAP = 220;
    const NODE_GAP = 80;
    const positioned: LayoutNode[] = [];
    for (const [l, ids] of layers) {
      const totalHeight = ids.length * (NODE_HEIGHT + NODE_GAP) - NODE_GAP;
      const startY = -totalHeight / 2;
      ids.forEach((id, i) => {
        const node = nodeMap.get(id)!;
        positioned.push({
          ...node,
          x: l * LAYER_GAP + NODE_WIDTH / 2 + 40,
          y: startY + i * (NODE_HEIGHT + NODE_GAP) + NODE_HEIGHT / 2,
        });
      });
    }

    layoutNodes.value = positioned;
    layoutNodeMap.value = new Map(positioned.map((n) => [n.id, n]));
  }, [
    graph.value,
    focusedNode?.kind,
    focusedNode?.namespace,
    focusedNode?.name,
  ]);

  // ── Connectivity sets for hover highlight ──

  function getConnectedIds(nodeId: string): Set<string> {
    const connected = new Set<string>([nodeId]);
    if (!graph.value) return connected;
    const focusedGraph = filterGraphToNeighborhood(graph.value, focusedNode);
    for (const edge of focusedGraph.edges) {
      if (edge.source === nodeId) connected.add(edge.target);
      if (edge.target === nodeId) connected.add(edge.source);
    }
    return connected;
  }

  // ── Zoom/Pan handlers ──

  const handleWheel = (e: WheelEvent) => {
    e.preventDefault();
    const svg = svgRef.current;
    if (!svg) return;

    const delta = e.deltaY > 0 ? -0.1 : 0.1;
    const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom.value + delta));

    const rect = svg.getBoundingClientRect();
    const mx = (e.clientX - rect.left) / rect.width;
    const my = (e.clientY - rect.top) / rect.height;

    const oldW = BASE_WIDTH / zoom.value;
    const oldH = BASE_HEIGHT / zoom.value;
    const newW = BASE_WIDTH / nextZoom;
    const newH = BASE_HEIGHT / nextZoom;

    panX.value = panX.value + (oldW - newW) * mx;
    panY.value = panY.value + (oldH - newH) * my;
    zoom.value = nextZoom;
  };

  const handleMouseDown = (e: MouseEvent) => {
    if (e.button !== 0) return;
    dragging.value = true;
    dragStart.value = { x: e.clientX, y: e.clientY };
    panStart.value = { x: panX.value, y: panY.value };
  };

  const handleMouseMove = (e: MouseEvent) => {
    if (!dragging.value) return;
    const svg = svgRef.current;
    if (!svg) return;

    const rect = svg.getBoundingClientRect();
    const vbW = BASE_WIDTH / zoom.value;
    const vbH = BASE_HEIGHT / zoom.value;
    const scaleX = vbW / rect.width;
    const scaleY = vbH / rect.height;

    const dx = (e.clientX - dragStart.value.x) * scaleX;
    const dy = (e.clientY - dragStart.value.y) * scaleY;
    panX.value = panStart.value.x - dx;
    panY.value = panStart.value.y - dy;
  };

  const handleMouseUp = () => {
    dragging.value = false;
  };

  const fitToView = () => {
    zoom.value = 1;
    panX.value = 0;
    panY.value = 0;
  };

  // ── Render helpers ──

  const vbW = BASE_WIDTH / zoom.value;
  const vbH = BASE_HEIGHT / zoom.value;

  const connectedSet = hoveredNode.value
    ? getConnectedIds(hoveredNode.value)
    : null;

  function nodeOpacity(id: string): number {
    if (!connectedSet) return 1;
    return connectedSet.has(id) ? 1 : 0.15;
  }

  function edgeOpacity(source: string, target: string, base: number): number {
    if (!connectedSet) return base;
    return connectedSet.has(source) && connectedSet.has(target) ? base : 0.08;
  }

  const activeNamespace = namespace ?? selectedNamespace.value;
  const renderedGraph = graph.value
    ? filterGraphToNeighborhood(graph.value, focusedNode)
    : null;

  // ── No namespace selected ──

  if (activeNamespace === "all") {
    return (
      <div class="flex items-center justify-center rounded-lg border border-border-primary bg-bg-surface p-12">
        <p class="text-text-secondary">
          Select a namespace to view its resource topology.
        </p>
      </div>
    );
  }

  // ── Loading ──

  if (loading.value) {
    return (
      <div class="flex items-center justify-center rounded-lg border border-border-primary bg-bg-surface p-12">
        <Spinner class="text-accent" />
        <span class="ml-3 text-text-secondary">Loading topology...</span>
      </div>
    );
  }

  // ── Error ──

  if (error.value) {
    return (
      <div class="space-y-4">
        <ErrorBanner message={error.value} />
        <button
          type="button"
          class="rounded bg-accent-primary px-3 py-1.5 text-sm font-medium text-white hover:opacity-90"
          onClick={() => fetchGraph()}
        >
          Retry
        </button>
      </div>
    );
  }

  // ── Empty state ──

  if (!renderedGraph || renderedGraph.nodes.length === 0) {
    return (
      <div class="flex items-center justify-center rounded-lg border border-border-primary bg-bg-surface p-12">
        <p class="text-text-secondary">
          {focusedNode
            ? "No chain edges - this resource has no synced consumers yet."
            : "No resources found in this namespace."}
        </p>
      </div>
    );
  }

  // ── Selected node data ──

  const selected = selectedNode.value
    ? renderedGraph.nodes.find((n) => n.id === selectedNode.value) ?? null
    : null;

  // ── Main render ──

  const graphMinHeight = minHeight ?? (embedded ? 400 : 500);
  const svgMinHeight = Math.max(320, graphMinHeight - 40);
  const meshUnavailable = unavailableOverlay.value === "mesh";
  const esoChainUnavailable = unavailableOverlay.value === "eso-chain";
  const topologyHref = viewTopologyHref ??
    `/observability/topology?namespace=${
      encodeURIComponent(activeNamespace)
    }&overlay=${encodeURIComponent(requestedOverlay.value)}`;

  return (
    <div class={`relative ${embedded ? "block" : "flex"} gap-0`}>
      {/* Graph area */}
      <div
        class="flex-1 overflow-hidden rounded-lg border border-border-primary bg-bg-surface"
        style={{ minHeight: `${graphMinHeight}px` }}
      >
        {/* Toolbar */}
        <div class="flex flex-col gap-2 border-b border-border-primary px-4 py-2 md:flex-row md:items-center md:justify-between">
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="rounded border border-border-primary bg-bg-surface px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              onClick={fitToView}
            >
              Fit to view
            </button>
            <button
              type="button"
              class="rounded border border-border-primary bg-bg-surface px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              onClick={() => fetchGraph()}
            >
              Refresh
            </button>
            <div
              role="radiogroup"
              aria-label="Topology overlay"
              class="ml-0 inline-flex overflow-hidden rounded border border-border-primary bg-bg-surface md:ml-2"
            >
              {(
                [
                  ["none", "Base", false, "Show base resource dependencies"],
                  [
                    "mesh",
                    "Mesh",
                    meshUnavailable,
                    meshUnavailable
                      ? "No service mesh detected in this cluster"
                      : "Show Istio VirtualService and Linkerd ServiceProfile traffic edges",
                  ],
                  [
                    "eso-chain",
                    "ESO chain",
                    esoChainUnavailable,
                    esoChainUnavailable
                      ? "ESO not installed in this cluster"
                      : "Show External Secrets chain edges",
                  ],
                ] as Array<[TopologyOverlay, string, boolean, string]>
              ).map(([value, label, disabled, title]) => {
                const active = requestedOverlay.value === value;
                return (
                  <button
                    key={value}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    disabled={disabled}
                    title={title}
                    class={`px-2.5 py-1 text-xs transition-colors ${
                      active
                        ? "bg-brand text-white"
                        : disabled
                        ? "cursor-not-allowed text-text-muted opacity-60"
                        : "text-text-secondary hover:bg-bg-elevated hover:text-text-primary"
                    }`}
                    onClick={() => {
                      requestedOverlay.value = value;
                    }}
                  >
                    {label}
                  </button>
                );
              })}
            </div>
          </div>
          <div class="flex items-center gap-3">
            {requestedOverlay.value === "mesh" &&
              renderedGraph.overlay === "mesh" && (
              <div class="flex items-center gap-3 text-xs text-text-muted">
                <span class="flex items-center gap-1.5">
                  <span
                    class="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent)" }}
                    aria-hidden="true"
                  />
                  Istio
                </span>
                <span class="flex items-center gap-1.5">
                  <span
                    class="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent-secondary)" }}
                    aria-hidden="true"
                  />
                  Linkerd
                </span>
              </div>
            )}
            {requestedOverlay.value === "eso-chain" &&
              renderedGraph.overlay === "eso-chain" && (
              <div class="flex items-center gap-3 text-xs text-text-muted">
                <span class="flex items-center gap-1.5">
                  <span
                    class="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent)" }}
                    aria-hidden="true"
                  />
                  Auth
                </span>
                <span class="flex items-center gap-1.5">
                  <span
                    class="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent-secondary)" }}
                    aria-hidden="true"
                  />
                  Sync
                </span>
                <span class="flex items-center gap-1.5">
                  <span
                    class="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--muted)" }}
                    aria-hidden="true"
                  />
                  Consumer
                </span>
              </div>
            )}
            {embedded && (
              <a
                href={topologyHref}
                class="rounded border border-border-primary bg-bg-surface px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              >
                View in Topology
              </a>
            )}
            {renderedGraph.computedAt && (
              <span class="text-xs text-text-muted">
                Updated {timeAgo(renderedGraph.computedAt)}
              </span>
            )}
          </div>
        </div>

        {/* SVG Graph */}
        <svg
          ref={svgRef}
          width="100%"
          height="100%"
          viewBox={`${panX.value} ${panY.value} ${vbW} ${vbH}`}
          preserveAspectRatio="xMidYMid meet"
          aria-label="Namespace resource topology graph"
          style={{
            cursor: dragging.value ? "grabbing" : "grab",
            minHeight: `${svgMinHeight}px`,
          }}
          onWheel={handleWheel}
          onMouseDown={handleMouseDown}
          onMouseMove={handleMouseMove}
          onMouseUp={handleMouseUp}
          onMouseLeave={handleMouseUp}
        >
          <defs>
            <style>
              {`
@keyframes nsTopoPulse {
  0%, 100% { stroke-opacity: 1; }
  50% { stroke-opacity: 0.4; }
}
.ns-topo-failing rect { animation: nsTopoPulse 1.5s ease-in-out infinite; }
`}
            </style>
            {/* Arrowhead markers: default for owner/selector/ingress, separate for mount */}
            <marker
              id="arrow-default"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--border-primary)" />
            </marker>
            <marker
              id="arrow-mount"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path
                d="M0,0 L10,3 L0,6 Z"
                fill="var(--text-muted)"
                opacity="0.5"
              />
            </marker>
            <marker
              id="arrow-mesh-vs"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--accent)" />
            </marker>
            <marker
              id="arrow-mesh-sp"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--accent-secondary)" />
            </marker>
            <marker
              id="arrow-eso-auth"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--accent)" />
            </marker>
            <marker
              id="arrow-eso-sync"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--accent-secondary)" />
            </marker>
            <marker
              id="arrow-eso-consumer"
              viewBox="0 0 10 6"
              refX="10"
              refY="3"
              markerWidth="8"
              markerHeight="6"
              orient="auto"
            >
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--muted)" />
            </marker>
          </defs>

          {/* Edges */}
          {renderedGraph.edges.map((edge, i) => {
            const src = layoutNodeMap.value.get(edge.source);
            const tgt = layoutNodeMap.value.get(edge.target);
            if (!src || !tgt) return null;
            // Backend EdgeType is a Go string typedef (open enum); a new
            // type added server-side would land in the response before
            // any frontend-side TS update. Fall back to the owner style
            // so an unknown edge renders as a generic dependency line
            // rather than crashing on style.dasharray of undefined.
            const style = EDGE_STYLES[edge.type] ?? EDGE_STYLES.owner;
            const op = edgeOpacity(edge.source, edge.target, style.opacity);
            const stroke = style.stroke ?? "var(--border-primary)";
            const markerId = style.markerId ??
              (edge.type === "mount" ? "arrow-mount" : "arrow-default");
            return (
              <line
                key={`edge-${i}`}
                x1={src.x}
                y1={src.y}
                x2={tgt.x}
                y2={tgt.y}
                stroke={stroke}
                stroke-width={isMeshEdge(edge.type) || isESOChainEdge(edge.type)
                  ? 2
                  : 1.5}
                stroke-dasharray={style.dasharray}
                opacity={op}
                marker-end={`url(#${markerId})`}
              />
            );
          })}

          {/* Nodes */}
          {layoutNodes.value.map((node) => {
            const healthColor = HEALTH_COLORS[node.health];
            const op = nodeOpacity(node.id);
            const isSelected = selectedNode.value === node.id;
            return (
              <g
                key={node.id}
                class={node.health === "failing" ? "ns-topo-failing" : ""}
                transform={`translate(${node.x - NODE_WIDTH / 2}, ${
                  node.y - NODE_HEIGHT / 2
                })`}
                opacity={op}
                style={{ cursor: "pointer" }}
                onMouseEnter={() => {
                  hoveredNode.value = node.id;
                }}
                onMouseLeave={() => {
                  hoveredNode.value = null;
                }}
                onClick={() => {
                  selectedNode.value = selectedNode.value === node.id
                    ? null
                    : node.id;
                }}
              >
                <rect
                  width={NODE_WIDTH}
                  height={NODE_HEIGHT}
                  rx={8}
                  fill="var(--bg-surface)"
                  stroke={healthColor}
                  stroke-width={isSelected ? 2.5 : 1.5}
                />
                {/* Kind label */}
                <text
                  x={10}
                  y={20}
                  font-size="10"
                  fill="var(--text-muted)"
                  font-family="var(--font-mono, monospace)"
                >
                  {displayKind(node.kind)}
                </text>
                {/* Resource name */}
                <text
                  x={10}
                  y={40}
                  font-size="13"
                  font-weight="600"
                  fill="var(--text-primary)"
                  font-family="var(--font-sans, sans-serif)"
                >
                  {node.name.length > 20
                    ? node.name.substring(0, 18) + "..."
                    : node.name}
                </text>
              </g>
            );
          })}
        </svg>
      </div>

      {/* Slide-out detail panel */}
      {selected && !embedded && (
        <div
          class="shrink-0 overflow-y-auto border-l border-border-primary bg-bg-surface"
          style={{ width: `${PANEL_WIDTH}px`, minHeight: "500px" }}
        >
          {/* Panel header */}
          <div class="flex items-center justify-between border-b border-border-primary px-4 py-3">
            <div class="min-w-0">
              <span
                class="inline-block rounded px-1.5 py-0.5 text-xs font-medium"
                style={{
                  backgroundColor: "var(--bg-elevated)",
                  color: "var(--text-secondary)",
                }}
              >
                {selected.kind}
              </span>
              <h3 class="mt-1 truncate text-sm font-semibold text-text-primary">
                {selected.name}
              </h3>
            </div>
            <button
              type="button"
              class="ml-2 shrink-0 rounded p-1 text-text-muted hover:text-text-primary"
              onClick={() => {
                selectedNode.value = null;
              }}
              aria-label="Close panel"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
              >
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </button>
          </div>

          {/* Panel body */}
          <div class="space-y-4 p-4">
            {/* Health badge */}
            <div class="flex items-center gap-2">
              <span class="text-xs text-text-muted">Health</span>
              <span
                class="inline-block rounded-full px-2 py-0.5 text-xs font-medium"
                style={{
                  backgroundColor: selected.health === "healthy"
                    ? "color-mix(in srgb, var(--status-success) 15%, transparent)"
                    : selected.health === "degraded"
                    ? "color-mix(in srgb, var(--status-warning) 15%, transparent)"
                    : selected.health === "failing"
                    ? "color-mix(in srgb, var(--status-error) 15%, transparent)"
                    : "var(--bg-elevated)",
                  color: HEALTH_COLORS[selected.health],
                }}
              >
                {selected.health}
              </span>
            </div>

            {/* Summary */}
            {selected.summary && (
              <div>
                <span class="text-xs text-text-muted">Summary</span>
                <p class="mt-1 text-sm text-text-secondary">
                  {selected.summary}
                </p>
              </div>
            )}

            {/* Quick actions */}
            <div class="space-y-2 pt-2">
              <span class="text-xs font-medium text-text-muted">
                Quick Actions
              </span>
              <a
                href={getResourceHref(
                  selected.kind,
                  selected.namespace,
                  selected.name,
                )}
                class="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-bg-elevated"
              >
                View Detail
              </a>
              {selected.kind === "Pod" && (
                <a
                  href={`/observability/logs?namespace=${
                    encodeURIComponent(selected.namespace)
                  }&pod=${encodeURIComponent(selected.name)}`}
                  class="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-bg-elevated"
                >
                  View Logs
                </a>
              )}
              <a
                href={`/observability/logs?namespace=${
                  encodeURIComponent(selected.namespace)
                }`}
                class="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-bg-elevated"
              >
                Investigate
              </a>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
