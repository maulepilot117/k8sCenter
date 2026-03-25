import { SectionHeader } from "@/components/ui/Field.tsx";

interface Container {
  name: string;
  resources?: {
    requests?: Record<string, string>;
    limits?: Record<string, string>;
  };
}

interface ContainerResourcesTableProps {
  containers: Container[];
}

/**
 * Shows resource requests and limits per container.
 * Renders nothing if no containers have resource specifications.
 */
export function ContainerResourcesTable(
  { containers }: ContainerResourcesTableProps,
) {
  // Only show if at least one container has resource specs
  const hasResources = containers.some((c) => c.resources?.requests || c.resources?.limits);
  if (!hasResources) return null;

  return (
    <div>
      <SectionHeader>Resource Requests &amp; Limits</SectionHeader>
      <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Container
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                CPU Request
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                CPU Limit
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Memory Request
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Memory Limit
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
            {containers.map((c) => (
              <tr key={c.name}>
                <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
                  {c.name}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400">
                  {c.resources?.requests?.cpu ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400">
                  {c.resources?.limits?.cpu ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400">
                  {c.resources?.requests?.memory ?? "-"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-slate-600 dark:text-slate-400">
                  {c.resources?.limits?.memory ?? "-"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
