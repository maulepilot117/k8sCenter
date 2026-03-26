/** Breadcrumb navigation component. */
interface BreadcrumbProps {
  items: { label: string; href?: string }[];
}

export function Breadcrumb({ items }: BreadcrumbProps) {
  return (
    <nav
      aria-label="Breadcrumb"
      class="mb-4 flex items-center gap-1.5 text-sm"
      style={{ color: "var(--text-muted)" }}
    >
      {items.map((item, i) => (
        <span key={i} class="flex items-center gap-1.5">
          {i > 0 && <span style={{ color: "var(--border-primary)" }}>/</span>}
          {item.href
            ? <a href={item.href} class="hover:underline">{item.label}</a>
            : (
              <span
                class="font-medium"
                style={{ color: "var(--text-primary)" }}
              >
                {item.label}
              </span>
            )}
        </span>
      ))}
    </nav>
  );
}
