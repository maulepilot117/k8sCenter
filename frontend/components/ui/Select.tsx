import type { ComponentChildren, JSX } from "preact";

// SelectHTMLAttributes provides value/multiple/etc.; HTMLAttributes alone does not.
interface SelectProps extends JSX.SelectHTMLAttributes<HTMLSelectElement> {
  label?: string;
  error?: string;
  /** Renders a red asterisk after the label. */
  required?: boolean;
  /** Helper text shown below the select (above any error). */
  description?: string;
  /** Simple option list. Mutually exclusive with children. */
  options?: Array<{ value: string; label: string }>;
  /** Use children for richer markup like <optgroup>. */
  children?: ComponentChildren;
}

export function Select(
  {
    label,
    error,
    required,
    description,
    id,
    options,
    children,
    class: className,
    ...props
  }: SelectProps,
) {
  const selectId = id ?? label?.toLowerCase().replace(/\s+/g, "-");
  const errorId = error && selectId ? `${selectId}-error` : undefined;
  const descId = description && selectId ? `${selectId}-desc` : undefined;
  const describedBy = [descId, errorId].filter(Boolean).join(" ") ||
    undefined;

  return (
    <div class="space-y-1">
      {label && (
        <div class="flex items-baseline gap-0.5">
          <label
            for={selectId}
            class="block text-sm font-medium text-text-secondary"
          >
            {label}
          </label>
          {/* Asterisk rendered as a sibling — see Input.tsx for rationale. */}
          {required && (
            <span aria-hidden="true" class="text-sm text-danger">*</span>
          )}
        </div>
      )}
      <select
        id={selectId}
        aria-invalid={error ? "true" : undefined}
        aria-describedby={describedBy}
        aria-required={required ? "true" : undefined}
        class={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
          error
            ? "border-danger focus:ring-danger/50"
            : "border-border-primary focus:border-brand focus:ring-brand/50 bg-surface text-text-primary"
        } ${className ?? ""}`}
        {...props}
      >
        {options
          ? options.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {opt.label}
            </option>
          ))
          : children}
      </select>
      {description && (
        <p id={descId} class="text-xs text-text-muted">{description}</p>
      )}
      {error && <p id={errorId} class="text-sm text-danger">{error}</p>}
    </div>
  );
}
