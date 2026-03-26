import type { K8sResource, RoleBinding } from"@/lib/k8s-types.ts";
import { Field, SectionHeader } from"@/components/ui/Field.tsx";
import { resolveRoleHref } from"@/lib/rbac-utils.ts";

export function RoleBindingOverview({ resource }: { resource: K8sResource }) {
 return <BindingDetail resource={resource} />;
}

export function BindingDetail({ resource }: { resource: K8sResource }) {
 const b = resource as RoleBinding; // same shape as ClusterRoleBinding
 const roleRef = b.roleRef;
 const subjects = b.subjects ?? [];

 return (
 <div class="space-y-4">
 {/* Role Reference */}
 <div>
 <SectionHeader>Role Reference</SectionHeader>
 <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
 <Field label="Kind" value={roleRef?.kind ??"-"} />
 <Field
 label="Name"
 value={roleRef?.name
 ? (
 <a
 href={resolveRoleHref(
 roleRef.kind,
 roleRef.name,
 b.metadata?.namespace,
 )}
 class="text-brand hover:underline"
 >
 {roleRef.name}
 </a>
 )
 : (
"-"
 )}
 />
 <Field
 label="API Group"
 value={roleRef?.apiGroup ??"rbac.authorization.k8s.io"}
 />
 </div>
 </div>

 {/* Subjects */}
 {subjects.length > 0 && (
 <div>
 <SectionHeader>Subjects</SectionHeader>
 <div class="overflow-x-auto rounded-md border border-border-primary">
 <table class="w-full text-sm">
 <thead>
 <tr class="border-b border-border-primary">
 <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
 Kind
 </th>
 <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
 Name
 </th>
 <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
 Namespace
 </th>
 </tr>
 </thead>
 <tbody class="divide-y divide-border-subtle">
 {subjects.map((s, i) => (
 <tr key={i}>
 <td class="px-3 py-1.5 text-text-secondary">
 {s.kind}
 </td>
 <td class="px-3 py-1.5 font-medium text-text-secondary">
 {s.name}
 </td>
 <td class="px-3 py-1.5 text-text-secondary">
 {s.namespace ??"-"}
 </td>
 </tr>
 ))}
 </tbody>
 </table>
 </div>
 </div>
 )}
 </div>
 );
}
