/** @jsxImportSource react */
import { SectionHeader } from "@/components/ui/Field.tsx";
import type { K8sResource, Namespace } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";

export function NamespaceOverview({ resource }: { resource: K8sResource }) {
  const ns = resource as Namespace;
  const phase = ns.status?.phase ?? "Active";

  return (
    <div className="space-y-4">
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div>
          <div className="text-xs font-medium text-text-muted">Phase</div>
          <div className="mt-0.5">
            <span
              className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${statusColor(
                phase,
              )}`}
            >
              {phase}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
