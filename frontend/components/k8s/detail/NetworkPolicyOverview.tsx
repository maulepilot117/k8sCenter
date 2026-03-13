import type { K8sResource } from "@/lib/k8s-types.ts";
import type { NetworkPolicy } from "@/lib/k8s-types.ts";
import { KeyValueTable } from "./KeyValueTable.tsx";

export function NetworkPolicyOverview({ resource }: { resource: K8sResource }) {
  const np = resource as NetworkPolicy;
  const spec = np.spec;

  const ingressCount = spec.ingress?.length ?? 0;
  const egressCount = spec.egress?.length ?? 0;

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
              Policy Types
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {spec.policyTypes?.join(", ") ?? "Ingress"}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Ingress Rules
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {ingressCount}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-slate-500 dark:text-slate-400">
              Egress Rules
            </div>
            <div class="text-sm text-slate-900 dark:text-slate-100">
              {egressCount}
            </div>
          </div>
        </div>
      </div>

      {/* Pod Selector */}
      {spec.podSelector.matchLabels &&
        Object.keys(spec.podSelector.matchLabels).length > 0 && (
        <KeyValueTable
          title="Pod Selector"
          data={spec.podSelector.matchLabels}
        />
      )}
    </div>
  );
}
