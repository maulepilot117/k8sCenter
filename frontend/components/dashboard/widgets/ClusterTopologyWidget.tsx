import type { JSX } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import WidgetShell from "@/components/ui/WidgetShell.tsx";
import { dashboardData } from "@/lib/dashboard/data.ts";
import { registerWidget } from "@/lib/dashboard/registry.ts";
// The joins, the health grading, the layout and the per-row coverage live in
// lib/, under test (D-10, KTD8). This file only draws what they return.
import type {
  TopologyEdgeKind,
  TopologyHealth,
  TopologyKind,
  TopologyNode,
  TopologyView,
} from "@/lib/dashboard/topology.ts";
import {
  clusterTopologyView,
  stepTopologyZoom,
  TOPOLOGY_KIND_LABEL,
  TOPOLOGY_KINDS,
  TOPOLOGY_MIN_ZOOM,
  TOPOLOGY_PAGE_HREF,
  topologyMaxZoom,
} from "@/lib/dashboard/topology.ts";
import type { DataSourceKey } from "@/lib/dashboard/types.ts";
import type { ResourceListPage } from "@/lib/dashboard/wire-types.ts";

const KIND_COLOR: Readonly<Record<TopologyKind, string>> = {
  node: "var(--accent)",
  service: "var(--accent-secondary)",
  workload: "var(--info)",
  pod: "var(--success)",
  pvc: "var(--warning)",
};

const EDGE_COLOR: Readonly<Record<TopologyEdgeKind, string>> = {
  "node-service": "var(--accent)",
  "service-workload": "var(--accent-secondary)",
  "service-pod": "var(--accent-secondary)",
  "workload-pod": "var(--info)",
  "pod-pvc": "var(--warning)",
};

const HEALTH_COLOR: Readonly<Record<TopologyHealth, string>> = {
  healthy: "var(--success)",
  warning: "var(--warning)",
  error: "var(--error)",
};

/** The reads behind each row. Workloads are three controller kinds. */
const ROW_SOURCES: Readonly<Record<TopologyKind, readonly DataSourceKey[]>> = {
  node: ["nodes-list"],
  service: ["services-list"],
  workload: ["deployments-list", "statefulsets-list", "daemonsets-list"],
  pod: ["pods-list"],
  pvc: ["pvcs-list"],
};

/** Pixels a press must travel before it is a pan rather than a click. */
const DRAG_THRESHOLD = 4;
const LABEL_CHARS = 15;

interface Camera {
  zoom: number;
  x: number;
  y: number;
}

interface Active {
  id: string;
  /** Pointer position inside the canvas box, in CSS pixels. */
  x: number;
  y: number;
  /** The canvas box at the time, so the panel can open away from the edge. */
  w: number;
  h: number;
}

/**
 * The cluster as one picture: nodes, the services they carry traffic for, the
 * workloads behind those services, their pods, and the claims the pods mount.
 *
 * The map the Overview carried before the Liquid Glass redesign, rebuilt as a
 * catalog widget. Cluster-wide like the original, which is why every list it
 * reads can come back as a capped page and the card says so rather than
 * drawing the first 500 pods as if they were all of them. The namespace-scoped,
 * server-built graph stays on the topology page this card links to.
 *
 * Only the pod list is required. Nodes are cluster-scoped and services, claims
 * and controllers are each separately RBAC-gated, so an account that can see
 * pods but not nodes still gets a map -- without its top row, and with a note
 * naming the row that is missing rather than an empty band that reads as a
 * cluster with no nodes.
 *
 * Wheel zoom needs Ctrl (or Cmd): a dashboard is a scrolling page, and a card
 * that captured every wheel event would trap the scroll whenever the pointer
 * crossed it.
 */
function ClusterTopology() {
  const read = (key: DataSourceKey) =>
    dashboardData.state<ResourceListPage>(key).data;
  const nodes = read("nodes-list");
  const services = read("services-list");
  const deployments = read("deployments-list");
  const statefulsets = read("statefulsets-list");
  const daemonsets = read("daemonsets-list");
  const pods = read("pods-list");
  const pvcs = read("pvcs-list");

  const view = useMemo(
    () =>
      clusterTopologyView({
        nodes,
        services,
        deployments,
        statefulsets,
        daemonsets,
        pods,
        pvcs,
      }),
    [nodes, services, deployments, statefulsets, daemonsets, pods, pvcs],
  );

  return (
    <WidgetShell
      title="Cluster Topology"
      action={
        <a
          href={TOPOLOGY_PAGE_HREF}
          class="text-xs text-accent no-underline"
          data-testid="cluster-topology-link"
        >
          Open topology →
        </a>
      }
    >
      <div class="flex h-full min-h-[220px] flex-col gap-2">
        {view.nodes.length === 0 ? (
          <p
            data-testid={
              view.rows.pod.readable
                ? "cluster-topology-empty"
                : "cluster-topology-unreadable"
            }
            class="flex flex-1 items-center justify-center py-4 text-center text-xs text-text-muted"
          >
            {view.rows.pod.readable
              ? "There is nothing on this cluster this account can draw."
              : "The pod list returned a result this card cannot read."}
          </p>
        ) : (
          <TopologyCanvas view={view} />
        )}
        <Legend view={view} />
        <CoverageNotes view={view} />
      </div>
    </WidgetShell>
  );
}

function TopologyCanvas({ view }: { view: TopologyView }) {
  const [camera, setCamera] = useState<Camera>({ zoom: 1, x: 0, y: 0 });
  const [active, setActive] = useState<Active | null>(null);
  const [dragging, setDragging] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);
  const press = useRef<{
    id: number;
    sx: number;
    sy: number;
    cam: Camera;
    dragging: boolean;
  } | null>(null);
  const suppressClick = useRef(false);

  const byId = useMemo(() => new Map(view.nodes.map((n) => [n.id, n])), [view]);
  const activeNode = active === null ? undefined : byId.get(active.id);

  // The zoom ceiling depends on how small the fitted map is drawn, so the box
  // is measured. Debounced like WidgetHost's observer: a grid resize fires
  // every frame and each measurement re-renders every shape.
  const [box, setBox] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    let resizeTimer: ReturnType<typeof setTimeout> | undefined;
    const measure = () => setBox({ w: el.clientWidth, h: el.clientHeight });
    const ro = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = globalThis.setTimeout(measure, 100);
    });
    ro.observe(el);
    measure();
    return () => {
      clearTimeout(resizeTimer);
      ro.disconnect();
    };
  }, []);
  const maxZoom = topologyMaxZoom(view, box.w, box.h);

  const vbW = view.width / camera.zoom;
  const vbH = view.height / camera.zoom;

  /** Zooms to `next`, keeping the point at fractions (fx, fy) of the canvas
   * where it is on screen. */
  const zoomTo = (next: number, fx = 0.5, fy = 0.5) => {
    setCamera((c) => {
      const zoom = Math.min(maxZoom, Math.max(TOPOLOGY_MIN_ZOOM, next));
      const oldW = view.width / c.zoom;
      const oldH = view.height / c.zoom;
      const newW = view.width / zoom;
      const newH = view.height / zoom;
      return {
        zoom,
        x: c.x + (oldW - newW) * fx,
        y: c.y + (oldH - newH) * fy,
      };
    });
  };

  const onWheel = (e: WheelEvent) => {
    if (!e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    const rect = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
    zoomTo(
      stepTopologyZoom(camera.zoom, e.deltaY > 0 ? -1 : 1, maxZoom),
      (e.clientX - rect.left) / rect.width,
      (e.clientY - rect.top) / rect.height,
    );
  };

  const onPointerDown = (e: PointerEvent) => {
    if (e.button !== 0) return;
    press.current = {
      id: e.pointerId,
      sx: e.clientX,
      sy: e.clientY,
      cam: camera,
      dragging: false,
    };
  };

  const onPointerMove = (e: PointerEvent) => {
    const p = press.current;
    if (p === null || p.id !== e.pointerId) return;
    const dx = e.clientX - p.sx;
    const dy = e.clientY - p.sy;
    if (!p.dragging) {
      if (Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      // Captured only once the press is a pan. Capturing on pointerdown would
      // retarget the click to the svg and stop every node link working.
      p.dragging = true;
      (e.currentTarget as Element).setPointerCapture(e.pointerId);
      setDragging(true);
      setActive(null);
    }
    const rect = (e.currentTarget as SVGSVGElement).getBoundingClientRect();
    setCamera({
      zoom: p.cam.zoom,
      x: p.cam.x - dx * (vbW / rect.width),
      y: p.cam.y - dy * (vbH / rect.height),
    });
  };

  const endPress = (e: PointerEvent) => {
    const p = press.current;
    if (p === null || p.id !== e.pointerId) return;
    suppressClick.current = p.dragging;
    press.current = null;
    setDragging(false);
  };

  const showFor = (id: string, clientX: number, clientY: number) => {
    const box = boxRef.current?.getBoundingClientRect();
    if (!box) return;
    setActive({
      id,
      x: clientX - box.left,
      y: clientY - box.top,
      w: box.width,
      h: box.height,
    });
  };

  const isRelated = (edgeFrom: string, edgeTo: string) =>
    active !== null && (edgeFrom === active.id || edgeTo === active.id);

  return (
    <div
      ref={boxRef}
      class="relative min-h-0 flex-1 overflow-hidden rounded-md border border-border-subtle bg-base"
      data-testid="cluster-topology-canvas"
    >
      <svg
        role="group"
        aria-label={`Cluster topology: ${view.nodes.length} resources, ${view.edges.length} relationships`}
        width="100%"
        height="100%"
        viewBox={`${camera.x} ${camera.y} ${vbW} ${vbH}`}
        preserveAspectRatio="xMidYMid meet"
        class={`block h-full w-full touch-none select-none ${
          dragging ? "cursor-grabbing" : "cursor-grab"
        }`}
        onWheel={onWheel}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endPress}
        onPointerCancel={endPress}
        onClickCapture={(e) => {
          if (suppressClick.current) {
            e.preventDefault();
            e.stopPropagation();
            suppressClick.current = false;
          }
        }}
      >
        <title>Ctrl + scroll to zoom, drag to pan</title>
        {view.edges.map((edge) => {
          const from = byId.get(edge.from);
          const to = byId.get(edge.to);
          if (!from || !to) return null;
          const lit = isRelated(edge.from, edge.to);
          return (
            <line
              key={`${edge.from}>${edge.to}`}
              x1={from.x}
              y1={from.y}
              x2={to.x}
              y2={to.y}
              stroke={EDGE_COLOR[edge.kind]}
              stroke-width={lit ? 2.5 : 1.5}
              stroke-opacity={active === null ? 0.35 : lit ? 0.9 : 0.12}
            />
          );
        })}
        {view.nodes.map((node) => (
          <NodeGlyph
            key={node.id}
            node={node}
            active={active?.id === node.id}
            onEnter={(e) => showFor(node.id, e.clientX, e.clientY)}
            onFocus={(el) => {
              const r = el.getBoundingClientRect();
              showFor(node.id, r.left + r.width / 2, r.top + r.height / 2);
            }}
            onLeave={() => setActive(null)}
          />
        ))}
      </svg>

      <div class="absolute right-1.5 top-1.5 flex gap-1">
        <CanvasButton
          label="Zoom in"
          disabled={camera.zoom >= maxZoom}
          onClick={() => zoomTo(stepTopologyZoom(camera.zoom, 1, maxZoom))}
        >
          +
        </CanvasButton>
        <CanvasButton
          label="Zoom out"
          disabled={camera.zoom <= TOPOLOGY_MIN_ZOOM}
          onClick={() => zoomTo(stepTopologyZoom(camera.zoom, -1, maxZoom))}
        >
          −
        </CanvasButton>
        <CanvasButton
          label="Reset view"
          onClick={() => setCamera({ zoom: 1, x: 0, y: 0 })}
        >
          ⟲
        </CanvasButton>
      </div>

      {camera.zoom !== 1 && (
        <span class="pointer-events-none absolute bottom-1 right-2 text-[10px] text-text-muted">
          {Math.round(camera.zoom * 100)}%
        </span>
      )}

      {active !== null && activeNode !== undefined && (
        <DetailPanel node={activeNode} at={active} />
      )}
    </div>
  );
}

function CanvasButton({
  label,
  disabled = false,
  onClick,
  children,
}: {
  label: string;
  disabled?: boolean;
  onClick: () => void;
  children: string;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      class="flex h-6 w-6 items-center justify-center rounded border border-border-subtle bg-elevated text-xs text-text-secondary enabled:hover:bg-hover enabled:hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-40"
    >
      {children}
    </button>
  );
}

function NodeGlyph({
  node,
  active,
  onEnter,
  onFocus,
  onLeave,
}: {
  node: TopologyNode;
  active: boolean;
  onEnter: (e: PointerEvent) => void;
  onFocus: (el: Element) => void;
  onLeave: () => void;
}) {
  const r = node.size / 2;
  const kindColor = KIND_COLOR[node.kind];
  // A healthy object wears its kind's colour; anything else wears the health
  // colour, so a failing pod is red in a row of green rather than a
  // slightly different green.
  const ring =
    node.health === "healthy" ? kindColor : HEALTH_COLOR[node.health];
  const fill = `color-mix(in srgb, ${ring} ${active ? 22 : 11}%, transparent)`;
  const label =
    node.name.length > LABEL_CHARS
      ? `${node.name.slice(0, LABEL_CHARS - 1)}…`
      : node.name;
  const square = node.kind === "workload" || node.kind === "pvc";
  const title =
    node.namespace === ""
      ? `${node.name} (${node.status})`
      : `${node.namespace}/${node.name} (${node.status})`;

  return (
    <a
      href={node.href}
      aria-label={title}
      class="outline-none"
      onFocus={(e) => onFocus(e.currentTarget as Element)}
      onBlur={onLeave}
    >
      <g
        transform={`translate(${node.x}, ${node.y})`}
        onPointerEnter={onEnter}
        onPointerLeave={onLeave}
        class="cursor-pointer"
      >
        {square ? (
          <rect
            x={-r}
            y={-r}
            width={node.size}
            height={node.size}
            rx={node.kind === "workload" ? 8 : 6}
            fill={fill}
            stroke={ring}
            stroke-width={active ? 3 : 2}
          />
        ) : (
          <circle
            r={r}
            fill={fill}
            stroke={ring}
            stroke-width={active ? 3 : 2}
          />
        )}
        <text
          text-anchor="middle"
          dy="0.35em"
          fill={kindColor}
          font-size={Math.max(9, node.size * 0.22)}
          font-weight="700"
          class="pointer-events-none"
        >
          {node.abbr}
        </text>
        <text
          text-anchor="middle"
          y={r + 14}
          fill="var(--text-muted)"
          font-size="9"
          class="pointer-events-none"
        >
          {label}
        </text>
        {node.containers.length > 1 && (
          <g class="pointer-events-none">
            <circle cx={r - 4} cy={-r + 4} r="7" fill="var(--accent)" />
            <text
              x={r - 4}
              y={-r + 4}
              text-anchor="middle"
              dy="0.35em"
              font-size="8"
              font-weight="700"
              fill="var(--bg-base)"
            >
              {node.containers.length}
            </text>
          </g>
        )}
      </g>
    </a>
  );
}

/**
 * The hovered or focused object, its status and what it is connected to.
 *
 * Opens on whichever side of the pointer has more room, so it never has to be
 * measured to stay inside the canvas.
 */
function DetailPanel({ node, at }: { node: TopologyNode; at: Active }) {
  const OFFSET = 14;
  const style: JSX.CSSProperties = {
    ...(at.x > at.w / 2
      ? { right: `${Math.max(0, at.w - at.x + OFFSET)}px` }
      : { left: `${at.x + OFFSET}px` }),
    ...(at.y > at.h / 2
      ? { bottom: `${Math.max(0, at.h - at.y + OFFSET)}px` }
      : { top: `${at.y + OFFSET}px` }),
  };
  const kindLabel =
    node.kind === "workload"
      ? node.workloadKind
      : TOPOLOGY_KIND_LABEL[node.kind].replace(/s$/, "");

  return (
    <div
      data-testid="cluster-topology-detail"
      style={style}
      class="pointer-events-none absolute z-10 w-60 max-w-[calc(100%-1rem)] overflow-hidden rounded-md border border-border-subtle bg-elevated text-[11px] shadow-lg"
    >
      <div class="flex items-center gap-2 border-b border-border-subtle px-2.5 py-2">
        <span
          class="shrink-0 rounded px-1 py-0.5 font-mono text-[9px] font-bold uppercase"
          style={{
            color: KIND_COLOR[node.kind],
            background: `color-mix(in srgb, ${KIND_COLOR[node.kind]} 14%, transparent)`,
          }}
        >
          {kindLabel}
        </span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-xs font-semibold text-text-primary">
            {node.name}
          </span>
          {node.namespace !== "" && (
            <span class="block truncate text-[10px] text-text-muted">
              {node.namespace}
            </span>
          )}
        </span>
        <span
          class="h-2 w-2 shrink-0 rounded-full"
          style={{ background: HEALTH_COLOR[node.health] }}
        />
      </div>
      <div class="flex flex-col gap-1.5 px-2.5 py-2 text-text-secondary">
        <span>
          <span class="text-text-muted">Status </span>
          <span style={{ color: HEALTH_COLOR[node.health] }}>
            {node.status}
          </span>
        </span>
        {node.nodeName !== "" && (
          <span>
            <span class="text-text-muted">Node </span>
            {node.nodeName}
          </span>
        )}
        {node.volumeName !== "" && (
          <span>
            <span class="text-text-muted">Volume </span>
            {node.volumeName}
          </span>
        )}
        {node.containers.length > 0 && (
          <span class="truncate">
            <span class="text-text-muted">
              {node.containers.length === 1 ? "Container " : "Containers "}
            </span>
            <span class="font-mono">{node.containers.join(", ")}</span>
          </span>
        )}
        {node.related.map((group) => (
          <div key={group.kind} class="border-t border-border-subtle pt-1.5">
            <span
              class="block text-[9px] font-semibold uppercase tracking-wide"
              style={{ color: KIND_COLOR[group.kind] }}
            >
              {TOPOLOGY_KIND_LABEL[group.kind]}{" "}
              <span class="font-normal text-text-muted">{group.total}</span>
            </span>
            <span class="mt-1 flex flex-wrap gap-1">
              {group.items.map((item) => (
                <span
                  key={item}
                  class="max-w-[9rem] truncate rounded bg-hover px-1.5 font-mono text-[10px]"
                >
                  {item}
                </span>
              ))}
              {group.total > group.items.length && (
                <span class="text-[10px] text-text-muted">
                  +{group.total - group.items.length} more
                </span>
              )}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function Legend({ view }: { view: TopologyView }) {
  return (
    <div
      class="flex shrink-0 flex-wrap gap-x-3 gap-y-1 text-[11px] text-text-muted"
      data-testid="cluster-topology-legend"
    >
      {TOPOLOGY_KINDS.map((kind) => (
        <span key={kind} class="flex items-center gap-1">
          <span
            class="h-2 w-2 rounded-full"
            style={{ background: KIND_COLOR[kind] }}
          />
          {TOPOLOGY_KIND_LABEL[kind]}{" "}
          <span class="text-text-secondary">
            {view.rows[kind].available ? view.rows[kind].counted : "—"}
          </span>
        </span>
      ))}
    </div>
  );
}

/**
 * What the picture leaves out, said in words. An undrawn row and a capped row
 * both look like a smaller cluster, which is the one reading this card must
 * not invite.
 */
function CoverageNotes({ view }: { view: TopologyView }) {
  const missing = TOPOLOGY_KINDS.filter((k) => !view.rows[k].available).map(
    (k) => {
      const states = ROW_SOURCES[k].map((s) => dashboardData.state(s));
      const why = states.some((s) => s.loading)
        ? "loading"
        : states.every((s) => s.errorKind === "permission")
          ? "not permitted for this account"
          : "unavailable";
      return `${TOPOLOGY_KIND_LABEL[k]} (${why})`;
    },
  );
  const capped = TOPOLOGY_KINDS.filter((k) => view.rows[k].truncated);

  if (missing.length === 0 && capped.length === 0 && view.skipped === 0) {
    return null;
  }
  return (
    <div
      class="flex shrink-0 flex-col gap-0.5 text-[11px] leading-snug text-text-muted"
      data-testid="cluster-topology-coverage"
    >
      {missing.length > 0 && <span>Not drawn: {missing.join(", ")}.</span>}
      {capped.map((k) => (
        <span key={k}>
          Showing {view.rows[k].counted} of {view.rows[k].total}{" "}
          {TOPOLOGY_KIND_LABEL[k].toLowerCase()}.
        </span>
      ))}
      {view.skipped > 0 && (
        <span>
          {view.skipped} object{view.skipped === 1 ? "" : "s"} had no readable
          name and {view.skipped === 1 ? "was" : "were"} left out.
        </span>
      )}
    </div>
  );
}

registerWidget({
  id: "cluster-topology",
  title: "Cluster Topology",
  family: "cluster",
  scopes: ["overview"],
  sources: [
    "pods-list",
    "nodes-list",
    "services-list",
    "deployments-list",
    "statefulsets-list",
    "daemonsets-list",
    "pvcs-list",
  ],
  // Everything but pods. Each row is separately RBAC-gated, and nodes are
  // cluster-scoped, so gating the card on all seven would blank the whole map
  // for an account that can see six of them. The card names each missing row.
  optionalSources: [
    "nodes-list",
    "services-list",
    "deployments-list",
    "statefulsets-list",
    "daemonsets-list",
    "pvcs-list",
  ],
  // Wide and tall: five rows of labelled shapes, a legend and up to three
  // coverage notes. Narrower than six columns and the labels overlap before
  // the cluster is even mid-sized.
  minW: 6,
  minH: 4,
  defaultW: 8,
  defaultH: 6,
  modes: ["normal"],
  render: () => <ClusterTopology />,
});
