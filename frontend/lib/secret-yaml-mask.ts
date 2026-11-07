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
      if (line.trim() === "") continue;
      if (keyIndent < 0 && indent > 0) keyIndent = indent;
      if (keyIndent > 0 && indent > keyIndent) continue;
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
