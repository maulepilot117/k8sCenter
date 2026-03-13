import { useCallback, useState } from "preact/hooks";

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
  language = "yaml",
  showLineNumbers = true,
}: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback for non-secure contexts
      const textarea = document.createElement("textarea");
      textarea.value = code;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand("copy");
      document.body.removeChild(textarea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [code]);

  const lines = code.split("\n");
  const lineNumWidth = String(lines.length).length;

  return (
    <div class="relative group rounded-lg border border-slate-200 bg-slate-50 dark:border-slate-700 dark:bg-slate-900">
      {/* Copy button */}
      <button
        type="button"
        onClick={handleCopy}
        class="absolute right-2 top-2 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-600 opacity-0 transition-opacity hover:bg-slate-100 group-hover:opacity-100 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-400 dark:hover:bg-slate-700"
        title="Copy to clipboard"
      >
        {copied ? "Copied!" : "Copy"}
      </button>

      <pre class="overflow-x-auto p-4 text-sm leading-relaxed">
        <code>
          {lines.map((line, i) => (
            <div key={i} class="flex">
              {showLineNumbers && (
                <span
                  class="select-none pr-4 text-right text-slate-400 dark:text-slate-600"
                  style={{ minWidth: `${lineNumWidth + 1}ch` }}
                >
                  {i + 1}
                </span>
              )}
              <span class="flex-1">
                {language === "yaml"
                  ? highlightYaml(line)
                  : <span class="text-slate-800 dark:text-slate-200">{line}</span>}
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
      <span class="text-slate-400 dark:text-slate-500 italic">{line}</span>
    );
  }

  // Key: value pairs
  const keyMatch = line.match(/^(\s*)([\w.\-/]+)(:)(.*)/);
  if (keyMatch) {
    const [, indent, key, colon, rest] = keyMatch;
    return (
      <span>
        <span class="text-slate-800 dark:text-slate-200">{indent}</span>
        <span class="text-cyan-700 dark:text-cyan-400">{key}</span>
        <span class="text-slate-500">{colon}</span>
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
        <span class="text-slate-800 dark:text-slate-200">{indent}</span>
        <span class="text-orange-600 dark:text-orange-400">{dash}</span>
        {highlightValue(" " + rest)}
      </span>
    );
  }

  return <span class="text-slate-800 dark:text-slate-200">{line}</span>;
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
        <span class="text-green-700 dark:text-green-400">{trimmed}</span>
      </span>
    );
  }

  // Booleans
  if (/^(true|false)$/i.test(trimmed)) {
    return (
      <span>
        <span>{leadingSpace}</span>
        <span class="text-purple-700 dark:text-purple-400">{trimmed}</span>
      </span>
    );
  }

  // Numbers
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
    return (
      <span>
        <span>{leadingSpace}</span>
        <span class="text-amber-700 dark:text-amber-400">{trimmed}</span>
      </span>
    );
  }

  // Null
  if (/^(null|~)$/.test(trimmed)) {
    return (
      <span>
        <span>{leadingSpace}</span>
        <span class="text-slate-400 dark:text-slate-500 italic">{trimmed}</span>
      </span>
    );
  }

  return <span class="text-slate-800 dark:text-slate-200">{value}</span>;
}
