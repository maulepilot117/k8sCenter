/**
 * Masks the values of a Secret manifest for display, keeping its keys.
 *
 * The Secret wizard's review step shows the generated YAML, whose
 * `stringData` holds every value in plaintext. Shown masked, the review still
 * says which keys will be written; the YAML that is applied is unchanged.
 *
 * Line-based, for the shape the backend's YAML marshaler writes: a top-level
 * `stringData:` or `data:` line, then one `key: value` line per entry at one
 * indent. A value that runs onto further lines (a `|` block, or a long string
 * the marshaler wrapped) has those lines indented deeper than its key; they
 * are dropped, so no part of a value survives.
 */
export const SECRET_VALUE_MASK = "••••••••";

const VALUE_MAP = /^(stringData|data):\s*$/;
const KEY_LINE =
  /^(\s+)("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^:\s][^:]*?):(?:\s|$)/;

export function maskSecretYaml(yaml: string): string {
  const out: string[] = [];
  let inMap = false;
  let keyIndent = -1;
  for (const line of yaml.split("\n")) {
    if (inMap) {
      const indent = line.length - line.trimStart().length;
      if (line.trim() === "") continue; // inside a block value, or a gap
      if (keyIndent < 0 && indent > 0) keyIndent = indent;
      if (keyIndent > 0 && indent > keyIndent) continue; // a value's next line
      const key = indent === keyIndent ? KEY_LINE.exec(line) : null;
      if (key) {
        out.push(`${key[1]}${key[2]}: ${SECRET_VALUE_MASK}`);
        continue;
      }
      inMap = false;
    }
    if (VALUE_MAP.test(line)) {
      inMap = true;
      keyIndent = -1;
    }
    out.push(line);
  }
  return out.join("\n");
}
