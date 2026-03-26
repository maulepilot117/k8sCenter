import type { JSX } from"preact";
import { Spinner } from"@/components/ui/Spinner.tsx";

type ButtonVariant ="primary" |"secondary" |"danger" |"ghost";
type ButtonSize ="sm" |"md" |"lg";

interface ButtonProps extends JSX.HTMLAttributes<HTMLButtonElement> {
 variant?: ButtonVariant;
 size?: ButtonSize;
 loading?: boolean;
 disabled?: boolean;
}

const variantStyles: Record<ButtonVariant, Record<string, string>> = {
 primary: {
 background:"var(--accent)",
 color:"var(--bg-base)",
 },
 secondary: {
 background:"transparent",
 color:"var(--text-secondary)",
 border:"1px solid var(--border-primary)",
 },
 danger: {
 background:"var(--error)",
 color:"white",
 },
 ghost: {
 background:"transparent",
 color:"var(--text-secondary)",
 },
};

const sizeClasses: Record<ButtonSize, string> = {
 sm:"px-2.5 py-1.5 text-xs",
 md:"px-4 py-2 text-sm",
 lg:"px-6 py-3 text-base",
};

export function Button({
 variant ="primary",
 size ="md",
 loading = false,
 disabled,
 class: className,
 children,
 style,
 ...props
}: ButtonProps) {
 const varStyle = variantStyles[variant];
 const mergedStyle = typeof style ==="string"
 ? varStyle
 : { ...varStyle, ...(style as Record<string, string> | undefined) };

 return (
 <button
 {...props}
 disabled={disabled || loading}
 class={`inline-flex items-center justify-center font-medium rounded-md transition-colors focus:outline-none focus:ring-2 focus:ring-brand/50 disabled:cursor-not-allowed disabled:opacity-50 ${
 sizeClasses[size]
 } ${className ??""}`}
 style={mergedStyle}
 >
 {loading && <Spinner size="sm" class="-ml-1 mr-2" />}
 {children}
 </button>
 );
}
