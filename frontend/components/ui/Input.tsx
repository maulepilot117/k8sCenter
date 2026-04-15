import type { JSX } from "preact";

// Use InputHTMLAttributes for input-specific props (value, type, etc.).
// HTMLAttributes alone lacks them in this Preact JSX type setup.
interface InputProps extends JSX.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  /** Renders a red asterisk after the label. */
  required?: boolean;
  /** Helper text shown below the input (above any error). */
  description?: string;
}

export function Input(
  {
    label,
    error,
    required,
    description,
    id,
    class: className,
    ...props
  }: InputProps,
) {
  const inputId = id ?? label?.toLowerCase().replace(/\s+/g, "-");
  const errorId = error && inputId ? `${inputId}-error` : undefined;
  const descId = description && inputId ? `${inputId}-desc` : undefined;
  const describedBy = [descId, errorId].filter(Boolean).join(" ") ||
    undefined;

  return (
    <div class="space-y-1">
      {label && (
        <div class="flex items-baseline gap-0.5">
          <label
            for={inputId}
            class="block text-sm font-medium text-text-secondary"
          >
            {label}
          </label>
          {
            /* Asterisk rendered as a sibling — kept OUT of the <label> so the
              accessible/visible label text is exactly the `label` prop (some
              test frameworks match label text including descendants).
              aria-required on the input announces required-ness for AT. */
          }
          {required && (
            <span aria-hidden="true" class="text-sm text-danger">*</span>
          )}
        </div>
      )}
      <input
        id={inputId}
        aria-invalid={error ? "true" : undefined}
        aria-describedby={describedBy}
        aria-required={required ? "true" : undefined}
        class={`block w-full rounded-md border px-3 py-2 text-sm shadow-sm transition-colors focus:outline-none focus:ring-2 ${
          error
            ? "border-danger focus:ring-danger/50"
            : "border-border-primary focus:border-brand focus:ring-brand/50 border-border-primary bg-surface text-text-primary"
        } ${className ?? ""}`}
        {...props}
      />
      {description && (
        <p id={descId} class="text-xs text-text-muted">{description}</p>
      )}
      {error && <p id={errorId} class="text-sm text-danger">{error}</p>}
    </div>
  );
}
