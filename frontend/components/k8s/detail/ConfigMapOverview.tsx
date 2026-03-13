import type { K8sResource } from "@/lib/k8s-types.ts";
import type { ConfigMap } from "@/lib/k8s-types.ts";

export function ConfigMapOverview({ resource }: { resource: K8sResource }) {
  const cm = resource as ConfigMap;
  const entries = Object.entries(cm.data ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div class="space-y-4">
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Data ({entries.length} {entries.length === 1 ? "key" : "keys"})
        </h4>
        {entries.length === 0
          ? (
            <p class="text-sm text-slate-500 dark:text-slate-400">
              No data keys.
            </p>
          )
          : (
            <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-slate-200 dark:border-slate-700">
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                      Key
                    </th>
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                      Value Preview
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                  {entries.map(([key, value]) => (
                    <tr key={key}>
                      <td class="px-3 py-1.5 font-mono text-xs text-cyan-700 dark:text-cyan-400 whitespace-nowrap align-top">
                        {key}
                      </td>
                      <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300 break-all">
                        <pre class="whitespace-pre-wrap text-xs font-mono text-slate-600 dark:text-slate-400">
                          {value.length > 200 ? `${value.slice(0, 200)}...` : value}
                        </pre>
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
