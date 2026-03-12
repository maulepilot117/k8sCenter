import type { JSX } from "preact";

type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";
type ButtonSize = "sm" | "md" | "lg";

interface ButtonProps extends JSX.HTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}

const variantClasses: Record<ButtonVariant, string> = {
  primary:
    "bg-brand text-white hover:bg-brand-dark focus:ring-brand/50 disabled:bg-blue-300",
  secondary:
    "bg-white text-slate-700 border border-slate-300 hover:bg-slate-50 focus:ring-slate-300/50 dark:bg-slate-800 dark:text-slate-200 dark:border-slate-600 dark:hover:bg-slate-700",
  danger: "bg-danger text-white hover:bg-red-600 focus:ring-danger/50",
  ghost:
    "text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800",
};

const sizeClasses: Record<ButtonSize, string> = {
  sm: "px-2.5 py-1.5 text-xs",
  md: "px-4 py-2 text-sm",
  lg: "px-6 py-3 text-base",
};

export function Button({
  variant = "primary",
  size = "md",
  loading = false,
  disabled,
  class: className,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      {...props}
      disabled={disabled || loading}
      class={`inline-flex items-center justify-center font-medium rounded-md transition-colors focus:outline-none focus:ring-2 disabled:cursor-not-allowed ${
        variantClasses[variant]
      } ${sizeClasses[size]} ${className ?? ""}`}
    >
      {loading && (
        <svg
          class="animate-spin -ml-1 mr-2 h-4 w-4"
          fill="none"
          viewBox="0 0 24 24"
        >
          <circle
            class="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            stroke-width="4"
          />
          <path
            class="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
          />
        </svg>
      )}
      {children}
    </button>
  );
}
