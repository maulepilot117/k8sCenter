/** @jsxImportSource react */
import { SectionHeader } from "@/components/ui/Field.tsx";
import { withUniqueKeys } from "@/lib/unique-keys.ts";

interface Rule {
  apiGroups?: string[];
  resources?: string[];
  verbs?: string[];
}

export function RulesTable({ rules }: { rules?: Rule[] }) {
  if (!rules?.length) {
    return <p className="text-sm text-text-muted">No rules defined</p>;
  }

  const keyed = withUniqueKeys(rules, (r) =>
    [r.apiGroups, r.resources, r.verbs].map((x) => x?.join(",")).join("|"),
  );

  return (
    <div>
      <SectionHeader>Rules</SectionHeader>
      <div className="overflow-x-auto rounded-md border border-border-primary">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border-primary">
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                API Groups
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Resources
              </th>
              <th className="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Verbs
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border-subtle">
            {keyed.map(({ item: rule, key }) => (
              <tr key={key}>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {rule.apiGroups?.join(",") || "*"}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
                  {rule.resources?.join(",") || "*"}
                </td>
                <td className="px-3 py-1.5 font-mono text-xs text-text-secondary">
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
