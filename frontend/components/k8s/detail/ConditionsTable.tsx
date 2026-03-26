import { statusColor } from "@/lib/status-colors.ts";
import { age } from "@/lib/format.ts";

interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
}

interface ConditionsTableProps {
  conditions: Condition[];
}

export function ConditionsTable({ conditions }: ConditionsTableProps) {
  if (!conditions || conditions.length === 0) return null;

  return (
    <div>
      <h4 class="text-xs font-medium uppercase text-text-muted mb-2">
        Conditions
      </h4>
      <div class="overflow-x-auto rounded-md border border-border-primary">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-border-primary">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Type
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Status
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Reason
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Message
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-text-muted">
                Last Transition
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {conditions.map((c) => (
              <tr key={c.type}>
                <td class="px-3 py-1.5 font-medium text-text-secondary">
                  {c.type}
                </td>
                <td class="px-3 py-1.5">
                  <span
                    class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${
                      statusColor(c.status)
                    }`}
                  >
                    {c.status}
                  </span>
                </td>
                <td class="px-3 py-1.5 text-text-secondary">
                  {c.reason ?? "-"}
                </td>
                <td class="px-3 py-1.5 text-text-secondary max-w-sm truncate">
                  {c.message ?? "-"}
                </td>
                <td class="px-3 py-1.5 text-text-muted">
                  {c.lastTransitionTime ? age(c.lastTransitionTime) : "-"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
