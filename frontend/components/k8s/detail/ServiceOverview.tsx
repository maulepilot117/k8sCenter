import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Service } from "@/lib/k8s-types.ts";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function ServiceOverview({ resource }: { resource: K8sResource }) {
  const s = resource as Service;
  const spec = s.spec;

  return (
    <div class="space-y-4">
      {/* Summary */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Summary
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Type
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.type}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Cluster IP
            </div>
            <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
              {spec.clusterIP ?? "None"}
            </div>
          </div>
        </div>
      </div>

      {/* Ports */}
      {spec.ports && spec.ports.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Ports
          </h4>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Name
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Protocol
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Port
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Target Port
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {spec.ports.map((p, i) => (
                  <tr key={p.name ?? i}>
                    <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                      {p.name ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                      {p.protocol ?? "TCP"}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-slate-700 dark:text-slate-300">
                      {p.port}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-slate-700 dark:text-slate-300">
                      {p.targetPort != null ? String(p.targetPort) : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Selector */}
      {spec.selector && Object.keys(spec.selector).length > 0 && (
        <KeyValueTable title="Selector" data={spec.selector} />
      )}
    </div>
  );
}
