import type { ComponentChildren } from "preact";

type AlertVariant = "error" | "warning" | "info" | "success";

interface AlertProps {
  variant?: AlertVariant;
  children: ComponentChildren;
  class?: string;
}

const VARIANT_CLASSES: Record<AlertVariant, string> = {
  error: "bg-red-50 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  warning:
    "bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  info: "bg-blue-50 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  success:
    "bg-green-50 text-green-800 dark:bg-green-900/30 dark:text-green-400",
};

export function Alert(
  { variant = "error", children, class: className }: AlertProps,
) {
  return (
    <div
      class={`rounded-md px-4 py-3 text-sm ${VARIANT_CLASSES[variant]} ${
        className ?? ""
      }`}
    >
      {children}
    </div>
  );
}
