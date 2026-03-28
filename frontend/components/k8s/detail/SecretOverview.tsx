import type { K8sResource, Secret } from "@/lib/k8s-types.ts";
import { Field, SectionHeader } from "@/components/ui/Field.tsx";

export function SecretOverview({ resource }: { resource: K8sResource }) {
  const s = resource as Secret;
  const entries = Object.entries(s.data ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div class="space-y-4">
      {/* Type */}
      <div>
        <SectionHeader>Summary</SectionHeader>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Type" value={s.type ?? "Opaque"} mono />
          <Field label="Keys" value={String(entries.length)} />
        </div>
      </div>

      {/* Data Keys (masked) */}
      {entries.length > 0 && (
        <div>
          <SectionHeader>Data</SectionHeader>
          <div class="overflow-x-auto rounded-md border border-border-primary">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-border-primary">
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Key
                  </th>
                  <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                    Value
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border-subtle">
                {entries.map(([key]) => (
                  <tr key={key}>
                    <td class="px-3 py-1.5 font-mono text-xs text-accent whitespace-nowrap">
                      {key}
                    </td>
                    <td class="px-3 py-1.5 font-mono text-xs text-text-muted">
                      ****
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
