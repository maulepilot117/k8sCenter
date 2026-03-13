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
      <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
        Conditions
      </h4>
      <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Type
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Status
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Reason
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Message
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Last Transition
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
            {conditions.map((c) => (
              <tr key={c.type}>
                <td class="px-3 py-1.5 font-medium text-slate-700 dark:text-slate-300">
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
                <td class="px-3 py-1.5 text-slate-600 dark:text-slate-400">
                  {c.reason ?? "-"}
                </td>
                <td class="px-3 py-1.5 text-slate-600 dark:text-slate-400 max-w-sm truncate">
                  {c.message ?? "-"}
                </td>
                <td class="px-3 py-1.5 text-slate-500">
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
