import type { K8sResource } from "@/lib/k8s-types.ts";
import type { Deployment } from "@/lib/k8s-types.ts";
import { ConditionsTable } from "./ConditionsTable.tsx";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function DeploymentOverview({ resource }: { resource: K8sResource }) {
  const d = resource as Deployment;
  const spec = d.spec;
  const status = d.status;

  // Extract strategy from spec
  const strategy = (spec as Record<string, unknown>).strategy as
    | {
      type?: string;
      rollingUpdate?: { maxUnavailable?: unknown; maxSurge?: unknown };
    }
    | undefined;

  // Extract container images from pod template
  const template = spec.template as
    | { spec?: { containers?: Array<{ name: string; image: string }> } }
    | undefined;
  const containers = template?.spec?.containers ?? [];

  return (
    <div class="space-y-4">
      {/* Replica Counts */}
      <div>
        <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
          Replicas
        </h4>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Desired
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.replicas ?? 1}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Ready
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.readyReplicas ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Available
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.availableReplicas ?? 0}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Updated
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {status.updatedReplicas ?? 0}
            </div>
          </div>
        </div>
      </div>

      {/* Strategy */}
      {strategy && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Strategy
          </h4>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div>
              <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                Type
              </div>
              <div class="text-sm text-slate-900 dark:text-slate-100">
                {strategy.type ?? "RollingUpdate"}
              </div>
            </div>
            {strategy.rollingUpdate && (
              <>
                <div>
                  <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                    Max Unavailable
                  </div>
                  <div class="text-sm text-slate-900 dark:text-slate-100">
                    {String(strategy.rollingUpdate.maxUnavailable ?? "25%")}
                  </div>
                </div>
                <div>
                  <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
                    Max Surge
                  </div>
                  <div class="text-sm text-slate-900 dark:text-slate-100">
                    {String(strategy.rollingUpdate.maxSurge ?? "25%")}
                  </div>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* Container Images */}
      {containers.length > 0 && (
        <div>
          <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
            Containers
          </h4>
          <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-slate-200 dark:border-slate-700">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Name
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                    Image
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
                {containers.map((c) => (
                  <tr key={c.name}>
                    <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                      {c.name}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400 break-all">
                      {c.image}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Selector */}
      {spec.selector?.matchLabels && (
        <KeyValueTable title="Selector" data={spec.selector.matchLabels} />
      )}

      {/* Conditions */}
      {status.conditions && <ConditionsTable conditions={status.conditions} />}
    </div>
  );
}
