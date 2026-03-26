import type { ComponentChildren } from"preact";

type AlertVariant ="error" |"warning" |"info" |"success";

interface AlertProps {
 variant?: AlertVariant;
 children: ComponentChildren;
 class?: string;
}

const VARIANT_STYLES: Record<AlertVariant, Record<string, string>> = {
 error: { background:"var(--error-dim)", color:"var(--error)" },
 warning: { background:"var(--warning-dim)", color:"var(--warning)" },
 info: { background:"var(--accent-dim)", color:"var(--accent)" },
 success: { background:"var(--success-dim)", color:"var(--success)" },
};

export function Alert(
 { variant ="error", children, class: className }: AlertProps,
) {
 return (
 <div
 class={`rounded-md px-4 py-3 text-sm ${className ??""}`}
 style={VARIANT_STYLES[variant]}
 >
 {children}
 </div>
 );
}
