import type { K8sResource, StatefulSet } from"@/lib/k8s-types.ts";
import { Field, SectionHeader } from"@/components/ui/Field.tsx";
import { ContainerResourcesTable } from"./ContainerResourcesTable.tsx";
import { KeyValueTable } from"./KeyValueTable.tsx";

export function StatefulSetOverview({ resource }: { resource: K8sResource }) {
 const s = resource as StatefulSet;
 const spec = s.spec;
 const status = s.status;
 const updateStrategy = spec.updateStrategy;
 const containers = spec.template?.spec?.containers ?? [];

 return (
 <div class="space-y-4">
 {/* Replicas */}
 <div>
 <SectionHeader>Replicas</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
 <Field label="Desired" value={String(spec.replicas ?? 1)} />
 <Field label="Ready" value={String(status?.readyReplicas ?? 0)} />
 <Field label="Current" value={String(status?.currentReplicas ?? 0)} />
 <Field label="Updated" value={String(status?.updatedReplicas ?? 0)} />
 </div>
 </div>

 {/* Configuration */}
 <div>
 <SectionHeader>Configuration</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
 <Field label="Service Name" value={spec.serviceName} />
 {updateStrategy && (
 <>
 <Field
 label="Update Strategy"
 value={updateStrategy.type ??"RollingUpdate"}
 />
 {updateStrategy.rollingUpdate?.partition != null && (
 <Field
 label="Partition"
 value={String(updateStrategy.rollingUpdate.partition)}
 />
 )}
 </>
 )}
 </div>
 </div>

 {/* Container Images */}
 {containers.length > 0 && (
 <div>
 <SectionHeader>Containers</SectionHeader>
 <div class="overflow-x-auto rounded-md border border-border-primary">
 <table class="w-full text-sm">
 <thead>
 <tr class="border-b border-border-primary">
 <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
 Name
 </th>
 <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
 Image
 </th>
 </tr>
 </thead>
 <tbody class="divide-y divide-border-subtle">
 {containers.map((c) => (
 <tr key={c.name}>
 <td class="px-3 py-1.5 font-medium text-text-secondary">
 {c.name}
 </td>
 <td class="px-3 py-1.5 font-mono text-xs text-text-secondary break-all">
 {c.image}
 </td>
 </tr>
 ))}
 </tbody>
 </table>
 </div>
 </div>
 )}

 {/* Resource Requests & Limits */}
 <ContainerResourcesTable containers={containers} />

 {/* Selector */}
 {spec.selector?.matchLabels && (
 <KeyValueTable title="Selector" data={spec.selector.matchLabels} />
 )}
 </div>
 );
}
