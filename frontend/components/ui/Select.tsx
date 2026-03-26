import type { JSX } from"preact";

interface SelectProps extends JSX.HTMLAttributes<HTMLSelectElement> {
 label?: string;
 error?: string;
 options: Array<{ value: string; label: string }>;
}

export function Select(
 { label, error, id, options, class: className, ...props }: SelectProps,
) {
 const selectId = id ?? label?.toLowerCase().replace(/\s+/g,"-");
 return (
 <div class="space-y-1">
 {label && (
 <label
 for={selectId}
 class="block text-sm font-medium text-text-secondary"
 >
 {label}
 </label>
 )}
 <select
 id={selectId}
 class={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
 error
 ?"border-danger focus:ring-danger/50"
 :"border-border-primary focus:border-brand focus:ring-brand/50 border-border-primary bg-surface text-text-primary"
 } ${className ??""}`}
 {...props}
 >
 {options.map((opt) => (
 <option key={opt.value} value={opt.value}>
 {opt.label}
 </option>
 ))}
 </select>
 {error && <p class="text-sm text-danger">{error}</p>}
 </div>
 );
}
