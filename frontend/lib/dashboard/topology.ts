/**
 * The graph behind the cluster-topology widget.
 *
 * Ported from the pre-Liquid-Glass `ClusterTopology` island, which drew five
 * rows -- nodes, services, workloads, pods, claims -- and joined them by the
 * relationships the objects themselves carry: a pod's `spec.nodeName`, a
 * service's selector, a workload's label selector, a pod's PVC volumes. The
 * island did all of that inside the component; here it is pure and under test
 * (D-10, KTD8), and the component only draws what this returns.
 *
 * Four things the island got wrong and this does not:
 *
 * - Node ids were joined with dashes, so `pod-a-b-c` was both namespace `a-b`
 *   pod `c` and namespace `a` pod `b-c`. Ids here are joined with `/`, which a
 *   Kubernetes name cannot contain.
 * - An empty selector matched every pod in its namespace. Kubernetes reads an
 *   empty Service selector as "manage no endpoints", and an empty workload
 *   selector is invalid, so both select nothing here.
 * - Related-resource lists were sliced for display and the slice's length was
 *   then printed as the count. `total` is carried separately.
 * - Every workload and service drew green. A workload is graded by the same
 *   readiness rule the workload-health card uses, and a selector service with
 *   no pods behind it is a warning -- but only when the pod page is the whole
 *   population, since a capped page cannot prove a match is absent.
 *
 * Pure: no DOM, no fetch, no signals.
 */
import { listOr, num, numOr, obj, str } from "./narrow.ts";
import type { PageCoverage } from "./page-coverage.ts";
import { coverage } from "./page-coverage.ts";
import type { ResourceListPage } from "./wire-types.ts";
import type { WorkloadKind } from "./workload-health.ts";
import { WORKLOAD_KIND_HREFS, workloadReady } from "./workload-health.ts";

export const TOPOLOGY_KINDS = [
  "node",
  "service",
  "workload",
  "pod",
  "pvc",
] as const;
export type TopologyKind = (typeof TOPOLOGY_KINDS)[number];

export type TopologyHealth = "healthy" | "warning" | "error";

export type TopologyEdgeKind =
  | "node-service"
  | "service-workload"
  | "service-pod"
  | "workload-pod"
  | "pod-pvc";

/** The plural display name per row, for legends and coverage notes. */
export const TOPOLOGY_KIND_LABEL: Readonly<Record<TopologyKind, string>> = {
  node: "Nodes",
  service: "Services",
  workload: "Workloads",
  pod: "Pods",
  pvc: "PVCs",
};

/** The full topology page, which is namespace-scoped and server-built. */
export const TOPOLOGY_PAGE_HREF = "/observability/topology";

/** How many names a related-resource group lists before it only counts. */
export const RELATED_LIMIT = 6;

/** The seven list reads this graph is drawn from. Any may be absent -- not
 * landed yet, refused for this account -- and the graph draws the rest. */
export interface TopologyPages {
  nodes?: ResourceListPage | null;
  services?: ResourceListPage | null;
  deployments?: ResourceListPage | null;
  statefulsets?: ResourceListPage | null;
  daemonsets?: ResourceListPage | null;
  pods?: ResourceListPage | null;
  pvcs?: ResourceListPage | null;
}

/** One of the seven reads. The workload row is drawn from three of them. */
export type TopologySource = keyof TopologyPages;

/** What a coverage note calls each read. */
export const TOPOLOGY_SOURCE_LABEL: Readonly<Record<TopologySource, string>> = {
  nodes: "Nodes",
  services: "Services",
  deployments: "Deployments",
  statefulsets: "StatefulSets",
  daemonsets: "DaemonSets",
  pods: "Pods",
  pvcs: "PVCs",
};

export interface TopologyRelated {
  kind: TopologyKind;
  /** At most RELATED_LIMIT names. */
  items: string[];
  /** How many there are in all. */
  total: number;
}

export interface TopologyNode {
  /** `kind/namespace/name`, or `node//name` for a node. Unique per graph. */
  id: string;
  kind: TopologyKind;
  name: string;
  /** "" for a node, which is cluster-scoped. */
  namespace: string;
  /** "Deployment", "StatefulSet" or "DaemonSet" for a workload, else "". */
  workloadKind: string;
  /** The short glyph drawn inside the shape. */
  abbr: string;
  href: string;
  health: TopologyHealth;
  /** What the detail panel prints as the status: a phase, or a readiness. */
  status: string;
  /** Pods: the node it is scheduled on, "" when unscheduled. */
  nodeName: string;
  /** PVCs: the bound volume, "" when unbound. */
  volumeName: string;
  /** Pods: container names, in spec order. */
  containers: string[];
  related: TopologyRelated[];
  x: number;
  y: number;
  size: number;
}

export interface TopologyEdge {
  from: string;
  to: string;
  kind: TopologyEdgeKind;
}

/** Coverage per row. `available` is false when the row's read has not
 * answered at all -- a different claim from answering with nothing. */
export interface TopologyRowCoverage extends PageCoverage {
  available: boolean;
  /**
   * The row's reads that have no page: not landed yet, or refused. Empty for
   * a complete row; every source when `available` is false. The workload row
   * is the one that can be partial -- an account that may list Deployments
   * but not DaemonSets -- and without this its count would read as every
   * workload on the cluster.
   */
  absent: TopologySource[];
}

export interface TopologyView {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  /** The virtual canvas the coordinates are laid out on. */
  width: number;
  height: number;
  rows: Readonly<Record<TopologyKind, TopologyRowCoverage>>;
  /** Items that arrived without a readable name and were left out. */
  skipped: number;
}

// --------------------------------------------------------------------------
// Selectors
// --------------------------------------------------------------------------

function stringMap(value: unknown): Record<string, string> {
  const o = obj(value);
  if (o === null) return {};
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(o)) {
    if (typeof v === "string") out[k] = v;
  }
  return out;
}

/** A Service selector: a plain label map, empty selecting nothing. */
export function serviceSelects(
  selector: Readonly<Record<string, string>>,
  labels: Readonly<Record<string, string>>,
): boolean {
  const entries = Object.entries(selector);
  if (entries.length === 0) return false;
  return entries.every(([k, v]) => labels[k] === v);
}

/**
 * A workload's `metav1.LabelSelector`: `matchLabels` AND every
 * `matchExpressions` requirement. Empty selects nothing -- apps/v1 refuses an
 * empty selector, so one that reads as empty is one this build could not
 * read, and matching everything would draw it as owning its whole namespace.
 * An operator it does not know fails closed for the same reason.
 */
export function labelSelectorSelects(
  selector: unknown,
  labels: Readonly<Record<string, string>>,
): boolean {
  const sel = obj(selector);
  if (sel === null) return false;
  const matchLabels = stringMap(sel.matchLabels);
  const expressions = listOr(sel.matchExpressions);
  if (Object.keys(matchLabels).length === 0 && expressions.length === 0) {
    return false;
  }
  for (const [k, v] of Object.entries(matchLabels)) {
    if (labels[k] !== v) return false;
  }
  for (const raw of expressions) {
    const req = obj(raw);
    if (req === null) return false;
    const key = str(req.key);
    const values = listOr(req.values).filter(
      (v): v is string => typeof v === "string",
    );
    const has = Object.hasOwn(labels, key);
    switch (str(req.operator)) {
      case "In":
        if (!has || !values.includes(labels[key])) return false;
        break;
      case "NotIn":
        if (has && values.includes(labels[key])) return false;
        break;
      case "Exists":
        if (!has) return false;
        break;
      case "DoesNotExist":
        if (has) return false;
        break;
      default:
        return false;
    }
  }
  return true;
}

// --------------------------------------------------------------------------
// Health
// --------------------------------------------------------------------------

function nodeHealth(item: Record<string, unknown>): {
  health: TopologyHealth;
  status: string;
} {
  const conditions = listOr(obj(item.status)?.conditions);
  const ready = conditions
    .map(obj)
    .find((c) => c !== null && str(c.type) === "Ready");
  return str(ready?.status) === "True"
    ? { health: "healthy", status: "Ready" }
    : { health: "error", status: "NotReady" };
}

function podHealth(item: Record<string, unknown>): {
  health: TopologyHealth;
  status: string;
} {
  const status = obj(item.status) ?? {};
  const phase = str(status.phase);
  if (phase === "Succeeded") return { health: "healthy", status: phase };
  if (phase === "Pending") return { health: "warning", status: phase };
  if (phase !== "Running") {
    return { health: "error", status: phase === "" ? "Unknown" : phase };
  }
  // Running is a phase, not a verdict: a pod whose container is crash-looping
  // stays Running between restarts.
  const statuses = listOr(status.containerStatuses).map(obj);
  const unready = statuses.filter((c) => c !== null && c.ready !== true);
  return unready.length === 0
    ? { health: "healthy", status: phase }
    : { health: "warning", status: `${phase}, not ready` };
}

function pvcHealth(item: Record<string, unknown>): {
  health: TopologyHealth;
  status: string;
} {
  const phase = str(obj(item.status)?.phase);
  if (phase === "Bound") return { health: "healthy", status: phase };
  if (phase === "Pending") return { health: "warning", status: phase };
  return { health: "error", status: phase === "" ? "Unknown" : phase };
}

function workloadHealth(
  kind: WorkloadKind,
  item: Record<string, unknown>,
): { health: TopologyHealth; status: string } {
  const status = obj(item.status) ?? {};
  const desired =
    kind === "daemonsets"
      ? numOr(status.desiredNumberScheduled, 0)
      : numOr(obj(item.spec)?.replicas, 1);
  const ready =
    num(kind === "daemonsets" ? status.numberReady : status.readyReplicas) ?? 0;
  const label = `${ready}/${desired} ready`;
  if (workloadReady(kind, item)) return { health: "healthy", status: label };
  return { health: ready === 0 ? "error" : "warning", status: label };
}

// --------------------------------------------------------------------------
// Hrefs
// --------------------------------------------------------------------------

const enc = encodeURIComponent;

const WORKLOAD_SINGULAR: Readonly<Record<WorkloadKind, string>> = {
  deployments: "Deployment",
  statefulsets: "StatefulSet",
  daemonsets: "DaemonSet",
};

const WORKLOAD_ABBR: Readonly<Record<WorkloadKind, string>> = {
  deployments: "DEP",
  statefulsets: "STS",
  daemonsets: "DS",
};

// --------------------------------------------------------------------------
// Layout
// --------------------------------------------------------------------------

/** Each row's vertical position, as a share of the canvas height. */
const ROW_Y: Readonly<Record<TopologyKind, number>> = {
  node: 0.08,
  service: 0.25,
  workload: 0.45,
  pod: 0.65,
  pvc: 0.85,
};

/** Horizontal room each item in the widest row is given. */
const ITEM_SPACING = 62;
/** A small cluster is laid out at roughly the card's own aspect, so a fitted
 * viewBox fills the card instead of letterboxing it. */
const CANVAS_ASPECT = 2.3;
const MIN_CANVAS = 400;
/** Past this the canvas grows sideways only, so the rows stay a fixed
 * distance apart. Height in step with width made a wide cluster's canvas far
 * taller than any card, so the map was fitted by its height into the middle
 * of the card at a scale no zoom could make readable. */
const MAX_CANVAS_HEIGHT = 560;

export const TOPOLOGY_MIN_ZOOM = 0.5;
/** The ceiling when the canvas has not been measured, and the floor of the
 * measured one. */
const BASE_MAX_ZOOM = 4;
/** Screen pixels per canvas unit at full zoom: the smallest label, drawn at
 * 8 units, then reads at 12px. */
const READABLE_SCALE = 1.5;
const ZOOM_FACTOR = 1.25;

/**
 * How far the canvas may zoom in a box of `boxW` x `boxH` pixels. A fixed
 * ceiling stops a wide cluster short of legible: the fitted scale shrinks as
 * the widest row grows, so the ceiling is set from it instead -- always far
 * enough for the smallest label to read, never below the fixed one.
 */
export function topologyMaxZoom(
  canvas: Readonly<{ width: number; height: number }>,
  boxW: number,
  boxH: number,
): number {
  if (!(boxW > 0) || !(boxH > 0)) return BASE_MAX_ZOOM;
  const fit = Math.min(boxW / canvas.width, boxH / canvas.height);
  return Math.max(BASE_MAX_ZOOM, READABLE_SCALE / fit);
}

/** One zoom step in `direction`, clamped to [TOPOLOGY_MIN_ZOOM, max].
 * Multiplicative, because the measured ceiling can be 10x or more and equal
 * linear steps would take dozens of clicks to cross it. */
export function stepTopologyZoom(
  current: number,
  direction: 1 | -1,
  max: number,
): number {
  const next = direction === 1 ? current * ZOOM_FACTOR : current / ZOOM_FACTOR;
  return Math.min(max, Math.max(TOPOLOGY_MIN_ZOOM, next));
}

/** The visible window: `zoom` over the whole canvas, its top-left at x/y. */
export interface TopologyCamera {
  zoom: number;
  x: number;
  y: number;
}

type Canvas = Readonly<{ width: number; height: number }>;
type Box = Readonly<{ w: number; h: number }>;

/**
 * How the svg draws its viewBox into the box under preserveAspectRatio
 * "xMidYMid meet": ONE scale for both axes, the smaller of the two fits, and
 * the slack on the other axis split evenly either side. A wide cluster's
 * canvas is far wider than any card, so the vertical slack is most of the box,
 * and converting each axis by its own box ratio moved the map a fraction of
 * the cursor on that axis. Null while the box is unmeasured.
 */
function meetFit(
  cam: TopologyCamera,
  canvas: Canvas,
  box: Box,
): { scale: number; offX: number; offY: number } | null {
  if (!(box.w > 0) || !(box.h > 0)) return null;
  const vbW = canvas.width / cam.zoom;
  const vbH = canvas.height / cam.zoom;
  const scale = Math.min(box.w / vbW, box.h / vbH);
  return {
    scale,
    offX: (box.w - vbW * scale) / 2,
    offY: (box.h - vbH * scale) / 2,
  };
}

/** The camera after dragging `dx`/`dy` box pixels from `start`: the canvas
 * point that was under the cursor stays under it. */
export function panTopologyCamera(
  start: TopologyCamera,
  dx: number,
  dy: number,
  canvas: Canvas,
  box: Box,
): TopologyCamera {
  const fit = meetFit(start, canvas, box);
  if (fit === null) return start;
  return {
    zoom: start.zoom,
    x: start.x - dx / fit.scale,
    y: start.y - dy / fit.scale,
  };
}

/** The camera at `zoom`, holding the canvas point under `point` (box pixels)
 * where it is on screen. An unmeasured box changes the zoom only. */
export function zoomTopologyCameraAt(
  cam: TopologyCamera,
  zoom: number,
  canvas: Canvas,
  box: Box,
  point: Readonly<{ x: number; y: number }>,
): TopologyCamera {
  const before = meetFit(cam, canvas, box);
  const after = meetFit({ ...cam, zoom }, canvas, box);
  if (before === null || after === null) return { ...cam, zoom };
  const ux = cam.x + (point.x - before.offX) / before.scale;
  const uy = cam.y + (point.y - before.offY) / before.scale;
  return {
    zoom,
    x: ux - (point.x - after.offX) / after.scale,
    y: uy - (point.y - after.offY) / after.scale,
  };
}

/** Shapes shrink on a busy row so neighbours do not overlap. */
function rowSize(kind: TopologyKind, count: number): number {
  switch (kind) {
    case "node":
      return count > 6 ? 40 : 52;
    case "service":
      return count > 8 ? 32 : 44;
    case "workload":
      return count > 10 ? 28 : 36;
    case "pod":
      return count > 10 ? 28 : 36;
    case "pvc":
      return count > 6 ? 28 : 36;
  }
}

// --------------------------------------------------------------------------
// The view
// --------------------------------------------------------------------------

interface Parsed {
  item: Record<string, unknown>;
  name: string;
  namespace: string;
  labels: Record<string, string>;
}

function parseItems(
  page: ResourceListPage | null | undefined,
  namespaced: boolean,
): { parsed: Parsed[]; skipped: number } {
  const parsed: Parsed[] = [];
  let skipped = 0;
  for (const raw of listOr(page?.items)) {
    const item = obj(raw);
    const meta = obj(item?.metadata);
    const name = str(meta?.name);
    const namespace = namespaced ? str(meta?.namespace) : "";
    if (item === null || name === "" || (namespaced && namespace === "")) {
      skipped++;
      continue;
    }
    parsed.push({ item, name, namespace, labels: stringMap(meta?.labels) });
  }
  // Grouped by namespace so a namespace's services, workloads and pods sit
  // over one another and their edges stay short.
  parsed.sort(
    (a, b) =>
      a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name),
  );
  return { parsed, skipped };
}

function rowCoverage(
  pages: TopologyPages,
  sources: readonly TopologySource[],
  counted: number,
): TopologyRowCoverage {
  const absent = sources.filter((s) => pages[s] == null);
  const present = sources
    .map((s) => pages[s])
    .filter((p): p is ResourceListPage => p != null);
  if (present.length === 0) {
    return {
      available: false,
      absent,
      total: 0,
      counted: 0,
      truncated: false,
      readable: false,
    };
  }
  let total = 0;
  let truncated = false;
  let readable = false;
  for (const page of present) {
    const c = coverage(page, listOr(page.items).length);
    total += c.total;
    truncated ||= c.truncated;
    readable ||= c.readable;
  }
  return { available: true, absent, total, counted, truncated, readable };
}

function addRelated(
  node: TopologyNode,
  kind: TopologyKind,
  names: readonly string[],
): void {
  if (names.length === 0) return;
  node.related.push({
    kind,
    items: names.slice(0, RELATED_LIMIT),
    total: names.length,
  });
}

function push<K, V>(map: Map<K, V[]>, key: K, value: V): void {
  const existing = map.get(key);
  if (existing) existing.push(value);
  else map.set(key, [value]);
}

/**
 * The whole graph: positioned nodes, typed edges and per-row coverage.
 *
 * Total: every page may be null, missing or malformed, and none of that
 * throws. A row whose read has not answered is reported unavailable and drawn
 * empty, and the card says which rows those are rather than letting an empty
 * row pass for an empty cluster.
 */
export function clusterTopologyView(pages: TopologyPages): TopologyView {
  const nodesIn = parseItems(pages.nodes, false);
  const svcsIn = parseItems(pages.services, true);
  const podsIn = parseItems(pages.pods, true);
  const pvcsIn = parseItems(pages.pvcs, true);
  const workloadPages: [WorkloadKind, ResourceListPage | null | undefined][] = [
    ["deployments", pages.deployments],
    ["statefulsets", pages.statefulsets],
    ["daemonsets", pages.daemonsets],
  ];
  const wlIn = workloadPages.map(([kind, page]) => {
    const { parsed, skipped } = parseItems(page, true);
    return { kind, parsed, skipped };
  });
  const workloads = wlIn
    .flatMap(({ kind, parsed }) => parsed.map((p) => ({ ...p, kind })))
    .sort(
      (a, b) =>
        a.namespace.localeCompare(b.namespace) ||
        a.name.localeCompare(b.name) ||
        a.kind.localeCompare(b.kind),
    );

  const skipped =
    nodesIn.skipped +
    svcsIn.skipped +
    podsIn.skipped +
    pvcsIn.skipped +
    wlIn.reduce((n, w) => n + w.skipped, 0);

  const rows: Record<TopologyKind, TopologyRowCoverage> = {
    node: rowCoverage(pages, ["nodes"], nodesIn.parsed.length),
    service: rowCoverage(pages, ["services"], svcsIn.parsed.length),
    workload: rowCoverage(
      pages,
      ["deployments", "statefulsets", "daemonsets"],
      workloads.length,
    ),
    pod: rowCoverage(pages, ["pods"], podsIn.parsed.length),
    pvc: rowCoverage(pages, ["pvcs"], pvcsIn.parsed.length),
  };

  // Canvas.
  const widest = Math.max(
    nodesIn.parsed.length,
    svcsIn.parsed.length,
    workloads.length,
    podsIn.parsed.length,
    pvcsIn.parsed.length,
  );
  const width = Math.max(MIN_CANVAS, (widest + 1) * ITEM_SPACING);
  const height = Math.min(
    MAX_CANVAS_HEIGHT,
    Math.max(MIN_CANVAS, width / CANVAS_ASPECT),
  );
  const place = (kind: TopologyKind, index: number, count: number) => ({
    x: (width / (count + 1)) * (index + 1),
    y: height * ROW_Y[kind],
    size: rowSize(kind, count),
  });

  const blank = {
    workloadKind: "",
    nodeName: "",
    volumeName: "",
    containers: [] as string[],
  };

  // Nodes.
  const graph: TopologyNode[] = [];
  const nodeById = new Map<string, TopologyNode>();
  const add = (n: TopologyNode) => {
    graph.push(n);
    nodeById.set(n.id, n);
  };

  nodesIn.parsed.forEach((p, i) => {
    add({
      ...blank,
      id: `node//${p.name}`,
      kind: "node",
      name: p.name,
      namespace: "",
      abbr: `N${i + 1}`,
      href: `/cluster/nodes/${enc(p.name)}`,
      ...nodeHealth(p.item),
      related: [],
      ...place("node", i, nodesIn.parsed.length),
    });
  });

  const svcSelectors = new Map<string, Record<string, string>>();
  svcsIn.parsed.forEach((p, i) => {
    const id = `service/${p.namespace}/${p.name}`;
    svcSelectors.set(id, stringMap(obj(p.item.spec)?.selector));
    add({
      ...blank,
      id,
      kind: "service",
      name: p.name,
      namespace: p.namespace,
      abbr: "SVC",
      href: `/networking/services/${enc(p.namespace)}/${enc(p.name)}`,
      health: "healthy",
      status: "Active",
      related: [],
      ...place("service", i, svcsIn.parsed.length),
    });
  });

  workloads.forEach((w, i) => {
    add({
      ...blank,
      id: `workload/${w.namespace}/${WORKLOAD_SINGULAR[w.kind]}/${w.name}`,
      kind: "workload",
      name: w.name,
      namespace: w.namespace,
      workloadKind: WORKLOAD_SINGULAR[w.kind],
      abbr: WORKLOAD_ABBR[w.kind],
      href: `${WORKLOAD_KIND_HREFS[w.kind]}/${enc(w.namespace)}/${enc(w.name)}`,
      ...workloadHealth(w.kind, w.item),
      related: [],
      ...place("workload", i, workloads.length),
    });
  });

  podsIn.parsed.forEach((p, i) => {
    const spec = obj(p.item.spec) ?? {};
    add({
      ...blank,
      id: `pod/${p.namespace}/${p.name}`,
      kind: "pod",
      name: p.name,
      namespace: p.namespace,
      abbr: "P",
      href: `/workloads/pods/${enc(p.namespace)}/${enc(p.name)}`,
      ...podHealth(p.item),
      nodeName: str(spec.nodeName),
      containers: listOr(spec.containers)
        .map((c) => str(obj(c)?.name))
        .filter((n) => n !== ""),
      related: [],
      ...place("pod", i, podsIn.parsed.length),
    });
  });

  pvcsIn.parsed.forEach((p, i) => {
    add({
      ...blank,
      id: `pvc/${p.namespace}/${p.name}`,
      kind: "pvc",
      name: p.name,
      namespace: p.namespace,
      abbr: "PVC",
      href: `/storage/pvcs/${enc(p.namespace)}/${enc(p.name)}`,
      ...pvcHealth(p.item),
      volumeName: str(obj(p.item.spec)?.volumeName),
      related: [],
      ...place("pvc", i, pvcsIn.parsed.length),
    });
  });

  // Relationships, computed once and read by both the edges and the detail
  // panel so the two cannot disagree.
  const podsByNode = new Map<string, string[]>(); // node id -> pod names
  const podsBySvc = new Map<string, string[]>(); // svc id -> pod ids
  const svcsByPod = new Map<string, string[]>(); // pod id -> svc ids
  const podsByWl = new Map<string, string[]>(); // wl id -> pod ids
  const wlsByPod = new Map<string, string[]>(); // pod id -> wl ids
  const pvcsByPod = new Map<string, string[]>(); // pod id -> pvc ids
  const podsByPvc = new Map<string, string[]>(); // pvc id -> pod ids

  for (const p of podsIn.parsed) {
    const podId = `pod/${p.namespace}/${p.name}`;
    const nodeId = `node//${str(obj(p.item.spec)?.nodeName)}`;
    if (nodeById.has(nodeId)) push(podsByNode, nodeId, podId);

    for (const s of svcsIn.parsed) {
      if (s.namespace !== p.namespace) continue;
      const svcId = `service/${s.namespace}/${s.name}`;
      if (serviceSelects(svcSelectors.get(svcId) ?? {}, p.labels)) {
        push(podsBySvc, svcId, podId);
        push(svcsByPod, podId, svcId);
      }
    }

    for (const w of workloads) {
      if (w.namespace !== p.namespace) continue;
      if (labelSelectorSelects(obj(w.item.spec)?.selector, p.labels)) {
        const wlId = `workload/${w.namespace}/${WORKLOAD_SINGULAR[w.kind]}/${w.name}`;
        push(podsByWl, wlId, podId);
        push(wlsByPod, podId, wlId);
      }
    }

    for (const vol of listOr(obj(p.item.spec)?.volumes)) {
      const claim = str(obj(obj(vol)?.persistentVolumeClaim)?.claimName);
      const pvcId = `pvc/${p.namespace}/${claim}`;
      if (claim !== "" && nodeById.has(pvcId)) {
        push(pvcsByPod, podId, pvcId);
        push(podsByPvc, pvcId, podId);
      }
    }
  }

  // Edges, deduplicated: a service selecting three pods of one workload is
  // one service->workload edge, not three.
  const edges: TopologyEdge[] = [];
  const seen = new Set<string>();
  const edge = (from: string, to: string, kind: TopologyEdgeKind) => {
    const key = `${from}>${to}`;
    if (seen.has(key)) return;
    seen.add(key);
    edges.push({ from, to, kind });
  };

  const nodeOfPod = (podId: string) => {
    const n = nodeById.get(podId);
    return n && n.nodeName !== "" ? `node//${n.nodeName}` : null;
  };

  for (const [svcId, podIds] of podsBySvc) {
    for (const podId of podIds) {
      const nodeId = nodeOfPod(podId);
      if (nodeId !== null && nodeById.has(nodeId)) {
        edge(nodeId, svcId, "node-service");
      }
      const wlIds = wlsByPod.get(podId);
      if (wlIds === undefined) {
        // A bare pod, or one whose controller this account cannot list:
        // the service reaches it directly.
        edge(svcId, podId, "service-pod");
      } else {
        for (const wlId of wlIds) edge(svcId, wlId, "service-workload");
      }
    }
  }
  for (const [wlId, podIds] of podsByWl) {
    for (const podId of podIds) edge(wlId, podId, "workload-pod");
  }
  for (const [podId, pvcIds] of pvcsByPod) {
    for (const pvcId of pvcIds) edge(podId, pvcId, "pod-pvc");
  }

  // The detail panel's related groups.
  const nameOf = (id: string) => nodeById.get(id)?.name ?? id;
  const unique = (ids: Iterable<string>) => [...new Set(ids)];
  const podPageComplete = rows.pod.available && !rows.pod.truncated;

  for (const n of graph) {
    switch (n.kind) {
      case "node":
        addRelated(n, "pod", (podsByNode.get(n.id) ?? []).map(nameOf));
        break;
      case "service": {
        const podIds = podsBySvc.get(n.id) ?? [];
        addRelated(
          n,
          "workload",
          unique(podIds.flatMap((p) => wlsByPod.get(p) ?? [])).map(
            (id) => `${nodeById.get(id)?.workloadKind}/${nameOf(id)}`,
          ),
        );
        addRelated(n, "pod", podIds.map(nameOf));
        const selector = svcSelectors.get(n.id) ?? {};
        if (
          podPageComplete &&
          Object.keys(selector).length > 0 &&
          podIds.length === 0
        ) {
          n.health = "warning";
          n.status = "No matching pods";
        }
        break;
      }
      case "workload": {
        const podIds = podsByWl.get(n.id) ?? [];
        addRelated(
          n,
          "service",
          unique(podIds.flatMap((p) => svcsByPod.get(p) ?? [])).map(nameOf),
        );
        addRelated(n, "pod", podIds.map(nameOf));
        addRelated(
          n,
          "node",
          unique(
            podIds
              .map((p) => nodeById.get(p)?.nodeName ?? "")
              .filter((name) => name !== ""),
          ),
        );
        break;
      }
      case "pod":
        addRelated(n, "service", (svcsByPod.get(n.id) ?? []).map(nameOf));
        addRelated(n, "pvc", (pvcsByPod.get(n.id) ?? []).map(nameOf));
        break;
      case "pvc":
        addRelated(n, "pod", (podsByPvc.get(n.id) ?? []).map(nameOf));
        break;
    }
  }

  return { nodes: graph, edges, width, height, rows, skipped };
}
