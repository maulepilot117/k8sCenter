/**
 * Turning a revealed Secret value into something safe to show.
 *
 * The reveal endpoint (`GET /v1/resources/secrets/{ns}/{name}/reveal/{key}`,
 * audited server-side) returns the key's bytes base64-encoded. Text values
 * are shown decoded; anything that is not valid UTF-8 (a keystore, a binary
 * certificate) is shown as the base64 it arrived as, labelled as binary.
 */

export interface RevealedValue {
  /** What to display: decoded text, or base64 for binary data. */
  text: string;
  /** True when the bytes are not UTF-8 text and `text` is base64. */
  binary: boolean;
}

export function decodeSecretValue(base64: string): RevealedValue {
  let bytes: Uint8Array;
  try {
    bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
  } catch {
    return { text: base64, binary: true };
  }
  try {
    const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    return { text: sanitizeSecretValue(text), binary: false };
  } catch {
    return { text: base64, binary: true };
  }
}

/** BiDi formatting characters: the Trojan Source vector. */
const BIDI = new Set([
  0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069,
]);
const ZERO_WIDTH = new Set([0x200b, 0x200c, 0x200d, 0xfeff]);

function isUnsafe(code: number): boolean {
  if (code < 0x20 && code !== 0x09 && code !== 0x0a && code !== 0x0d) {
    return true;
  }
  if (code === 0x7f || (code >= 0x80 && code <= 0x9f)) return true;
  return BIDI.has(code) || ZERO_WIDTH.has(code);
}

/**
 * Escapes control, BiDi and zero-width characters as `\uXXXX`, so a value
 * cannot display as one string and copy as another (a password followed by
 * U+202E reverses what comes after it on screen). Mirrors
 * `sanitizeSecretValue` in mobile/lib/features/resources/secret_screens.dart.
 */
export function sanitizeSecretValue(input: string): string {
  let out = "";
  for (const ch of input) {
    const code = ch.codePointAt(0) as number;
    out += isUnsafe(code) ? `\\u${code.toString(16).padStart(4, "0")}` : ch;
  }
  return out;
}
