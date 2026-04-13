import type { Condition } from "@/lib/gateway-types.ts";

function formatTime(ts?: string): string {
  if (!ts) return "-";
  const d = new Date(ts);
  return d.toLocaleDateString() + " " + d.toLocaleTimeString();
}

export default function ConditionsTable(
  { conditions }: { conditions?: Condition[] },
) {
  if (!conditions || conditions.length === 0) {
    return null;
  }

  return (
    <div class="mt-4">
      <h3 class="text-sm font-medium text-text-secondary mb-2">Conditions</h3>
      <div class="rounded-lg border border-border-primary overflow-hidden">
        <table class="min-w-full divide-y divide-border-subtle">
          <thead class="bg-surface">
            <tr>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Type
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Status
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Reason
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Message
              </th>
              <th class="px-4 py-2 text-left text-xs font-medium text-text-muted">
                Last Transition
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border-subtle">
            {conditions.map((c, i) => (
              <tr key={i}>
                <td class="px-4 py-2 text-sm text-text-primary font-medium">
                  {c.type}
                </td>
                <td class="px-4 py-2 text-sm">
                  <span
                    class={c.status === "True"
                      ? "text-success"
                      : c.status === "False"
                      ? "text-danger"
                      : "text-warning"}
                  >
                    {c.status}
                  </span>
                </td>
                <td class="px-4 py-2 text-sm text-text-secondary">
                  {c.reason || "-"}
                </td>
                <td class="px-4 py-2 text-sm text-text-muted max-w-xs truncate">
                  {c.message || "-"}
                </td>
                <td class="px-4 py-2 text-sm text-text-muted">
                  {formatTime(c.lastTransitionTime)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
