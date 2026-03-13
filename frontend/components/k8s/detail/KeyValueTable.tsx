interface KeyValueTableProps {
  title: string;
  data: Record<string, string>;
}

export function KeyValueTable({ title, data }: KeyValueTableProps) {
  const entries = Object.entries(data).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return null;

  return (
    <div>
      <h4 class="text-xs font-medium uppercase text-slate-500 dark:text-slate-400 mb-2">
        {title}
      </h4>
      <div class="overflow-x-auto rounded-md border border-slate-200 dark:border-slate-700">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-slate-200 dark:border-slate-700">
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Key
              </th>
              <th class="px-3 py-1.5 text-left text-xs font-medium text-slate-500">
                Value
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
            {entries.map(([key, value]) => (
              <tr key={key}>
                <td class="px-3 py-1.5 font-mono text-xs text-cyan-700 dark:text-cyan-400 whitespace-nowrap">
                  {key}
                </td>
                <td class="px-3 py-1.5 text-slate-700 dark:text-slate-300 break-all">
                  {value}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
