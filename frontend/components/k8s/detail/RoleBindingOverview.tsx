import type { K8sResource } from "@/lib/k8s-types.ts";
import type { RoleBinding } from "@/lib/k8s-types.ts";

export function RoleBindingOverview({ resource }: { resource: K8sResource }) {
  const rb = resource as RoleBinding;
  const subjects = rb.subjects ?? [];

  return (
    <div class="space-y-4">
      {/* Role Reference */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Role Reference
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Kind
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {rb.roleRef.kind}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Name
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {rb.roleRef.name}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              API Group
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {rb.roleRef.apiGroup}
            </div>
          </div>
        </div>
      </div>

      {/* Subjects */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Subjects ({subjects.length})
        </h4>
        {subjects.length === 0
          ? (
            <p class="text-sm text-slate-500 dark:text-slate-400">
              No subjects.
            </p>
          )
          : (
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
                      <td class="px-3 py-1.5 font-mono text-slate-700 dark:text-slate-300">
                        {s.name}
                      </td>
                      <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                        {s.namespace ?? "-"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
      </div>
    </div>
  );
}
