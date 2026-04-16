/** Generic tinted badge — color text on a color-mix background. */
export function ColorBadge(
  { label, color }: { label: string; color: string },
) {
  return (
    <span
      class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
      style={{
        color,
        backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`,
      }}
    >
      {label}
    </span>
  );
}
