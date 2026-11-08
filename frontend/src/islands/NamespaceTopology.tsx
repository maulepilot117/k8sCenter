/** @jsxImportSource react */
import {
  type MouseEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { ErrorBanner } from "@/components/ui/ErrorBanner.tsx";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { ApiError, apiGet } from "@/lib/api.ts";
import { timeAgo } from "@/lib/timeAgo.ts";
import { withUniqueKeys } from "@/lib/unique-keys.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";
import { selectedNamespace } from "@/src/lib/namespace.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

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
  // Set by the backend when ?overlay=... is requested.
  // "mesh" / "eso-chain" — overlay applied (edges may or may not be present).
  // "unavailable" — overlay was requested but couldn't be applied
  //                  (no backing CRDs installed, provider unwired, fetch errored).
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
  type:
    | "owner"
    | "selector"
    | "mount"
    | "ingress"
    | "mesh_vs"
    | "mesh_sp"
    | "eso_auth"
    | "eso_sync"
    | "eso_consumer";
}

// ── Layout types ──

interface LayoutNode extends TopoNode {
  x: number;
  y: number;
}

// ── Constants ──

const NODE_WIDTH = 180;
const NODE_HEIGHT = 56;
const BASE_WIDTH = 1200;
const BASE_HEIGHT = 800;
const MIN_ZOOM = 0.3;
const MAX_ZOOM = 3;
const PANEL_WIDTH = 320;

type OverlayMode = "none" | "mesh" | "eso-chain";

interface NamespaceTopologyProps {
  namespace?: string;
  defaultOverlay?: OverlayMode;
  focusedNodeId?: string;
  embedded?: boolean;
  minHeight?: number;
}

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
  TopoEdge["type"],
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
    dasharray: "2,2",
    opacity: 0.8,
    stroke: "var(--accent)",
    markerId: "arrow-eso-auth",
  },
  eso_sync: {
    dasharray: "5,2",
    opacity: 0.85,
    stroke: "var(--accent-secondary)",
    markerId: "arrow-eso-sync",
  },
  eso_consumer: {
    dasharray: "",
    opacity: 0.65,
    stroke: "var(--text-muted)",
    markerId: "arrow-eso-consumer",
  },
};

function isMeshEdge(t: TopoEdge["type"]): boolean {
  return t === "mesh_vs" || t === "mesh_sp";
}

function isOverlayEdge(t: TopoEdge["type"]): boolean {
  return (
    isMeshEdge(t) ||
    t === "eso_auth" ||
    t === "eso_sync" ||
    t === "eso_consumer"
  );
}

// ── Kind abbreviations ──

const KIND_ABBREVIATIONS: Record<string, string> = {
  HorizontalPodAutoscaler: "HPA",
  PodDisruptionBudget: "PDB",
  PersistentVolumeClaim: "PVC",
  ReplicaSet: "RS",
  DaemonSet: "DS",
  StatefulSet: "STS",
  ConfigMap: "CM",
  NetworkPolicy: "NetPol",
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
    ExternalSecret: `/external-secrets/externalsecrets/${eNs}/${eName}`,
    SecretStore: `/external-secrets/stores/${eNs}/${eName}`,
    ClusterSecretStore: `/external-secrets/cluster-stores/${eName}`,
    PersistentVolumeClaim: `/storage/pvcs/${eNs}/${eName}`,
    Job: `/workloads/jobs/${eNs}/${eName}`,
    CronJob: `/workloads/cronjobs/${eNs}/${eName}`,
    ReplicaSet: `/workloads/replicasets/${eNs}/${eName}`,
  };
  return (
    kindRoutes[kind] ??
    `/resources/${encodeURIComponent(kind)}/${encodeURIComponent(ns)}/${encodeURIComponent(
      name,
    )}`
  );
}

function focusedSubgraph(graph: TopoGraph, focusedNodeId?: string): TopoGraph {
  if (!focusedNodeId) return graph;
  const nodeIds = new Set(graph.nodes.map((n) => n.id));
  if (!nodeIds.has(focusedNodeId)) {
    return { ...graph, nodes: [], edges: [] };
  }
  const adjacent = new Map<string, string[]>();
  for (const node of graph.nodes) adjacent.set(node.id, []);
  for (const edge of graph.edges) {
    if (!nodeIds.has(edge.source) || !nodeIds.has(edge.target)) continue;
    adjacent.get(edge.source)!.push(edge.target);
    adjacent.get(edge.target)!.push(edge.source);
  }
  const keep = new Set<string>([focusedNodeId]);
  const queue = [focusedNodeId];
  for (let i = 0; i < queue.length; i++) {
    const current = queue[i];
    for (const next of adjacent.get(current) ?? []) {
      if (keep.has(next)) continue;
      keep.add(next);
      queue.push(next);
    }
  }
  const edges = graph.edges.filter(
    (e) => keep.has(e.source) && keep.has(e.target),
  );
  if (edges.length === 0) {
    return { ...graph, nodes: [], edges: [] };
  }
  return {
    ...graph,
    nodes: graph.nodes.filter((n) => keep.has(n.id)),
    edges,
  };
}

// ── Component ──

/** Space kept around the graph when it is fitted to the view. */
const FIT_PADDING = 40;

/**
 * The zoom and pan that frame every node. The layout centres each column on
 * y = 0, so a tall graph extends well above it: a view fixed at (0, 0) showed
 * only its lower part, or none of it. A graph smaller than the view stays at
 * 1x and is centred; one too large even at MIN_ZOOM is centred at MIN_ZOOM.
 */
export function fitViewBox(nodes: readonly { x: number; y: number }[]): {
  zoom: number;
  panX: number;
  panY: number;
} {
  if (nodes.length === 0) return { zoom: 1, panX: 0, panY: 0 };
  let minX = Number.POSITIVE_INFINITY;
  let minY = Number.POSITIVE_INFINITY;
  let maxX = Number.NEGATIVE_INFINITY;
  let maxY = Number.NEGATIVE_INFINITY;
  for (const n of nodes) {
    minX = Math.min(minX, n.x - NODE_WIDTH / 2);
    maxX = Math.max(maxX, n.x + NODE_WIDTH / 2);
    minY = Math.min(minY, n.y - NODE_HEIGHT / 2);
    maxY = Math.max(maxY, n.y + NODE_HEIGHT / 2);
  }
  const width = maxX - minX + 2 * FIT_PADDING;
  const height = maxY - minY + 2 * FIT_PADDING;
  const zoom = Math.max(
    MIN_ZOOM,
    Math.min(1, BASE_WIDTH / width, BASE_HEIGHT / height),
  );
  return {
    zoom,
    panX: (minX + maxX) / 2 - BASE_WIDTH / zoom / 2,
    panY: (minY + maxY) / 2 - BASE_HEIGHT / zoom / 2,
  };
}

/**
 * Custom left-to-right topological layout (no dagre: it uses Node.js
 * builtins). Columns are the longest path from a root, each centred on y = 0.
 */
function layoutGraph(graph: TopoGraph): LayoutNode[] {
  if (graph.nodes.length === 0) return [];
  const nodes = graph.nodes;
  const edges = graph.edges;
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
    const parentLayers = (parents.get(id) ?? []).map((pid) => assignLayer(pid));
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
  return positioned;
}

export default function NamespaceTopology({
  namespace,
  defaultOverlay = "none",
  focusedNodeId,
  embedded = false,
  minHeight,
}: NamespaceTopologyProps) {
  const isClient = useIsClient();
  const storeNamespace = useSignalValue(selectedNamespace);
  const ns = namespace ?? storeNamespace;
  const [graph, setGraph] = useState<TopoGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // The backend refuses topology under a remote cluster selection with
  // reason unsupported_platform (#532): the graph comes from the local
  // cluster's informers. That is a fixed fact about the feature, not a
  // failure, so it gets its own state with no Retry.
  const [remoteUnsupported, setRemoteUnsupported] = useState(false);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [hoveredNode, setHoveredNode] = useState<string | null>(null);
  const [view, setView] = useState({ zoom: 1, panX: 0, panY: 0 });
  const [dragging, setDragging] = useState(false);
  const [overlayMode, setOverlayMode] = useState<OverlayMode>(defaultOverlay);
  const [unavailableOverlay, setUnavailableOverlay] =
    useState<OverlayMode | null>(null);

  const svgRef = useRef<SVGSVGElement>(null);
  // The wheel listener and the drag session run later than the render that
  // created them, so they read the current view from a ref. The handlers
  // write it too, so two events before the next render compound instead of
  // overwriting each other.
  const viewRef = useLatest(view);
  const dragRef = useRef({
    active: false,
    startX: 0,
    startY: 0,
    panX: 0,
    panY: 0,
  });
  const latestRef = useLatest({ ns, overlayMode, focusedNodeId });

  const layoutNodes = useMemo(() => (graph ? layoutGraph(graph) : []), [graph]);
  const layoutNodeMap = useMemo(
    () => new Map(layoutNodes.map((n) => [n.id, n])),
    [layoutNodes],
  );
  // A full page sizes the graph to the window (see the SVG's height), so its
  // floors are low enough not to override that on a short window; they only
  // stop the graph collapsing on a very short one.
  const panelMinHeight = minHeight ?? (embedded ? 400 : 320);
  const svgMinHeight = embedded ? 460 : 280;

  // ── Data fetching ──

  const fetchGraph = useCallback(
    async (target: string, overlay: OverlayMode, focus?: string) => {
      if (target === "all") return;
      setLoading(true);
      setError(null);
      setRemoteUnsupported(false);
      try {
        const overlayParam = overlay === "none" ? "" : `?overlay=${overlay}`;
        const res = await apiGet<TopoGraph>(
          `/v1/topology/${target}${overlayParam}`,
        );
        setGraph(focusedSubgraph(res.data, focus));
        if (res.data.overlay === "unavailable") {
          setUnavailableOverlay(overlay);
          setOverlayMode("none");
        } else if (res.data.overlay === latestRef.current.overlayMode) {
          setUnavailableOverlay(null);
        }
      } catch (err) {
        setGraph(null);
        if (err instanceof ApiError && err.reason === "unsupported_platform") {
          setRemoteUnsupported(true);
          return;
        }
        setError(
          err instanceof Error ? err.message : "Failed to fetch topology",
        );
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    if (!IS_BROWSER) return;
    if (ns === "all") {
      setLoading(false);
      return;
    }
    setUnavailableOverlay(null);
    fetchGraph(ns, latestRef.current.overlayMode, focusedNodeId);
  }, [ns, focusedNodeId, fetchGraph]);

  useEffect(() => {
    if (!IS_BROWSER) return;
    const current = latestRef.current;
    if (current.ns === "all") return;
    fetchGraph(current.ns, overlayMode, current.focusedNodeId);
  }, [overlayMode, fetchGraph]);

  // A new layout is framed to fit.
  useEffect(() => {
    setView(fitViewBox(layoutNodes));
  }, [layoutNodes]);

  const showGraph =
    isClient &&
    ns !== "all" &&
    !loading &&
    !remoteUnsupported &&
    !error &&
    graph !== null &&
    graph.nodes.length > 0;

  // React attaches onWheel as a passive listener, where preventDefault does
  // nothing and the page would scroll while zooming, so the listener is
  // native and non-passive.
  useEffect(() => {
    const svg = svgRef.current;
    if (!showGraph || !svg) return;
    const handleWheel = (e: WheelEvent) => {
      e.preventDefault();
      const { zoom, panX, panY } = viewRef.current;
      const delta = e.deltaY > 0 ? -0.1 : 0.1;
      const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom + delta));

      const rect = svg.getBoundingClientRect();
      const mx = (e.clientX - rect.left) / rect.width;
      const my = (e.clientY - rect.top) / rect.height;

      const oldW = BASE_WIDTH / zoom;
      const oldH = BASE_HEIGHT / zoom;
      const newW = BASE_WIDTH / nextZoom;
      const newH = BASE_HEIGHT / nextZoom;

      const next = {
        zoom: nextZoom,
        panX: panX + (oldW - newW) * mx,
        panY: panY + (oldH - newH) * my,
      };
      viewRef.current = next;
      setView(next);
    };
    svg.addEventListener("wheel", handleWheel, { passive: false });
    return () => svg.removeEventListener("wheel", handleWheel);
  }, [showGraph]);

  // ── Connectivity sets for hover highlight ──

  function getConnectedIds(nodeId: string): Set<string> {
    const connected = new Set<string>([nodeId]);
    if (!graph) return connected;
    for (const edge of graph.edges) {
      if (edge.source === nodeId) connected.add(edge.target);
      if (edge.target === nodeId) connected.add(edge.source);
    }
    return connected;
  }

  // ── Pan handlers ──

  const handleMouseDown = (e: MouseEvent<SVGSVGElement>) => {
    if (e.button !== 0) return;
    dragRef.current = {
      active: true,
      startX: e.clientX,
      startY: e.clientY,
      panX: viewRef.current.panX,
      panY: viewRef.current.panY,
    };
    setDragging(true);
  };

  const handleMouseMove = (e: MouseEvent<SVGSVGElement>) => {
    const drag = dragRef.current;
    if (!drag.active) return;
    const svg = svgRef.current;
    if (!svg) return;

    const rect = svg.getBoundingClientRect();
    const { zoom } = viewRef.current;
    const scaleX = BASE_WIDTH / zoom / rect.width;
    const scaleY = BASE_HEIGHT / zoom / rect.height;

    const next = {
      zoom,
      panX: drag.panX - (e.clientX - drag.startX) * scaleX,
      panY: drag.panY - (e.clientY - drag.startY) * scaleY,
    };
    viewRef.current = next;
    setView(next);
  };

  const handleMouseUp = () => {
    dragRef.current.active = false;
    setDragging(false);
  };

  const fitToView = () => setView(fitViewBox(layoutNodes));

  // ── Render helpers ──

  const vbW = BASE_WIDTH / view.zoom;
  const vbH = BASE_HEIGHT / view.zoom;

  const connectedSet = hoveredNode ? getConnectedIds(hoveredNode) : null;

  function nodeOpacity(id: string): number {
    if (!connectedSet) return 1;
    return connectedSet.has(id) ? 1 : 0.15;
  }

  function edgeOpacity(source: string, target: string, base: number): number {
    if (!connectedSet) return base;
    return connectedSet.has(source) && connectedSet.has(target) ? base : 0.08;
  }

  // ── Loading (also the server and hydration pass) ──

  const loadingPanel = (
    <div className="flex items-center justify-center rounded-lg border border-border-primary bg-surface p-12">
      <Spinner className="text-accent" />
      <span className="ml-3 text-text-secondary">Loading topology...</span>
    </div>
  );

  // ── No namespace selected ──

  if (!isClient) return loadingPanel;

  if (ns === "all") {
    return (
      <div className="flex items-center justify-center rounded-lg border border-border-primary bg-surface p-12">
        <p className="text-text-secondary">
          Select a namespace to view its resource topology.
        </p>
      </div>
    );
  }

  if (loading) return loadingPanel;

  // ── Unsupported on a remote cluster ──

  if (remoteUnsupported) {
    return (
      <div data-topology-state="remote-unsupported">
        <Alert variant="info">
          Resource topology is not available for remote clusters. The graph is
          built from the local cluster's live resource cache, which remote
          clusters do not have. Switch to the local cluster to view it.
        </Alert>
      </div>
    );
  }

  // ── Error ──

  if (error) {
    return (
      <div className="space-y-4">
        <ErrorBanner message={error} />
        <button
          type="button"
          className="rounded bg-accent px-3 py-1.5 text-sm font-medium hover:opacity-90"
          style={{ color: "var(--bg-base)" }}
          onClick={() => fetchGraph(ns, overlayMode, focusedNodeId)}
        >
          Retry
        </button>
      </div>
    );
  }

  // ── Empty state ──

  if (!graph || graph.nodes.length === 0) {
    return (
      <div className="flex items-center justify-center rounded-lg border border-border-primary bg-surface p-12">
        <p className="text-text-secondary">
          No resources found in this namespace.
        </p>
      </div>
    );
  }

  // ── Selected node data ──

  const selected = selectedNode
    ? (graph.nodes.find((n) => n.id === selectedNode) ?? null)
    : null;

  // ── Main render ──

  return (
    <div className="relative flex gap-0">
      {/* Graph area */}
      <div
        className="flex-1 overflow-hidden rounded-lg border border-border-primary bg-surface"
        style={{ minHeight: `${panelMinHeight}px` }}
      >
        {/* Toolbar */}
        <div className="flex items-center justify-between border-b border-border-primary px-4 py-2">
          <div className="flex items-center gap-2">
            <button
              type="button"
              className="rounded border border-border-primary bg-surface px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              onClick={fitToView}
            >
              Fit to view
            </button>
            <button
              type="button"
              className="rounded border border-border-primary bg-surface px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              onClick={() => fetchGraph(ns, overlayMode, focusedNodeId)}
            >
              Refresh
            </button>
            <div
              role="radiogroup"
              aria-label="Topology overlay"
              className="ml-2 inline-flex overflow-hidden rounded border border-border-primary"
            >
              {(
                [
                  ["none", "Base"],
                  ["mesh", "Mesh"],
                  ["eso-chain", "ESO chain"],
                ] as Array<[OverlayMode, string]>
              ).map(([mode, label]) => {
                const disabled = unavailableOverlay === mode;
                const active = overlayMode === mode;
                return (
                  <button
                    key={mode}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    disabled={disabled}
                    title={
                      disabled
                        ? mode === "eso-chain"
                          ? "ESO not installed in this cluster"
                          : "No service mesh detected in this cluster"
                        : label
                    }
                    className={`px-2.5 py-1 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                      active
                        ? "bg-accent"
                        : "bg-surface text-text-secondary hover:text-text-primary"
                    }`}
                    style={active ? { color: "var(--bg-base)" } : undefined}
                    onClick={() => setOverlayMode(mode)}
                  >
                    {label}
                  </button>
                );
              })}
            </div>
          </div>
          <div className="flex items-center gap-3">
            {overlayMode === "mesh" && graph.overlay === "mesh" && (
              <div className="flex items-center gap-3 text-xs text-text-muted">
                <span className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent)" }}
                    aria-hidden="true"
                  />
                  Istio
                </span>
                <span className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent-secondary)" }}
                    aria-hidden="true"
                  />
                  Linkerd
                </span>
              </div>
            )}
            {overlayMode === "eso-chain" && graph.overlay === "eso-chain" && (
              <div className="flex items-center gap-3 text-xs text-text-muted">
                <span className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent)" }}
                    aria-hidden="true"
                  />
                  Auth
                </span>
                <span className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--accent-secondary)" }}
                    aria-hidden="true"
                  />
                  Sync
                </span>
                <span className="flex items-center gap-1.5">
                  <span
                    className="inline-block h-0.5 w-4"
                    style={{ backgroundColor: "var(--text-muted)" }}
                    aria-hidden="true"
                  />
                  Consumer
                </span>
              </div>
            )}
            {graph.computedAt && (
              <span className="text-xs text-text-muted">
                Updated {timeAgo(graph.computedAt)}
              </span>
            )}
          </div>
        </div>

        {/* SVG Graph */}
        <svg
          ref={svgRef}
          width="100%"
          height="100%"
          viewBox={`${view.panX} ${view.panY} ${vbW} ${vbH}`}
          preserveAspectRatio="xMidYMid meet"
          aria-label="Namespace resource topology graph"
          style={{
            cursor: dragging ? "grabbing" : "grab",
            minHeight: `${svgMinHeight}px`,
            // Without a height the SVG takes its viewBox's aspect ratio and
            // on a wide page runs past the bottom of the window, so a fitted
            // graph still needs a page scroll. A full page caps it to the
            // space below the page header and the graph toolbar.
            ...(embedded ? {} : { height: "calc(100dvh - 260px)" }),
          }}
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
              <path d="M0,0 L10,3 L0,6 Z" fill="var(--text-muted)" />
            </marker>
          </defs>

          {/* Edges */}
          {withUniqueKeys(
            graph.edges,
            (e) => `${e.type}:${e.source}:${e.target}`,
          ).map(({ item: edge, key }) => {
            const src = layoutNodeMap.get(edge.source);
            const tgt = layoutNodeMap.get(edge.target);
            if (!src || !tgt) return null;
            // Backend EdgeType is a Go string typedef (open enum); a new
            // type added server-side would land in the response before
            // any frontend-side TS update. Fall back to the owner style
            // so an unknown edge renders as a generic dependency line
            // rather than crashing on style.dasharray of undefined.
            const style = EDGE_STYLES[edge.type] ?? EDGE_STYLES.owner;
            const op = edgeOpacity(edge.source, edge.target, style.opacity);
            const stroke = style.stroke ?? "var(--border-primary)";
            const markerId =
              style.markerId ??
              (edge.type === "mount" ? "arrow-mount" : "arrow-default");
            return (
              <line
                key={key}
                x1={src.x}
                y1={src.y}
                x2={tgt.x}
                y2={tgt.y}
                stroke={stroke}
                strokeWidth={isOverlayEdge(edge.type) ? 2 : 1.5}
                strokeDasharray={style.dasharray}
                opacity={op}
                markerEnd={`url(#${markerId})`}
              />
            );
          })}

          {/* Nodes */}
          {layoutNodes.map((node) => {
            const healthColor = HEALTH_COLORS[node.health];
            const op = nodeOpacity(node.id);
            const isSelected = selectedNode === node.id;
            return (
              <g
                key={node.id}
                className={node.health === "failing" ? "ns-topo-failing" : ""}
                transform={`translate(${node.x - NODE_WIDTH / 2}, ${
                  node.y - NODE_HEIGHT / 2
                })`}
                opacity={op}
                style={{ cursor: "pointer" }}
                onMouseEnter={() => setHoveredNode(node.id)}
                onMouseLeave={() => setHoveredNode(null)}
                onClick={() =>
                  setSelectedNode((prev) => (prev === node.id ? null : node.id))
                }
              >
                <rect
                  width={NODE_WIDTH}
                  height={NODE_HEIGHT}
                  rx={8}
                  fill="var(--bg-surface)"
                  stroke={healthColor}
                  strokeWidth={isSelected ? 2.5 : 1.5}
                />
                {/* Kind label */}
                <text
                  x={10}
                  y={20}
                  fontSize="10"
                  fill="var(--text-muted)"
                  fontFamily="var(--font-mono, monospace)"
                >
                  {displayKind(node.kind)}
                </text>
                {/* Resource name */}
                <text
                  x={10}
                  y={40}
                  fontSize="13"
                  fontWeight="600"
                  fill="var(--text-primary)"
                  fontFamily="var(--font-sans, sans-serif)"
                >
                  {node.name.length > 20
                    ? `${node.name.substring(0, 18)}...`
                    : node.name}
                </text>
              </g>
            );
          })}
        </svg>
      </div>

      {/* Slide-out detail panel */}
      {selected && (
        <div
          className="shrink-0 overflow-y-auto border-l border-border-primary bg-surface"
          style={{
            width: `${PANEL_WIDTH}px`,
            minHeight: `${panelMinHeight}px`,
          }}
        >
          {/* Panel header */}
          <div className="flex items-center justify-between border-b border-border-primary px-4 py-3">
            <div className="min-w-0">
              <span
                className="inline-block rounded px-1.5 py-0.5 text-xs font-medium"
                style={{
                  backgroundColor: "var(--bg-elevated)",
                  color: "var(--text-secondary)",
                }}
              >
                {selected.kind}
              </span>
              <h3 className="mt-1 truncate text-sm font-semibold text-text-primary">
                {selected.name}
              </h3>
            </div>
            <button
              type="button"
              className="ml-2 shrink-0 rounded p-1 text-text-muted hover:text-text-primary"
              onClick={() => setSelectedNode(null)}
              aria-label="Close panel"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </button>
          </div>

          {/* Panel body */}
          <div className="space-y-4 p-4">
            {/* Health badge */}
            <div className="flex items-center gap-2">
              <span className="text-xs text-text-muted">Health</span>
              <span
                className="inline-block rounded-full px-2 py-0.5 text-xs font-medium"
                style={{
                  backgroundColor:
                    selected.health === "healthy"
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
                <span className="text-xs text-text-muted">Summary</span>
                <p className="mt-1 text-sm text-text-secondary">
                  {selected.summary}
                </p>
              </div>
            )}

            {/* Quick actions */}
            <div className="space-y-2 pt-2">
              <span className="text-xs font-medium text-text-muted">
                Quick Actions
              </span>
              <a
                href={getResourceHref(
                  selected.kind,
                  selected.namespace,
                  selected.name,
                )}
                className="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-elevated"
              >
                View Detail
              </a>
              {selected.kind === "Pod" && (
                <a
                  href={`/observability/logs?namespace=${encodeURIComponent(
                    selected.namespace,
                  )}&pod=${encodeURIComponent(selected.name)}`}
                  className="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-elevated"
                >
                  View Logs
                </a>
              )}
              <a
                href={`/observability/logs?namespace=${encodeURIComponent(
                  selected.namespace,
                )}`}
                className="block rounded border border-border-primary px-3 py-2 text-sm text-text-primary hover:bg-elevated"
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
