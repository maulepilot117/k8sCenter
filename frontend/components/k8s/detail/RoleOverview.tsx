import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Role } from "@/lib/k8s-types.ts";

export function RoleOverview({ resource }: { resource: K8sResource }) {
  const r = resource as Role;
  const rules = r.rules ?? [];

  return (
    <div class="space-y-4">
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Rules ({rules.length})
        </h4>
        {rules.length === 0
          ? (
            <p class="text-sm text-slate-500 dark:text-slate-400">
              No rules defined.
            </p>
          )
          : (
            <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-slate-200 dark:border-slate-700">
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                      API Groups
                    </th>
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                      Resources
                    </th>
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                      Verbs
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                  {rules.map((rule, i) => (
                    <tr key={i}>
                      <td class="px-3 py-1.5 font-mono text-xs text-slate-700 dark:text-slate-300">
                        {rule.apiGroups?.map((g) => g || '""').join(", ") ??
                          "*"}
                      </td>
                      <td class="px-3 py-1.5 font-mono text-xs text-slate-700 dark:text-slate-300">
                        {rule.resources?.join(", ") ?? "*"}
                      </td>
                      <td class="px-3 py-1.5">
                        <div class="flex flex-wrap gap-1">
                          {(rule.verbs ?? []).map((v) => (
                            <span
                              key={v}
                              class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset bg-slate-50 text-slate-600 ring-slate-500/20 dark:bg-slate-500/10 dark:text-slate-400 dark:ring-slate-500/20"
                            >
                              {v}
                            </span>
                          ))}
                        </div>
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
