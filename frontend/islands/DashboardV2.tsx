import { useSignal } from"@preact/signals";
import { useEffect } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet } from"@/lib/api.ts";

interface CountResponse { items: unknown[]; metadata?: { total?: number } }
import { age } from"@/lib/format.ts";
import type { K8sEvent } from"@/lib/k8s-types.ts";
import { Skeleton } from"@/components/ui/Skeleton.tsx";
import { StatusDot } from"@/components/ui/StatusDot.tsx";
import HealthScoreRing from"@/islands/HealthScoreRing.tsx";
import MetricCard from"@/islands/MetricCard.tsx";
import UtilizationGauge from"@/islands/UtilizationGauge.tsx";
import ClusterTopology from"@/islands/ClusterTopology.tsx";

interface ClusterInfoData {
 clusterID: string;
 kubernetesVersion: string;
 platform: string;
 nodeCount: number;
 kubecenter: {
 version: string;
 commit: string;
 buildDate: string;
 };
}

interface ResourceCounts {
 deployments: number;
 pods: number;
 services: number;
 namespaces: number;
 nodes: number;
}

interface UtilData {
 value: number;
 used: string;
 total: string;
}

export default function DashboardV2() {
 const clusterInfo = useSignal<ClusterInfoData | null>(null);
 const counts = useSignal<ResourceCounts>({
 deployments: 0,
 pods: 0,
 services: 0,
 namespaces: 0,
 nodes: 0,
 });
 const events = useSignal<K8sEvent[]>([]);
 const cpuUtil = useSignal<UtilData | null>(null);
 const memUtil = useSignal<UtilData | null>(null);
 const loading = useSignal(true);

 useEffect(() => {
 if (!IS_BROWSER) return;

 async function load() {
 loading.value = true;

 const [
 infoRes,
 deplRes,
 podRes,
 svcRes,
 nsRes,
 eventsRes,
 cpuRes,
 memRes,
 ] = await Promise.allSettled([
 apiGet<ClusterInfoData>("/v1/cluster/info"),
 apiGet<CountResponse>("/v1/resources/deployments?limit=1"),
 apiGet<CountResponse>("/v1/resources/pods?limit=1"),
 apiGet<CountResponse>("/v1/resources/services?limit=1"),
 apiGet<CountResponse>("/v1/resources/namespaces?limit=1"),
 apiGet<K8sEvent[]>("/v1/resources/events?limit=10"),
 apiGet<{ result: { value: [number, string] }[] }>(
 `/v1/monitoring/query?query=${
 encodeURIComponent(
 '100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)',
 )
 }`,
 ),
 apiGet<{ result: { value: [number, string] }[] }>(
 `/v1/monitoring/query?query=${
 encodeURIComponent(
"(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100",
 )
 }`,
 ),
 ]);

 if (infoRes.status ==="fulfilled") {
 clusterInfo.value = infoRes.value.data;
 }

 const nodeCount = infoRes.status ==="fulfilled"
 ? infoRes.value.data?.nodeCount ?? 0
 : 0;

 counts.value = {
 nodes: nodeCount,
 deployments: deplRes.status ==="fulfilled"
 ? deplRes.value.metadata?.total ?? 0
 : 0,
 pods: podRes.status ==="fulfilled"
 ? podRes.value.metadata?.total ?? 0
 : 0,
 services: svcRes.status ==="fulfilled"
 ? svcRes.value.metadata?.total ?? 0
 : 0,
 namespaces: nsRes.status ==="fulfilled"
 ? nsRes.value.metadata?.total ?? 0
 : 0,
 };

 if (
 eventsRes.status ==="fulfilled" &&
 Array.isArray(eventsRes.value.data)
 ) {
 events.value = eventsRes.value.data;
 }

 // CPU utilization
 if (cpuRes.status ==="fulfilled" && cpuRes.value.data?.result?.[0]) {
 const val = parseFloat(cpuRes.value.data.result[0].value[1]);
 cpuUtil.value = {
 value: val,
 used: `${val.toFixed(1)}%`,
 total:"100%",
 };
 }

 // Memory utilization
 if (memRes.status ==="fulfilled" && memRes.value.data?.result?.[0]) {
 const val = parseFloat(memRes.value.data.result[0].value[1]);
 memUtil.value = {
 value: val,
 used: `${val.toFixed(1)}%`,
 total:"100%",
 };
 }

 loading.value = false;
 }

 load();
 }, []);

 if (!IS_BROWSER) {
 return <div style={{ minHeight:"400px" }} />;
 }

 if (loading.value) {
 return (
 <div style={{ display:"flex", flexDirection:"column", gap:"16px" }}>
 <Skeleton class="h-10 w-64" />
 <div style={{ display:"grid", gridTemplateColumns:"repeat(12, 1fr)", gap:"16px" }}>
 <div style={{ gridColumn:"span 4" }}>
 <Skeleton class="h-56 w-full rounded-lg" />
 </div>
 <div style={{ gridColumn:"span 8" }}>
 <div style={{ display:"grid", gridTemplateColumns:"1fr 1fr", gap:"12px" }}>
 {[1, 2, 3, 4].map((i) => (
 <Skeleton key={i} class="h-28 w-full rounded-lg" />
 ))}
 </div>
 </div>
 </div>
 <div style={{ display:"grid", gridTemplateColumns:"1fr 1fr", gap:"16px" }}>
 <Skeleton class="h-36 w-full rounded-lg" />
 <Skeleton class="h-36 w-full rounded-lg" />
 </div>
 </div>
 );
 }

 const info = clusterInfo.value;
 const c = counts.value;

 const metricCards = [
 {
 value: c.nodes,
 label:"Nodes",
 status: c.nodes > 0 ?"success" :"warning",
 statusText: c.nodes > 0 ?"Healthy" :"None",
 href:"/cluster/nodes",
 },
 {
 value: c.pods,
 label:"Pods",
 status: c.pods > 0 ?"success" :"info",
 statusText: c.pods > 0 ?"Running" :"None",
 href:"/workloads/pods",
 },
 {
 value: c.services,
 label:"Services",
 status:"info" as const,
 statusText:"Active",
 href:"/networking/services",
 },
 {
 value: c.deployments,
 label:"Deployments",
 status: c.deployments > 0 ?"success" :"info",
 statusText: c.deployments > 0 ?"Available" :"None",
 href:"/workloads/deployments",
 },
 ] as const;

 return (
 <div style={{ display:"flex", flexDirection:"column", gap:"16px" }}>
 {/* Header row */}
 <div
 style={{
 display:"flex",
 justifyContent:"space-between",
 alignItems:"flex-start",
 }}
 >
 <div>
 <h1
 style={{
 fontSize:"24px",
 fontWeight: 700,
 color:"var(--text-primary)",
 margin: 0,
 }}
 >
 Cluster Overview
 </h1>
 {info && (
 <p
 style={{
 fontSize:"13px",
 color:"var(--text-muted)",
 marginTop:"4px",
 }}
 >
 {info.platform} &middot; Kubernetes {info.kubernetesVersion}
 {` \u00B7 ${c.nodes} node${c.nodes !== 1 ?"s" :""}`}
 </p>
 )}
 </div>
 <div style={{ display:"flex", gap:"8px" }}>
 <a
 href="/workloads/deployments/new"
 style={{
 display:"inline-flex",
 alignItems:"center",
 gap:"6px",
 padding:"8px 14px",
 borderRadius:"var(--radius)",
 background:"var(--accent)",
 color:"#fff",
 fontSize:"13px",
 fontWeight: 500,
 textDecoration:"none",
 border:"none",
 }}
 >
 <svg
 width="14"
 height="14"
 viewBox="0 0 24 24"
 fill="none"
 stroke="currentColor"
 stroke-width="2"
 stroke-linecap="round"
 stroke-linejoin="round"
 >
 <circle cx="12" cy="12" r="10" />
 <path d="M8 12h8M12 8v8" />
 </svg>
 Deploy
 </a>
 <a
 href="/tools/yaml-apply"
 style={{
 display:"inline-flex",
 alignItems:"center",
 gap:"6px",
 padding:"8px 14px",
 borderRadius:"var(--radius)",
 background:"var(--bg-elevated)",
 color:"var(--text-primary)",
 fontSize:"13px",
 fontWeight: 500,
 textDecoration:"none",
 border:"1px solid var(--border-primary)",
 }}
 >
 <svg
 width="14"
 height="14"
 viewBox="0 0 24 24"
 fill="none"
 stroke="currentColor"
 stroke-width="2"
 stroke-linecap="round"
 stroke-linejoin="round"
 >
 <path d="M16 18l2-2-2-2M8 18l-2-2 2-2M12 10l-2 8" />
 </svg>
 YAML
 </a>
 </div>
 </div>

 {/* Health Score + Metric Cards row */}
 <div
 style={{
 display:"grid",
 gridTemplateColumns:"repeat(12, 1fr)",
 gap:"16px",
 }}
 >
 {/* Health Score */}
 <div
 style={{
 gridColumn:"span 4",
 background:"var(--bg-surface)",
 border:"1px solid var(--border-primary)",
 borderRadius:"var(--radius)",
 padding:"16px",
 display:"flex",
 flexDirection:"column",
 }}
 >
 <div
 style={{
 display:"flex",
 alignItems:"center",
 gap:"8px",
 marginBottom:"12px",
 }}
 >
 <StatusDot status="success" pulse size={8} />
 <span
 style={{
 fontSize:"13px",
 fontWeight: 600,
 color:"var(--text-primary)",
 }}
 >
 Cluster Health
 </span>
 </div>
 <HealthScoreRing />
 </div>

 {/* Metric Cards */}
 <div
 style={{
 gridColumn:"span 8",
 display:"grid",
 gridTemplateColumns:"1fr 1fr",
 gap:"12px",
 }}
 >
 {metricCards.map((card) => (
 <MetricCard
 key={card.label}
 value={card.value}
 label={card.label}
 status={card.status as"success" |"warning" |"error" |"info"}
 statusText={card.statusText}
 href={card.href}
 />
 ))}
 </div>
 </div>

 {/* CPU + Memory Utilization row */}
 <div
 style={{
 display:"grid",
 gridTemplateColumns:"repeat(12, 1fr)",
 gap:"16px",
 }}
 >
 <div style={{ gridColumn:"span 6" }}>
 {cpuUtil.value
 ? (
 <UtilizationGauge
 title="CPU Utilization"
 value={cpuUtil.value.value}
 used={cpuUtil.value.used}
 total={cpuUtil.value.total}
 color="var(--accent)"
 secondaryColor="var(--accent-secondary)"
 />
 )
 : (
 <UtilizationGauge
 title="CPU Utilization"
 value={0}
 used="N/A"
 total="N/A"
 color="var(--accent)"
 secondaryColor="var(--accent-secondary)"
 />
 )}
 </div>
 <div style={{ gridColumn:"span 6" }}>
 {memUtil.value
 ? (
 <UtilizationGauge
 title="Memory Utilization"
 value={memUtil.value.value}
 used={memUtil.value.used}
 total={memUtil.value.total}
 color="var(--accent-secondary)"
 secondaryColor="var(--accent)"
 />
 )
 : (
 <UtilizationGauge
 title="Memory Utilization"
 value={0}
 used="N/A"
 total="N/A"
 color="var(--accent-secondary)"
 secondaryColor="var(--accent)"
 />
 )}
 </div>
 </div>

 {/* Topology + Events row */}
 <div
 style={{
 display:"grid",
 gridTemplateColumns:"repeat(12, 1fr)",
 gap:"16px",
 }}
 >
 {/* Cluster Topology placeholder */}
 <div
 style={{
 gridColumn:"span 7",
 background:"var(--bg-surface)",
 border:"1px solid var(--border-primary)",
 borderRadius:"var(--radius)",
 padding:"16px",
 minHeight:"280px",
 }}
 >
 <div
 style={{
 display:"flex",
 alignItems:"center",
 gap:"8px",
 marginBottom:"16px",
 }}
 >
 <StatusDot status="info" pulse size={8} />
 <span
 style={{
 fontSize:"13px",
 fontWeight: 600,
 color:"var(--text-primary)",
 }}
 >
 Cluster Topology
 </span>
 </div>
 <ClusterTopology />
 </div>

 {/* Recent Events */}
 <div
 style={{
 gridColumn:"span 5",
 background:"var(--bg-surface)",
 border:"1px solid var(--border-primary)",
 borderRadius:"var(--radius)",
 padding:"16px",
 minHeight:"280px",
 display:"flex",
 flexDirection:"column",
 }}
 >
 <div
 style={{
 fontSize:"13px",
 fontWeight: 600,
 color:"var(--text-primary)",
 marginBottom:"12px",
 }}
 >
 Recent Events
 </div>
 <div
 style={{
 flex: 1,
 display:"flex",
 flexDirection:"column",
 gap:"6px",
 overflow:"auto",
 }}
 >
 {events.value.length === 0
 ? (
 <div
 style={{
 color:"var(--text-muted)",
 fontSize:"12px",
 textAlign:"center",
 paddingTop:"40px",
 }}
 >
 No recent events
 </div>
 )
 : events.value.map((evt, idx) => {
 const isWarning = evt.type ==="Warning";
 return (
 <div
 key={`${evt.metadata?.uid ?? idx}`}
 style={{
 display:"flex",
 alignItems:"flex-start",
 gap:"8px",
 padding:"6px 0",
 borderBottom: idx < events.value.length - 1
 ?"1px solid var(--border-subtle)"
 :"none",
 }}
 >
 <div style={{ paddingTop:"4px", flexShrink: 0 }}>
 <StatusDot
 status={isWarning ?"warning" :"info"}
 size={6}
 />
 </div>
 <div
 style={{
 flex: 1,
 minWidth: 0,
 fontSize:"12px",
 color:"var(--text-secondary)",
 lineHeight:"1.4",
 }}
 >
 {evt.involvedObject && (
 <span
 style={{
 color:"var(--accent)",
 fontFamily:"var(--font-mono, monospace)",
 fontWeight: 500,
 }}
 >
 {evt.involvedObject.name}{""}
 </span>
 )}
 <span
 style={{
 overflow:"hidden",
 textOverflow:"ellipsis",
 whiteSpace:"nowrap",
 display:"inline",
 }}
 >
 {evt.message}
 </span>
 </div>
 <span
 style={{
 fontSize:"11px",
 color:"var(--text-muted)",
 flexShrink: 0,
 whiteSpace:"nowrap",
 }}
 >
 {evt.metadata?.creationTimestamp
 ? age(evt.metadata.creationTimestamp)
 :""}
 </span>
 </div>
 );
 })}
 </div>
 </div>
 </div>
 </div>
 );
}
