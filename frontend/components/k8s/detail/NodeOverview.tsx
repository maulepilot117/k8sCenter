import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Node } from "@/lib/k8s-types.ts";
import { ConditionsTable } from "./ConditionsTable.tsx";

export function NodeOverview({ resource }: { resource: K8sResource }) {
  const n = resource as Node;
  const spec = n.spec;
  const status = n.status;

  const capacity = status.capacity ?? {};
  const allocatable = status.allocatable ?? {};
  const capacityKeys = [
    ...new Set([...Object.keys(capacity), ...Object.keys(allocatable)]),
  ].sort();

  return (
    <div class="space-y-4">
      {/* System Info */}
      {status.nodeInfo && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            System Info
          </h4>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                Kubelet Version
              </div>
              <div class="text-sm text-slate-900 dark:text-slate-100">
                {status.nodeInfo.kubeletVersion}
              </div>
            </div>
            <div>
              <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                OS Image
              </div>
              <div class="text-sm text-slate-900 dark:text-slate-100">
                {status.nodeInfo.osImage}
              </div>
            </div>
            <div>
              <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                Architecture
              </div>
              <div class="text-sm text-slate-900 dark:text-slate-100">
                {status.nodeInfo.architecture}
              </div>
            </div>
            <div>
              <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                Container Runtime
              </div>
              <div class="text-sm text-slate-900 dark:text-slate-100">
                {status.nodeInfo.containerRuntimeVersion}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Addresses */}
      {status.addresses && status.addresses.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Addresses
          </h4>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {status.addresses.map((a) => (
              <div key={a.type}>
                <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                  {a.type}
                </div>
                <div class="text-sm font-mono text-slate-900 dark:text-slate-100">
                  {a.address}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Capacity vs Allocatable */}
      {capacityKeys.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Capacity vs Allocatable
          </h4>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Resource
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Capacity
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Allocatable
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {capacityKeys.map((key) => (
                  <tr key={key}>
                    <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                      {key}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-slate-700 dark:text-slate-300">
                      {capacity[key] ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-slate-700 dark:text-slate-300">
                      {allocatable[key] ?? "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Taints */}
      {spec.taints && spec.taints.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Taints
          </h4>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Key
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Value
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Effect
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {spec.taints.map((t) => (
                  <tr key={`${t.key}-${t.effect}`}>
                    <td class="px-3 py-1.5 font-mono text-xs text-cyan-700 dark:text-cyan-400">
                      {t.key}
                    </td>
                    <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                      {t.value ?? "-"}
                    </td>
                    <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300">
                      {t.effect}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Unschedulable */}
      {spec.unschedulable && (
        <div>
          <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-400 dark:ring-amber-500/20">
            Cordoned (Unschedulable)
          </span>
        </div>
      )}

      {/* Conditions */}
      {status.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
