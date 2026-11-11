import { Field, SectionHeader } from "@/components/ui/Field.tsx";
import type { K8sResource, PersistentVolumeClaim } from "@/lib/k8s-types.ts";
import { statusColor } from "@/lib/status-colors.ts";

export function PVCOverview({ resource }: { resource: K8sResource }) {
  const pvc = resource as PersistentVolumeClaim;
  const spec = pvc.spec;
  const status = pvc.status;
  const phase = status?.phase ?? "Pending";

  return (
    <div className="space-y-4">
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
          <Field label="Storage Class" value={spec?.storageClassName ?? "-"} />
          <Field
            label="Access Modes"
            value={spec?.accessModes?.join(",") ?? "-"}
          />
          <Field
            label="Requested Capacity"
            value={spec?.resources?.requests?.storage ?? "-"}
            mono
          />
          <Field
            label="Actual Capacity"
            value={status?.capacity?.storage ?? "-"}
            mono
          />
          <Field label="Volume Name" value={spec?.volumeName ?? "-"} mono />
        </div>
      </div>
    </div>
  );
}
