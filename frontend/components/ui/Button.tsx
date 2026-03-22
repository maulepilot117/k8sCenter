import type { JSX } from "preact";
import { Spinner } from "@/components/ui/Spinner.tsx";

type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";
type ButtonSize = "sm" | "md" | "lg";

interface ButtonProps extends JSX.HTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  disabled?: boolean;
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
      {loading && <Spinner size="sm" class="-ml-1 mr-2" />}
      {children}
    </button>
  );
}
