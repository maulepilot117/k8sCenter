import type { ComponentChildren } from "preact";

interface EmptyStateProps {
  title: string;
  description?: string;
  icon?: string;
  actions?: ComponentChildren;
}

export function EmptyState(
  { title, description, icon, actions }: EmptyStateProps,
) {
  return (
    <div class="flex flex-col items-center justify-center py-12 text-center">
      {icon && (
        <div class="mb-4 text-4xl text-slate-300 dark:text-slate-600">
          {icon}
        </div>
      )}
      <h3 class="text-lg font-medium text-slate-900 dark:text-white">
        {title}
      </h3>
      {description && (
        <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
          {description}
        </p>
      )}
      {actions && <div class="mt-4">{actions}</div>}
    </div>
  );
}
