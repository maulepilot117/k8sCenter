import type { ComponentChildren } from "preact";

interface PageHeaderProps {
  title: string;
  subtitle?: string;
  actions?: ComponentChildren;
}

export function PageHeader({ title, subtitle, actions }: PageHeaderProps) {
  return (
    <div class="mb-6 flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-slate-900 dark:text-white">
          {title}
        </h1>
        {subtitle && (
          <p class="mt-1 text-sm text-slate-500 dark:text-slate-400">
            {subtitle}
          </p>
        )}
      </div>
      {actions && <div class="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
