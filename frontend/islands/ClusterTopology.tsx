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
}

interface K8sService {
  metadata: K8sMetadata;
  spec?: { selector?: Record<string, string> };
}

interface K8sPod {
  metadata: K8sMetadata;
  spec?: { nodeName?: string };
}

interface K8sIngress {
  metadata: K8sMetadata;
  spec?: {
    rules?: {
      http?: {
        paths?: { backend?: { service?: { name: string } } }[];
      };
    }[];
  };
}

interface TopoNode {
  id: string;
  label: string;
  abbr: string;
  kind: "node" | "service" | "pod" | "ingress";
  x: number;
  y: number;
  size: number;
}

interface TopoEdge {
  from: string;
  to: string;
  color: string;
}

const MAX_NODES = 5;
const MAX_SERVICES = 6;
const MAX_PODS = 8;
const MAX_INGRESSES = 3;

function matchesSelector(
  selector: Record<string, string> | undefined,
  labels: Record<string, string> | undefined,
): boolean {
  if (!selector || !labels) return false;
  return Object.entries(selector).every(([k, v]) => labels[k] === v);
}

export default function ClusterTopology() {
  const nodes = useSignal<TopoNode[]>([]);
  const edges = useSignal<TopoEdge[]>([]);
  const loading = useSignal(true);
  const containerRef = useRef<HTMLDivElement>(null);
  const dimensions = useSignal({ width: 600, height: 240 });

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

  // Fetch data and compute layout
  useEffect(() => {
    if (!IS_BROWSER) return;

    async function load() {
      loading.value = true;

      const [nodesRes, svcsRes, podsRes, ingRes] = await Promise.allSettled([
        apiGet<K8sNode[]>("/v1/resources/nodes"),
        apiGet<K8sService[]>("/v1/resources/services"),
        apiGet<K8sPod[]>("/v1/resources/pods"),
        apiGet<K8sIngress[]>("/v1/resources/ingresses"),
      ]);

      const k8sNodes =
        (nodesRes.status === "fulfilled" && Array.isArray(nodesRes.value.data)
          ? nodesRes.value.data
          : []).slice(0, MAX_NODES);

      const k8sSvcs =
        (svcsRes.status === "fulfilled" && Array.isArray(svcsRes.value.data)
          ? svcsRes.value.data
          : []).slice(0, MAX_SERVICES);

      const k8sPods =
        (podsRes.status === "fulfilled" && Array.isArray(podsRes.value.data)
          ? podsRes.value.data
          : []).slice(0, MAX_PODS);

      const k8sIngresses =
        (ingRes.status === "fulfilled" && Array.isArray(ingRes.value.data)
          ? ingRes.value.data
          : []).slice(0, MAX_INGRESSES);

      const w = dimensions.value.width;
      const h = dimensions.value.height;

      const topoNodes: TopoNode[] = [];
      const topoEdges: TopoEdge[] = [];

      // Position nodes in columns
      const placeColumn = (
        items: { id: string; label: string; kind: TopoNode["kind"] }[],
        xPct: number,
        size: number,
      ) => {
        const count = items.length;
        if (count === 0) return;
        const spacing = h / (count + 1);
        items.forEach((item, i) => {
          const abbr = item.label.substring(0, 2).toUpperCase();
          topoNodes.push({
            ...item,
            abbr,
            x: w * xPct,
            y: spacing * (i + 1),
            size,
          });
        });
      };

      placeColumn(
        k8sNodes.map((n) => ({
          id: `node-${n.metadata.name}`,
          label: n.metadata.name,
          kind: "node" as const,
        })),
        0.12,
        52,
      );

      placeColumn(
        k8sSvcs.map((s) => ({
          id: `svc-${s.metadata.namespace}-${s.metadata.name}`,
          label: s.metadata.name,
          kind: "service" as const,
        })),
        0.40,
        44,
      );

      placeColumn(
        k8sPods.map((p) => ({
          id: `pod-${p.metadata.namespace}-${p.metadata.name}`,
          label: p.metadata.name,
          kind: "pod" as const,
        })),
        0.62,
        36,
      );

      placeColumn(
        k8sIngresses.map((ig) => ({
          id: `ing-${ig.metadata.namespace}-${ig.metadata.name}`,
          label: ig.metadata.name,
          kind: "ingress" as const,
        })),
        0.85,
        40,
      );

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

      // Build node-to-service edges: if any pod on a node is selected by a service
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

      // Build ingress-to-service edges
      for (const ig of k8sIngresses) {
        const igId = `ing-${ig.metadata.namespace}-${ig.metadata.name}`;
        const backendNames = new Set<string>();
        for (const rule of ig.spec?.rules ?? []) {
          for (const path of rule.http?.paths ?? []) {
            const svcName = path.backend?.service?.name;
            if (svcName) backendNames.add(svcName);
          }
        }
        for (const svcName of backendNames) {
          const svcId = `svc-${ig.metadata.namespace}-${svcName}`;
          if (topoNodes.some((n) => n.id === svcId)) {
            topoEdges.push({
              from: igId,
              to: svcId,
              color: "var(--warning)",
            });
          }
        }
      }

      nodes.value = topoNodes;
      edges.value = topoEdges;
      loading.value = false;
    }

    load();
  }, [dimensions.value.width, dimensions.value.height]);

  if (!IS_BROWSER) {
    return <div style={{ minHeight: "200px" }} />;
  }

  if (loading.value) {
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "200px",
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
          height: "200px",
          color: "var(--text-muted)",
          fontSize: "13px",
        }}
      >
        No resources found
      </div>
    );
  }

  const kindStyles: Record<
    TopoNode["kind"],
    { border: string; bg: string; borderRadius: string }
  > = {
    node: {
      border: "var(--accent)",
      bg: "var(--accent-dim)",
      borderRadius: "50%",
    },
    service: {
      border: "var(--accent-secondary)",
      bg: "rgba(124,92,252,0.08)",
      borderRadius: "50%",
    },
    pod: {
      border: "var(--success)",
      bg: "var(--success-dim)",
      borderRadius: "50%",
    },
    ingress: {
      border: "var(--warning)",
      bg: "var(--warning-dim)",
      borderRadius: "6px",
    },
  };

  const nodeMap = new Map(nodes.value.map((n) => [n.id, n]));

  return (
    <div
      ref={containerRef}
      style={{
        position: "relative",
        width: "100%",
        height: "200px",
        overflow: "hidden",
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
        const style = kindStyles[node.kind];
        return (
          <div
            key={node.id}
            title={node.label}
            style={{
              position: "absolute",
              left: `${node.x - node.size / 2}px`,
              top: `${node.y - node.size / 2}px`,
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
              color: style.border,
              cursor: "default",
              transition: "transform 0.15s ease",
              zIndex: 1,
            }}
            onMouseEnter={(e) => {
              (e.currentTarget as HTMLDivElement).style.transform =
                "scale(1.15)";
            }}
            onMouseLeave={(e) => {
              (e.currentTarget as HTMLDivElement).style.transform = "scale(1)";
            }}
          >
            {node.abbr}
          </div>
        );
      })}
    </div>
  );
}
