import { useSignal } from"@preact/signals";
import { IS_BROWSER } from"fresh/runtime";
import { useEffect } from"preact/hooks";
import { apiGet } from"@/lib/api.ts";
import { StatusBadge } from"@/components/ui/StatusBadge.tsx";

interface RelatedPodsProps {
 namespace: string;
 /** Label selector to find pods (e.g.,"app=nginx") */
 labelSelector: string;
 /** Parent resource name for display */
 parentName: string;
}

interface PodInfo {
 metadata: {
 name: string;
 namespace: string;
 creationTimestamp: string;
 };
 spec: {
 nodeName: string;
 containers: { name: string }[];
 };
 status: {
 phase: string;
 containerStatuses?: {
 name: string;
 ready: boolean;
 restartCount: number;
 }[];
 };
}

function podStatus(pod: PodInfo): string {
 const phase = pod.status?.phase ??"Unknown";
 if (phase ==="Running") {
 const allReady = pod.status?.containerStatuses?.every((c) => c.ready);
 return allReady ?"running" :"warning";
 }
 if (phase ==="Succeeded") return"completed";
 if (phase ==="Failed") return"failed";
 if (phase ==="Pending") return"warning";
 return"unknown";
}

export default function RelatedPods(
 { namespace, labelSelector, parentName }: RelatedPodsProps,
) {
 const pods = useSignal<PodInfo[]>([]);
 const loading = useSignal(true);
 const error = useSignal("");

 useEffect(() => {
 if (!IS_BROWSER) return;
 fetchPods();
 }, [namespace, labelSelector]);

 async function fetchPods() {
 loading.value = true;
 error.value ="";
 try {
 const res = await apiGet<PodInfo[]>(
 `/v1/resources/pods/${namespace}?labelSelector=${
 encodeURIComponent(labelSelector)
 }`,
 );
 pods.value = Array.isArray(res.data) ? res.data : [];
 } catch (err) {
 error.value = err instanceof Error ? err.message :"Failed to load pods";
 } finally {
 loading.value = false;
 }
 }

 if (!IS_BROWSER) return null;

 if (loading.value) {
 return (
 <div class="flex justify-center py-8">
 <div class="h-5 w-5 animate-spin rounded-full border-2 border-border-primary border-t-brand" />
 </div>
 );
 }

 if (error.value) {
 return (
 <div class="rounded-md bg-danger-dim px-4 py-3 text-sm text-red-700 bg-danger-dim text-danger">
 {error.value}
 </div>
 );
 }

 if (pods.value.length === 0) {
 return (
 <div class="py-8 text-center text-sm text-text-muted">
 No pods found for {parentName}
 </div>
 );
 }

 return (
 <div class="overflow-x-auto rounded-lg border border-border-primary">
 <table class="w-full text-sm">
 <thead class="bg-surface">
 <tr>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Pod
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Status
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Restarts
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Node
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Containers
 </th>
 <th class="px-3 py-2 text-left font-medium text-text-secondary">
 Actions
 </th>
 </tr>
 </thead>
 <tbody class="divide-y divide-border-primary">
 {pods.value.map((pod) => {
 const restarts = pod.status?.containerStatuses?.reduce(
 (sum, c) => sum + (c.restartCount || 0),
 0,
 ) ?? 0;
 return (
 <tr
 key={pod.metadata.name}
 class="hover:bg-hover/50"
 >
 <td class="px-3 py-2">
 <a
 href={`/workloads/pods/${pod.metadata.namespace}/${pod.metadata.name}`}
 class="font-medium text-brand hover:underline"
 >
 {pod.metadata.name}
 </a>
 </td>
 <td class="px-3 py-2">
 <StatusBadge
 status={podStatus(pod)}
 label={pod.status?.phase ??"Unknown"}
 />
 </td>
 <td class="px-3 py-2 text-text-secondary">
 {restarts}
 </td>
 <td class="px-3 py-2 text-text-secondary">
 {pod.spec?.nodeName ??"-"}
 </td>
 <td class="px-3 py-2 text-text-secondary">
 {pod.spec?.containers?.length ?? 0}
 </td>
 <td class="px-3 py-2">
 <a
 href={`/workloads/pods/${pod.metadata.namespace}/${pod.metadata.name}#logs`}
 class="text-xs text-brand hover:underline"
 >
 Logs
 </a>
 </td>
 </tr>
 );
 })}
 </tbody>
 </table>
 </div>
 );
}
