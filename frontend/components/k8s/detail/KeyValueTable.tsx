interface KeyValueTableProps {
 title: string;
 data: Record<string, string>;
}

export function KeyValueTable({ title, data }: KeyValueTableProps) {
 const entries = Object.entries(data).sort(([a], [b]) => a.localeCompare(b));
 if (entries.length === 0) return null;

 return (
 <div>
 <h4 class="text-xs font-medium uppercase text-text-muted mb-2">
 {title}
 </h4>
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
 {entries.map(([key, value]) => (
 <tr key={key}>
 <td class="px-3 py-1.5 font-mono text-xs text-cyan-700 text-accent whitespace-nowrap">
 {key}
 </td>
 <td class="px-3 py-1.5 text-text-secondary break-all">
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
