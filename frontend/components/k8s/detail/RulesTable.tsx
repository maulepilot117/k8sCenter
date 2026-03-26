import { SectionHeader } from "@/components/ui/Field.tsx";

interface Rule {
  apiGroups?: string[];
  resources?: string[];
  verbs?: string[];
}

export function RulesTable({ rules }: { rules?: Rule[] }) {
  if (!rules?.length) {
    return <p class="text-sm text-text-muted">No rules defined</p>;
  }

  return (
    <div>
      <SectionHeader>Rules</SectionHeader>
      <div class="overflow-x-auto rounded-md border border-border-primary">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                API Groups
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Resources
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Verbs
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {rules.map((rule, i) => (
              <tr key={i}>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {rule.apiGroups?.join(",") || "*"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {rule.resources?.join(",") || "*"}
                </td>
                <td class="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {rule.verbs?.join(",") || "*"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
