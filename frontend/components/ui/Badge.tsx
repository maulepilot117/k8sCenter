type BadgeVariant = "success" | "warning" | "danger" | "info" | "neutral";

interface BadgeProps {
  variant?: BadgeVariant;
  children: string;
  class?: string;
}

const variantClasses: Record<BadgeVariant, string> = {
  success:
    "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
  warning:
    "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  danger: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  info: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  neutral: "bg-slate-100 text-slate-700 dark:bg-slate-700 dark:text-slate-300",
};

export function Badge(
  { variant = "neutral", children, class: className }: BadgeProps,
) {
  return (
    <span
      class={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
        variantClasses[variant]
      } ${className ?? ""}`}
    >
      {children}
    </span>
  );
}
