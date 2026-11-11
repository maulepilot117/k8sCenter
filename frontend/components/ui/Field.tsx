import type { ReactNode } from "react";

/** Reusable label/value field pair for detail views. */
export function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-xs font-medium text-text-muted">{label}</dt>
      <dd
        className={`mt-0.5 text-sm text-text-primary break-all ${
          mono ? "font-mono text-xs" : ""
        }`}
      >
        {value}
      </dd>
    </div>
  );
}

/** Section header used across overview components. */
export function SectionHeader({ children }: { children: ReactNode }) {
  return (
    <h4 className="text-xs font-medium uppercase text-text-muted mb-2">
      {children}
    </h4>
  );
}
