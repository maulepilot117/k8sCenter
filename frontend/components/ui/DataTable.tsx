import type { ComponentChildren } from "preact";

export interface Column<T> {
  key: string;
  label: string;
  sortable?: boolean;
  render?: (item: T) => ComponentChildren;
  class?: string;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  data: T[];
  sortKey?: string;
  sortDir?: "asc" | "desc";
  onSort?: (key: string) => void;
  rowKey: (item: T) => string;
  onRowClick?: (item: T) => void;
  emptyMessage?: string;
  renderRowActions?: (item: T) => ComponentChildren;
}

export function DataTable<T>({
  columns,
  data,
  sortKey,
  sortDir,
  onSort,
  rowKey,
  onRowClick,
  emptyMessage = "No resources found",
  renderRowActions,
}: DataTableProps<T>) {
  const totalCols = columns.length + (renderRowActions ? 1 : 0);
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 dark:border-slate-700">
            {columns.map((col) => (
              <th
                key={col.key}
                class={`px-4 py-3 text-left text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400 ${
                  col.sortable
                    ? "cursor-pointer select-none hover:text-slate-700 dark:hover:text-slate-200"
                    : ""
                } ${col.class ?? ""}`}
                onClick={col.sortable && onSort
                  ? () => onSort(col.key)
                  : undefined}
              >
                <span class="inline-flex items-center gap-1">
                  {col.label}
                  {col.sortable && sortKey === col.key && (
                    <SortIcon dir={sortDir ?? "asc"} />
                  )}
                </span>
              </th>
            ))}
            {renderRowActions && (
              <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400 w-12" />
            )}
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 dark:divide-slate-700/50">
          {data.length === 0
            ? (
              <tr>
                <td
                  colSpan={totalCols}
                  class="px-4 py-12 text-center text-sm text-slate-400 dark:text-slate-500"
                >
                  {emptyMessage}
                </td>
              </tr>
            )
            : (
              data.map((item) => (
                <tr
                  key={rowKey(item)}
                  class={`transition-colors hover:bg-slate-50 dark:hover:bg-slate-800/50 ${
                    onRowClick ? "cursor-pointer" : ""
                  }`}
                  onClick={onRowClick ? () => onRowClick(item) : undefined}
                >
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      class={`px-4 py-3 text-slate-700 dark:text-slate-300 ${
                        col.class ?? ""
                      }`}
                    >
                      {col.render ? col.render(item) : String(
                        (item as Record<string, unknown>)[col.key] ?? "",
                      )}
                    </td>
                  ))}
                  {renderRowActions && (
                    <td class="px-4 py-3 text-right">
                      {renderRowActions(item)}
                    </td>
                  )}
                </tr>
              ))
            )}
        </tbody>
      </table>
    </div>
  );
}

function SortIcon({ dir }: { dir: "asc" | "desc" }) {
  return (
    <svg class="h-3.5 w-3.5" viewBox="0 0 16 16" fill="currentColor">
      {dir === "asc" ? <path d="M8 4l4 5H4z" /> : <path d="M8 12l4-5H4z" />}
    </svg>
  );
}
