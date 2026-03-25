/** Breadcrumb navigation component. */
interface BreadcrumbProps {
  items: { label: string; href?: string }[];
}

export function Breadcrumb({ items }: BreadcrumbProps) {
  return (
    <nav
      aria-label="Breadcrumb"
      class="mb-4 flex items-center gap-1.5 text-sm text-slate-500 dark:text-slate-400"
    >
      {items.map((item, i) => (
        <span key={i} class="flex items-center gap-1.5">
          {i > 0 && <span class="text-slate-300 dark:text-slate-600">/</span>}
          {item.href
            ? (
              <a
                href={item.href}
                class="hover:text-slate-700 dark:hover:text-slate-200"
              >
                {item.label}
              </a>
            )
            : (
              <span class="font-medium text-slate-700 dark:text-slate-200">
                {item.label}
              </span>
            )}
        </span>
      ))}
    </nav>
  );
}
