import type { ComponentChildren } from "preact";

interface CardProps {
  title?: string;
  children: ComponentChildren;
  class?: string;
}

export function Card({ title, children, class: className }: CardProps) {
  return (
    <div
      class={`rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-700 dark:bg-slate-800 ${
        className ?? ""
      }`}
    >
      {title && (
        <h3 class="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400">
          {title}
        </h3>
      )}
      {children}
    </div>
  );
}
