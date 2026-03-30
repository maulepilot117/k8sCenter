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

/** Rich tooltip data computed from relationships */
interface TopoTooltipData {
  kind: TopoNode["kind"];
  name: string;
  namespace?: string;
  health: "healthy" | "warning" | "error";
  /** Workload kind for workloads (Deployment, StatefulSet, DaemonSet) */
  workloadKind?: string;
  /** Node name (for pods) */
  nodeName?: string;
  /** Pod phase (for pods) */
  podPhase?: string;
  /** PVC phase (for pvcs) */
  pvcPhase?: string;
  /** Volume name (for pvcs) */
  volumeName?: string;
  /** Container names (for pods) */
  containers?: string[];
  /** Related resources: { kind, names[] } */
  related: { kind: string; color: string; items: string[] }[];
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
    text: "var(--info)",
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

  // Hovered node + tooltip position as a single atomic signal (prevents desync)
  const tooltip = useSignal<
    { nodeId: string; x: number; y: number } | null
  >(null);
  // Rich tooltip data per node
  const tooltipData = useSignal<Map<string, TopoTooltipData>>(new Map());
  const containerRef = useRef<HTMLDivElement>(null);
  // Cached container rect to avoid getBoundingClientRect() on every mousemove
  const cachedRect = useRef<DOMRect | null>(null);

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
    // Virtual height matches the container's aspect ratio so the SVG
    // fills the container without wasted space. The topology card is
    // roughly 2.3:1 (wide). 5 rows need enough vertical space for
    // nodes + labels (~80px between rows).
    const virtualHeight = Math.max(400, virtualWidth / 2.3);
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
            color: "var(--info)",
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
    const topoNodeIds = new Set(topoNodes.map((n) => n.id));
    for (const pod of k8sPods) {
      for (const vol of pod.spec?.volumes ?? []) {
        if (vol.persistentVolumeClaim?.claimName) {
          const pvcId =
            `pvc-${pod.metadata.namespace}-${vol.persistentVolumeClaim.claimName}`;
          const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
          if (topoNodeIds.has(pvcId)) {
            topoEdges.push({
              from: podId,
              to: pvcId,
              color: "var(--info)",
            });
          }
        }
      }
    }

    // ── Build rich tooltip data from relationships ──
    const ttMap = new Map<string, TopoTooltipData>();

    // Pre-compute lookup maps
    const podsByNode = new Map<string, string[]>(); // nodeName → pod names
    const podToNode = new Map<string, string>(); // podId → nodeName
    const podToSvcs = new Map<string, string[]>(); // podId → service names
    const svcToPods = new Map<string, string[]>(); // svcId → pod names
    const svcToWorkloads = new Map<string, string[]>(); // svcId → workload labels
    const wlToPods = new Map<string, string[]>(); // wlId → pod names
    const podToPVCs = new Map<string, string[]>(); // podId → pvc names
    const pvcToPods = new Map<string, string[]>(); // pvcId → pod names
    const wlToSvcs = new Map<string, string[]>(); // wlId → service names

    // Pod → Node
    for (const pod of k8sPods) {
      const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
      if (pod.spec?.nodeName) {
        podToNode.set(podId, pod.spec.nodeName);
        const list = podsByNode.get(pod.spec.nodeName) ?? [];
        list.push(pod.metadata.name);
        podsByNode.set(pod.spec.nodeName, list);
      }
    }

    // Service → Pod relationships
    for (const svc of k8sSvcs) {
      const svcId = `svc-${svc.metadata.namespace}-${svc.metadata.name}`;
      for (const pod of k8sPods) {
        if (
          svc.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(svc.spec?.selector, pod.metadata.labels)
        ) {
          const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
          // svc → pods
          const sp = svcToPods.get(svcId) ?? [];
          sp.push(pod.metadata.name);
          svcToPods.set(svcId, sp);
          // pod → svcs
          const ps = podToSvcs.get(podId) ?? [];
          ps.push(svc.metadata.name);
          podToSvcs.set(podId, ps);
        }
      }
    }

    // Workload → Pod + Workload → Service relationships
    for (const wl of k8sWorkloads) {
      const wlId = `wl-${wl.kind}-${wl.metadata.namespace}-${wl.metadata.name}`;
      const sel = wl.spec?.selector?.matchLabels;
      if (!sel) continue;
      for (const pod of k8sPods) {
        if (
          wl.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(sel, pod.metadata.labels)
        ) {
          const wp = wlToPods.get(wlId) ?? [];
          wp.push(pod.metadata.name);
          wlToPods.set(wlId, wp);
        }
      }
      // Service → Workload
      for (const svc of k8sSvcs) {
        if (svc.metadata.namespace !== wl.metadata.namespace) continue;
        const svcId = `svc-${svc.metadata.namespace}-${svc.metadata.name}`;
        const svcPods = svcToPods.get(svcId) ?? [];
        const wlPodSet = new Set(wlToPods.get(wlId) ?? []);
        if (svcPods.some((p) => wlPodSet.has(p))) {
          const sw = svcToWorkloads.get(svcId) ?? [];
          sw.push(`${wl.kind}/${wl.metadata.name}`);
          svcToWorkloads.set(svcId, sw);
          const ws = wlToSvcs.get(wlId) ?? [];
          ws.push(svc.metadata.name);
          wlToSvcs.set(wlId, ws);
        }
      }
    }

    // Pod → PVC
    for (const pod of k8sPods) {
      const podId = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
      for (const vol of pod.spec?.volumes ?? []) {
        if (vol.persistentVolumeClaim?.claimName) {
          const pvcName = vol.persistentVolumeClaim.claimName;
          const pvcId = `pvc-${pod.metadata.namespace}-${pvcName}`;
          if (topoNodeIds.has(pvcId)) {
            const pp = podToPVCs.get(podId) ?? [];
            pp.push(pvcName);
            podToPVCs.set(podId, pp);
            const pcp = pvcToPods.get(pvcId) ?? [];
            pcp.push(pod.metadata.name);
            pvcToPods.set(pvcId, pcp);
          }
        }
      }
    }

    // Build tooltip entries for each node
    for (const n of k8sNodes) {
      const id = `node-${n.metadata.name}`;
      const pods = podsByNode.get(n.metadata.name) ?? [];
      const related: TopoTooltipData["related"] = [];
      if (pods.length > 0) {
        related.push({
          kind: "Pods",
          color: "var(--success)",
          items: pods.slice(0, 6),
        });
      }
      ttMap.set(id, {
        kind: "node",
        name: n.metadata.name,
        health: getHealthStatus("node", n),
        related,
      });
    }

    for (const svc of k8sSvcs) {
      const id = `svc-${svc.metadata.namespace}-${svc.metadata.name}`;
      const related: TopoTooltipData["related"] = [];
      const wls = svcToWorkloads.get(id) ?? [];
      if (wls.length > 0) {
        related.push({
          kind: "Workloads",
          color: "var(--info)",
          items: wls.slice(0, 4),
        });
      }
      const pods = svcToPods.get(id) ?? [];
      if (pods.length > 0) {
        related.push({
          kind: "Pods",
          color: "var(--success)",
          items: pods.slice(0, 6),
        });
      }
      ttMap.set(id, {
        kind: "service",
        name: svc.metadata.name,
        namespace: svc.metadata.namespace,
        health: getHealthStatus("service", svc),
        related,
      });
    }

    for (const wl of k8sWorkloads) {
      const id = `wl-${wl.kind}-${wl.metadata.namespace}-${wl.metadata.name}`;
      const related: TopoTooltipData["related"] = [];
      const svcs = wlToSvcs.get(id) ?? [];
      if (svcs.length > 0) {
        related.push({
          kind: "Services",
          color: "var(--accent-secondary)",
          items: svcs.slice(0, 4),
        });
      }
      const pods = wlToPods.get(id) ?? [];
      if (pods.length > 0) {
        related.push({
          kind: "Pods",
          color: "var(--success)",
          items: pods.slice(0, 6),
        });
      }
      // Find which nodes the pods run on
      const nodeNames = new Set<string>();
      for (const pod of k8sPods) {
        if (
          wl.metadata.namespace === pod.metadata.namespace &&
          matchesSelector(
            wl.spec?.selector?.matchLabels,
            pod.metadata.labels,
          ) &&
          pod.spec?.nodeName
        ) {
          nodeNames.add(pod.spec.nodeName);
        }
      }
      if (nodeNames.size > 0) {
        related.push({
          kind: "Nodes",
          color: "var(--accent)",
          items: [...nodeNames],
        });
      }
      ttMap.set(id, {
        kind: "workload",
        name: wl.metadata.name,
        namespace: wl.metadata.namespace,
        health: getHealthStatus("workload", wl),
        workloadKind: wl.kind,
        related,
      });
    }

    for (const pod of k8sPods) {
      const id = `pod-${pod.metadata.namespace}-${pod.metadata.name}`;
      const related: TopoTooltipData["related"] = [];
      const svcs = podToSvcs.get(id) ?? [];
      if (svcs.length > 0) {
        related.push({
          kind: "Services",
          color: "var(--accent-secondary)",
          items: svcs.slice(0, 4),
        });
      }
      const pvcs = podToPVCs.get(id) ?? [];
      if (pvcs.length > 0) {
        related.push({ kind: "PVCs", color: "var(--warning)", items: pvcs });
      }
      ttMap.set(id, {
        kind: "pod",
        name: pod.metadata.name,
        namespace: pod.metadata.namespace,
        health: getHealthStatus("pod", pod),
        nodeName: pod.spec?.nodeName,
        podPhase: pod.status?.phase,
        containers: (pod.spec?.containers ?? []).map((c) => c.name),
        related,
      });
    }

    for (const pvc of k8sPVCs) {
      const id = `pvc-${pvc.metadata.namespace}-${pvc.metadata.name}`;
      const related: TopoTooltipData["related"] = [];
      const pods = pvcToPods.get(id) ?? [];
      if (pods.length > 0) {
        related.push({
          kind: "Pods",
          color: "var(--success)",
          items: pods.slice(0, 6),
        });
      }
      ttMap.set(id, {
        kind: "pvc",
        name: pvc.metadata.name,
        namespace: pvc.metadata.namespace,
        health: getHealthStatus("pvc", pvc),
        pvcPhase: pvc.status?.phase,
        volumeName: pvc.spec?.volumeName,
        related,
      });
    }

    tooltipData.value = ttMap;
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
    // Hide tooltip while dragging
    tooltip.value = null;
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
      ref={containerRef}
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
          const isHovered = tooltip.value?.nodeId === node.id;
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
                onMouseEnter={(e: MouseEvent) => {
                  cachedRect.current =
                    containerRef.current?.getBoundingClientRect() ?? null;
                  const cr = cachedRect.current;
                  if (cr) {
                    tooltip.value = {
                      nodeId: node.id,
                      x: e.clientX - cr.left,
                      y: e.clientY - cr.top,
                    };
                  }
                }}
                onMouseMove={(e: MouseEvent) => {
                  const cr = cachedRect.current;
                  if (cr) {
                    tooltip.value = {
                      nodeId: node.id,
                      x: e.clientX - cr.left,
                      y: e.clientY - cr.top,
                    };
                  }
                }}
                onMouseLeave={() => {
                  tooltip.value = null;
                }}
              >
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

      {/* Rich tooltip overlay */}
      {tooltip.value && (() => {
        const tt = tooltipData.value.get(tooltip.value.nodeId);
        if (!tt) return null;
        const container = containerRef.current;
        const cw = container?.offsetWidth ?? 600;
        const ch = container?.offsetHeight ?? 400;
        // Position tooltip with offset, flip if near edges
        const OFFSET = 14;
        const TT_W = 260;
        const TT_H_EST = 160;
        const px = tooltip.value.x;
        const py = tooltip.value.y;
        const flipX = px + TT_W + OFFSET > cw;
        const flipY = py + TT_H_EST + OFFSET > ch;
        const left = flipX ? px - TT_W - OFFSET : px + OFFSET;
        const top = flipY ? py - TT_H_EST - OFFSET : py + OFFSET;

        const kindColors: Record<string, string> = {
          node: "var(--accent)",
          service: "var(--accent-secondary)",
          workload: "var(--info)",
          pod: "var(--success)",
          pvc: "var(--warning)",
        };
        const kindLabels: Record<string, string> = {
          node: "Node",
          service: "Service",
          workload: tt.workloadKind ?? "Workload",
          pod: "Pod",
          pvc: "PVC",
        };
        const healthColors: Record<string, string> = {
          healthy: "var(--success)",
          warning: "var(--warning)",
          error: "var(--error)",
        };
        const healthLabels: Record<string, string> = {
          healthy: "Healthy",
          warning: "Warning",
          error: "Error",
        };
        const accentColor = kindColors[tt.kind] ?? "var(--accent)";

        return (
          <div
            style={{
              position: "absolute",
              left: `${Math.max(0, left)}px`,
              top: `${Math.max(0, top)}px`,
              width: `${TT_W}px`,
              pointerEvents: "none",
              zIndex: 50,
              animation: "topo-tooltip-in 50ms ease-out",
            }}
          >
            <div
              style={{
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-primary)",
                borderRadius: "var(--radius-sm)",
                boxShadow:
                  `0 8px 32px rgba(0,0,0,0.4), 0 0 0 1px ${accentColor}22`,
                overflow: "hidden",
                fontFamily: "var(--font-sans)",
              }}
            >
              {/* Header */}
              <div
                style={{
                  padding: "8px 10px",
                  borderBottom: "1px solid var(--border-subtle)",
                  display: "flex",
                  alignItems: "center",
                  gap: "8px",
                  background:
                    `linear-gradient(135deg, ${accentColor}12, transparent)`,
                }}
              >
                <span
                  style={{
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    width: "20px",
                    height: "20px",
                    borderRadius: "4px",
                    background: `${accentColor}20`,
                    color: accentColor,
                    fontSize: "9px",
                    fontWeight: 700,
                    fontFamily: "var(--font-mono)",
                    flexShrink: 0,
                  }}
                >
                  {kindLabels[tt.kind]?.substring(0, 3).toUpperCase()}
                </span>
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div
                    style={{
                      fontSize: "12px",
                      fontWeight: 600,
                      color: "var(--text-primary)",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {tt.name}
                  </div>
                  {tt.namespace && (
                    <div
                      style={{
                        fontSize: "10px",
                        color: "var(--text-muted)",
                        marginTop: "1px",
                      }}
                    >
                      {tt.namespace}
                    </div>
                  )}
                </div>
                <span
                  style={{
                    width: "7px",
                    height: "7px",
                    borderRadius: "50%",
                    background: healthColors[tt.health],
                    boxShadow: `0 0 6px ${healthColors[tt.health]}`,
                    flexShrink: 0,
                  }}
                />
              </div>

              {/* Details */}
              <div style={{ padding: "6px 10px", fontSize: "11px" }}>
                {/* Kind-specific details */}
                <div
                  style={{
                    display: "flex",
                    flexWrap: "wrap",
                    gap: "4px 12px",
                    color: "var(--text-secondary)",
                    lineHeight: "18px",
                  }}
                >
                  <span>
                    <span style={{ color: "var(--text-muted)" }}>Status</span>
                    <span style={{ color: healthColors[tt.health] }}>
                      {tt.podPhase ?? tt.pvcPhase ?? healthLabels[tt.health]}
                    </span>
                  </span>
                  {tt.nodeName && (
                    <span>
                      <span style={{ color: "var(--text-muted)" }}>Node</span>
                      <span style={{ color: "var(--accent)" }}>
                        {tt.nodeName}
                      </span>
                    </span>
                  )}
                  {tt.volumeName && (
                    <span>
                      <span style={{ color: "var(--text-muted)" }}>PV</span>
                      {tt.volumeName}
                    </span>
                  )}
                  {tt.containers && tt.containers.length > 0 && (
                    <span>
                      <span style={{ color: "var(--text-muted)" }}>
                        {tt.containers.length === 1
                          ? "Container "
                          : `${tt.containers.length} containers `}
                      </span>
                      {tt.containers.length === 1 ? tt.containers[0] : ""}
                    </span>
                  )}
                  {tt.containers && tt.containers.length > 1 && (
                    <div
                      style={{
                        width: "100%",
                        display: "flex",
                        flexWrap: "wrap",
                        gap: "3px",
                        marginTop: "2px",
                      }}
                    >
                      {tt.containers.map((c) => (
                        <span
                          key={c}
                          style={{
                            padding: "1px 5px",
                            borderRadius: "3px",
                            background: "var(--bg-hover)",
                            fontSize: "10px",
                            fontFamily: "var(--font-mono)",
                            color: "var(--text-secondary)",
                          }}
                        >
                          {c}
                        </span>
                      ))}
                    </div>
                  )}
                </div>

                {/* Related resources */}
                {tt.related.length > 0 && (
                  <div
                    style={{
                      marginTop: "6px",
                      borderTop: "1px solid var(--border-subtle)",
                      paddingTop: "6px",
                    }}
                  >
                    {tt.related.map((rel) => (
                      <div key={rel.kind} style={{ marginBottom: "4px" }}>
                        <div
                          style={{
                            fontSize: "9px",
                            fontWeight: 600,
                            textTransform: "uppercase",
                            letterSpacing: "0.5px",
                            color: rel.color,
                            marginBottom: "3px",
                          }}
                        >
                          {rel.kind}
                          <span
                            style={{
                              color: "var(--text-muted)",
                              fontWeight: 400,
                              marginLeft: "4px",
                            }}
                          >
                            {rel.items.length}
                          </span>
                        </div>
                        <div
                          style={{
                            display: "flex",
                            flexWrap: "wrap",
                            gap: "3px",
                          }}
                        >
                          {rel.items.map((item) => (
                            <span
                              key={item}
                              style={{
                                padding: "1px 6px",
                                borderRadius: "3px",
                                background:
                                  `color-mix(in srgb, ${rel.color} 10%, transparent)`,
                                border:
                                  `1px solid color-mix(in srgb, ${rel.color} 20%, transparent)`,
                                fontSize: "10px",
                                fontFamily: "var(--font-mono)",
                                color: "var(--text-secondary)",
                                maxWidth: "140px",
                                overflow: "hidden",
                                textOverflow: "ellipsis",
                                whiteSpace: "nowrap",
                              }}
                            >
                              {item}
                            </span>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </div>
        );
      })()}

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
