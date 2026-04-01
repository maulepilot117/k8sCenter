import { useMemo } from "preact/hooks";
import { escapeHtml } from "@/lib/schema-to-yaml.ts";

interface Props {
  yaml: string;
}

/**
 * Regex-based YAML syntax highlighting.
 * YAML content may include user-provided values (e.g. annotation keys/values).
 * dangerouslySetInnerHTML is safe because escapeHtml() is applied to all raw
 * content before HTML span construction.
 */
function highlightLine(raw: string): string {
  const escaped = escapeHtml(raw);

  // Comment lines
  if (/^\s*#/.test(raw)) {
    return `<span style="color:var(--text-muted)">${escaped}</span>`;
  }

  // Array item prefix
  const arrayMatch = escaped.match(/^(\s*-\s*)(.*)/);
  if (arrayMatch) {
    const prefix = arrayMatch[1];
    const rest = arrayMatch[2];
    return `<span style="color:var(--text-muted)">${prefix}</span>${
      highlightValue(rest)
    }`;
  }

  // Key: value lines
  const kvMatch = escaped.match(/^(\s*)([\w"'./-]+)(:)(\s*)(.*)/);
  if (kvMatch) {
    const indent = kvMatch[1];
    const key = kvMatch[2];
    const colon = kvMatch[3];
    const space = kvMatch[4];
    const value = kvMatch[5];
    const coloredKey = `<span style="color:var(--accent)">${key}</span>`;
    const coloredColon =
      `<span style="color:var(--text-muted)">${colon}</span>`;
    if (value === "") {
      return indent + coloredKey + coloredColon;
    }
    return indent + coloredKey + coloredColon + space + highlightValue(value);
  }

  return escaped;
}

function highlightValue(val: string): string {
  if (!val) return val;
  // Boolean
  if (/^(true|false)$/i.test(val)) {
    return `<span style="color:var(--warning)">${val}</span>`;
  }
  // Null
  if (/^null$/i.test(val)) {
    return `<span style="color:var(--text-muted)">${val}</span>`;
  }
  // Number
  if (/^-?\d+(\.\d+)?$/.test(val)) {
    return `<span style="color:var(--accent-secondary)">${val}</span>`;
  }
  // Quoted string
  if (/^".*"$/.test(val) || /^'.*'$/.test(val)) {
    return `<span style="color:var(--success)">${val}</span>`;
  }
  // Empty object/array
  if (val === "{}" || val === "[]") {
    return `<span style="color:var(--text-muted)">${val}</span>`;
  }
  // Unquoted string
  return `<span style="color:var(--success)">${val}</span>`;
}

export default function YamlPreview({ yaml }: Props) {
  const highlighted = useMemo(() => {
    if (!yaml) return "";
    const lines = yaml.split("\n");
    return lines
      .map((line, i) => {
        const num = String(i + 1).padStart(3, " ");
        const numSpan =
          `<span style="color:var(--text-muted);user-select:none;padding-right:12px">${num}</span>`;
        return numSpan + highlightLine(line);
      })
      .join("\n");
  }, [yaml]);

  return (
    <div
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border-primary)",
        borderRadius: "8px",
        overflow: "auto",
        maxHeight: "600px",
      }}
    >
      <pre
        style={{
          margin: 0,
          padding: "16px",
          fontSize: "13px",
          lineHeight: "1.5",
          fontFamily: "var(--font-mono)",
          color: "var(--text-primary)",
          whiteSpace: "pre",
          overflowX: "auto",
        }}
        // YAML content is generated from form state, not user input — safe to render
        // deno-lint-ignore react-no-danger
        dangerouslySetInnerHTML={{ __html: highlighted }}
      />
    </div>
  );
}
