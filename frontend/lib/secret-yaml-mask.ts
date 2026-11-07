export const SECRET_VALUE_MASK = "••••••••";

const VALUE_MAP = /^(stringData|data):\s*$/;
const KEY_LINE =
  /^(\s+)("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^:\s][^:]*?):(?:\s|$)/;

/**
 * Masks every value under a top-level `stringData:` or `data:` map, keeping
 * the keys. It fails closed: inside the map, a line that is not a key line at
 * the map's indent (a value's continuation, a comment, anything unforeseen)
 * is dropped rather than shown, and only the next top-level key ends the map.
 */
export function maskSecretYaml(yaml: string): string {
  const out: string[] = [];
  let inMap = false;
  let keyIndent = -1;
  for (const line of yaml.split("\n")) {
    const indent = line.length - line.trimStart().length;
    const text = line.trim();
    if (inMap && (indent > 0 || text === "" || text.startsWith("#"))) {
      if (text === "" || text.startsWith("#")) continue;
      if (keyIndent < 0) keyIndent = indent;
      if (indent !== keyIndent) continue;
      const pad = line.slice(0, indent);
      if (text.startsWith("? ")) {
        // An explicit key (the marshaler writes keys over 128 chars this way).
        out.push(line);
      } else if (text.startsWith(":")) {
        out.push(`${pad}: ${SECRET_VALUE_MASK}`);
      } else {
        const key = KEY_LINE.exec(line);
        if (key) out.push(`${key[1]}${key[2]}: ${SECRET_VALUE_MASK}`);
      }
      continue;
    }
    inMap = VALUE_MAP.test(line);
    if (inMap) keyIndent = -1;
    out.push(line);
  }
  return out.join("\n");
}
