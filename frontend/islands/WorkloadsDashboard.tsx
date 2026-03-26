import { useSignal } from"@preact/signals";
import { useEffect } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet } from"@/lib/api.ts";
import { selectedNamespace } from"@/lib/namespace.ts";
import { DOMAIN_SECTIONS } from"@/lib/constants.ts";
import SubNav from"@/islands/SubNav.tsx";
import ResourceTable from"@/islands/ResourceTable.tsx";
import { SummaryRing } from"@/components/ui/SummaryRing.tsx";

interface SummaryData {
 totalDeployments: number;
 availableDeployments: number;
 totalPods: number;
 runningPods: number;
 pendingPods: number;
 failedPods: number;
}

const EMPTY_SUMMARY: SummaryData = {
 totalDeployments: 0,
 availableDeployments: 0,
 totalPods: 0,
 runningPods: 0,
 pendingPods: 0,
 failedPods: 0,
};

const workloadsSection = DOMAIN_SECTIONS.find((s) => s.id ==="workloads")!;

function resolveKind(currentPath: string): {
 kind: string;
 title: string;
 createHref?: string;
} {
 const tabs = workloadsSection.tabs ?? [];
 for (const tab of tabs) {
 if (
 tab.href === currentPath ||
 (currentPath.startsWith(tab.href) &&
 currentPath[tab.href.length] ==="/")
 ) {
 const label = tab.label;
 return {
 kind: tab.kind!,
 title: label,
 createHref: `${tab.href}/new`,
 };
 }
 }
 // Default: Deployments
 return {
 kind:"deployments",
 title:"Deployments",
 createHref:"/workloads/deployments/new",
 };
}

export default function WorkloadsDashboard(
 { currentPath }: { currentPath: string },
) {
 const summary = useSignal<SummaryData>(EMPTY_SUMMARY);
 const loading = useSignal(true);
 const namespace = selectedNamespace.value;

 useEffect(() => {
 if (!IS_BROWSER) return;

 loading.value = true;

 const nsPath = namespace && namespace !=="all" ? `/${namespace}` :"";

 const fetchSummary = async () => {
 try {
 const [deploymentsRes, podsRes] = await Promise.all([
 apiGet<{
 data: Array<{ status?: Record<string, unknown> }>;
 metadata?: { total: number };
 }>(`/v1/resources/deployments${nsPath}?limit=500`),
 apiGet<{
 data: Array<{ status?: { phase?: string } }>;
 metadata?: { total: number };
 }>(`/v1/resources/pods${nsPath}?limit=500`),
 ]);

 const deps = deploymentsRes.data ?? [];
 const totalDeps = deploymentsRes.metadata?.total ?? deps.length;
 let availableDeps = 0;
 for (const d of deps) {
 const available = (d.status as Record<string, unknown>)
 ?.availableReplicas;
 if (available && Number(available) > 0) availableDeps++;
 }

 const pods = podsRes.data ?? [];
 const totalPods = podsRes.metadata?.total ?? pods.length;
 let running = 0;
 let pending = 0;
 let failed = 0;
 for (const p of pods) {
 const phase = p.status?.phase;
 if (phase ==="Running" || phase ==="Succeeded") running++;
 else if (phase ==="Pending") pending++;
 else if (phase ==="Failed") failed++;
 }

 summary.value = {
 totalDeployments: totalDeps,
 availableDeployments: availableDeps,
 totalPods: totalPods,
 runningPods: running,
 pendingPods: pending,
 failedPods: failed,
 };
 } catch {
 summary.value = EMPTY_SUMMARY;
 } finally {
 loading.value = false;
 }
 };

 fetchSummary();
 }, [namespace]);

 const { kind, title, createHref } = resolveKind(currentPath);
 const s = summary.value;

 const summaryCards = [
 {
 label:"Deployments",
 value: s.totalDeployments,
 max: s.totalDeployments,
 color:"var(--accent)",
 },
 {
 label:"Available",
 value: s.availableDeployments,
 max: s.totalDeployments,
 color:"var(--success)",
 },
 {
 label:"Pods Running",
 value: s.runningPods,
 max: s.totalPods,
 color:"var(--success)",
 },
 {
 label:"Pods Pending",
 value: s.pendingPods,
 max: s.totalPods,
 color:"var(--warning)",
 },
 {
 label:"Pods Failed",
 value: s.failedPods,
 max: s.totalPods,
 color:"var(--error)",
 },
 {
 label:"Total Pods",
 value: s.totalPods,
 max: s.totalPods,
 color:"var(--accent)",
 },
 ];

 return (
 <div style={{ display:"flex", flexDirection:"column", height:"100%" }}>
 {/* Page header */}
 <div
 style={{
 padding:"16px 20px 12px",
 display:"flex",
 alignItems:"center",
 justifyContent:"space-between",
 }}
 >
 <div>
 <h1
 style={{
 fontSize:"20px",
 fontWeight: 600,
 letterSpacing:"-0.02em",
 color:"var(--text-primary)",
 margin: 0,
 }}
 >
 Workloads
 </h1>
 <p
 style={{
 fontSize:"13px",
 color:"var(--text-muted)",
 marginTop:"2px",
 margin: 0,
 }}
 >
 Manage Deployments, StatefulSets, DaemonSets, Pods, Jobs, and
 CronJobs
 </p>
 </div>
 <a
 href="/workloads/deployments/new"
 style={{
 display:"inline-flex",
 alignItems:"center",
 gap:"6px",
 padding:"7px 14px",
 fontSize:"13px",
 fontWeight: 500,
 color:"white",
 background:"var(--accent)",
 borderRadius:"6px",
 textDecoration:"none",
 border:"none",
 cursor:"pointer",
 }}
 >
 + New Workload
 </a>
 </div>

 {/* Sub-navigation */}
 <SubNav tabs={workloadsSection.tabs ?? []} currentPath={currentPath} />

 {/* Summary strip */}
 <div
 style={{
 display:"grid",
 gridTemplateColumns:"repeat(6, 1fr)",
 gap:"12px",
 padding:"16px 20px",
 borderBottom:"1px solid var(--border-subtle)",
 }}
 >
 {summaryCards.map((card) => (
 <div
 key={card.label}
 style={{
 display:"flex",
 alignItems:"center",
 gap:"10px",
 padding:"10px 12px",
 borderRadius:"8px",
 background:"var(--bg-surface)",
 border:"1px solid var(--border-subtle)",
 }}
 >
 <SummaryRing
 value={loading.value ? 0 : card.value}
 max={Math.max(card.max, 1)}
 size={36}
 color={card.color}
 />
 <div>
 <div
 style={{
 fontSize:"11px",
 fontWeight: 500,
 color:"var(--text-muted)",
 textTransform:"uppercase",
 letterSpacing:"0.04em",
 }}
 >
 {card.label}
 </div>
 <div
 style={{
 fontSize:"18px",
 fontWeight: 600,
 color:"var(--text-primary)",
 lineHeight: 1.2,
 }}
 >
 {loading.value ?"-" : card.value}
 </div>
 </div>
 </div>
 ))}
 </div>

 {/* Resource table */}
 <div style={{ flex: 1, minHeight: 0, overflow:"auto" }}>
 <ResourceTable
 kind={kind}
 title={title}
 createHref={createHref}
 />
 </div>
 </div>
 );
}
