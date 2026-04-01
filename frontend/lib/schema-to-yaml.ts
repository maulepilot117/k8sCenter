/**
 * Convert form state to a properly formatted YAML string.
 * Pure recursive serializer — no external dependencies.
 */

/** Characters that require quoting in YAML string values. */
const NEEDS_QUOTE =
  /[:#{}[\],&*?|>!%@`"'\n\r\t]|^(true|false|null|yes|no|on|off)$/i;

/** HTML-escape for safe rendering in highlighted preview. */
export function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function quoteString(val: string): string {
  if (val === "") return '""';
  if (NEEDS_QUOTE.test(val)) {
    return '"' + val.replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"';
  }
  return val;
}

function isEmptyValue(val: unknown): boolean {
  if (val === undefined || val === null || val === "") return true;
  if (Array.isArray(val) && val.length === 0) return true;
  if (
    typeof val === "object" && !Array.isArray(val) &&
    Object.keys(val as Record<string, unknown>).length === 0
  ) return true;
  return false;
}

function serializeValue(val: unknown, indent: number): string {
  const pad = "  ".repeat(indent);

  if (val === null || val === undefined) return "null";
  if (typeof val === "boolean") return val ? "true" : "false";
  if (typeof val === "number") return String(val);
  if (typeof val === "string") return quoteString(val);

  if (Array.isArray(val)) {
    if (val.length === 0) return "[]";
    const lines: string[] = [];
    for (const item of val) {
      if (typeof item === "object" && item !== null && !Array.isArray(item)) {
        const objLines = serializeObject(
          item as Record<string, unknown>,
          indent + 1,
        );
        if (objLines.length > 0) {
          // First key on same line as dash
          lines.push(pad + "- " + objLines[0].trimStart());
          for (let i = 1; i < objLines.length; i++) {
            lines.push(pad + "  " + objLines[i].trimStart());
          }
        } else {
          lines.push(pad + "- {}");
        }
      } else {
        lines.push(pad + "- " + serializeValue(item, indent + 1));
      }
    }
    return "\n" + lines.join("\n");
  }

  if (typeof val === "object") {
    const obj = val as Record<string, unknown>;
    const keys = Object.keys(obj);
    if (keys.length === 0) return "{}";
    const lines = serializeObject(obj, indent);
    return "\n" + lines.join("\n");
  }

  return String(val);
}

function serializeObject(
  obj: Record<string, unknown>,
  indent: number,
): string[] {
  const pad = "  ".repeat(indent);
  const lines: string[] = [];
  for (const [key, val] of Object.entries(obj)) {
    if (isEmptyValue(val)) continue;
    const serialized = serializeValue(val, indent + 1);
    if (serialized.startsWith("\n")) {
      lines.push(pad + quoteString(key) + ":" + serialized);
    } else {
      lines.push(pad + quoteString(key) + ": " + serialized);
    }
  }
  return lines;
}

/**
 * Safely set a nested property on an object without prototype pollution.
 * Returns a shallow-copied object with the value set at the given dot-notation path.
 */
export function safeDeepSet(
  obj: Record<string, unknown>,
  path: string,
  value: unknown,
): Record<string, unknown> {
  const parts = path.split(".");
  const result = { ...obj };

  if (parts.length === 0) return result;

  // Single-level set
  if (parts.length === 1) {
    const key = parts[0];
    if (key === "__proto__" || key === "constructor" || key === "prototype") {
      return result;
    }
    if (value === undefined) {
      delete result[key];
    } else {
      result[key] = value;
    }
    return result;
  }

  // Multi-level deep set — build path, reject dangerous keys at each level
  let current: Record<string, unknown> = result;
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i];
    if (
      part === "__proto__" || part === "constructor" || part === "prototype"
    ) {
      return result; // abort without modifying
    }
    if (
      !(part in current) || typeof current[part] !== "object" ||
      current[part] === null
    ) {
      // Check if next segment is numeric — create array instead of object
      const nextPart = parts[i + 1];
      if (/^\d+$/.test(nextPart)) {
        current[part] = [];
      } else {
        current[part] = {};
      }
    }
    current = current[part] as Record<string, unknown>;
  }

  const lastKey = parts[parts.length - 1];
  if (
    lastKey === "__proto__" || lastKey === "constructor" ||
    lastKey === "prototype"
  ) {
    return result;
  }
  if (value === undefined) {
    delete current[lastKey];
  } else {
    current[lastKey] = value;
  }
  return result;
}

/**
 * Build a full Kubernetes resource YAML from form state.
 */
export function formStateToYaml(
  apiVersion: string,
  kind: string,
  metadata: {
    name: string;
    namespace?: string;
    labels?: Record<string, string>;
  },
  spec: Record<string, unknown>,
): string {
  const lines: string[] = [];
  lines.push("apiVersion: " + quoteString(apiVersion));
  lines.push("kind: " + quoteString(kind));
  lines.push("metadata:");
  lines.push("  name: " + quoteString(metadata.name || ""));
  if (metadata.namespace) {
    lines.push("  namespace: " + quoteString(metadata.namespace));
  }
  if (metadata.labels && Object.keys(metadata.labels).length > 0) {
    lines.push("  labels:");
    for (const [k, v] of Object.entries(metadata.labels)) {
      if (k) lines.push("    " + quoteString(k) + ": " + quoteString(v));
    }
  }

  if (!isEmptyValue(spec)) {
    lines.push("spec:");
    const specLines = serializeObject(spec, 1);
    lines.push(...specLines);
  }

  return lines.join("\n") + "\n";
}
