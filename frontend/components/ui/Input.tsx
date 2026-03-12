import type { JSX } from "preact";

interface InputProps extends JSX.HTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

export function Input(
  { label, error, id, class: className, ...props }: InputProps,
) {
  const inputId = id ?? label?.toLowerCase().replace(/\s+/g, "-");
  return (
    <div class="space-y-1">
      {label && (
        <label
          for={inputId}
          class="block text-sm font-medium text-slate-700 dark:text-slate-300"
        >
          {label}
        </label>
      )}
      <input
        id={inputId}
        class={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
          error
            ? "border-danger focus:ring-danger/50"
            : "border-slate-300 focus:border-brand focus:ring-brand/50 dark:border-slate-600 dark:bg-slate-800 dark:text-white"
        } ${className ?? ""}`}
        {...props}
      />
      {error && <p class="text-sm text-danger">{error}</p>}
    </div>
  );
}
