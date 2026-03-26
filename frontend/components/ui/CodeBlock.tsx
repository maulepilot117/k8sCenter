import { useCallback, useState } from"preact/hooks";

interface CodeBlockProps {
 code: string;
 language?: string;
 showLineNumbers?: boolean;
}

/**
 * Code display with syntax highlighting, line numbers, and copy-to-clipboard.
 * Uses simple token-based highlighting for YAML — no heavy dependencies.
 */
export function CodeBlock({
 code,
 language ="yaml",
 showLineNumbers = true,
}: CodeBlockProps) {
 const [copied, setCopied] = useState(false);

 const handleCopy = useCallback(async () => {
 try {
 await navigator.clipboard.writeText(code);
 setCopied(true);
 setTimeout(() => setCopied(false), 2000);
 } catch {
 // Clipboard API unavailable (non-secure context)
 }
 }, [code]);

 const lines = code.split("\n");
 const lineNumWidth = String(lines.length).length;

 return (
 <div class="relative group rounded-lg border border-border-primary bg-surface bg-base">
 {/* Copy button */}
 <button
 type="button"
 onClick={handleCopy}
 class="absolute right-2 top-2 rounded-md border border-border-primary bg-surface px-2 py-1 text-xs text-text-secondary opacity-0 transition-opacity hover:bg-elevated group-hover:opacity-100 text-text-muted"
 title="Copy to clipboard"
 >
 {copied ?"Copied!" :"Copy"}
 </button>

 <pre class="overflow-x-auto p-4 text-sm leading-relaxed">
 <code>
 {lines.map((line, i) => (
 <div key={i} class="flex">
 {showLineNumbers && (
 <span
 class="select-none pr-4 text-right text-text-muted"
 style={{ minWidth: `${lineNumWidth + 1}ch` }}
 >
 {i + 1}
 </span>
 )}
 <span class="flex-1">
 {language ==="yaml"
 ? highlightYaml(line)
 : <span class="text-text-primary">{line}</span>}
 </span>
 </div>
 ))}
 </code>
 </pre>
 </div>
 );
}

/** Simple YAML syntax highlighter — tokenizes one line at a time. */
function highlightYaml(line: string): preact.JSX.Element {
 // Comment lines
 if (/^\s*#/.test(line)) {
 return (
 <span class="text-text-muted italic">{line}</span>
 );
 }

 // Key: value pairs
 const keyMatch = line.match(/^(\s*)([\w.\-/]+)(:)(.*)/);
 if (keyMatch) {
 const [, indent, key, colon, rest] = keyMatch;
 return (
 <span>
 <span class="text-text-primary">{indent}</span>
 <span class="text-cyan-700 text-accent">{key}</span>
 <span class="text-text-muted">{colon}</span>
 {highlightValue(rest)}
 </span>
 );
 }

 // List items (- value)
 const listMatch = line.match(/^(\s*)(- )(.*)/);
 if (listMatch) {
 const [, indent, dash, rest] = listMatch;
 return (
 <span>
 <span class="text-text-primary">{indent}</span>
 <span class="text-orange-600 text-warning">{dash}</span>
 {highlightValue("" + rest)}
 </span>
 );
 }

 return <span class="text-text-primary">{line}</span>;
}

/** Highlights a YAML value portion. */
function highlightValue(value: string): preact.JSX.Element {
 const trimmed = value.trimStart();
 const leadingSpace = value.slice(0, value.length - trimmed.length);

 // Strings in quotes
 if (/^["'].*["']$/.test(trimmed)) {
 return (
 <span>
 <span>{leadingSpace}</span>
 <span class="text-success">{trimmed}</span>
 </span>
 );
 }

 // Booleans
 if (/^(true|false)$/i.test(trimmed)) {
 return (
 <span>
 <span>{leadingSpace}</span>
 <span class="text-purple-700 text-accent-secondary">{trimmed}</span>
 </span>
 );
 }

 // Numbers
 if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
 return (
 <span>
 <span>{leadingSpace}</span>
 <span class="text-warning">{trimmed}</span>
 </span>
 );
 }

 // Null
 if (/^(null|~)$/.test(trimmed)) {
 return (
 <span>
 <span>{leadingSpace}</span>
 <span class="text-text-muted italic">{trimmed}</span>
 </span>
 );
 }

 return <span class="text-text-primary">{value}</span>;
}
