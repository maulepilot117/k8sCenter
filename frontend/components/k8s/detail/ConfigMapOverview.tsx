import type { ConfigMap, K8sResource } from "@/lib/k8s-types.ts";
import { SectionHeader } from "@/components/ui/Field.tsx";

export function ConfigMapOverview({ resource }: { resource: K8sResource }) {
  const cm = resource as ConfigMap;
  const entries = Object.entries(cm.data ?? {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div class="space-y-4">
      <div>
        <SectionHeader>
          {`Data (${entries.length} ${entries.length === 1 ? "key" : "keys"})`}
        </SectionHeader>
        {entries.length === 0
          ? (
            <p class="text-sm text-text-muted">
              No data keys.
            </p>
          )
          : (
            <div class="overflow-x-auto rounded-md border border-border-primary">
              <table class="w-full text-sm">
                <thead>
                  <tr class="border-b border-border-primary">
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                      Key
                    </th>
                    <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                      Value Preview
                    </th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-border-subtle">
                  {entries.map(([key, value]) => (
                    <tr key={key}>
                      <td class="px-3 py-1.5 font-mono text-xs text-accent whitespace-nowrap align-top">
                        {key}
                      </td>
                      <td class="px-3 py-1.5 text-text-secondary break-all">
                        <pre class="whitespace-pre-wrap text-xs font-mono text-text-secondary">
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
