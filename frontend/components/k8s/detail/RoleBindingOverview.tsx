import type { K8sResource, RoleBinding } from "@/lib/k8s-types.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import { resolveRoleHref } from "@/lib/rbac-utils.ts";

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
          <Field label="Kind" value={roleRef?.kind ?? "-"} />
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
            value={roleRef?.apiGroup ?? "rbac.authorization.k8s.io"}
          />
        </div>
      </div>

      {/* Subjects */}
      {subjects.length > 0 && (
        <div>
          <SectionHeader>Subjects</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Kind
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Name
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Namespace
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {subjects.map((s, i) => (
                  <tr key={i}>
                    <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                      {s.kind}
                    </td>
                    <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                      {s.name}
                    </td>
                    <td class="px-3 py-1.5 text-slate-600 dark:text-slate-400">
                      {s.namespace ?? "-"}
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
